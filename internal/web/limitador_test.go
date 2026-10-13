package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func peticion(remoteAddr string, cabeceras map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range cabeceras {
		r.Header.Set(k, v)
	}
	return r
}

func TestClientIPSinProxyIgnoraCabeceras(t *testing.T) {
	r := peticion("203.0.113.9:4000", map[string]string{
		"CF-Connecting-IP": "198.51.100.1",
		"X-Forwarded-For":  "198.51.100.2",
	})
	if got := clientIPDetrasDe(r, nil); got != "203.0.113.9" {
		t.Fatalf("sin proxies configurados debe usar RemoteAddr, dio %q", got)
	}
}

func TestClientIPDeUnProxyNoConfiableIgnoraCabeceras(t *testing.T) {
	proxies := cargarProxiesConfiables("127.0.0.1")
	r := peticion("203.0.113.9:4000", map[string]string{"CF-Connecting-IP": "198.51.100.1"})
	if got := clientIPDetrasDe(r, proxies); got != "203.0.113.9" {
		t.Fatalf("una conexión que no viene del proxy no puede fijar su IP, dio %q", got)
	}
}

func TestClientIPUsaCloudflareSiVieneDelProxy(t *testing.T) {
	proxies := cargarProxiesConfiables("127.0.0.1, ::1")
	r := peticion("127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "198.51.100.1"})
	if got := clientIPDetrasDe(r, proxies); got != "198.51.100.1" {
		t.Fatalf("debía usar CF-Connecting-IP, dio %q", got)
	}
}

func TestClientIPAceptaCIDRYIPv6(t *testing.T) {
	proxies := cargarProxiesConfiables("10.0.0.0/8,::1")
	if got := clientIPDetrasDe(peticion("10.1.2.3:1", map[string]string{"CF-Connecting-IP": "198.51.100.1"}), proxies); got != "198.51.100.1" {
		t.Fatalf("el CIDR no se aplicó, dio %q", got)
	}
	if got := clientIPDetrasDe(peticion("[::1]:1", map[string]string{"CF-Connecting-IP": "2001:db8::7"}), proxies); got != "2001:db8::7" {
		t.Fatalf("IPv6 no se aplicó, dio %q", got)
	}
}

func TestClientIPXForwardedForTomaLaUltimaIPNoConfiable(t *testing.T) {
	proxies := cargarProxiesConfiables("127.0.0.1,192.0.2.0/24")
	// El cliente intentó hacerse pasar por 1.1.1.1; el proxy añadió su IP real
	// (198.51.100.7) y un segundo proxy de confianza (192.0.2.5) la suya.
	r := peticion("127.0.0.1:5000", map[string]string{
		"X-Forwarded-For": "1.1.1.1, 198.51.100.7, 192.0.2.5",
	})
	if got := clientIPDetrasDe(r, proxies); got != "198.51.100.7" {
		t.Fatalf("no debía fiarse de lo que escribió el cliente, dio %q", got)
	}
}

func TestClientIPRechazaValoresQueNoSonIP(t *testing.T) {
	proxies := cargarProxiesConfiables("127.0.0.1")
	casos := []map[string]string{
		{"CF-Connecting-IP": "no-es-una-ip"},
		{"CF-Connecting-IP": "<script>alert(1)</script>"},
		{"X-Forwarded-For": "basura, mas basura"},
		{},
	}
	for _, cab := range casos {
		if got := clientIPDetrasDe(peticion("127.0.0.1:5000", cab), proxies); got != "127.0.0.1" {
			t.Errorf("con %v debía quedarse con RemoteAddr, dio %q", cab, got)
		}
	}
}

func TestCargarProxiesConfiablesIgnoraEntradasInvalidas(t *testing.T) {
	redes := cargarProxiesConfiables("127.0.0.1, , nada, 10.0.0.0/99, 192.0.2.0/24")
	if len(redes) != 2 {
		t.Fatalf("debían quedar 2 redes válidas, quedaron %d", len(redes))
	}
}

// El caso que motivó el cambio: detrás de Cloudflare, dos visitantes distintos
// no deben compartir contador.
func TestLimitadorSeparaVisitantesDetrasDeProxy(t *testing.T) {
	proxies := cargarProxiesConfiables("127.0.0.1")
	l := nuevoLimitador(2, 1<<40, 1<<40)
	atacante := clientIPDetrasDe(peticion("127.0.0.1:1", map[string]string{"CF-Connecting-IP": "198.51.100.1"}), proxies)
	cliente := clientIPDetrasDe(peticion("127.0.0.1:2", map[string]string{"CF-Connecting-IP": "198.51.100.2"}), proxies)
	l.Contar(atacante)
	l.Contar(atacante)
	if l.Permitido(atacante) {
		t.Fatal("el atacante debía quedar bloqueado")
	}
	if !l.Permitido(cliente) {
		t.Fatal("otro visitante no debía quedar bloqueado")
	}
}
