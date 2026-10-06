package davfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/quota"
	"github.com/google/uuid"
)

// ------------------------------------------------------------- lectura ----

// lector sirve el contenido desde Google Drive sin guardarlo en la laptop.
// El Explorador de Windows lee a saltos (pide el principio, luego el final,
// luego vuelve), así que cada salto hacia atrás obliga a reabrir el flujo con
// una cabecera Range. Es el precio de no almacenar nada en local.
type lector struct {
	fs      *FS
	ctx     context.Context
	archivo *models.File

	cuerpo io.ReadCloser
	pos    int64 // dónde está el lector
	abierto int64 // posición en la que se abrió el flujo actual
	leidos int64
}

func (f *FS) nuevoLector(ctx context.Context, archivo *models.File) (*lector, error) {
	if archivo.Status != "ready" || archivo.DriveFileID == "" {
		return nil, os.ErrNotExist
	}
	// La cuota de descarga se comprueba una vez al abrir, no en cada bloque.
	if err := f.deps.Quota.CheckBandwidth(ctx, f.user, archivo.SizeBytes); err != nil {
		var limite *quota.LimitError
		if errors.As(err, &limite) {
			slog.Warn("webdav: cuota de descarga superada", "usuario", f.user.Email)
			return nil, os.ErrPermission
		}
		return nil, err
	}
	return &lector{fs: f, ctx: ctx, archivo: archivo}, nil
}

func (l *lector) abrir() error {
	if l.cuerpo != nil && l.abierto == l.pos {
		return nil
	}
	l.cerrarCuerpo()

	acc, err := l.fs.deps.Repo.PrimaryAccount(l.ctx)
	if err != nil {
		return err
	}
	rango := ""
	if l.pos > 0 {
		rango = fmt.Sprintf("bytes=%d-", l.pos)
	}
	resp, err := l.fs.deps.Drive.Download(l.ctx, acc, l.archivo.DriveFileID, rango)
	if err != nil {
		return err
	}
	l.cuerpo = resp.Body
	l.abierto = l.pos
	return nil
}

func (l *lector) cerrarCuerpo() {
	if l.cuerpo != nil {
		_ = l.cuerpo.Close()
		l.cuerpo = nil
	}
}

func (l *lector) Read(p []byte) (int, error) {
	if err := l.abrir(); err != nil {
		return 0, err
	}
	n, err := l.cuerpo.Read(p)
	l.pos += int64(n)
	l.leidos += int64(n)
	return n, err
}

func (l *lector) Seek(offset int64, whence int) (int64, error) {
	var destino int64
	switch whence {
	case io.SeekStart:
		destino = offset
	case io.SeekCurrent:
		destino = l.pos + offset
	case io.SeekEnd:
		destino = l.archivo.SizeBytes + offset
	default:
		return 0, os.ErrInvalid
	}
	if destino < 0 {
		return 0, os.ErrInvalid
	}
	if destino != l.pos {
		l.cerrarCuerpo() // el flujo actual ya no sirve
		l.pos = destino
	}
	return l.pos, nil
}

func (l *lector) Close() error {
	l.cerrarCuerpo()

	// Se contabiliza lo leído de verdad, no el tamaño del archivo: el
	// Explorador abre y cierra archivos solo para ver sus propiedades.
	if l.leidos > 0 {
		go func(bytes int64, userID, fileID uuid.UUID) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			u, f := userID, fileID
			if err := l.fs.deps.Repo.InsertBandwidth(ctx, &u, &f, nil, "download", bytes, "webdav"); err != nil {
				slog.Error("webdav: no se pudo registrar el consumo", "error", err)
			}
			if bytes >= l.archivo.SizeBytes && l.archivo.SizeBytes > 0 {
				_ = l.fs.deps.Repo.RegisterDownload(ctx, f)
			}
		}(l.leidos, l.fs.user.ID, l.archivo.ID)

		l.fs.auditar("file.download", "file", l.archivo.ID.String(), l.archivo.Name, true,
			map[string]any{"via": "webdav", "bytes": l.leidos})
	}
	return nil
}

func (l *lector) Stat() (os.FileInfo, error)            { return infoArchivo(l.archivo), nil }
func (l *lector) Write([]byte) (int, error)             { return 0, os.ErrPermission }
func (l *lector) Readdir(int) ([]os.FileInfo, error)    { return nil, os.ErrInvalid }

// ------------------------------------------------------------ escritura ---

// escritor acumula en un archivo temporal y sube a Drive al cerrarse.
//
// No se transmite directamente a Drive porque WebDAV puede escribir en
// cualquier orden y reabrir el archivo a mitad, mientras que una subida
// resumible exige los bytes en orden. El temporal se borra siempre, también
// si la subida falla.
type escritor struct {
	fs     *FS
	ctx    context.Context
	padre  *models.Folder
	nombre string

	tmp      *os.File
	escritos int64
	cerrado  bool
}

func (f *FS) nuevoEscritor(ctx context.Context, r *resuelto) (*escritor, error) {
	tmp, err := os.CreateTemp("", "docuhub-dav-*")
	if err != nil {
		return nil, err
	}
	return &escritor{fs: f, ctx: ctx, padre: r.padre, nombre: r.nombre, tmp: tmp}, nil
}

func (e *escritor) Write(p []byte) (int, error) {
	n, err := e.tmp.Write(p)
	e.escritos += int64(n)
	return n, err
}

func (e *escritor) Seek(offset int64, whence int) (int64, error) {
	return e.tmp.Seek(offset, whence)
}

func (e *escritor) Read(p []byte) (int, error) { return e.tmp.Read(p) }

func (e *escritor) Close() error {
	if e.cerrado {
		return nil
	}
	e.cerrado = true

	ruta := e.tmp.Name()
	defer func() {
		_ = e.tmp.Close()
		_ = os.Remove(ruta)
	}()

	tam, err := e.tmp.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := e.tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}

	// Windows crea archivos vacíos para comprobar si puede escribir; no tiene
	// sentido subirlos a Drive.
	if tam == 0 {
		return nil
	}

	if err := e.fs.deps.Quota.CheckStorage(e.ctx, e.fs.user, tam); err != nil {
		var limite *quota.LimitError
		if errors.As(err, &limite) {
			e.fs.auditar("file.upload", "folder", e.padre.ID.String(), e.nombre, false,
				map[string]any{"via": "webdav", "motivo": "cuota superada"})
			return os.ErrPermission
		}
		return err
	}

	acc, err := e.fs.cuenta(e.ctx)
	if err != nil {
		return err
	}
	padreDrive := e.padre.DriveFolderID
	if padreDrive == "" {
		padreDrive = acc.RootFolderID
	}

	sesion, err := e.fs.deps.Drive.StartResumable(e.ctx, acc, e.nombre, "application/octet-stream", padreDrive, tam)
	if err != nil {
		return err
	}
	res, err := e.fs.deps.Drive.UploadChunk(e.ctx, sesion, 0, tam, tam, e.tmp)
	if err != nil {
		return err
	}
	if !res.Complete || res.File == nil {
		return errors.New("la subida no se completó")
	}

	// Si ya existía un archivo con ese nombre, se versiona en lugar de
	// duplicarlo, igual que al subir desde la web.
	existente, err := e.fs.deps.Repo.FileByNameInFolder(e.ctx, e.padre.ID, e.nombre)
	if err == nil {
		if existente.DriveFileID != "" && existente.DriveFileID != res.File.ID {
			_ = e.fs.deps.Repo.AddVersion(e.ctx, existente.ID, existente.Version,
				existente.DriveFileID, existente.SizeBytes, existente.MD5, existente.OwnerID)
			_, _ = e.fs.deps.Repo.BumpVersion(e.ctx, existente.ID)
			if existente.OwnerID != nil {
				_ = e.fs.deps.Repo.AddUsedBytes(e.ctx, *existente.OwnerID, -existente.SizeBytes)
			}
		}
		if err := e.fs.deps.Repo.MarkFileReady(e.ctx, existente.ID, res.File.ID, tam, res.File.MD5Checksum); err != nil {
			return err
		}
		_ = e.fs.deps.Repo.AddUsedBytes(e.ctx, e.fs.user.ID, tam)
		e.registrar(existente.ID, tam)
		return nil
	}

	creado, err := e.fs.deps.Repo.CreateFile(e.ctx, e.padre.ID, e.nombre,
		"application/octet-stream", tam, &e.fs.user.ID, &acc.ID)
	if err != nil {
		return err
	}
	if err := e.fs.deps.Repo.MarkFileReady(e.ctx, creado.ID, res.File.ID, tam, res.File.MD5Checksum); err != nil {
		return err
	}
	_ = e.fs.deps.Repo.AddUsedBytes(e.ctx, e.fs.user.ID, tam)
	e.registrar(creado.ID, tam)
	return nil
}

func (e *escritor) registrar(fileID uuid.UUID, tam int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		u, f := e.fs.user.ID, fileID
		if err := e.fs.deps.Repo.InsertBandwidth(ctx, &u, &f, nil, "upload", tam, "webdav"); err != nil {
			slog.Error("webdav: no se pudo registrar la subida", "error", err)
		}
	}()
	e.fs.auditar("file.upload", "file", fileID.String(), e.nombre, true,
		map[string]any{"via": "webdav", "bytes": tam})
}

func (e *escritor) Stat() (os.FileInfo, error) {
	return &info{nombre: e.nombre, tam: e.escritos, modTime: time.Now()}, nil
}

func (e *escritor) Readdir(int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }
