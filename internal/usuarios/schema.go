package usuarios

import (
	"database/sql"
	"fmt"
)

const schema = `
CREATE TABLE IF NOT EXISTS modulos (
	id     SERIAL PRIMARY KEY,
	clave  VARCHAR(50)  UNIQUE NOT NULL,
	nombre VARCHAR(100) NOT NULL,
	orden  SMALLINT NOT NULL DEFAULT 0
);

INSERT INTO modulos (clave, nombre, orden) VALUES
	('bd', 'Base de datos', 1),
	('inventario', 'Inventario', 2),
	('sitio', 'Sitio web', 3),
	('pedidos', 'Pedidos', 4)
ON CONFLICT (clave) DO NOTHING;

CREATE TABLE IF NOT EXISTS usuarios (
	id            SERIAL PRIMARY KEY,
	usuario       VARCHAR(50) UNIQUE NOT NULL,
	password_hash VARCHAR(255) NOT NULL,
	secret_key    VARCHAR(64) NOT NULL,
	rol           VARCHAR(20) NOT NULL CHECK (rol IN ('superusuario','administrativo')),
	activo        BOOLEAN NOT NULL DEFAULT true,
	creado_en     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS usuario_modulos (
	usuario_id INTEGER NOT NULL REFERENCES usuarios(id) ON DELETE CASCADE,
	modulo_id  INTEGER NOT NULL REFERENCES modulos(id) ON DELETE CASCADE,
	PRIMARY KEY (usuario_id, modulo_id)
);
`

// Migrate crea el esquema de usuarios/módulos/permisos si todavía no
// existe, y siembra el catálogo fijo de módulos. Idempotente.
func Migrate(conn *sql.DB) error {
	if _, err := conn.Exec(schema); err != nil {
		return fmt.Errorf("migrando esquema de usuarios: %w", err)
	}
	return nil
}
