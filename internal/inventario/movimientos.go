package inventario

import (
	"database/sql"
	"errors"
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
		`SELECT m.id, m.producto_id, p.nombre, m.tipo, m.cantidad, m.motivo, m.es_venta, m.precio_venta, m.fecha, m.creado_en
		 FROM movimientos_inventario m
		 JOIN productos p ON p.id = m.producto_id
		 ORDER BY m.fecha DESC, m.creado_en DESC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Movimiento
	for rows.Next() {
		var mv Movimiento
		if err := rows.Scan(&mv.ID, &mv.ProductoID, &mv.ProductoNombre, &mv.Tipo, &mv.Cantidad, &mv.Motivo, &mv.EsVenta, &mv.PrecioVenta, &mv.Fecha, &mv.CreadoEn); err != nil {
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
		`SELECT id, producto_id, tipo, cantidad, motivo, es_venta, precio_venta, fecha, creado_en
		 FROM movimientos_inventario
		 WHERE producto_id = $1
		 ORDER BY fecha ASC, creado_en ASC`,
		productoID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Movimiento
	for rows.Next() {
		var mv Movimiento
		if err := rows.Scan(&mv.ID, &mv.ProductoID, &mv.Tipo, &mv.Cantidad, &mv.Motivo, &mv.EsVenta, &mv.PrecioVenta, &mv.Fecha, &mv.CreadoEn); err != nil {
			return nil, err
		}
		out = append(out, mv)
	}
	return out, rows.Err()
}

// ListMovimientosReporte devuelve los movimientos entre desde y hasta
// (ambos incluidos), filtrados por tipo. Si soloVentas es true, ignora
// incluirIngresos/incluirEgresos y devuelve únicamente consumos marcados
// como venta.
func ListMovimientosReporte(conn *sql.DB, desde, hasta time.Time, incluirIngresos, incluirEgresos, soloVentas bool) ([]Movimiento, error) {
	query := `
		SELECT m.id, m.producto_id, p.nombre, m.tipo, m.cantidad, m.motivo, m.es_venta, m.precio_venta, m.fecha, m.creado_en
		FROM movimientos_inventario m
		JOIN productos p ON p.id = m.producto_id
		WHERE m.fecha BETWEEN $1 AND $2
	`
	args := []any{desde, hasta}

	if soloVentas {
		query += ` AND m.tipo = 'consumo' AND m.es_venta = true`
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
	query += ` ORDER BY m.fecha ASC, m.creado_en ASC`

	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Movimiento
	for rows.Next() {
		var mv Movimiento
		if err := rows.Scan(&mv.ID, &mv.ProductoID, &mv.ProductoNombre, &mv.Tipo, &mv.Cantidad, &mv.Motivo, &mv.EsVenta, &mv.PrecioVenta, &mv.Fecha, &mv.CreadoEn); err != nil {
			return nil, err
		}
		out = append(out, mv)
	}
	return out, rows.Err()
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
	rows, err := tx.Query(`SELECT id FROM productos WHERE id = ANY($1) ORDER BY id FOR UPDATE`, pq.Array(ids))
	if err != nil {
		return err
	}
	return rows.Close()
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
		`INSERT INTO movimientos_inventario (producto_id, tipo, cantidad, motivo, es_venta, precio_venta, fecha) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		productoID, tipo, cantidad, motivo, esVenta, precioVenta, fecha,
	)
	return err
}
