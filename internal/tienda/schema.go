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
-- 'transferencia_por_revisar' mide 25 caracteres. Ampliar un VARCHAR no
-- reescribe la tabla.
ALTER TABLE pedidos ALTER COLUMN estado TYPE VARCHAR(30);
DROP INDEX IF EXISTS idx_pedidos_reserva;
CREATE INDEX IF NOT EXISTS idx_pedidos_reserva_v2 ON pedidos(estado, expira_en)
	WHERE estado IN ('pendiente_pago', 'pagando', 'espera_transferencia', 'transferencia_por_revisar');
-- Cómo paga el pedido. Los anteriores a la transferencia son de tarjeta.
ALTER TABLE pedidos ADD COLUMN IF NOT EXISTS metodo_pago VARCHAR(20) NOT NULL DEFAULT 'tarjeta';
-- Lo que se cobró de envío a domicilio. Es una copia del costo vigente al hacer el
-- pedido (ya incluido en total): cambiar el costo después no altera pedidos hechos.
ALTER TABLE pedidos ADD COLUMN IF NOT EXISTS costo_envio NUMERIC(12,2) NOT NULL DEFAULT 0;
-- Cancelación de un pedido ya pagado: quién, cuándo y si sus productos
-- volvieron al inventario.
ALTER TABLE pedidos ADD COLUMN IF NOT EXISTS cancelado_por VARCHAR(50) NOT NULL DEFAULT '';
ALTER TABLE pedidos ADD COLUMN IF NOT EXISTS cancelado_en TIMESTAMPTZ;
ALTER TABLE pedidos ADD COLUMN IF NOT EXISTS inventario_devuelto BOOLEAN NOT NULL DEFAULT false;
-- Seguimiento del dinero de un pedido cancelado. El reembolso se hace fuera del
-- sistema (banco o pasarela); aquí solo queda anotado si falta y quién lo hizo.
ALTER TABLE pagos ADD COLUMN IF NOT EXISTS reembolso VARCHAR(10) NOT NULL DEFAULT '';
ALTER TABLE pagos ADD COLUMN IF NOT EXISTS reembolso_por VARCHAR(50) NOT NULL DEFAULT '';
ALTER TABLE pagos ADD COLUMN IF NOT EXISTS reembolso_en TIMESTAMPTZ;
-- Para limitar pedidos abiertos por correo.
CREATE INDEX IF NOT EXISTS idx_pedidos_email ON pedidos(lower(cliente_email)) WHERE cliente_email <> '';
`

// schemaComprobantes guarda los comprobantes de transferencia que suben los
// clientes. El archivo vive fuera de media/ (que es público): solo el admin lo
// descarga. El índice único impide usar la misma boleta de un banco en dos
// pedidos (salvo que el comprobante se haya rechazado).
const schemaComprobantes = `
CREATE TABLE IF NOT EXISTS comprobantes (
	id                  SERIAL PRIMARY KEY,
	pedido_id           INTEGER      NOT NULL REFERENCES pedidos(id) ON DELETE CASCADE,
	archivo             VARCHAR(80)  NOT NULL,
	numero_boleta       VARCHAR(40)  NOT NULL,
	banco_origen        VARCHAR(100) NOT NULL,
	fecha_transferencia DATE         NOT NULL,
	estado              VARCHAR(20)  NOT NULL DEFAULT 'por_revisar'
		CHECK (estado IN ('por_revisar', 'aceptado', 'rechazado')),
	nota                VARCHAR(255) NOT NULL DEFAULT '',
	revisado_por        VARCHAR(50)  NOT NULL DEFAULT '',
	revisado_en         TIMESTAMPTZ,
	creado_en           TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- Lo que el cliente dice haber transferido. El negocio lo compara con el total del
-- pedido al revisar. 0 en los comprobantes anteriores a este campo.
ALTER TABLE comprobantes ADD COLUMN IF NOT EXISTS monto NUMERIC(12,2) NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_comprobantes_pedido ON comprobantes(pedido_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_comprobantes_boleta
	ON comprobantes (lower(banco_origen), lower(numero_boleta)) WHERE estado <> 'rechazado';
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
	if _, err := conn.Exec(schemaComprobantes); err != nil {
		return fmt.Errorf("migrando esquema de comprobantes: %w", err)
	}
	return nil
}
