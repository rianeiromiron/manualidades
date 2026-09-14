package inventario

import (
	"database/sql"
	"fmt"
)

const schema = `
CREATE TABLE IF NOT EXISTS categorias (
	id          SERIAL PRIMARY KEY,
	nombre      VARCHAR(100) NOT NULL UNIQUE,
	descripcion TEXT NOT NULL DEFAULT '',
	creado_en   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS productos (
	id             SERIAL PRIMARY KEY,
	categoria_id   INTEGER NOT NULL REFERENCES categorias(id),
	nombre         VARCHAR(150) NOT NULL,
	descripcion    TEXT NOT NULL DEFAULT '',
	precio_compra  NUMERIC(12,2) NOT NULL DEFAULT 0,
	precio_venta   NUMERIC(12,2) NOT NULL DEFAULT 0,
	activo         BOOLEAN NOT NULL DEFAULT true,
	creado_en      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS producto_fotos (
	id           SERIAL PRIMARY KEY,
	producto_id  INTEGER NOT NULL REFERENCES productos(id) ON DELETE CASCADE,
	ruta         VARCHAR(500) NOT NULL,
	orden        INTEGER NOT NULL DEFAULT 0,
	creado_en    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS movimientos_inventario (
	id           SERIAL PRIMARY KEY,
	producto_id  INTEGER NOT NULL REFERENCES productos(id),
	tipo         VARCHAR(10) NOT NULL CHECK (tipo IN ('ingreso', 'consumo')),
	cantidad     NUMERIC(12,2) NOT NULL CHECK (cantidad > 0),
	motivo       VARCHAR(255) NOT NULL DEFAULT '',
	es_venta     BOOLEAN NOT NULL DEFAULT false,
	precio_venta NUMERIC(12,2) NOT NULL DEFAULT 0,
	fecha        DATE NOT NULL DEFAULT CURRENT_DATE,
	creado_en    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Bases creadas antes de separar "fecha del movimiento" (elegida por el
-- usuario) de "fecha de registro" (siempre automática): se agrega la
-- columna nueva y se recorta la hora de "fecha", que antes mezclaba ambas.
ALTER TABLE movimientos_inventario ADD COLUMN IF NOT EXISTS creado_en TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE movimientos_inventario ALTER COLUMN fecha TYPE DATE USING fecha::date;
ALTER TABLE movimientos_inventario ALTER COLUMN fecha SET DEFAULT CURRENT_DATE;

-- Marca si un consumo corresponde a una venta (para el reporte "solo ventas").
ALTER TABLE movimientos_inventario ADD COLUMN IF NOT EXISTS es_venta BOOLEAN NOT NULL DEFAULT false;

-- Precio real al que se vendió, solo aplica cuando es_venta = true.
ALTER TABLE movimientos_inventario ADD COLUMN IF NOT EXISTS precio_venta NUMERIC(12,2) NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_productos_categoria ON productos(categoria_id);
CREATE INDEX IF NOT EXISTS idx_fotos_producto ON producto_fotos(producto_id);
CREATE INDEX IF NOT EXISTS idx_movimientos_producto ON movimientos_inventario(producto_id);
`

// Migrate crea las tablas de inventario si todavía no existen. Es
// idempotente: correrla varias veces sobre la misma base no tiene efecto
// después de la primera vez.
func Migrate(conn *sql.DB) error {
	if _, err := conn.Exec(schema); err != nil {
		return fmt.Errorf("migrando esquema de inventario: %w", err)
	}
	return nil
}
