package web

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// limitador cuenta intentos por clave (normalmente la IP del cliente) dentro
// de una ventana de tiempo, y bloquea la clave un rato al superar el máximo.
// Vive en memoria del proceso: un reinicio del servidor limpia todos los
// contadores. Es intencionalmente simple (sin Redis ni tabla en Postgres)
// porque el objetivo es frenar automatización (fuerza bruta, prueba de
// tarjetas), no llevar una auditoría persistente de intentos.
type limitador struct {
	mu        sync.Mutex
	maximo    int
	ventana   time.Duration
	bloqueo   time.Duration
	registros map[string]*registroIntentos
}

type registroIntentos struct {
	intentos       int
	desde          time.Time
	bloqueadoHasta time.Time
}

func nuevoLimitador(maximo int, ventana, bloqueo time.Duration) *limitador {
	return &limitador{
		maximo:    maximo,
		ventana:   ventana,
		bloqueo:   bloqueo,
		registros: make(map[string]*registroIntentos),
	}
}

// Permitido indica si clave puede intentar ahora mismo. No cuenta como
// intento por sí solo: eso lo hace Contar, una vez que el llamador sabe qué
// intento se está haciendo.
func (l *limitador) Permitido(clave string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.registros[clave]
	return !ok || time.Now().After(r.bloqueadoHasta)
}

// Contar suma un intento para clave. Al llegar al máximo dentro de la
// ventana, bloquea la clave durante `bloqueo`.
func (l *limitador) Contar(clave string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	r, ok := l.registros[clave]
	if !ok || now.Sub(r.desde) > l.ventana {
		r = &registroIntentos{desde: now}
		l.registros[clave] = r
	}
	r.intentos++
	if r.intentos >= l.maximo {
		r.bloqueadoHasta = now.Add(l.bloqueo)
	}
	l.limpiarVencidos(now)
}

// Limpiar borra el contador de clave: un login correcto, o un pago aprobado,
// no deben seguir arrastrando los intentos fallidos previos.
func (l *limitador) Limpiar(clave string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.registros, clave)
}

// limpiarVencidos evita que el mapa crezca sin límite si muchas IPs distintas
// fallan alguna vez y nunca vuelven a intentar. Se llama con el lock ya
// tomado, y solo barre cuando el mapa ya es grande, para no pagar ese costo
// en cada intento normal.
func (l *limitador) limpiarVencidos(now time.Time) {
	if len(l.registros) < 1000 {
		return
	}
	for clave, r := range l.registros {
		if now.Sub(r.desde) > l.ventana && now.After(r.bloqueadoHasta) {
			delete(l.registros, clave)
		}
	}
}

// clientIP extrae la IP del cliente de RemoteAddr (host:puerto). No confía en
// cabeceras como X-Forwarded-For: sin un proxy de confianza configurado
// delante, esa cabecera la puede mandar cualquiera y anularía el límite.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginLimiter frena la fuerza bruta contra /admin/login: 5 intentos
// fallidos por IP en 10 minutos bloquean esa IP 15 minutos. Se cuenta por IP
// y no por usuario para no abrir la puerta a que cualquiera bloquee la cuenta
// del admin real solo fallando el password muchas veces desde otra parte.
var loginLimiter = nuevoLimitador(5, 10*time.Minute, 15*time.Minute)

// checkoutLimiter frena la prueba automatizada de tarjetas y el acaparo de
// reservas de stock por IP: 8 intentos de pago en 10 minutos bloquean esa IP
// 10 minutos. Cuenta tanto los aprobados como los rechazados porque el límite
// es sobre la frecuencia de intentos, no sobre el resultado; un pago aprobado
// limpia el contador para no frenar la siguiente compra legítima.
var checkoutLimiter = nuevoLimitador(8, 10*time.Minute, 10*time.Minute)
