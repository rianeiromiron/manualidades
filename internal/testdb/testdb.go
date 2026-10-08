// Package testdb crea bases de datos PostgreSQL temporales para las pruebas
// de integración. Nunca toca la base real: crea una con nombre único en el
// servidor configurado en config.json, y la elimina al terminar la prueba.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"manualidades/internal/config"
	"manualidades/internal/db"
)

var (
	sondaOnce sync.Once
	sondaErr  error
)

// Nueva devuelve una base temporal con las migraciones indicadas ya aplicadas.
// Si no hay config.json o Postgres accesible, salta la prueba.
func Nueva(t *testing.T, migraciones ...func(*sql.DB) error) *sql.DB {
	t.Helper()

	// config.Load lee config.json relativo al directorio actual: se sube a la
	// raíz del proyecto (donde está go.mod) solo para leerlo.
	cwd, _ := os.Getwd()
	raiz := cwd
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(raiz + "/go.mod"); err == nil {
			break
		}
		raiz += "/.."
	}
	if err := os.Chdir(raiz); err != nil {
		t.Skip("no se pudo ubicar config.json:", err)
	}
	cfg, err := config.Load()
	os.Chdir(cwd)
	if err != nil {
		t.Skip("sin config.json:", err)
	}

	// Sonda con timeout, una vez por ejecución: si el puerto acepta
	// conexiones pero no hay un Postgres respondiendo, se salta en vez de colgarse.
	sondaOnce.Do(func() {
		probe, err := sql.Open("postgres", cfg.DSNMaintenance()+" connect_timeout=5")
		if err != nil {
			sondaErr = err
			return
		}
		defer probe.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		sondaErr = probe.PingContext(ctx)
	})
	if sondaErr != nil {
		t.Skip("no hay Postgres accesible:", sondaErr)
	}

	cfg.DBName = fmt.Sprintf("manualidades_test_%d", time.Now().UnixNano())
	if _, err := db.EnsureDatabase(cfg); err != nil {
		t.Skip("no se pudo crear la base temporal:", err)
	}
	conn, err := db.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		admin, err := sql.Open("postgres", cfg.DSNMaintenance())
		if err != nil {
			return
		}
		defer admin.Close()
		admin.Exec(`DROP DATABASE IF EXISTS "` + cfg.DBName + `" WITH (FORCE)`)
	})

	for _, migrar := range migraciones {
		if err := migrar(conn); err != nil {
			t.Fatal(err)
		}
		// Las migraciones deben ser idempotentes.
		if err := migrar(conn); err != nil {
			t.Fatalf("segunda migración: %v", err)
		}
	}
	return conn
}
