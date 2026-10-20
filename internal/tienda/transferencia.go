package tienda

import (
	"database/sql"
	"time"

	"github.com/google/uuid"

	"manualidades/internal/inventario"
)

// TTLTransferencia es cuánto tiempo se le guarda el stock a un cliente que
// eligió pagar por transferencia: lo que puede tardar en hacerla y subir el
// comprobante. Si vence sin comprobante, el pedido pasa a expirado y el stock
// se libera. Una vez subido el comprobante la reserva ya no vence: espera la
// decisión del negocio (estado transferencia_por_revisar).
const TTLTransferencia = 48 * time.Hour

// Transferencia identifica un pedido que espera una transferencia: el pedido
// que reserva el stock y el token con el que el cliente vuelve a él.
type Transferencia struct {
	PedidoID int
	Token    string
}

// IniciarTransferencia crea un pedido en espera_transferencia y reserva su
// stock por TTLTransferencia, sin tocar el kardex ni cobrar nada. Valida el
// stock disponible con los productos bloqueados, igual que IniciarPago, y si
// no alcanza devuelve un error que envuelve a inventario.ErrStockInsuficiente
// sin dejar nada guardado.
//
// A diferencia de IniciarPago, siempre crea un pedido nuevo: no hay reintentos
// con otra tarjeta que reutilicen uno anterior. lineas y total deben venir de
// CotizarCarrito.
func IniciarTransferencia(conn *sql.DB, datos DatosPedido, lineas []LineaPedido, total float64) (Transferencia, error) {
	if len(lineas) == 0 {
		return Transferencia{}, ErrCarritoVacio
	}

	tx, err := conn.Begin()
	if err != nil {
		return Transferencia{}, err
	}
	defer tx.Rollback()

	ids := make([]int, 0, len(lineas))
	for _, l := range lineas {
		ids = append(ids, l.ProductoID)
	}
	if err := inventario.BloquearProductos(tx, ids); err != nil {
		return Transferencia{}, err
	}
	if err := validarStockDisponible(tx, lineas, 0); err != nil {
		return Transferencia{}, err
	}

	nit := datos.ClienteNIT
	if nit == "" {
		nit = "CF"
	}
	token := uuid.NewString()
	var pedidoID int
	err = tx.QueryRow(
		`INSERT INTO pedidos
			(cliente_nombre, cliente_telefono, cliente_email, cliente_nit,
			 metodo_entrega, direccion_entrega, notas, total, estado, expira_en, token, metodo_pago, costo_envio)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, `+sqlExpiraTransferencia+`, $10, $11, $12)
		 RETURNING id`,
		datos.ClienteNombre, datos.ClienteTelefono, datos.ClienteEmail, nit,
		datos.MetodoEntrega, datos.DireccionEntrega, datos.Notas, total, EstadoEsperaTransferencia, token, MetodoPagoTransferencia, datos.CostoEnvio,
	).Scan(&pedidoID)
	if err != nil {
		return Transferencia{}, err
	}
	if err := insertarLineas(tx, pedidoID, lineas); err != nil {
		return Transferencia{}, err
	}
	if err := tx.Commit(); err != nil {
		return Transferencia{}, err
	}
	return Transferencia{PedidoID: pedidoID, Token: token}, nil
}
