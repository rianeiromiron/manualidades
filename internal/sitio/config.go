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

	// Fondo animado de la tienda (cascada de pinceles y frascos de pintura).
	// Si CascadaActiva es false la tienda se ve como siempre y los otros tres
	// valores no se usan.
	CascadaActiva bool
	// CascadaOpacidad es el porcentaje (20-100) con que se ven las figuras.
	CascadaOpacidad int
	// CascadaVelocidad va de 1 (muy lenta) a 10 (rápida).
	CascadaVelocidad int
	// CascadaCantidad es cuántas figuras se ven: 1 pocas, 2 normal, 3 bastantes.
	CascadaCantidad int
}

// Límites de los ajustes de la cascada. Los comparte la validación de Save y
// los CHECK de la base de datos.
const (
	CascadaOpacidadMin   = 20
	CascadaOpacidadMax   = 100
	CascadaVelocidadMin  = 1
	CascadaVelocidadMax  = 10
	CascadaCantidadMin   = 1
	CascadaCantidadMax   = 3
	CascadaOpacidadBase  = 90
	CascadaVelocidadBase = 5
	CascadaCantidadBase  = 2
)

const columnas = `nombre_negocio, direccion, telefono, email, facebook_url, instagram_url,
	whatsapp_numero, logo_ruta, color_fondo, color_texto, color_marco, color_acento, costo_envio,
	cascada_activa, cascada_opacidad, cascada_velocidad, cascada_cantidad`

func Get(conn *sql.DB) (Config, error) {
	var c Config
	err := conn.QueryRow(`SELECT `+columnas+` FROM config_sitio WHERE id = 1`).Scan(
		&c.NombreNegocio, &c.Direccion, &c.Telefono, &c.Email, &c.FacebookURL, &c.InstagramURL,
		&c.WhatsappNumero, &c.LogoRuta, &c.ColorFondo, &c.ColorTexto, &c.ColorMarco, &c.ColorAcento, &c.CostoEnvio,
		&c.CascadaActiva, &c.CascadaOpacidad, &c.CascadaVelocidad, &c.CascadaCantidad,
	)
	return c, err
}

// ErrCascadaInvalida se devuelve cuando la opacidad, la velocidad o la cantidad
// de la cascada están fuera de rango.
var ErrCascadaInvalida = errors.New("los ajustes de la cascada están fuera de rango (opacidad 20-100, velocidad 1-10, cantidad 1-3)")

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
	// Los tres ajustes de la cascada acaban dentro del CSS de la tienda: solo se
	// aceptan números dentro de su rango (aunque la cascada esté apagada, para
	// no guardar basura que se active después).
	if c.CascadaOpacidad < CascadaOpacidadMin || c.CascadaOpacidad > CascadaOpacidadMax ||
		c.CascadaVelocidad < CascadaVelocidadMin || c.CascadaVelocidad > CascadaVelocidadMax ||
		c.CascadaCantidad < CascadaCantidadMin || c.CascadaCantidad > CascadaCantidadMax {
		return ErrCascadaInvalida
	}
	_, err := conn.Exec(
		`UPDATE config_sitio SET
			nombre_negocio = $1, direccion = $2, telefono = $3, email = $4,
			facebook_url = $5, instagram_url = $6, whatsapp_numero = $7,
			color_fondo = $8, color_texto = $9, color_marco = $10, color_acento = $11,
			costo_envio = $12,
			cascada_activa = $13, cascada_opacidad = $14, cascada_velocidad = $15, cascada_cantidad = $16,
			actualizado_en = now()
		 WHERE id = 1`,
		c.NombreNegocio, c.Direccion, c.Telefono, c.Email,
		c.FacebookURL, c.InstagramURL, c.WhatsappNumero,
		c.ColorFondo, c.ColorTexto, c.ColorMarco, c.ColorAcento, c.CostoEnvio,
		c.CascadaActiva, c.CascadaOpacidad, c.CascadaVelocidad, c.CascadaCantidad,
	)
	return err
}

// SaveLogo actualiza únicamente la ruta del logo guardado.
func SaveLogo(conn *sql.DB, ruta string) error {
	_, err := conn.Exec(`UPDATE config_sitio SET logo_ruta = $1, actualizado_en = now() WHERE id = 1`, ruta)
	return err
}
