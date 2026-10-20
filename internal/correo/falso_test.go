package correo

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

// recibido es lo que el servidor falso vio en una sesión.
type recibido struct {
	Usuario string
	Desde   string
	Para    []string
	Datos   string
}

// falso es un servidor SMTP mínimo para las pruebas: habla STARTTLS o SSL con
// un certificado propio y valida usuario y contraseña.
type falso struct {
	ln          net.Listener
	tlsCfg      *tls.Config
	roots       *x509.CertPool
	ssl         bool   // abre ya cifrado
	sinStartTLS bool   // no ofrece STARTTLS
	usuario     string // credenciales válidas
	password    string
	rechaza     string // destinatario que responde 550

	mu        sync.Mutex
	sesiones  []recibido
	vioAuth   bool
	vioClaro  bool // llegó una autenticación sin cifrar
	terminada chan struct{}
}

func certificadoDePrueba(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	clave, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	plantilla := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:         true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, plantilla, plantilla, &clave.PublicKey, clave)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: clave}, pool
}

func nuevoFalso(t *testing.T, ssl, sinStartTLS bool) *falso {
	t.Helper()
	cert, pool := certificadoDePrueba(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &falso{
		ln: ln, roots: pool, ssl: ssl, sinStartTLS: sinStartTLS,
		tlsCfg:  &tls.Config{Certificates: []tls.Certificate{cert}},
		usuario: "tienda@ejemplo.com", password: "clave-de-aplicacion",
		terminada: make(chan struct{}),
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				close(f.terminada)
				return
			}
			go f.atender(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *falso) puerto() int { return f.ln.Addr().(*net.TCPAddr).Port }

// config devuelve una Config que apunta a este servidor.
func (f *falso) config() Config {
	seg := STARTTLS
	if f.ssl {
		seg = SSL
	}
	return Config{
		Servidor: "127.0.0.1", Puerto: f.puerto(), Seguridad: seg,
		Usuario: f.usuario, Password: f.password,
		DesdeNombre: "Manualidades Cristal", DesdeCorreo: f.usuario,
	}
}

func (f *falso) ultima(t *testing.T) recibido {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sesiones {
		if s.Datos != "" {
			return s
		}
	}
	t.Fatal("el servidor no recibió ningún mensaje")
	return recibido{}
}

func (f *falso) atender(raw net.Conn) {
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(10 * time.Second))
	var conn net.Conn = raw
	cifrado := false
	if f.ssl {
		conn = tls.Server(raw, f.tlsCfg)
		cifrado = true
	}
	var ses recibido
	r := textproto.NewReader(bufio.NewReader(conn))
	resp := func(s string) { io.WriteString(conn, s+"\r\n") }

	resp("220 falso ESMTP")
	for {
		linea, err := r.ReadLine()
		if err != nil {
			return
		}
		cmd := strings.ToUpper(linea)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			io.WriteString(conn, "250-falso\r\n")
			if !cifrado && !f.sinStartTLS {
				io.WriteString(conn, "250-STARTTLS\r\n")
			}
			io.WriteString(conn, "250-AUTH PLAIN\r\n250 8BITMIME\r\n")
		case cmd == "STARTTLS":
			resp("220 listo")
			conn = tls.Server(raw, f.tlsCfg)
			r = textproto.NewReader(bufio.NewReader(conn))
			cifrado = true
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			f.mu.Lock()
			f.vioAuth = true
			if !cifrado {
				f.vioClaro = true
			}
			f.mu.Unlock()
			b, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(linea[len("AUTH PLAIN"):]))
			partes := strings.Split(string(b), "\x00")
			if len(partes) == 3 && partes[1] == f.usuario && partes[2] == f.password {
				ses.Usuario = partes[1]
				resp("235 2.7.0 aceptado")
			} else {
				resp("535 5.7.8 Usuario o contraseña no aceptados")
			}
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			ses.Desde = direccionEntre(linea[len("MAIL FROM:"):])
			resp("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			d := direccionEntre(linea[len("RCPT TO:"):])
			if f.rechaza != "" && d == f.rechaza {
				resp("550 5.1.1 no existe")
				continue
			}
			ses.Para = append(ses.Para, d)
			resp("250 ok")
		case cmd == "DATA":
			resp("354 adelante")
			b, _ := io.ReadAll(r.DotReader())
			// DotReader entrega los saltos como LF; en el cable eran CRLF.
			ses.Datos = strings.ReplaceAll(string(b), "\n", "\r\n")
			resp("250 ok")
		case cmd == "QUIT":
			resp("221 adiós")
			f.mu.Lock()
			f.sesiones = append(f.sesiones, ses)
			f.mu.Unlock()
			return
		default:
			resp("250 ok")
		}
	}
}

// direccionEntre extrae lo que hay entre < y > (ignora parámetros como BODY=).
func direccionEntre(s string) string {
	i, j := strings.Index(s, "<"), strings.Index(s, ">")
	if i < 0 || j < i {
		return strings.TrimSpace(s)
	}
	return s[i+1 : j]
}
