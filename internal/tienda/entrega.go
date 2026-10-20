package tienda

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrPedidoNoModificable: el pedido ya no admite cambios del cliente (no es de
	// transferencia, ya venció, tiene un comprobante en revisión o ya se pagó).
	ErrPedidoNoModificable = errors.New("este pedido ya no admite cambios")
	// ErrEntregaInvalida envuelve mensajes seguros de mostrar al cliente.
	ErrEntregaInvalida = errors.New("entrega inválida")
)

// ResultadoEntrega dice cómo quedó el pedido tras cambiar la entrega.
type ResultadoEntrega struct {
	// Cambio es false si el cliente mandó lo mismo que ya había.
	Cambio     bool
	CostoEnvio float64
	Total      float64
}

func entregaInvalida(msg string) error { return fmt.Errorf("%w: %s", ErrEntregaInvalida, msg) }

// TextoEntrega devuelve el mensaje de un ErrEntregaInvalida sin el prefijo técnico.
func TextoEntrega(err error) string {
	return strings.TrimPrefix(err.Error(), ErrEntregaInvalida.Error()+": ")
}

// CambiarEntrega cambia entre «recoger» y «domicilio» en un pedido por
// transferencia que todavía espera el comprobante, y recalcula lo que se debe
// transferir.
//
//   - El costo de envío se calcula así: al pasar a domicilio se cobra costoActual
//     (el vigente hoy); si el pedido ya era a domicilio, conserva el costo con que
//     se creó, aunque el configurado haya cambiado (el cliente vio ese monto).
//   - Al recoger en tienda se quita el envío y se borra la dirección.
//   - El plazo de la reserva NO se extiende: cambiar la entrega no alarga las 48 h.
//   - Una vez subido el comprobante ya no se puede cambiar (el monto debe coincidir
//     con lo transferido); si el negocio lo rechaza, vuelve a poder cambiarse.
//
// El stock no cambia, así que no hace falta bloquear productos.
func CambiarEntrega(conn *sql.DB, pedidoID int, metodo, direccion string, costoActual float64) (ResultadoEntrega, error) {
	direccion = strings.TrimSpace(direccion)
	switch metodo {
	case "recoger":
		direccion = ""
	case "domicilio":
		if direccion == "" {
			return ResultadoEntrega{}, entregaInvalida("Escribe la dirección de entrega.")
		}
		if utf8.RuneCountInString(direccion) > 255 {
			return ResultadoEntrega{}, entregaInvalida("La dirección es demasiado larga.")
		}
		for _, r := range direccion {
			if unicode.IsControl(r) {
				return ResultadoEntrega{}, entregaInvalida("La dirección no puede contener saltos de línea.")
			}
		}
	default:
		return ResultadoEntrega{}, entregaInvalida("Elige cómo quieres recibir tu pedido.")
	}
	if math.IsNaN(costoActual) || costoActual < 0 {
		costoActual = 0
	}

	tx, err := conn.Begin()
	if err != nil {
		return ResultadoEntrega{}, err
	}
	defer tx.Rollback()

	var estado, metodoPago, entregaPrevia, dirPrevia string
	var envioPrevio float64
	var vigente bool
	err = tx.QueryRow(
		`SELECT estado, metodo_pago, metodo_entrega, direccion_entrega, costo_envio, COALESCE(expira_en > now(), false)
		 FROM pedidos WHERE id = $1 FOR UPDATE`, pedidoID,
	).Scan(&estado, &metodoPago, &entregaPrevia, &dirPrevia, &envioPrevio, &vigente)
	if errors.Is(err, sql.ErrNoRows) {
		return ResultadoEntrega{}, ErrPedidoNoExiste
	}
	if err != nil {
		return ResultadoEntrega{}, err
	}
	if metodoPago != MetodoPagoTransferencia || estado != EstadoEsperaTransferencia || !vigente {
		return ResultadoEntrega{}, ErrPedidoNoModificable
	}

	envio := 0.0
	if metodo == "domicilio" {
		envio = costoActual
		if entregaPrevia == "domicilio" {
			envio = envioPrevio
		}
	}
	var subtotal float64
	if err := tx.QueryRow(`SELECT COALESCE(SUM(subtotal), 0) FROM pedido_items WHERE pedido_id = $1`, pedidoID).Scan(&subtotal); err != nil {
		return ResultadoEntrega{}, err
	}
	total := math.Round((subtotal+envio)*100) / 100

	res := ResultadoEntrega{CostoEnvio: envio, Total: total}
	if metodo == entregaPrevia && direccion == dirPrevia && envio == envioPrevio {
		return res, nil // nada que cambiar
	}
	if _, err := tx.Exec(
		`UPDATE pedidos SET metodo_entrega = $1, direccion_entrega = $2, costo_envio = $3, total = $4 WHERE id = $5`,
		metodo, direccion, envio, total, pedidoID,
	); err != nil {
		return ResultadoEntrega{}, err
	}
	if err := tx.Commit(); err != nil {
		return ResultadoEntrega{}, err
	}
	res.Cambio = true
	return res, nil
}

// CancelarPorElCliente cancela, a pedido del propio cliente, un pedido por
// transferencia que todavía no ha subido su comprobante. Suelta la reserva de los
// productos; no hay inventario que devolver ni dinero que reembolsar. Si ya hay
// un comprobante en revisión se pide revisarlo (ErrEnRevision): decidir sobre un
// posible pago le toca al negocio, no a una cancelación del cliente.
func CancelarPorElCliente(conn *sql.DB, pedidoID int) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var estado, metodoPago string
	err = tx.QueryRow(`SELECT estado, metodo_pago FROM pedidos WHERE id = $1 FOR UPDATE`, pedidoID).Scan(&estado, &metodoPago)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPedidoNoExiste
	}
	if err != nil {
		return err
	}
	switch {
	case estado == EstadoCancelado:
		return ErrPedidoCancelado
	case estado == EstadoTransferenciaPorRevisar:
		return ErrEnRevision
	case metodoPago != MetodoPagoTransferencia || estado != EstadoEsperaTransferencia:
		return ErrNoSePuedeCancelar
	}
	if _, err := tx.Exec(
		`UPDATE pedidos SET estado = $1, expira_en = NULL, cancelado_por = 'cliente', cancelado_en = $2, inventario_devuelto = false WHERE id = $3`,
		EstadoCancelado, time.Now(), pedidoID,
	); err != nil {
		return err
	}
	return tx.Commit()
}
