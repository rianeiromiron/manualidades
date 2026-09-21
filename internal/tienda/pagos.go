package tienda

import (
	"database/sql"
	"time"
)

// querier lo satisfacen tanto *sql.DB como *sql.Tx (mismo patrón que
// internal/inventario/movimientos.go), para poder registrar el pago dentro
// de la misma transacción que reserva o confirma el pedido.
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

const (
	MetodoTarjeta = "tarjeta"

	// EstadoPagoIniciado es un cobro que se está procesando: se registra
	// antes de llamar a la pasarela y se reemplaza por el desenlace real al
	// volver. Si se queda así, el proceso se interrumpió a mitad del cobro.
	EstadoPagoIniciado = "iniciado"

	EstadoPagoAprobado  = "aprobado"
	EstadoPagoRechazado = "rechazado"
	// EstadoPagoFallo es una falla técnica del proveedor (no respondió, o
	// respondió con error) — distinto de un rechazo de negocio.
	EstadoPagoFallo = "fallo"
	// EstadoPagoPorConciliar es un cobro que la pasarela pudo haber aprobado
	// (el cliente pudo haber pagado) pero cuyo pedido no se pudo confirmar:
	// el stock ya no alcanzó al confirmar, o el proceso se interrumpió a
	// mitad del cobro. Requiere conciliación manual: verificar en la
	// pasarela y reembolsar, o completar el pedido a mano. El motivo queda
	// en MotivoRechazo.
	EstadoPagoPorConciliar = "por_conciliar"
)

type Pago struct {
	ID       int
	PedidoID *int // nil solo en pagos antiguos, previos a la reserva de stock
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

// iniciarPago deja constancia de que se va a llamar a la pasarela, ANTES de
// llamarla, dentro de la misma transacción que reserva el stock. Si el
// proceso muere a mitad del cobro, este registro queda en EstadoPagoIniciado
// y LimpiarExpirados lo pasa a EstadoPagoPorConciliar para revisión manual.
func iniciarPago(q querier, pedidoID int, metodo string, monto float64) (int, error) {
	var id int
	err := q.QueryRow(
		`INSERT INTO pagos (pedido_id, metodo, monto, estado, referencia)
		 VALUES ($1, $2, $3, $4, '')
		 RETURNING id`,
		pedidoID, metodo, monto, EstadoPagoIniciado,
	).Scan(&id)
	return id, err
}

// actualizarPago fija el desenlace de un intento ya iniciado. La referencia
// la da el proveedor (internal/pasarela); si nunca respondió, queda vacía.
func actualizarPago(q querier, pagoID int, estado, referencia, tarjetaMarca, tarjetaUltimos4, motivo string) error {
	_, err := q.Exec(
		`UPDATE pagos SET estado = $2, referencia = $3, tarjeta_marca = $4, tarjeta_ultimos4 = $5, motivo_rechazo = $6
		 WHERE id = $1`,
		pagoID, estado, referencia, tarjetaMarca, tarjetaUltimos4, motivo,
	)
	return err
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

// ListPagos devuelve los intentos de pago más recientes, incluidos los
// rechazados, con falla del proveedor o por conciliar — para que el admin
// también pueda verlos, no solo las ventas concretadas en /admin/pedidos.
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
