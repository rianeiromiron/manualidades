// Package sesiones lleva el registro en base de datos de las sesiones del
// panel de administración. La cookie de sesión sigue firmada con HMAC (ver
// internal/web/handlers_auth.go), pero además lleva un identificador
// aleatorio; solo es válida mientras ese identificador exista aquí, no esté
// revocado y no haya vencido. Eso permite cerrar una sesión concreta (la
// robada, la de otro equipo) sin rotar la llave y tumbar todas las de esa
// identidad.
package sesiones

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

const schema = `
CREATE TABLE IF NOT EXISTS sesiones (
	id         VARCHAR(32) PRIMARY KEY,
	kind       VARCHAR(10)  NOT NULL,
	ident      VARCHAR(50)  NOT NULL,
	creada_en  TIMESTAMPTZ  NOT NULL DEFAULT now(),
	expira_en  TIMESTAMPTZ  NOT NULL,
	ip         VARCHAR(64)  NOT NULL DEFAULT '',
	user_agent VARCHAR(255) NOT NULL DEFAULT '',
	revocada   BOOLEAN      NOT NULL DEFAULT false
);

CREATE INDEX IF NOT EXISTS sesiones_identidad_idx ON sesiones (kind, ident);
`

// Migrate crea la tabla de sesiones si todavía no existe. Idempotente.
func Migrate(conn *sql.DB) error {
	if _, err := conn.Exec(schema); err != nil {
		return fmt.Errorf("migrando esquema de sesiones: %w", err)
	}
	return nil
}

// Sesion es una fila del registro, lista para mostrarse en pantalla.
type Sesion struct {
	ID        string
	Kind      string
	Ident     string
	CreadaEn  time.Time
	ExpiraEn  time.Time
	IP        string
	UserAgent string
}

// NuevoID devuelve un identificador aleatorio de 128 bits en hexadecimal.
func NuevoID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func recortar(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// Crear registra una sesión nueva. También borra, de paso, las ya vencidas.
func Crear(conn *sql.DB, id, kind, ident string, expira time.Time, ip, userAgent string) error {
	conn.Exec(`DELETE FROM sesiones WHERE expira_en < now() - interval '1 day'`)
	_, err := conn.Exec(
		`INSERT INTO sesiones (id, kind, ident, expira_en, ip, user_agent)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		id, kind, ident, expira, recortar(ip, 64), recortar(userAgent, 255),
	)
	return err
}

// Valida dice si la sesión existe para esa identidad, no está revocada y no
// ha vencido. Un error de base de datos se devuelve tal cual: quien llama
// decide, y para una sesión de administración debe tratarlo como inválida.
func Valida(conn *sql.DB, id, kind, ident string) (bool, error) {
	var ok bool
	err := conn.QueryRow(
		`SELECT EXISTS (
			SELECT 1 FROM sesiones
			WHERE id = $1 AND kind = $2 AND ident = $3 AND NOT revocada AND expira_en > now()
		)`, id, kind, ident,
	).Scan(&ok)
	return ok, err
}

// Revocar invalida una sesión concreta de esa identidad. No falla si no
// existe: el resultado buscado (que deje de servir) ya se cumple.
func Revocar(conn *sql.DB, id, kind, ident string) error {
	_, err := conn.Exec(
		`UPDATE sesiones SET revocada = true WHERE id = $1 AND kind = $2 AND ident = $3`,
		id, kind, ident,
	)
	return err
}

// RevocarOtras invalida todas las sesiones de la identidad salvo la indicada.
func RevocarOtras(conn *sql.DB, kind, ident, conservar string) error {
	_, err := conn.Exec(
		`UPDATE sesiones SET revocada = true WHERE kind = $1 AND ident = $2 AND id <> $3`,
		kind, ident, conservar,
	)
	return err
}

// RevocarTodas invalida todas las sesiones de la identidad (p. ej. al
// cambiarle la contraseña).
func RevocarTodas(conn *sql.DB, kind, ident string) error {
	_, err := conn.Exec(`UPDATE sesiones SET revocada = true WHERE kind = $1 AND ident = $2`, kind, ident)
	return err
}

// Activas lista las sesiones vigentes de la identidad, la más reciente primero.
func Activas(conn *sql.DB, kind, ident string) ([]Sesion, error) {
	rows, err := conn.Query(
		`SELECT id, kind, ident, creada_en, expira_en, ip, user_agent
		 FROM sesiones
		 WHERE kind = $1 AND ident = $2 AND NOT revocada AND expira_en > now()
		 ORDER BY creada_en DESC`,
		kind, ident,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Sesion
	for rows.Next() {
		var s Sesion
		if err := rows.Scan(&s.ID, &s.Kind, &s.Ident, &s.CreadaEn, &s.ExpiraEn, &s.IP, &s.UserAgent); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
