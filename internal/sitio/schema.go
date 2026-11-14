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

-- Fondo animado de la tienda (cascada de pinceles y frascos de pintura). Nace
-- apagado: la tienda se ve igual que antes hasta que se active desde el admin.
--   opacidad: porcentaje 20-100 | velocidad: 1 (lenta) a 10 (rápida)
--   cantidad: 1 pocas, 2 normal, 3 bastantes figuras
ALTER TABLE config_sitio ADD COLUMN IF NOT EXISTS cascada_activa BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE config_sitio ADD COLUMN IF NOT EXISTS cascada_opacidad SMALLINT NOT NULL DEFAULT 90
	CHECK (cascada_opacidad BETWEEN 20 AND 100);
ALTER TABLE config_sitio ADD COLUMN IF NOT EXISTS cascada_velocidad SMALLINT NOT NULL DEFAULT 5
	CHECK (cascada_velocidad BETWEEN 1 AND 10);
ALTER TABLE config_sitio ADD COLUMN IF NOT EXISTS cascada_cantidad SMALLINT NOT NULL DEFAULT 2
	CHECK (cascada_cantidad BETWEEN 1 AND 3);

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
