package sitio

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Formas de pago que ofrece la tienda. Cada una se puede activar o desactivar
// desde el admin sin tocar el código: así la transferencia puede abrirse antes
// que la tarjeta, o pausarse una de las dos.
const (
	FormaTarjeta       = "tarjeta"
	FormaTransferencia = "transferencia"
)

// ErrFormaPagoDesconocida se devuelve cuando la clave no es una forma de pago.
var ErrFormaPagoDesconocida = errors.New("esa forma de pago no existe")

type FormaPago struct {
	Clave  string
	Nombre string
	Activa bool
	// ActualizadoPor y ActualizadoEn dicen quién y cuándo la activó o desactivó
	// por última vez (vacíos si nunca se ha cambiado).
	ActualizadoPor string
	ActualizadoEn  *time.Time
}

// ListFormasPago devuelve todas las formas de pago, en el orden en que se ofrecen.
func ListFormasPago(conn *sql.DB) ([]FormaPago, error) {
	rows, err := conn.Query(`SELECT clave, nombre, activa, actualizado_por, actualizado_en FROM formas_pago ORDER BY orden, clave`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FormaPago
	for rows.Next() {
		var f FormaPago
		var en sql.NullTime
		if err := rows.Scan(&f.Clave, &f.Nombre, &f.Activa, &f.ActualizadoPor, &en); err != nil {
			return nil, err
		}
		if en.Valid {
			f.ActualizadoEn = &en.Time
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FormaPagoActiva dice si la forma de pago está activada. Una clave desconocida
// se considera desactivada.
func FormaPagoActiva(conn *sql.DB, clave string) (bool, error) {
	var activa bool
	err := conn.QueryRow(`SELECT activa FROM formas_pago WHERE clave = $1`, clave).Scan(&activa)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return activa, err
}

// SetFormaPagoActiva activa o desactiva una forma de pago y anota quién lo hizo.
// Desactivar una forma de pago no afecta a los pedidos ya creados con ella: solo
// impide crear pedidos nuevos.
func SetFormaPagoActiva(conn *sql.DB, clave string, activa bool, usuario string) error {
	if len([]rune(usuario)) > 50 {
		usuario = string([]rune(usuario)[:50])
	}
	res, err := conn.Exec(
		`UPDATE formas_pago SET activa = $1, actualizado_por = $2, actualizado_en = now() WHERE clave = $3`,
		activa, strings.TrimSpace(usuario), clave,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrFormaPagoDesconocida
	}
	return nil
}
