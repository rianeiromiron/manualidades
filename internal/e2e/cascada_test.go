package e2e

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// guardaSitio envía el formulario de Sitio web como lo hace el navegador, con
// los datos mínimos del negocio más los campos de la cascada que se pasen.
func guardaSitio(t *testing.T, admin *cliente, csrf string, cascada url.Values) string {
	t.Helper()
	datos := url.Values{
		"csrf":           {csrf},
		"nombre_negocio": {"Manualidades Cristal"},
		"color_fondo":    {"#FBE9EC"},
		"color_texto":    {"#1F2A44"},
		"color_marco":    {"#8A8F98"},
		"color_acento":   {"#C1592B"},
	}
	for k, v := range cascada {
		datos[k] = v
	}
	resp, body := admin.postMultipart("/admin/sitio", datos)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("guardar sitio: %d", resp.StatusCode)
	}
	return body
}

func TestLaTiendaNoTraeLaCascadaPorOmision(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	for _, ruta := range []string{"/", "/carrito"} {
		_, body := e.cliente().get(ruta)
		for _, noQuiero := range []string{"fondo-cascada", "cascada.css", "cascada.js", "con-cascada", "--cascada-"} {
			if strings.Contains(body, noQuiero) {
				t.Errorf("%s: con la cascada apagada la página no debe llevar %q", ruta, noQuiero)
			}
		}
	}
}

func TestActivarLaCascadaDesdeElAdminCambiaLaTienda(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()

	// La pantalla de admin ofrece los controles, con la cascada apagada.
	_, body := admin.get("/admin/sitio")
	for _, quiero := range []string{"cascada_activa", "cascada_opacidad", "cascada_velocidad", "cascada_cantidad", "cascadaPrevia"} {
		if !strings.Contains(body, quiero) {
			t.Errorf("el admin no muestra %q", quiero)
		}
	}
	if strings.Contains(body, `id="cascadaActiva" value="1" checked`) {
		t.Error("la cascada debe verse apagada por omisión")
	}

	// Se activa con opacidad 60, velocidad 8 y bastantes figuras.
	body = guardaSitio(t, admin, csrf, url.Values{
		"cascada_activa": {"1"}, "cascada_opacidad": {"60"}, "cascada_velocidad": {"8"}, "cascada_cantidad": {"3"},
	})
	if !strings.Contains(body, "Configuración guardada.") {
		t.Fatalf("no confirmó el guardado:\n%s", body)
	}

	_, tienda := e.cliente().get("/")
	for _, quiero := range []string{
		`class="con-cascada"`, `/static/css/cascada.css`, `/static/js/cascada.js`,
		`class="fondo-cascada" data-cantidad="3" data-velocidad="8"`,
		"--cascada-opacidad: 60",
	} {
		if !strings.Contains(tienda, quiero) {
			t.Errorf("la tienda con cascada no trae %q", quiero)
		}
	}

	// La hoja de estilos y el script se sirven (la CSP solo deja archivos propios).
	c := e.cliente()
	for _, ruta := range []string{"/static/css/cascada.css", "/static/js/cascada.js"} {
		if resp, _ := c.get(ruta); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", ruta, resp.StatusCode)
		}
	}

	// Se apaga: el formulario sin el checkbox deja la tienda como al principio,
	// y los ajustes numéricos se conservan.
	guardaSitio(t, admin, csrf, url.Values{"cascada_opacidad": {"60"}, "cascada_velocidad": {"8"}, "cascada_cantidad": {"3"}})
	_, tienda = e.cliente().get("/")
	if strings.Contains(tienda, "fondo-cascada") || strings.Contains(tienda, "con-cascada") {
		t.Error("al apagar la cascada la tienda debe volver a verse como antes")
	}
	_, body = admin.get("/admin/sitio")
	if !strings.Contains(body, `value="60"`) || !strings.Contains(body, `<option value="3" selected>`) {
		t.Error("al apagar la cascada se perdieron los ajustes guardados")
	}
}

func TestLosAjustesInvalidosDeLaCascadaSeRechazan(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	admin := e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()

	for _, malo := range []url.Values{
		{"cascada_activa": {"1"}, "cascada_opacidad": {"5"}},
		{"cascada_activa": {"1"}, "cascada_opacidad": {"500"}},
		{"cascada_activa": {"1"}, "cascada_velocidad": {"0"}},
		{"cascada_activa": {"1"}, "cascada_velocidad": {"99"}},
		{"cascada_activa": {"1"}, "cascada_cantidad": {"7"}},
		{"cascada_activa": {"1"}, "cascada_opacidad": {"90;} body{display:none"}},
		{"cascada_activa": {"1"}, "cascada_velocidad": {"abc"}},
	} {
		body := guardaSitio(t, admin, csrf, malo)
		if strings.Contains(body, "Configuración guardada.") {
			t.Errorf("%v: se aceptó un valor inválido", malo)
		}
	}
	// Nada de eso llegó a la tienda.
	_, tienda := e.cliente().get("/")
	if strings.Contains(tienda, "fondo-cascada") || strings.Contains(tienda, "display:none") {
		t.Error("un ajuste inválido se filtró a la tienda")
	}
}
