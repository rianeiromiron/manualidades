package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"manualidades/internal/config"
	"manualidades/internal/sesiones"
	"manualidades/internal/usuarios"
)

const (
	sessionCookieName = "admin_session"
	sessionDuration   = 12 * time.Hour
)

const (
	minPasswordBytes = 8
	// bcrypt solo usa los primeros 72 bytes: más allá de eso ignora el resto
	// (o, según la versión de x/crypto, falla). Se rechaza de entrada para no
	// aceptar en silencio una contraseña que no se verifica completa.
	maxPasswordBytes = 72
)

// errorLargoPassword devuelve el mensaje a mostrar si la contraseña no cumple
// el largo permitido, o "" si es válida.
func errorLargoPassword(p string) string {
	switch {
	case len(p) < minPasswordBytes:
		return "La contraseña debe tener al menos 8 caracteres."
	case len(p) > maxPasswordBytes:
		return "La contraseña no puede superar los 72 caracteres."
	}
	return ""
}

// rutas de /admin que deben quedar accesibles sin haber iniciado sesión.
var publicAdminPaths = map[string]bool{
	"/admin/login":  true,
	"/admin/logout": true,
	"/admin/setup":  true,
}

// todosLosModulos es el catálogo de claves de módulo que reconoce el nav
// del admin (debe coincidir con lo sembrado en internal/usuarios/schema.go).
var todosLosModulos = []string{"bd", "inventario", "sitio", "pedidos", "reportes"}

// Sesion representa la identidad autenticada de la request actual. Kind
// distingue al usuario admin "raíz" (bootstrap, vive en admin.json) de un
// usuario normal (vive en la tabla usuarios): son dos fuentes de
// credenciales distintas, pero comparten el mismo mecanismo de cookie.
type Sesion struct {
	Kind    string // "admin" | "user"
	Usuario string
	Rol     string   // "" para admin; "superusuario" | "administrativo" para user
	Modulos []string // claves permitidas; solo se usa si Rol == "administrativo"

	// ID es el identificador de esta sesión concreta (registro en la tabla
	// sesiones) e Ident la identidad dueña: el nombre del admin o el id del
	// usuario. Con ellos se lista y se revoca una sesión individual.
	ID    string
	Ident string

	// secretKey es la SecretKey de admin.json o de la fila de usuarios que
	// firmó esta sesión. No sale de este paquete: sirve para derivar el
	// token CSRF de sus formularios (ver csrfToken/RequireCSRF), igual que
	// ya sirve para firmar la cookie de sesión en loadSesion.
	secretKey string
}

// TieneAcceso indica si esta sesión puede usar el módulo con esa clave.
// admin y superusuario tienen acceso a todo salvo lo que se controle por
// separado (el mantenimiento de usuarios, ver RequireOnlyAdmin).
func (s Sesion) TieneAcceso(clave string) bool {
	if s.Kind == "admin" || s.Rol == usuarios.RolSuperusuario {
		return true
	}
	return contains(s.Modulos, clave)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

type ctxKey int

const ctxKeySesion ctxKey = 0

func sesionFromContext(r *http.Request) (Sesion, bool) {
	s, ok := r.Context().Value(ctxKeySesion).(Sesion)
	return s, ok
}

// RequireAdminAuth protege todo /admin/* salvo login/logout/setup. Si
// todavía no existe un usuario administrador configurado, manda a
// /admin/setup; si existe pero no hay sesión válida, manda a /admin/login.
// Cuando la sesión es válida, la deja disponible en el contexto de la
// request para que render() y los middlewares de permisos (RequireModule,
// RequireOnlyAdmin) no tengan que volver a decodificar la cookie.
func (a *App) RequireAdminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicAdminPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		if !config.AdminExists() {
			http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
			return
		}
		sesion, ok := a.loadSesion(r)
		if !ok {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeySesion, sesion)))
	})
}

// RequireModule exige, además de una sesión válida (ya garantizada por
// RequireAdminAuth), que esa sesión tenga acceso al módulo indicado.
func (a *App) RequireModule(clave string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sesion, ok := sesionFromContext(r)
			if !ok || !sesion.TieneAcceso(clave) {
				http.Error(w, "No autorizado: no tienes acceso a este módulo.", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// csrfToken deriva, a partir de la SecretKey de la sesión activa, el valor
// que sus formularios deben llevar en el campo oculto "csrf". Se calcula
// igual que la firma de la cookie de sesión (HMAC-SHA256): cambia solo
// cuando cambia esa llave (rotar la contraseña invalida también los
// formularios ya renderizados, junto con la sesión) y nadie sin una sesión
// válida de esa misma identidad lo puede reproducir.
func csrfToken(secretKeyB64 string) string {
	return computeSignature("csrf", secretKeyB64)
}

// RequireCSRF exige, en cada POST de /admin/* con sesión (login/logout/setup
// quedan fuera: todavía no hay sesión que firme un token), que el formulario
// incluya el campo oculto "csrf" con el valor que injectNav puso en la
// página que lo generó. SameSite=Lax ya bloquea la mayoría de los POST entre
// sitios en navegadores modernos; este token es la segunda capa, la que no
// depende de que el navegador de quien visita la página lo implemente bien.
func (a *App) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || publicAdminPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		sesion, ok := sesionFromContext(r)
		if !ok {
			http.Error(w, "No autorizado.", http.StatusForbidden)
			return
		}
		// Deja el formulario ya parseado (incluidos los POST con archivos,
		// como productos o el logo del sitio) para que el handler real no
		// tenga que volver a leer el body.
		if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
			r.ParseForm()
		}
		esperado := csrfToken(sesion.secretKey)
		if recibido := r.FormValue("csrf"); recibido == "" || !hmac.Equal([]byte(recibido), []byte(esperado)) {
			http.Error(w, "Formulario expirado o inválido. Recarga la página e inténtalo de nuevo.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireOnlyAdmin protege el mantenimiento de usuarios: es exclusivo del
// usuario admin de admin.json, ni siquiera un superusuario puede entrar.
func (a *App) RequireOnlyAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sesion, ok := sesionFromContext(r)
		if !ok || sesion.Kind != "admin" {
			http.Error(w, "No autorizado: el mantenimiento de usuarios es exclusivo del usuario admin.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loadSesion decodifica y valida la cookie de sesión. El payload sin
// firmar indica de qué identidad se trata (admin o un usuario por id);
// solo con eso se sabe qué SecretKey usar para verificar la firma. Un
// atacante que altere el identificador invalida la firma, porque no
// conoce la SecretKey del dueño real de ese identificador.
func (a *App) loadSesion(r *http.Request) (Sesion, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return Sesion{}, false
	}
	kind, ident, sid, expiry, payloadEnc, sig, ok := decodeSessionToken(cookie.Value)
	if !ok || time.Now().Unix() >= expiry {
		return Sesion{}, false
	}

	switch kind {
	case "admin":
		admin, err := config.LoadAdmin()
		if err != nil || ident != admin.Usuario {
			return Sesion{}, false
		}
		if !hmac.Equal([]byte(sig), []byte(computeSignature(payloadEnc, admin.SecretKey))) {
			return Sesion{}, false
		}
		// El admin raíz debe poder entrar sin base de datos (es la forma de
		// configurarla): sin BD no hay registro que consultar y vale la firma.
		if conn := a.DB(); conn != nil && !sesionVigente(conn, sid, kind, ident) {
			return Sesion{}, false
		}
		return Sesion{Kind: "admin", Usuario: admin.Usuario, secretKey: admin.SecretKey, ID: sid, Ident: ident}, true

	case "user":
		id, err := strconv.Atoi(ident)
		if err != nil {
			return Sesion{}, false
		}
		conn := a.DB()
		if conn == nil {
			return Sesion{}, false
		}
		u, err := usuarios.GetUsuario(conn, id)
		if err != nil || !u.Activo {
			return Sesion{}, false
		}
		if !hmac.Equal([]byte(sig), []byte(computeSignature(payloadEnc, u.SecretKey))) {
			return Sesion{}, false
		}
		if !sesionVigente(conn, sid, kind, ident) {
			return Sesion{}, false
		}
		var modulos []string
		if u.Rol == usuarios.RolAdministrativo {
			modulos, err = usuarios.ModulosClavesAsignadas(conn, id)
			if err != nil {
				return Sesion{}, false
			}
		}
		return Sesion{Kind: "user", Usuario: u.Usuario, Rol: u.Rol, Modulos: modulos, secretKey: u.SecretKey, ID: sid, Ident: ident}, true

	default:
		return Sesion{}, false
	}
}

// sesionVigente consulta el registro de sesiones. Si la consulta falla se
// trata como inválida: ante la duda, no se deja pasar.
func sesionVigente(conn *sql.DB, sid, kind, ident string) bool {
	ok, err := sesiones.Valida(conn, sid, kind, ident)
	return err == nil && ok
}

// buildSessionToken firma un payload "kind|ident|expiry|sid" con la
// SecretKey del dueño de esa identidad (admin.json o la fila de usuarios).
// sid es el identificador de la sesión en la tabla sesiones; al ir dentro de
// lo firmado, nadie puede cambiarlo para reutilizar otra sesión.
func buildSessionToken(kind, ident, sid, secretKeyB64 string, expiry int64) string {
	payload := kind + "|" + ident + "|" + strconv.FormatInt(expiry, 10) + "|" + sid
	payloadEnc := base64.RawURLEncoding.EncodeToString([]byte(payload))
	sig := computeSignature(payloadEnc, secretKeyB64)
	return payloadEnc + "." + sig
}

func decodeSessionToken(token string) (kind, ident, sid string, expiry int64, payloadEnc, sig string, ok bool) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", "", "", 0, "", "", false
	}
	payloadEnc, sig = parts[0], parts[1]
	payload, err := base64.RawURLEncoding.DecodeString(payloadEnc)
	if err != nil {
		return "", "", "", 0, "", "", false
	}
	// Las cookies anteriores a las sesiones individuales traían 3 campos y
	// ya no valen: hay que iniciar sesión de nuevo una vez.
	fields := strings.SplitN(string(payload), "|", 4)
	if len(fields) != 4 || fields[3] == "" {
		return "", "", "", 0, "", "", false
	}
	expiry, err = strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return "", "", "", 0, "", "", false
	}
	return fields[0], fields[1], fields[3], expiry, payloadEnc, sig, true
}

func computeSignature(payloadEnc, secretKeyB64 string) string {
	key, err := base64.StdEncoding.DecodeString(secretKeyB64)
	if err != nil {
		key = []byte(secretKeyB64) // no debería pasar; degrada sin tronar
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payloadEnc))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// AdminSetup crea el primer (y único) usuario administrador. Una vez
// existe uno, esta ruta deja de estar disponible.
func (a *App) AdminSetup(w http.ResponseWriter, r *http.Request) {
	if config.AdminExists() {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodGet {
		renderAuth(w, "admin_setup.html", map[string]any{"Title": "Configurar acceso"})
		return
	}

	if err := r.ParseForm(); err != nil {
		renderAuth(w, "admin_setup.html", map[string]any{"Title": "Configurar acceso", "Message": "Formulario inválido.", "MessageKind": "error"})
		return
	}
	usuario := r.FormValue("usuario")
	password := r.FormValue("password")
	confirmar := r.FormValue("confirmar")

	switch {
	case usuario == "" || password == "":
		renderAuth(w, "admin_setup.html", map[string]any{"Title": "Configurar acceso", "Message": "Usuario y contraseña son obligatorios.", "MessageKind": "error", "Usuario": usuario})
		return
	case errorLargoPassword(password) != "":
		renderAuth(w, "admin_setup.html", map[string]any{"Title": "Configurar acceso", "Message": errorLargoPassword(password), "MessageKind": "error", "Usuario": usuario})
		return
	case password != confirmar:
		renderAuth(w, "admin_setup.html", map[string]any{"Title": "Configurar acceso", "Message": "Las contraseñas no coinciden.", "MessageKind": "error", "Usuario": usuario})
		return
	}

	if err := config.CreateAdmin(usuario, password); err != nil {
		renderAuth(w, "admin_setup.html", map[string]any{"Title": "Configurar acceso", "Message": "No se pudo crear el usuario: " + err.Error(), "MessageKind": "error", "Usuario": usuario})
		return
	}

	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (a *App) AdminLogin(w http.ResponseWriter, r *http.Request) {
	if !config.AdminExists() {
		http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodGet {
		data := map[string]any{"Title": "Iniciar sesión"}
		if r.URL.Query().Get("password_changed") != "" {
			data["Message"] = "Contraseña actualizada. Inicia sesión de nuevo."
			data["MessageKind"] = "success"
		}
		renderAuth(w, "admin_login.html", data)
		return
	}

	if err := r.ParseForm(); err != nil {
		renderAuth(w, "admin_login.html", map[string]any{"Title": "Iniciar sesión", "Message": "Formulario inválido.", "MessageKind": "error"})
		return
	}

	// Límite de intentos por IP: sin esto, un script podía probar
	// contraseñas sin parar contra /admin/login. Se cuenta por IP, no por
	// usuario, para que nadie pueda bloquear al admin real solo fallando su
	// password muchas veces desde otro lado (ver internal/web/limitador.go).
	ip := clientIP(r)
	if !loginLimiter.Permitido(ip) {
		renderAuth(w, "admin_login.html", map[string]any{"Title": "Iniciar sesión", "Message": "Demasiados intentos fallidos. Espera unos minutos e inténtalo de nuevo.", "MessageKind": "error"})
		return
	}

	usuarioForm := r.FormValue("usuario")
	password := r.FormValue("password")

	// Primero se intenta contra la identidad raíz (admin.json); si el
	// nombre de usuario no coincide con ella, se busca en la tabla
	// usuarios (superusuario/administrativo). Son dos almacenes de
	// credenciales distintos por el mismo motivo que admin.json existe
	// separado de config.json: admin.json debe poder validarse sin BD.
	admin, adminErr := config.LoadAdmin()
	if adminErr == nil && usuarioForm == admin.Usuario && admin.VerifyPassword(password) {
		loginLimiter.Limpiar(ip)
		if !a.iniciarSesion(w, r, "admin", admin.Usuario, admin.SecretKey) {
			renderAuth(w, "admin_login.html", map[string]any{"Title": "Iniciar sesión", "Message": "No se pudo iniciar la sesión. Inténtalo de nuevo.", "MessageKind": "error", "Usuario": usuarioForm})
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	if conn := a.DB(); conn != nil {
		u, err := usuarios.GetUsuarioPorNombre(conn, usuarioForm)
		if err == nil && u.Activo && usuarios.VerifyPassword(u, password) {
			loginLimiter.Limpiar(ip)
			if !a.iniciarSesion(w, r, "user", strconv.Itoa(u.ID), u.SecretKey) {
				renderAuth(w, "admin_login.html", map[string]any{"Title": "Iniciar sesión", "Message": "No se pudo iniciar la sesión. Inténtalo de nuevo.", "MessageKind": "error", "Usuario": usuarioForm})
				return
			}
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
	}

	loginLimiter.Contar(ip)
	renderAuth(w, "admin_login.html", map[string]any{"Title": "Iniciar sesión", "Message": "Usuario o contraseña incorrectos.", "MessageKind": "error", "Usuario": usuarioForm})
}

// iniciarSesion registra la sesión nueva y deja su cookie. Devuelve false si
// no se pudo registrar (la cookie no se envía): una sesión que no consta en
// la tabla no pasaría la validación de todos modos.
func (a *App) iniciarSesion(w http.ResponseWriter, r *http.Request, kind, ident, secretKey string) bool {
	sid, err := sesiones.NuevoID()
	if err != nil {
		log.Printf("sesiones: no se pudo generar el id: %v", err)
		return false
	}
	expira := time.Now().Add(sessionDuration)
	// Sin base de datos solo puede entrar el admin raíz (ver loadSesion), y
	// no hay dónde registrar la sesión.
	if conn := a.DB(); conn != nil {
		if err := sesiones.Crear(conn, sid, kind, ident, expira, clientIP(r), r.UserAgent()); err != nil {
			log.Printf("sesiones: no se pudo registrar la sesión: %v", err)
			return false
		}
	}
	expiry := expira.Unix()
	token := buildSessionToken(kind, ident, sid, secretKey, expiry)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/admin",
		Expires:  time.Unix(expiry, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   esConexionSegura(r),
	})
	return true
}

// borrarCookieSesion limpia la cookie de sesión con los mismos atributos con
// los que iniciarSesion la creó. Un navegador identifica una cookie por
// nombre + Path, no por sus demás atributos, pero mandarlos completos evita
// sorpresas y deja explícito que sigue siendo la misma política.
func borrarCookieSesion(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/admin",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   esConexionSegura(r),
	})
}

// AdminLogout cierra la sesión también en el servidor: además de borrar la
// cookie, revoca su registro, para que una copia robada de la cookie deje de
// servir al instante en vez de seguir válida hasta que venza.
func (a *App) AdminLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if kind, ident, sid, _, _, _, ok := decodeSessionToken(cookie.Value); ok {
			if conn := a.DB(); conn != nil {
				if err := sesiones.Revocar(conn, sid, kind, ident); err != nil {
					log.Printf("sesiones: no se pudo revocar al cerrar sesión: %v", err)
				}
			}
		}
	}
	borrarCookieSesion(w, r)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// AdminCambiarPassword permite a la sesión actual (admin o un usuario de
// la tabla usuarios) cambiar su propia contraseña. Requiere la contraseña
// actual; al guardar, rota también la llave de firma de sesión de esa
// identidad, así que cierra su sesión actual (y solo la suya) y manda a
// iniciar sesión de nuevo.
func (a *App) AdminCambiarPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		render(w, r, "admin_cambiar_password.html", map[string]any{"Title": "Cambiar contraseña", "Active": "cuenta"})
		return
	}

	data := map[string]any{"Title": "Cambiar contraseña", "Active": "cuenta"}

	if err := r.ParseForm(); err != nil {
		data["Message"], data["MessageKind"] = "Formulario inválido.", "error"
		render(w, r, "admin_cambiar_password.html", data)
		return
	}
	actual := r.FormValue("actual")
	nueva := r.FormValue("nueva")
	confirmar := r.FormValue("confirmar")

	switch {
	case errorLargoPassword(nueva) != "":
		data["Message"], data["MessageKind"] = errorLargoPassword(nueva), "error"
		render(w, r, "admin_cambiar_password.html", data)
		return
	case nueva != confirmar:
		data["Message"], data["MessageKind"] = "Las contraseñas nuevas no coinciden.", "error"
		render(w, r, "admin_cambiar_password.html", data)
		return
	}

	sesion, _ := sesionFromContext(r)

	if sesion.Kind == "admin" {
		admin, err := config.LoadAdmin()
		if err != nil || !admin.VerifyPassword(actual) {
			data["Message"], data["MessageKind"] = "La contraseña actual no es correcta.", "error"
			render(w, r, "admin_cambiar_password.html", data)
			return
		}
		if err := config.UpdatePassword(admin, nueva); err != nil {
			data["Message"], data["MessageKind"] = "No se pudo guardar: "+err.Error(), "error"
			render(w, r, "admin_cambiar_password.html", data)
			return
		}
	} else {
		conn := a.DB()
		u, err := usuarios.GetUsuarioPorNombre(conn, sesion.Usuario)
		if err != nil || !usuarios.VerifyPassword(u, actual) {
			data["Message"], data["MessageKind"] = "La contraseña actual no es correcta.", "error"
			render(w, r, "admin_cambiar_password.html", data)
			return
		}
		if err := usuarios.UpdatePropiaPassword(conn, u.ID, nueva); err != nil {
			data["Message"], data["MessageKind"] = "No se pudo guardar: "+err.Error(), "error"
			render(w, r, "admin_cambiar_password.html", data)
			return
		}
	}

	// Rotar la llave ya invalida todas las cookies de esta identidad; se
	// marcan también como revocadas para que no sigan apareciendo en la lista.
	if conn := a.DB(); conn != nil {
		if err := sesiones.RevocarTodas(conn, sesion.Kind, sesion.Ident); err != nil {
			log.Printf("sesiones: no se pudieron revocar al cambiar la contraseña: %v", err)
		}
	}
	borrarCookieSesion(w, r)
	http.Redirect(w, r, "/admin/login?password_changed=1", http.StatusSeeOther)
}

// AdminSesiones lista las sesiones vigentes de la identidad actual para que
// pueda cerrar una concreta (la de un equipo perdido, o una que no reconoce).
func (a *App) AdminSesiones(w http.ResponseWriter, r *http.Request) {
	sesion, _ := sesionFromContext(r)
	data := map[string]any{"Title": "Sesiones activas", "Active": "sesiones", "SesionActual": sesion.ID}
	switch r.URL.Query().Get("ok") {
	case "revocada":
		data["Message"], data["MessageKind"] = "Sesión cerrada.", "success"
	case "otras":
		data["Message"], data["MessageKind"] = "Se cerraron las demás sesiones.", "success"
	}

	conn := a.DB()
	if conn == nil {
		data["SinBD"] = true
		render(w, r, "admin_sesiones.html", data)
		return
	}
	lista, err := sesiones.Activas(conn, sesion.Kind, sesion.Ident)
	if err != nil {
		log.Printf("sesiones: no se pudieron listar: %v", err)
		data["Message"], data["MessageKind"] = "No se pudo leer la lista de sesiones.", "error"
	}
	data["Sesiones"] = lista
	render(w, r, "admin_sesiones.html", data)
}

// AdminSesionRevocar cierra una sesión concreta de la identidad actual. Si es
// la propia, además borra la cookie y manda a iniciar sesión.
func (a *App) AdminSesionRevocar(w http.ResponseWriter, r *http.Request) {
	sesion, _ := sesionFromContext(r)
	id := mux.Vars(r)["id"]
	conn := a.DB()
	if conn == nil {
		http.Error(w, "Base de datos no disponible.", http.StatusServiceUnavailable)
		return
	}
	// Revocar filtra por kind+ident: nadie puede cerrar sesiones ajenas.
	if err := sesiones.Revocar(conn, id, sesion.Kind, sesion.Ident); err != nil {
		log.Printf("sesiones: no se pudo revocar: %v", err)
		http.Error(w, "No se pudo cerrar la sesión.", http.StatusInternalServerError)
		return
	}
	if id == sesion.ID {
		borrarCookieSesion(w, r)
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/sesiones?ok=revocada", http.StatusSeeOther)
}

// AdminSesionesRevocarOtras cierra todas las sesiones de la identidad actual
// menos la que está usando.
func (a *App) AdminSesionesRevocarOtras(w http.ResponseWriter, r *http.Request) {
	sesion, _ := sesionFromContext(r)
	conn := a.DB()
	if conn == nil {
		http.Error(w, "Base de datos no disponible.", http.StatusServiceUnavailable)
		return
	}
	if err := sesiones.RevocarOtras(conn, sesion.Kind, sesion.Ident, sesion.ID); err != nil {
		log.Printf("sesiones: no se pudieron revocar las demás: %v", err)
		http.Error(w, "No se pudieron cerrar las sesiones.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/sesiones?ok=otras", http.StatusSeeOther)
}
