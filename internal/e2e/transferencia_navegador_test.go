package e2e

import (
	"testing"

	"github.com/chromedp/chromedp"
)

// El selector de forma de pago del checkout (JavaScript) y el recorrido hasta la
// página del pedido, en un navegador real y con la política CSP activa.
func TestNavegadorCheckoutPorTransferencia(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	n := nuevoNavegador(t)

	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitVisible(`.t-agregar`), chromedp.Click(`.t-agregar`))
	n.espera(t, "el producto queda en el carrito", `(JSON.parse(localStorage.getItem('manualidades_carrito'))||[]).length === 1`)
	n.correr(t, chromedp.Navigate(e.URL+"/checkout"), chromedp.WaitVisible(`#tFormCheckout`))

	// Por omisión: tarjeta, con sus campos y el correo opcional.
	n.espera(t, "por omisión se paga con tarjeta",
		`document.querySelector('input[name="metodo_pago"]:checked').value === 'tarjeta' && !document.getElementById('tTarjetaCampos').hidden && document.getElementById('tPagarBtn').textContent === 'Pagar'`)
	n.js(t, `document.querySelector('input[name="cliente_email"]').required === false`, new(bool))

	// Transferencia: se ocultan los campos de tarjeta, el correo pasa a ser obligatorio.
	n.correr(t, chromedp.Click(`input[name="metodo_pago"][value="transferencia"]`))
	n.espera(t, "al elegir transferencia cambia el formulario",
		`document.getElementById('tTarjetaCampos').hidden
		 && !document.getElementById('tTransferenciaInfo').hidden
		 && document.querySelector('input[name="cliente_email"]').required
		 && Array.from(document.querySelectorAll('#tTarjetaCampos input')).every(i => !i.required)
		 && document.getElementById('tPagarBtn').textContent === 'Continuar con la transferencia'`)

	// Volver a tarjeta restablece todo.
	n.correr(t, chromedp.Click(`input[name="metodo_pago"][value="tarjeta"]`))
	n.espera(t, "al volver a tarjeta se restablecen los campos",
		`!document.getElementById('tTarjetaCampos').hidden && !document.querySelector('input[name="cliente_email"]').required
		 && Array.from(document.querySelectorAll('#tTarjetaCampos input')).every(i => i.required)
		 && document.getElementById('tPagarBtn').textContent === 'Pagar'`)

	// El envío a domicilio se suma al total que se ve, y se quita al recoger.
	n.espera(t, "recoger: sin línea de envío y total del producto",
		`document.getElementById('tResumenEnvio').hidden && document.getElementById('tResumenTotal').textContent === 'Q 25.00'`)
	n.correr(t, chromedp.Click(`input[name="metodo_entrega"][value="domicilio"]`))
	n.espera(t, "domicilio: aparece el envío y el total lo incluye",
		`!document.getElementById('tResumenEnvio').hidden && document.getElementById('tResumenEnvioMonto').textContent === 'Q 45.00' && document.getElementById('tResumenTotal').textContent === 'Q 70.00'`)
	n.correr(t, chromedp.Click(`input[name="metodo_entrega"][value="recoger"]`))
	n.espera(t, "al volver a recoger se quita el envío",
		`document.getElementById('tResumenEnvio').hidden && document.getElementById('tResumenTotal').textContent === 'Q 25.00'`)

	// Con la transferencia elegida, el aviso y el botón ocupan todo el ancho del
	// formulario (antes cada uno quedaba a media columna).
	n.correr(t, chromedp.Click(`input[name="metodo_pago"][value="transferencia"]`))
	n.espera(t, "el aviso y el botón ocupan todo el ancho",
		`(function(){
			var f = document.getElementById('tFormCheckout').getBoundingClientRect().width;
			var aviso = document.getElementById('tTransferenciaInfo').getBoundingClientRect().width;
			var boton = document.getElementById('tPagarBtn').getBoundingClientRect().width;
			return aviso > f * 0.95 && boton > f * 0.95;
		})()`)

	// Recorrido: transferencia → página del pedido.
	n.correr(t,
		chromedp.Click(`input[name="metodo_pago"][value="transferencia"]`),
		chromedp.SendKeys(`input[name="cliente_nombre"]`, "Ana Pérez"),
		chromedp.SendKeys(`input[name="cliente_telefono"]`, "5555-1234"),
		chromedp.SendKeys(`input[name="cliente_email"]`, "ana@ejemplo.com"),
		chromedp.Click(`#tPagarBtn`),
	)
	n.espera(t, "se llega a la página privada del pedido",
		`/^\/pedido\/[0-9a-f-]{36}$/.test(location.pathname) && document.body.innerText.includes('Haz la transferencia')`)
	n.espera(t, "se ve la cuenta bancaria y el formulario del comprobante",
		`document.body.innerText.includes('0000-0000-1') && !!document.querySelector('input[type="file"][name="comprobante"]')`)
	n.espera(t, "el carrito quedó vacío", `(JSON.parse(localStorage.getItem('manualidades_carrito'))||[]).length === 0`)

	// «Atrás» no debe devolver un formulario trabado en «Procesando…»: el pedido
	// ya se creó y el carrito está vacío.
	n.correr(t, chromedp.NavigateBack())
	n.espera(t, "al volver atrás el checkout no queda trabado",
		`location.pathname === '/checkout' && !document.getElementById('tCheckoutVacio').hidden
		 && document.getElementById('tCheckoutContenido').hidden && !document.getElementById('tPagarBtn').disabled`)

	var estado string
	e.Conn.QueryRow(`SELECT estado FROM pedidos`).Scan(&estado)
	if estado != "espera_transferencia" {
		t.Errorf("estado del pedido = %q", estado)
	}
	n.errores.exigeSinErrores(t, "checkout por transferencia")
}
