package e2e

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"manualidades/internal/tienda"
	"manualidades/internal/usuarios"
)

// Pruebas de inyección SQL. Se lanzan cargas clásicas contra TODAS las
// entradas que llegan a una consulta (parámetros de URL, ids en la ruta,
// formularios, JSON del checkout, cookies, login) y se comprueba, sobre la
// base de datos real, que:
//
//   - nada se modifica de más (huella de cada tabla antes/después),
//   - los datos enviados se guardan LITERALES (o se rechazan con limpieza),
//   - ninguna respuesta filtra errores internos de SQL,
//   - ninguna petición se demora (pg_sleep inyectado → inyección ciega),
//   - el login y la confirmación de pedido no se evaden con tautologías.
//
// La base lleva una tabla «canario»: si una carga lograra ejecutar un
// DROP/DELETE/UPDATE, su huella cambiaría o la tabla desaparecería.

// metodoMultipart es un POST como multipart/form-data (formularios con archivos).
const metodoMultipart = "POST multipart"

// cargasSQL son las cargas. Ninguna supera 45 caracteres para caber en los
// campos más cortos (usuario: 50) y poder llegar hasta la consulta.
var cargasSQL = []string{
	`'`,
	`' OR '1'='1`,
	`' OR 1=1 --`,
	`1 OR 1=1`,
	`'; DROP TABLE canario; --`,
	`"; DELETE FROM usuarios; --`,
	`' UNION SELECT password_hash FROM usuarios --`,
	`'); UPDATE usuarios SET rol='x' --`,
	`1; SELECT pg_sleep(6) --`,
	`' OR pg_sleep(6) --`,
	`' OR (SELECT 1 FROM pg_sleep(6)) IS NOT NULL --`,
	`\' OR 1=1 --`,
	`%27%20OR%201=1`,
}

// huellas de errores internos de PostgreSQL / del driver que nunca deben
// llegar al navegador.
var huellasErrorSQL = []string{
	"SQLSTATE", "pq:", "syntax error at", "invalid input syntax", "unterminated quoted",
	`relation "`, `column "`, "violates ", "value too long", "duplicate key",
}

func filtraErrorSQL(body string) string {
	for _, h := range huellasErrorSQL {
		if strings.Contains(body, h) {
			return h
		}
	}
	return ""
}

// huella devuelve un resumen (md5) del contenido de cada tabla, salvo
// «sesiones», que cambia legítimamente con cada inicio de sesión.
func huella(t *testing.T, conn *sql.DB) map[string]string {
	t.Helper()
	rows, err := conn.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	var tablas []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		if n != "sesiones" {
			tablas = append(tablas, n)
		}
	}
	rows.Close()

	out := map[string]string{}
	for _, n := range tablas {
		var h string
		q := fmt.Sprintf(`SELECT COALESCE(md5(string_agg(t::text, E'\n' ORDER BY t::text)), '-') FROM %q t`, n)
		if err := conn.QueryRow(q).Scan(&h); err != nil {
			t.Fatalf("huella de %s: %v", n, err)
		}
		out[n] = h
	}
	return out
}

// cambios lista las tablas cuyo contenido difiere (o que aparecieron o
// desaparecieron) entre dos huellas.
func cambios(antes, despues map[string]string) []string {
	var out []string
	for n, h := range antes {
		if d, ok := despues[n]; !ok {
			out = append(out, n+" (DESAPARECIÓ)")
		} else if d != h {
			out = append(out, n)
		}
	}
	for n := range despues {
		if _, ok := antes[n]; !ok {
			out = append(out, n+" (nueva)")
		}
	}
	sort.Strings(out)
	return out
}

func contiene(lista []string, v string) bool {
	for _, x := range lista {
		if x == v {
			return true
		}
	}
	return false
}

// escenario es un entorno con datos de ejemplo y la tabla canario.
type escenario struct {
	e          *entorno
	productoID int
	categoria  int
	pedidoID   int    // pedido pagado de control
	pedidoTok  string // su token (el que guarda el navegador de su dueño)
}

func nuevoEscenario(t *testing.T) *escenario {
	t.Helper()
	e := nuevoEntorno(t, true)
	s := &escenario{e: e}
	s.productoID, s.categoria = sembrarProducto(t, e.Conn, "Taza azul")

	if _, err := e.Conn.Exec(`CREATE TABLE canario (id int, valor text); INSERT INTO canario VALUES (1, 'intacto')`); err != nil {
		t.Fatal(err)
	}
	if _, err := usuarios.CreateUsuario(e.Conn, "ana", "clave-de-ana-1", usuarios.RolSuperusuario, nil); err != nil {
		t.Fatal(err)
	}

	// Un pedido ya pagado, con su token: sirve de control para la
	// confirmación y como dato existente para las pantallas de pedidos.
	lineas, total, err := tienda.CotizarCarrito(e.Conn, []tienda.ItemCarrito{{ProductoID: s.productoID, Cantidad: 1}})
	if err != nil {
		t.Fatal(err)
	}
	intento, err := tienda.IniciarPago(e.Conn, "", tienda.DatosPedido{
		ClienteNombre: "Cliente Control", ClienteTelefono: "5555", MetodoEntrega: "recoger",
	}, lineas, total)
	if err != nil {
		t.Fatal(err)
	}
	if err := tienda.ConfirmarPago(e.Conn, intento, tienda.ResultadoPago{Referencia: "R-CONTROL", Marca: "visa", Ultimos4: "4242"}); err != nil {
		t.Fatal(err)
	}
	s.pedidoID, s.pedidoTok = intento.PedidoID, intento.Token
	return s
}

// pedir hace la petición y aplica las comprobaciones comunes a TODAS: sin
// error 5xx, sin errores de SQL en el cuerpo y sin demora (pg_sleep).
func (s *escenario) pedir(t *testing.T, c *cliente, metodo, ruta string, datos url.Values) (*http.Response, string) {
	t.Helper()
	corta := ruta
	if len(corta) > 110 {
		corta = corta[:110] + "…"
	}
	ini := time.Now()
	var resp *http.Response
	var body string
	switch metodo {
	case http.MethodGet:
		resp, body = c.get(ruta)
	case metodoMultipart:
		resp, body = c.postMultipart(ruta, datos)
	default:
		resp, body = c.post(ruta, datos)
	}
	if d := time.Since(ini); d > 3*time.Second {
		t.Errorf("%s %s tardó %v (posible inyección ciega por tiempo)", metodo, corta, d.Round(time.Millisecond))
	}
	if resp.StatusCode >= 500 {
		t.Errorf("%s %s respondió %d", metodo, corta, resp.StatusCode)
	}
	if h := filtraErrorSQL(body); h != "" {
		t.Errorf("%s %s filtra un error interno de la base de datos (%q)", metodo, corta, h)
	}
	return resp, body
}

// ---------------------------------------------------------------------------
// 1. Lecturas: parámetros de URL e ids de la ruta
// ---------------------------------------------------------------------------

func TestSQLiParametrosDeLecturaNoModificanNiFallan(t *testing.T) {
	s := nuevoEscenario(t)
	admin := s.e.cliente()
	admin.entraComoAdmin()

	q := url.QueryEscape
	p := url.PathEscape
	const fechas = "desde=2026-01-01&hasta=2026-12-31"
	rutas := map[string]func(c string) string{
		// ids en la ruta
		"tienda: producto":           func(c string) string { return "/producto/" + p(c) },
		"tienda: confirmación":       func(c string) string { return "/pedido/" + p(c) + "/confirmacion" },
		"admin: pedido":              func(c string) string { return "/admin/pedidos/" + p(c) },
		"admin: producto editar":     func(c string) string { return "/admin/mantenimiento/inventario/productos/" + p(c) + "/editar" },
		"admin: movimientos de prod": func(c string) string { return "/admin/mantenimiento/inventario/productos/" + p(c) + "/movimientos" },
		"admin: categoría editar":    func(c string) string { return "/admin/mantenimiento/inventario/categorias/" + p(c) + "/editar" },
		"admin: usuario editar":      func(c string) string { return "/admin/usuarios/" + p(c) + "/editar" },
		// parámetros de consulta
		"admin: pedidos?estado": func(c string) string { return "/admin/pedidos?estado=" + q(c) },
		"admin: reporte movimientos (fechas)": func(c string) string {
			return "/admin/reportes/movimientos?desde=" + q(c) + "&hasta=" + q(c)
		},
		"admin: reporte movimientos (tipos)": func(c string) string {
			return "/admin/reportes/movimientos?" + fechas + "&ingresos=" + q(c) + "&egresos=" + q(c) + "&solo_ventas=" + q(c)
		},
	}
	// Los reportes comparten parámetros: se prueba cada uno con la carga en
	// las fechas (para ver la validación) y, con fechas válidas, en el resto
	// (para que la carga llegue hasta la consulta).
	for _, rep := range []string{"ventas", "productos", "utilidad", "stock", "pedidos"} {
		rep := rep
		rutas["admin: reporte "+rep+" (fechas)"] = func(c string) string {
			return "/admin/reportes/" + rep + "?desde=" + q(c) + "&hasta=" + q(c)
		}
		rutas["admin: reporte "+rep+" (filtros)"] = func(c string) string {
			return "/admin/reportes/" + rep + "?" + fechas + "&categoria=" + q(c) + "&origen=" + q(c) + "&agrup=" + q(c)
		}
		rutas["admin: reporte "+rep+" (csv)"] = func(c string) string {
			return "/admin/reportes/" + rep + "?" + fechas + "&categoria=" + q(c) + "&origen=" + q(c) + "&agrup=" + q(c) + "&formato=csv"
		}
	}

	antes := huella(t, s.e.Conn)
	n := 0
	for nombre, f := range rutas {
		for _, carga := range cargasSQL {
			resp, _ := s.pedir(t, admin, http.MethodGet, f(carga), nil)
			n++
			// Un id o filtro absurdo debe dar «no encontrado» o una página
			// normal, nunca un error.
			if resp.StatusCode >= 500 {
				t.Logf("   ↳ vector: %s con carga %q", nombre, carga)
			}
		}
	}
	if d := cambios(antes, huella(t, s.e.Conn)); len(d) != 0 {
		t.Errorf("las lecturas modificaron la base de datos: %v", d)
	}
	t.Logf("%d peticiones de lectura con %d cargas × %d vectores", n, len(cargasSQL), len(rutas))
}

// ---------------------------------------------------------------------------
// 2. Escrituras: los datos se guardan literales, sin tocar otras tablas
// ---------------------------------------------------------------------------

func TestSQLiFormulariosGuardanLiteralYNoTocanOtrasTablas(t *testing.T) {
	s := nuevoEscenario(t)
	admin := s.e.cliente()
	admin.entraComoAdmin()
	csrf := admin.csrf()
	conn := s.e.Conn
	hoy := time.Now().Format("2006-01-02")
	const base = "/admin/mantenimiento/inventario"

	existe := func(query string, args ...any) bool {
		var n int
		if err := conn.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n > 0
	}

	type operacion struct {
		nombre    string
		multipart bool     // el formulario real es multipart/form-data
		rechaza   bool     // la validación debe rechazar TODAS las cargas
		permitir  []string // únicas tablas que puede cambiar
		enviar    func(c string) (string, url.Values)
		literal   func(c string) bool // ¿quedó guardado exactamente como se envió?
	}
	ops := []operacion{
		{
			"categoría", false, false, []string{"categorias"},
			func(c string) (string, url.Values) {
				return base + "/categorias", url.Values{"csrf": {csrf}, "nombre": {c}, "descripcion": {c}}
			},
			func(c string) bool {
				return existe(`SELECT count(*) FROM categorias WHERE nombre = $1 AND descripcion = $1`, c)
			},
		},
		{
			"producto", true, false, []string{"productos"},
			func(c string) (string, url.Values) {
				return base + "/productos/nuevo", url.Values{
					"csrf": {csrf}, "nombre": {c}, "descripcion": {c}, "categoria_id": {fmt.Sprint(s.categoria)},
					"precio_compra": {"1"}, "precio_venta": {"2"}, "activo": {"on"},
				}
			},
			func(c string) bool {
				return existe(`SELECT count(*) FROM productos WHERE nombre = $1 AND descripcion = $1`, c)
			},
		},
		{
			"usuario", false, false, []string{"usuarios", "usuario_modulos"},
			func(c string) (string, url.Values) {
				return "/admin/usuarios/nuevo", url.Values{
					"csrf": {csrf}, "usuario": {c}, "password": {"clave-larga-1"}, "confirmar": {"clave-larga-1"}, "rol": {"superusuario"},
				}
			},
			func(c string) bool { return existe(`SELECT count(*) FROM usuarios WHERE usuario = $1`, c) },
		},
		{
			"movimiento (motivo)", false, false, []string{"movimientos_inventario"},
			func(c string) (string, url.Values) {
				return base + "/movimientos", url.Values{
					"csrf": {csrf}, "producto_id": {fmt.Sprint(s.productoID)}, "tipo": {"ingreso"},
					"cantidad": {"1"}, "fecha": {hoy}, "motivo": {c},
				}
			},
			func(c string) bool { return existe(`SELECT count(*) FROM movimientos_inventario WHERE motivo = $1`, c) },
		},
		{
			"sitio (textos)", true, false, []string{"config_sitio"},
			func(c string) (string, url.Values) {
				return "/admin/sitio", url.Values{
					"csrf": {csrf}, "nombre_negocio": {c}, "direccion": {c}, "telefono": {c}, "email": {c},
					"facebook_url": {c}, "instagram_url": {c}, "whatsapp_numero": {c},
					"color_fondo": {"#FBE9EC"}, "color_texto": {"#1F2A44"}, "color_marco": {"#8A8F98"}, "color_acento": {"#C1592B"},
				}
			},
			func(c string) bool {
				return existe(`SELECT count(*) FROM config_sitio WHERE nombre_negocio = $1 AND direccion = $1 AND email = $1`, c)
			},
		},
		{
			"sitio (colores)", true, true, []string{"config_sitio"},
			func(c string) (string, url.Values) {
				return "/admin/sitio", url.Values{
					"csrf": {csrf}, "nombre_negocio": {"Manualidades"},
					"color_fondo": {c}, "color_texto": {c}, "color_marco": {c}, "color_acento": {c},
				}
			},
			func(c string) bool { return existe(`SELECT count(*) FROM config_sitio WHERE color_fondo = $1`, c) },
		},
		{
			"pedido (estado)", false, false, nil, // el estado se valida: no debe cambiar nada
			func(c string) (string, url.Values) {
				return fmt.Sprintf("/admin/pedidos/%d", s.pedidoID), url.Values{"csrf": {csrf}, "estado": {c}}
			},
			func(c string) bool { return false },
		},
		{
			"cambiar contraseña (actual)", false, false, nil,
			func(c string) (string, url.Values) {
				return "/admin/cambiar-password", url.Values{"csrf": {csrf}, "actual": {c}, "nueva": {"otra-clave-123"}, "confirmar": {"otra-clave-123"}}
			},
			func(c string) bool { return false },
		},
	}

	for _, op := range ops {
		guardados := 0
		for _, carga := range cargasSQL {
			antes := huella(t, conn)
			ruta, datos := op.enviar(carga)
			metodo := http.MethodPost
			if op.multipart {
				metodo = metodoMultipart
			}
			s.pedir(t, admin, metodo, ruta, datos)
			for _, tabla := range cambios(antes, huella(t, conn)) {
				if !contiene(op.permitir, tabla) {
					t.Errorf("[%s] la carga %q modificó la tabla «%s», que no corresponde", op.nombre, carga, tabla)
				}
			}
			if op.literal(carga) {
				guardados++
			}
		}
		t.Logf("[%s] guardadas literales: %d de %d (el resto se rechazó con validación)", op.nombre, guardados, len(cargasSQL))
		if op.rechaza && guardados != 0 {
			t.Errorf("[%s] se guardaron %d valores que debieron rechazarse por su formato", op.nombre, guardados)
		}
		if op.permitir != nil && !op.rechaza && guardados == 0 {
			t.Errorf("[%s] ninguna carga llegó a guardarse: la prueba no está ejercitando la consulta", op.nombre)
		}
	}

	// Control: un color con el formato correcto SÍ se guarda (si no, el
	// «rechazo» de arriba podría deberse a un formulario mal armado).
	if resp, _ := admin.postMultipart("/admin/sitio", url.Values{
		"csrf": {csrf}, "nombre_negocio": {"Manualidades"},
		"color_fondo": {"#123456"}, "color_texto": {"#1F2A44"}, "color_marco": {"#8A8F98"}, "color_acento": {"#C1592B"},
	}); resp.StatusCode != http.StatusOK || !existe(`SELECT count(*) FROM config_sitio WHERE color_fondo = '#123456'`) {
		t.Error("un color válido (#123456) no se guardó: el control positivo falló")
	}

	// Segundo orden: los datos hostiles YA guardados se vuelven a leer y a
	// dibujar en muchas pantallas; si alguna los concatenara en SQL, aquí
	// saltaría.
	antes := huella(t, conn)
	rutas := []string{
		"/", "/carrito", "/checkout",
		"/admin", "/admin/sitio", "/admin/usuarios", "/admin/pedidos", "/admin/pedidos/pagos",
		base + "/productos", base + "/categorias", base + "/movimientos",
		fmt.Sprintf("/admin/pedidos/%d", s.pedidoID),
	}
	for _, rep := range []string{"", "/ventas", "/productos", "/utilidad", "/stock", "/pedidos", "/movimientos"} {
		rutas = append(rutas, "/admin/reportes"+rep)
	}
	for _, consulta := range []struct{ tabla, col, prefijo, sufijo string }{
		{"productos", "id", "/producto/", ""},
		{"productos", "id", base + "/productos/", "/editar"},
		{"productos", "id", base + "/productos/", "/movimientos"},
		{"categorias", "id", base + "/categorias/", "/editar"},
		{"usuarios", "id", "/admin/usuarios/", "/editar"},
	} {
		rows, err := conn.Query(fmt.Sprintf(`SELECT %s FROM %s`, consulta.col, consulta.tabla))
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id int
			rows.Scan(&id)
			rutas = append(rutas, fmt.Sprintf("%s%d%s", consulta.prefijo, id, consulta.sufijo))
		}
		rows.Close()
	}
	for _, ruta := range rutas {
		s.pedir(t, admin, http.MethodGet, ruta, nil)
	}
	if d := cambios(antes, huella(t, conn)); len(d) != 0 {
		t.Errorf("volver a leer los datos hostiles modificó la base de datos: %v", d)
	}
	t.Logf("segundo orden: %d pantallas con datos hostiles guardados", len(rutas))

	var valor string
	if err := conn.QueryRow(`SELECT valor FROM canario WHERE id = 1`).Scan(&valor); err != nil || valor != "intacto" {
		t.Errorf("la tabla canario fue alterada (valor %q, err %v)", valor, err)
	}
	var usuariosN int
	conn.QueryRow(`SELECT count(*) FROM usuarios WHERE rol = 'x'`).Scan(&usuariosN)
	if usuariosN != 0 {
		t.Error("una carga logró cambiar el rol de un usuario")
	}
}

// ---------------------------------------------------------------------------
// 3. Login: ninguna tautología evade la contraseña
// ---------------------------------------------------------------------------

func TestSQLiLoginNoSeEvade(t *testing.T) {
	s := nuevoEscenario(t)
	intentos := 0

	// El ataque clásico contra un login con SQL concatenado: un UNION que
	// inventa una fila de usuario con un hash de contraseña conocido por el
	// atacante, de modo que bcrypt "valida" su propia contraseña. Se
	// construye con un hash real para que, si existiera la inyección, el
	// login SÍ se completara y la prueba lo detectara.
	hash, err := bcrypt.GenerateFromPassword([]byte("clave-del-atacante"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	union := fmt.Sprintf(`x' UNION SELECT 999,'hacker','%s','AAAA','superusuario',true --`, hash)
	c := s.e.cliente()
	resp, _ := s.pedir(t, c, http.MethodPost, "/admin/login", url.Values{"usuario": {union}, "password": {"clave-del-atacante"}})
	intentos++
	if resp.StatusCode == http.StatusSeeOther && resp.Header.Get("Location") == "/admin" {
		t.Error("¡login evadido con un UNION que fabrica el hash de contraseña!")
	}
	s.e.cliente().login(adminUsuario, adminClave) // reinicia el limitador

	for _, carga := range cargasSQL {
		for _, par := range []struct{ usuario, clave string }{
			{carga, carga},
			{carga, "x"},
			{"admin" + carga, "x"},
			{"ana" + carga, carga},
			{"admin", carga},
			{"ana", carga},
		} {
			c := s.e.cliente()
			resp, _ := s.pedir(t, c, http.MethodPost, "/admin/login", url.Values{"usuario": {par.usuario}, "password": {par.clave}})
			intentos++
			if resp.StatusCode == http.StatusSeeOther && resp.Header.Get("Location") == "/admin" {
				t.Errorf("¡login evadido con usuario=%q password=%q!", par.usuario, par.clave)
			}
			if c.estaAutenticado() {
				t.Errorf("quedó una sesión activa tras usuario=%q password=%q", par.usuario, par.clave)
			}
			// Un login correcto reinicia el contador de intentos fallidos
			// (límite de 5 por IP); sin esto, tras 5 fallos el limitador
			// bloquearía y la prueba dejaría de llegar a la consulta.
			if !s.e.cliente().login(adminUsuario, adminClave) {
				t.Fatal("el login legítimo dejó de funcionar durante la prueba")
			}
		}
	}
	t.Logf("%d intentos de login con cargas, ninguno entró", intentos)
}

// ---------------------------------------------------------------------------
// 4. Checkout público (JSON) y confirmación por cookie
// ---------------------------------------------------------------------------

func TestSQLiCheckoutYConfirmacion(t *testing.T) {
	// Sin pasarela: el cobro falla (502) DESPUÉS de reservar y guardar el
	// pedido, que es justo lo que se quiere ejercitar.
	t.Setenv("PASARELA_URL", "http://127.0.0.1:1")
	s := nuevoEscenario(t)
	conn := s.e.Conn

	enviar := func(cuerpo string) (*http.Response, string) {
		req, _ := http.NewRequest(http.MethodPost, s.e.URL+"/checkout/confirmar", strings.NewReader(cuerpo))
		req.Header.Set("Content-Type", "application/json")
		return s.e.cliente().hacer(req)
	}
	jsonStr := func(v string) string {
		// escapa para JSON sin depender de encoding/json en la prueba
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
		return `"` + r.Replace(v) + `"`
	}

	// (a) Campos de texto del cliente con cargas: se guardan literales.
	for _, carga := range []string{cargasSQL[1], cargasSQL[4], cargasSQL[6], cargasSQL[10]} {
		antes := huella(t, conn)
		cuerpo := fmt.Sprintf(`{"cliente_nombre":%[1]s,"cliente_telefono":%[1]s,"cliente_email":%[1]s,"cliente_nit":"' OR 1=1--",`+
			`"metodo_entrega":"domicilio","direccion_entrega":%[1]s,"notas":%[1]s,`+
			`"items":[{"producto_id":%[2]d,"cantidad":1}],`+
			`"tarjeta_numero":"4111111111111111","tarjeta_titular":"T","tarjeta_expiracion":"12/30","tarjeta_cvv":"123"}`,
			jsonStr(carga), s.productoID)
		ini := time.Now()
		resp, body := enviar(cuerpo)
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Skip("el limitador de intentos del checkout ya se agotó en este proceso; vuelve a ejecutar la prueba")
		}
		if time.Since(ini) > 3*time.Second {
			t.Errorf("checkout con carga %q tardó %v", carga, time.Since(ini))
		}
		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("checkout con carga %q: se esperaba 502 (pasarela caída), llegó %d: %s", carga, resp.StatusCode, body)
		}
		if h := filtraErrorSQL(body); h != "" {
			t.Errorf("checkout filtra un error de SQL (%q)", h)
		}
		var n int
		conn.QueryRow(`SELECT count(*) FROM pedidos WHERE cliente_nombre = $1 AND cliente_telefono = $1 AND cliente_email = $1 AND direccion_entrega = $1 AND notas = $1`, carga).Scan(&n)
		if n != 1 {
			t.Errorf("la carga %q no quedó guardada literal en el pedido (filas: %d)", carga, n)
		}
		for _, tabla := range cambios(antes, huella(t, conn)) {
			if !contiene([]string{"pedidos", "pedido_items", "pagos"}, tabla) {
				t.Errorf("el checkout con carga %q modificó la tabla «%s»", carga, tabla)
			}
		}
	}

	// (b) Tipos equivocados en el JSON: se rechazan antes de llegar a SQL.
	antes := huella(t, conn)
	for _, cuerpo := range []string{
		`{"cliente_nombre":"a","cliente_telefono":"1","metodo_entrega":"recoger","items":[{"producto_id":"1 OR 1=1","cantidad":1}]}`,
		`{"cliente_nombre":"a","cliente_telefono":"1","metodo_entrega":"recoger","items":[{"producto_id":1,"cantidad":"1; DROP TABLE canario"}]}`,
		`{"cliente_nombre":"a","cliente_telefono":"1","metodo_entrega":"recoger' OR '1'='1","items":[{"producto_id":1,"cantidad":1}]}`,
	} {
		resp, body := enviar(cuerpo)
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Skip("el limitador de intentos del checkout ya se agotó en este proceso; vuelve a ejecutar la prueba")
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("JSON con tipos inválidos respondió %d, se esperaba 400: %s", resp.StatusCode, body)
		}
		if h := filtraErrorSQL(body); h != "" {
			t.Errorf("filtra un error de SQL (%q)", h)
		}
	}
	if d := cambios(antes, huella(t, conn)); len(d) != 0 {
		t.Errorf("peticiones inválidas modificaron la base de datos: %v", d)
	}

	// (c) Confirmación de pedido: la cookie con el token es la «llave». Una
	// tautología SQL en ella no debe abrir el pedido de otra persona.
	url0 := fmt.Sprintf("/pedido/%d/confirmacion", s.pedidoID)
	conCookie := func(valor string) *cliente {
		c := s.e.cliente()
		u, _ := url.Parse(s.e.URL + "/")
		c.http.Jar.SetCookies(u, []*http.Cookie{{Name: "pedido_token", Value: valor, Path: "/"}})
		return c
	}
	// Control: el token verdadero SÍ abre la confirmación (si no, la prueba
	// de abajo no demostraría nada).
	if resp, _ := conCookie(s.pedidoTok).get(url0); resp.StatusCode != http.StatusOK {
		t.Fatalf("el token legítimo no abre su confirmación (status %d); la prueba no es válida", resp.StatusCode)
	}
	for _, carga := range append([]string{""}, cargasSQL...) {
		// Los valores de cookie no admiten ; , " \ ni espacios: se envían
		// con la codificación que el navegador usaría.
		valor := url.QueryEscape(carga)
		resp, body := s.pedir(t, conCookie(valor), http.MethodGet, url0, nil)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("la confirmación se abrió con el token %q", carga)
		}
		if strings.Contains(body, "Gracias por tu compra") {
			t.Errorf("el token %q mostró datos del pedido", carga)
		}
	}
	// Y el token legítimo no abre el pedido de otro id.
	if resp, _ := conCookie(s.pedidoTok).get("/pedido/999999/confirmacion"); resp.StatusCode == http.StatusOK {
		t.Error("el token abrió un pedido que no es suyo")
	}
}
