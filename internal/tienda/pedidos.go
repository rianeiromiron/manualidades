package tienda

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

type ItemCarrito struct {
	ProductoID int
	Cantidad   float64
}

// Estados de un pedido. Hay dos grupos:
//
// Ciclo de pago (los maneja el checkout, ver checkout.go). Mientras un
// pedido está en pendiente_pago o pagando, sus líneas RESERVAN stock (hasta
// expira_en) pero todavía no tocan el kardex:
//
//	pendiente_pago → pagando → pagado
//	      ↑              │
//	      └── (pago rechazado o con falla: el cliente puede reintentar)
//	pendiente_pago / pagando → expirado  (la reserva venció o se liberó)
//
// Logística (los maneja el admin, ver PedidosList/PedidoDetalle). Un pedido
// pagado entra al kardex y de ahí en adelante solo cambia a mano:
//
//	pagado → procesando → entregado  (o cancelado)
const (
	EstadoPendientePago = "pendiente_pago"
	EstadoPagando       = "pagando"
	EstadoExpirado      = "expirado"

	EstadoPagado     = "pagado"
	EstadoProcesando = "procesando" // "procesando en bodega"
	EstadoEntregado  = "entregado"
	EstadoCancelado  = "cancelado"
)

// EstadosValidos son los estados logísticos: los únicos que el admin puede
// asignar a mano, y los únicos que cuentan como "pedido real".
var EstadosValidos = []string{EstadoPagado, EstadoProcesando, EstadoEntregado, EstadoCancelado}

var (
	ErrEstadoInvalido = errors.New("estado de pedido inválido")
	// ErrEstadoNoModificable: el pedido sigue en el ciclo de pago (o
	// expiró), así que el admin no puede marcarlo como entregado, etc.
	ErrEstadoNoModificable = errors.New("el pedido no está pagado: su estado no se puede cambiar a mano")
)

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
	// ExpiraEn solo tiene valor mientras el pedido reserva stock
	// (pendiente_pago / pagando).
	ExpiraEn *time.Time
	Items    []PedidoItem
}

// EsReal indica si el pedido ya está pagado (o más allá), es decir, si es un
// pedido que bodega puede ver y cuyo estado logístico se puede cambiar.
func (p Pedido) EsReal() bool { return esEstadoValido(p.Estado) }

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

// CotizarCarrito calcula precios reales desde el catálogo (nunca se confía
// en lo que mande el navegador), junta líneas repetidas del mismo producto y
// descarta cantidades no positivas. NO valida stock: eso lo hace IniciarPago
// dentro de su transacción, con los productos bloqueados, que es la única
// verificación que no se puede intercalar con otro pedido.
func CotizarCarrito(conn *sql.DB, items []ItemCarrito) ([]LineaPedido, float64, error) {
	var lineas []LineaPedido
	var total float64
	posicion := map[int]int{} // producto_id -> índice en lineas

	for _, item := range items {
		if item.Cantidad <= 0 {
			continue
		}
		if i, ok := posicion[item.ProductoID]; ok {
			lineas[i].Cantidad += item.Cantidad
			lineas[i].Subtotal = lineas[i].Cantidad * lineas[i].PrecioUnitario
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

		posicion[item.ProductoID] = len(lineas)
		lineas = append(lineas, LineaPedido{item.ProductoID, nombre, item.Cantidad, precioVenta, item.Cantidad * precioVenta})
	}

	if len(lineas) == 0 {
		return nil, 0, ErrCarritoVacio
	}
	for _, l := range lineas {
		total += l.Subtotal
	}
	return lineas, total, nil
}

// ResultadoPago es lo que devolvió la pasarela para un cobro aprobado.
type ResultadoPago struct {
	Referencia string
	Marca      string
	Ultimos4   string
}

// ListPedidos devuelve solo pedidos reales (pagados en adelante). Los que
// siguen en el ciclo de pago o expiraron no aparecen: bodega no debe ver, y
// mucho menos despachar, algo que no está pagado. Ver ListPedidosPendientes.
func ListPedidos(conn *sql.DB) ([]Pedido, error) {
	return queryPedidos(conn,
		`SELECT id, cliente_nombre, cliente_telefono, cliente_email, cliente_nit,
			metodo_entrega, direccion_entrega, notas, estado, total, creado_en, expira_en
		 FROM pedidos WHERE estado = ANY($1) ORDER BY creado_en DESC`,
		pq.Array(EstadosValidos),
	)
}

// ListPedidosPendientes devuelve los pedidos que hoy están reservando stock
// mientras esperan (o intentan) el pago.
func ListPedidosPendientes(conn *sql.DB) ([]Pedido, error) {
	return queryPedidos(conn,
		`SELECT id, cliente_nombre, cliente_telefono, cliente_email, cliente_nit,
			metodo_entrega, direccion_entrega, notas, estado, total, creado_en, expira_en
		 FROM pedidos
		 WHERE estado IN ('pendiente_pago', 'pagando') AND expira_en > now()
		 ORDER BY creado_en DESC`,
	)
}

func queryPedidos(conn *sql.DB, query string, args ...any) ([]Pedido, error) {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Pedido
	for rows.Next() {
		p, err := scanPedido(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanPedido(row interface{ Scan(dest ...any) error }) (Pedido, error) {
	var p Pedido
	var expira sql.NullTime
	if err := row.Scan(
		&p.ID, &p.ClienteNombre, &p.ClienteTelefono, &p.ClienteEmail, &p.ClienteNIT,
		&p.MetodoEntrega, &p.DireccionEntrega, &p.Notas, &p.Estado, &p.Total, &p.CreadoEn, &expira,
	); err != nil {
		return Pedido{}, err
	}
	if expira.Valid {
		p.ExpiraEn = &expira.Time
	}
	return p, nil
}

func GetPedido(conn *sql.DB, id int) (Pedido, error) {
	p, err := scanPedido(conn.QueryRow(
		`SELECT id, cliente_nombre, cliente_telefono, cliente_email, cliente_nit,
			metodo_entrega, direccion_entrega, notas, estado, total, creado_en, expira_en
		 FROM pedidos WHERE id = $1`,
		id,
	))
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

// ActualizarEstado cambia el estado logístico de un pedido (usado desde el
// admin). Solo aplica a pedidos ya pagados en adelante: uno que sigue en el
// ciclo de pago o expiró no se puede "entregar" a mano.
func ActualizarEstado(conn *sql.DB, pedidoID int, nuevoEstado string) error {
	if !esEstadoValido(nuevoEstado) {
		return ErrEstadoInvalido
	}
	res, err := conn.Exec(
		`UPDATE pedidos SET estado = $1 WHERE id = $2 AND estado = ANY($3)`,
		nuevoEstado, pedidoID, pq.Array(EstadosValidos),
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrEstadoNoModificable
	}
	return nil
}
