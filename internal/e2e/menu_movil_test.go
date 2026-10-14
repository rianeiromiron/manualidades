package e2e

import (
	"fmt"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// estadoEnPantalla dice si el elemento se ve de verdad en la ventana actual:
// "ok" si existe, no está oculto y cabe a lo ancho; si no, el motivo.
func (n *navegador) estadoEnPantalla(t *testing.T, selector string) string {
	t.Helper()
	var estado string
	n.js(t, fmt.Sprintf(`(function(sel){
		var el = document.querySelector(sel);
		if (!el) return 'no existe';
		var cs = getComputedStyle(el);
		if (cs.display === 'none' || cs.visibility === 'hidden') return 'oculto';
		var r = el.getBoundingClientRect();
		if (r.width === 0 || r.height === 0) return 'sin tamaño';
		if (r.left < 0 || r.right > window.innerWidth + 0.5) return 'fuera de la pantalla';
		return 'ok';
	})(%q)`, selector), &estado)
	return estado
}

func (n *navegador) sinScrollHorizontal(t *testing.T) bool {
	t.Helper()
	var ok bool
	n.js(t, `document.documentElement.scrollWidth <= window.innerWidth`, &ok)
	return ok
}

// En un celular, las acciones de la cuenta (Cuenta, Sesiones, Cerrar sesión)
// se salían de la pantalla y no se podían usar. Ahora van detrás de un botón ☰.
func TestNavegadorMenuAdminEnElCelular(t *testing.T) {
	e := nuevoEntorno(t, true)
	n := nuevoNavegador(t)

	// --- Celular (390 px de ancho) ---
	n.correr(t, emulation.SetDeviceMetricsOverride(390, 844, 2, true))
	n.correr(t,
		chromedp.Navigate(e.URL+"/admin/login"),
		chromedp.WaitVisible(`input[name="usuario"]`),
		chromedp.SendKeys(`input[name="usuario"]`, adminUsuario),
		chromedp.SendKeys(`input[name="password"]`, adminClave),
		chromedp.Click(`button[type="submit"]`),
		chromedp.WaitVisible(`.nav-burger`),
	)
	n.errores.exigeSinErrores(t, "inicio de sesión desde el celular")

	if s := n.estadoEnPantalla(t, ".nav-burger"); s != "ok" {
		t.Fatalf("el botón del menú no se ve en el celular: %s", s)
	}
	// Con el menú cerrado no debe ocupar sitio ni empujar la página.
	if s := n.estadoEnPantalla(t, `.topnav-right a[href="/admin/sesiones"]`); s == "ok" {
		t.Error("con el menú cerrado, «Sesiones» no debería verse")
	}
	if !n.sinScrollHorizontal(t) {
		t.Error("con el menú cerrado, la página se desborda a lo ancho")
	}

	n.correr(t, chromedp.Click(`.nav-burger`))
	n.espera(t, "al pulsar ☰ aparece el menú", `getComputedStyle(document.querySelector('.topnav-right')).display !== 'none'`)

	for _, selector := range []string{
		`.topnav a[href="/admin"]`,
		`.topnav-right a[href="/admin/cambiar-password"]`,
		`.topnav-right a[href="/admin/sesiones"]`,
		`.topnav-logout button`,
	} {
		if s := n.estadoEnPantalla(t, selector); s != "ok" {
			t.Errorf("con el menú abierto, %s no se ve completo en el celular: %s", selector, s)
		}
	}
	if !n.sinScrollHorizontal(t) {
		t.Error("con el menú abierto, la página se desborda a lo ancho")
	}

	// Volver a pulsar ☰ lo cierra.
	n.correr(t, chromedp.Click(`.nav-burger`))
	n.espera(t, "al pulsar ☰ otra vez se cierra el menú", `getComputedStyle(document.querySelector('.topnav-right')).display === 'none'`)

	// --- Portátil chico (1280 px): tampoco cabe la barra entera, así que usa ☰ ---
	n.correr(t, emulation.SetDeviceMetricsOverride(1280, 800, 1, false))
	n.correr(t, chromedp.Navigate(e.URL+"/admin"), chromedp.WaitVisible(`.nav-burger`))
	if !n.sinScrollHorizontal(t) {
		t.Error("a 1280 px la página se desborda a lo ancho")
	}
	n.correr(t, chromedp.Click(`.nav-burger`))
	n.espera(t, "a 1280 px el menú se abre", `getComputedStyle(document.querySelector('.topnav-right')).display !== 'none'`)
	for _, selector := range []string{`.topnav-right a[href="/admin/sesiones"]`, `.topnav-logout button`} {
		if s := n.estadoEnPantalla(t, selector); s != "ok" {
			t.Errorf("a 1280 px, con el menú abierto, %s no se ve: %s", selector, s)
		}
	}

	// --- Portátil y escritorio: sin botón y con todo a la vista, sin desbordar ---
	for _, ancho := range []int64{1520, 1800, 1920, 2560} {
		n.correr(t, emulation.SetDeviceMetricsOverride(ancho, 900, 1, false))
		n.correr(t, chromedp.Navigate(e.URL+"/admin"), chromedp.WaitVisible(`.topnav-user`))
		if s := n.estadoEnPantalla(t, ".nav-burger"); s == "ok" {
			t.Errorf("a %d px no debería verse el botón ☰", ancho)
		}
		for _, selector := range []string{
			`.topnav a[href="/admin"]`,
			`.topnav-right a[href="/admin/cambiar-password"]`,
			`.topnav-right a[href="/admin/sesiones"]`,
			`.topnav-logout button`,
		} {
			if s := n.estadoEnPantalla(t, selector); s != "ok" {
				t.Errorf("a %d px, %s no se ve completo: %s", ancho, selector, s)
			}
		}
		if !n.sinScrollHorizontal(t) {
			t.Errorf("a %d px la página se desborda a lo ancho", ancho)
		}
	}
	n.errores.exigeSinErrores(t, "menú del panel")
}
