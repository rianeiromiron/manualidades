package web

import (
	"fmt"
	"strings"
	"time"

	"manualidades/internal/correo"
	"manualidades/internal/cuentas"
	"manualidades/internal/tienda"
)

// Los textos de los correos de la tienda. Son texto plano a propósito: cuentan
// con pocos datos y evitan los problemas de renderizado de HTML en correos.

func textoCuentas(lista []cuentas.Cuenta) string {
	var b strings.Builder
	for i, c := range lista {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "  Banco: %s\n  Cuenta %s en %s: %s\n  A nombre de: %s\n", c.Banco, strings.ToLower(c.TipoNombre()), c.Moneda, c.Numero, c.Titular)
		if c.Instrucciones != "" {
			fmt.Fprintf(&b, "  Nota: %s\n", c.Instrucciones)
		}
	}
	return b.String()
}

func fechaLarga(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Local().Format("02/01/2006 15:04")
}

// correoPedidoCreado es el correo que recibe el cliente al crear su pedido por
// transferencia: datos para pagar y el enlace para subir el comprobante.
func correoPedidoCreado(p tienda.Pedido, lista []cuentas.Cuenta, enlace string) correo.Mensaje {
	envio := ""
	if p.CostoEnvio > 0 {
		envio = fmt.Sprintf(" (incluye Q %.2f de envío a domicilio)", p.CostoEnvio)
	}
	return correo.Mensaje{
		Para:   []string{p.ClienteEmail},
		Asunto: fmt.Sprintf("Tu pedido #%d: datos para tu transferencia", p.ID),
		Texto: fmt.Sprintf(`Hola %s,

Recibimos tu pedido #%d por Q %.2f%s. Para completarlo, haz una transferencia por ese monto a una de estas cuentas:

%s
Usa el número de pedido (#%d) como referencia.

Cuando la hayas hecho, sube la foto de tu comprobante aquí:
%s

Guardamos tus productos hasta el %s. Después de esa fecha el pedido se cancela y los productos vuelven a estar disponibles.

Conserva este correo: el enlace es la forma de volver a tu pedido. Si lo pierdes, puedes pedirlo de nuevo en la sección «Consultar mi pedido» de la tienda.
`, p.ClienteNombre, p.ID, p.Total, envio, textoCuentas(lista), p.ID, enlace, fechaLarga(p.ExpiraEn)),
	}
}

// correoEnlacePedido reenvía el enlace a quien lo pidió desde «Consultar mi pedido».
func correoEnlacePedido(nombre string, id int, para, enlace string) correo.Mensaje {
	return correo.Mensaje{
		Para:   []string{para},
		Asunto: fmt.Sprintf("Enlace de tu pedido #%d", id),
		Texto: fmt.Sprintf(`Hola %s,

Pediste el enlace de tu pedido #%d. Aquí lo tienes:
%s

Si no lo pediste tú, puedes ignorar este correo.
`, nombre, id, enlace),
	}
}

// correoComprobanteRecibido avisa al negocio que hay una transferencia por revisar.
func correoComprobanteRecibido(para []string, p tienda.Pedido, d tienda.DatosComprobante, base string) correo.Mensaje {
	admin := "(configura la dirección de la tienda en la pantalla Correo para recibir el enlace)"
	if base != "" {
		admin = base + "/admin/pedidos/" + fmt.Sprint(p.ID)
	}
	return correo.Mensaje{
		Para:   para,
		Asunto: fmt.Sprintf("Transferencia por revisar: pedido #%d", p.ID),
		Texto: fmt.Sprintf(`Hay una transferencia por revisar.

Pedido: #%d
Cliente: %s (%s, %s)
Monto del pedido: Q %.2f

Lo que indicó el cliente:
  Banco de origen: %s
  Número de boleta: %s
  Fecha: %s

Revísala en tu banca en línea (busca el monto y la boleta) y luego apruébala o recházala aquí:
%s
`, p.ID, p.ClienteNombre, p.ClienteTelefono, p.ClienteEmail, p.Total, d.BancoOrigen, d.NumeroBoleta,
			d.Fecha.Format("02/01/2006"), admin),
	}
}

// correoTransferenciaAprobada confirma al cliente que su pago se verificó.
func correoTransferenciaAprobada(p tienda.Pedido, enlace string) correo.Mensaje {
	entrega := "Puedes pasar a recogerlo a la tienda cuando te avisemos que está listo."
	if p.MetodoEntrega == "domicilio" {
		entrega = "Te lo entregaremos en: " + p.DireccionEntrega + "."
	}
	ver := ""
	if enlace != "" {
		ver = "\nPuedes ver el estado de tu pedido aquí:\n" + enlace + "\n"
	}
	return correo.Mensaje{
		Para:   []string{p.ClienteEmail},
		Asunto: fmt.Sprintf("Confirmamos tu pago: pedido #%d", p.ID),
		Texto: fmt.Sprintf(`Hola %s,

Confirmamos tu transferencia por Q %.2f. ¡Gracias! Tu pedido #%d ya está pagado y lo estamos preparando.

%s
%s`, p.ClienteNombre, p.Total, p.ID, entrega, ver),
	}
}

// correoTransferenciaRechazada explica al cliente por qué no se aceptó su
// comprobante y qué puede hacer: subir otro (con plazo nuevo) o, si el pedido
// se canceló, ponerse en contacto con la tienda.
func correoTransferenciaRechazada(p tienda.Pedido, motivo, enlace string, cancelado bool) correo.Mensaje {
	if cancelado {
		return correo.Mensaje{
			Para:   []string{p.ClienteEmail},
			Asunto: fmt.Sprintf("Tu pedido #%d fue cancelado", p.ID),
			Texto: fmt.Sprintf(`Hola %s,

No pudimos confirmar tu transferencia y cancelamos el pedido #%d. Los productos volvieron a estar disponibles.

Motivo: %s

Si ya hiciste el pago o crees que es un error, comunícate con la tienda y ten a mano tu comprobante.
`, p.ClienteNombre, p.ID, motivo),
		}
	}
	subir := "Responde a este correo o comunícate con la tienda."
	if enlace != "" {
		subir = "Sube un comprobante nuevo aquí:\n" + enlace
	}
	return correo.Mensaje{
		Para:   []string{p.ClienteEmail},
		Asunto: fmt.Sprintf("No pudimos confirmar tu transferencia: pedido #%d", p.ID),
		Texto: fmt.Sprintf(`Hola %s,

No pudimos confirmar la transferencia de tu pedido #%d.

Motivo: %s

%s

Guardamos tus productos hasta el %s.
`, p.ClienteNombre, p.ID, motivo, subir, fechaLarga(p.ExpiraEn)),
	}
}
