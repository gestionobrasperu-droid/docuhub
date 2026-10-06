// Package davfs expone la plataforma como un sistema de archivos WebDAV, para
// que Windows pueda montarla con una letra de unidad y trabajar desde el
// Explorador.
//
// La diferencia con montar Google Drive directamente es el control: aquí cada
// lectura y cada escritura pasa por los permisos, las cuotas y la bitácora de
// DocuHub, igual que si se hiciera desde la web. Quien no tenga permiso sobre
// una carpeta no la ve en el Explorador.
//
// No hay nada almacenado en la laptop: los bytes van y vienen de Google Drive.
// Lo único que toca el disco local es un archivo temporal mientras se sube,
// porque WebDAV escribe en trozos desordenados y Drive necesita el tamaño por
// adelantado.
package davfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/docuhub/docuhub/internal/drive"
	"github.com/docuhub/docuhub/internal/models"
	"github.com/docuhub/docuhub/internal/quota"
	"github.com/docuhub/docuhub/internal/repo"
	"github.com/google/uuid"
	"golang.org/x/net/webdav"
)

// Deps son las piezas de la plataforma que el sistema de archivos necesita.
type Deps struct {
	Repo  *repo.Repo
	Drive *drive.Client
	Quota *quota.Checker
	// Audit recibe las operaciones para que queden en la bitácora igual que
	// las hechas desde la web.
	Audit func(action, resType, resID, resName string, ok bool, meta map[string]any)
}

// FS implementa webdav.FileSystem para un usuario concreto. Se crea uno por
// petición: el usuario forma parte del estado porque de él dependen los
// permisos y las cuotas.
type FS struct {
	deps Deps
	user *models.User
}

func New(deps Deps, user *models.User) *FS {
	return &FS{deps: deps, user: user}
}

// ---------------------------------------------------------------- rutas ----

// resolver recorre la ruta desde la carpeta raíz. Devuelve la carpeta que
// contiene al último elemento, y el archivo o la carpeta final si existen.
type resuelto struct {
	padre   *models.Folder
	cadena  []*models.Folder
	carpeta *models.Folder // no nil si la ruta apunta a una carpeta
	archivo *models.File   // no nil si apunta a un archivo
	nombre  string         // último segmento
	esRaiz  bool
}

func limpiar(name string) []string {
	name = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(name, "\\", "/")), "/")
	if name == "" || name == "." {
		return nil
	}
	return strings.Split(name, "/")
}

func (f *FS) resolver(ctx context.Context, name string) (*resuelto, error) {
	raiz, err := f.deps.Repo.RootFolder(ctx)
	if err != nil {
		return nil, os.ErrNotExist
	}

	partes := limpiar(name)
	r := &resuelto{padre: raiz, carpeta: raiz, cadena: []*models.Folder{raiz}, esRaiz: true}
	if len(partes) == 0 {
		return r, nil
	}

	actual := raiz
	cadena := []*models.Folder{raiz}

	for i, p := range partes {
		ultimo := i == len(partes)-1

		hijas, err := f.deps.Repo.ListSubfolders(ctx, actual.ID)
		if err != nil {
			return nil, err
		}
		var encontrada *models.Folder
		for _, h := range hijas {
			if strings.EqualFold(h.Name, p) {
				encontrada = h
				break
			}
		}

		if encontrada != nil {
			actual = encontrada
			cadena = append(cadena, actual)
			if ultimo {
				return &resuelto{padre: cadena[len(cadena)-2], cadena: cadena, carpeta: actual, nombre: p}, nil
			}
			continue
		}

		if !ultimo {
			return nil, os.ErrNotExist
		}

		// Último segmento y no es carpeta: puede ser un archivo existente o
		// uno que está a punto de crearse.
		archivo, err := f.deps.Repo.FileByNameInFolder(ctx, actual.ID, p)
		if err == nil {
			return &resuelto{padre: actual, cadena: cadena, archivo: archivo, nombre: p}, nil
		}
		if !errors.Is(err, repo.ErrNotFound) {
			return nil, err
		}
		return &resuelto{padre: actual, cadena: cadena, nombre: p}, nil
	}

	return r, nil
}

// permitido calcula el nivel del usuario sobre una carpeta de la cadena.
func (f *FS) permitido(ctx context.Context, cadena []*models.Folder, resType string, resID uuid.UUID, owner *uuid.UUID) string {
	ids := make([]uuid.UUID, 0, len(cadena))
	restringida := false
	for _, c := range cadena {
		ids = append(ids, c.ID)
		if c.Restricted {
			restringida = true
		}
	}

	explicito, err := f.deps.Repo.EffectiveLevel(ctx, f.user, resType, resID, ids, owner)
	if err != nil {
		return ""
	}
	if restringida {
		return explicito
	}

	base := models.LevelViewer
	switch f.user.Role {
	case models.RoleAdmin, models.RoleManager:
		base = models.LevelManager
	case models.RoleMember:
		base = models.LevelEditor
	}
	if models.LevelRank[explicito] > models.LevelRank[base] {
		return explicito
	}
	return base
}

func alMenos(nivel, minimo string) bool {
	return models.LevelRank[nivel] >= models.LevelRank[minimo]
}

// ------------------------------------------------------- webdav.FileSystem --

func (f *FS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	r, err := f.resolver(ctx, name)
	if err != nil {
		return err
	}
	if r.carpeta != nil || r.archivo != nil {
		return os.ErrExist
	}
	if !alMenos(f.permitido(ctx, r.cadena, "folder", r.padre.ID, r.padre.OwnerID), models.LevelEditor) {
		return os.ErrPermission
	}

	acc, err := f.cuenta(ctx)
	if err != nil {
		return err
	}
	padreDrive := r.padre.DriveFolderID
	if padreDrive == "" {
		padreDrive = acc.RootFolderID
	}
	carpetaDrive, err := f.deps.Drive.EnsureFolder(ctx, acc, r.nombre, padreDrive)
	if err != nil {
		return err
	}

	ruta := strings.TrimSuffix(r.padre.Path, "/") + "/" + r.nombre
	nueva, err := f.deps.Repo.CreateFolder(ctx, &r.padre.ID, r.nombre, ruta,
		carpetaDrive.ID, &acc.ID, &f.user.ID, false)
	if err != nil {
		return err
	}
	f.auditar("folder.create", "folder", nueva.ID.String(), nueva.Path, true, map[string]any{"via": "webdav"})
	return nil
}

func (f *FS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	r, err := f.resolver(ctx, name)
	if err != nil {
		return nil, err
	}

	// ¿Escritura?
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0 && flag&os.O_WRONLY != 0 || flag&os.O_CREATE != 0 {
		if r.carpeta != nil {
			return nil, os.ErrInvalid
		}
		if !alMenos(f.permitido(ctx, r.cadena, "folder", r.padre.ID, r.padre.OwnerID), models.LevelEditor) {
			return nil, os.ErrPermission
		}
		return f.nuevoEscritor(ctx, r)
	}

	// Carpeta: se abre para listarla.
	if r.carpeta != nil {
		if !alMenos(f.permitido(ctx, r.cadena, "folder", r.carpeta.ID, r.carpeta.OwnerID), models.LevelViewer) {
			return nil, os.ErrPermission
		}
		return &dirFile{fs: f, ctx: ctx, carpeta: r.carpeta, cadena: r.cadena}, nil
	}

	if r.archivo == nil {
		return nil, os.ErrNotExist
	}
	if !alMenos(f.permitido(ctx, r.cadena, "file", r.archivo.ID, r.archivo.OwnerID), models.LevelDownloader) {
		return nil, os.ErrPermission
	}
	return f.nuevoLector(ctx, r.archivo)
}

func (f *FS) RemoveAll(ctx context.Context, name string) error {
	r, err := f.resolver(ctx, name)
	if err != nil {
		return err
	}

	if r.archivo != nil {
		if !alMenos(f.permitido(ctx, r.cadena, "file", r.archivo.ID, r.archivo.OwnerID), models.LevelEditor) {
			return os.ErrPermission
		}
		if err := f.deps.Repo.SoftDeleteFile(ctx, r.archivo.ID); err != nil {
			return err
		}
		if r.archivo.OwnerID != nil {
			_ = f.deps.Repo.AddUsedBytes(ctx, *r.archivo.OwnerID, -r.archivo.SizeBytes)
		}
		if acc, err := f.cuenta(ctx); err == nil && r.archivo.DriveFileID != "" {
			if err := f.deps.Drive.Trash(ctx, acc, r.archivo.DriveFileID); err != nil {
				slog.Warn("webdav: no se pudo enviar a la papelera de Drive", "error", err)
			}
		}
		f.auditar("file.delete", "file", r.archivo.ID.String(), r.archivo.Name, true, map[string]any{"via": "webdav"})
		return nil
	}

	if r.carpeta != nil {
		if r.carpeta.IsRoot {
			return os.ErrPermission
		}
		if !alMenos(f.permitido(ctx, r.cadena, "folder", r.carpeta.ID, r.carpeta.OwnerID), models.LevelManager) {
			return os.ErrPermission
		}
		if err := f.deps.Repo.SoftDeleteFolder(ctx, r.carpeta.ID); err != nil {
			return err
		}
		f.auditar("folder.delete", "folder", r.carpeta.ID.String(), r.carpeta.Path, true, map[string]any{"via": "webdav"})
		return nil
	}
	return os.ErrNotExist
}

func (f *FS) Rename(ctx context.Context, oldName, newName string) error {
	origen, err := f.resolver(ctx, oldName)
	if err != nil {
		return err
	}
	destino, err := f.resolver(ctx, newName)
	if err != nil {
		return err
	}
	if destino.carpeta != nil || destino.archivo != nil {
		return os.ErrExist
	}

	acc, _ := f.cuenta(ctx)

	if origen.archivo != nil {
		if !alMenos(f.permitido(ctx, origen.cadena, "file", origen.archivo.ID, origen.archivo.OwnerID), models.LevelEditor) {
			return os.ErrPermission
		}
		// Mover entre carpetas y renombrar llegan por la misma operación.
		if destino.padre.ID != origen.padre.ID {
			if err := f.deps.Repo.MoveFile(ctx, origen.archivo.ID, destino.padre.ID); err != nil {
				return err
			}
		}
		if !strings.EqualFold(origen.nombre, destino.nombre) {
			if acc != nil && origen.archivo.DriveFileID != "" {
				if err := f.deps.Drive.Rename(ctx, acc, origen.archivo.DriveFileID, destino.nombre); err != nil {
					return err
				}
			}
			if _, err := f.deps.Repo.UpdateFileMeta(ctx, origen.archivo.ID, &destino.nombre, nil, nil); err != nil {
				return err
			}
		}
		f.auditar("file.update", "file", origen.archivo.ID.String(), destino.nombre, true,
			map[string]any{"via": "webdav", "accion": "mover o renombrar"})
		return nil
	}

	if origen.carpeta != nil {
		if origen.carpeta.IsRoot {
			return os.ErrPermission
		}
		if !alMenos(f.permitido(ctx, origen.cadena, "folder", origen.carpeta.ID, origen.carpeta.OwnerID), models.LevelManager) {
			return os.ErrPermission
		}
		if acc != nil && origen.carpeta.DriveFolderID != "" {
			if err := f.deps.Drive.Rename(ctx, acc, origen.carpeta.DriveFolderID, destino.nombre); err != nil {
				return err
			}
		}
		ruta := strings.TrimSuffix(destino.padre.Path, "/") + "/" + destino.nombre
		if err := f.deps.Repo.RenameFolder(ctx, origen.carpeta.ID, destino.nombre, ruta); err != nil {
			return err
		}
		f.auditar("folder.update", "folder", origen.carpeta.ID.String(), ruta, true, map[string]any{"via": "webdav"})
		return nil
	}
	return os.ErrNotExist
}

func (f *FS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	r, err := f.resolver(ctx, name)
	if err != nil {
		return nil, err
	}
	if r.carpeta != nil {
		if !alMenos(f.permitido(ctx, r.cadena, "folder", r.carpeta.ID, r.carpeta.OwnerID), models.LevelViewer) {
			return nil, os.ErrPermission
		}
		return infoCarpeta(r.carpeta), nil
	}
	if r.archivo != nil {
		if !alMenos(f.permitido(ctx, r.cadena, "file", r.archivo.ID, r.archivo.OwnerID), models.LevelViewer) {
			return nil, os.ErrPermission
		}
		return infoArchivo(r.archivo), nil
	}
	return nil, os.ErrNotExist
}

// ------------------------------------------------------------- auxiliares --

func (f *FS) cuenta(ctx context.Context) (*models.DriveAccount, error) {
	acc, err := f.deps.Repo.PrimaryAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("no hay cuenta de Google Drive conectada")
	}
	return acc, nil
}

func (f *FS) auditar(action, resType, resID, resName string, ok bool, meta map[string]any) {
	if f.deps.Audit != nil {
		f.deps.Audit(action, resType, resID, resName, ok, meta)
	}
}

// ---------------------------------------------------------- os.FileInfo ----

type info struct {
	nombre  string
	tam     int64
	dir     bool
	modTime time.Time
}

func (i *info) Name() string { return i.nombre }
func (i *info) Size() int64  { return i.tam }
func (i *info) Mode() os.FileMode {
	if i.dir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func (i *info) ModTime() time.Time { return i.modTime }
func (i *info) IsDir() bool        { return i.dir }
func (i *info) Sys() any           { return nil }

func infoCarpeta(c *models.Folder) os.FileInfo {
	return &info{nombre: c.Name, dir: true, modTime: c.UpdatedAt}
}

func infoArchivo(a *models.File) os.FileInfo {
	return &info{nombre: a.Name, tam: a.SizeBytes, modTime: a.UpdatedAt}
}

// ------------------------------------------------------------- directorio --

type dirFile struct {
	fs      *FS
	ctx     context.Context
	carpeta *models.Folder
	cadena  []*models.Folder

	mu       sync.Mutex
	leidos   bool
	entradas []os.FileInfo
	pos      int
}

func (d *dirFile) cargar() error {
	if d.leidos {
		return nil
	}
	hijas, err := d.fs.deps.Repo.ListSubfolders(d.ctx, d.carpeta.ID)
	if err != nil {
		return err
	}
	for _, h := range hijas {
		cadena := append(append([]*models.Folder{}, d.cadena...), h)
		// Una carpeta restringida sin permiso no aparece en el Explorador.
		if alMenos(d.fs.permitido(d.ctx, cadena, "folder", h.ID, h.OwnerID), models.LevelViewer) {
			d.entradas = append(d.entradas, infoCarpeta(h))
		}
	}

	archivos, err := d.fs.deps.Repo.ListFiles(d.ctx, d.carpeta.ID)
	if err != nil {
		return err
	}
	for _, a := range archivos {
		d.entradas = append(d.entradas, infoArchivo(a))
	}
	d.leidos = true
	return nil
}

func (d *dirFile) Readdir(count int) ([]os.FileInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.cargar(); err != nil {
		return nil, err
	}
	if count <= 0 {
		resto := d.entradas[d.pos:]
		d.pos = len(d.entradas)
		return resto, nil
	}
	if d.pos >= len(d.entradas) {
		return nil, io.EOF
	}
	fin := d.pos + count
	if fin > len(d.entradas) {
		fin = len(d.entradas)
	}
	trozo := d.entradas[d.pos:fin]
	d.pos = fin
	return trozo, nil
}

func (d *dirFile) Stat() (os.FileInfo, error) { return infoCarpeta(d.carpeta), nil }
func (d *dirFile) Close() error               { return nil }
func (d *dirFile) Read([]byte) (int, error)   { return 0, os.ErrInvalid }
func (d *dirFile) Write([]byte) (int, error)  { return 0, os.ErrInvalid }
func (d *dirFile) Seek(int64, int) (int64, error) {
	return 0, os.ErrInvalid
}
