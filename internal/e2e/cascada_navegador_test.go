package e2e

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"manualidades/internal/sitio"
)

// estiloDe devuelve una propiedad CSS calculada por el navegador.
func (n *navegador) estiloDe(t *testing.T, selector, propiedad string) string {
	t.Helper()
	var v string
	n.js(t, `(function(){var el=document.querySelector(`+"`"+selector+"`"+`);
		return el ? getComputedStyle(el).getPropertyValue(`+"`"+propiedad+"`"+`) : 'no existe';})()`, &v)
	return v
}

// cuantasFiguras cuenta las figuras distintas que dibujó el script en la
// cascada que hay en pantalla.
func (n *navegador) cuantasFiguras(t *testing.T, selector string) int {
	t.Helper()
	var c float64
	n.js(t, `(document.querySelector(`+"`"+selector+"`"+`)||{})._puntos || 0`, &c)
	return int(c)
}

// La cascada se ve en la tienda con lo configurado en el admin, y no rompe nada
// (ni errores de JavaScript ni bloqueos de la política de seguridad).
func TestNavegadorCascadaEnLaTienda(t *testing.T) {
	// Con catálogo (producto y categoría), como una tienda real.
	e := nuevoEntornoTransferencia(t, true)
	n := nuevoNavegador(t)

	cfg, err := sitio.Get(e.Conn)
	if err != nil {
		t.Fatal(err)
	}

	// Apagada: no hay cascada y el body pinta el color de fondo de siempre.
	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitReady("body"))
	if v := n.estiloDe(t, ".fondo-cascada", "opacity"); v != "no existe" {
		t.Errorf("con la cascada apagada existe .fondo-cascada (opacity %q)", v)
	}
	if v := n.estiloDe(t, "body", "background-color"); v == "rgba(0, 0, 0, 0)" {
		t.Error("con la cascada apagada el body debe seguir pintando su fondo")
	}
	n.errores.exigeSinErrores(t, "tienda sin cascada")

	// Encendida: 60 % de opacidad, velocidad 5 y bastantes figuras.
	cfg.CascadaActiva, cfg.CascadaOpacidad, cfg.CascadaVelocidad, cfg.CascadaCantidad = true, 60, 5, 3
	if err := sitio.Save(e.Conn, cfg); err != nil {
		t.Fatal(err)
	}
	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitReady("body"))
	n.espera(t, "el script dibuja las figuras", `document.querySelectorAll('.cascada-capa use').length > 0`)
	if v := n.estiloDe(t, ".fondo-cascada", "opacity"); v != "0.6" {
		t.Errorf("opacidad = %q, quiero 0.6", v)
	}
	if v := n.estiloDe(t, "body", "background-color"); v != "rgba(0, 0, 0, 0)" {
		t.Errorf("con la cascada el body debe ser transparente, es %q", v)
	}

	// La caída es continua: la capa mide dos alturas y baja exactamente una; la
	// distribución es más alta que la ventana, así que nada se repite a la vista.
	var alto, altoCapa, ventana, duracion float64
	n.js(t, `parseFloat(getComputedStyle(document.querySelector('.fondo-cascada')).getPropertyValue('--cascada-alto'))`, &alto)
	n.js(t, `document.querySelector('.cascada-capa').getBoundingClientRect().height`, &altoCapa)
	n.js(t, `window.innerHeight`, &ventana)
	n.js(t, `parseFloat(getComputedStyle(document.querySelector('.cascada-capa')).animationDuration)`, &duracion)
	if alto <= ventana {
		t.Errorf("el periodo (%v px) debe ser mayor que la ventana (%v px) para que no se repita a la vista", alto, ventana)
	}
	if math.Abs(altoCapa-2*alto) > 1 {
		t.Errorf("la capa mide %v px, quiero el doble del periodo (%v)", altoCapa, 2*alto)
	}
	// Velocidad 5 = 20 px por segundo.
	if v := alto / duracion; math.Abs(v-20) > 0.5 {
		t.Errorf("cae a %.1f px/s, quiero 20", v)
	}
	time.Sleep(500 * time.Millisecond)
	n.errores.exigeSinErrores(t, "tienda con cascada")

	// Más cantidad = más figuras: pocas < normal < bastantes.
	cuantas := map[int]int{}
	for _, cantidad := range []int{1, 2, 3} {
		cfg.CascadaCantidad = cantidad
		if err := sitio.Save(e.Conn, cfg); err != nil {
			t.Fatal(err)
		}
		n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitReady("body"))
		n.espera(t, "se dibujan las figuras", `document.querySelectorAll('.cascada-capa use').length > 0`)
		cuantas[cantidad] = n.cuantasFiguras(t, ".fondo-cascada")
	}
	if !(cuantas[1] > 0 && cuantas[1] < cuantas[2] && cuantas[2] < cuantas[3]) {
		t.Errorf("figuras por cantidad = %v; quiero pocas < normal < bastantes", cuantas)
	}

	// La velocidad cambia la duración: nivel 10 = 40 px/s.
	cfg.CascadaVelocidad, cfg.CascadaCantidad = 10, 3
	sitio.Save(e.Conn, cfg)
	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitReady("body"))
	n.espera(t, "se dibujan las figuras", `document.querySelectorAll('.cascada-capa use').length > 0`)
	n.js(t, `parseFloat(getComputedStyle(document.querySelector('.fondo-cascada')).getPropertyValue('--cascada-alto'))`, &alto)
	n.js(t, `parseFloat(getComputedStyle(document.querySelector('.cascada-capa')).animationDuration)`, &duracion)
	if v := alto / duracion; math.Abs(v-40) > 0.5 {
		t.Errorf("velocidad 10: cae a %.1f px/s, quiero 40", v)
	}
}

// Lo que se pidió: nada de patrón repetido y ninguna figura encima de otra.
func TestNavegadorCascadaAlAzarYSinTraslapes(t *testing.T) {
	e := nuevoEntornoTransferencia(t, true)
	n := nuevoNavegador(t)
	n.correr(t, chromedp.Navigate(e.URL+"/admin/login"), chromedp.WaitReady("body"))
	// cascada.js se puede usar suelto: se carga en una página cualquiera.
	n.js(t, `new Promise(function(ok){var s=document.createElement('script');
		s.src='/static/js/cascada.js';s.onload=function(){ok(true)};document.head.appendChild(s);})`, nil)
	n.espera(t, "cascada.js cargó", `!!window.Cascada`)

	// Distancia mínima real entre cualquier par de puntos (con la vuelta
	// vertical) frente a la que se pidió, para las tres cantidades y varios tamaños.
	for _, caso := range []struct {
		ancho, alto, dmin int
	}{{1600, 1100, 90}, {1600, 1100, 120}, {1600, 1100, 170}, {390, 1000, 90}, {1920, 1300, 90}, {80, 400, 90}} {
		var minima, cuantos float64
		n.js(t, `(function(w,h,d){
			var p = window.Cascada.generarPuntos(w,h,d), m = Infinity;
			for (var i=0;i<p.length;i++) for (var j=i+1;j<p.length;j++) {
				var dx=p[i].x-p[j].x, dy=Math.abs(p[i].y-p[j].y); dy=Math.min(dy,h-dy);
				m=Math.min(m,Math.sqrt(dx*dx+dy*dy));
			}
			window.__n = p.length; return m;
		})(`+strconv.Itoa(caso.ancho)+`,`+strconv.Itoa(caso.alto)+`,`+strconv.Itoa(caso.dmin)+`)`, &minima)
		n.js(t, `window.__n`, &cuantos)
		if cuantos < 1 {
			t.Errorf("%+v: no generó figuras", caso)
		}
		// Con una sola figura la distancia mínima es infinita (JSON la da como nula).
		if cuantos > 1 && minima < float64(caso.dmin) {
			t.Errorf("%+v: dos figuras quedaron a %.1f px, menos de los %d mínimos (se traslapan)", caso, minima, caso.dmin)
		}
	}

	// Dos generaciones seguidas no se parecen: es al azar, no un patrón fijo.
	var iguales bool
	n.js(t, `(function(){
		var a = JSON.stringify(window.Cascada.generarPuntos(1600,1100,120));
		var b = JSON.stringify(window.Cascada.generarPuntos(1600,1100,120));
		return a === b;
	})()`, &iguales)
	if iguales {
		t.Error("dos distribuciones consecutivas salieron idénticas")
	}

	// Sin repetición a corta distancia: si hubiera un mosaico, muchos puntos
	// compartirían la misma columna. Al azar, las columnas se reparten.
	var columnasDistintas float64
	n.js(t, `(function(){
		var p = window.Cascada.generarPuntos(1600,1100,90), s = {};
		p.forEach(function(q){ s[Math.round(q.x/4)] = 1; });
		return Object.keys(s).length / p.length;
	})()`, &columnasDistintas)
	if columnasDistintas < 0.7 {
		t.Errorf("solo el %.0f%% de las figuras tiene una columna propia; parece un mosaico", columnasDistintas*100)
	}
}

// Los controles del admin mueven la vista previa en vivo, sin errores.
func TestNavegadorCascadaVistaPreviaEnElAdmin(t *testing.T) {
	e := nuevoEntorno(t, true)
	n := nuevoNavegador(t)
	n.iniciaSesionAdmin(t, e.URL)

	n.correr(t, chromedp.Navigate(e.URL+"/admin/sitio"), chromedp.WaitVisible("#cascadaPrevia", chromedp.ByID))
	n.espera(t, "la vista previa dibuja figuras", `document.querySelectorAll('#cascadaPrevia use').length > 0`)
	n.errores.exigeSinErrores(t, "admin sitio")

	// Apagada de inicio: el bloque se atenúa.
	var apagada bool
	n.js(t, `document.getElementById('cascadaBloque').classList.contains('cascada-apagada')`, &apagada)
	if !apagada {
		t.Error("con la cascada apagada el bloque debe verse atenuado")
	}
	normal := n.cuantasFiguras(t, "#cascadaPrevia")

	// Se mueven los controles como lo haría una persona.
	n.js(t, `(function(){
		var a=document.getElementById('cascadaActiva'); a.checked=true; a.dispatchEvent(new Event('change'));
		var o=document.getElementById('cascadaOpacidad'); o.value=40; o.dispatchEvent(new Event('input'));
		var v=document.getElementById('cascadaVelocidad'); v.value=10; v.dispatchEvent(new Event('input'));
		var c=document.getElementById('cascadaCantidad'); c.value=1; c.dispatchEvent(new Event('change'));
	})()`, nil)

	if v := n.estiloDe(t, "#cascadaPrevia", "opacity"); v != "0.4" {
		t.Errorf("opacidad de la vista previa = %q, quiero 0.4", v)
	}
	if pocas := n.cuantasFiguras(t, "#cascadaPrevia"); pocas == 0 || pocas >= normal {
		t.Errorf("con «pocas figuras» la vista previa tiene %d figuras y con «normal» tenía %d; quiero menos", pocas, normal)
	}
	var alto, duracion float64
	n.js(t, `parseFloat(getComputedStyle(document.getElementById('cascadaPrevia')).getPropertyValue('--cascada-alto'))`, &alto)
	n.js(t, `parseFloat(getComputedStyle(document.querySelector('#cascadaPrevia .cascada-capa')).animationDuration)`, &duracion)
	if v := alto / duracion; math.Abs(v-40) > 0.5 {
		t.Errorf("velocidad 10: la vista previa cae a %.1f px/s, quiero 40", v)
	}
	var texto string
	n.js(t, `document.getElementById('cascadaOpacidadValor').textContent`, &texto)
	if texto != "40%" {
		t.Errorf("el rótulo de opacidad dice %q, quiero 40%%", texto)
	}
	n.js(t, `document.getElementById('cascadaBloque').classList.contains('cascada-apagada')`, &apagada)
	if apagada {
		t.Error("al activar la cascada el bloque debe dejar de verse atenuado")
	}
	n.errores.exigeSinErrores(t, "admin sitio tras mover los controles")
}
