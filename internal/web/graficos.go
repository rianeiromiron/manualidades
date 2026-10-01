package web

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"strings"
	"unicode/utf8"
)

// Gráficos SVG generados en el servidor, sin librerías. Todo texto que viene
// de la base (nombres de producto, etc.) se escapa aquí porque el resultado se
// devuelve como template.HTML y html/template ya no lo toca.

const (
	colorTerracota = "var(--terracotta)"
	colorOro       = "var(--gold)"
	colorSalvia    = "var(--sage)"
	colorTinta     = "var(--ink-soft)"
)

// miles formatea con separador de miles y dos decimales: 1234.5 → "1,234.50".
func miles(v float64) string {
	neg := v < 0
	s := fmt.Sprintf("%.2f", math.Abs(v))
	ent, dec, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range ent {
		if i > 0 && (len(ent)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String() + "." + dec
	if neg && out != "0.00" {
		return "-" + out
	}
	return out
}

func quetzales(v float64) string { return "Q " + miles(v) }

// entero formatea sin decimales si el valor es entero (unidades), con dos si no.
func cantidadTexto(v float64) string {
	if v == math.Trunc(v) {
		s := miles(v)
		return strings.TrimSuffix(s, ".00")
	}
	return miles(v)
}

// cortar acorta un texto largo para que quepa en la etiqueta de un gráfico.
func cortar(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

// escala devuelve un máximo "redondo" y el paso entre marcas para un eje que
// empieza en 0 y llega al menos a max, con unas 4 divisiones.
func escala(max float64) (tope, paso float64) {
	if max <= 0 {
		return 1, 0.25
	}
	crudo := max / 4
	mag := math.Pow(10, math.Floor(math.Log10(crudo)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if crudo <= m*mag {
			paso = m * mag
			break
		}
	}
	return paso * math.Ceil(max/paso), paso
}

// abreviar acorta cifras del eje: 12500 → "12.5k".
func abreviar(v float64) string {
	switch {
	case math.Abs(v) >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", v/1_000_000), ".0") + "M"
	case math.Abs(v) >= 10_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", v/1_000), ".0") + "k"
	case v == math.Trunc(v):
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

type serieGrafico struct {
	Nombre  string
	Color   string
	Valores []float64
}

// barrasApiladas dibuja una barra por etiqueta con las series apiladas
// (p. ej. ventas de tienda + manuales por día). Devuelve "" si todo es cero.
func barrasApiladas(etiquetas []string, series []serieGrafico, formato func(float64) string) template.HTML {
	n := len(etiquetas)
	if n == 0 {
		return ""
	}
	totales := make([]float64, n)
	var max float64
	for _, s := range series {
		for i, v := range s.Valores {
			totales[i] += v
		}
	}
	for _, t := range totales {
		if t > max {
			max = t
		}
	}
	if max == 0 {
		return ""
	}
	tope, paso := escala(max)

	const w, h = 720.0, 300.0
	const izq, der, arriba, abajo = 56.0, 12.0, 34.0, 46.0
	ancho := w - izq - der
	alto := h - arriba - abajo
	y := func(v float64) float64 { return arriba + alto - v/tope*alto }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="grafico" viewBox="0 0 %.0f %.0f" role="img" xmlns="http://www.w3.org/2000/svg">`, w, h)

	// Leyenda.
	x := izq
	for _, s := range series {
		fmt.Fprintf(&b, `<rect x="%.0f" y="10" width="12" height="12" style="fill:%s"/><text x="%.0f" y="20" class="g-txt">%s</text>`,
			x, s.Color, x+17, html.EscapeString(s.Nombre))
		x += 17 + float64(utf8.RuneCountInString(s.Nombre))*7 + 22
	}

	// Cuadrícula y eje Y.
	for v := 0.0; v <= tope+paso/2; v += paso {
		fmt.Fprintf(&b, `<line x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f" class="g-grid"/><text x="%.0f" y="%.1f" class="g-txt g-eje" text-anchor="end">%s</text>`,
			izq, w-der, y(v), y(v), izq-6, y(v)+4, abreviar(v))
	}

	// Barras.
	paraCada := ancho / float64(n)
	bw := math.Min(paraCada*0.72, 44)
	cadaCuantas := int(math.Ceil(float64(n) / 12))
	for i := 0; i < n; i++ {
		cx := izq + paraCada*(float64(i)+0.5)
		base := 0.0
		var tip strings.Builder
		tip.WriteString(html.EscapeString(etiquetas[i]))
		for _, s := range series {
			v := s.Valores[i]
			tip.WriteString(fmt.Sprintf(" · %s: %s", html.EscapeString(s.Nombre), formato(v)))
		}
		fmt.Fprintf(&b, `<g><title>%s</title>`, tip.String())
		for _, s := range series {
			v := s.Valores[i]
			if v <= 0 {
				continue
			}
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" style="fill:%s"/>`,
				cx-bw/2, y(base+v), bw, math.Max(y(base)-y(base+v), 0.5), s.Color)
			base += v
		}
		// Franja transparente para que el tooltip salga también sobre barras bajas.
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" style="fill:transparent"/></g>`,
			cx-paraCada/2, arriba, paraCada, alto)
		if i%cadaCuantas == 0 {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.0f" class="g-txt g-eje" text-anchor="middle">%s</text>`,
				cx, h-abajo+18, html.EscapeString(etiquetas[i]))
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

type barraH struct {
	Etiqueta string
	Valor    float64
	Texto    string // valor ya formateado, para mostrar junto a la barra
	Color    string // vacío = terracota
}

// barrasHorizontales dibuja un ranking: una fila por elemento, con barra
// proporcional al valor (admite negativos). Devuelve "" si no hay datos.
func barrasHorizontales(items []barraH) template.HTML {
	if len(items) == 0 {
		return ""
	}
	min, max := 0.0, 0.0
	for _, it := range items {
		min = math.Min(min, it.Valor)
		max = math.Max(max, it.Valor)
	}
	if max == 0 && min == 0 {
		return ""
	}

	const w = 720.0
	const etiq, der, fila = 200.0, 110.0, 30.0
	h := float64(len(items))*fila + 8
	ancho := w - etiq - der
	rango := max - min
	cero := etiq + (-min)/rango*ancho
	x := func(v float64) float64 { return etiq + (v-min)/rango*ancho }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="grafico" viewBox="0 0 %.0f %.0f" role="img" xmlns="http://www.w3.org/2000/svg">`, w, h)
	for i, it := range items {
		top := 4 + float64(i)*fila
		color := it.Color
		if color == "" {
			color = colorTerracota
		}
		x0, x1 := cero, x(it.Valor)
		if x1 < x0 {
			x0, x1 = x1, x0
		}
		fmt.Fprintf(&b, `<g><title>%s: %s</title><text x="%.0f" y="%.1f" class="g-txt" text-anchor="end">%s</text>`,
			html.EscapeString(it.Etiqueta), html.EscapeString(it.Texto), etiq-8, top+fila/2+4, html.EscapeString(cortar(it.Etiqueta, 30)))
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" style="fill:%s"/>`,
			x0, top+4, math.Max(x1-x0, 1), fila-10, color)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" class="g-txt g-val">%s</text></g>`,
			math.Max(x(it.Valor), cero)+6, top+fila/2+4, html.EscapeString(it.Texto))
	}
	if min < 0 {
		fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="0" y2="%.0f" class="g-grid g-cero"/>`, cero, cero, h)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

type tajada struct {
	Etiqueta string
	Valor    float64
	Texto    string
	Color    string
}

// dona dibuja un anillo con su leyenda al lado. Devuelve "" si la suma es 0.
func dona(items []tajada) template.HTML {
	var total float64
	for _, it := range items {
		total += it.Valor
	}
	if total <= 0 {
		return ""
	}

	const r, grosor = 70.0, 30.0
	circ := 2 * math.Pi * r
	var b strings.Builder
	b.WriteString(`<svg class="grafico grafico-dona" viewBox="0 0 520 190" role="img" xmlns="http://www.w3.org/2000/svg">`)
	fmt.Fprintf(&b, `<g transform="translate(100 95) rotate(-90)">`)
	var acum float64
	for _, it := range items {
		if it.Valor <= 0 {
			continue
		}
		largo := it.Valor / total * circ
		fmt.Fprintf(&b, `<circle r="%.0f" fill="none" stroke-width="%.0f" style="stroke:%s" stroke-dasharray="%.2f %.2f" stroke-dashoffset="%.2f"><title>%s: %s</title></circle>`,
			r, grosor, it.Color, largo, circ-largo, -acum, html.EscapeString(it.Etiqueta), html.EscapeString(it.Texto))
		acum += largo
	}
	b.WriteString(`</g>`)
	for i, it := range items {
		y := 40 + float64(i)*30
		fmt.Fprintf(&b, `<rect x="210" y="%.0f" width="14" height="14" style="fill:%s"/><text x="232" y="%.0f" class="g-txt">%s</text><text x="232" y="%.0f" class="g-txt g-val">%s (%.0f%%)</text>`,
			y-11, it.Color, y, html.EscapeString(it.Etiqueta), y+14, html.EscapeString(it.Texto), it.Valor/total*100)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}
