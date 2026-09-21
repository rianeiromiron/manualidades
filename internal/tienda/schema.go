package tienda

import (
	"database/sql"
	"fmt"
)

const schema = `
CREATE TABLE IF NOT EXISTS pedidos (
	id                SERIAL PRIMARY KEY,
	cliente_nombre    VARCHAR(150) NOT NULL,
	cliente_telefono  VARCHAR(50)  NOT NULL,
	cliente_email     VARCHAR(150) NOT NULL DEFAULT '',
	cliente_nit       VARCHAR(20)  NOT NULL DEFAULT 'CF',
	metodo_entrega    VARCHAR(20)  NOT NULL CHECK (metodo_entrega IN ('recoger', 'domicilio')),
	direccion_entrega VARCHAR(255) NOT NULL DEFAULT '',
	notas             VARCHAR(500) NOT NULL DEFAULT '',
	estado            VARCHAR(20)  NOT NULL DEFAULT 'pagado',
	total             NUMERIC(12,2) NOT NULL,
	creado_en         TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pedido_items (
	id               SERIAL PRIMARY KEY,
	pedido_id        INTEGER NOT NULL REFERENCES pedidos(id) ON DELETE CASCADE,
	producto_id      INTEGER NOT NULL REFERENCES productos(id),
	nombre_producto  VARCHAR(150) NOT NULL,
	cantidad         NUMERIC(12,2) NOT NULL CHECK (cantidad > 0),
	precio_unitario  NUMERIC(12,2) NOT NULL,
	subtotal         NUMERIC(12,2) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pedido_items_pedido ON pedido_items(pedido_id);

CREATE TABLE IF NOT EXISTS pagos (
	id               SERIAL PRIMARY KEY,
	pedido_id        INTEGER REFERENCES pedidos(id) ON DELETE CASCADE,
	metodo           VARCHAR(30)  NOT NULL DEFAULT 'tarjeta',
	monto            NUMERIC(12,2) NOT NULL,
	estado           VARCHAR(20)  NOT NULL CHECK (estado IN ('iniciado', 'aprobado', 'rechazado', 'fallo', 'por_conciliar')),
	referencia       VARCHAR(60)  NOT NULL,
	tarjeta_marca    VARCHAR(20)  NOT NULL DEFAULT '',
	tarjeta_ultimos4 VARCHAR(4)   NOT NULL DEFAULT '',
	motivo_rechazo   VARCHAR(255) NOT NULL DEFAULT '',
	creado_en        TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_pagos_pedido ON pagos(pedido_id);
`

// alteraciones para bases creadas antes de que pagos.pedido_id pudiera ser
// nulo y antes de que 'iniciado', 'fallo' y 'por_conciliar' existieran como
// estados. Todas son idempotentes.
const alterPagos = `
ALTER TABLE pagos ALTER COLUMN pedido_id DROP NOT NULL;
ALTER TABLE pagos DROP CONSTRAINT IF EXISTS pagos_estado_check;
ALTER TABLE pagos ADD CONSTRAINT pagos_estado_check CHECK (estado IN ('iniciado', 'aprobado', 'rechazado', 'fallo', 'por_conciliar'));
`

// alterPedidos agrega lo necesario para reservar stock antes de cobrar:
//   - expira_en: hasta cuándo vale la reserva de un pedido pendiente_pago /
//     pagando (NULL en pedidos ya pagados o cerrados).
//   - token: identificador aleatorio que el navegador guarda en una cookie
//     para reencontrar su pedido pendiente (el id es secuencial y adivinable).
//
// Idempotente.
const alterPedidos = `
ALTER TABLE pedidos ADD COLUMN IF NOT EXISTS expira_en TIMESTAMPTZ;
ALTER TABLE pedidos ADD COLUMN IF NOT EXISTS token VARCHAR(64);
CREATE UNIQUE INDEX IF NOT EXISTS idx_pedidos_token ON pedidos(token) WHERE token IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_pedidos_reserva ON pedidos(estado, expira_en) WHERE estado IN ('pendiente_pago', 'pagando');
`

// Migrate crea las tablas de pedidos si todavía no existen, y actualiza el
// esquema de pedidos y pagos en instalaciones creadas antes de la pasarela
// simulada y de la reserva de stock. Idempotente.
func Migrate(conn *sql.DB) error {
	if _, err := conn.Exec(schema); err != nil {
		return fmt.Errorf("migrando esquema de tienda: %w", err)
	}
	if _, err := conn.Exec(alterPagos); err != nil {
		return fmt.Errorf("actualizando esquema de pagos: %w", err)
	}
	if _, err := conn.Exec(alterPedidos); err != nil {
		return fmt.Errorf("actualizando esquema de pedidos: %w", err)
	}
	return nil
}
