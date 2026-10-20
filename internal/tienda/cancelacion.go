package tienda

import (
	"database/sql"
	"errors"
	"time"

	"manualidades/internal/inventario"
)

var (
	// ErrNoSePuedeCancelar: solo se cancela un pedido ya pagado (pagado,
	// procesando o entregado). Uno sin pagar se libera solo al vencer, o se
	// rechaza desde la revisión de la transferencia.
	ErrNoSePuedeCancelar = errors.New("este pedido no está pagado, así que no se puede cancelar desde aquí")
	// ErrFaltaDecisionDeInventario: cancelar un pedido ya entregado exige decir si
	// el producto volvió a bodega.
	ErrFaltaDecisionDeInventario = errors.New("indica si el producto volvió a bodega")
	ErrSinReembolsoPendiente     = errors.New("este pedido no tiene un reembolso pendiente")
)

// Reembolso de un pago cancelado.
const (
	ReembolsoPendiente = "pendiente"
	ReembolsoHecho     = "hecho"
)

// ResultadoCancelacion dice qué pasó al cancelar.
type ResultadoCancelacion struct {
	InventarioDevuelto bool
	// ReembolsoPendiente es el monto aprobado que falta devolver al cliente (0 si
	// el pedido no tenía un pago aprobado).
	ReembolsoPendiente float64
}

// CancelarPedido cancela un pedido ya pagado, en una sola transacción:
//
//   - Pagado o procesando: sus productos siguen en bodega, así que se devuelven
//     al inventario siempre.
//   - Entregado: el producto salió; solo se devuelve si volvioABodega es true.
//   - El pago aprobado queda marcado con reembolso pendiente: devolver el dinero
//     ocurre fuera del sistema (banco o pasarela) y se confirma con
//     MarcarReembolsado.
//   - El pedido queda cancelado, con quién y cuándo, y es definitivo.
//
// Devolver el inventario anula los consumos que el pedido dejó en el kardex (el
// historial se conserva) y no se puede repetir.
//
// volvioABodega solo importa si el pedido estaba entregado; en ese caso el
// llamador debe haber preguntado (si no se sabe, ErrFaltaDecisionDeInventario).
func CancelarPedido(conn *sql.DB, pedidoID int, volvioABodega *bool, usuario string, ahora time.Time) (ResultadoCancelacion, error) {
	tx, err := conn.Begin()
	if err != nil {
		return ResultadoCancelacion{}, err
	}
	defer tx.Rollback()

	// Orden de bloqueo del paquete: productos y después el pedido.
	lineas, err := lineasDePedido(tx, pedidoID)
	if err != nil {
		return ResultadoCancelacion{}, err
	}
	ids := make([]int, 0, len(lineas))
	for _, l := range lineas {
		ids = append(ids, l.ProductoID)
	}
	if err := inventario.BloquearProductos(tx, ids); err != nil {
		return ResultadoCancelacion{}, err
	}

	var estado string
	err = tx.QueryRow(`SELECT estado FROM pedidos WHERE id = $1 FOR UPDATE`, pedidoID).Scan(&estado)
	if errors.Is(err, sql.ErrNoRows) {
		return ResultadoCancelacion{}, ErrPedidoNoExiste
	}
	if err != nil {
		return ResultadoCancelacion{}, err
	}
	switch estado {
	case EstadoCancelado:
		return ResultadoCancelacion{}, ErrPedidoCancelado
	case EstadoPagado, EstadoProcesando, EstadoEntregado:
	default:
		return ResultadoCancelacion{}, ErrNoSePuedeCancelar
	}

	devolver := true
	if estado == EstadoEntregado {
		if volvioABodega == nil {
			return ResultadoCancelacion{}, ErrFaltaDecisionDeInventario
		}
		devolver = *volvioABodega
	}
	if devolver {
		if _, err := inventario.AnularMovimientosDePedidoTx(tx, pedidoID, ahora); err != nil {
			return ResultadoCancelacion{}, err
		}
	}

	var pendiente float64
	if err := tx.QueryRow(
		`SELECT COALESCE(SUM(monto), 0) FROM pagos WHERE pedido_id = $1 AND estado = $2 AND reembolso = ''`,
		pedidoID, EstadoPagoAprobado,
	).Scan(&pendiente); err != nil {
		return ResultadoCancelacion{}, err
	}
	if _, err := tx.Exec(
		`UPDATE pagos SET reembolso = $1 WHERE pedido_id = $2 AND estado = $3 AND reembolso = ''`,
		ReembolsoPendiente, pedidoID, EstadoPagoAprobado,
	); err != nil {
		return ResultadoCancelacion{}, err
	}
	if _, err := tx.Exec(
		`UPDATE pedidos SET estado = $1, expira_en = NULL, cancelado_por = $2, cancelado_en = now(), inventario_devuelto = $3 WHERE id = $4`,
		EstadoCancelado, truncar(usuario, 50), devolver, pedidoID,
	); err != nil {
		return ResultadoCancelacion{}, err
	}
	if err := tx.Commit(); err != nil {
		return ResultadoCancelacion{}, err
	}
	return ResultadoCancelacion{InventarioDevuelto: devolver, ReembolsoPendiente: pendiente}, nil
}

// MarcarReembolsado anota que ya se devolvió el dinero de un pedido cancelado,
// con quién y cuándo. El reembolso en sí se hace fuera del sistema.
func MarcarReembolsado(conn *sql.DB, pedidoID int, usuario string) error {
	res, err := conn.Exec(
		`UPDATE pagos SET reembolso = $1, reembolso_por = $2, reembolso_en = now()
		 WHERE pedido_id = $3 AND estado = $4 AND reembolso = $5`,
		ReembolsoHecho, truncar(usuario, 50), pedidoID, EstadoPagoAprobado, ReembolsoPendiente,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSinReembolsoPendiente
	}
	return nil
}

// Cancelacion son los datos de un pedido cancelado que muestra el admin.
type Cancelacion struct {
	Por                string
	En                 *time.Time
	InventarioDevuelto bool
}

// GetCancelacion lee quién canceló un pedido, cuándo y si se devolvió el
// inventario. Los pedidos cancelados antes de existir esta función no tienen
// quién ni cuándo, y su inventario no se devolvió.
func GetCancelacion(conn *sql.DB, pedidoID int) (Cancelacion, error) {
	var c Cancelacion
	var en sql.NullTime
	err := conn.QueryRow(
		`SELECT cancelado_por, cancelado_en, inventario_devuelto FROM pedidos WHERE id = $1`, pedidoID,
	).Scan(&c.Por, &en, &c.InventarioDevuelto)
	if en.Valid {
		c.En = &en.Time
	}
	return c, err
}
