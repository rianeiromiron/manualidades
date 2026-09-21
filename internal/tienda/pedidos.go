package tienda

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"manualidades/internal/inventario"
)

type ItemCarrito struct {
	ProductoID int
	Cantidad   float64
}

// Estados posibles de un pedido. Siempre nace en EstadoPagado; el resto se
// maneja desde el admin (PedidosList/PedidoDetalle).
const (
	EstadoPagado     = "pagado"
	EstadoProcesando = "procesando" // "procesando en bodega"
	EstadoEntregado  = "entregado"
	EstadoCancelado  = "cancelado"
)

var EstadosValidos = []string{EstadoPagado, EstadoProcesando, EstadoEntregado, EstadoCancelado}

var ErrEstadoInvalido = errors.New("estado de pedido inválido")

func esEstadoValido(estado string) bool {
	for _, e := range EstadosValidos {
		if e == estado {
			return true
		}
	}
	return false
}

// DatosPedido es lo que llega desde el checkout público.
type DatosPedido struct {
	ClienteNombre    string
	ClienteTelefono  string
	ClienteEmail     string
	ClienteNIT       string
	MetodoEntrega    string // "recoger" | "domicilio"
	DireccionEntrega string
	Notas            string
	Items            []ItemCarrito
}

type PedidoItem struct {
	ID             int
	ProductoID     int
	NombreProducto string
	Cantidad       float64
	PrecioUnitario float64
	Subtotal       float64
}

type Pedido struct {
	ID               int
	ClienteNombre    string
	ClienteTelefono  string
	ClienteEmail     string
	ClienteNIT       string
	MetodoEntrega    string
	DireccionEntrega string
	Notas            string
	Estado           string
	Total            float64
	CreadoEn         time.Time
	Items            []PedidoItem
}

var ErrCarritoVacio = errors.New("el carrito está vacío")

// LineaPedido es una línea ya cotizada contra el catálogo actual (nunca se
// confía en precios que mande el navegador).
type LineaPedido struct {
	ProductoID     int
	Nombre         string
	Cantidad       float64
	PrecioUnitario float64
	Subtotal       float64
}

// CotizarCarrito calcula precios reales desde el catálogo y valida que haya
// stock suficiente para cada línea. Se usa ANTES de cobrar — no tiene
// sentido llamar a la pasarela de pago por algo que ya sabemos que no hay.
// El chequeo real y atómico de stock sigue pasando en CrearPedido (esto es
// solo una verificación anticipada para no cobrar en vano).
func CotizarCarrito(conn *sql.DB, items []ItemCarrito) ([]LineaPedido, float64, error) {
	var lineas []LineaPedido
	var total float64

	for _, item := range items {
		if item.Cantidad <= 0 {
			continue
		}
		var nombre string
		var precioVenta float64
		err := conn.QueryRow(
			`SELECT nombre, precio_venta FROM productos WHERE id = $1 AND activo = true`,
			item.ProductoID,
		).Scan(&nombre, &precioVenta)
		if err != nil {
			return nil, 0, fmt.Errorf("producto %d no disponible: %w", item.ProductoID, err)
		}

		stock, err := inventario.StockActual(conn, item.ProductoID)
		if err != nil {
			return nil, 0, err
		}
		if item.Cantidad > stock {
			return nil, 0, fmt.Errorf("%s: %w", nombre, inventario.ErrStockInsuficiente)
		}

		subtotal := item.Cantidad * precioVenta
		total += subtotal
		lineas = append(lineas, LineaPedido{item.ProductoID, nombre, item.Cantidad, precioVenta, subtotal})
	}

	if len(lineas) == 0 {
		return nil, 0, ErrCarritoVacio
	}
	return lineas, total, nil
}

// ResultadoPago es lo que devolvió la pasarela para un cobro ya aprobado.
type ResultadoPago struct {
	Referencia string
	Marca      string
	Ultimos4   string
}

// CrearPedido inserta el pedido, sus líneas y el pago aprobado, y registra
// un movimiento de consumo por producto en el kardex (reusando
// inventario.CreateMovimientoTx) — todo en una sola transacción: si algo
// falla a mitad de camino, no queda nada guardado. lineas/total deben venir
// de CotizarCarrito, y pago de un cobro ya aprobado por la pasarela — esta
// función no cobra nada, solo registra lo que ya se cobró.
func CrearPedido(conn *sql.DB, datos DatosPedido, lineas []LineaPedido, total float64, pago ResultadoPago) (int, error) {
	if len(lineas) == 0 {
		return 0, ErrCarritoVacio
	}

	tx, err := conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Bloquea todos los productos del pedido de una vez y en orden de id,
	// antes de validar stock: evita sobreventa entre pedidos concurrentes y
	// deadlocks entre pedidos que incluyen los mismos productos.
	ids := make([]int, 0, len(lineas))
	for _, l := range lineas {
		ids = append(ids, l.ProductoID)
	}
	if err := inventario.BloquearProductos(tx, ids); err != nil {
		return 0, err
	}

	nit := datos.ClienteNIT
	if nit == "" {
		nit = "CF"
	}

	var pedidoID int
	err = tx.QueryRow(
		`INSERT INTO pedidos
			(cliente_nombre, cliente_telefono, cliente_email, cliente_nit,
			 metodo_entrega, direccion_entrega, notas, total)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		datos.ClienteNombre, datos.ClienteTelefono, datos.ClienteEmail, nit,
		datos.MetodoEntrega, datos.DireccionEntrega, datos.Notas, total,
	).Scan(&pedidoID)
	if err != nil {
		return 0, err
	}

	ahora := time.Now()
	for _, l := range lineas {
		if _, err := tx.Exec(
			`INSERT INTO pedido_items (pedido_id, producto_id, nombre_producto, cantidad, precio_unitario, subtotal)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			pedidoID, l.ProductoID, l.Nombre, l.Cantidad, l.PrecioUnitario, l.Subtotal,
		); err != nil {
			return 0, err
		}

		motivo := fmt.Sprintf("Venta online #%d", pedidoID)
		if err := inventario.CreateMovimientoTx(tx, l.ProductoID, "consumo", l.Cantidad, motivo, true, l.PrecioUnitario, ahora); err != nil {
			if errors.Is(err, inventario.ErrStockInsuficiente) {
				return 0, fmt.Errorf("%s: %w", l.Nombre, inventario.ErrStockInsuficiente)
			}
			return 0, err
		}
	}

	if _, err := RegistrarPago(tx, &pedidoID, MetodoTarjeta, total, EstadoPagoAprobado, pago.Referencia, pago.Marca, pago.Ultimos4, ""); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return pedidoID, nil
}

func ListPedidos(conn *sql.DB) ([]Pedido, error) {
	rows, err := conn.Query(
		`SELECT id, cliente_nombre, cliente_telefono, cliente_email, cliente_nit,
			metodo_entrega, direccion_entrega, notas, estado, total, creado_en
		 FROM pedidos ORDER BY creado_en DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Pedido
	for rows.Next() {
		var p Pedido
		if err := rows.Scan(
			&p.ID, &p.ClienteNombre, &p.ClienteTelefono, &p.ClienteEmail, &p.ClienteNIT,
			&p.MetodoEntrega, &p.DireccionEntrega, &p.Notas, &p.Estado, &p.Total, &p.CreadoEn,
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func GetPedido(conn *sql.DB, id int) (Pedido, error) {
	var p Pedido
	err := conn.QueryRow(
		`SELECT id, cliente_nombre, cliente_telefono, cliente_email, cliente_nit,
			metodo_entrega, direccion_entrega, notas, estado, total, creado_en
		 FROM pedidos WHERE id = $1`,
		id,
	).Scan(
		&p.ID, &p.ClienteNombre, &p.ClienteTelefono, &p.ClienteEmail, &p.ClienteNIT,
		&p.MetodoEntrega, &p.DireccionEntrega, &p.Notas, &p.Estado, &p.Total, &p.CreadoEn,
	)
	if err != nil {
		return Pedido{}, err
	}

	rows, err := conn.Query(
		`SELECT id, producto_id, nombre_producto, cantidad, precio_unitario, subtotal
		 FROM pedido_items WHERE pedido_id = $1 ORDER BY id`,
		id,
	)
	if err != nil {
		return Pedido{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var it PedidoItem
		if err := rows.Scan(&it.ID, &it.ProductoID, &it.NombreProducto, &it.Cantidad, &it.PrecioUnitario, &it.Subtotal); err != nil {
			return Pedido{}, err
		}
		p.Items = append(p.Items, it)
	}
	return p, rows.Err()
}

// ActualizarEstado cambia el estado de un pedido (usado desde el admin).
// Un pedido siempre nace en EstadoPagado; de ahí en adelante el cambio de
// estado es manual.
func ActualizarEstado(conn *sql.DB, pedidoID int, nuevoEstado string) error {
	if !esEstadoValido(nuevoEstado) {
		return ErrEstadoInvalido
	}
	_, err := conn.Exec(`UPDATE pedidos SET estado = $1 WHERE id = $2`, nuevoEstado, pedidoID)
	return err
}
