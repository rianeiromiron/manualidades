package sitio

import (
	"database/sql"
	"errors"
	"math"
	"regexp"
)

// Config son los datos editables desde /admin/sitio: identidad del negocio,
// contacto, redes sociales, logo y los 4 colores base de la tienda pública.
type Config struct {
	NombreNegocio  string
	Direccion      string
	Telefono       string
	Email          string
	FacebookURL    string
	InstagramURL   string
	WhatsappNumero string
	LogoRuta       string
	ColorFondo     string
	ColorTexto     string
	ColorMarco     string
	ColorAcento    string
	// CostoEnvio es lo que se suma a un pedido de entrega a domicilio.
	CostoEnvio float64
}

const columnas = `nombre_negocio, direccion, telefono, email, facebook_url, instagram_url,
	whatsapp_numero, logo_ruta, color_fondo, color_texto, color_marco, color_acento, costo_envio`

func Get(conn *sql.DB) (Config, error) {
	var c Config
	err := conn.QueryRow(`SELECT `+columnas+` FROM config_sitio WHERE id = 1`).Scan(
		&c.NombreNegocio, &c.Direccion, &c.Telefono, &c.Email, &c.FacebookURL, &c.InstagramURL,
		&c.WhatsappNumero, &c.LogoRuta, &c.ColorFondo, &c.ColorTexto, &c.ColorMarco, &c.ColorAcento, &c.CostoEnvio,
	)
	return c, err
}

// ErrColorInvalido se devuelve cuando alguno de los colores no es #RRGGBB.
var ErrColorInvalido = errors.New("los colores deben tener el formato #RRGGBB (por ejemplo #FBE9EC)")

// ErrCostoEnvioInvalido se devuelve cuando el costo de envío no es un monto
// entre 0 y 9,999.99.
var ErrCostoEnvioInvalido = errors.New("el costo de envío debe ser un monto entre 0 y 9999.99")

var reColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// Save actualiza la fila única de configuración. No toca logo_ruta si la
// llamada no la incluye explícitamente (ver SaveLogo).
func Save(conn *sql.DB, c Config) error {
	// Los colores acaban dentro de un bloque <style> de la tienda: solo se
	// aceptan en formato #RRGGBB (también evita el error de la columna de 7
	// caracteres, que mostraba un mensaje interno de la base de datos).
	for _, color := range []string{c.ColorFondo, c.ColorTexto, c.ColorMarco, c.ColorAcento} {
		if !reColor.MatchString(color) {
			return ErrColorInvalido
		}
	}
	// Se redondea a centavos: un monto con más decimales no se puede cobrar.
	c.CostoEnvio = math.Round(c.CostoEnvio*100) / 100
	if math.IsNaN(c.CostoEnvio) || c.CostoEnvio < 0 || c.CostoEnvio > 9999.99 {
		return ErrCostoEnvioInvalido
	}
	_, err := conn.Exec(
		`UPDATE config_sitio SET
			nombre_negocio = $1, direccion = $2, telefono = $3, email = $4,
			facebook_url = $5, instagram_url = $6, whatsapp_numero = $7,
			color_fondo = $8, color_texto = $9, color_marco = $10, color_acento = $11,
			costo_envio = $12,
			actualizado_en = now()
		 WHERE id = 1`,
		c.NombreNegocio, c.Direccion, c.Telefono, c.Email,
		c.FacebookURL, c.InstagramURL, c.WhatsappNumero,
		c.ColorFondo, c.ColorTexto, c.ColorMarco, c.ColorAcento, c.CostoEnvio,
	)
	return err
}

// SaveLogo actualiza únicamente la ruta del logo guardado.
func SaveLogo(conn *sql.DB, ruta string) error {
	_, err := conn.Exec(`UPDATE config_sitio SET logo_ruta = $1, actualizado_en = now() WHERE id = 1`, ruta)
	return err
}
