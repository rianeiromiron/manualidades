package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Local guarda los archivos en disco: en desarrollo y también en producción
// mientras la app corra en una sola máquina. Si algún día las fotos se mueven
// a un almacén S3-compatible (Cloudflare R2), basta otra implementación de la
// interfaz Storage que reutilice NormalizarImagen.
type Local struct {
	// Dir es la carpeta física donde se escriben los archivos.
	Dir string
	// URLPrefix es el prefijo con el que se sirven esos archivos por HTTP.
	URLPrefix string
}

func NewLocal(dir, urlPrefix string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creando carpeta de medios %s: %w", dir, err)
	}
	return &Local{Dir: dir, URLPrefix: strings.TrimSuffix(urlPrefix, "/")}, nil
}

// Save valida y reprocesa la imagen (ver NormalizarImagen) y la guarda con un
// nombre aleatorio. La extensión sale del contenido real, no de filename, que
// se ignora: así un archivo subido como "x.html" nunca queda con esa extensión.
func (l *Local) Save(filename string, data io.Reader) (string, error) {
	datos, ext, err := NormalizarImagen(data)
	if err != nil {
		return "", err
	}
	unique := uuid.NewString() + ext

	if err := os.WriteFile(filepath.Join(l.Dir, unique), datos, 0o644); err != nil {
		return "", fmt.Errorf("guardando archivo: %w", err)
	}
	return unique, nil
}

func (l *Local) URL(path string) string {
	return l.URLPrefix + "/" + path
}

func (l *Local) Delete(path string) error {
	err := os.Remove(filepath.Join(l.Dir, path))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
