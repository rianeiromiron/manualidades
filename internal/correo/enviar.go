package correo

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	// tiempoConexion limita abrir la conexión; tiempoTotal, todo el envío. Un
	// servidor que no responde no debe dejar colgada la petición del admin.
	tiempoConexion = 10 * time.Second
	tiempoTotal    = 30 * time.Second
)

// Mensaje es un correo de texto plano.
type Mensaje struct {
	Para   []string
	Asunto string
	Texto  string
}

// Etapas de un envío, para explicar dónde falló.
const (
	EtapaConexion     = "conexion"
	EtapaSeguridad    = "seguridad"
	EtapaAutenticar   = "autenticacion"
	EtapaRemitente    = "remitente"
	EtapaDestinatario = "destinatario"
	EtapaEnvio        = "envio"
)

// ErrEnvio dice en qué etapa falló un envío y con qué código SMTP (0 si no hubo
// respuesta del servidor). Descripcion es un texto seguro para mostrar.
type ErrEnvio struct {
	Etapa  string
	Codigo int
	Causa  error
}

func (e *ErrEnvio) Error() string { return fmt.Sprintf("correo (%s): %v", e.Etapa, e.Causa) }
func (e *ErrEnvio) Unwrap() error { return e.Causa }

// Descripcion explica el fallo en español, sin datos sensibles.
func (e *ErrEnvio) Descripcion(c Config) string {
	codigo := ""
	if e.Codigo != 0 {
		codigo = " (código " + strconv.Itoa(e.Codigo) + ")"
	}
	switch e.Etapa {
	case EtapaConexion:
		return fmt.Sprintf("No se pudo conectar con %s en el puerto %d. Revisa el servidor y el puerto, y que el servidor de la tienda pueda salir a internet por ese puerto.", c.Servidor, c.Puerto)
	case EtapaSeguridad:
		return "No se pudo establecer la conexión cifrada. Revisa que la seguridad (STARTTLS o SSL) corresponda al puerto: 587 usa STARTTLS y 465 usa SSL/TLS."
	case EtapaAutenticar:
		msg := "El servidor rechazó el usuario o la contraseña" + codigo + "."
		if strings.HasSuffix(c.Servidor, "gmail.com") {
			msg += " Con Gmail hay que usar una contraseña de aplicación (no la contraseña normal de la cuenta), y la verificación en dos pasos debe estar activa."
		}
		return msg
	case EtapaRemitente:
		return "El servidor no aceptó el correo del remitente" + codigo + ". Suele pasar cuando no coincide con la cuenta con la que se inició sesión."
	case EtapaDestinatario:
		return "El servidor no aceptó uno de los destinatarios" + codigo + "."
	default:
		return "El servidor no aceptó el mensaje" + codigo + "."
	}
}

func codigoDe(err error) int {
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code
	}
	return 0
}

func falla(etapa string, err error) error {
	return &ErrEnvio{Etapa: etapa, Codigo: codigoDe(err), Causa: err}
}

// Enviar manda un correo con la configuración dada.
func Enviar(c Config, m Mensaje) error { return enviar(c, m, nil) }

// enviar es Enviar con una raíz de confianza TLS opcional (las pruebas usan un
// certificado propio).
func enviar(c Config, m Mensaje, roots *x509.CertPool) error {
	if !c.Configurado() {
		return ErrNoConfigurado
	}
	cuerpo, from, para, err := armar(c, m)
	if err != nil {
		return err
	}

	direccion := net.JoinHostPort(c.Servidor, strconv.Itoa(c.Puerto))
	tlsCfg := &tls.Config{ServerName: c.Servidor, MinVersion: tls.VersionTLS12, RootCAs: roots}
	dialer := &net.Dialer{Timeout: tiempoConexion}

	var conn net.Conn
	if c.Seguridad == SSL {
		conn, err = tls.DialWithDialer(dialer, "tcp", direccion, tlsCfg)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) || isDial(err) {
				return falla(EtapaConexion, err)
			}
			return falla(EtapaSeguridad, err)
		}
	} else {
		conn, err = dialer.Dial("tcp", direccion)
		if err != nil {
			return falla(EtapaConexion, err)
		}
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(tiempoTotal))

	cli, err := smtp.NewClient(conn, c.Servidor)
	if err != nil {
		return falla(EtapaConexion, err)
	}
	defer cli.Close()

	if err := cli.Hello(nombreLocal(c.DesdeCorreo)); err != nil {
		return falla(EtapaConexion, err)
	}
	if c.Seguridad == STARTTLS {
		if ok, _ := cli.Extension("STARTTLS"); !ok {
			return falla(EtapaSeguridad, errors.New("el servidor no ofrece STARTTLS"))
		}
		if err := cli.StartTLS(tlsCfg); err != nil {
			return falla(EtapaSeguridad, err)
		}
	}
	if c.Usuario != "" {
		if err := cli.Auth(smtp.PlainAuth("", c.Usuario, c.Password, c.Servidor)); err != nil {
			return falla(EtapaAutenticar, err)
		}
	}
	if err := cli.Mail(from); err != nil {
		return falla(EtapaRemitente, err)
	}
	for _, d := range para {
		if err := cli.Rcpt(d); err != nil {
			return falla(EtapaDestinatario, err)
		}
	}
	w, err := cli.Data()
	if err != nil {
		return falla(EtapaEnvio, err)
	}
	if _, err := w.Write(cuerpo); err != nil {
		return falla(EtapaEnvio, err)
	}
	if err := w.Close(); err != nil {
		return falla(EtapaEnvio, err)
	}
	cli.Quit()
	return nil
}

// isDial reconoce los errores de red al abrir la conexión TLS directa (que
// tls.DialWithDialer mezcla con los de certificado).
func isDial(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// nombreLocal es el nombre con el que nos presentamos al servidor (EHLO): el
// dominio del remitente.
func nombreLocal(desde string) string {
	if i := strings.LastIndex(desde, "@"); i >= 0 && i < len(desde)-1 {
		return desde[i+1:]
	}
	return "localhost"
}

// armar valida el mensaje y arma el correo completo (cabeceras y cuerpo).
// Rechaza saltos de línea en asunto y direcciones: así nada que venga de un
// cliente puede colar cabeceras extra (Bcc, etc.).
func armar(c Config, m Mensaje) (cuerpo []byte, from string, para []string, err error) {
	if len(m.Para) == 0 {
		return nil, "", nil, errors.New("el mensaje no tiene destinatarios")
	}
	if len(m.Para) > MaxDestinatarios {
		return nil, "", nil, errors.New("el mensaje tiene demasiados destinatarios")
	}
	for _, p := range m.Para {
		d, err := direccion("El destinatario", p)
		if err != nil {
			return nil, "", nil, err
		}
		para = append(para, d)
	}
	for _, r := range m.Asunto {
		if unicode.IsControl(r) {
			return nil, "", nil, errors.New("el asunto no puede contener saltos de línea")
		}
	}

	remitente := mail.Address{Name: c.DesdeNombre, Address: c.DesdeCorreo}
	var b bytes.Buffer
	cab := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	cab("From", remitente.String())
	cab("To", strings.Join(para, ", "))
	cab("Subject", mime.QEncoding.Encode("utf-8", m.Asunto))
	cab("Date", time.Now().Format(time.RFC1123Z))
	cab("Message-ID", "<"+idAleatorio()+"@"+nombreLocal(c.DesdeCorreo)+">")
	cab("MIME-Version", "1.0")
	cab("Content-Type", `text/plain; charset="utf-8"`)
	cab("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := io.WriteString(qp, normalizarSaltos(m.Texto)); err != nil {
		return nil, "", nil, err
	}
	qp.Close()
	return b.Bytes(), c.DesdeCorreo, para, nil
}

func normalizarSaltos(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func idAleatorio() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}
