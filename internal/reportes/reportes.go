// Package reportes reúne las consultas de solo lectura que alimentan la
// sección "Reportes" del admin. Una venta es un consumo de inventario con
// es_venta = true: ahí caen tanto los pedidos pagados de la tienda (que llevan
// pedido_id) como las ventas registradas a mano (pedido_id NULL), así que una
// sola fuente cubre ambos orígenes y nada se cuenta dos veces. Los pedidos
// cancelados no cuentan como venta aunque su consumo siga en el kardex.
package reportes

import (
	"database/sql"
	"fmt"
	"time"
)

// Orígenes de una venta, para filtrar.
const (
	OrigenTodos  = ""
	OrigenTienda = "tienda"
	OrigenManual = "manual"
)

// Agrupaciones válidas de ventas por período.
const (
	AgrupDia    = "day"
	AgrupSemana = "week"
	AgrupMes    = "month"
)

// Filtro son los criterios comunes de los reportes. Desde y Hasta son fechas
// (ambas incluidas); CategoriaID 0 significa todas.
type Filtro struct {
	Desde, Hasta time.Time
	CategoriaID  int
	Origen       string
}

// ventasDesde arma el FROM/WHERE común de las ventas y sus argumentos.
func (f Filtro) ventasDesde() (string, []any) {
	q := `
		FROM movimientos_inventario m
		JOIN productos p ON p.id = m.producto_id
		LEFT JOIN pedidos pe ON pe.id = m.pedido_id
		WHERE m.tipo = 'consumo' AND m.es_venta
		  AND m.fecha BETWEEN $1 AND $2
		  AND pe.estado IS DISTINCT FROM 'cancelado'
		  AND NOT EXISTS (SELECT 1 FROM movimientos_inventario a WHERE a.anula_a = m.id)`
	args := []any{f.Desde, f.Hasta}
	if f.CategoriaID > 0 {
		args = append(args, f.CategoriaID)
		q += fmt.Sprintf(" AND p.categoria_id = $%d", len(args))
	}
	switch f.Origen {
	case OrigenTienda:
		q += " AND m.pedido_id IS NOT NULL"
	case OrigenManual:
		q += " AND m.pedido_id IS NULL"
	}
	return q, args
}

// ---- Ventas por período ----

type VentaPeriodo struct {
	Periodo  time.Time
	Tienda   float64
	Manual   float64
	Unidades float64
	Pedidos  int // pedidos de la tienda con venta en el período
}

func (v VentaPeriodo) Total() float64 { return v.Tienda + v.Manual }

// InicioPeriodo devuelve el primer día del período (día, semana que empieza
// en lunes, o mes) que contiene a t, igual que date_trunc de Postgres.
func InicioPeriodo(t time.Time, agrup string) time.Time {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	switch agrup {
	case AgrupSemana:
		return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
	case AgrupMes:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return t
}

func siguientePeriodo(t time.Time, agrup string) time.Time {
	switch agrup {
	case AgrupSemana:
		return t.AddDate(0, 0, 7)
	case AgrupMes:
		return t.AddDate(0, 1, 0)
	}
	return t.AddDate(0, 0, 1)
}

// NumPeriodos cuenta cuántos períodos abarca el rango del filtro.
func NumPeriodos(f Filtro, agrup string) int {
	n := 0
	for p := InicioPeriodo(f.Desde, agrup); !p.After(f.Hasta); p = siguientePeriodo(p, agrup) {
		n++
	}
	return n
}

// VentasPorPeriodo devuelve una fila por cada período del rango, incluidos los
// que no tuvieron ventas (en cero), para que el gráfico no salte días.
func VentasPorPeriodo(conn *sql.DB, f Filtro, agrup string) ([]VentaPeriodo, error) {
	if agrup != AgrupDia && agrup != AgrupSemana && agrup != AgrupMes {
		agrup = AgrupDia
	}
	desde, args := f.ventasDesde()
	// agrup viene de la lista blanca de arriba, no del usuario.
	rows, err := conn.Query(`
		SELECT date_trunc('`+agrup+`', m.fecha)::date,
			COALESCE(SUM(CASE WHEN m.pedido_id IS NOT NULL THEN m.cantidad * m.precio_venta END), 0),
			COALESCE(SUM(CASE WHEN m.pedido_id IS NULL THEN m.cantidad * m.precio_venta END), 0),
			COALESCE(SUM(m.cantidad), 0),
			COUNT(DISTINCT m.pedido_id)`+desde+`
		GROUP BY 1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	porPeriodo := map[time.Time]VentaPeriodo{}
	for rows.Next() {
		var v VentaPeriodo
		if err := rows.Scan(&v.Periodo, &v.Tienda, &v.Manual, &v.Unidades, &v.Pedidos); err != nil {
			return nil, err
		}
		v.Periodo = InicioPeriodo(v.Periodo, AgrupDia)
		porPeriodo[v.Periodo] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []VentaPeriodo
	for p := InicioPeriodo(f.Desde, agrup); !p.After(f.Hasta); p = siguientePeriodo(p, agrup) {
		v, ok := porPeriodo[p]
		if !ok {
			v = VentaPeriodo{Periodo: p}
		}
		out = append(out, v)
	}
	return out, nil
}

// ---- Ventas y utilidad por producto ----

type ProductoVenta struct {
	ID        int
	Nombre    string
	Categoria string
	Unidades  float64
	Ingresos  float64
	Costo     float64 // unidades × precio de compra ACTUAL del producto
}

func (p ProductoVenta) Utilidad() float64 { return p.Ingresos - p.Costo }

// Margen es la utilidad como porcentaje de lo vendido (0 si no hubo ventas).
func (p ProductoVenta) Margen() float64 {
	if p.Ingresos == 0 {
		return 0
	}
	return p.Utilidad() / p.Ingresos * 100
}

// VentasPorProducto devuelve todos los productos activos con lo que vendieron
// en el rango, de mayor a menor ingreso; los que no vendieron salen al final
// con ceros.
func VentasPorProducto(conn *sql.DB, f Filtro) ([]ProductoVenta, error) {
	desde, args := f.ventasDesde()
	// La categoría ya se filtró dentro de la subconsulta.
	rows, err := conn.Query(`
		SELECT p.id, p.nombre, c.nombre,
			COALESCE(v.unidades, 0), COALESCE(v.ingresos, 0), COALESCE(v.unidades, 0) * p.precio_compra
		FROM productos p
		JOIN categorias c ON c.id = p.categoria_id
		LEFT JOIN (
			SELECT m.producto_id, SUM(m.cantidad) AS unidades, SUM(m.cantidad * m.precio_venta) AS ingresos`+desde+`
			GROUP BY m.producto_id
		) v ON v.producto_id = p.id
		WHERE (p.activo OR v.producto_id IS NOT NULL)`+f.categoriaProducto()+`
		ORDER BY COALESCE(v.ingresos, 0) DESC, p.nombre`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ProductoVenta
	for rows.Next() {
		var p ProductoVenta
		if err := rows.Scan(&p.ID, &p.Nombre, &p.Categoria, &p.Unidades, &p.Ingresos, &p.Costo); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// categoriaProducto repite el filtro de categoría sobre la tabla productos de
// la consulta externa (los argumentos ya los puso ventasDesde).
func (f Filtro) categoriaProducto() string {
	if f.CategoriaID > 0 {
		return fmt.Sprintf(" AND p.categoria_id = %d", f.CategoriaID)
	}
	return ""
}

// ---- Stock valorizado ----

type StockProducto struct {
	Nombre       string
	Categoria    string
	Stock        float64
	PrecioCompra float64
	PrecioVenta  float64
}

func (s StockProducto) ValorCosto() float64 { return s.Stock * s.PrecioCompra }
func (s StockProducto) ValorVenta() float64 { return s.Stock * s.PrecioVenta }

// StockValorizado devuelve la existencia actual de los productos activos
// (ingresos menos consumos), por categoría y nombre.
func StockValorizado(conn *sql.DB, categoriaID int) ([]StockProducto, error) {
	q := `
		SELECT p.nombre, c.nombre, COALESCE(m.stock, 0), p.precio_compra, p.precio_venta
		FROM productos p
		JOIN categorias c ON c.id = p.categoria_id
		LEFT JOIN (
			SELECT producto_id,
				SUM(CASE WHEN tipo = 'ingreso' THEN cantidad ELSE -cantidad END) AS stock
			FROM movimientos_inventario GROUP BY producto_id
		) m ON m.producto_id = p.id
		WHERE p.activo`
	var args []any
	if categoriaID > 0 {
		q += " AND p.categoria_id = $1"
		args = append(args, categoriaID)
	}
	rows, err := conn.Query(q+` ORDER BY c.nombre, p.nombre`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StockProducto
	for rows.Next() {
		var s StockProducto
		if err := rows.Scan(&s.Nombre, &s.Categoria, &s.Stock, &s.PrecioCompra, &s.PrecioVenta); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---- Pedidos ----

// rangoLocal convierte las fechas elegidas (desde y hasta, incluidas) en un
// intervalo [inicio, fin) de instantes a medianoche de la hora local, para no
// depender de la zona horaria con que Postgres convierta timestamptz a fecha.
func rangoLocal(desde, hasta time.Time) (inicio, fin time.Time) {
	inicio = time.Date(desde.Year(), desde.Month(), desde.Day(), 0, 0, 0, 0, time.Local)
	fin = time.Date(hasta.Year(), hasta.Month(), hasta.Day()+1, 0, 0, 0, 0, time.Local)
	return inicio, fin
}

type PedidosEstado struct {
	Estado  string
	Pedidos int
	Total   float64
}

// PedidosPorEstado cuenta los pedidos creados en el rango según su estado
// actual, incluidos los que nunca llegaron a pagarse.
func PedidosPorEstado(conn *sql.DB, desde, hasta time.Time) ([]PedidosEstado, error) {
	inicio, fin := rangoLocal(desde, hasta)
	rows, err := conn.Query(`
		SELECT estado, COUNT(*), COALESCE(SUM(total), 0)
		FROM pedidos
		WHERE creado_en >= $1 AND creado_en < $2
		GROUP BY estado`, inicio, fin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PedidosEstado
	for rows.Next() {
		var e PedidosEstado
		if err := rows.Scan(&e.Estado, &e.Pedidos, &e.Total); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type PedidosEntrega struct {
	Metodo  string
	Pedidos int
	Total   float64
}

// PedidosPorEntrega cuenta los pedidos ya pagados (y no cancelados) del rango
// según cómo se entregan.
func PedidosPorEntrega(conn *sql.DB, desde, hasta time.Time) ([]PedidosEntrega, error) {
	inicio, fin := rangoLocal(desde, hasta)
	rows, err := conn.Query(`
		SELECT metodo_entrega, COUNT(*), COALESCE(SUM(total), 0)
		FROM pedidos
		WHERE creado_en >= $1 AND creado_en < $2
		  AND estado IN ('pagado', 'procesando', 'entregado')
		GROUP BY metodo_entrega ORDER BY metodo_entrega`, inicio, fin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PedidosEntrega
	for rows.Next() {
		var e PedidosEntrega
		if err := rows.Scan(&e.Metodo, &e.Pedidos, &e.Total); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
