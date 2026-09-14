package tienda

import (
	"database/sql"
	"time"
)

// querier lo satisfacen tanto *sql.DB como *sql.Tx (mismo patrón que
// internal/inventario/movimientos.go), para poder registrar el pago dentro
// de la misma transacción que crea el pedido.
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

const (
	MetodoTarjeta = "tarjeta"

	EstadoPagoAprobado  = "aprobado"
	EstadoPagoRechazado = "rechazado"
	// EstadoPagoFallo es una falla técnica del proveedor (no respondió, o
	// respondió con error) — distinto de un rechazo de negocio.
	EstadoPagoFallo = "fallo"
)

type Pago struct {
	ID       int
	PedidoID *int // nil si el intento nunca llegó a generar un pedido
	Metodo   string
	Monto    float64
	Estado   string
	// Referencia es el número de transacción que dio el proveedor; vacío
	// si el proveedor nunca llegó a responder (EstadoPagoFallo).
	Referencia      string
	TarjetaMarca    string
	TarjetaUltimos4 string
	MotivoRechazo   string
	CreadoEn        time.Time
}

// RegistrarPago guarda un intento de pago. pedidoID es nil cuando el
// intento no resultó en un pedido (rechazo o falla del proveedor). La
// referencia la da el proveedor (internal/pasarela); si nunca respondió,
// se guarda vacía.
func RegistrarPago(q querier, pedidoID *int, metodo string, monto float64, estado, referencia, tarjetaMarca, tarjetaUltimos4, motivoRechazo string) (int, error) {
	var id int
	err := q.QueryRow(
		`INSERT INTO pagos (pedido_id, metodo, monto, estado, referencia, tarjeta_marca, tarjeta_ultimos4, motivo_rechazo)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		pedidoID, metodo, monto, estado, referencia, tarjetaMarca, tarjetaUltimos4, motivoRechazo,
	).Scan(&id)
	return id, err
}

func scanPago(row interface{ Scan(dest ...any) error }) (Pago, error) {
	var p Pago
	var pedidoID sql.NullInt64
	err := row.Scan(&p.ID, &pedidoID, &p.Metodo, &p.Monto, &p.Estado, &p.Referencia, &p.TarjetaMarca, &p.TarjetaUltimos4, &p.MotivoRechazo, &p.CreadoEn)
	if err != nil {
		return Pago{}, err
	}
	if pedidoID.Valid {
		v := int(pedidoID.Int64)
		p.PedidoID = &v
	}
	return p, nil
}

func GetPagoPorPedido(conn *sql.DB, pedidoID int) (Pago, error) {
	row := conn.QueryRow(
		`SELECT id, pedido_id, metodo, monto, estado, referencia, tarjeta_marca, tarjeta_ultimos4, motivo_rechazo, creado_en
		 FROM pagos WHERE pedido_id = $1 ORDER BY id DESC LIMIT 1`,
		pedidoID,
	)
	return scanPago(row)
}

// ListPagos devuelve los intentos de pago más recientes, incluidos los que
// nunca llegaron a generar un pedido (rechazados o con falla del
// proveedor) — para que el admin también pueda verlos, no solo las ventas
// concretadas en /admin/pedidos.
func ListPagos(conn *sql.DB, limit int) ([]Pago, error) {
	rows, err := conn.Query(
		`SELECT id, pedido_id, metodo, monto, estado, referencia, tarjeta_marca, tarjeta_ultimos4, motivo_rechazo, creado_en
		 FROM pagos ORDER BY creado_en DESC LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Pago
	for rows.Next() {
		p, err := scanPago(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
