// Comando principal de DocuHub.
//
// Arranca en este orden: configuración → base de datos → migraciones →
// dependencias → usuario administrador inicial → servidor HTTP. Cualquier
// fallo en los primeros pasos termina el proceso con un mensaje claro, porque
// arrancar a medias en una plataforma de archivos es peor que no arrancar.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/docuhub/docuhub/internal/config"
	"github.com/docuhub/docuhub/internal/crypto"
	"github.com/docuhub/docuhub/internal/drive"
	"github.com/docuhub/docuhub/internal/httpapi"
	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/quota"
	"github.com/docuhub/docuhub/internal/repo"
	"github.com/docuhub/docuhub/internal/store"
	"github.com/docuhub/docuhub/web"
)

func main() {
	// Rompe-cristales: restablecer la contraseña de alguien sin pasar por la
	// interfaz. Hace falta cuando el único administrador pierde su clave y no
	// queda nadie dentro que pueda restablecérsela. Sin esto, la única salida
	// es escribir el hash a mano en la base, que es justo el tipo de maniobra
	// que acaba mal.
	//
	//   docker compose exec app docuhub -reset-password correo@empresa.com
	if len(os.Args) > 2 && os.Args[1] == "-reset-password" {
		if err := resetPassword(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "no se pudo restablecer:", err)
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		slog.Error("el servidor no pudo arrancar", "error", err)
		os.Exit(1)
	}
}

// resetPassword genera una contraseña nueva, la deja marcada como temporal y
// cierra las sesiones abiertas de esa persona. No muestra la anterior porque
// no se puede: solo se guarda su hash.
func resetPassword(email string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	r := repo.New(db)
	user, err := r.UserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("no hay ningún usuario con el correo %q", email)
	}

	password, err := crypto.NewToken(9)
	if err != nil {
		return err
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return err
	}
	if err := r.SetPassword(ctx, user.ID, hash, true); err != nil {
		return err
	}
	if err := r.RevokeUserSessions(ctx, user.ID); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("  Contraseña restablecida")
	fmt.Println("  --------------------------------------------")
	fmt.Println("  Usuario:    ", user.Email)
	fmt.Println("  Rol:        ", user.Role)
	fmt.Println("  Contraseña: ", password)
	fmt.Println("  --------------------------------------------")
	fmt.Println("  Es temporal: la plataforma pedirá cambiarla al entrar.")
	fmt.Println("  Las sesiones abiertas de esta persona se han cerrado.")
	fmt.Println()
	return nil
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	setupLogger(cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		return err
	}

	enc, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return err
	}

	r := repo.New(db)
	driveClient := drive.New(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.DriveScope,
		cfg.RedirectURI(), enc, r)
	quotas := quota.New(r, cfg.DefaultQuotaBytes, cfg.DefaultBandwidthBytes)

	if err := bootstrapAdmin(ctx, r, cfg); err != nil {
		return err
	}
	if !cfg.DriveConfigured() {
		slog.Warn("Google Drive sin credenciales: define GOOGLE_CLIENT_ID y GOOGLE_CLIENT_SECRET para poder conectar la cuenta")
	}

	srv := httpapi.New(cfg, r, driveClient, enc, quotas, web.Handler())

	maintenanceCtx, cancelMaintenance := context.WithCancel(context.Background())
	defer cancelMaintenance()
	go srv.RunMaintenance(maintenanceCtx)

	httpServer := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: srv,
		// Sin WriteTimeout ni ReadTimeout globales: una subida de 30 GB dura
		// lo que dure. El límite real lo pone el context de cada petición.
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("DocuHub en marcha",
			"puerto", cfg.Port, "url", cfg.BaseURL, "entorno", cfg.Env)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("apagando; se esperan hasta 30 s a que terminen las transferencias en curso")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

// setupLogger usa texto legible en desarrollo y JSON en producción, donde el
// log lo leen herramientas (docker logs, el watchdog) y no una persona.
func setupLogger(env string) {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if env == "dev" {
		opts.Level = slog.LevelDebug
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, opts)))
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, opts)))
}

// bootstrapAdmin crea la primera cuenta si la base está vacía. Sin esto no
// habría manera de entrar la primera vez.
func bootstrapAdmin(ctx context.Context, r *repo.Repo, cfg *config.Config) error {
	count, err := r.CountUsers(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	password := cfg.BootstrapAdminPassword
	generated := false
	if password == "" {
		password, err = crypto.NewToken(12)
		if err != nil {
			return err
		}
		generated = true
	}

	hash, err := crypto.HashPassword(password)
	if err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(cfg.BootstrapAdminEmail))
	user, err := r.CreateUser(ctx, email, "Administrador", hash, models.RoleAdmin, 0, 0, generated)
	if err != nil {
		return err
	}

	if generated {
		slog.Warn("=================================================================")
		slog.Warn("USUARIO ADMINISTRADOR CREADO — anota estos datos, no se repiten")
		slog.Warn("correo: " + user.Email)
		slog.Warn("contraseña temporal: " + password)
		slog.Warn("Cámbiala al entrar. Para fijarla tú, usa BOOTSTRAP_ADMIN_PASSWORD.")
		slog.Warn("=================================================================")
	} else {
		slog.Info("usuario administrador creado desde la configuración", "correo", user.Email)
	}
	return nil
}
