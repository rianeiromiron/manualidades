package sitio

import (
	"database/sql"
	"fmt"
)

const schema = `
CREATE TABLE IF NOT EXISTS config_sitio (
	id               SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
	nombre_negocio   VARCHAR(150) NOT NULL DEFAULT 'Manualidades',
	direccion        VARCHAR(255) NOT NULL DEFAULT '',
	telefono         VARCHAR(50)  NOT NULL DEFAULT '',
	email            VARCHAR(150) NOT NULL DEFAULT '',
	facebook_url     VARCHAR(255) NOT NULL DEFAULT '',
	instagram_url    VARCHAR(255) NOT NULL DEFAULT '',
	whatsapp_numero  VARCHAR(50)  NOT NULL DEFAULT '',
	logo_ruta        VARCHAR(500) NOT NULL DEFAULT '',
	color_fondo      VARCHAR(7)   NOT NULL DEFAULT '#FBE9EC',
	color_texto      VARCHAR(7)   NOT NULL DEFAULT '#1F2A44',
	color_marco      VARCHAR(7)   NOT NULL DEFAULT '#8A8F98',
	color_acento     VARCHAR(7)   NOT NULL DEFAULT '#C1592B',
	actualizado_en   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

INSERT INTO config_sitio (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- Lo que se cobra por entregar un pedido a domicilio. Vive en la base de datos,
-- no en el código: se cambia desde el admin (Sitio web). Q 45.00 de inicio.
ALTER TABLE config_sitio ADD COLUMN IF NOT EXISTS costo_envio NUMERIC(12,2) NOT NULL DEFAULT 45.00
	CHECK (costo_envio >= 0 AND costo_envio <= 9999.99);

-- Formas de pago que ofrece la tienda, activables desde el admin. Ambas nacen
-- activas para no cambiar el comportamiento de una instalación existente.
CREATE TABLE IF NOT EXISTS formas_pago (
	clave           VARCHAR(20) PRIMARY KEY CHECK (clave IN ('tarjeta', 'transferencia')),
	nombre          VARCHAR(50) NOT NULL,
	activa          BOOLEAN     NOT NULL DEFAULT true,
	orden           SMALLINT    NOT NULL DEFAULT 0,
	actualizado_por VARCHAR(50) NOT NULL DEFAULT '',
	actualizado_en  TIMESTAMPTZ
);
INSERT INTO formas_pago (clave, nombre, orden) VALUES
	('tarjeta', 'Tarjeta de crédito', 1),
	('transferencia', 'Transferencia bancaria', 2)
ON CONFLICT (clave) DO NOTHING;
`

// Migrate crea la tabla de configuración del sitio (una sola fila) si
// todavía no existe. Idempotente.
func Migrate(conn *sql.DB) error {
	if _, err := conn.Exec(schema); err != nil {
		return fmt.Errorf("migrando esquema del sitio: %w", err)
	}
	return nil
}
