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
