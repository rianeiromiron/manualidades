package web

import "net/http"

// contentSecurityPolicy limita de dónde puede cargar recursos cada página:
// nada de scripts, estilos o fuentes de dominios ajenos salvo los que el
// sitio ya usa (Google Fonts), y bloquea que otro sitio la incruste en un
// <iframe> (frame-ancestors). No se restringe 'unsafe-inline' en
// script-src/style-src porque las plantillas usan JS y estilos inline
// extensamente (ver web/templates); quitarlo implicaría mover ese código a
// archivos aparte en cada plantilla, un cambio mucho más grande que agregar
// esta cabecera. Sigue bloqueando lo más común: cargar un script, un
// <object> o un iframe ajeno inyectado por un XSS.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"img-src 'self'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// SecurityHeaders agrega, a toda respuesta (admin, tienda y estáticos), un
// conjunto de cabeceras que no cambian el comportamiento normal del sitio
// pero sí lo protegen de ataques comunes del lado del navegador:
//
//   - X-Content-Type-Options evita que el navegador "adivine" un tipo de
//     contenido distinto al declarado (mitiga ciertos XSS vía archivos
//     subidos, ej. una "foto" que en realidad es HTML).
//   - X-Frame-Options bloquea que el sitio se cargue dentro de un <iframe>
//     de otra página (clickjacking); frame-ancestors en el CSP hace lo
//     mismo para los navegadores que ya lo soportan.
//   - Referrer-Policy no manda la URL completa como referer a otros sitios
//     (por ejemplo, al abrir un link externo desde una página con datos en
//     la query string).
//   - Strict-Transport-Security solo se manda si la conexión ya es HTTPS
//     (ver esConexionSegura): mandarla sobre HTTP no tiene efecto en los
//     navegadores, así que no tiene sentido instruir algo que no aplica.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		if esConexionSegura(r) {
			h.Set("Strict-Transport-Security", "max-age=15552000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
