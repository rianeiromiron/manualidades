package db

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"

	"manualidades/internal/config"
)

// Test intenta abrir una conexión y hacer ping contra la base configurada.
func Test(cfg config.DBConfig) error {
	conn, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Ping()
}

// EnsureDatabase verifica si la base de datos configurada existe en el
// servidor y la crea si hace falta. Se conecta a la base "postgres" para
// poder emitir CREATE DATABASE (no se puede crear una base estando
// conectado a ella misma).
func EnsureDatabase(cfg config.DBConfig) (created bool, err error) {
	conn, err := sql.Open("postgres", cfg.DSNMaintenance())
	if err != nil {
		return false, err
	}
	defer conn.Close()

	if err := conn.Ping(); err != nil {
		return false, fmt.Errorf("no se pudo conectar al servidor: %w", err)
	}

	var exists bool
	err = conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, cfg.DBName).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("verificando base de datos: %w", err)
	}
	if exists {
		return false, nil
	}

	// El nombre de la base no se puede parametrizar en CREATE DATABASE;
	// se valida y se cita explícitamente.
	if !isValidIdentifier(cfg.DBName) {
		return false, fmt.Errorf("nombre de base de datos inválido: %q", cfg.DBName)
	}
	quoted := `"` + cfg.DBName + `"`
	if _, err := conn.Exec(`CREATE DATABASE ` + quoted); err != nil {
		return false, fmt.Errorf("creando base de datos: %w", err)
	}
	return true, nil
}

func isValidIdentifier(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, r := range s {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}

// Open abre un pool de conexiones contra la base configurada.
func Open(cfg config.DBConfig) (*sql.DB, error) {
	conn, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}
