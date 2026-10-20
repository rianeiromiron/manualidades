package correo

import (
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func texto(t *testing.T, datos string) (cabeceras, cuerpo string) {
	t.Helper()
	i := strings.Index(datos, "\r\n\r\n")
	if i < 0 {
		t.Fatalf("mensaje sin cuerpo: %q", datos)
	}
	b, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(datos[i+4:])))
	if err != nil {
		t.Fatal(err)
	}
	return datos[:i], string(b)
}

func TestEnviaPorSTARTTLSConAutenticacion(t *testing.T) {
	f := nuevoFalso(t, false, false)
	m := Mensaje{
		Para:   []string{"dueña@ejemplo.com", "bodega@ejemplo.com"},
		Asunto: "Transferencia por revisar – pedido #12",
		Texto:  "Hola, ñandú.\nSegunda línea.",
	}
	if err := enviar(f.config(), m, f.roots); err != nil {
		t.Fatal(err)
	}
	s := f.ultima(t)
	if s.Usuario != f.usuario || s.Desde != f.usuario {
		t.Errorf("sesión = %+v", s)
	}
	if len(s.Para) != 2 || s.Para[0] != "dueña@ejemplo.com" {
		t.Errorf("destinatarios = %v", s.Para)
	}
	cab, cuerpo := texto(t, s.Datos)
	if !strings.Contains(cuerpo, "Hola, ñandú.\r\nSegunda línea.") {
		t.Errorf("cuerpo = %q", cuerpo)
	}
	// El asunto viaja codificado y se recupera igual.
	var asunto string
	for _, l := range strings.Split(cab, "\r\n") {
		if strings.HasPrefix(l, "Subject: ") {
			asunto, _ = new(mime.WordDecoder).DecodeHeader(strings.TrimPrefix(l, "Subject: "))
		}
	}
	if asunto != m.Asunto {
		t.Errorf("asunto = %q, quiero %q", asunto, m.Asunto)
	}
	for _, quiero := range []string{"From: ", "Manualidades Cristal", "Message-ID: <", "Date: ", "MIME-Version: 1.0", "text/plain"} {
		if !strings.Contains(cab, quiero) {
			t.Errorf("faltan cabeceras (%q): %s", quiero, cab)
		}
	}
}

func TestEnviaPorSSL(t *testing.T) {
	f := nuevoFalso(t, true, false)
	if err := enviar(f.config(), Mensaje{Para: []string{"a@ejemplo.com"}, Asunto: "x", Texto: "y"}, f.roots); err != nil {
		t.Fatal(err)
	}
	if got := f.ultima(t).Usuario; got != f.usuario {
		t.Errorf("usuario autenticado = %q", got)
	}
}

func TestContrasenaIncorrectaSeExplica(t *testing.T) {
	f := nuevoFalso(t, false, false)
	c := f.config()
	c.Password = "otra"
	err := enviar(c, Mensaje{Para: []string{"a@ejemplo.com"}, Asunto: "x", Texto: "y"}, f.roots)
	var e *ErrEnvio
	if !errors.As(err, &e) || e.Etapa != EtapaAutenticar || e.Codigo != 535 {
		t.Fatalf("err = %v, quiero ErrEnvio de autenticación 535", err)
	}
	if d := e.Descripcion(c); strings.Contains(d, "Gmail") || !strings.Contains(d, "535") {
		t.Errorf("descripción = %q", d)
	}
	c.Servidor = "smtp.gmail.com"
	if d := e.Descripcion(c); !strings.Contains(d, "contraseña de aplicación") {
		t.Errorf("con Gmail debe sugerir la contraseña de aplicación: %q", d)
	}
	if strings.Contains(e.Descripcion(c), c.Password) {
		t.Error("la descripción no debe contener la contraseña")
	}
}

// Si el servidor no ofrece cifrado, no se manda la contraseña ni el correo.
func TestSinStartTLSNoSeEnviaLaContrasena(t *testing.T) {
	f := nuevoFalso(t, false, true)
	err := enviar(f.config(), Mensaje{Para: []string{"a@ejemplo.com"}, Asunto: "x", Texto: "y"}, f.roots)
	var e *ErrEnvio
	if !errors.As(err, &e) || e.Etapa != EtapaSeguridad {
		t.Fatalf("err = %v, quiero fallo de seguridad", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.vioAuth {
		t.Fatal("se intentó autenticar sin cifrado")
	}
}

func TestCertificadoNoConfiableSeRechaza(t *testing.T) {
	f := nuevoFalso(t, false, false)
	err := enviar(f.config(), Mensaje{Para: []string{"a@ejemplo.com"}, Asunto: "x", Texto: "y"}, nil) // sin la raíz de prueba
	var e *ErrEnvio
	if !errors.As(err, &e) || e.Etapa != EtapaSeguridad {
		t.Fatalf("err = %v, quiero fallo de seguridad", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.vioAuth {
		t.Fatal("se mandó la contraseña a un servidor con certificado no confiable")
	}
}

func TestDestinatarioRechazado(t *testing.T) {
	f := nuevoFalso(t, false, false)
	f.rechaza = "malo@ejemplo.com"
	err := enviar(f.config(), Mensaje{Para: []string{"bueno@ejemplo.com", "malo@ejemplo.com"}, Asunto: "x", Texto: "y"}, f.roots)
	var e *ErrEnvio
	if !errors.As(err, &e) || e.Etapa != EtapaDestinatario || e.Codigo != 550 {
		t.Fatalf("err = %v, quiero destinatario 550", err)
	}
}

func TestServidorQueNoResponde(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	puerto := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nadie escucha ahí
	c := Config{Servidor: "127.0.0.1", Puerto: puerto, Seguridad: STARTTLS, DesdeCorreo: "a@ejemplo.com"}
	err := enviar(c, Mensaje{Para: []string{"b@ejemplo.com"}, Asunto: "x", Texto: "y"}, nil)
	var e *ErrEnvio
	if !errors.As(err, &e) || e.Etapa != EtapaConexion {
		t.Fatalf("err = %v, quiero fallo de conexión", err)
	}
	if !strings.Contains(e.Descripcion(c), "127.0.0.1") {
		t.Errorf("la descripción debe nombrar el servidor: %q", e.Descripcion(c))
	}
}

func TestNoSePuedenColarCabeceras(t *testing.T) {
	f := nuevoFalso(t, false, false)
	malos := []Mensaje{
		{Para: []string{"a@ejemplo.com"}, Asunto: "hola\r\nBcc: robo@malo.com", Texto: "x"},
		{Para: []string{"a@ejemplo.com"}, Asunto: "hola\nBcc: robo@malo.com", Texto: "x"},
		{Para: []string{"a@ejemplo.com\r\nBcc: robo@malo.com"}, Asunto: "x", Texto: "x"},
		{Para: []string{"Otro <a@ejemplo.com>"}, Asunto: "x", Texto: "x"},
		{Para: nil, Asunto: "x", Texto: "x"},
	}
	for i, m := range malos {
		if err := enviar(f.config(), m, f.roots); err == nil {
			t.Errorf("caso %d: se aceptó un mensaje inválido", i)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sesiones {
		if s.Datos != "" {
			t.Fatalf("se envió algo: %q", s.Datos)
		}
	}
}

func TestSinConfiguracionNoEnvia(t *testing.T) {
	if err := Enviar(Config{}, Mensaje{Para: []string{"a@ejemplo.com"}}); !errors.Is(err, ErrNoConfigurado) {
		t.Fatalf("err = %v, quiero ErrNoConfigurado", err)
	}
}

// ---- Configuración ----

func validaCfg() Config {
	return Config{
		Servidor: "smtp.gmail.com", Puerto: 587, Seguridad: STARTTLS,
		Usuario: "tienda@gmail.com", Password: "abcd efgh ijkl mnop",
		DesdeNombre: "Manualidades Cristal", DesdeCorreo: "tienda@gmail.com",
	}
}

func enTemporal(t *testing.T) {
	t.Helper()
	previo := archivo
	archivo = filepath.Join(t.TempDir(), "smtp.json")
	t.Cleanup(func() { archivo = previo })
}

func TestLimpiarValida(t *testing.T) {
	casos := map[string]func(*Config){
		"servidor vacío":           func(c *Config) { c.Servidor = "" },
		"servidor con espacios":    func(c *Config) { c.Servidor = "smtp gmail.com" },
		"servidor con barra":       func(c *Config) { c.Servidor = "smtp.gmail.com/x" },
		"puerto cero":              func(c *Config) { c.Puerto = 0 },
		"puerto enorme":            func(c *Config) { c.Puerto = 70000 },
		"sin cifrado":              func(c *Config) { c.Seguridad = "ninguna" },
		"usuario con salto":        func(c *Config) { c.Usuario = "a\nb" },
		"usuario sin contraseña":   func(c *Config) { c.Password = "" },
		"remitente inválido":       func(c *Config) { c.DesdeCorreo = "no-es-correo" },
		"remitente con nombre":     func(c *Config) { c.DesdeCorreo = "Ana <a@b.com>" },
		"remitente con salto":      func(c *Config) { c.DesdeCorreo = "a@b.com\r\nBcc: x@y.z" },
		"nombre con salto":         func(c *Config) { c.DesdeNombre = "Ana\r\nBcc: x@y.z" },
		"contraseña con control":   func(c *Config) { c.Password = "ab\x00cd" },
		"demasiados destinatarios": func(c *Config) { c.Destinatarios = make([]string, MaxDestinatarios+1) },
	}
	for nombre, rompe := range casos {
		c := validaCfg()
		rompe(&c)
		if _, err := c.Limpiar(); !errors.Is(err, ErrValidacion) {
			t.Errorf("%s: err = %v, quiero ErrValidacion", nombre, err)
		}
	}
	c, err := validaCfg().Limpiar()
	if err != nil {
		t.Fatal(err)
	}
	if c.Password != "abcdefghijklmnop" {
		t.Errorf("la contraseña de aplicación de Gmail debe quedar sin espacios: %q", c.Password)
	}
	// Un servidor sin usuario (relé interno) es válido.
	sinAuth := validaCfg()
	sinAuth.Usuario, sinAuth.Password = "", ""
	if _, err := sinAuth.Limpiar(); err != nil {
		t.Errorf("sin autenticación: %v", err)
	}
}

func TestParseDestinatarios(t *testing.T) {
	l, err := ParseDestinatarios("a@x.com, b@x.com;\nA@X.com\r\n  ")
	if err != nil || len(l) != 2 || l[0] != "a@x.com" || l[1] != "b@x.com" {
		t.Fatalf("l = %v, err = %v", l, err)
	}
	if l, err := ParseDestinatarios(""); err != nil || len(l) != 0 {
		t.Errorf("vacío: %v, %v", l, err)
	}
	for _, malo := range []string{"a@x.com, nada", "Ana <a@x.com>", "a@x.com\r\nBcc: z@z.z,"} {
		if _, err := ParseDestinatarios(malo); !errors.Is(err, ErrValidacion) {
			t.Errorf("%q: err = %v", malo, err)
		}
	}
	muchos := strings.Repeat("a@x.com,", 0)
	for i := 0; i <= MaxDestinatarios; i++ {
		muchos += string(rune('a'+i)) + "@x.com,"
	}
	if _, err := ParseDestinatarios(muchos); !errors.Is(err, ErrValidacion) {
		t.Errorf("demasiados: err = %v", err)
	}
}

func TestGuardarYLeer(t *testing.T) {
	enTemporal(t)
	if c, err := Load(); err != nil || c.Configurado() {
		t.Fatalf("sin archivo: %+v, %v", c, err)
	}
	c := validaCfg()
	c.Destinatarios = []string{"dueña@x.com"}
	if _, err := Save(c); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(archivo); info.Mode().Perm() != 0o600 {
			t.Errorf("permisos = %v, quiero 600", info.Mode().Perm())
		}
	}
	if _, err := os.Stat(archivo + ".tmp"); err == nil {
		t.Error("quedó el archivo temporal")
	}
	g, err := Load()
	if err != nil || !g.Configurado() || g.Password != "abcdefghijklmnop" || len(g.Destinatarios) != 1 {
		t.Fatalf("leído = %+v, %v", g, err)
	}
}

func TestContrasenaEnBlancoConservaLaGuardada(t *testing.T) {
	enTemporal(t)
	Save(validaCfg())

	nueva := validaCfg()
	nueva.Password = ""
	nueva.DesdeNombre = "Otro nombre"
	if _, err := Save(nueva); err != nil {
		t.Fatal(err)
	}
	g, _ := Load()
	if g.Password != "abcdefghijklmnop" || g.DesdeNombre != "Otro nombre" {
		t.Fatalf("leído = %+v", g)
	}

	// Pero no se reutiliza hacia otro servidor ni otro usuario.
	otroServidor := validaCfg()
	otroServidor.Password = ""
	otroServidor.Servidor = "smtp.malo.com"
	if _, err := Save(otroServidor); !errors.Is(err, ErrValidacion) {
		t.Fatalf("otro servidor con contraseña en blanco: err = %v", err)
	}
	otroUsuario := validaCfg()
	otroUsuario.Password = ""
	otroUsuario.Usuario = "otro@gmail.com"
	if _, err := Save(otroUsuario); !errors.Is(err, ErrValidacion) {
		t.Fatalf("otro usuario con contraseña en blanco: err = %v", err)
	}
	if g, _ := Load(); g.Servidor != "smtp.gmail.com" || g.Usuario != "tienda@gmail.com" {
		t.Fatalf("un guardado rechazado cambió el archivo: %+v", g)
	}
}

func TestGuardarInvalidoNoTocaElArchivo(t *testing.T) {
	enTemporal(t)
	Save(validaCfg())
	mala := validaCfg()
	mala.Puerto = 0
	if _, err := Save(mala); !errors.Is(err, ErrValidacion) {
		t.Fatalf("err = %v", err)
	}
	if g, _ := Load(); g.Puerto != 587 {
		t.Fatalf("puerto = %d, quiero 587", g.Puerto)
	}
}

func TestLimpiarUrlTienda(t *testing.T) {
	buenos := map[string]string{
		"":                                       "",
		"  ":                                     "",
		"https://tienda.ejemplo.com":             "https://tienda.ejemplo.com",
		"https://tienda.ejemplo.com/":            "https://tienda.ejemplo.com",
		"https://prueba.manualidadescristal.com": "https://prueba.manualidadescristal.com",
		"https://tienda.ejemplo.com:8443":        "https://tienda.ejemplo.com:8443",
		"http://localhost:8090":                  "http://localhost:8090",
		"http://127.0.0.1:8090/":                 "http://127.0.0.1:8090",
	}
	for entrada, quiero := range buenos {
		if got, err := LimpiarUrlTienda(entrada); err != nil || got != quiero {
			t.Errorf("%q → (%q, %v), quiero %q", entrada, got, err, quiero)
		}
	}
	malos := []string{
		"http://tienda.ejemplo.com", // sin cifrado fuera de localhost
		"ftp://tienda.ejemplo.com",
		"tienda.ejemplo.com", // sin esquema
		"https://",
		"https://tienda.ejemplo.com/ruta",
		"https://tienda.ejemplo.com?x=1",
		"https://tienda.ejemplo.com/#frag",
		"https://usuario:clave@tienda.ejemplo.com",
		"https://tienda ejemplo.com",
		"https://tienda.ejemplo.com\r\nBcc: x@y.z",
		"javascript:alert(1)",
	}
	for _, m := range malos {
		if got, err := LimpiarUrlTienda(m); !errors.Is(err, ErrValidacion) {
			t.Errorf("%q → (%q, %v), quiero ErrValidacion", m, got, err)
		}
	}
}
