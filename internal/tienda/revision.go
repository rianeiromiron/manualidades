package tienda

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"manualidades/internal/inventario"
)

var (
	// ErrPedidoNoRevisable: el pedido no es de transferencia o ya no está en
	// revisión (otra persona lo aprobó o rechazó antes).
	ErrPedidoNoRevisable = errors.New("este pedido ya no está esperando revisión")
	// ErrComprobanteNoRevisable: el comprobante no es de este pedido o ya se
	// revisó.
	ErrComprobanteNoRevisable = errors.New("este comprobante ya no se puede revisar")
)

// truncar corta a n caracteres (no bytes) sin partir uno multibyte.
func truncar(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// bloquearParaRevision deja bloqueados los productos y el pedido, y el
// comprobante, y comprueba que siguen en revisión. El orden de bloqueo es el de
// todo el paquete: productos (por id) y después el pedido.
func bloquearParaRevision(tx *sql.Tx, pedidoID, comprobanteID int) (c Comprobante, total float64, lineas []LineaPedido, err error) {
	lineas, err = lineasDePedido(tx, pedidoID)
	if err != nil {
		return c, 0, nil, err
	}
	ids := make([]int, 0, len(lineas))
	for _, l := range lineas {
		ids = append(ids, l.ProductoID)
	}
	if err := inventario.BloquearProductos(tx, ids); err != nil {
		return c, 0, nil, err
	}

	var estado, metodo string
	err = tx.QueryRow(`SELECT estado, metodo_pago, total FROM pedidos WHERE id = $1 FOR UPDATE`, pedidoID).Scan(&estado, &metodo, &total)
	if errors.Is(err, sql.ErrNoRows) {
		return c, 0, nil, ErrPedidoNoExiste
	}
	if err != nil {
		return c, 0, nil, err
	}
	if metodo != MetodoPagoTransferencia || estado != EstadoTransferenciaPorRevisar {
		return c, 0, nil, ErrPedidoNoRevisable
	}

	err = tx.QueryRow(
		`SELECT id, numero_boleta, banco_origen, estado FROM comprobantes WHERE id = $1 AND pedido_id = $2 FOR UPDATE`,
		comprobanteID, pedidoID,
	).Scan(&c.ID, &c.NumeroBoleta, &c.BancoOrigen, &c.Estado)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && c.Estado != ComprobantePorRevisar) {
		return c, 0, nil, ErrComprobanteNoRevisable
	}
	return c, total, lineas, err
}

// AprobarTransferencia confirma que el depósito llegó: en una sola transacción
// descuenta el inventario (kardex), registra el pago como aprobado, marca el
// comprobante como aceptado (con quién y cuándo) y pasa el pedido a pagado.
//
// Si el stock físico ya no alcanza (por ejemplo, se consumió a mano mientras
// tanto), devuelve un error que envuelve a inventario.ErrStockInsuficiente y no
// cambia nada: el pedido sigue en revisión para que el negocio decida.
func AprobarTransferencia(conn *sql.DB, pedidoID, comprobanteID int, revisor string, ahora time.Time) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	c, total, lineas, err := bloquearParaRevision(tx, pedidoID, comprobanteID)
	if err != nil {
		return err
	}

	motivo := fmt.Sprintf("Venta online #%d (transferencia)", pedidoID)
	for _, l := range lineas {
		if err := inventario.CreateMovimientoPedidoTx(tx, pedidoID, l.ProductoID, "consumo", l.Cantidad, motivo, true, l.PrecioUnitario, ahora); err != nil {
			if errors.Is(err, inventario.ErrStockInsuficiente) {
				return fmt.Errorf("%s: %w", l.Nombre, inventario.ErrStockInsuficiente)
			}
			return err
		}
	}

	referencia := truncar(c.BancoOrigen+" "+c.NumeroBoleta, 60)
	if _, err := tx.Exec(
		`INSERT INTO pagos (pedido_id, metodo, monto, estado, referencia, motivo_rechazo) VALUES ($1, $2, $3, $4, $5, $6)`,
		pedidoID, MetodoPagoTransferencia, total, EstadoPagoAprobado, referencia, truncar("Confirmado por "+revisor, 255),
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE comprobantes SET estado = $1, revisado_por = $2, revisado_en = now() WHERE id = $3`,
		ComprobanteAceptado, truncar(revisor, 50), comprobanteID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE pedidos SET estado = $1, expira_en = NULL WHERE id = $2`, EstadoPagado, pedidoID); err != nil {
		return err
	}
	return tx.Commit()
}

// RechazarTransferencia rechaza el comprobante, con un motivo que verá el
// cliente. Con cancelar=false el pedido vuelve a esperar la transferencia con un
// plazo nuevo de TTLTransferencia, para que suba otro comprobante. Con
// cancelar=true el pedido se cancela y el stock vuelve a estar disponible.
func RechazarTransferencia(conn *sql.DB, pedidoID, comprobanteID int, revisor, motivo string, cancelar bool) error {
	motivo = strings.TrimSpace(motivo)
	if n := utf8.RuneCountInString(motivo); n < 3 || n > 255 {
		return invalido("Escribe el motivo del rechazo (entre 3 y 255 caracteres): el cliente lo verá.")
	}
	for _, r := range motivo {
		if unicode.IsControl(r) {
			return invalido("El motivo no puede contener saltos de línea.")
		}
	}

	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, _, _, err := bloquearParaRevision(tx, pedidoID, comprobanteID); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE comprobantes SET estado = $1, nota = $2, revisado_por = $3, revisado_en = now() WHERE id = $4`,
		ComprobanteRechazado, motivo, truncar(revisor, 50), comprobanteID,
	); err != nil {
		return err
	}
	if cancelar {
		_, err = tx.Exec(`UPDATE pedidos SET estado = $1, expira_en = NULL WHERE id = $2`, EstadoExpirado, pedidoID)
	} else {
		_, err = tx.Exec(`UPDATE pedidos SET estado = $1, expira_en = `+sqlExpiraTransferencia+` WHERE id = $2`, EstadoEsperaTransferencia, pedidoID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
