// Package correo guarda la configuración del servidor de correo saliente (SMTP)
// y envía mensajes con ella.
//
// La configuración vive en smtp.json, junto a config.json y admin.json: no en
// la base de datos, para que un volcado de la base no arrastre la contraseña.
// Las conexiones son siempre cifradas (STARTTLS o SSL); nunca se envía la
// contraseña ni el correo en texto plano.
package correo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Modos de seguridad de la conexión.
const (
	// STARTTLS arranca en claro y sube a TLS antes de autenticarse (puerto 587).
	STARTTLS = "starttls"
	// SSL abre la conexión ya cifrada (puerto 465).
	SSL = "ssl"
)

// Seguridades son las opciones que se ofrecen en la pantalla.
var Seguridades = []Opcion{
	{STARTTLS, "STARTTLS (puerto 587, el más común)"},
	{SSL, "SSL/TLS (puerto 465)"},
}

type Opcion struct{ Valor, Nombre string }

// MaxDestinatarios acota la lista de avisos.
const MaxDestinatarios = 10

type Config struct {
	Servidor      string   `json:"servidor"`
	Puerto        int      `json:"puerto"`
	Seguridad     string   `json:"seguridad"`
	Usuario       string   `json:"usuario"`
	Password      string   `json:"password"`
	DesdeNombre   string   `json:"desde_nombre"`
	DesdeCorreo   string   `json:"desde_correo"`
	Destinatarios []string `json:"destinatarios"`
	// UrlTienda es la dirección pública de la tienda (https://dominio), la que
	// llevan los enlaces de los correos. Se configura a mano y NO se deduce de la
	// petición: la cabecera Host la controla quien hace el pedido, y un enlace
	// armado con ella permitiría mandar correos con enlaces a un sitio falso.
	UrlTienda string `json:"url_tienda"`
}

// Configurado indica si hay lo mínimo para poder enviar.
func (c Config) Configurado() bool {
	return c.Servidor != "" && c.Puerto != 0 && c.DesdeCorreo != ""
}

var (
	// ErrValidacion envuelve mensajes seguros de mostrar tal cual al admin.
	ErrValidacion = errors.New("datos inválidos")
	// ErrNoConfigurado: se intentó enviar sin haber guardado la configuración.
	ErrNoConfigurado = errors.New("el servidor de correo no está configurado")
)

func invalido(formato string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidacion, fmt.Sprintf(formato, args...))
}

// TextoValidacion devuelve el texto de un error de validación sin el prefijo
// técnico.
func TextoValidacion(err error) string {
	return strings.TrimPrefix(err.Error(), ErrValidacion.Error()+": ")
}

var reServidor = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

func sinControl(campo, valor string) error {
	for _, r := range valor {
		if unicode.IsControl(r) {
			return invalido("%s no puede contener saltos de línea ni caracteres de control.", campo)
		}
	}
	return nil
}

// direccion valida un correo simple («nombre@dominio»), sin nombre visible.
func direccion(campo, valor string) (string, error) {
	valor = strings.TrimSpace(valor)
	if err := sinControl(campo, valor); err != nil {
		return "", err
	}
	a, err := mail.ParseAddress(valor)
	if err != nil || a.Address != valor || !strings.Contains(a.Address, "@") || utf8.RuneCountInString(valor) > 254 {
		return "", invalido("%s no es una dirección de correo válida.", campo)
	}
	return a.Address, nil
}

// ParseDestinatarios convierte el texto del formulario (separado por comas,
// punto y coma o saltos de línea) en una lista validada y sin repetidos.
func ParseDestinatarios(texto string) ([]string, error) {
	partes := strings.FieldsFunc(texto, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' })
	var out []string
	vistos := map[string]bool{}
	for _, p := range partes {
		if strings.TrimSpace(p) == "" {
			continue
		}
		d, err := direccion("Uno de los correos de aviso", p)
		if err != nil {
			return nil, invalido("«%s» no es una dirección de correo válida.", strings.TrimSpace(p))
		}
		if k := strings.ToLower(d); !vistos[k] {
			vistos[k] = true
			out = append(out, d)
		}
	}
	if len(out) > MaxDestinatarios {
		return nil, invalido("Se admiten como máximo %d correos de aviso.", MaxDestinatarios)
	}
	return out, nil
}

// Limpiar quita espacios sobrantes y valida la configuración completa. Las
// contraseñas de aplicación de Gmail se muestran con espacios; en esa cuenta se
// quitan.
func (c Config) Limpiar() (Config, error) {
	c.Servidor = strings.ToLower(strings.TrimSpace(c.Servidor))
	c.Seguridad = strings.TrimSpace(c.Seguridad)
	c.Usuario = strings.TrimSpace(c.Usuario)
	c.DesdeNombre = strings.TrimSpace(c.DesdeNombre)

	if c.Servidor == "" || len(c.Servidor) > 253 || !reServidor.MatchString(c.Servidor) {
		return c, invalido("El servidor no es válido (ej. smtp.gmail.com).")
	}
	if c.Puerto < 1 || c.Puerto > 65535 {
		return c, invalido("El puerto debe estar entre 1 y 65535.")
	}
	if c.Seguridad != STARTTLS && c.Seguridad != SSL {
		return c, invalido("La seguridad de la conexión no es válida.")
	}
	if err := sinControl("El usuario", c.Usuario); err != nil {
		return c, err
	}
	if utf8.RuneCountInString(c.Usuario) > 254 {
		return c, invalido("El usuario es demasiado largo.")
	}
	if err := sinControl("La contraseña", c.Password); err != nil {
		return c, err
	}
	if utf8.RuneCountInString(c.Password) > 200 {
		return c, invalido("La contraseña es demasiado larga.")
	}
	if strings.HasSuffix(c.Servidor, "gmail.com") {
		c.Password = strings.ReplaceAll(c.Password, " ", "")
	}
	if c.Usuario != "" && c.Password == "" {
		return c, invalido("Falta la contraseña del servidor.")
	}
	if err := sinControl("El nombre del remitente", c.DesdeNombre); err != nil {
		return c, err
	}
	if utf8.RuneCountInString(c.DesdeNombre) > 100 {
		return c, invalido("El nombre del remitente es demasiado largo.")
	}
	d, err := direccion("El correo del remitente", c.DesdeCorreo)
	if err != nil {
		return c, err
	}
	c.DesdeCorreo = d
	if len(c.Destinatarios) > MaxDestinatarios {
		return c, invalido("Se admiten como máximo %d correos de aviso.", MaxDestinatarios)
	}
	u, err := LimpiarUrlTienda(c.UrlTienda)
	if err != nil {
		return c, err
	}
	c.UrlTienda = u
	return c, nil
}

// LimpiarUrlTienda valida la dirección pública de la tienda y la devuelve sin
// barra final. Vacía es válida (aún no configurada). Debe ser https, salvo
// localhost para desarrollo; sin ruta, parámetros ni usuario.
func LimpiarUrlTienda(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if err := sinControl("La dirección de la tienda", s); err != nil {
		return "", err
	}
	u, err := url.Parse(s)
	malo := invalido("La dirección de la tienda debe ser como https://tienda.ejemplo.com (sin rutas ni parámetros).")
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", malo
	}
	host := u.Hostname()
	local := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", invalido("La dirección de la tienda debe empezar con https:// (http solo se admite en localhost).")
	}
	if !local && !reServidor.MatchString(host) {
		return "", malo
	}
	return u.Scheme + "://" + u.Host, nil
}

// archivo es la ruta de smtp.json (relativa al directorio de trabajo, como
// config.json y admin.json). Las pruebas la cambian.
var archivo = "smtp.json"

var mu sync.Mutex

// Load lee smtp.json. Si no existe devuelve una configuración vacía y sin error.
func Load() (Config, error) {
	mu.Lock()
	defer mu.Unlock()
	return load()
}

func load() (Config, error) {
	data, err := os.ReadFile(archivo)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("leyendo %s: %w", archivo, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parseando %s: %w", archivo, err)
	}
	return c, nil
}

// Save valida y guarda la configuración. Si c.Password viene vacío conserva la
// contraseña ya guardada (la pantalla nunca la muestra, así que no la reenvía).
// Escribe en un archivo temporal y lo renombra, con permisos 600.
func Save(c Config) (Config, error) {
	mu.Lock()
	defer mu.Unlock()

	if c.Password == "" {
		previa, err := load()
		if err != nil {
			return Config{}, err
		}
		// La contraseña guardada solo se reutiliza con el mismo servidor y
		// usuario: si no, viajaría a un destino distinto del que se escribió.
		if previa.Password != "" && (strings.ToLower(strings.TrimSpace(c.Servidor)) != previa.Servidor ||
			strings.TrimSpace(c.Usuario) != previa.Usuario) {
			return c, invalido("Cambiaste el servidor o el usuario: escribe también la contraseña.")
		}
		c.Password = previa.Password
	}
	c, err := c.Limpiar()
	if err != nil {
		return c, err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return c, fmt.Errorf("serializando la configuración de correo: %w", err)
	}
	tmp := archivo + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return c, fmt.Errorf("escribiendo %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, archivo); err != nil {
		os.Remove(tmp)
		return c, fmt.Errorf("reemplazando %s: %w", archivo, err)
	}
	return c, nil
}
