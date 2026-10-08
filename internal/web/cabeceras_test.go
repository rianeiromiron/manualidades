package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCSPNoPermiteScriptsInline(t *testing.T) {
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	for _, directiva := range strings.Split(csp, ";") {
		directiva = strings.TrimSpace(directiva)
		if strings.HasPrefix(directiva, "script-src") && strings.Contains(directiva, "'unsafe-inline'") {
			t.Errorf("script-src permite scripts inline: %q", directiva)
		}
	}
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("falta script-src 'self' en la CSP: %q", csp)
	}
}

var (
	reScriptInline  = regexp.MustCompile(`(?is)<script\b[^>]*>`)
	reScriptSrc     = regexp.MustCompile(`(?is)\bsrc\s*=`)
	reEventoInline  = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	reJavascriptURL = regexp.MustCompile(`(?i)(href|src|action|formaction)\s*=\s*["']?\s*javascript:`)
)

// Con la CSP sin 'unsafe-inline', cualquier <script> sin src, atributo onXXX
// o URL javascript: en una plantilla simplemente dejaría de funcionar. Esta
// prueba lo detecta antes de que llegue al navegador.
func TestPlantillasSinJavaScriptInline(t *testing.T) {
	archivos, err := filepath.Glob("../../web/templates/*.html")
	if err != nil || len(archivos) == 0 {
		t.Fatalf("no se encontraron plantillas: %v", err)
	}
	for _, f := range archivos {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		for _, etiqueta := range reScriptInline.FindAllString(s, -1) {
			if !reScriptSrc.MatchString(etiqueta) {
				t.Errorf("%s: <script> inline: %s", filepath.Base(f), etiqueta)
			}
		}
		if m := reEventoInline.FindString(s); m != "" {
			t.Errorf("%s: atributo de evento inline (%q)", filepath.Base(f), strings.TrimSpace(m))
		}
		if m := reJavascriptURL.FindString(s); m != "" {
			t.Errorf("%s: URL javascript: (%q)", filepath.Base(f), m)
		}
	}
}
