// Package cuentas mantiene las cuentas bancarias a las que los clientes pueden
// hacer transferencias, y el historial de quién las cambió.
//
// Cambiar una cuenta equivale a desviar los pagos de la tienda, así que cada
// alta, edición, activación, desactivación y baja queda registrada junto con
// quién la hizo, desde qué dirección y qué cambió.
package cuentas

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const schema = `
CREATE TABLE IF NOT EXISTS cuentas_bancarias (
	id             SERIAL PRIMARY KEY,
	banco          VARCHAR(100) NOT NULL,
	tipo_cuenta    VARCHAR(20)  NOT NULL CHECK (tipo_cuenta IN ('monetaria', 'ahorro')),
	numero         VARCHAR(40)  NOT NULL,
	titular        VARCHAR(150) NOT NULL,
	moneda         VARCHAR(3)   NOT NULL DEFAULT 'GTQ' CHECK (moneda IN ('GTQ', 'USD')),
	instrucciones  VARCHAR(300) NOT NULL DEFAULT '',
	activa         BOOLEAN      NOT NULL DEFAULT true,
	orden          INTEGER      NOT NULL DEFAULT 0,
	creado_en      TIMESTAMPTZ  NOT NULL DEFAULT now(),
	actualizado_en TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Sin clave foránea a propósito: el historial debe sobrevivir a la baja de la
-- cuenta.
CREATE TABLE IF NOT EXISTS cuentas_bancarias_historial (
	id         SERIAL PRIMARY KEY,
	cuenta_id  INTEGER      NOT NULL,
	accion     VARCHAR(20)  NOT NULL CHECK (accion IN ('creada', 'editada', 'activada', 'desactivada', 'eliminada')),
	usuario    VARCHAR(50)  NOT NULL,
	ip         VARCHAR(45)  NOT NULL DEFAULT '',
	detalle    TEXT         NOT NULL DEFAULT '',
	creado_en  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cuentas_historial_fecha ON cuentas_bancarias_historial(creado_en DESC);
`

// Migrate crea las tablas de cuentas bancarias si no existen. Idempotente.
func Migrate(conn *sql.DB) error {
	if _, err := conn.Exec(schema); err != nil {
		return fmt.Errorf("migrando esquema de cuentas bancarias: %w", err)
	}
	return nil
}

// Tipos de cuenta y monedas aceptados, en el orden en que se ofrecen.
var (
	Tipos   = []Opcion{{"monetaria", "Monetaria"}, {"ahorro", "Ahorro"}}
	Monedas = []Opcion{{"GTQ", "Quetzales (GTQ)"}, {"USD", "Dólares (USD)"}}
)

// Opcion es un valor aceptado y su texto para mostrar.
type Opcion struct{ Valor, Nombre string }

func esOpcion(opciones []Opcion, valor string) bool {
	for _, o := range opciones {
		if o.Valor == valor {
			return true
		}
	}
	return false
}

func nombreDe(opciones []Opcion, valor string) string {
	for _, o := range opciones {
		if o.Valor == valor {
			return o.Nombre
		}
	}
	return valor
}

type Cuenta struct {
	ID            int
	Banco         string
	TipoCuenta    string
	Numero        string
	Titular       string
	Moneda        string
	Instrucciones string
	Activa        bool
	Orden         int
	CreadoEn      time.Time
	ActualizadoEn time.Time
}

// TipoNombre y MonedaNombre devuelven el texto para mostrar al usuario.
func (c Cuenta) TipoNombre() string   { return nombreDe(Tipos, c.TipoCuenta) }
func (c Cuenta) MonedaNombre() string { return nombreDe(Monedas, c.Moneda) }

// Datos es lo que el formulario del admin envía para crear o editar.
type Datos struct {
	Banco         string
	TipoCuenta    string
	Numero        string
	Titular       string
	Moneda        string
	Instrucciones string
	Orden         int
}

// Quien identifica a la persona que hace un cambio, para el historial.
type Quien struct {
	Usuario string
	IP      string
}

var (
	// ErrValidacion envuelve los mensajes de datos inválidos: son seguros de
	// mostrar tal cual al usuario del admin.
	ErrValidacion = errors.New("datos inválidos")
	ErrNoExiste   = errors.New("la cuenta no existe")
)

func invalido(formato string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidacion, fmt.Sprintf(formato, args...))
}

// Mensaje devuelve el texto de un error de validación sin el prefijo técnico.
func Mensaje(err error) string {
	return strings.TrimPrefix(err.Error(), ErrValidacion.Error()+": ")
}

var reNumero = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z \-]*[0-9A-Za-z]$`)

// textoSimple valida un campo de una sola línea: sin saltos de línea ni otros
// caracteres de control (que luego irían a correos y pantallas).
func textoSimple(campo, valor string, min, max int) error {
	n := utf8.RuneCountInString(valor)
	if n < min {
		if min == 0 {
			return nil
		}
		return invalido("%s es obligatorio.", campo)
	}
	if n > max {
		return invalido("%s admite como máximo %d caracteres.", campo, max)
	}
	for _, r := range valor {
		if unicode.IsControl(r) {
			return invalido("%s no puede contener saltos de línea ni caracteres de control.", campo)
		}
	}
	return nil
}

// limpiar quita espacios sobrantes y valida. Devuelve los datos normalizados.
func limpiar(d Datos) (Datos, error) {
	d.Banco = strings.TrimSpace(d.Banco)
	d.TipoCuenta = strings.TrimSpace(d.TipoCuenta)
	d.Numero = strings.TrimSpace(d.Numero)
	d.Titular = strings.TrimSpace(d.Titular)
	d.Moneda = strings.TrimSpace(d.Moneda)
	d.Instrucciones = strings.TrimSpace(d.Instrucciones)

	if err := textoSimple("El banco", d.Banco, 2, 100); err != nil {
		return d, err
	}
	if !esOpcion(Tipos, d.TipoCuenta) {
		return d, invalido("El tipo de cuenta no es válido.")
	}
	if err := textoSimple("El número de cuenta", d.Numero, 4, 40); err != nil {
		return d, err
	}
	if !reNumero.MatchString(d.Numero) {
		return d, invalido("El número de cuenta solo puede llevar letras, números, espacios y guiones.")
	}
	if err := textoSimple("El titular", d.Titular, 2, 150); err != nil {
		return d, err
	}
	if !esOpcion(Monedas, d.Moneda) {
		return d, invalido("La moneda no es válida.")
	}
	if err := textoSimple("Las instrucciones", d.Instrucciones, 0, 300); err != nil {
		return d, err
	}
	if d.Orden < 0 || d.Orden > 9999 {
		return d, invalido("El orden debe estar entre 0 y 9999.")
	}
	return d, nil
}

const columnas = `id, banco, tipo_cuenta, numero, titular, moneda, instrucciones, activa, orden, creado_en, actualizado_en`

func scan(row interface{ Scan(...any) error }) (Cuenta, error) {
	var c Cuenta
	err := row.Scan(&c.ID, &c.Banco, &c.TipoCuenta, &c.Numero, &c.Titular, &c.Moneda, &c.Instrucciones,
		&c.Activa, &c.Orden, &c.CreadoEn, &c.ActualizadoEn)
	return c, err
}

func consultar(conn *sql.DB, donde string) ([]Cuenta, error) {
	rows, err := conn.Query(`SELECT ` + columnas + ` FROM cuentas_bancarias ` + donde + ` ORDER BY orden, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Cuenta
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// List devuelve todas las cuentas, activas o no (para el admin).
func List(conn *sql.DB) ([]Cuenta, error) { return consultar(conn, "") }

// ListActivas devuelve solo las cuentas que se ofrecen a los clientes.
func ListActivas(conn *sql.DB) ([]Cuenta, error) { return consultar(conn, "WHERE activa") }

func Get(conn *sql.DB, id int) (Cuenta, error) {
	c, err := scan(conn.QueryRow(`SELECT `+columnas+` FROM cuentas_bancarias WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Cuenta{}, ErrNoExiste
	}
	return c, err
}

// anotar registra un cambio en el historial dentro de la transacción del
// cambio mismo: o quedan los dos, o ninguno.
func anotar(tx *sql.Tx, cuentaID int, accion string, q Quien, detalle string) error {
	_, err := tx.Exec(
		`INSERT INTO cuentas_bancarias_historial (cuenta_id, accion, usuario, ip, detalle) VALUES ($1, $2, $3, $4, $5)`,
		cuentaID, accion, q.Usuario, q.IP, detalle,
	)
	return err
}

// descripcion resume una cuenta para el historial (con el número completo:
// el historial solo lo ve quien tiene acceso a este módulo).
func descripcion(c Cuenta) string {
	estado := "activa"
	if !c.Activa {
		estado = "inactiva"
	}
	return fmt.Sprintf("%s · %s · %s · %s · %s · %s", c.Banco, c.TipoNombre(), c.Numero, c.Titular, c.Moneda, estado)
}

// Create valida y guarda una cuenta nueva (activa) y anota el alta.
func Create(conn *sql.DB, d Datos, q Quien) (int, error) {
	d, err := limpiar(d)
	if err != nil {
		return 0, err
	}
	tx, err := conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var id int
	if err := tx.QueryRow(
		`INSERT INTO cuentas_bancarias (banco, tipo_cuenta, numero, titular, moneda, instrucciones, orden)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		d.Banco, d.TipoCuenta, d.Numero, d.Titular, d.Moneda, d.Instrucciones, d.Orden,
	).Scan(&id); err != nil {
		return 0, err
	}
	nueva := Cuenta{Banco: d.Banco, TipoCuenta: d.TipoCuenta, Numero: d.Numero, Titular: d.Titular, Moneda: d.Moneda, Activa: true}
	if err := anotar(tx, id, "creada", q, descripcion(nueva)); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// cambios lista, campo por campo, lo que difiere entre dos versiones.
func cambios(antes Cuenta, d Datos) []string {
	var out []string
	cmp := func(campo, a, b string) {
		if a != b {
			out = append(out, fmt.Sprintf("%s: «%s» → «%s»", campo, a, b))
		}
	}
	cmp("banco", antes.Banco, d.Banco)
	cmp("tipo", antes.TipoNombre(), nombreDe(Tipos, d.TipoCuenta))
	cmp("número", antes.Numero, d.Numero)
	cmp("titular", antes.Titular, d.Titular)
	cmp("moneda", antes.Moneda, d.Moneda)
	cmp("instrucciones", antes.Instrucciones, d.Instrucciones)
	cmp("orden", fmt.Sprint(antes.Orden), fmt.Sprint(d.Orden))
	return out
}

// Update valida y guarda los cambios. Si no cambió nada, no escribe ni anota.
func Update(conn *sql.DB, id int, d Datos, q Quien) error {
	d, err := limpiar(d)
	if err != nil {
		return err
	}
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	antes, err := scan(tx.QueryRow(`SELECT `+columnas+` FROM cuentas_bancarias WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoExiste
	}
	if err != nil {
		return err
	}
	dif := cambios(antes, d)
	if len(dif) == 0 {
		return nil
	}
	if _, err := tx.Exec(
		`UPDATE cuentas_bancarias
		 SET banco = $1, tipo_cuenta = $2, numero = $3, titular = $4, moneda = $5, instrucciones = $6, orden = $7,
		     actualizado_en = now()
		 WHERE id = $8`,
		d.Banco, d.TipoCuenta, d.Numero, d.Titular, d.Moneda, d.Instrucciones, d.Orden, id,
	); err != nil {
		return err
	}
	if err := anotar(tx, id, "editada", q, strings.Join(dif, "\n")); err != nil {
		return err
	}
	return tx.Commit()
}

// SetActiva activa o desactiva una cuenta. Repetir el estado actual no hace
// nada ni anota.
func SetActiva(conn *sql.DB, id int, activa bool, q Quien) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	c, err := scan(tx.QueryRow(`SELECT `+columnas+` FROM cuentas_bancarias WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoExiste
	}
	if err != nil {
		return err
	}
	if c.Activa == activa {
		return nil
	}
	if _, err := tx.Exec(`UPDATE cuentas_bancarias SET activa = $1, actualizado_en = now() WHERE id = $2`, activa, id); err != nil {
		return err
	}
	accion := "desactivada"
	if activa {
		accion = "activada"
	}
	c.Activa = activa
	if err := anotar(tx, id, accion, q, descripcion(c)); err != nil {
		return err
	}
	return tx.Commit()
}

// Delete da de baja una cuenta; el historial conserva sus datos.
func Delete(conn *sql.DB, id int, q Quien) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	c, err := scan(tx.QueryRow(`SELECT `+columnas+` FROM cuentas_bancarias WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoExiste
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM cuentas_bancarias WHERE id = $1`, id); err != nil {
		return err
	}
	if err := anotar(tx, id, "eliminada", q, descripcion(c)); err != nil {
		return err
	}
	return tx.Commit()
}

type Cambio struct {
	ID       int
	CuentaID int
	Accion   string
	Usuario  string
	IP       string
	Detalle  string
	Fecha    time.Time
}

// Historial devuelve los últimos cambios, del más reciente al más antiguo.
func Historial(conn *sql.DB, limite int) ([]Cambio, error) {
	rows, err := conn.Query(
		`SELECT id, cuenta_id, accion, usuario, ip, detalle, creado_en
		 FROM cuentas_bancarias_historial ORDER BY creado_en DESC, id DESC LIMIT $1`, limite)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Cambio
	for rows.Next() {
		var c Cambio
		if err := rows.Scan(&c.ID, &c.CuentaID, &c.Accion, &c.Usuario, &c.IP, &c.Detalle, &c.Fecha); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
