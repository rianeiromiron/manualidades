package inventario

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

type Movimiento struct {
	ID             int
	ProductoID     int
	ProductoNombre string
	Tipo           string // "ingreso" o "consumo"
	Cantidad       float64
	Motivo         string
	EsVenta        bool      // solo tiene sentido cuando Tipo == "consumo"
	PrecioVenta    float64   // precio real de venta; solo aplica cuando EsVenta == true
	Fecha          time.Time // fecha del movimiento (elegida por el usuario)
	CreadoEn       time.Time // fecha y hora reales en que se registró en el sistema

	// AnulaA es el id del movimiento que este anula (0 si no es una anulación).
	// AnuladoPor es el id de la anulación que lo cancela (0 si sigue vigente).
	// PedidoID es el pedido de la tienda que lo originó (0 si es manual).
	AnulaA     int
	AnuladoPor int
	PedidoID   int
}

// Anulable dice si el movimiento se puede anular desde la pantalla: solo los
// manuales (los de un pedido se corrigen cancelando el pedido), que no sean
// ya una anulación ni estén anulados.
func (m Movimiento) Anulable() bool {
	return m.PedidoID == 0 && m.AnulaA == 0 && m.AnuladoPor == 0
}

// columnasMovimiento y scanMovimiento van juntas: m es la tabla de
// movimientos y p la de productos (para el nombre).
const columnasMovimiento = `m.id, m.producto_id, p.nombre, m.tipo, m.cantidad, m.motivo, m.es_venta, m.precio_venta, m.fecha, m.creado_en,
	COALESCE(m.anula_a, 0),
	COALESCE((SELECT a.id FROM movimientos_inventario a WHERE a.anula_a = m.id), 0),
	COALESCE(m.pedido_id, 0)`

func scanMovimiento(row interface{ Scan(dest ...any) error }) (Movimiento, error) {
	var mv Movimiento
	err := row.Scan(&mv.ID, &mv.ProductoID, &mv.ProductoNombre, &mv.Tipo, &mv.Cantidad, &mv.Motivo, &mv.EsVenta, &mv.PrecioVenta, &mv.Fecha, &mv.CreadoEn,
		&mv.AnulaA, &mv.AnuladoPor, &mv.PedidoID)
	return mv, err
}

var ErrStockInsuficiente = errors.New("stock insuficiente para ese consumo")

// querier lo satisfacen tanto *sql.DB como *sql.Tx, para poder reusar
// StockActual dentro de una transacción más grande (ej. al crear un pedido
// desde la tienda pública) sin duplicar la lógica.
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

func ListMovimientos(conn *sql.DB, limit int) ([]Movimiento, error) {
	rows, err := conn.Query(
		`SELECT `+columnasMovimiento+`
		 FROM movimientos_inventario m
		 JOIN productos p ON p.id = m.producto_id
		 ORDER BY m.fecha DESC, m.creado_en DESC, m.id DESC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	return recogerMovimientos(rows)
}

func recogerMovimientos(rows *sql.Rows) ([]Movimiento, error) {
	defer rows.Close()
	var out []Movimiento
	for rows.Next() {
		mv, err := scanMovimiento(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, mv)
	}
	return out, rows.Err()
}

// ListMovimientosProducto devuelve el historial de un solo producto en
// orden cronológico ascendente, para poder calcular el saldo acumulado.
func ListMovimientosProducto(conn *sql.DB, productoID int) ([]Movimiento, error) {
	rows, err := conn.Query(
		`SELECT `+columnasMovimiento+`
		 FROM movimientos_inventario m
		 JOIN productos p ON p.id = m.producto_id
		 WHERE m.producto_id = $1
		 ORDER BY m.fecha ASC, m.creado_en ASC, m.id ASC`,
		productoID,
	)
	if err != nil {
		return nil, err
	}
	return recogerMovimientos(rows)
}

// ListMovimientosReporte devuelve los movimientos entre desde y hasta
// (ambos incluidos), filtrados por tipo. Si soloVentas es true, ignora
// incluirIngresos/incluirEgresos y devuelve únicamente consumos marcados
// como venta que no hayan sido anulados.
func ListMovimientosReporte(conn *sql.DB, desde, hasta time.Time, incluirIngresos, incluirEgresos, soloVentas bool) ([]Movimiento, error) {
	query := `
		SELECT ` + columnasMovimiento + `
		FROM movimientos_inventario m
		JOIN productos p ON p.id = m.producto_id
		WHERE m.fecha BETWEEN $1 AND $2
	`
	args := []any{desde, hasta}

	if soloVentas {
		query += ` AND m.tipo = 'consumo' AND m.es_venta = true
			AND NOT EXISTS (SELECT 1 FROM movimientos_inventario a WHERE a.anula_a = m.id)`
	} else {
		var tipos []string
		if incluirIngresos {
			tipos = append(tipos, "ingreso")
		}
		if incluirEgresos {
			tipos = append(tipos, "consumo")
		}
		if len(tipos) == 0 {
			return nil, nil
		}
		query += ` AND m.tipo = ANY($3)`
		args = append(args, pq.Array(tipos))
	}
	query += ` ORDER BY m.fecha ASC, m.creado_en ASC, m.id ASC`

	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return recogerMovimientos(rows)
}

func StockActual(conn querier, productoID int) (float64, error) {
	var stock float64
	err := conn.QueryRow(
		`SELECT COALESCE(SUM(CASE WHEN tipo = 'ingreso' THEN cantidad ELSE -cantidad END), 0)
		 FROM movimientos_inventario WHERE producto_id = $1`,
		productoID,
	).Scan(&stock)
	return stock, err
}

// BloquearProductos toma un lock de fila (SELECT ... FOR UPDATE) sobre los
// productos indicados hasta que termine la transacción. Serializa a quienes
// consumen stock del mismo producto: sin esto, dos transacciones pueden leer
// el mismo stock y ambas pasar la validación (READ COMMITTED). Los ids se
// bloquean siempre en orden ascendente para que dos transacciones con los
// mismos productos en distinto orden no queden en deadlock.
func BloquearProductos(tx *sql.Tx, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.Exec(`SELECT id FROM productos WHERE id = ANY($1) ORDER BY id FOR UPDATE`, pq.Array(ids))
	return err
}

// CreateMovimiento registra un ingreso o consumo con la fecha de negocio
// indicada (permite registrar hoy con retraso, o cargar movimientos de
// días anteriores). esVenta y precioVenta solo se guardan para consumos
// (un ingreso nunca es una venta); si esVenta es false, precioVenta se
// descarta también. La fecha y hora de registro ("creado_en") las pone la
// base de datos automáticamente y no se pueden elegir. Para un consumo
// valida primero que no deje el stock en negativo. Abre su propia
// transacción; para incluirlo en una más grande usar CreateMovimientoTx.
func CreateMovimiento(conn *sql.DB, productoID int, tipo string, cantidad float64, motivo string, esVenta bool, precioVenta float64, fecha time.Time) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := CreateMovimientoTx(tx, productoID, tipo, cantidad, motivo, esVenta, precioVenta, fecha); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateMovimientoTx es CreateMovimiento dentro de una transacción ya
// abierta. Para un consumo bloquea primero la fila del producto, de modo que
// la lectura del stock y el INSERT no se intercalen con otro consumo
// concurrente del mismo producto.
func CreateMovimientoTx(tx *sql.Tx, productoID int, tipo string, cantidad float64, motivo string, esVenta bool, precioVenta float64, fecha time.Time) error {
	return CreateMovimientoPedidoTx(tx, 0, productoID, tipo, cantidad, motivo, esVenta, precioVenta, fecha)
}

// CreateMovimientoPedidoTx es CreateMovimientoTx para un movimiento que nace
// de un pedido de la tienda: guarda pedidoID (0 = movimiento manual, queda
// NULL) para que los reportes distingan ventas de tienda y manuales.
func CreateMovimientoPedidoTx(tx *sql.Tx, pedidoID, productoID int, tipo string, cantidad float64, motivo string, esVenta bool, precioVenta float64, fecha time.Time) error {
	if tipo == "consumo" {
		if err := BloquearProductos(tx, []int{productoID}); err != nil {
			return err
		}
		stock, err := StockActual(tx, productoID)
		if err != nil {
			return err
		}
		if cantidad > stock {
			return ErrStockInsuficiente
		}
	} else {
		esVenta = false
	}
	if !esVenta {
		precioVenta = 0
	}
	_, err := tx.Exec(
		`INSERT INTO movimientos_inventario (producto_id, tipo, cantidad, motivo, es_venta, precio_venta, fecha, pedido_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, 0))`,
		productoID, tipo, cantidad, motivo, esVenta, precioVenta, fecha, pedidoID,
	)
	return err
}

var (
	ErrMovimientoNoExiste = errors.New("el movimiento no existe")
	ErrMovimientoAnulado  = errors.New("ese movimiento ya fue anulado")
	ErrEsAnulacion        = errors.New("una anulación no se puede anular; registra un movimiento nuevo")
	ErrMovimientoDePedido = errors.New("ese movimiento viene de un pedido de la tienda; se corrige cancelando el pedido")
)

// AnularMovimiento corrige un movimiento manual equivocado SIN borrarlo ni
// editarlo: registra el movimiento contrario (un ingreso se anula con un
// consumo y viceversa) por la misma cantidad, apuntando al original. El
// historial queda completo, el stock se corrige solo (se calcula sumando
// movimientos) y los reportes de ventas ignoran las ventas anuladas.
//
// Para cambiar un dato (por ejemplo 11 en vez de 1) se anula y se registra uno
// nuevo. Anular un ingreso que ya se consumió en parte falla con
// ErrStockInsuficiente, igual que cualquier consumo que dejara stock negativo.
// fecha es la fecha de negocio de la anulación (normalmente hoy).
func AnularMovimiento(conn *sql.DB, id int, fecha time.Time) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var productoID, pedidoID, anulaA int
	var tipo string
	var cantidad float64
	err = tx.QueryRow(
		`SELECT producto_id, tipo, cantidad, COALESCE(pedido_id, 0), COALESCE(anula_a, 0)
		 FROM movimientos_inventario WHERE id = $1`, id,
	).Scan(&productoID, &tipo, &cantidad, &pedidoID, &anulaA)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMovimientoNoExiste
	}
	if err != nil {
		return err
	}
	switch {
	case pedidoID != 0:
		return ErrMovimientoDePedido
	case anulaA != 0:
		return ErrEsAnulacion
	}

	// Serializa con cualquier otro movimiento del mismo producto: la lectura
	// del stock y el INSERT no se intercalan con un consumo concurrente.
	if err := BloquearProductos(tx, []int{productoID}); err != nil {
		return err
	}

	// Con el producto bloqueado, nadie más puede estar anulando este
	// movimiento: si ya lo anularon, se dice así (y no «stock insuficiente»,
	// que sería engañoso para quien hace doble clic). El índice único sigue
	// siendo la última barrera.
	var yaAnulado bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM movimientos_inventario WHERE anula_a = $1)`, id).Scan(&yaAnulado); err != nil {
		return err
	}
	if yaAnulado {
		return ErrMovimientoAnulado
	}

	inverso := "consumo"
	if tipo == "consumo" {
		inverso = "ingreso"
	} else {
		stock, err := StockActual(tx, productoID)
		if err != nil {
			return err
		}
		if cantidad > stock {
			return ErrStockInsuficiente
		}
	}

	_, err = tx.Exec(
		`INSERT INTO movimientos_inventario (producto_id, tipo, cantidad, motivo, es_venta, precio_venta, fecha, anula_a)
		 VALUES ($1, $2, $3, $4, false, 0, $5, $6)`,
		productoID, inverso, cantidad, fmt.Sprintf("Anulación del movimiento #%d", id), fecha, id,
	)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" { // índice único: ya estaba anulado
		return ErrMovimientoAnulado
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
