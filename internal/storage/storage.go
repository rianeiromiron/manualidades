package storage

import "io"

// Storage guarda archivos binarios (fotos de producto) y sabe construir la
// URL pública para mostrarlos. La implementación se elige por configuración:
// Local para desarrollo, S3-compatible (Cloudflare R2, etc.) en producción,
// sin que el resto de la app tenga que cambiar.
type Storage interface {
	// Save valida que el contenido sea una imagen JPEG, PNG o WebP, la
	// reprocesa (ver NormalizarImagen) y la guarda bajo un nombre único.
	// Devuelve la ruta relativa para persistir en la base de datos.
	// filename solo es informativo: la extensión sale del contenido.
	Save(filename string, data io.Reader) (path string, err error)

	// URL construye el link público a partir de la ruta guardada.
	URL(path string) string

	// Delete elimina el archivo. No es un error si ya no existe.
	Delete(path string) error
}
