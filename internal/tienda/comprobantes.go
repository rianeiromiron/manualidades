package tienda

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lib/pq"
)

// MaxComprobantesPorPedido acota cuántos comprobantes puede subir un cliente
// por pedido (cada uno se guarda en disco).
const MaxComprobantesPorPedido = 5

// MaxTransferenciasAbiertas es cuántos pedidos por transferencia pueden tener
// abiertos a la vez con el mismo correo. Cada uno aparta stock hasta 48 h; sin
// tope, una sola persona podría acaparar el inventario.
const MaxTransferenciasAbiertas = 3

// Estados de un comprobante.
const (
	ComprobantePorRevisar = "por_revisar"
	ComprobanteAceptado   = "aceptado"
	ComprobanteRechazado  = "rechazado"
)

var (
	// ErrComprobanteInvalido envuelve mensajes seguros de mostrar al cliente.
	ErrComprobanteInvalido = errors.New("comprobante inválido")
	// ErrPedidoNoAceptaComprobante: el pedido no es de transferencia, ya venció,
	// ya está en revisión o ya se pagó.
	ErrPedidoNoAceptaComprobante = errors.New("este pedido ya no admite comprobantes")
	ErrDemasiadosComprobantes    = errors.New("se alcanzó el máximo de comprobantes para este pedido")
	// ErrBoletaRepetida: esa boleta de ese banco ya está registrada.
	ErrBoletaRepetida = errors.New("esa boleta ya está registrada")
	ErrPedidoNoExiste = errors.New("el pedido no existe")
)

func invalido(formato string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrComprobanteInvalido, fmt.Sprintf(formato, args...))
}

// TextoComprobante devuelve el mensaje de un ErrComprobanteInvalido sin el
// prefijo técnico.
func TextoComprobante(err error) string {
	return strings.TrimPrefix(err.Error(), ErrComprobanteInvalido.Error()+": ")
}

// DatosComprobante es lo que el cliente escribe al subir su comprobante.
type DatosComprobante struct {
	NumeroBoleta string
	BancoOrigen  string
	Fecha        time.Time // solo importa el día
	// Monto es lo que el cliente dice haber transferido, en quetzales.
	Monto float64
}

var reBoleta = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z\-]*$`)

// Limpiar normaliza y valida los datos. hoy es el día actual en la hora local
// del negocio (se recibe para poder probarlo).
func (d DatosComprobante) Limpiar(hoy time.Time) (DatosComprobante, error) {
	d.NumeroBoleta = strings.TrimSpace(d.NumeroBoleta)
	d.BancoOrigen = strings.TrimSpace(d.BancoOrigen)

	if n := utf8.RuneCountInString(d.NumeroBoleta); n < 4 || n > 40 || !reBoleta.MatchString(d.NumeroBoleta) {
		return d, invalido("El número de boleta debe tener entre 4 y 40 letras, números o guiones.")
	}
	if n := utf8.RuneCountInString(d.BancoOrigen); n < 2 || n > 100 {
		return d, invalido("Escribe el banco desde el que hiciste la transferencia.")
	}
	for _, r := range d.BancoOrigen {
		if unicode.IsControl(r) {
			return d, invalido("El banco no puede contener saltos de línea.")
		}
	}
	if d.Fecha.IsZero() {
		return d, invalido("Indica la fecha de la transferencia.")
	}
	dia := time.Date(d.Fecha.Year(), d.Fecha.Month(), d.Fecha.Day(), 0, 0, 0, 0, time.UTC)
	hoyDia := time.Date(hoy.Year(), hoy.Month(), hoy.Day(), 0, 0, 0, 0, time.UTC)
	if dia.After(hoyDia) {
		return d, invalido("La fecha de la transferencia no puede ser futura.")
	}
	if dia.Before(hoyDia.AddDate(0, 0, -30)) {
		return d, invalido("La fecha de la transferencia es de hace más de 30 días.")
	}
	d.Fecha = dia

	// Se redondea a centavos. No se exige que coincida con el total: el negocio lo
	// compara al revisar y decide (puede faltar un envío, o sobrar un centavo).
	d.Monto = math.Round(d.Monto*100) / 100
	if math.IsNaN(d.Monto) || d.Monto <= 0 || d.Monto > 999999.99 {
		return d, invalido("Indica el monto que transferiste, en quetzales.")
	}
	return d, nil
}

type Comprobante struct {
	ID           int
	PedidoID     int
	Archivo      string
	NumeroBoleta string
	BancoOrigen  string
	Fecha        time.Time
	Estado       string
	Nota         string
	Monto        float64
	CreadoEn     time.Time
	// RevisadoPor y RevisadoEn quedan al aprobar o rechazar.
	RevisadoPor string
	RevisadoEn  *time.Time
}

// RegistrarComprobante guarda un comprobante y pasa el pedido a
// transferencia_por_revisar, quitándole el vencimiento: desde ese momento la
// reserva dura hasta que el negocio decida. Todo en una transacción, con el
// pedido bloqueado, para que dos subidas simultáneas no se pisen.
//
// archivo es el nombre ya guardado en disco. Si devuelve error, el llamador
// debe borrar ese archivo.
func RegistrarComprobante(conn *sql.DB, pedidoID int, archivo string, d DatosComprobante, hoy time.Time) (int, error) {
	d, err := d.Limpiar(hoy)
	if err != nil {
		return 0, err
	}
	tx, err := conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var estado, metodo string
	var vigente bool
	err = tx.QueryRow(
		`SELECT estado, metodo_pago, COALESCE(expira_en > now(), false) FROM pedidos WHERE id = $1 FOR UPDATE`, pedidoID,
	).Scan(&estado, &metodo, &vigente)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrPedidoNoExiste
	}
	if err != nil {
		return 0, err
	}
	if metodo != MetodoPagoTransferencia || estado != EstadoEsperaTransferencia || !vigente {
		return 0, ErrPedidoNoAceptaComprobante
	}

	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM comprobantes WHERE pedido_id = $1`, pedidoID).Scan(&n); err != nil {
		return 0, err
	}
	if n >= MaxComprobantesPorPedido {
		return 0, ErrDemasiadosComprobantes
	}

	var id int
	err = tx.QueryRow(
		`INSERT INTO comprobantes (pedido_id, archivo, numero_boleta, banco_origen, fecha_transferencia, monto)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		pedidoID, archivo, d.NumeroBoleta, d.BancoOrigen, d.Fecha, d.Monto,
	).Scan(&id)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" { // unique_violation
		return 0, ErrBoletaRepetida
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(
		`UPDATE pedidos SET estado = $1, expira_en = NULL WHERE id = $2`, EstadoTransferenciaPorRevisar, pedidoID,
	); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// ListComprobantes devuelve los comprobantes de un pedido, del más antiguo al
// más reciente.
func ListComprobantes(conn *sql.DB, pedidoID int) ([]Comprobante, error) {
	rows, err := conn.Query(
		`SELECT id, pedido_id, archivo, numero_boleta, banco_origen, fecha_transferencia, estado, nota, creado_en,
			revisado_por, revisado_en, monto
		 FROM comprobantes WHERE pedido_id = $1 ORDER BY id`, pedidoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comprobante
	for rows.Next() {
		var c Comprobante
		var revisado sql.NullTime
		if err := rows.Scan(&c.ID, &c.PedidoID, &c.Archivo, &c.NumeroBoleta, &c.BancoOrigen, &c.Fecha, &c.Estado, &c.Nota, &c.CreadoEn,
			&c.RevisadoPor, &revisado, &c.Monto); err != nil {
			return nil, err
		}
		if revisado.Valid {
			c.RevisadoEn = &revisado.Time
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PedidoPorToken devuelve el pedido de transferencia que tiene ese token.
// Cualquier otro caso (token desconocido, pedido de tarjeta) es ErrPedidoNoExiste:
// el token es la llave de acceso del cliente, y no abre pedidos de otro tipo.
func PedidoPorToken(conn *sql.DB, token string) (Pedido, error) {
	if token == "" {
		return Pedido{}, ErrPedidoNoExiste
	}
	var id int
	err := conn.QueryRow(
		`SELECT id FROM pedidos WHERE token = $1 AND metodo_pago = $2`, token, MetodoPagoTransferencia,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Pedido{}, ErrPedidoNoExiste
	}
	if err != nil {
		return Pedido{}, err
	}
	return GetPedido(conn, id)
}

// TokenParaReenvio devuelve el token de un pedido de transferencia si el
// correo coincide (sin distinguir mayúsculas). Sirve para reenviar el enlace al
// correo que el cliente dio al hacer el pedido; nunca se muestra en pantalla.
func TokenParaReenvio(conn *sql.DB, pedidoID int, email string) (token, nombre string, ok bool, err error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", "", false, nil
	}
	err = conn.QueryRow(
		`SELECT token, cliente_nombre FROM pedidos
		 WHERE id = $1 AND metodo_pago = $2 AND token IS NOT NULL AND lower(cliente_email) = lower($3)`,
		pedidoID, MetodoPagoTransferencia, email,
	).Scan(&token, &nombre)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return token, nombre, true, nil
}

// TransferenciasAbiertas cuenta los pedidos por transferencia que ese correo
// tiene apartando stock (esperando o en revisión).
func TransferenciasAbiertas(conn *sql.DB, email string) (int, error) {
	var n int
	err := conn.QueryRow(
		`SELECT count(*) FROM pedidos
		 WHERE lower(cliente_email) = lower($1) AND metodo_pago = $2 AND estado IN ($3, $4)`,
		strings.TrimSpace(email), MetodoPagoTransferencia, EstadoEsperaTransferencia, EstadoTransferenciaPorRevisar,
	).Scan(&n)
	return n, err
}

// TokenDePedido devuelve el token del enlace privado de un pedido ("" si no
// tiene). Lo usa el correo que se manda al cliente tras una revisión.
func TokenDePedido(conn *sql.DB, pedidoID int) (string, error) {
	var token sql.NullString
	err := conn.QueryRow(`SELECT token FROM pedidos WHERE id = $1`, pedidoID).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrPedidoNoExiste
	}
	return token.String, err
}
