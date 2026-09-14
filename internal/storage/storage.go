package storage

import "io"

// Storage guarda archivos binarios (fotos de producto) y sabe construir la
// URL pública para mostrarlos. La implementación se elige por configuración:
// Local para desarrollo, S3-compatible (Cloudflare R2, etc.) en producción,
// sin que el resto de la app tenga que cambiar.
type Storage interface {
	// Save guarda el contenido bajo un nombre único derivado de filename
	// y devuelve la ruta relativa para persistir en la base de datos.
	Save(filename string, data io.Reader) (path string, err error)

	// URL construye el link público a partir de la ruta guardada.
	URL(path string) string

	// Delete elimina el archivo. No es un error si ya no existe.
	Delete(path string) error
}
