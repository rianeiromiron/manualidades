package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"manualidades/internal/inventario"
)

// ---------------------------------------------------------------------------
// Infraestructura: Chrome headless + recolector de errores de JavaScript/CSP
// ---------------------------------------------------------------------------

// buscarNavegador devuelve un Chrome/Edge instalado (o el indicado en
// E2E_CHROME); "" si no hay ninguno.
func buscarNavegador() string {
	candidatos := []string{
		os.Getenv("E2E_CHROME"),
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		"/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}
	for _, c := range candidatos {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// recolector guarda todo lo que el navegador reporta como falla de la página:
// excepciones de JavaScript, console.error y avisos de seguridad (entre ellos
// «Refused to execute inline script…», que es la CSP bloqueando algo).
type recolector struct {
	mu      sync.Mutex
	errores []string
}

func (r *recolector) add(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errores = append(r.errores, fmt.Sprintf(format, args...))
}

func (r *recolector) lista() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.errores...)
}

func (r *recolector) vaciar() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errores = nil
}

// exigeSinErrores falla si el navegador reportó algo desde la última vez.
func (r *recolector) exigeSinErrores(t *testing.T, donde string) {
	t.Helper()
	// Los eventos llegan de forma asíncrona: un instante de margen.
	time.Sleep(150 * time.Millisecond)
	if errs := r.lista(); len(errs) > 0 {
		t.Errorf("%s: el navegador reportó errores:\n  - %s", donde, strings.Join(errs, "\n  - "))
	}
	r.vaciar()
}

type navegador struct {
	ctx       context.Context
	errores   *recolector
	dialogos  *[]string
	aceptar   *atomic.Bool
	dialogosM *sync.Mutex
}

func nuevoNavegador(t *testing.T) *navegador {
	t.Helper()
	ruta := buscarNavegador()
	if ruta == "" {
		t.Skip("no hay Chrome/Edge instalado (se puede indicar con E2E_CHROME)")
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(ruta),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(cancelAlloc)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelCtx)
	ctx, cancelT := context.WithTimeout(ctx, 150*time.Second)
	t.Cleanup(cancelT)

	nav := &navegador{
		ctx:       ctx,
		errores:   &recolector{},
		dialogos:  new([]string),
		aceptar:   new(atomic.Bool),
		dialogosM: new(sync.Mutex),
	}

	chromedp.ListenTarget(ctx, func(ev any) {
		switch e := ev.(type) {
		case *runtime.EventExceptionThrown:
			txt := e.ExceptionDetails.Text
			if e.ExceptionDetails.Exception != nil {
				txt += " " + e.ExceptionDetails.Exception.Description
			}
			nav.errores.add("excepción de JS: %s", txt)
		case *runtime.EventConsoleAPICalled:
			if e.Type == runtime.APITypeError {
				var partes []string
				for _, a := range e.Args {
					partes = append(partes, strings.Trim(string(a.Value), `"`))
				}
				nav.errores.add("console.error: %s", strings.Join(partes, " "))
			}
		case *log.EventEntryAdded:
			// Las fallas de red (p. ej. las fuentes de Google, bloqueadas a
			// propósito) no son errores de la aplicación.
			if e.Entry.Level == log.LevelError && e.Entry.Source != log.SourceNetwork {
				nav.errores.add("%s: %s", e.Entry.Source, e.Entry.Text)
			}
		case *page.EventJavascriptDialogOpening:
			msg := e.Message
			go func() {
				nav.dialogosM.Lock()
				*nav.dialogos = append(*nav.dialogos, msg)
				nav.dialogosM.Unlock()
				chromedp.Run(ctx, page.HandleJavaScriptDialog(nav.aceptar.Load()))
			}()
		}
	})

	err := chromedp.Run(ctx,
		log.Enable(),
		runtime.Enable(),
		network.Enable(),
		network.SetBlockedURLs([]string{"*fonts.googleapis.com*", "*fonts.gstatic.com*"}),
	)
	if err != nil {
		t.Skipf("no se pudo iniciar el navegador (%s): %v", ruta, err)
	}
	return nav
}

func (n *navegador) correr(t *testing.T, acciones ...chromedp.Action) {
	t.Helper()
	if err := chromedp.Run(n.ctx, acciones...); err != nil {
		t.Fatalf("acción de navegador falló: %v", err)
	}
}

// js evalúa una expresión en la página y deja el resultado en out.
func (n *navegador) js(t *testing.T, expr string, out any) {
	t.Helper()
	if out == nil {
		// Sin resultado que leer: se fuerza un valor serializable para que
		// una expresión que devuelve undefined no cuente como error.
		expr = "(" + expr + ", true)"
		out = new(bool)
	}
	n.correr(t, chromedp.Evaluate(expr, out))
}

// marca asigna un id al primer elemento que cumpla el selector CSS dentro del
// JavaScript dado, para poder hacerle clic con certeza.
func (n *navegador) marca(t *testing.T, buscar, id string) {
	t.Helper()
	var ok bool
	n.js(t, fmt.Sprintf(`(function(){var el=%s; if(!el) return false; el.id=%q; return true;})()`, buscar, id), &ok)
	if !ok {
		t.Fatalf("no se encontró el elemento para marcar como #%s", id)
	}
}

func (n *navegador) dialogosVistos() []string {
	n.dialogosM.Lock()
	defer n.dialogosM.Unlock()
	return append([]string(nil), *n.dialogos...)
}

// espera repite cond cada 100 ms hasta que sea verdadera o venza el tiempo.
func (n *navegador) espera(t *testing.T, descripcion, expr string) {
	t.Helper()
	limite := time.Now().Add(8 * time.Second)
	for time.Now().Before(limite) {
		var ok bool
		if err := chromedp.Run(n.ctx, chromedp.Evaluate(expr, &ok)); err == nil && ok {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no se cumplió a tiempo: %s", descripcion)
}

func (n *navegador) iniciaSesionAdmin(t *testing.T, base string) {
	t.Helper()
	n.correr(t,
		chromedp.Navigate(base+"/admin/login"),
		chromedp.WaitVisible(`input[name="usuario"]`),
		chromedp.SendKeys(`input[name="usuario"]`, adminUsuario),
		chromedp.SendKeys(`input[name="password"]`, adminClave),
		chromedp.Click(`button[type="submit"]`),
		chromedp.WaitVisible(`.topnav-user`),
	)
}

// ---------------------------------------------------------------------------
// Datos de ejemplo
// ---------------------------------------------------------------------------

// nombreHostil es un nombre de producto pensado para romper cualquier
// concatenación de HTML: etiquetas, comillas de ambos tipos y ampersand.
const nombreHostil = `<img src=x onerror="window.__xss=1"><b id="inj">N</b> "q" & 'a'`

func sembrarProducto(t *testing.T, conn *sql.DB, nombre string) (productoID, categoriaID int) {
	t.Helper()
	if err := inventario.CreateCategoria(conn, "General", "ejemplo"); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT id FROM categorias WHERE nombre = 'General'`).Scan(&categoriaID); err != nil {
		t.Fatal(err)
	}
	id, err := inventario.CreateProducto(conn, inventario.Producto{
		CategoriaID: categoriaID, Nombre: nombre, Descripcion: "desc", PrecioCompra: 10, PrecioVenta: 25, Activo: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := inventario.CreateMovimiento(conn, id, "ingreso", 10, "inicial", false, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	return id, categoriaID
}

// ---------------------------------------------------------------------------
// XSS almacenado en el carrito + CSP
// ---------------------------------------------------------------------------

// Un producto con HTML en el nombre debe mostrarse como texto en el carrito.
// OJO: la CSP por sí sola ya impediría que corra el onerror, así que no basta
// con comprobar que no hubo alerta; se verifica además que el nombre NO se
// interpretó como HTML (no aparece un <img> ni un <b> que no existían), que
// es lo que fallaba con la versión anterior del carrito (innerHTML).
func TestNavegadorXSSAlmacenadoEnElCarrito(t *testing.T) {
	e := nuevoEntorno(t, true)
	productoID, _ := sembrarProducto(t, e.Conn, nombreHostil)
	n := nuevoNavegador(t)

	n.correr(t,
		chromedp.Navigate(fmt.Sprintf("%s/producto/%d", e.URL, productoID)),
		chromedp.WaitVisible(`#tAgregarBtn`),
		chromedp.Click(`#tAgregarBtn`),
	)

	// El nombre llega intacto del servidor al carrito (atributos data-*).
	var guardado string
	n.js(t, `(JSON.parse(localStorage.getItem('manualidades_carrito'))[0] || {}).nombre`, &guardado)
	if guardado != nombreHostil {
		t.Fatalf("el nombre se alteró en el camino: %q", guardado)
	}

	n.correr(t, chromedp.Navigate(e.URL+"/carrito"), chromedp.WaitVisible(`#tLineas strong`))

	var res struct {
		Xss          string `json:"xss"`
		ImgInyect    int    `json:"imgInyect"`
		BoldInyect   int    `json:"boldInyect"`
		Texto        string `json:"texto"`
		HTMLEscapado bool   `json:"htmlEscapado"`
	}
	n.js(t, `({
		xss: typeof window.__xss,
		imgInyect: document.querySelectorAll('#tLineas img[src="x"]').length,
		boldInyect: document.querySelectorAll('#tLineas #inj, #tLineas strong b').length,
		texto: document.querySelector('#tLineas strong').textContent,
		htmlEscapado: document.querySelector('#tLineas strong').innerHTML.indexOf('&lt;img') !== -1
	})`, &res)

	if res.Xss != "undefined" {
		t.Error("se ejecutó el JavaScript del nombre del producto")
	}
	if res.ImgInyect != 0 || res.BoldInyect != 0 {
		t.Errorf("el nombre se interpretó como HTML (img inyectadas: %d, <b> inyectados: %d)", res.ImgInyect, res.BoldInyect)
	}
	if res.Texto != nombreHostil {
		t.Errorf("el carrito muestra %q, se esperaba el nombre literal %q", res.Texto, nombreHostil)
	}
	if !res.HTMLEscapado {
		t.Error("el nombre no quedó como texto escapado en el DOM")
	}

	// La misma protección en el resumen del checkout.
	n.correr(t, chromedp.Navigate(e.URL+"/checkout"), chromedp.WaitVisible(`#tResumenLineas td`))
	var checkoutTexto string
	n.js(t, `document.querySelector('#tResumenLineas td').textContent`, &checkoutTexto)
	if checkoutTexto != nombreHostil {
		t.Errorf("el checkout muestra %q", checkoutTexto)
	}
	n.js(t, `typeof window.__xss`, &res.Xss)
	if res.Xss != "undefined" {
		t.Error("se ejecutó JavaScript en el checkout")
	}
}

// La CSP debe bloquear de verdad el JavaScript que alguien lograra inyectar.
func TestNavegadorCSPBloqueaJavaScriptInyectado(t *testing.T) {
	e := nuevoEntorno(t, true)
	n := nuevoNavegador(t)
	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitReady(`body`))
	n.errores.vaciar()

	var res struct {
		ScriptInline string `json:"scriptInline"`
		Handler      string `json:"handler"`
		JSURL        string `json:"jsUrl"`
	}
	n.js(t, `(function () {
		var s = document.createElement('script');
		s.textContent = 'window.__scriptInline = 1';
		document.body.appendChild(s);

		var b = document.createElement('button');
		b.setAttribute('onclick', 'window.__handler = 1');
		document.body.appendChild(b);
		b.click();

		var a = document.createElement('a');
		a.href = 'javascript:window.__jsurl = 1';
		document.body.appendChild(a);
		a.click();

		return { scriptInline: typeof window.__scriptInline, handler: typeof window.__handler, jsUrl: typeof window.__jsurl };
	})()`, &res)

	if res.ScriptInline != "undefined" {
		t.Error("la CSP dejó ejecutar un <script> inline inyectado")
	}
	if res.Handler != "undefined" {
		t.Error("la CSP dejó ejecutar un atributo onclick inyectado")
	}
	if res.JSURL != "undefined" {
		t.Error("la CSP dejó ejecutar una URL javascript: inyectada")
	}
	// Y el navegador lo registró como violación de la política (prueba de que
	// la política está activa y es la que bloquea, no un accidente).
	time.Sleep(300 * time.Millisecond)
	var csp bool
	for _, m := range n.errores.lista() {
		if strings.Contains(m, "Content Security Policy") || strings.Contains(m, "Refused to") {
			csp = true
		}
	}
	if !csp {
		t.Errorf("el navegador no reportó violaciones de CSP; reportes: %v", n.errores.lista())
	}
}

// ---------------------------------------------------------------------------
// Que los scripts movidos a archivos sigan funcionando — tienda
// ---------------------------------------------------------------------------

func TestNavegadorTiendaFuncionaConLaCSP(t *testing.T) {
	e := nuevoEntorno(t, true)
	productoID, _ := sembrarProducto(t, e.Conn, "Taza azul")
	n := nuevoNavegador(t)

	// --- Inicio: filtro de categorías y botón de agregar ---
	n.correr(t, chromedp.Navigate(e.URL+"/"), chromedp.WaitVisible(`#tFiltroBtn`), chromedp.Click(`#tFiltroBtn`))
	n.espera(t, "el panel de filtros se abre", `!document.getElementById('tFiltroPanel').hidden`)

	n.correr(t, chromedp.Click(`[data-action="none"]`))
	n.espera(t, "«ninguna» oculta todas las tarjetas y avisa",
		`Array.from(document.querySelectorAll('#tGrid .t-card')).every(c => c.hidden) && !document.getElementById('tSinResultados').hidden`)
	n.correr(t, chromedp.Click(`[data-action="all"]`))
	n.espera(t, "«todas» vuelve a mostrar las tarjetas",
		`Array.from(document.querySelectorAll('#tGrid .t-card')).every(c => !c.hidden)`)

	n.correr(t, chromedp.Click(`.t-agregar`))
	n.espera(t, "agregar desde el inicio guarda en el carrito",
		fmt.Sprintf(`(JSON.parse(localStorage.getItem('manualidades_carrito'))||[]).some(i => i.productoId === %d)`, productoID))
	n.errores.exigeSinErrores(t, "inicio de la tienda")

	// --- Detalle del producto ---
	n.correr(t, chromedp.Navigate(fmt.Sprintf("%s/producto/%d", e.URL, productoID)), chromedp.WaitVisible(`#tAgregarBtn`))
	n.errores.exigeSinErrores(t, "detalle del producto")

	// --- Carrito: sumar, restar, quitar ---
	n.correr(t, chromedp.Navigate(e.URL+"/carrito"), chromedp.WaitVisible(`#tLineas .t-linea`))
	n.correr(t, chromedp.Click(`[data-accion="mas"]`))
	n.espera(t, "«+» sube la cantidad a 2", `document.querySelector('.t-linea-qty span').textContent === '2'`)
	n.correr(t, chromedp.Click(`[data-accion="menos"]`))
	n.espera(t, "«−» baja la cantidad a 1", `document.querySelector('.t-linea-qty span').textContent === '1'`)
	n.correr(t, chromedp.Click(`[data-accion="quitar"]`))
	n.espera(t, "«✕» vacía el carrito y muestra el aviso", `!document.getElementById('tCarritoVacio').hidden`)
	n.errores.exigeSinErrores(t, "carrito")

	// --- Checkout: resumen y entrega a domicilio ---
	n.js(t, fmt.Sprintf(`Carrito.agregar({productoId: %d, nombre: 'Taza azul', precio: 25, foto: '', stock: 10}, 2)`, productoID), nil)
	n.correr(t, chromedp.Navigate(e.URL+"/checkout"), chromedp.WaitVisible(`#tResumenLineas tr`))
	n.espera(t, "el resumen muestra el total", `document.getElementById('tResumenTotal').textContent === 'Q 50.00'`)
	n.correr(t, chromedp.Click(`input[name="metodo_entrega"][value="domicilio"]`))
	n.espera(t, "domicilio muestra la dirección", `!document.getElementById('tDireccionRow').hidden`)
	n.correr(t, chromedp.Click(`input[name="metodo_entrega"][value="recoger"]`))
	n.espera(t, "recoger oculta la dirección", `document.getElementById('tDireccionRow').hidden`)
	n.errores.exigeSinErrores(t, "checkout")
}

// ---------------------------------------------------------------------------
// Que los scripts movidos a archivos sigan funcionando — panel de admin
// ---------------------------------------------------------------------------

func TestNavegadorAdminFuncionaConLaCSP(t *testing.T) {
	e := nuevoEntorno(t, true)
	productoID, _ := sembrarProducto(t, e.Conn, "Taza azul")
	if err := inventario.CreateCategoria(e.Conn, "Vacia", ""); err != nil {
		t.Fatal(err)
	}
	n := nuevoNavegador(t)
	n.iniciaSesionAdmin(t, e.URL)
	n.errores.exigeSinErrores(t, "inicio de sesión del admin")

	// --- Menú: enlaces de Cuenta y Sesiones ---
	var enlaces struct {
		Cuenta   string `json:"cuenta"`
		Sesiones string `json:"sesiones"`
	}
	n.js(t, `({
		cuenta: (Array.from(document.querySelectorAll('.topnav-right a')).find(a => a.textContent.trim() === 'Cuenta') || {}).getAttribute('href'),
		sesiones: (Array.from(document.querySelectorAll('.topnav-right a')).find(a => a.textContent.trim() === 'Sesiones') || {}).getAttribute('href')
	})`, &enlaces)
	if enlaces.Cuenta != "/admin/cambiar-password" || enlaces.Sesiones != "/admin/sesiones" {
		t.Errorf("enlaces del menú incorrectos: %+v", enlaces)
	}

	// --- Productos: filtro de texto, historial por fetch y confirmación ---
	n.correr(t, chromedp.Navigate(e.URL+"/admin/mantenimiento/inventario/productos"), chromedp.WaitVisible(`#textoFiltro`))
	n.correr(t, chromedp.SendKeys(`#textoFiltro`, "zzz-no-existe"))
	n.espera(t, "el filtro sin coincidencias muestra el aviso", `!document.getElementById('sinResultados').hidden`)
	n.js(t, `(function(){var f=document.getElementById('textoFiltro'); f.value=''; f.dispatchEvent(new Event('input', {bubbles: true}));})()`, nil)
	n.espera(t, "limpiar el filtro oculta el aviso", `document.getElementById('sinResultados').hidden`)

	n.correr(t, chromedp.Click(`.mov-toggle`))
	n.espera(t, "el historial de movimientos se carga por fetch",
		`(function(){var p=document.querySelector('.mov-panel'); return p.innerHTML.length > 0 && p.innerHTML.indexOf('Cargando') === -1;})()`)

	// Cancelar el diálogo de confirmación NO elimina el producto…
	n.aceptar.Store(false)
	n.marca(t, `document.querySelector('form[data-confirm] button[type="submit"]')`, "borrar-producto")
	n.correr(t, chromedp.Click(`#borrar-producto`))
	for fin := time.Now().Add(8 * time.Second); len(n.dialogosVistos()) == 0 && time.Now().Before(fin); {
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond) // margen para que, si se hubiera enviado, ya constara
	if d := n.dialogosVistos(); len(d) != 1 || !strings.Contains(d[0], "¿Eliminar Taza azul?") {
		t.Fatalf("diálogos vistos: %q, se esperaba uno con «¿Eliminar Taza azul?»", d)
	}
	var existe int
	e.Conn.QueryRow(`SELECT count(*) FROM productos WHERE id = $1`, productoID).Scan(&existe)
	if existe != 1 {
		t.Error("cancelar la confirmación eliminó el producto")
	}

	// …y aceptarla sí envía el formulario (se prueba con la categoría vacía,
	// que se puede borrar sin tocar el kardex).
	n.aceptar.Store(true)
	n.correr(t,
		chromedp.Navigate(e.URL+"/admin/mantenimiento/inventario/categorias"),
		chromedp.WaitVisible(`form[data-confirm]`),
	)
	var conf string
	n.js(t, `Array.from(document.querySelectorAll('form[data-confirm]')).map(f => f.getAttribute('data-confirm')).find(m => m.indexOf('Vacia') !== -1)`, &conf)
	if conf != "¿Eliminar la categoría Vacia?" {
		t.Fatalf("el formulario de la categoría no trae su mensaje de confirmación: %q", conf)
	}
	n.marca(t, `Array.from(document.querySelectorAll('form[data-confirm]')).find(f => f.getAttribute('data-confirm').indexOf('Vacia') !== -1).querySelector('button[type="submit"]')`, "borrar-categoria")
	n.correr(t, chromedp.Click(`#borrar-categoria`))
	limite := time.Now().Add(8 * time.Second)
	var cats int
	for time.Now().Before(limite) {
		e.Conn.QueryRow(`SELECT count(*) FROM categorias WHERE nombre = 'Vacia'`).Scan(&cats)
		if cats == 0 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if cats != 0 {
		t.Error("aceptar la confirmación no eliminó la categoría")
	}
	n.errores.exigeSinErrores(t, "productos y categorías")

	// --- Usuarios: el selector de rol muestra/oculta los módulos ---
	n.correr(t, chromedp.Navigate(e.URL+"/admin/usuarios/nuevo"), chromedp.WaitVisible(`#rolSelect`))
	cambiarRol := func(rol string) {
		n.js(t, fmt.Sprintf(`(function(){var s=document.getElementById('rolSelect'); s.value=%q; s.dispatchEvent(new Event('change',{bubbles:true}));})()`, rol), nil)
	}
	cambiarRol("administrativo")
	n.espera(t, "administrativo muestra los módulos", `!document.getElementById('modulosBox').hidden`)
	cambiarRol("superusuario")
	n.espera(t, "superusuario oculta los módulos", `document.getElementById('modulosBox').hidden`)
	n.errores.exigeSinErrores(t, "formulario de usuario")

	// --- Sitio: la vista previa sigue los colores ---
	n.correr(t, chromedp.Navigate(e.URL+"/admin/sitio"), chromedp.WaitVisible(`#colorFondo`))
	n.js(t, `(function(){var c=document.getElementById('colorFondo'); c.value='#123456'; c.dispatchEvent(new Event('input',{bubbles:true}));})()`, nil)
	n.espera(t, "la vista previa toma el color elegido",
		`document.getElementById('preview').style.getPropertyValue('--p-fondo') === '#123456'`)
	n.errores.exigeSinErrores(t, "configuración del sitio")

	// --- Sesiones y reportes: cargan sin errores de JavaScript ni de CSP ---
	for _, ruta := range []string{
		"/admin", "/admin/sesiones", "/admin/cambiar-password", "/admin/usuarios",
		"/admin/mantenimiento/inventario/movimientos", "/admin/pedidos",
		"/admin/reportes", "/admin/reportes/ventas", "/admin/reportes/productos", "/admin/reportes/utilidad",
		"/admin/reportes/stock", "/admin/reportes/pedidos", "/admin/reportes/movimientos",
	} {
		n.correr(t, chromedp.Navigate(e.URL+ruta), chromedp.WaitReady(`body`))
		n.errores.exigeSinErrores(t, ruta)
	}

	// El botón de imprimir del reporte tiene su listener (window.print se
	// sustituye para no abrir el diálogo del sistema).
	n.correr(t, chromedp.Navigate(e.URL+"/admin/reportes/movimientos"), chromedp.WaitReady(`body`))
	var imprime any
	n.js(t, `(function(){
		var llamado = false; window.print = function(){ llamado = true; };
		var b = document.getElementById('btnImprimir');
		if (!b) return 'sin-boton';
		b.click(); return llamado;
	})()`, &imprime)
	if imprime == false {
		t.Error("el botón Imprimir existe pero no llama a window.print()")
	}
	t.Logf("botón Imprimir: %v", imprime)
}
