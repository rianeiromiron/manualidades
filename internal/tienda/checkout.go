package tienda

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"manualidades/internal/inventario"
)

// Flujo de checkout con reserva de stock ANTES de cobrar:
//
//  1. IniciarPago    (transacción corta) bloquea los productos, valida el
//     stock disponible (físico − reservas vigentes de otros pedidos), crea o
//     reutiliza el pedido en estado pagando y deja el intento de pago en
//     "iniciado". El stock queda reservado, pero el kardex no se toca.
//  2. pasarela.Cobrar (fuera de cualquier transacción: una transacción no
//     debe quedar abierta esperando a un tercero).
//  3. Según el desenlace:
//     - aprobado  → ConfirmarPago (transacción corta): escribe el kardex,
//     marca el pago aprobado y el pedido pagado.
//     - rechazado / falla → RegistrarCobroFallido: el pedido vuelve a
//     pendiente_pago conservando la reserva, para que el cliente reintente.
//     - aprobado pero no se pudo confirmar → MarcarPorConciliar: queda
//     constancia del cobro para reembolsarlo o completarlo a mano.
//
// Orden de bloqueo en TODAS las transacciones de este archivo: primero las
// filas de productos (en orden de id), después la fila del pedido. Un orden
// único evita deadlocks.

const (
	// TTLReserva es cuánto dura la reserva de un pedido pendiente_pago: el
	// tiempo que tiene el cliente para corregir la tarjeta y reintentar.
	TTLReserva = 20 * time.Minute
	// TTLPagando es cuánto dura la reserva mientras se está cobrando. Es muy
	// superior al timeout de la pasarela (10 s): si vence, es porque el
	// proceso murió a mitad del cobro.
	TTLPagando = 5 * time.Minute
)

// El vencimiento se calcula con el reloj de la base de datos (now()), el
// mismo que usan las consultas de reservas vigentes, para no depender de que
// el reloj del servidor web esté sincronizado con el de Postgres.
var (
	sqlExpiraReserva = fmt.Sprintf("now() + interval '%d seconds'", int(TTLReserva.Seconds()))
	sqlExpiraPagando = fmt.Sprintf("now() + interval '%d seconds'", int(TTLPagando.Seconds()))
)

// sqlReservaVigente filtra (sobre un alias pe de pedidos) los pedidos que
// hoy retienen stock.
const sqlReservaVigente = `pe.estado IN ('pendiente_pago', 'pagando') AND pe.expira_en > now()`

var (
	// ErrPedidoEnProceso: ya hay un cobro en curso para el pedido de este
	// navegador (doble clic, o dos pestañas). No se intenta cobrar dos veces.
	ErrPedidoEnProceso = errors.New("ya hay un pago en proceso para este pedido")
	// ErrPedidoNoPagable: al confirmar, el pedido ya no estaba en estado
	// pagando (por ejemplo, la limpieza lo expiró por haber tardado demasiado).
	ErrPedidoNoPagable = errors.New("el pedido ya no estaba esperando el cobro")
)

// Intento identifica un cobro en curso: el pedido que reserva el stock, el
// registro de pago asociado, y el token que el navegador debe recordar (en
// una cookie) para reencontrar el pedido si el pago falla.
type Intento struct {
	PedidoID int
	PagoID   int
	Token    string
}

// ReservadoPorProducto devuelve, por producto, cuánto stock retienen hoy los
// pedidos pendiente_pago / pagando vigentes. Sirve para mostrar en el
// catálogo el stock disponible real (físico − reservado).
func ReservadoPorProducto(conn *sql.DB) (map[int]float64, error) {
	rows, err := conn.Query(
		`SELECT pi.producto_id, SUM(pi.cantidad)
		 FROM pedido_items pi JOIN pedidos pe ON pe.id = pi.pedido_id
		 WHERE ` + sqlReservaVigente + `
		 GROUP BY pi.producto_id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int]float64{}
	for rows.Next() {
		var id int
		var n float64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// reservadoPorOtros es el stock de un producto retenido por reservas
// vigentes de pedidos distintos de excluirPedidoID (0 = no excluir ninguno).
func reservadoPorOtros(q querier, productoID, excluirPedidoID int) (float64, error) {
	var n float64
	err := q.QueryRow(
		`SELECT COALESCE(SUM(pi.cantidad), 0)
		 FROM pedido_items pi JOIN pedidos pe ON pe.id = pi.pedido_id
		 WHERE pi.producto_id = $1 AND pe.id <> $2 AND `+sqlReservaVigente,
		productoID, excluirPedidoID,
	).Scan(&n)
	return n, err
}

// pedidoReutilizable busca el pedido que el navegador recuerda por su token
// y lo deja bloqueado. Devuelve su id si se puede reutilizar (pendiente_pago:
// un intento anterior falló), 0 si no hay ninguno (o ya no sirve: pagado,
// expirado), y ErrPedidoEnProceso si otro cobro del mismo pedido sigue vivo.
func pedidoReutilizable(tx *sql.Tx, token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	var id int
	var estado string
	var vigente bool
	err := tx.QueryRow(
		`SELECT id, estado, COALESCE(expira_en > now(), false) FROM pedidos WHERE token = $1 FOR UPDATE`,
		token,
	).Scan(&id, &estado, &vigente)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	switch {
	case estado == EstadoPendientePago:
		return id, nil
	case estado == EstadoPagando && vigente:
		return 0, ErrPedidoEnProceso
	default:
		return 0, nil
	}
}

// IniciarPago es el paso 1 del checkout. token es el que el navegador
// recordaba de un intento anterior ("" si no hay). lineas/total deben venir
// de CotizarCarrito. Si no alcanza el stock devuelve un error que envuelve a
// inventario.ErrStockInsuficiente y NO queda nada guardado.
//
// Si el token corresponde a un pedido pendiente_pago (el cliente reintenta
// tras un rechazo), se reutiliza ese mismo pedido: se reemplazan sus líneas y
// datos por los actuales y se reinicia el cobro, sin crear otro pedido.
func IniciarPago(conn *sql.DB, token string, datos DatosPedido, lineas []LineaPedido, total float64) (Intento, error) {
	if len(lineas) == 0 {
		return Intento{}, ErrCarritoVacio
	}

	tx, err := conn.Begin()
	if err != nil {
		return Intento{}, err
	}
	defer tx.Rollback()

	ids := make([]int, 0, len(lineas))
	for _, l := range lineas {
		ids = append(ids, l.ProductoID)
	}
	if err := inventario.BloquearProductos(tx, ids); err != nil {
		return Intento{}, err
	}

	pedidoID, err := pedidoReutilizable(tx, token)
	if err != nil {
		return Intento{}, err
	}

	// Stock disponible = físico − lo que reservan otros pedidos. Se excluye
	// el propio pedido si se reutiliza: su reserva anterior está por
	// reemplazarse por la nueva.
	for _, l := range lineas {
		fisico, err := inventario.StockActual(tx, l.ProductoID)
		if err != nil {
			return Intento{}, err
		}
		reservado, err := reservadoPorOtros(tx, l.ProductoID, pedidoID)
		if err != nil {
			return Intento{}, err
		}
		if l.Cantidad > fisico-reservado {
			return Intento{}, fmt.Errorf("%s: %w", l.Nombre, inventario.ErrStockInsuficiente)
		}
	}

	nit := datos.ClienteNIT
	if nit == "" {
		nit = "CF"
	}

	if pedidoID == 0 {
		token = uuid.NewString()
		err = tx.QueryRow(
			`INSERT INTO pedidos
				(cliente_nombre, cliente_telefono, cliente_email, cliente_nit,
				 metodo_entrega, direccion_entrega, notas, total, estado, expira_en, token)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, `+sqlExpiraPagando+`, $10)
			 RETURNING id`,
			datos.ClienteNombre, datos.ClienteTelefono, datos.ClienteEmail, nit,
			datos.MetodoEntrega, datos.DireccionEntrega, datos.Notas, total, EstadoPagando, token,
		).Scan(&pedidoID)
		if err != nil {
			return Intento{}, err
		}
	} else {
		if _, err := tx.Exec(
			`UPDATE pedidos SET cliente_nombre = $1, cliente_telefono = $2, cliente_email = $3, cliente_nit = $4,
				metodo_entrega = $5, direccion_entrega = $6, notas = $7, total = $8,
				estado = $9, expira_en = `+sqlExpiraPagando+`
			 WHERE id = $10`,
			datos.ClienteNombre, datos.ClienteTelefono, datos.ClienteEmail, nit,
			datos.MetodoEntrega, datos.DireccionEntrega, datos.Notas, total, EstadoPagando, pedidoID,
		); err != nil {
			return Intento{}, err
		}
		if _, err := tx.Exec(`DELETE FROM pedido_items WHERE pedido_id = $1`, pedidoID); err != nil {
			return Intento{}, err
		}
	}

	for _, l := range lineas {
		if _, err := tx.Exec(
			`INSERT INTO pedido_items (pedido_id, producto_id, nombre_producto, cantidad, precio_unitario, subtotal)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			pedidoID, l.ProductoID, l.Nombre, l.Cantidad, l.PrecioUnitario, l.Subtotal,
		); err != nil {
			return Intento{}, err
		}
	}

	pagoID, err := iniciarPago(tx, pedidoID, MetodoTarjeta, total)
	if err != nil {
		return Intento{}, err
	}

	if err := tx.Commit(); err != nil {
		return Intento{}, err
	}
	return Intento{PedidoID: pedidoID, PagoID: pagoID, Token: token}, nil
}

// ConfirmarPago es el paso 3 cuando la pasarela aprobó el cobro: en una sola
// transacción escribe un consumo por producto en el kardex, marca el pago
// como aprobado y el pedido como pagado. Si algo falla, hace rollback
// completo y devuelve el error (ErrPedidoNoPagable, o uno que envuelve a
// inventario.ErrStockInsuficiente si el stock físico ya no alcanza); el
// llamador debe entonces usar MarcarPorConciliar.
func ConfirmarPago(conn *sql.DB, in Intento, pago ResultadoPago) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Las líneas de un pedido pendiente no cambian mientras esté pagando,
	// así que se pueden leer antes de bloquear.
	lineas, err := lineasDePedido(tx, in.PedidoID)
	if err != nil {
		return err
	}
	ids := make([]int, 0, len(lineas))
	for _, l := range lineas {
		ids = append(ids, l.ProductoID)
	}
	if err := inventario.BloquearProductos(tx, ids); err != nil {
		return err
	}

	var estado string
	if err := tx.QueryRow(`SELECT estado FROM pedidos WHERE id = $1 FOR UPDATE`, in.PedidoID).Scan(&estado); err != nil {
		return err
	}
	if estado != EstadoPagando {
		return ErrPedidoNoPagable
	}

	ahora := time.Now()
	motivo := fmt.Sprintf("Venta online #%d", in.PedidoID)
	for _, l := range lineas {
		if err := inventario.CreateMovimientoTx(tx, l.ProductoID, "consumo", l.Cantidad, motivo, true, l.PrecioUnitario, ahora); err != nil {
			if errors.Is(err, inventario.ErrStockInsuficiente) {
				return fmt.Errorf("%s: %w", l.Nombre, inventario.ErrStockInsuficiente)
			}
			return err
		}
	}

	if err := actualizarPago(tx, in.PagoID, EstadoPagoAprobado, pago.Referencia, pago.Marca, pago.Ultimos4, ""); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE pedidos SET estado = $1, expira_en = NULL WHERE id = $2`, EstadoPagado, in.PedidoID); err != nil {
		return err
	}
	return tx.Commit()
}

func lineasDePedido(q querier, pedidoID int) ([]LineaPedido, error) {
	rows, err := q.Query(
		`SELECT producto_id, nombre_producto, cantidad, precio_unitario, subtotal
		 FROM pedido_items WHERE pedido_id = $1 ORDER BY id`,
		pedidoID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LineaPedido
	for rows.Next() {
		var l LineaPedido
		if err := rows.Scan(&l.ProductoID, &l.Nombre, &l.Cantidad, &l.PrecioUnitario, &l.Subtotal); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RegistrarCobroFallido es el paso 3 cuando la pasarela rechazó el cobro
// (estado EstadoPagoRechazado) o no respondió (EstadoPagoFallo): guarda el
// desenlace del intento y devuelve el pedido a pendiente_pago con la reserva
// renovada, para que el cliente pueda reintentar con otra tarjeta.
func RegistrarCobroFallido(conn *sql.DB, in Intento, estado, referencia, motivo string) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := actualizarPago(tx, in.PagoID, estado, referencia, "", "", motivo); err != nil {
		return err
	}
	// Solo si sigue en pagando: si la limpieza ya lo expiró, no se revive.
	if _, err := tx.Exec(
		`UPDATE pedidos SET estado = $1, expira_en = `+sqlExpiraReserva+` WHERE id = $2 AND estado = $3`,
		EstadoPendientePago, in.PedidoID, EstadoPagando,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// MarcarPorConciliar es el último recurso cuando la pasarela aprobó el cobro
// pero ConfirmarPago no pudo completarse (típicamente porque el stock físico
// bajó por un consumo manual del admin). Deja el pago como por_conciliar con
// los datos reales del cobro y el motivo, y libera la reserva (el pedido pasa
// a expirado). El negocio decide después: reembolsar, o completar el pedido
// a mano.
func MarcarPorConciliar(conn *sql.DB, in Intento, pago ResultadoPago, motivo string) error {
	if len(motivo) > 255 {
		motivo = motivo[:255]
	}
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := actualizarPago(tx, in.PagoID, EstadoPagoPorConciliar, pago.Referencia, pago.Marca, pago.Ultimos4, motivo); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE pedidos SET estado = $1, expira_en = NULL WHERE id = $2 AND estado IN ($3, $4)`,
		EstadoExpirado, in.PedidoID, EstadoPendientePago, EstadoPagando,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// LimpiarExpirados libera las reservas vencidas. No es necesario para que
// el stock disponible sea correcto (las consultas ya ignoran reservas
// vencidas); solo ordena el estado: los pedidos pendiente_pago vencidos pasan
// a expirado, y los que murieron a mitad del cobro (pagando vencido, con el
// intento aún en "iniciado") dejan su pago en por_conciliar para revisión
// manual, porque no se sabe si la pasarela llegó a cobrar. Devuelve cuántos
// pedidos expiró.
func LimpiarExpirados(conn *sql.DB) (int, error) {
	tx, err := conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`UPDATE pagos SET estado = $1, motivo_rechazo = $2
		 WHERE estado = $3
		   AND pedido_id IN (SELECT id FROM pedidos WHERE estado = $4 AND expira_en < now())`,
		EstadoPagoPorConciliar,
		"El proceso se interrumpió durante el cobro: verifica en la pasarela si se cobró.",
		EstadoPagoIniciado, EstadoPagando,
	); err != nil {
		return 0, err
	}

	res, err := tx.Exec(
		`UPDATE pedidos SET estado = $1, expira_en = NULL
		 WHERE estado IN ($2, $3) AND expira_en < now()`,
		EstadoExpirado, EstadoPendientePago, EstadoPagando,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}
