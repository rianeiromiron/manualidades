package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"manualidades/internal/inventario"
)

// productoConStock crea un producto y deja exactamente `stock` unidades.
func productoConStock(t *testing.T, e *entorno, nombre string, stock float64) int {
	t.Helper()
	id, _ := sembrarProducto(t, e.Conn, nombre) // 10 unidades
	if stock < 10 {
		if err := inventario.CreateMovimiento(e.Conn, id, "consumo", 10-stock, "ajuste", false, 0, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

const cantidadEnCarritoJS = `(function(){ var c = JSON.parse(localStorage.getItem('manualidades_carrito')||'[]'); return c.length ? c[0].cantidad : 0; })()`

func (n *navegador) cantidad(t *testing.T) int {
	t.Helper()
	var v float64
	n.js(t, cantidadEnCarritoJS, &v)
	return int(v)
}

// Un producto con una sola unidad no se puede agregar al carrito más de una vez.
func TestNavegadorElCarritoNoPasaDelStockDisponible(t *testing.T) {
	e := nuevoEntorno(t, true)
	id := productoConStock(t, e, "Billetera", 1)
	n := nuevoNavegador(t)

	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitVisible(`.t-agregar`), chromedp.Click(`.t-agregar`))
	n.espera(t, "la primera unidad se agrega y el botón avisa que ya no hay más",
		`(JSON.parse(localStorage.getItem('manualidades_carrito'))||[]).length === 1
		 && document.querySelector('.t-agregar').disabled
		 && document.querySelector('.t-agregar').textContent.includes('máximo')`)

	// Aunque se intente por otro camino, no pasa de lo que hay.
	var agregado float64
	n.js(t, fmt.Sprintf(`Carrito.agregar({productoId: %d, nombre: 'Billetera', precio: 25, stock: 1}, 5).agregado`, id), &agregado)
	if agregado != 0 || n.cantidad(t) != 1 {
		t.Fatalf("agregó %v más; el carrito quedó con %d, quiero 1", agregado, n.cantidad(t))
	}

	// Al recargar, el botón sigue reflejando lo que ya hay en el carrito.
	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitVisible(`.t-agregar`))
	n.espera(t, "tras recargar el botón sigue desactivado",
		`document.querySelector('.t-agregar').disabled && document.querySelector('.t-agregar').textContent.includes('máximo')`)

	// En el carrito, «+» no deja pasar del máximo.
	n.correr(t, chromedp.Navigate(e.URL+"/carrito"), chromedp.WaitVisible(`#tLineas .t-linea`))
	n.espera(t, "en el carrito «+» está desactivado",
		`document.querySelector('[data-accion="mas"]').disabled && document.querySelector('.t-linea-qty span').textContent === '1'`)

	// Si se quita del carrito, el botón vuelve a funcionar.
	n.correr(t, chromedp.Click(`[data-accion="quitar"]`))
	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitVisible(`.t-agregar`))
	n.espera(t, "al quitarlo, el botón vuelve a estar activo",
		`!document.querySelector('.t-agregar').disabled && document.querySelector('.t-agregar').textContent === 'Agregar al carrito'`)
	n.errores.exigeSinErrores(t, "agregar con poco stock")
}

// Hacer clic muchas veces en un producto con 10 unidades se detiene en 10.
func TestNavegadorMuchosClicsSeDetienenEnElStock(t *testing.T) {
	e := nuevoEntorno(t, true)
	productoConStock(t, e, "Taza", 10)
	n := nuevoNavegador(t)

	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitVisible(`.t-agregar`))
	n.js(t, `(function(){ for (var i = 0; i < 25; i++) document.querySelector('.t-agregar').click(); return 1; })()`, new(float64))
	n.espera(t, "se detiene en 10 y el botón queda desactivado",
		`(JSON.parse(localStorage.getItem('manualidades_carrito'))||[])[0].cantidad === 10 && document.querySelector('.t-agregar').disabled`)
	if got := n.cantidad(t); got != 10 {
		t.Fatalf("el carrito quedó con %d unidades, quiero 10", got)
	}
	// El contador del encabezado dice lo mismo.
	n.espera(t, "el contador del carrito dice 10", `document.getElementById('tCartCount').textContent === '10'`)
}

// En la página del producto, pedir más de lo que hay agrega lo posible y lo explica.
func TestNavegadorPedirMasDeLoQueHayEnLaPaginaDelProducto(t *testing.T) {
	e := nuevoEntorno(t, true)
	id := productoConStock(t, e, "Collar", 3)
	n := nuevoNavegador(t)

	n.correr(t, chromedp.Navigate(fmt.Sprintf("%s/producto/%d", e.URL, id)), chromedp.WaitVisible(`#tAgregarBtn`),
		chromedp.SetValue(`#tCantidad`, "5"), chromedp.Click(`#tAgregarBtn`))
	n.espera(t, "se agregan las 3 que hay y se explica",
		`(JSON.parse(localStorage.getItem('manualidades_carrito'))||[])[0].cantidad === 3
		 && !document.getElementById('tAgregarAviso').hidden
		 && document.getElementById('tAgregarAviso').textContent.includes('Solo hay 3')
		 && document.getElementById('tAgregarBtn').disabled`)
	if got := n.cantidad(t); got != 3 {
		t.Fatalf("el carrito quedó con %d, quiero 3", got)
	}
	n.errores.exigeSinErrores(t, "página del producto")
}

// Un carrito que ya quedó con más de lo disponible (de antes de este arreglo) se
// corrige al abrir el carrito y el checkout, y se le avisa al cliente.
func TestNavegadorUnCarritoPasadoDeStockSeCorrige(t *testing.T) {
	e := nuevoEntorno(t, true)
	id := productoConStock(t, e, "Billetera", 1)
	n := nuevoNavegador(t)
	sembrar := func(cantidad, stock int) string {
		return fmt.Sprintf(`localStorage.setItem('manualidades_carrito', JSON.stringify([{productoId: %d, nombre: 'Billetera', precio: 25, foto: '', stock: %d, cantidad: %d}])); 1`, id, stock, cantidad)
	}

	// Tres unidades de un producto con una sola.
	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitVisible(`.t-agregar`))
	n.js(t, sembrar(3, 1), new(float64))
	n.correr(t, chromedp.Navigate(e.URL+"/carrito"), chromedp.WaitVisible(`#tLineas .t-linea`))
	n.espera(t, "el carrito baja a 1 y avisa",
		`document.querySelector('.t-linea-qty span').textContent === '1'
		 && !document.getElementById('tCarritoAviso').hidden
		 && document.getElementById('tCarritoAviso').textContent.includes('de 3 a 1')
		 && document.getElementById('tTotal').textContent === 'Q 25.00'`)
	if got := n.cantidad(t); got != 1 {
		t.Fatalf("lo guardado quedó en %d, quiero 1", got)
	}

	// También en el checkout, que nunca debe pedir más de lo disponible.
	n.js(t, sembrar(3, 1), new(float64))
	n.correr(t, chromedp.Navigate(e.URL+"/checkout"), chromedp.WaitVisible(`#tFormCheckout`))
	n.espera(t, "el checkout corrige y avisa",
		`document.getElementById('tResumenTotal').textContent === 'Q 25.00'
		 && !document.getElementById('tCheckoutError').hidden
		 && document.getElementById('tCheckoutError').textContent.includes('Ajustamos')`)

	// Un producto que ya no tiene nada disponible se quita.
	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitVisible(`.t-agregar`))
	n.js(t, sembrar(2, 0), new(float64))
	n.correr(t, chromedp.Navigate(e.URL+"/carrito"))
	n.espera(t, "se quita y el carrito queda vacío",
		`!document.getElementById('tCarritoVacio').hidden && document.getElementById('tCarritoAviso').textContent.includes('Quitamos')`)
	n.errores.exigeSinErrores(t, "carrito con stock pasado")
}
