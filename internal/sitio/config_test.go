package sitio

import (
	"errors"
	"math"
	"testing"

	"manualidades/internal/testdb"
)

func TestCostoDeEnvioPorOmisionEsQ45(t *testing.T) {
	conn := testdb.Nueva(t, Migrate)
	c, err := Get(conn)
	if err != nil {
		t.Fatal(err)
	}
	if c.CostoEnvio != 45 {
		t.Fatalf("costo de envío = %v, quiero 45", c.CostoEnvio)
	}
}

func TestGuardarElCostoDeEnvio(t *testing.T) {
	conn := testdb.Nueva(t, Migrate)
	c, _ := Get(conn)

	c.CostoEnvio = 60
	if err := Save(conn, c); err != nil {
		t.Fatal(err)
	}
	if g, _ := Get(conn); g.CostoEnvio != 60 {
		t.Fatalf("costo guardado = %v, quiero 60", g.CostoEnvio)
	}

	// Se redondea a centavos y el cero es válido (envío gratis).
	c.CostoEnvio = 12.346
	Save(conn, c)
	if g, _ := Get(conn); g.CostoEnvio != 12.35 {
		t.Errorf("12.346 se guardó como %v, quiero 12.35", g.CostoEnvio)
	}
	c.CostoEnvio = 0
	if err := Save(conn, c); err != nil {
		t.Fatalf("envío gratis: %v", err)
	}

	// Montos inválidos se rechazan y no cambian lo guardado.
	c.CostoEnvio = 25
	Save(conn, c)
	for _, malo := range []float64{-1, 10000, math.NaN(), math.Inf(1)} {
		c.CostoEnvio = malo
		if err := Save(conn, c); !errors.Is(err, ErrCostoEnvioInvalido) {
			t.Errorf("%v: err = %v, quiero ErrCostoEnvioInvalido", malo, err)
		}
	}
	if g, _ := Get(conn); g.CostoEnvio != 25 {
		t.Fatalf("un guardado inválido cambió el costo a %v", g.CostoEnvio)
	}
}

func TestMigrarNoReiniciaElCostoYAgregaElCampoAUnaBaseAntigua(t *testing.T) {
	conn := testdb.Nueva(t, Migrate)
	c, _ := Get(conn)
	c.CostoEnvio = 70
	Save(conn, c)
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	if g, _ := Get(conn); g.CostoEnvio != 70 {
		t.Fatalf("migrar de nuevo cambió el costo a %v", g.CostoEnvio)
	}

	// Una base creada antes de este campo lo recibe con el valor por omisión.
	if _, err := conn.Exec(`ALTER TABLE config_sitio DROP COLUMN costo_envio`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	if g, err := Get(conn); err != nil || g.CostoEnvio != 45 {
		t.Fatalf("base antigua: costo = %v, err = %v; quiero 45", g.CostoEnvio, err)
	}
}

func TestLaCascadaNaceApagadaConValoresPorOmision(t *testing.T) {
	conn := testdb.Nueva(t, Migrate)
	c, err := Get(conn)
	if err != nil {
		t.Fatal(err)
	}
	if c.CascadaActiva || c.CascadaOpacidad != 90 || c.CascadaVelocidad != 5 || c.CascadaCantidad != 2 {
		t.Fatalf("cascada por omisión = %+v; quiero apagada, 90, 5 y 2", c)
	}
}

func TestGuardarLosAjustesDeLaCascada(t *testing.T) {
	conn := testdb.Nueva(t, Migrate)
	c, _ := Get(conn)
	c.CascadaActiva, c.CascadaOpacidad, c.CascadaVelocidad, c.CascadaCantidad = true, 55, 8, 3
	if err := Save(conn, c); err != nil {
		t.Fatal(err)
	}
	g, _ := Get(conn)
	if !g.CascadaActiva || g.CascadaOpacidad != 55 || g.CascadaVelocidad != 8 || g.CascadaCantidad != 3 {
		t.Fatalf("guardado = %+v", g)
	}

	// Los extremos de cada rango son válidos.
	for _, ok := range [][3]int{{20, 1, 1}, {100, 10, 3}} {
		c.CascadaOpacidad, c.CascadaVelocidad, c.CascadaCantidad = ok[0], ok[1], ok[2]
		if err := Save(conn, c); err != nil {
			t.Errorf("%v debía ser válido: %v", ok, err)
		}
	}

	// Fuera de rango se rechaza y no cambia lo guardado.
	c.CascadaOpacidad, c.CascadaVelocidad, c.CascadaCantidad = 55, 8, 3
	Save(conn, c)
	for _, malo := range [][3]int{{19, 8, 3}, {101, 8, 3}, {55, 0, 3}, {55, 11, 3}, {55, 8, 0}, {55, 8, 4}, {0, 0, 0}} {
		c.CascadaOpacidad, c.CascadaVelocidad, c.CascadaCantidad = malo[0], malo[1], malo[2]
		if err := Save(conn, c); !errors.Is(err, ErrCascadaInvalida) {
			t.Errorf("%v: err = %v, quiero ErrCascadaInvalida", malo, err)
		}
	}
	if g, _ := Get(conn); g.CascadaOpacidad != 55 || g.CascadaVelocidad != 8 || g.CascadaCantidad != 3 {
		t.Fatalf("un guardado inválido cambió la cascada: %+v", g)
	}
}

func TestMigrarNoReiniciaLaCascadaYAgregaLosCamposAUnaBaseAntigua(t *testing.T) {
	conn := testdb.Nueva(t, Migrate)
	c, _ := Get(conn)
	c.CascadaActiva, c.CascadaOpacidad = true, 40
	Save(conn, c)
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	if g, _ := Get(conn); !g.CascadaActiva || g.CascadaOpacidad != 40 {
		t.Fatalf("migrar de nuevo cambió la cascada: %+v", g)
	}

	// Una base creada antes de la cascada la recibe apagada y con los valores base.
	for _, col := range []string{"cascada_activa", "cascada_opacidad", "cascada_velocidad", "cascada_cantidad"} {
		if _, err := conn.Exec(`ALTER TABLE config_sitio DROP COLUMN ` + col); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	g, err := Get(conn)
	if err != nil || g.CascadaActiva || g.CascadaOpacidad != 90 || g.CascadaVelocidad != 5 || g.CascadaCantidad != 2 {
		t.Fatalf("base antigua: %+v, err = %v", g, err)
	}
}

func TestLasFormasDePagoNacenActivasYSePuedenCambiar(t *testing.T) {
	conn := testdb.Nueva(t, Migrate)
	lista, err := ListFormasPago(conn)
	if err != nil || len(lista) != 2 || lista[0].Clave != FormaTarjeta || lista[1].Clave != FormaTransferencia {
		t.Fatalf("formas = %+v, %v; quiero tarjeta y transferencia en ese orden", lista, err)
	}
	for _, f := range lista {
		if !f.Activa || f.ActualizadoPor != "" || f.ActualizadoEn != nil {
			t.Errorf("%s debe nacer activa y sin cambios: %+v", f.Clave, f)
		}
	}

	if err := SetFormaPagoActiva(conn, FormaTarjeta, false, "silvia"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := FormaPagoActiva(conn, FormaTarjeta); ok {
		t.Error("la tarjeta debía quedar desactivada")
	}
	if ok, _ := FormaPagoActiva(conn, FormaTransferencia); !ok {
		t.Error("desactivar la tarjeta no debe tocar la transferencia")
	}
	lista, _ = ListFormasPago(conn)
	if lista[0].ActualizadoPor != "silvia" || lista[0].ActualizadoEn == nil {
		t.Errorf("debe quedar anotado quién y cuándo: %+v", lista[0])
	}

	// Migrar de nuevo no reactiva lo que se desactivó.
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	if ok, _ := FormaPagoActiva(conn, FormaTarjeta); ok {
		t.Error("migrar de nuevo reactivó la tarjeta")
	}

	if err := SetFormaPagoActiva(conn, FormaTarjeta, true, "beto"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := FormaPagoActiva(conn, FormaTarjeta); !ok {
		t.Error("la tarjeta debía volver a activarse")
	}
}

func TestUnaFormaDePagoDesconocidaSeRechaza(t *testing.T) {
	conn := testdb.Nueva(t, Migrate)
	for _, clave := range []string{"efectivo", "", "TARJETA", "tarjeta' OR '1'='1"} {
		if err := SetFormaPagoActiva(conn, clave, true, "silvia"); !errors.Is(err, ErrFormaPagoDesconocida) {
			t.Errorf("%q: err = %v, quiero ErrFormaPagoDesconocida", clave, err)
		}
		if ok, err := FormaPagoActiva(conn, clave); ok || err != nil {
			t.Errorf("%q: activa = %v, err = %v; una clave desconocida cuenta como desactivada", clave, ok, err)
		}
	}
	if lista, _ := ListFormasPago(conn); len(lista) != 2 {
		t.Errorf("hay %d formas de pago, quiero 2", len(lista))
	}
}
