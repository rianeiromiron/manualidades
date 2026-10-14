package web

import (
	"strings"
	"testing"

	"manualidades/internal/config"
)

func TestPasswordParaGuardar(t *testing.T) {
	casos := []struct{ enviada, guardada, quiero string }{
		{"", "la-actual", "la-actual"}, // campo vacío: se conserva
		{"nueva", "la-actual", "nueva"},
		{"nueva", "", "nueva"},
		{"", "", ""},
	}
	for _, c := range casos {
		if got := passwordParaGuardar(c.enviada, c.guardada); got != c.quiero {
			t.Errorf("passwordParaGuardar(%q, %q) = %q, quería %q", c.enviada, c.guardada, got, c.quiero)
		}
	}
}

// La contraseña de la base de datos no debe viajar nunca dentro del HTML:
// cualquiera con acceso a esa pantalla podría leerla con "ver código fuente".
func TestConfigDBNoMuestraLaContrasena(t *testing.T) {
	const secreta = "clave-secreta-de-prueba-123"
	out := ejecutarContent(t, "config_db.html", map[string]any{
		"Config": config.DBConfig{Host: "127.0.0.1", Port: 5432, User: "manualidades", Password: secreta, DBName: "manualidades", SSLMode: "disable"},
	})
	if strings.Contains(out, secreta) {
		t.Fatal("la plantilla imprime la contraseña de la base de datos")
	}
	for _, quiero := range []string{`name="password"`, `value="127.0.0.1"`, `value="manualidades"`} {
		if !strings.Contains(out, quiero) {
			t.Errorf("falta %q en config_db", quiero)
		}
	}
	// El recuadro con valores del Docker de desarrollo no debe llegar a producción.
	for _, noQuiero := range []string{"docker inspect", "gestion_documentos", "Docker local"} {
		if strings.Contains(out, noQuiero) {
			t.Errorf("config_db todavía contiene texto de desarrollo: %q", noQuiero)
		}
	}
}
