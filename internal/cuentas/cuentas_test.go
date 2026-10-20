package cuentas

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"manualidades/internal/testdb"
)

var quien = Quien{Usuario: "ana", IP: "203.0.113.7"}

func valido() Datos {
	return Datos{
		Banco: "Banco de Prueba", TipoCuenta: "monetaria", Numero: "0000-0000-1",
		Titular: "Manualidades Cristal", Moneda: "GTQ", Instrucciones: "Indica el número de pedido.", Orden: 1,
	}
}

func nuevaBase(t *testing.T) *sql.DB {
	t.Helper()
	return testdb.Nueva(t, Migrate)
}

func historial(t *testing.T, conn *sql.DB) []Cambio {
	t.Helper()
	h, err := Historial(conn, 100)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestCrearYListar(t *testing.T) {
	conn := nuevaBase(t)
	id, err := Create(conn, valido(), quien)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Get(conn, id)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Activa || c.Banco != "Banco de Prueba" || c.Numero != "0000-0000-1" || c.TipoNombre() != "Monetaria" {
		t.Fatalf("cuenta guardada = %+v", c)
	}
	if l, _ := List(conn); len(l) != 1 {
		t.Fatalf("List = %d cuentas, quiero 1", len(l))
	}
	h := historial(t, conn)
	if len(h) != 1 || h[0].Accion != "creada" || h[0].Usuario != "ana" || h[0].IP != "203.0.113.7" || h[0].CuentaID != id {
		t.Fatalf("historial = %+v", h)
	}
}

func TestValidacion(t *testing.T) {
	conn := nuevaBase(t)
	casos := map[string]func(*Datos){
		"banco vacío":                  func(d *Datos) { d.Banco = "   " },
		"banco larguísimo":             func(d *Datos) { d.Banco = strings.Repeat("a", 101) },
		"tipo desconocido":             func(d *Datos) { d.TipoCuenta = "corriente" },
		"moneda desconocida":           func(d *Datos) { d.Moneda = "EUR" },
		"número muy corto":             func(d *Datos) { d.Numero = "12" },
		"número con símbolos":          func(d *Datos) { d.Numero = "123;DROP" },
		"número que empieza con guion": func(d *Datos) { d.Numero = "-1234" },
		"titular vacío":                func(d *Datos) { d.Titular = "" },
		"salto de línea en el banco":   func(d *Datos) { d.Banco = "Banco\nBcc: x@y.z" },
		"salto de línea en el titular": func(d *Datos) { d.Titular = "Ana\r\nOtro" },
		"control en instrucciones":     func(d *Datos) { d.Instrucciones = "hola\x00" },
		"instrucciones largas":         func(d *Datos) { d.Instrucciones = strings.Repeat("x", 301) },
		"orden negativo":               func(d *Datos) { d.Orden = -1 },
		"orden enorme":                 func(d *Datos) { d.Orden = 10000 },
	}
	for nombre, rompe := range casos {
		d := valido()
		rompe(&d)
		_, err := Create(conn, d, quien)
		if !errors.Is(err, ErrValidacion) {
			t.Errorf("%s: err = %v, quiero ErrValidacion", nombre, err)
		}
	}
	if l, _ := List(conn); len(l) != 0 {
		t.Fatalf("quedaron %d cuentas guardadas tras datos inválidos", len(l))
	}
	if h := historial(t, conn); len(h) != 0 {
		t.Fatalf("quedaron %d entradas de historial tras datos inválidos", len(h))
	}

	// Un número válido con letras (IBAN) y espacios se acepta.
	d := valido()
	d.Numero = "GT82 TRAJ 0102 0000 0012"
	if _, err := Create(conn, d, quien); err != nil {
		t.Fatalf("número con letras y espacios: %v", err)
	}
}

func TestEditarAnotaSoloLoQueCambio(t *testing.T) {
	conn := nuevaBase(t)
	id, _ := Create(conn, valido(), quien)

	// Sin cambios: no escribe ni anota.
	if err := Update(conn, id, valido(), quien); err != nil {
		t.Fatal(err)
	}
	if h := historial(t, conn); len(h) != 1 {
		t.Fatalf("guardar sin cambios anotó: %d entradas, quiero 1", len(h))
	}

	d := valido()
	d.Numero = "9999-8888-7"
	d.Titular = "Otra Persona"
	otro := Quien{Usuario: "beto", IP: "198.51.100.2"}
	if err := Update(conn, id, d, otro); err != nil {
		t.Fatal(err)
	}
	h := historial(t, conn)
	if len(h) != 2 || h[0].Accion != "editada" || h[0].Usuario != "beto" {
		t.Fatalf("historial = %+v", h)
	}
	for _, quiero := range []string{"número: «0000-0000-1» → «9999-8888-7»", "titular: «Manualidades Cristal» → «Otra Persona»"} {
		if !strings.Contains(h[0].Detalle, quiero) {
			t.Errorf("el detalle %q no menciona %q", h[0].Detalle, quiero)
		}
	}
	if strings.Contains(h[0].Detalle, "banco") {
		t.Errorf("el detalle menciona un campo que no cambió: %q", h[0].Detalle)
	}

	// Una edición inválida no toca nada.
	mala := valido()
	mala.Numero = "x"
	if err := Update(conn, id, mala, quien); !errors.Is(err, ErrValidacion) {
		t.Fatalf("err = %v, quiero ErrValidacion", err)
	}
	if c, _ := Get(conn, id); c.Numero != "9999-8888-7" {
		t.Fatalf("la edición inválida cambió el número a %q", c.Numero)
	}
	if err := Update(conn, 9999, valido(), quien); !errors.Is(err, ErrNoExiste) {
		t.Fatalf("editar una inexistente: err = %v, quiero ErrNoExiste", err)
	}
}

func TestActivarDesactivar(t *testing.T) {
	conn := nuevaBase(t)
	id, _ := Create(conn, valido(), quien)
	otra := valido()
	otra.Numero = "5555-5555-5"
	Create(conn, otra, quien)

	if err := SetActiva(conn, id, false, quien); err != nil {
		t.Fatal(err)
	}
	if l, _ := ListActivas(conn); len(l) != 1 || l[0].ID == id {
		t.Fatalf("ListActivas = %+v; la desactivada no debe salir", l)
	}
	if l, _ := List(conn); len(l) != 2 {
		t.Fatalf("List = %d, quiero 2 (el admin ve todas)", len(l))
	}

	n := len(historial(t, conn))
	if err := SetActiva(conn, id, false, quien); err != nil { // repetir no hace nada
		t.Fatal(err)
	}
	if got := len(historial(t, conn)); got != n {
		t.Fatalf("repetir el estado anotó (%d → %d)", n, got)
	}

	if err := SetActiva(conn, id, true, quien); err != nil {
		t.Fatal(err)
	}
	h := historial(t, conn)
	if h[0].Accion != "activada" || h[1].Accion != "desactivada" {
		t.Fatalf("acciones = %q, %q", h[0].Accion, h[1].Accion)
	}
	if err := SetActiva(conn, 9999, true, quien); !errors.Is(err, ErrNoExiste) {
		t.Fatalf("err = %v, quiero ErrNoExiste", err)
	}
}

func TestEliminarConservaElHistorial(t *testing.T) {
	conn := nuevaBase(t)
	id, _ := Create(conn, valido(), quien)
	if err := Delete(conn, id, quien); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(conn, id); !errors.Is(err, ErrNoExiste) {
		t.Fatalf("Get tras eliminar: err = %v", err)
	}
	h := historial(t, conn)
	if len(h) != 2 || h[0].Accion != "eliminada" || !strings.Contains(h[0].Detalle, "0000-0000-1") {
		t.Fatalf("el historial debe conservar lo eliminado: %+v", h)
	}
	if err := Delete(conn, id, quien); !errors.Is(err, ErrNoExiste) {
		t.Fatalf("eliminar dos veces: err = %v, quiero ErrNoExiste", err)
	}
}

func TestMigrateEsIdempotente(t *testing.T) {
	conn := nuevaBase(t)
	if err := Migrate(conn); err != nil {
		t.Fatalf("segunda migración: %v", err)
	}
}
