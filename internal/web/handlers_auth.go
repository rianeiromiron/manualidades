package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"manualidades/internal/config"
	"manualidades/internal/usuarios"
)

const (
	sessionCookieName = "admin_session"
	sessionDuration   = 12 * time.Hour
)

// rutas de /admin que deben quedar accesibles sin haber iniciado sesión.
var publicAdminPaths = map[string]bool{
	"/admin/login":  true,
	"/admin/logout": true,
	"/admin/setup":  true,
}

// todosLosModulos es el catálogo de claves de módulo que reconoce el nav
// del admin (debe coincidir con lo sembrado en internal/usuarios/schema.go).
var todosLosModulos = []string{"bd", "inventario", "sitio", "pedidos"}

// Sesion representa la identidad autenticada de la request actual. Kind
// distingue al usuario admin "raíz" (bootstrap, vive en admin.json) de un
// usuario normal (vive en la tabla usuarios): son dos fuentes de
// credenciales distintas, pero comparten el mismo mecanismo de cookie.
type Sesion struct {
	Kind    string // "admin" | "user"
	Usuario string
	Rol     string   // "" para admin; "superusuario" | "administrativo" para user
	Modulos []string // claves permitidas; solo se usa si Rol == "administrativo"
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
	kind, ident, expiry, payloadEnc, sig, ok := decodeSessionToken(cookie.Value)
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
		return Sesion{Kind: "admin", Usuario: admin.Usuario}, true

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
		var modulos []string
		if u.Rol == usuarios.RolAdministrativo {
			modulos, err = usuarios.ModulosClavesAsignadas(conn, id)
			if err != nil {
				return Sesion{}, false
			}
		}
		return Sesion{Kind: "user", Usuario: u.Usuario, Rol: u.Rol, Modulos: modulos}, true

	default:
		return Sesion{}, false
	}
}

// buildSessionToken firma un payload "kind|ident|expiry" con la
// SecretKey del dueño de esa identidad (admin.json o la fila de usuarios).
func buildSessionToken(kind, ident, secretKeyB64 string, expiry int64) string {
	payload := kind + "|" + ident + "|" + strconv.FormatInt(expiry, 10)
	payloadEnc := base64.RawURLEncoding.EncodeToString([]byte(payload))
	sig := computeSignature(payloadEnc, secretKeyB64)
	return payloadEnc + "." + sig
}

func decodeSessionToken(token string) (kind, ident string, expiry int64, payloadEnc, sig string, ok bool) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", "", 0, "", "", false
	}
	payloadEnc, sig = parts[0], parts[1]
	payload, err := base64.RawURLEncoding.DecodeString(payloadEnc)
	if err != nil {
		return "", "", 0, "", "", false
	}
	fields := strings.SplitN(string(payload), "|", 3)
	if len(fields) != 3 {
		return "", "", 0, "", "", false
	}
	expiry, err = strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return "", "", 0, "", "", false
	}
	return fields[0], fields[1], expiry, payloadEnc, sig, true
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
	case len(password) < 8:
		renderAuth(w, "admin_setup.html", map[string]any{"Title": "Configurar acceso", "Message": "La contraseña debe tener al menos 8 caracteres.", "MessageKind": "error", "Usuario": usuario})
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
	usuarioForm := r.FormValue("usuario")
	password := r.FormValue("password")

	// Primero se intenta contra la identidad raíz (admin.json); si el
	// nombre de usuario no coincide con ella, se busca en la tabla
	// usuarios (superusuario/administrativo). Son dos almacenes de
	// credenciales distintos por el mismo motivo que admin.json existe
	// separado de config.json: admin.json debe poder validarse sin BD.
	admin, adminErr := config.LoadAdmin()
	if adminErr == nil && usuarioForm == admin.Usuario && admin.VerifyPassword(password) {
		a.iniciarSesion(w, r, "admin", admin.Usuario, admin.SecretKey)
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	if conn := a.DB(); conn != nil {
		u, err := usuarios.GetUsuarioPorNombre(conn, usuarioForm)
		if err == nil && u.Activo && usuarios.VerifyPassword(u, password) {
			a.iniciarSesion(w, r, "user", strconv.Itoa(u.ID), u.SecretKey)
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
	}

	renderAuth(w, "admin_login.html", map[string]any{"Title": "Iniciar sesión", "Message": "Usuario o contraseña incorrectos.", "MessageKind": "error", "Usuario": usuarioForm})
}

func (a *App) iniciarSesion(w http.ResponseWriter, r *http.Request, kind, ident, secretKey string) {
	expiry := time.Now().Add(sessionDuration).Unix()
	token := buildSessionToken(kind, ident, secretKey, expiry)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/admin",
		Expires:  time.Unix(expiry, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
}

func (a *App) AdminLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/admin",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
	})
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
	case len(nueva) < 8:
		data["Message"], data["MessageKind"] = "La nueva contraseña debe tener al menos 8 caracteres.", "error"
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

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/admin",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
	})
	http.Redirect(w, r, "/admin/login?password_changed=1", http.StatusSeeOther)
}
