package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Local guarda los archivos en disco. Pensado para desarrollo; en
// producción se reemplaza por una implementación S3-compatible que
// satisface la misma interfaz Storage.
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

func (l *Local) Save(filename string, data io.Reader) (string, error) {
	ext := filepath.Ext(filename)
	unique := uuid.NewString() + ext

	dst, err := os.Create(filepath.Join(l.Dir, unique))
	if err != nil {
		return "", fmt.Errorf("creando archivo: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, data); err != nil {
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
