package sesiones

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"manualidades/internal/config"
	"manualidades/internal/db"
)

// nuevaBase crea una base temporal (nunca toca la real) con el esquema de
// sesiones; la elimina al terminar. Sin Postgres accesible, salta el test.
func nuevaBase(t *testing.T) *sql.DB {
	t.Helper()

	cwd, _ := os.Getwd()
	if err := os.Chdir("../.."); err != nil {
		t.Skip("no se pudo ubicar config.json:", err)
	}
	cfg, err := config.Load()
	os.Chdir(cwd)
	if err != nil {
		t.Skip("sin config.json:", err)
	}
	probe, err := sql.Open("postgres", cfg.DSNMaintenance()+" connect_timeout=5")
	if err != nil {
		t.Skip("no hay Postgres accesible:", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	err = probe.PingContext(ctx)
	probe.Close()
	if err != nil {
		t.Skip("no hay Postgres accesible:", err)
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

	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(conn); err != nil {
		t.Fatalf("segunda migración: %v", err)
	}
	return conn
}

func nueva(t *testing.T, conn *sql.DB, kind, ident string, vida time.Duration) string {
	t.Helper()
	id, err := NuevoID()
	if err != nil {
		t.Fatal(err)
	}
	if err := Crear(conn, id, kind, ident, time.Now().Add(vida), "1.2.3.4", "test"); err != nil {
		t.Fatal(err)
	}
	return id
}

func valida(t *testing.T, conn *sql.DB, id, kind, ident string) bool {
	t.Helper()
	ok, err := Valida(conn, id, kind, ident)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestRevocarUnaSesionNoTocaLasDemas(t *testing.T) {
	conn := nuevaBase(t)
	a := nueva(t, conn, "user", "7", time.Hour)
	b := nueva(t, conn, "user", "7", time.Hour)

	if !valida(t, conn, a, "user", "7") || !valida(t, conn, b, "user", "7") {
		t.Fatal("las sesiones recién creadas deberían ser válidas")
	}
	if err := Revocar(conn, a, "user", "7"); err != nil {
		t.Fatal(err)
	}
	if valida(t, conn, a, "user", "7") {
		t.Error("la sesión revocada sigue válida")
	}
	if !valida(t, conn, b, "user", "7") {
		t.Error("revocar una sesión invalidó otra")
	}
}

func TestNoSeRevocaLaSesionDeOtraIdentidad(t *testing.T) {
	conn := nuevaBase(t)
	a := nueva(t, conn, "user", "7", time.Hour)

	if err := Revocar(conn, a, "user", "8"); err != nil {
		t.Fatal(err)
	}
	if !valida(t, conn, a, "user", "7") {
		t.Error("una identidad ajena pudo revocar la sesión")
	}
	// Y el id solo vale para su identidad.
	if valida(t, conn, a, "user", "8") {
		t.Error("la sesión es válida para otra identidad")
	}
	if valida(t, conn, a, "admin", "7") {
		t.Error("la sesión es válida para otro tipo de identidad")
	}
}

func TestSesionVencidaoDesconocidaNoEsValida(t *testing.T) {
	conn := nuevaBase(t)
	vencida := nueva(t, conn, "admin", "root", -time.Minute)
	if valida(t, conn, vencida, "admin", "root") {
		t.Error("una sesión vencida es válida")
	}
	if valida(t, conn, "inexistente", "admin", "root") {
		t.Error("un id desconocido es válido")
	}
}

func TestRevocarOtrasConservaLaActual(t *testing.T) {
	conn := nuevaBase(t)
	actual := nueva(t, conn, "user", "7", time.Hour)
	otra := nueva(t, conn, "user", "7", time.Hour)
	ajena := nueva(t, conn, "user", "9", time.Hour)

	if err := RevocarOtras(conn, "user", "7", actual); err != nil {
		t.Fatal(err)
	}
	if !valida(t, conn, actual, "user", "7") {
		t.Error("RevocarOtras cerró la sesión actual")
	}
	if valida(t, conn, otra, "user", "7") {
		t.Error("RevocarOtras dejó viva otra sesión")
	}
	if !valida(t, conn, ajena, "user", "9") {
		t.Error("RevocarOtras tocó a otro usuario")
	}
}

func TestActivasSoloListaLasVigentes(t *testing.T) {
	conn := nuevaBase(t)
	viva := nueva(t, conn, "user", "7", time.Hour)
	revocada := nueva(t, conn, "user", "7", time.Hour)
	nueva(t, conn, "user", "7", -time.Minute)
	Revocar(conn, revocada, "user", "7")

	lista, err := Activas(conn, "user", "7")
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 1 || lista[0].ID != viva {
		t.Errorf("Activas devolvió %+v, se esperaba solo %s", lista, viva)
	}
}
