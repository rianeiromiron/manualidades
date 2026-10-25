# Manualidades

Sistema de gestión y tienda en línea para un negocio de manualidades (bolsas,
monederos y artículos típicos tejidos a mano). Un solo binario en Go sirve
tanto el **panel de administración** como la **tienda pública**, contra una
base de datos Postgres.

Hoy la tienda vende por **transferencia bancaria**: el cliente transfiere, sube
su comprobante y el negocio lo revisa y lo aprueba desde el admin. El cobro con
**tarjeta** está construido contra una pasarela *simulada*; se activa o
desactiva desde el panel (Bancos → Formas de pago) mientras llega la pasarela
real de un banco.

No usa ningún framework de frontend: las páginas se renderizan del lado del
servidor con `html/template`, y el JavaScript que existe es vanilla,
escrito a mano, y deliberadamente mínimo (filtros, un carrito en
`localStorage` con tope de stock, y formularios con campos condicionales). Todo vive
en archivos de `web/static/js/`: las plantillas no llevan `<script>` ni
atributos de evento, para poder aplicar una CSP sin `'unsafe-inline'`.

---

## Dónde encontrar cada cosa

| Si quieres saber… | Mira |
|---|---|
| Qué está hecho y qué falta | [Estado del proyecto](#estado-del-proyecto) y [Roadmap](#roadmap) |
| Cómo levantarlo en tu máquina | [Cómo levantarlo localmente](#cómo-levantarlo-localmente) |
| Cómo está armado por dentro (checkout, reservas, transferencia, revisión, cancelaciones, correo, seguridad, permisos) | [Arquitectura](#arquitectura) |
| Dónde corre en producción y cómo se despliega y se respalda | [Producción](#producción) |
| Qué guarda la base de datos | [Modelo de datos](#modelo-de-datos) |
| Qué direcciones existen, en el admin y en la tienda | [Rutas](#rutas) |
| Qué hace cada pantalla del admin | [Los mantenimientos, explicados](#los-mantenimientos-explicados) |
| Cómo compra un cliente, con tarjeta o por transferencia | [La tienda pública](#la-tienda-pública) |
| Cómo se prueba | [Pruebas](#pruebas) |
| Por qué se hizo así, y qué límites tiene | [Decisiones de diseño](#decisiones-de-diseño-que-vale-la-pena-conocer) y [Limitaciones conocidas](#limitaciones-conocidas) |

---

## Estado del proyecto

| Fase | Qué es | Estado |
|---|---|---|
| Mantenimiento 1 | Conexión a la base de datos (Postgres) | ✅ Hecho |
| Mantenimiento 2 | Inventario: categorías, productos, fotos, kardex, reporte | ✅ Hecho |
| Mantenimiento 3 | Configuración del sitio: datos del negocio, logo, colores y **costo de envío a domicilio** | ✅ Hecho |
| Tienda — Fase 1 | Catálogo paginado (20 por página, filtro por categoría en el servidor), carrito con tope de stock y checkout con tarjeta (pago **simulado**) | ✅ Hecho |
| Estado del pedido | Pagado → Procesando en bodega / Entregado, manejado desde admin; **Cancelar pedido** como acción propia | ✅ Hecho |
| Reserva de stock | El checkout reserva el stock **antes** de cobrar (pedido `pendiente_pago`/`pagando`, con vencimiento) y solo descarga el kardex cuando el pago se aprueba | ✅ Hecho |
| Seguridad del admin | Login con usuario/contraseña, todo `/admin/*` protegido, sesiones revocables, CSRF, límite de intentos, cabeceras y CSP estricta | ✅ Hecho |
| Usuarios y permisos | Superusuarios y usuarios administrativos con módulos asignables | ✅ Hecho |
| Pasarela de pago | Registro de pagos (tabla `pagos`) + pasarela simulada (`pasarela-simulada`) con 3 modos: aprobar/rechazar/fallar | ✅ Hecho (simulada) |
| Pago por transferencia | Pedido que reserva stock 48 h, página privada con enlace por token, subida del comprobante (con monto), cambiar la entrega o cancelar, "Consultar mi pedido" y avisos por correo | ✅ Hecho |
| Revisión de transferencias | En `/admin/pedidos/{id}`: ver el comprobante, comparar el monto indicado con el total, aprobar (descuenta el inventario) o rechazar con un motivo | ✅ Hecho |
| Cancelaciones y reembolsos | Cancelar un pedido pagado devuelve el inventario (anulando sus salidas del kardex) y deja el reembolso pendiente de anotar | ✅ Hecho |
| Cuentas bancarias y formas de pago | Cuentas a las que se puede transferir (con historial de cambios) y activar/desactivar Tarjeta y Transferencia desde el admin | ✅ Hecho |
| Correo saliente | Servidor SMTP configurable (STARTTLS/SSL, p. ej. Gmail), correos de aviso y botón de prueba | ✅ Hecho |
| Reportes | Ventas, productos, utilidad, stock, pedidos y movimientos, con gráficos | ✅ Hecho |
| Despliegue | Servidor propio gratuito (Oracle Cloud) detrás de Cloudflare, respaldos cifrados diarios (con copia en Backblaze B2) y script de despliegue con reversión automática; tienda de prueba en `prueba.manualidadescristal.com` (ver [Producción](#producción)) | ✅ Hecho |
| Tienda — Fase 2 | Preparación de pedidos en bodega (flujo de picking/empaque) | ⬜ Pendiente (estados ya modelados) |
| Tienda — Fase 3 | Pasarela de pago real para la tarjeta (hoy desactivada en el servidor) | ⬜ Pendiente (el punto de reemplazo ya está aislado en `internal/pasarela`) |
| Validación de NIT contra SAT | — | ⬜ Pendiente |

---

## Stack tecnológico

- **Go 1.24** — `net/http` + [gorilla/mux](https://github.com/gorilla/mux) para rutas
- **Postgres** vía [lib/pq](https://github.com/lib/pq): un contenedor Docker en desarrollo y Postgres 16 instalado en el servidor en producción
- **html/template** de la librería estándar — sin React/Vue/htmx
- **JavaScript vanilla** — sin build step, sin npm, sin bundlers
- **CSS puro** — variables CSS (`:root`) y `color-mix()` para temas derivados; sin Tailwind ni preprocesadores
- **Correo saliente** — `net/smtp` de la librería estándar (STARTTLS o SSL); no suma dependencias

Dependencias (`go.mod`):

```
github.com/gorilla/mux v1.8.1
github.com/lib/pq v1.10.9
github.com/google/uuid v1.6.0   (nombres de archivo únicos al subir fotos)
golang.org/x/image v0.30.0      (decodificar WebP y reducir fotos al subirlas)
golang.org/x/crypto v0.31.0     (solo bcrypt, para las contraseñas)
github.com/chromedp/chromedp    (solo pruebas e2e con un navegador real)
```

---

## Cómo levantarlo localmente

1. **Postgres**: necesitas un servidor Postgres accesible (probado con
   `postgres:latest` en Docker). No hace falta crear la base de antemano —
   el propio Mantenimiento 1 la crea.

2. **Arrancar el servidor**:
   ```bash
   cd manualidades
   go run ./cmd/server
   ```
   Escucha en `http://localhost:8090`. La primera vez, ni `config.json` ni
   `admin.json` existen todavía, así que la app arranca sin conexión a base
   de datos (no truena — sirve la tienda y el panel admin en modo "sin BD").

3. **Crear el acceso al admin**: cualquier visita a `/admin/...` sin
   `admin.json` redirige a `http://localhost:8090/admin/setup` — crea ahí tu
   usuario y contraseña (esto no depende de la base de datos). Después inicia
   sesión en `/admin/login`.

4. **Configurar la base de datos**: ya adentro, entra a
   `http://localhost:8090/admin/mantenimiento/bd`, llena host/puerto/usuario/
   contraseña/nombre de base, y usa "Crear base de datos si no existe"
   seguido de "Guardar y probar conexión". Esto:
   - Escribe `config.json` en la raíz del proyecto (con la contraseña en
     texto plano — está en `.gitignore`, nunca se sube a control de
     versiones).
   - Corre automáticamente las migraciones de todos los esquemas (inventario,
     sitio, tienda, usuarios, sesiones y cuentas) — son idempotentes, se puede reconectar cuantas veces
     haga falta sin duplicar nada.

5. **Ya está**: `http://localhost:8090/admin` es el panel de administración,
   `http://localhost:8090/` es la tienda pública (sin autenticación, como debe
   ser).

6. **Pasarela de pago simulada** (solo para pagar con tarjeta; la transferencia no la necesita):
   es un proyecto hermano, `../pasarela-simulada` (su propio `go.mod`).
   ```bash
   cd ../pasarela-simulada
   go run .
   ```
   Escucha en `http://localhost:8091`. Su panel (`http://localhost:8091`) tiene 3 botones para forzar el
   resultado de cualquier cobro que le mande Manualidades: **Aprobar todo**,
   **Rechazar todo**, o **Simular caída** (responde 503, como si el
   proveedor no contestara). Arranca siempre en modo "Aprobar todo". Si no
   está corriendo, el checkout falla igual que si el proveedor real
   estuviera caído (es exactamente el mismo código que maneja ambos casos).

7. **Probar el pago por transferencia** (no necesita la pasarela):
   - En `/admin/cuentas` crea al menos una cuenta bancaria activa (una de
     prueba); sin ella la transferencia no se ofrece en el checkout.
   - Opcional: en `/admin/correo` configura un servidor SMTP (ver
     [Correo saliente](#correo-saliente)). Sin él todo funciona igual, pero los
     correos no se envían (queda anotado en el registro); el cliente ve su
     enlace en pantalla.
   - Haz un pedido eligiendo **Transferencia bancaria**, sube un comprobante
     desde la página del pedido y apruébalo en `/admin/pedidos`.

Archivos que la app crea o lee y que **no se suben a git** (`.gitignore`):
`config.json` (Postgres), `admin.json` (admin raíz), `smtp.json` (correo
saliente, con su contraseña), `media/` (fotos y logo) y `comprobantes/`
(imágenes de las transferencias).

No hace falta reiniciar el servidor para ver cambios en plantillas (`.html`)
o estilos (`.css`) — se leen del disco en cada petición. Sí hace falta
reiniciar (`go run` de nuevo) para cambios en archivos `.go`.

---

## Arquitectura

### El patrón `App`

`internal/web/app.go` define un único struct `App` con las dependencias
compartidas por todos los handlers:

```go
type App struct {
	conn         *sql.DB           // protegido por mutex; puede cambiar en caliente
	Storage      storage.Storage   // fotos de producto
	LogoStorage  storage.Storage   // logo del sitio
	Comprobantes storage.Storage   // imágenes de transferencias; carpeta privada (comprobantes/)
	EnviarCorreo func(correo.Config, correo.Mensaje) error // nil = correo.Enviar; las pruebas lo reemplazan
}
```

`app.DB()` / `app.SetDB()` permiten reconectar la base de datos en tiempo de
ejecución (cuando se guarda el Mantenimiento 1) sin reiniciar el proceso.
`app.requireDB(w, "activo")` es el guard que usan casi todos los handlers del
admin: si no hay conexión, renderiza `sin_bd.html` y el handler corta ahí.

### Abstracción de almacenamiento (`internal/storage`)

```go
type Storage interface {
	Save(filename string, data io.Reader) (path string, err error)
	URL(path string) string
	Delete(path string) error
}
```

Hoy solo existe `Local` (guarda en disco, sirve por una ruta estática). Es
la pieza pensada para producción: si el hosting no tiene disco persistente,
se agrega un `internal/storage/s3.go` que implemente la misma interfaz
(Cloudflare R2 u otro S3-compatible) y no hay que tocar nada más — ni el
resto de los handlers que ya usan `Storage`, ni las plantillas.

Los comprobantes de transferencia usan la misma implementación `Local`, pero en
una carpeta aparte (`comprobantes/`) que el router **no sirve**: solo el admin
puede verlos, por una ruta protegida. `Local.Abrir` rechaza cualquier nombre que
traiga carpetas o `..`.

### Pasarela de pago simulada

Manualidades nunca decide por sí misma si un cobro se aprueba: siempre se
lo pregunta a `pasarela-simulada`, un proyecto hermano (`../pasarela-simulada`,
módulo Go independiente) que hace de supuesto proveedor externo de cobros
con tarjeta. Es intencional que viva fuera del módulo `manualidades`: en
producción sería un proceso/servicio real y separado, así que ya se
comporta así en desarrollo.

- **Cliente HTTP** (`internal/pasarela/cliente.go`): `pasarela.Cobrar(...)`
  hace `POST /cobros` contra `pasarela.BaseURL()` (default
  `http://localhost:8091`, override con la variable de entorno
  `PASARELA_URL`). Cualquier error de red, timeout, o respuesta que no sea
  HTTP 200 se traduce a `ErrNoDisponible` — eso es una **falla técnica**
  del proveedor, no una decisión de negocio.
- Un HTTP 200 con `{"aprobado": false, "motivo": "..."}` **no** es un error
  Go — es una respuesta de negocio válida (tarjeta rechazada), exactamente
  como se comporta una pasarela real.
- El panel de `pasarela-simulada` (`http://localhost:8091`) tiene un
  interruptor de 3 modos para forzar cualquiera de los tres desenlaces
  (aprobar / rechazar / fallar) sin necesidad de tarjetas reales.

**Flujo de checkout** (`TiendaCheckoutConfirmar` en
`internal/web/handlers_tienda.go`, lógica en `internal/tienda/checkout.go`).
La idea central: **reservar el stock antes de cobrar**, para que el cliente
nunca pague por algo que ya no hay.

1. `tienda.CotizarCarrito` calcula precios reales desde el catálogo (nunca
   confía en lo que manda el navegador) y junta líneas repetidas. No valida
   stock: eso lo hace el paso siguiente, con los productos bloqueados.
2. `tienda.IniciarPago` — **transacción corta**: bloquea las filas de los
   productos, valida el stock *disponible* (físico − reservas vigentes de
   otros pedidos), crea el pedido en estado `pagando` con su vencimiento
   (`expira_en`) y deja el intento de pago en `pagos` como `iniciado`. Si no
   alcanza el stock → `409` y **no se cobra nada**. El kardex todavía no se
   toca: el stock queda *reservado*, no descargado.
3. `pasarela.Cobrar(...)` — esta llamada de red **nunca** ocurre dentro de
   una transacción SQL (una transacción no debe quedar abierta esperando a
   un tercero).
4. Según el resultado:
   - **Aprobado** → `tienda.ConfirmarPago` (transacción corta): escribe un
     consumo por producto en el kardex, marca el pago `aprobado` y el pedido
     `pagado`. Todo o nada.
   - **Rechazado o falla técnica** → `tienda.RegistrarCobroFallido`: el
     pago queda `rechazado`/`fallo` y el pedido **vuelve a `pendiente_pago`
     conservando la reserva** (20 min) para que el cliente reintente.
   - **Aprobado pero `ConfirmarPago` falla** (típicamente porque el admin
     consumió stock físico a mano mientras el cliente pagaba) →
     rollback completo y `tienda.MarcarPorConciliar`: el pago queda
     **`por_conciliar`** con la referencia real del cobro y el motivo, y el
     pedido pasa a `expirado`. El negocio lo **reembolsa o completa a mano**
     (no hay reversa automática en la pasarela). Si ni ese registro se puede
     guardar, queda en el log con la referencia y el monto.

**Estados de un pedido** (`internal/tienda/pedidos.go`):

```
Tarjeta:
  pendiente_pago ──► pagando ──► pagado ──► procesando ──► entregado
        ▲               │           └── desde aquí en adelante: manual, desde el admin
        └── rechazo ────┘

Transferencia:
  espera_transferencia ──► transferencia_por_revisar ──► pagado ──► (igual que arriba)
        │                          │
        │                          ├── rechazar, pedir otro ──► espera_transferencia (48 h nuevas)
        │                          └── rechazar y cancelar ───► expirado
        └── cancela el cliente o el negocio ───────────────────► cancelado

pendiente_pago / pagando / espera_transferencia ──► expirado   (la reserva venció o se liberó)
pagado / procesando / entregado ──► cancelado                  (acción "Cancelar pedido", definitiva)
```

**Reservan stock:** `pendiente_pago` y `pagando` (hasta `expira_en`: 20 y 5
minutos), `espera_transferencia` (48 horas) y `transferencia_por_revisar` (sin
vencimiento: espera a que el negocio decida). `pagado` en adelante ya está en el
kardex. Un pedido `cancelado` ya no reserva nada.

**Reintento de pago.** Al crear el pedido se genera un `token` aleatorio
(UUID) que el servidor manda en la cookie `pedido_token` (HttpOnly,
SameSite=Lax); el carrito sigue en `localStorage`. Si el pago falla y el
cliente vuelve a presionar **Pagar** (otra tarjeta, corregir un dato), el
servidor encuentra su pedido por el token y **lo reutiliza**: reemplaza sus
líneas y datos por los actuales y reinicia el cobro, sin crear otro pedido.
Se usa un token y no el `id` porque el `id` es secuencial y cualquiera podría
consultar pedidos ajenos. Si la reserva ya venció, se crea un pedido nuevo y
se vuelve a validar el stock (puede que ya no haya).

**Quién puede ver la confirmación.** La cookie `pedido_token` no se borra al
pagar: es la llave de `/pedido/{id}/confirmacion`. Esa página muestra el
nombre, la dirección y el detalle del cliente, y como el `id` es secuencial,
solo se sirve si el token de la cookie coincide con el del pedido
(`tienda.PedidoPerteneceAToken`); en cualquier otro caso responde `404`, igual
que un pedido inexistente. Tras un pago aprobado la cookie vive 24 horas. Una
compra nueva no la reutiliza (los pedidos pagados se ignoran al buscar un
pedido reintentable) y la reemplaza por el token del pedido nuevo. Los pedidos
anteriores a la columna `token` no tienen confirmación pública.

**Doble clic / dos pestañas.** Mientras un pedido está en `pagando`, otro
`IniciarPago` con el mismo token devuelve `ErrPedidoEnProceso` (`409`): no se
cobra dos veces.

**Vencimiento de reservas.** No depende de ningún proceso en segundo plano:
todas las consultas de stock disponible ignoran las reservas con
`expira_en < now()`, así que el stock se libera solo. Además,
`tienda.LimpiarExpirados` corre cada minuto (goroutine `limpiarReservas` en
`main.go`) solo para ordenar el estado: pasa a `expirado` los pedidos
vencidos y, si un pedido murió a mitad del cobro (`pagando` vencido con el
pago aún en `iniciado`), deja ese pago en `por_conciliar` porque no se sabe si
la pasarela llegó a cobrar.

**Qué ve el admin.** `/admin/pedidos` lista solo pedidos reales (pagados en
adelante) y, aparte, los "Pendientes de pago" (tarjeta o transferencia, con la
hora en que vence su reserva; las transferencias con comprobante esperan sin
vencer). Cada tabla empieza con el número de pedido, que enlaza a su detalle.
Bodega no ve ni puede cambiar de estado un pedido no pagado.
`/admin/pedidos/pagos` muestra **todos** los intentos, incluidos los
`rechazado`, `fallo`, `iniciado` y `por_conciliar`.

> **Limitación conocida:** si la pasarela tarda más del timeout (10 s) en
> responder, `pasarela.Cobrar` devuelve `ErrNoDisponible` y el intento se
> registra como `fallo` aunque el proveedor pudiera haber cobrado igual. Un
> proveedor real debería exponer una clave de idempotencia o un webhook para
> resolver ese caso; con la pasarela simulada no hay forma de distinguirlo.

Esto es exactamente el punto que la Fase 3 original del proyecto (ver
Roadmap) señalaba como "el único lugar a reemplazar por una pasarela real":
hoy ya está aislado ahí, solo faltaría cambiar `pasarela.BaseURL()` por la
URL de un proveedor de verdad.

### Pago por transferencia bancaria

Es la forma de vender mientras llega la pasarela real. El cliente transfiere por
su cuenta y **sube la foto de su comprobante**; el negocio comprueba el depósito
en su banco y lo aprueba. Código en `internal/tienda` (`transferencia.go`,
`comprobantes.go`, `entrega.go`) y `internal/web/handlers_transferencia.go`.

1. **Qué se ofrece.** El checkout solo muestra las formas de pago *disponibles*
   (`leerFormasPago`): la tarjeta, si el admin la tiene activa; la transferencia,
   si el admin la tiene activa **y** hay al menos una cuenta bancaria activa. Con
   una sola no hay selector; con ninguna el checkout avisa que por ahora no se
   reciben pagos en línea. El servidor lo exige también: `/checkout/confirmar` y
   `/checkout/transferencia` responden `409` si la forma de pago no está
   disponible, aunque la petición se arme a mano.
2. **Crear el pedido** (`POST /checkout/transferencia`, JSON). Exige correo
   válido (ahí llega el enlace), cotiza desde la base, calcula el envío en el
   servidor y llama a `tienda.IniciarTransferencia`: igual que `IniciarPago`,
   bloquea los productos y valida el stock disponible, pero deja el pedido en
   `espera_transferencia` con una reserva de **48 horas** (`TTLTransferencia`),
   sin tocar el kardex ni cobrar nada. Límites contra el acaparamiento de
   inventario: 6 pedidos por hora por IP y 3 pedidos abiertos por correo.
3. **Página privada del pedido** (`/pedido/{token}`). La llave es un UUID
   aleatorio, no el `id` (que es secuencial); solo abre pedidos de
   transferencia. Responde con `Cache-Control: no-store` y
   `Referrer-Policy: no-referrer` (el token va en la dirección). Muestra las
   cuentas activas, el monto exacto, el plazo y el formulario del comprobante.
4. **El comprobante.** Imagen JPG, PNG o WebP de hasta 10 MB (se valida por su
   contenido y se vuelve a codificar con `storage.NormalizarImagen`), más banco
   de origen, número de boleta, fecha (no futura, de hasta 30 días atrás) y
   **monto transferido**. Máximo 5 comprobantes por pedido. Una misma boleta de
   un mismo banco no puede usarse en dos pedidos (índice único parcial; un
   comprobante rechazado libera la suya). `RegistrarComprobante` corre en una
   transacción con el pedido bloqueado: pasa el pedido a
   `transferencia_por_revisar` y le quita el vencimiento — la reserva dura hasta
   que el negocio decida. Si el monto no coincide con el total del pedido se
   acepta (puede faltar un envío), pero se avisa al cliente, al negocio y en el
   admin.
5. **Mientras espera la transferencia**, el cliente puede **cambiar entre
   recoger y domicilio** (`CambiarEntrega`: recalcula el monto; un pedido que ya
   era a domicilio conserva el envío con que se creó; no extiende las 48 h) o
   **cancelar su pedido** (`CancelarPorElCliente`: libera la reserva). Con el
   comprobante ya subido ninguna de las dos cosas se puede: el monto debe
   coincidir con lo transferido y la decisión es del negocio.
6. **"Consultar mi pedido"** (`/consultar-pedido`): el cliente escribe número de
   pedido y correo y, si coinciden, se le **reenvía el enlace a ese correo**. La
   pantalla nunca muestra el pedido y responde siempre lo mismo (coincida o no),
   para no revelar qué pedidos ni qué correos existen. Límites: 5 intentos en 15
   minutos por IP y un reenvío cada 5 minutos por pedido.
7. **Avisos por correo:** al cliente cuando crea el pedido, cambia la entrega, y
   cuando el negocio aprueba o rechaza; al negocio (a los destinatarios
   configurados) cuando entra un comprobante, con una advertencia si el monto no
   coincide. Un fallo de correo solo se registra: no impide crear el pedido ni
   subir el comprobante.

**Los enlaces de los correos no salen de la petición.** La cabecera `Host` la
controla quien hace el pedido: si el enlace se armara con ella, alguien podría
crear un pedido con el correo de otra persona y mandarle un enlace a un sitio
falso. Por eso salen de la **dirección pública de la tienda** configurada en
`/admin/correo`. Sin ella (y fuera de localhost) no se manda ningún enlace por
correo; el cliente igual lo ve en pantalla.

### Revisión de transferencias en el admin

En `/admin/pedidos/{id}` (módulo Pedidos) el detalle de un pedido por
transferencia muestra cada comprobante con su imagen, la boleta, el banco, la
fecha y el **monto indicado**, con una marca *≠ total* y un recuadro de
advertencia si no coincide con el total del pedido. La lista de pedidos avisa
cuántas transferencias hay por revisar.

- **La imagen** se sirve por `/admin/pedidos/{id}/comprobantes/{cid}/archivo`,
  solo a una sesión con acceso a Pedidos. Se busca por id en la base (no por un
  nombre recibido) y se entrega con `X-Content-Type-Options: nosniff`,
  `Cache-Control: private, no-store` y una CSP `default-src 'none'; sandbox`.
- **Aprobar** (`tienda.AprobarTransferencia`) ocurre en **una transacción**:
  bloquea los productos y el pedido, escribe un consumo por producto en el
  kardex, registra el pago como `aprobado` con método `transferencia` (la
  referencia es banco + boleta), marca el comprobante `aceptado` con quién y
  cuándo, y pasa el pedido a `pagado`. Si el stock físico ya no alcanza, no
  cambia nada y el pedido sigue en revisión. Se avisa al cliente por correo.
- **Rechazar** exige un motivo (lo ve el cliente). Con *pedir otro comprobante*
  el pedido vuelve a `espera_transferencia` con 48 horas nuevas; con *cancelar*
  pasa a `expirado` y el stock vuelve a estar disponible. Se avisa por correo.
- **Dos revisores a la vez:** solo gana uno; el otro recibe "ya fue revisado"
  (el pedido se bloquea con `FOR UPDATE`).

### Cancelaciones y reembolsos

`tienda.ActualizarEstado` ya **no** permite cancelar: cancelar toca el
inventario y el dinero, así que solo se hace con la acción **Cancelar pedido**
(`tienda.CancelarPedido`), en una transacción y con confirmación:

| Estado del pedido | Qué pasa al cancelar |
|---|---|
| `espera_transferencia` | Suelta la reserva. No hay inventario que devolver ni dinero que reembolsar. Motivo opcional y aviso por correo al cliente. |
| `transferencia_por_revisar` | No se puede cancelar: hay un posible pago. Se resuelve rechazando el comprobante. |
| `pagado` / `procesando` | Devuelve el inventario y deja el reembolso pendiente. |
| `entregado` | Pregunta si el producto volvió a bodega; solo entonces devuelve el inventario. Deja el reembolso pendiente. |
| `cancelado` | Es **definitivo**: no se puede reabrir ni cancelar de nuevo. |

**Devolver el inventario no borra ni edita nada**: anula los consumos que el
pedido dejó en el kardex (`inventario.AnularMovimientosDePedidoTx`), con el mismo
mecanismo de la anulación manual — el historial se conserva y el índice único de
`anula_a` hace imposible devolver dos veces. Los reportes de ventas ignoran los
pedidos cancelados.

**El reembolso ocurre fuera del sistema** (banco o pasarela). Al cancelar, el
pago aprobado queda con `reembolso = 'pendiente'`; el detalle del pedido lo
muestra en un recuadro, y **Marcar como reembolsado** anota quién y cuándo. Los
pedidos guardan quién canceló y cuándo (`cancelado_por`, `cancelado_en`; el
cliente figura como `cliente`) y si el inventario volvió (`inventario_devuelto`).

### Formas de pago y cuentas bancarias

Ambas se administran en `/admin/cuentas` (módulo **Bancos**, clave `cuentas`):

- **Formas de pago** (`internal/sitio/formas_pago.go`, tabla `formas_pago`):
  Tarjeta y Transferencia se activan o desactivan con un botón, y queda anotado
  quién y cuándo. Nacen activas. Desactivar una forma de pago solo impide crear
  pedidos **nuevos** con ella: un cliente que ya pidió por transferencia puede
  seguir subiendo su comprobante y el negocio puede aprobarlo.
- **Cuentas bancarias** (`internal/cuentas`): banco, tipo (monetaria o ahorro),
  número, titular, moneda (GTQ o USD), instrucciones, orden y activa. Los datos
  se validan en el servidor (sin saltos de línea: terminan en correos). **Cambiar
  una cuenta equivale a desviar los pagos**, así que cada alta, edición,
  activación, desactivación y baja queda en `cuentas_bancarias_historial` con
  quién, desde qué IP y qué cambió (el historial sobrevive a la baja de la cuenta).

### Envío a domicilio

El costo vive en la base de datos (`config_sitio.costo_envio`, Q 45.00 por
omisión) y se cambia en `/admin/sitio`. El servidor lo calcula siempre
(`costoEnvioPara`): el navegador no manda ni puede cambiar ese monto. Se suma
al total de los pedidos a domicilio, tanto con tarjeta como con transferencia, y
se **copia** a `pedidos.costo_envio`: cambiar el costo después no altera
pedidos ya hechos. El checkout muestra la línea de envío en el resumen.

### Correo saliente

`internal/correo` guarda la configuración del servidor de correo y envía con ella.

- **Dónde vive:** `smtp.json`, junto a `config.json` y `admin.json` (permisos
  600, fuera de git, incluido en los respaldos), no en la base de datos, para
  que un volcado de la base no arrastre la contraseña.
- **Qué se configura** en `/admin/correo` (módulo **Correo**): servidor, puerto,
  seguridad (STARTTLS en el 587 o SSL en el 465), usuario, contraseña, nombre y
  correo del remitente, hasta 10 correos que reciben los avisos y la
  **dirección pública de la tienda** (la de los enlaces).
- **Siempre cifrado:** no existe el modo sin cifrar; se verifica el certificado
  y, si el servidor no ofrece cifrado, no se envía la contraseña ni el correo.
- **La contraseña nunca vuelve al navegador.** Dejarla en blanco al guardar
  conserva la anterior, pero solo si no cambiaron el servidor ni el usuario
  (así no viaja a un destino distinto del que se escribió).
- **Gmail:** hay que usar una *contraseña de aplicación* (requiere la
  verificación en dos pasos); la pantalla trae los pasos. Gmail pone siempre su
  propia cuenta como remitente y permite unos 500 correos al día.
- **Botón de prueba:** manda a los correos de aviso (o al remitente), nunca a una
  dirección que llegue en la petición, y una vez cada 10 segundos. Si falla,
  explica la etapa (conexión, cifrado, usuario o contraseña, remitente,
  destinatario) sin mostrar datos internos.
- **Protección de cabeceras:** el asunto y las direcciones no admiten saltos de
  línea, así que nada que venga de un cliente puede colar cabeceras (`Bcc`).
- El envío es asíncrono y un fallo solo se registra (`App.EnviarCorreo` permite
  a las pruebas reemplazarlo).

### Carrito y catálogo

- **Catálogo paginado** (`/?cat=1&cat=2&pagina=2`): 20 productos por página. El
  filtro de categorías y la paginación se resuelven en el servidor (no ocultando
  tarjetas con JavaScript), así que el enlace se puede compartir y "Atrás"
  funciona. Una página fuera de rango se ajusta a la última.
- **El carrito no pasa del stock disponible.** `Carrito.agregar` limita la
  cantidad al stock que trae la página y devuelve cuántas agregó; el botón
  *Agregar al carrito* se desactiva al llegar al máximo (también al recargar o
  volver con "Atrás"), la página del producto explica cuando se pidió más de lo
  que hay, y un carrito que ya traía de más se corrige al abrir el carrito o el
  checkout. El límite usa el stock del momento en que se agregó: el servidor
  vuelve a comprobarlo al crear el pedido, y esa es la barrera real.

### Config de base de datos (`internal/config`)

`config.json` (gitignored) guarda host/puerto/usuario/contraseña/nombre de
base/`sslmode`. Vive en un archivo plano, no en la base de datos misma —
evita la paradoja de necesitar la conexión para leer los datos de conexión.
`config.DSNMaintenance()` construye un DSN contra la base `postgres` (que
siempre existe) para poder emitir `CREATE DATABASE` la primera vez.

### Límites del servidor HTTP

`http.ListenAndServe(addr, r)` a secas no pone ningún límite de tiempo: una
conexión que manda el header o el body a medio byte por segundo (o que nunca
lo termina) se queda abierta indefinidamente, agotando las conexiones
disponibles del servidor (ataque tipo "slowloris"). `cmd/server/main.go` usa
en cambio un `http.Server` con `ReadHeaderTimeout` (5s), `ReadTimeout` (30s),
`WriteTimeout` (30s) e `IdleTimeout` (120s) — de sobra para la subida de fotos
(hasta `maxUploadBytes`, 20 MB) y para el checkout, que puede esperar hasta
10s a la pasarela (`internal/pasarela/cliente.go`).

Por el mismo motivo, `TiendaCheckoutConfirmar` envuelve `r.Body` en
`http.MaxBytesReader` antes de decodificar el JSON (`maxCheckoutBytes`, 1 MiB
en `internal/web/handlers_tienda.go`): sin ese límite, `json.Decode` leería el
body completo sin importar su tamaño. Un carrito real, aunque tenga muchos
productos, no se acerca ni de lejos a ese tope. Los formularios del admin no
necesitaron el mismo tratamiento: `r.ParseForm()` y `r.ParseMultipartForm()`
de la librería estándar ya limitan por su cuenta lo que leen (esta última con
el límite explícito `maxUploadBytes`).

### Cabeceras de seguridad

`web.SecurityHeaders` (`internal/web/cabeceras.go`), montada con `r.Use(...)`
sobre el router raíz en `internal/web/router.go` (aplica a admin, tienda y
estáticos por igual), agrega en toda respuesta:

- `X-Content-Type-Options: nosniff` — evita que el navegador "adivine" un
  tipo de contenido distinto al declarado (por ejemplo, una foto de producto
  subida que en realidad fuera HTML).
- `X-Frame-Options: DENY` — bloquea que el sitio se cargue dentro de un
  `<iframe>` de otra página (clickjacking).
- `Referrer-Policy: strict-origin-when-cross-origin` — no manda la URL
  completa como referer a otros sitios al seguir un link externo.
- `Content-Security-Policy` — limita de dónde puede cargar recursos la
  página: solo el propio origen (y Google Fonts para estilos y fuentes, que ya
  usan `layout.html` / `layout_tienda.html`). Además fija `connect-src 'self'`,
  `img-src 'self'`, `object-src 'none'`, `base-uri 'self'`,
  `form-action 'self'` y `frame-ancestors 'none'` (lo mismo que
  `X-Frame-Options`, pero lo reconocen también los navegadores que ya
  soportan CSP en vez de esa cabecera vieja). **`script-src` no permite
  `'unsafe-inline'`**: todo el JavaScript vive en `web/static/js/`, las
  plantillas no llevan `<script>` ni `onclick`/`onsubmit`, y los datos que un
  script necesita se pasan en atributos `data-*` (p. ej. `data-confirm` en los
  formularios que piden confirmación). Así, aunque un XSS lograra inyectar
  HTML, el navegador no ejecutaría el script. `TestPlantillasSinJavaScriptInline`
  vigila que nadie vuelva a meter JS inline. `style-src` sí conserva
  `'unsafe-inline'` (hay atributos `style` y el tema de la tienda se inyecta
  como `<style>`); un CSS inyectado no ejecuta código, así que el riesgo es
  bajo.
- `Strict-Transport-Security` — solo cuando la conexión ya es HTTPS (ver
  `esConexionSegura` en el punto anterior): mandarla sobre HTTP no tiene
  efecto en los navegadores.

### Render de plantillas

Dos helpers en `internal/web/app.go`:

- `render(w, page, data)` — envuelve `page` con `layout.html` (admin).
- `renderFragment(w, page, data)` — plantilla suelta sin layout, para pedazos
  de HTML inyectados por `fetch()` (ej. el historial de movimientos de un
  producto).

La tienda pública tiene su propio helper, `(a *App) renderTienda(...)` en
`handlers_tienda.go`, que envuelve con `layout_tienda.html` e inyecta
automáticamente `Sitio` (la configuración del negocio) en los datos de toda
página pública, para que el header/footer siempre tengan logo/colores/
contacto sin que cada handler tenga que acordarse de pedirlos.

### Reutilización de lógica de negocio entre inventario y tienda

`inventario.StockActual` recibe una interfaz `querier` (no `*sql.DB`
directamente):

```go
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}
```

Tanto `*sql.DB` como `*sql.Tx` la satisfacen. Para registrar movimientos:

- `inventario.CreateMovimiento(conn *sql.DB, ...)` abre su propia
  transacción (lo usa el panel de admin).
- `inventario.CreateMovimientoTx(tx *sql.Tx, ...)` corre dentro de una
  transacción ya abierta. Esto permite que `tienda.ConfirmarPago` escriba el
  movimiento de consumo de cada producto en el kardex, marque el pago
  aprobado y el pedido pagado, todo dentro de una sola transacción SQL — si
  algo falla a la mitad, no queda nada guardado.

### Concurrencia y transacciones

El acceso a datos es `database/sql` + `lib/pq` con SQL a mano (sin ORM). Las
operaciones de varios pasos usan el patrón `Begin` + `defer tx.Rollback()` +
`Commit` (`IniciarPago`, `ConfirmarPago`, `RegistrarCobroFallido`,
`MarcarPorConciliar`, `LimpiarExpirados`, `CrearUsuario`, `UpdateUsuario`,
`CreateMovimiento`).

Para evitar **sobreventa** entre consumos concurrentes (Postgres corre en
`READ COMMITTED`, así que leer el stock y luego insertar no es atómico),
`inventario.BloquearProductos` hace `SELECT ... FOR UPDATE` sobre las filas
de `productos` involucradas, **siempre en orden ascendente de id** para que
dos transacciones con los mismos productos en distinto orden no queden en
deadlock. `IniciarPago` y `ConfirmarPago` bloquean todos los productos del
pedido antes de validar stock, y `CreateMovimientoTx` bloquea el producto en
cada consumo.

**Orden de bloqueo único:** en todo `internal/tienda/checkout.go` se bloquean
primero los productos (por id) y después la fila del pedido. Un orden
consistente evita deadlocks entre el checkout, la confirmación y la limpieza.

**Ninguna transacción espera a la pasarela.** El cobro ocurre entre dos
transacciones cortas; el pedido en estado `pagando` es lo que impide que
otro cliente se lleve el stock mientras tanto.

**Reloj único.** Los vencimientos se escriben y se comparan con `now()` de
Postgres (nunca con la hora del servidor web), para no depender de que ambos
relojes estén sincronizados.

**Stock disponible ≠ stock físico.** `Producto.Stock` es la existencia según
el kardex (lo que ve el admin). `Producto.Disponible` = `Stock` − lo que
retienen los pedidos en pago (`tienda.ReservadoPorProducto`); es lo que la
tienda pública muestra y ofrece. Un consumo manual del admin se valida contra
el stock **físico**: la realidad física manda sobre una reserva, y si eso deja
sin stock a un pedido que ya se estaba cobrando, cae en `por_conciliar`.

### Seguridad del panel de administración

Todo `/admin/*` requiere haber iniciado sesión, excepto `/admin/login`,
`/admin/logout` y `/admin/setup`. El middleware que lo aplica es
`(a *App) RequireAdminAuth` (`internal/web/handlers_auth.go`), montado sobre
el subrouter `admin` en `internal/web/router.go` con
`admin.Use(app.RequireAdminAuth)`.

**Cómo funciona:**

- Las credenciales (usuario + hash bcrypt de la contraseña + una llave
  aleatoria de firma) viven en `admin.json` (gitignored), **no en la base de
  datos** — igual razón que `config.json`: si la contraseña viviera en
  Postgres, no se podría proteger el Mantenimiento 1 (la pantalla donde se
  configura la propia conexión a esa base) antes de tener la base
  configurada.
- Mientras `admin.json` no exista, cualquier ruta de `/admin/*` redirige a
  `/admin/setup`, un formulario de una sola vez para crear el usuario y
  contraseña iniciales. Una vez existe, `/admin/setup` deja de estar
  disponible (redirige a `/admin/login`).
- Las contraseñas deben tener entre 8 y 72 bytes (`errorLargoPassword`):
  bcrypt solo usa los primeros 72, y aceptar más sería aceptar en silencio una
  contraseña que no se verifica completa.
- Al iniciar sesión correctamente se registra la sesión en la tabla `sesiones`
  (`internal/sesiones`: id aleatorio, dueño, vencimiento, IP y navegador) y se
  firma una cookie (`admin_session`, `HttpOnly`, `SameSite=Lax`,
  `Path=/admin`) con HMAC-SHA256 usando la llave de esa identidad. El payload
  firmado es `tipo|identidad|vencimiento|id_de_sesión`, así que nadie puede
  cambiar el id para reutilizar otra sesión. Para aceptar una petición no
  basta la firma: la sesión debe constar en la tabla y no estar revocada ni
  vencida (12 horas). Si la consulta falla, se trata como inválida. La
  excepción es el `admin` raíz sin base de datos configurada, que debe poder
  entrar para configurarla: ahí solo vale la firma.
- **Sesiones revocables.** `/admin/sesiones` (link "Cuenta") lista las sesiones
  abiertas de la identidad actual, con IP y navegador, y permite cerrar una
  concreta o todas las demás. Cerrar sesión (`/admin/logout`, solo `POST`)
  también la revoca en el servidor, así que una copia robada de la cookie deja
  de servir al instante. Revocar filtra por tipo + identidad: nadie puede cerrar
  sesiones ajenas.
- `internal/config/admin.go` tiene toda la lógica de credenciales
  (`CreateAdmin`, `VerifyPassword`, `UpdatePassword`, con `bcrypt`);
  `handlers_auth.go` tiene la lógica de la cookie de sesión (firma/
  verificación) y los handlers (`AdminSetup`, `AdminLogin`, `AdminLogout`,
  `AdminCambiarPassword`).
- **Cambiar la contraseña** (`/admin/cambiar-password`, link "Cuenta" en el
  menú, ya logueado): pide la contraseña actual, y al guardar rota también
  la llave de firma de sesión — eso invalida la sesión actual (y cualquier
  otra) a propósito, forzando a iniciar sesión de nuevo con la contraseña
  nueva. No hay flujo de "olvidé mi contraseña": para el `admin` raíz hay que borrar
  `admin.json` en el servidor y volver a pasar por `/admin/setup` (que queda
  abierto hasta que se cree de nuevo: hacerlo de inmediato y, en producción, por
  un túnel SSH). Crear un `admin.json` nuevo genera otra llave de firma, así que
  cierra todas las sesiones.
- **Límite de intentos.** `/admin/login` bloquea una IP 15 minutos tras 5
  intentos fallidos en 10 minutos (`internal/web/limitador.go`, ver
  "Limitaciones conocidas" para el alcance de este límite). Se cuenta por IP
  y no por usuario, para que nadie pueda bloquear la cuenta del admin real
  fallando su password muchas veces desde otro lugar.
- **CSRF.** Cada POST de `/admin/*` con sesión (login/logout/setup quedan
  fuera: todavía no hay sesión que firme un token) exige un campo oculto
  `csrf` que coincida con el que `injectNav` puso en la página al
  renderizarla (`(a *App) RequireCSRF`, montado con `admin.Use` en
  `internal/web/router.go` justo después de `RequireAdminAuth`, porque necesita la sesión que ese
  middleware deja en el contexto). El valor es un HMAC-SHA256 de la
  `SecretKey` de esa sesión (`csrfToken`, misma llave que firma la cookie):
  nadie sin una sesión válida de esa identidad lo puede reproducir, y rotar
  la contraseña lo invalida junto con la sesión. `SameSite=Lax` ya bloquea la
  mayoría de los POST entre sitios en navegadores modernos; este token es la
  segunda capa, la que no depende de que el navegador de quien visita la
  página lo implemente bien. `/admin/logout` además solo acepta `POST` (antes
  aceptaba `GET`, así que un simple `<img>` o enlace en otra página podía
  cerrarle la sesión a quien la tuviera abierta).
- **Cookies `Secure` detrás de un proxy.** El atributo `Secure` (que evita
  que el navegador mande la cookie por HTTP) se decide con
  `esConexionSegura` (`internal/web/limitador.go`), no con `r.TLS != nil`
  directo: `r.TLS` solo es no-nil cuando el propio proceso de Go termina el
  TLS, y detrás de un proxy que lo termina antes (Railway, Render, un nginx
  delante) siempre sería `nil` aunque el sitio público sea https, dejando
  las cookies sin `Secure` sin que nadie lo note. `esConexionSegura` además
  confía en `X-Forwarded-Proto` cuando está presente — a diferencia de la
  IP del limitador de intentos, aquí es seguro hacerlo: falsificarla como
  mucho hace que el navegador reciba `Secure` sobre una conexión sin TLS, y
  eso ya lo rechaza guardar por su cuenta; nunca se puede usar para apagar
  `Secure` cuando sí hace falta. La misma función decide la cookie
  `pedido_token` de la tienda.

**Por qué este método y no otro:** para un panel de un solo administrador,
sesión + contraseña con hash es más simple que OAuth/JWT (que resuelven
problemas de multi-usuario/multi-servicio que aquí no existen) y da mejor
experiencia que HTTP Basic Auth (diálogo nativo feo del navegador, sin
logout real). La tienda pública (`/`, `/producto/...`, `/checkout`, etc.)
sigue completamente sin autenticación — nunca debió protegerse.

### Roles y permisos por módulo

Además del usuario `admin` de arranque (`admin.json`), el panel soporta
usuarios adicionales guardados en Postgres (tablas `usuarios`, `modulos`,
`usuario_modulos`, paquete `internal/usuarios`), con dos roles:

- **Superusuario**: acceso a todos los módulos del panel (Base de datos,
  Inventario, Sitio web, Pedidos, Bancos, Correo y Reportes) **excepto** el
  mantenimiento de usuarios.
- **Administrativo**: acceso solo a los módulos que se le asignen uno por
  uno desde `/admin/usuarios`.

Los módulos son `bd`, `inventario`, `sitio`, `pedidos`, `cuentas` (Bancos),
`correo` y `reportes`. **Bancos y Correo son sensibles**: cambiar una cuenta
bancaria o el servidor de correo puede desviar pagos o correos de clientes, así
que conviene asignarlos con cuidado a un usuario administrativo (un
superusuario los tiene siempre). Los cambios de cuentas bancarias y de formas de
pago quedan registrados con quién los hizo.

El mantenimiento de usuarios (`/admin/usuarios`) es exclusivo del usuario
`admin` — ni siquiera un superusuario puede entrar ahí
(`(a *App) RequireOnlyAdmin`, `handlers_auth.go`). Cada módulo del panel
vive en su propio subrouter en `internal/web/router.go` protegido con
`app.RequireModule("clave")`, que revisa el rol/módulos de la sesión activa
(guardada en el contexto de la request por `RequireAdminAuth`) y responde
403 si no corresponde. El nav (`layout.html`) y el Panel (`home.html`) usan
la misma información para no mostrar siquiera los enlaces a lo que esa
sesión no puede abrir.

**Sesión de un usuario de la tabla `usuarios`:** usa el mismo mecanismo de
cookie firmada que `admin` (HMAC-SHA256), pero cada fila de `usuarios`
tiene su propia `secret_key` — así, cuando un usuario cambia su propia
contraseña (`/admin/cambiar-password` funciona igual para `admin` o para
estos usuarios), solo se cierra *su* sesión, no la de los demás. Desactivar
un usuario (`activo = false`) surte efecto de inmediato en su siguiente
request, sin esperar a que expire la cookie, porque el estado se revisa
contra la base de datos en cada petición, no solo al iniciar sesión.

Como estos usuarios viven en Postgres, solo tienen sentido una vez
configurada la base de datos — el orden recomendado sigue siendo:
`/admin/setup` (crear `admin`) → configurar la base de datos → dar de alta
usuarios adicionales desde `/admin/usuarios`.

---

## Estructura de carpetas

```
cmd/server/main.go          punto de entrada: wiring de storage, DB, http.Server con timeouts
cmd/optimizar-fotos/        reduce las fotos ya guardadas (misma regla que al subir); no toca los originales

deploy/                     despliegue y respaldos del servidor: DESPLIEGUE.md (desplegar.ps1,
                            instalar-remoto.sh), RESPALDOS.md (restic diario) y RECUPERACION.md
                            (cómo restaurar desde un respaldo)
.claude/skills/desplegar/   skill de Claude Code (/desplegar) que guía el despliegue

internal/
  config/                   config.json (Postgres) y admin.json (credenciales del admin raíz)
  db/                        Open/Test/EnsureDatabase
  inventario/                categorías, productos, fotos, movimientos (kardex)
  sitio/                     configuración del negocio: datos, colores, costo de envío y formas de pago
  tienda/                    pedidos, pagos y checkout con reserva de stock (checkout.go); pago por
                             transferencia (transferencia.go, comprobantes.go, entrega.go, revision.go)
                             y cancelaciones (cancelacion.go)
  cuentas/                   cuentas bancarias para transferencias, con historial de cambios
  correo/                    configuración SMTP (smtp.json) y envío de correos
  pasarela/                  cliente HTTP de la pasarela de pago
  reportes/                  consultas de los reportes del admin
  usuarios/                  usuarios, roles y módulos asignables
  sesiones/                  registro de sesiones (crear, validar, revocar)
  storage/                   interfaz Storage + implementación Local
  web/                       App struct, router.go (rutas y middlewares), un archivo de handlers por área,
                             cabeceras.go (CSP), limitador.go (intentos por IP)
  testdb/                    base de datos temporal para las pruebas
  e2e/                       pruebas de seguridad de punta a punta

web/
  templates/
    layout.html              layout del admin (paleta terracota fija)
    layout_tienda.html        layout de la tienda (colores inyectados desde Sitio)
    *.html                    una plantilla por página
  static/
    css/style.css            estilos del admin; el menú superior pasa a un botón ☰ (checkbox,
                              sin JavaScript) por debajo de 1500 px y se compacta hasta 2100 px (con menos
                              margen lateral hasta 1900 px)
    css/tienda.css            estilos de la tienda pública (con color-mix())
    js/                       todo el JavaScript (carrito.js, tienda-*.js, admin-*.js);
                              las plantillas no llevan JS inline

media/
  productos/                 fotos de producto (gitignored)
  sitio/                     logo del negocio (gitignored)
comprobantes/                imágenes de las transferencias (gitignored; privadas, el router no
                             las sirve)

config.json                  gitignored — credenciales de Postgres
config.json.example          plantilla de referencia
admin.json                   gitignored — usuario admin raíz y llave de firma de sesiones
smtp.json                    gitignored — servidor de correo saliente (con su contraseña)
```

---

## Producción

La app corre en una máquina virtual gratuita (Always Free) de Oracle Cloud,
región México Central (Querétaro), con Ubuntu 24.04 sobre ARM (A1.Flex). Todo
el sistema vive en una sola máquina: no hay microservicios.

```
visitante ──HTTPS──> Cloudflare ──HTTPS──> Caddy ──> app Go (:8090) ──> Postgres 16
                     (DNS, CDN, WAF)       (:443)    systemd           (solo 127.0.0.1)
```

- **Cloudflare** lleva el DNS y es el único que puede llegar al puerto 443:
  Caddy responde 403 a cualquier conexión que no venga de los rangos de
  Cloudflare, y el modo de cifrado es *Full (strict)* con un certificado de
  origen de Cloudflare. La IP real del visitante llega en `CF-Connecting-IP`;
  la app solo la acepta porque `TRUSTED_PROXIES=127.0.0.1,::1` (ver
  "Limitaciones conocidas").
- **La app** está en `/opt/manualidades` (ejecutable, `web/`, `media/`,
  `comprobantes/`, `config.json`, `admin.json`, `smtp.json`) y corre como un usuario sin privilegios mediante
  el servicio de systemd `manualidades`.
- **Direcciones:** `prueba.manualidadescristal.com` apunta a la tienda;
  `manualidadescristal.com` sigue mostrando una página temporal de "En
  construcción" servida por un Worker de Cloudflare.
- **Fotos y comprobantes:** en el disco de la máquina (`media/` y
  `comprobantes/`), sin servicio externo.
- **Dirección pública de la tienda:** se configura en `/admin/correo` y es la
  que llevan los enlaces de los correos (hoy `https://prueba.manualidadescristal.com`).
  Al pasar al dominio principal hay que cambiarla allí y el parámetro
  `-UrlPublica` de `deploy/desplegar.ps1`.
- **Acceso al servidor:** solo por SSH con llave. Postgres no es accesible desde
  internet; para consultarlo se usa un túnel SSH.

Cómo llevar un cambio al servidor y cómo se respalda y restaura:

| Tema | Dónde |
|---|---|
| Desplegar (`deploy/desplegar.ps1`, skill `/desplegar` de Claude Code) | [deploy/DESPLIEGUE.md](deploy/DESPLIEGUE.md) |
| Respaldos diarios cifrados (restic) | [deploy/RESPALDOS.md](deploy/RESPALDOS.md) |
| Recuperación paso a paso (datos perdidos o servidor perdido) | [deploy/RECUPERACION.md](deploy/RECUPERACION.md) |

**Estado de la tienda:** el sitio de prueba vende por transferencia bancaria; la
tarjeta está desactivada en el servidor (Bancos → Formas de pago) porque aún no
hay pasarela real. Para pasar a `manualidadescristal.com` hay que cargar el
inventario, las cuentas bancarias reales y el costo de envío, poner la dirección
pública en Correo (y `-UrlPublica` en el script de despliegue) y apuntar el
dominio a la tienda (Caddy y Cloudflare).

**Pendiente antes de cobrar con tarjeta:** una pasarela de pago real (los datos
de tarjeta hoy pasan sin tokenizar por el servidor), cerrar los huecos de
idempotencia de los cobros (ver "Limitaciones conocidas") y reemplazar los
mensajes de error internos que llegan al usuario.

---

## Modelo de datos

Todas las tablas se crean automáticamente al conectar (`CREATE TABLE IF NOT
EXISTS`); las migraciones de columnas nuevas sobre bases ya existentes usan
`ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, así que son seguras de correr
repetidamente.

### Inventario (`internal/inventario/schema.go`)

| Tabla | Columnas clave | Notas |
|---|---|---|
| `categorias` | `nombre` (único), `descripcion` | |
| `productos` | `categoria_id`, `nombre`, `descripcion`, `precio_compra`, `precio_venta`, `activo` | `activo=false` lo oculta de la tienda pública |
| `producto_fotos` | `producto_id`, `ruta`, `orden` | 1 producto → N fotos; `ON DELETE CASCADE` |
| `movimientos_inventario` | `producto_id`, `tipo` (`ingreso`/`consumo`), `cantidad`, `motivo`, `es_venta`, `precio_venta`, `fecha`, `creado_en` | Ver abajo |

**`movimientos_inventario` es un kardex, no una tabla editable**: no tiene
`UPDATE` ni `DELETE` expuestos desde la UI — es un libro de movimientos
inmutable, igual que un libro contable real. El stock de un producto
**siempre** se calcula sumando/restando este historial
(`SUM(CASE WHEN tipo='ingreso' THEN cantidad ELSE -cantidad END)`), nunca se
guarda como un campo aparte que se pueda desincronizar.

**Cómo se corrige un error: anulando, no editando.** Si se registró un
movimiento equivocado (por ejemplo 11 en vez de 1), el botón **Anular** de
*Ingresos y consumos* no lo borra ni lo cambia: registra el movimiento
contrario (un ingreso se anula con un consumo, y viceversa) por la misma
cantidad, apuntando al original con `anula_a`. El original queda marcado como
*Anulado* y la nueva fila como *Anulación de #N*; luego se registra el dato
correcto. Así el historial queda completo y el stock se corrige solo. Reglas:
solo movimientos **manuales** (los que nacen de un pedido de la tienda se
corrigen cancelando el pedido); no se puede anular una anulación ni anular dos
veces (lo garantiza un índice único, también con clics simultáneos); y anular
un ingreso que ya se consumió falla si dejara el stock negativo. Los reportes
de **ventas** y de utilidad ignoran las ventas anuladas. Está en
`inventario.AnularMovimiento`.

Dos fechas distintas y con propósitos distintos:
- `fecha` (tipo `DATE`) — la fecha de negocio, elegida por quien registra el
  movimiento (permite cargar con retraso o corregir a qué día pertenece).
- `creado_en` (tipo `TIMESTAMPTZ`) — el momento real en que el registro se
  guardó en el sistema; siempre automático, nunca editable.

`es_venta` + `precio_venta` solo tienen sentido cuando `tipo='consumo'`: un
ingreso nunca es una venta, y el código lo fuerza aunque el formulario
mande algo distinto.

### Sitio (`internal/sitio/schema.go`)

Una sola fila (`config_sitio`, `id` fijo en 1): nombre del negocio,
dirección, teléfono, email, Facebook, Instagram, WhatsApp, ruta del logo,
4 colores (`color_fondo`, `color_texto`, `color_marco`, `color_acento`) y el
`costo_envio` a domicilio (Q 45.00 por omisión, de 0 a 9,999.99).

| Tabla | Columnas clave | Notas |
|---|---|---|
| `formas_pago` | `clave` (`tarjeta`/`transferencia`), `nombre`, `activa`, `orden`, `actualizado_por`, `actualizado_en` | Dos filas fijas, sembradas por la migración y activas al nacer; el admin solo las activa o desactiva |

### Tienda (`internal/tienda/schema.go`)

| Tabla | Columnas clave | Notas |
|---|---|---|
| `pedidos` | `cliente_nombre`, `cliente_telefono`, `cliente_email`, `cliente_nit` (default `'CF'`), `metodo_entrega` (`recoger`/`domicilio`), `direccion_entrega`, `metodo_pago` (`tarjeta`/`transferencia`), `costo_envio`, `estado`, `total`, `expira_en`, `token`, `cancelado_por`, `cancelado_en`, `inventario_devuelto` | `total` **incluye** `costo_envio`, que es una copia del costo vigente al crear el pedido. `estado` es uno de: `pendiente_pago`, `pagando`, `espera_transferencia`, `transferencia_por_revisar`, `expirado` (ciclo de pago, que el admin no cambia a mano) o `pagado`, `procesando`, `entregado`, `cancelado` (pedidos reales: ver `tienda.EstadosValidos`). Desde el admin un pedido pagado pasa a `procesando` o `entregado`; **`cancelado` solo se alcanza con "Cancelar pedido"**, que devuelve el inventario, y es definitivo. `expira_en` es hasta cuándo vale la reserva de stock (NULL si no vence o ya no reserva); `token` es el identificador aleatorio de la cookie `pedido_token` (tarjeta) y del enlace privado `/pedido/{token}` (transferencia), con índice único parcial. `cancelado_por` vale `cliente` si canceló el propio cliente |
| `pedido_items` | `pedido_id`, `producto_id`, `nombre_producto`, `cantidad`, `precio_unitario`, `subtotal` | Nombre y precio son una **copia** del momento de la compra — si el producto cambia de nombre o precio después, el pedido histórico no se altera |
| `comprobantes` | `pedido_id`, `archivo`, `numero_boleta`, `banco_origen`, `fecha_transferencia`, `monto`, `estado` (`por_revisar`/`aceptado`/`rechazado`), `nota`, `revisado_por`, `revisado_en` | Lo que sube el cliente al pagar por transferencia. `monto` es lo que el cliente dice haber transferido (0 en los anteriores a ese campo). Índice único (banco, boleta) entre los no rechazados: la misma boleta no sirve para dos pedidos. `archivo` está en `comprobantes/`, fuera de las carpetas públicas |
| `pagos` | `pedido_id` (**nullable**), `metodo` (`tarjeta`/`transferencia`), `monto`, `estado` (`iniciado`/`aprobado`/`rechazado`/`fallo`/`por_conciliar`), `referencia`, `tarjeta_marca`, `tarjeta_ultimos4`, `motivo_rechazo`, `reembolso`, `reembolso_por`, `reembolso_en` | Registro de **todo intento de pago con tarjeta**, separado a propósito de `pedidos.estado` (ese es el estado *logístico*, no el del pago). Todo intento nace con `pedido_id` (el pedido se crea antes de cobrar), en estado `iniciado`, y se reemplaza por el desenlace real: `aprobado`, `rechazado` (lo dijo el proveedor), `fallo` (el proveedor no respondió) o `por_conciliar` (el cobro pudo aprobarse pero el pedido no se pudo confirmar, o el proceso murió a mitad del cobro; requiere verificar en la pasarela y reembolsar o completar a mano). Un pago por transferencia se registra **ya aprobado** al aprobar el comprobante (referencia = banco + boleta). `reembolso` vale `''`, `pendiente` (el pedido se canceló) o `hecho`; el reembolso en sí se hace fuera del sistema. `pedido_id` sigue siendo nullable por los pagos anteriores a la reserva de stock — ver `internal/pasarela` y `/admin/pedidos/pagos` |

### Cuentas bancarias (`internal/cuentas/cuentas.go`)

| Tabla | Columnas clave | Notas |
|---|---|---|
| `cuentas_bancarias` | `banco`, `tipo_cuenta` (`monetaria`/`ahorro`), `numero`, `titular`, `moneda` (`GTQ`/`USD`), `instrucciones`, `activa`, `orden` | Solo las activas se ofrecen a los clientes; si no hay ninguna activa, la transferencia no se ofrece |
| `cuentas_bancarias_historial` | `cuenta_id` (sin clave foránea a propósito), `accion` (`creada`/`editada`/`activada`/`desactivada`/`eliminada`), `usuario`, `ip`, `detalle` | Cada cambio se anota en la misma transacción que lo hace, con qué campo cambió de qué a qué; sobrevive a la baja de la cuenta |

### Usuarios y permisos (`internal/usuarios/schema.go`)

| Tabla | Columnas clave | Notas |
|---|---|---|
| `modulos` | `clave` (único: `bd`/`inventario`/`sitio`/`pedidos`/`reportes`/`cuentas`/`correo`), `nombre`, `orden` | Catálogo fijo, sembrado por la migración; no se edita desde la UI |
| `usuarios` | `usuario` (único), `password_hash`, `secret_key`, `rol` (`superusuario`/`administrativo`), `activo` | `secret_key` es propia de cada fila — cambiar su contraseña solo cierra su propia sesión |
| `usuario_modulos` | `usuario_id`, `modulo_id` | Solo se usa para `rol='administrativo'`; un `superusuario` no necesita filas aquí |

### Sesiones (`internal/sesiones/sesiones.go`)

| Tabla | Columnas clave | Notas |
|---|---|---|
| `sesiones` | `id` (aleatorio), `kind` (`admin`/`user`), `ident`, `creada_en`, `expira_en`, `ip`, `user_agent`, `revocada` | Una fila por sesión abierta. El `id` va dentro de la cookie firmada; revocar es marcar `revocada`. Índice por (`kind`, `ident`) |

---

## Rutas

### Panel de administración (`/admin/...`)

Todas requieren sesión iniciada, excepto las 3 primeras.

| Método | Ruta | Qué hace |
|---|---|---|
| GET/POST | `/admin/setup` | Crear el usuario admin (solo si todavía no existe uno) |
| GET/POST | `/admin/login` | Iniciar sesión |
| POST | `/admin/logout` | Cerrar sesión (solo `POST`; también la revoca en el servidor) |
| GET/POST | `/admin/cambiar-password` | Cambiar la contraseña (requiere sesión) |
| GET | `/admin/sesiones` | Ver las sesiones abiertas de la cuenta (link "Cuenta") |
| POST | `/admin/sesiones/{id}/revocar` | Cerrar una sesión concreta (también `/admin/sesiones/revocar-otras`) |
| GET | `/admin` | Panel principal (accesos a los módulos que la sesión puede abrir) |
| GET/POST | `/admin/mantenimiento/bd` | Mantenimiento 1: conexión a Postgres |
| GET | `/admin/mantenimiento/inventario` | Landing del Mantenimiento 2 |
| GET/POST | `/admin/mantenimiento/inventario/categorias` | Listar/crear categorías |
| GET/POST | `/admin/mantenimiento/inventario/categorias/{id}/editar` | Editar categoría |
| POST | `/admin/mantenimiento/inventario/categorias/{id}/eliminar` | Eliminar categoría |
| GET | `/admin/mantenimiento/inventario/productos` | Listado con filtro por categoría (dropdown) + texto |
| GET/POST | `/admin/mantenimiento/inventario/productos/nuevo` | Crear producto (+ fotos) |
| GET/POST | `/admin/mantenimiento/inventario/productos/{id}/editar` | Editar producto (+ fotos) |
| POST | `/admin/mantenimiento/inventario/productos/{id}/eliminar` | Eliminar producto |
| POST | `/admin/mantenimiento/inventario/productos/{id}/fotos/{fotoId}/eliminar` | Eliminar una foto |
| GET | `/admin/mantenimiento/inventario/productos/{id}/movimientos` | Fragmento HTML: historial de un producto (usado por fetch) |
| GET/POST | `/admin/mantenimiento/inventario/movimientos` | Kardex general + formulario de registro |
| POST | `/admin/mantenimiento/inventario/movimientos/{id}/anular` | Anular un movimiento manual (registra el contrario; no borra nada) |
| GET | `/admin/mantenimiento/inventario/reporte` | Reporte por rango de fechas, con filtros e impresión |
| GET/POST | `/admin/sitio` | Mantenimiento 3: datos del negocio, logo, colores y costo de envío |
| GET | `/admin/pedidos` | Pedidos pagados de la tienda y, aparte, los pendientes de pago (reservan stock hasta que vencen) |
| GET | `/admin/pedidos/pagos` | Todos los intentos de pago, incluidos los rechazados, fallidos, en curso y por conciliar |
| GET/POST | `/admin/pedidos/{id}` | Detalle de un pedido (con sus comprobantes y el estado del reembolso); el POST cambia su estado (pagado, procesando, entregado) |
| POST | `/admin/pedidos/{id}/cancelar` | Cancelar un pedido (devuelve el inventario si estaba pagado; definitivo) |
| POST | `/admin/pedidos/{id}/reembolsado` | Anotar que ya se devolvió el dinero de un pedido cancelado |
| GET | `/admin/pedidos/{id}/comprobantes/{cid}/archivo` | La imagen de un comprobante (solo con sesión y módulo Pedidos) |
| POST | `/admin/pedidos/{id}/comprobantes/{cid}/aprobar` | Aprobar una transferencia: descuenta el inventario, registra el pago y pasa el pedido a `pagado` |
| POST | `/admin/pedidos/{id}/comprobantes/{cid}/rechazar` | Rechazar con motivo (pedir otro comprobante, o cancelar el pedido) |
| GET/POST | `/admin/cuentas` | **Bancos**: formas de pago, lista de cuentas bancarias, alta e historial de cambios |
| GET/POST | `/admin/cuentas/{id}/editar` | Editar una cuenta bancaria |
| POST | `/admin/cuentas/{id}/activar` \| `/desactivar` \| `/eliminar` | Cambiar el estado de una cuenta o darla de baja |
| POST | `/admin/cuentas/formas-pago/{clave}/activar` \| `/desactivar` | Activar o desactivar Tarjeta o Transferencia |
| GET/POST | `/admin/correo` | **Correo saliente**: servidor SMTP, correos de aviso y dirección pública de la tienda |
| POST | `/admin/correo/probar` | Enviar un correo de prueba (a los correos de aviso) |
| GET | `/admin/usuarios` | Lista de usuarios (superusuario/administrativo) — **exclusivo del usuario `admin`** |
| GET/POST | `/admin/usuarios/nuevo` | Crear usuario, con rol y módulos asignados |
| GET/POST | `/admin/usuarios/{id}/editar` | Editar rol/módulos; contraseña opcional (en blanco = no cambiarla) |
| POST | `/admin/usuarios/{id}/activar` \| `/desactivar` | Activar/desactivar sin pasar por el formulario completo |
| POST | `/admin/usuarios/{id}/eliminar` | Eliminar usuario |

### Tienda pública (`/...`)

| Método | Ruta | Qué hace |
|---|---|---|
| GET | `/` | Catálogo paginado de 20 (`?cat=1&cat=2&pagina=2`: filtro de categorías y página, resueltos en el servidor) |
| GET | `/producto/{id}` | Detalle de producto (galería, cantidad, agregar al carrito) |
| GET | `/carrito` | Carrito (leído de `localStorage` por JS) |
| GET | `/checkout` | Formulario de cliente + entrega + resumen |
| POST | `/checkout/confirmar` | Recibe el carrito completo (JSON): reserva el stock, cobra en la pasarela y, si aprueba, confirma el pedido y descarga el kardex. Usa la cookie `pedido_token` para reintentar sobre el mismo pedido |
| GET | `/pedido/{id}/confirmacion` | Página de gracias; vacía el carrito del navegador. Solo la ve el navegador que hizo el pedido (cookie `pedido_token`); a cualquier otro le responde `404` |
| POST | `/checkout/transferencia` | Recibe el carrito completo (JSON) y crea un pedido que reserva el stock 48 h y espera la transferencia; responde con la dirección de su página privada |
| GET | `/pedido/{token}` | Página privada del pedido por transferencia (cuentas, monto, plazo, formulario del comprobante, cambiar entrega, cancelar). La llave es el token del enlace, no el `id` |
| POST | `/pedido/{token}/comprobante` | Subir el comprobante (imagen + banco, boleta, fecha y monto); el pedido pasa a revisión |
| POST | `/pedido/{token}/entrega` | Cambiar entre recoger y domicilio mientras espera la transferencia (recalcula el monto) |
| POST | `/pedido/{token}/cancelar` | Cancelar el propio pedido mientras espera la transferencia |
| GET/POST | `/consultar-pedido` | Pedir que se reenvíe el enlace de un pedido (número + correo); responde siempre lo mismo |

### Recursos estáticos

`/static/...` (CSS/JS) y `/media/productos/...` + `/media/sitio/...` (fotos y
logo) — compartidas entre admin y tienda, sin prefijo.

---

## Los mantenimientos, explicados

### Mantenimiento 1 — Base de datos

Formulario con host/puerto/usuario/contraseña/nombre de base/`sslmode`. Al
guardar, prueba la conexión real; el botón "Crear base de datos si no
existe" se conecta primero a la base `postgres` (que siempre existe en
cualquier servidor) para poder emitir `CREATE DATABASE` sin depender de la
base destino. Pensado para que, al pasar a producción, sea lo **único** que
cambie.

La contraseña guardada **nunca se envía al navegador**: el campo aparece vacío
con la indicación "(sin cambios)", y dejarlo vacío al guardar conserva la
actual (`passwordParaGuardar`). Así no se puede leer con "ver código fuente"
desde un usuario que solo tenga este módulo.

### Mantenimiento 2 — Inventario

- **Categorías**: alta/edición/baja simple.
- **Productos**: categoría, descripción, precio de compra/venta,
  activo/inactivo, y **N fotografías por producto** (arrastras varios
  archivos a la vez). Al pasar el cursor sobre la miniatura de un producto
  con más de una foto, se despliega la galería completa — con CSS puro
  (`:hover`), sin una sola línea de JavaScript.
- **Filtro de productos**: dropdown de categorías (Seleccionar todas /
  Deseleccionar todas / cada categoría en orden alfabético con su
  checkbox) + buscador de texto libre (busca en nombre y descripción). Todo
  en el navegador, sin recargar la página.
- **Ingresos y consumos (kardex)**: registra movimientos con fecha propia,
  y si el movimiento es un consumo, un checkbox "Es una venta" (deshabilitado
  hasta elegir "Consumo") con el precio de venta real, auto-rellenado desde
  el precio del producto pero editable.
- **Reporte**: por rango de fechas, con checkboxes de Ingresos/Egresos y
  "Solo ventas" (que ignora los otros dos filtros), totales agregados, y un
  botón "Imprimir" que abre el diálogo nativo del sistema operativo
  (`window.print()`) con una vista impresa recortada (sin menú, sin
  formulario de filtros, sin la columna de auditoría).

### Mantenimiento 3 — Sitio web

Nombre del negocio, dirección, teléfono, email, redes sociales (Facebook,
Instagram, WhatsApp), logo, y 4 colores base (fondo, texto, marco/bordes,
acento) con **vista previa en vivo** antes de guardar, y el **costo de envío a
domicilio** (ver "Envío a domicilio"). Estos 4 colores se
inyectan como variables CSS (`--t-fondo`, `--t-texto`, `--t-marco`,
`--t-acento`) en el `<head>` de cada página de la tienda pública; todos los
demás tonos (sombras, hover, fondos de tarjeta) se **derivan** de esos 4 con
`color-mix()` en `tienda.css`, así que cualquier combinación de colores que
el negocio elija se ve coherente, no solo la paleta de ejemplo.

---

### Bancos — cuentas bancarias y formas de pago

`/admin/cuentas` (módulo `cuentas`): arriba, el panel **Formas de pago** para
activar o desactivar Tarjeta y Transferencia; debajo, las cuentas bancarias a las
que los clientes pueden transferir (alta, edición, activar/desactivar, baja) y el
historial de cambios. Ver "Formas de pago y cuentas bancarias".

### Correo (la pantalla del admin)

`/admin/correo` (módulo `correo`): servidor SMTP, correos que reciben los avisos,
dirección pública de la tienda y botón de prueba. Ver "Correo saliente" en
Arquitectura.

### Pedidos

`/admin/pedidos` (módulo `pedidos`): pedidos pagados y pendientes (con el número
de pedido en cada fila), intentos de pago, y en el detalle de cada pedido la
revisión de los comprobantes de transferencia (aprobar o rechazar), el cambio de
estado y **Cancelar pedido**. Ver "Revisión de transferencias en el admin" y
"Cancelaciones y reembolsos".

### Reportes

`/admin/reportes` (módulo `reportes`, `internal/reportes`): consultas de solo
lectura, con gráficos y filtros por rango de fechas — **ventas** por período
(separando tienda y venta manual), ventas por **producto**, **utilidad** (precio de
venta menos el precio de compra actual del producto), **stock** valorizado,
**pedidos** por estado y por forma de entrega, y los **movimientos** del kardex.
Una venta es un consumo marcado `es_venta`, así que las ventas de la tienda y las
manuales salen de la misma fuente y nada se cuenta dos veces. Los reportes de
ventas ignoran las ventas anuladas y los pedidos cancelados.

---

## La tienda pública

### Flujo de compra

1. **Catálogo** (`/`): productos activos, **paginados de 20 en 20**, filtrables
   por categoría (dropdown con checkboxes y botón *Aplicar*; el filtro y la
   página van en la dirección y los resuelve el servidor). Cada tarjeta tiene un botón
   "Agregar al carrito" directo (cantidad 1) además del link al detalle.
2. **Detalle** (`/producto/{id}`): galería completa (clic en miniatura para
   ampliar), selector de cantidad (limitado al stock disponible), "Agregar
   al carrito". Si se pide más de lo que hay, se agrega lo posible y se explica.
3. **Carrito** (`/carrito`): vive enteramente en `localStorage` del
   navegador — no hay sesión de servidor. Permite cambiar cantidades (sin pasar del
   stock disponible) y quitar productos.
4. **Checkout** (`/checkout`): nombre, teléfono, email, NIT (opcional,
   default `"CF"` = Consumidor Final), método de entrega — **Recoger en
   tienda** o **Entrega a domicilio** (la dirección solo se habilita si
   eligen domicilio) — y notas. El resumen suma el **envío a domicilio** cuando corresponde. El
   cliente elige la **forma de pago** entre las que estén disponibles (ver
   "Pago por transferencia bancaria"): con una sola no hay selector, y con
   ninguna el checkout avisa que por ahora no se reciben pagos en línea.
5. **Con tarjeta**, al presionar **Pagar**, el navegador manda el carrito
   completo (con los datos de tarjeta) por `fetch()` a
   `POST /checkout/confirmar`. El servidor:
   - Limita los intentos por IP (8 en 10 minutos) antes de procesar nada, para
     frenar la prueba automatizada de tarjetas (`internal/web/limitador.go`).
   - Recalcula los precios desde la base de datos (nunca confía en lo que
     mande el navegador) y suma el costo de envío si es a domicilio.
   - **Reserva el stock** creando el pedido en estado `pagando`. Si ya no
     hay, responde sin haber cobrado nada.
   - Le pide el cobro a `pasarela-simulada` (ver "Pasarela de pago
     simulada" más arriba) — esa llamada nunca ocurre dentro de una
     transacción SQL.
   - Si el proveedor rechaza el pago o falla, el pedido queda
     `pendiente_pago` con la reserva vigente (20 min) y el cliente puede
     reintentar con otra tarjeta: una cookie (`pedido_token`) le hace
     reencontrar el mismo pedido en vez de crear otro.
   - Si aprueba, en **una sola transacción** escribe **un movimiento de
     consumo por producto en el kardex** (marcado como venta, con el precio
     real) — la misma tabla que usa el Mantenimiento 2 —, marca el pago con
     la referencia real del proveedor y pasa el pedido a `pagado`. Si algo
     falla, no se guarda nada y el cobro queda `por_conciliar`.
6. **Con transferencia**, el botón dice *Continuar con la transferencia* y se
   piden nombre, teléfono y un correo válido. El servidor crea un pedido que
   reserva el stock 48 horas (`POST /checkout/transferencia`) y lleva al cliente
   a su página privada (`/pedido/{token}`): ahí ve las cuentas y el monto exacto,
   transfiere por su cuenta y **sube la foto del comprobante** con el banco, la
   boleta, la fecha y el monto. También recibe el enlace por correo, y puede
   recuperarlo en "Consultar mi pedido". El negocio lo revisa y lo aprueba desde
   el admin: recién entonces el pedido pasa a `pagado` y sale del inventario.
7. **Confirmación** (`/pedido/{id}/confirmacion`, solo tarjeta): resumen del
   pedido, y el JS de esta página vacía el carrito del navegador.

Como el checkout reusa exactamente la misma lógica que el kardex manual
(`inventario.CreateMovimientoTx`), **el Reporte de inventario ya muestra las
ventas online mezcladas con las manuales**, sin haber tocado ese código.

---

## Pruebas

```
go test ./...
```

- `internal/tienda/checkout_test.go` son pruebas de **integración** contra
  Postgres: crean una base temporal (`manualidades_test_<n>`) en el servidor
  configurado en `config.json`, le aplican las migraciones y la eliminan al
  terminar — **nunca tocan la base real**. Cubren el flujo aprobado, el
  rechazo con reintento sobre el mismo pedido, el doble clic, el stock
  agotado al confirmar (`por_conciliar`), la expiración, la ausencia de
  sobreventa con clientes concurrentes y la ausencia de deadlock con
  productos en orden inverso. Si no hay un Postgres respondiendo, se saltan
  (no fallan).
- `internal/web/plantillas_test.go` ejecuta las plantillas que dependen de
  los estados de pedido/pago y del stock disponible con datos de ejemplo
  (no necesita base de datos).
- `internal/e2e/` son las pruebas de **seguridad de punta a punta**: levantan
  el router real (`web.NewRouter`) con una base y un `admin.json` temporales
  y lo ejercitan por HTTP y con un Chrome/Edge real (headless, vía
  `chromedp`). Cubren: CSP sin scripts inline, XSS en el carrito, tope de 72
  caracteres en contraseñas, sesiones revocables (cerrar una, cerrar las
  demás, cookie robada tras cerrar sesión, cookies manipuladas o del formato
  anterior, aislamiento entre usuarios, CSRF), **inyección SQL** (`sqli_test.go`:
  12 cargas clásicas —tautologías, UNION, consultas apiladas, inyección ciega
  por tiempo— contra parámetros de URL, ids de ruta, formularios, login, el
  JSON del checkout y la cookie de confirmación; comprueba huella de cada
  tabla, datos guardados literales y que no se filtren errores de SQL) y que
  los scripts movidos a
  `web/static/js/` sigan funcionando en tienda y admin. Se ejecutan con
  `go test ./internal/e2e/ -v` (~2 minutos). Sin Postgres se saltan todas; sin
  Chrome/Edge solo las de navegador (se puede indicar uno con la variable
  `E2E_CHROME`).
- `internal/sesiones/sesiones_test.go` prueba el registro de sesiones contra
  una base temporal.
- **Pago por transferencia** (en `internal/tienda/*_test.go`, contra una base
  temporal): reserva de 48 h y vencimiento, comprobantes (boleta repetida, monto
  y fecha, dos subidas simultáneas), aprobar y rechazar (el kardex y el pago
  quedan coherentes; dos revisores a la vez, solo gana uno), cancelaciones (el
  inventario se devuelve una sola vez, un pedido entregado pregunta si volvió),
  cambio de entrega, costo de envío y subir un comprobante mientras se cancela.
- `internal/cuentas`, `internal/sitio` y `internal/correo`: validación y
  historial de las cuentas, formas de pago y costo de envío, y el envío de
  correos contra un **servidor SMTP falso en memoria con STARTTLS y SSL reales**
  (autenticación, contraseña incorrecta, certificado no confiable, destinatario
  rechazado, imposibilidad de colar cabeceras).
- `internal/e2e/` también recorre la tienda por transferencia de punta a punta
  (cliente y admin, con los correos capturados), las formas de pago, las
  cancelaciones, el costo de envío, el carrito con tope de stock y la
  paginación, y que la imagen de un comprobante no se sirva a nadie sin sesión
  con permiso. Varias pruebas se verificaron además "rompiendo a propósito" la
  regla que vigilan, para comprobar que fallan sin ella. En total son más de
  200 pruebas.

---

## Decisiones de diseño que vale la pena conocer

- **Sin framework de JS a propósito.** Cada interacción "en vivo" (filtros,
  carrito, checkbox condicionales) es JS vanilla escrito a mano. La
  alternativa más idiomática si algún día se quiere reducir ese JS sin
  perder la experiencia es [htmx](https://htmx.org/): el patrón de
  fragmentos HTML devueltos por el servidor (`renderFragment`, usado hoy
  para el historial de movimientos) ya está pensado en ese estilo.
- **Carrito en `localStorage`, no en sesión de servidor.** Decisión
  explícita: para la Fase 1 no hacía falta la complejidad de sesiones; el
  carrito se arma en el navegador y se envía completo una sola vez, al
  pagar.
- **Reservar stock antes de cobrar, sin tocar el kardex.** Reservar
  descontando y devolviendo con movimientos del kardex ensuciaría el historial
  y los reportes con consumos e ingresos falsos. En cambio la reserva vive en
  el pedido (`pendiente_pago`/`pagando` + `expira_en`), el stock disponible se
  calcula restándola, y el kardex solo se escribe cuando el pago se aprueba.
  Liberar una reserva es cambiar el estado de un pedido, sin dejar rastro
  contable.
- **Kardex inmutable.** `movimientos_inventario` no tiene edición ni borrado
  desde la UI a propósito — es un libro de movimientos, no una tabla de
  estado. El stock siempre se deriva sumando/restando, nunca se guarda como
  campo separado.
- **Precio y nombre del producto se copian al pedido** (`pedido_items`) en
  el momento de la compra. Si el producto cambia de precio o nombre después,
  los pedidos históricos no se alteran.
- **Rutas del admin bajo `/admin`, tienda en la raíz.** Fue una decisión
  explícita para que la tienda pública tenga la URL "natural" del dominio;
  en producción, el admin podría además vivir en un subdominio aparte, pero
  esa decisión se dejó para cuando exista un dominio real.
- **Los comprobantes no son prueba del pago.** Una captura se puede falsificar:
  la prueba real es ver el depósito en el banco. Por eso el sistema pide boleta,
  banco, fecha y monto, resalta cuando el monto no coincide, y deja la decisión
  (aprobar o rechazar) en una persona. No hay conciliación bancaria automática.
- **Una acción por cada cosa que toca dinero o inventario.** Cancelar un pedido
  no es cambiar una etiqueta: devuelve el inventario (anulando, no borrando) y
  deja el reembolso pendiente. Por eso existe `CancelarPedido` y
  `ActualizarEstado` se niega a cancelar. Cancelado es definitivo; si el cliente
  aún quiere el producto, se hace un pedido nuevo.
- **Los enlaces privados usan un token, no el `id`.** El `id` es secuencial y
  adivinable; el token (UUID) es la llave de acceso del cliente a su pedido, y
  las páginas que lo llevan en la dirección no se guardan en caché ni lo mandan
  como `Referer`.
- **El servidor decide los montos.** El costo de envío, los precios y el total
  los calcula siempre el servidor; el navegador solo los muestra.
- **Lo que se desactiva no deja a nadie en el aire.** Desactivar una forma de
  pago frena los pedidos nuevos, pero los pedidos ya creados con ella siguen
  hasta terminar.
- **La configuración con secretos vive en archivos, no en la base.** `config.json`
  (Postgres), `admin.json` y `smtp.json` están fuera de git y de los volcados de
  la base; entran en los respaldos cifrados.
- **Storage abstraído desde el día uno.** Nunca se llama directamente a
  `os.WriteFile` fuera de `internal/storage/local.go` — todo pasa por la
  interfaz `Storage`, precisamente para poder añadir un backend S3-compatible
  sin tocar el resto de la app cuando haga falta.

---

## Limitaciones conocidas

- **Un solo usuario raíz (`admin`).** Ya hay soporte para superusuarios y
  usuarios administrativos con permisos por módulo (`/admin/usuarios`), pero
  el mantenimiento de usuarios en sí solo lo puede abrir el `admin` original
  de `admin.json` — no hay forma de delegar esa delegación.
- **Sin "olvidé mi contraseña".** Para `admin`, si se pierde hay que borrar
  `admin.json` a mano en el servidor y volver a pasar por `/admin/setup`
  (ver "Seguridad del panel de administración": mientras tanto `/admin/setup`
  queda abierto, y al crear el nuevo acceso se cierran todas las sesiones).
  Para un usuario de la tabla `usuarios`, el `admin` puede resetearle la
  contraseña desde `/admin/usuarios/{id}/editar`, pero no hay flujo de
  autoservicio ("te mandamos un correo").
- **Límite de intentos en memoria, no persistente.** `internal/web/limitador.go`
  frena la fuerza bruta en `/admin/login` (5 fallos por IP en 10 minutos →
  bloqueo de 15 minutos) y la prueba automatizada de tarjetas en
  `/checkout/confirmar` (8 intentos por IP en 10 minutos → bloqueo de 10
  minutos). El contador vive en memoria del proceso: un reinicio del servidor
  lo limpia. Razonable para un panel/tienda de bajo tráfico; una plataforma
  con varias instancias del servidor necesitaría un límite compartido (Redis
  u otro almacén central), porque cada instancia llevaría su propio contador.
- **Otros límites de intentos, también en memoria:** crear pedidos por
  transferencia (6 por hora por IP, y 3 pedidos abiertos por correo), subir
  comprobantes (10 por hora), "Consultar mi pedido" (5 en 15 minutos, y un
  reenvío cada 5 minutos por pedido) y los cambios de entrega o cancelaciones del
  cliente (20 por hora). Frenan el acaparamiento de inventario y la automatización,
  pero alguien con muchos correos y muchas IP podría reservar stock durante 48 h:
  el negocio puede cancelar esos pedidos desde el admin.
- **Detrás de un proxy hay que configurar `TRUSTED_PROXIES`.** `clientIP`
  parte de `RemoteAddr` y solo lee `CF-Connecting-IP` o `X-Forwarded-For` si
  la conexión viene de una IP o CIDR listada en la variable de entorno
  `TRUSTED_PROXIES` (por ejemplo `127.0.0.1,::1` con Caddy en la misma
  máquina). Sin ella se ignoran esas cabeceras, que es lo correcto en
  desarrollo, pero detrás de Cloudflare o Caddy todos los visitantes
  parecerían una sola IP y 5 logins fallidos bloquearían a todos. Esto solo es
  seguro si el puerto de la app no es accesible desde fuera (escuchar en
  localhost o cerrarlo en el firewall) y, si Caddy confía en
  `CF-Connecting-IP`, si solo Cloudflare puede llegar a Caddy.
- **Datos de tarjeta en claro por el servidor.** Van del navegador a
  `/checkout/confirmar` y de ahí a la pasarela. No se guardan ni se loguean
  (solo marca y últimos 4 dígitos), pero pasan por la memoria del servidor. La
  solución real es tokenizar con el proveedor, y depende de la pasarela real
  (Fase 3).
- **Las fotos se reprocesan al subirlas.** `Save` (`internal/storage/imagen.go`)
  decide el tipo por el contenido, no por el nombre: solo acepta JPEG, PNG y
  WebP de hasta 10 MB (sin SVG ni GIF), rechaza imágenes que declaran más de
  50 millones de píxeles, reduce el lado más largo a 1200 px sin ampliar,
  respeta la orientación EXIF y descarta los metadatos (incluida la ubicación
  GPS). Un PNG sigue siendo PNG; JPEG y WebP se guardan como JPEG (la
  transparencia de un WebP se aplana sobre blanco). La extensión del archivo
  guardado sale del contenido. Las fotos subidas antes de este cambio no se
  reprocesan.
- **Errores internos mostrados al usuario.** Varios handlers muestran
  `err.Error()`, que puede incluir texto de PostgreSQL.
- **`style-src` conserva `'unsafe-inline'`** (ver "Cabeceras de seguridad").
- **Datos anteriores a una migración no se pueden completar retroactivamente
  con precisión.** Ejemplos concretos ya ocurridos en este proyecto:
  - Movimientos registrados antes de separar `fecha` de `creado_en` quedaron
    con `creado_en` igual al momento de la migración, no a su hora real de
    registro (esa información no se guardaba antes).
  - Ventas marcadas como tal antes de agregar `precio_venta` al kardex
    quedaron con precio `0` — cuentan como venta en los reportes, pero no
    suman al "Total vendido ($)".
- **El pago con tarjeta es simulado** (y está desactivado en el servidor). `pasarela-simulada` es un proyecto propio que
  hace de proveedor falso (con 3 modos: aprobar/rechazar/fallar); no hay
  integración real con ningún procesador de pagos todavía (Fase 3). El
  punto de reemplazo ya está aislado en `internal/pasarela`.
- **Sin preparación de pedidos en bodega todavía** (Fase 2): un pedido pagado
  se queda en `estado = 'pagado'` hasta que alguien lo cambie a mano; no hay
  flujo de picking/empaque/envío.
- **Sin validación de NIT contra la SAT.** El campo existe y se guarda, pero
  no se verifica contra ningún servicio externo.
- **Fotos y logo en disco local.** Decisión consciente para una sola máquina:
  en producción viven en `/opt/manualidades/media` y por eso `media/` entra en
  los respaldos. En una plataforma de contenedores efímeros (Railway, Render,
  Fly.io sin volumen) se perderían en cada redeploy; si algún día se cambia a
  una así, hace falta el backend S3-compatible mencionado arriba.
- **Los cobros con tarjeta no tienen idempotencia completa.** Hay tres huecos que
  se cerrarán al integrar la pasarela real: (1) dos envíos simultáneos del primer
  intento (sin cookie todavía) pueden crear dos pedidos y dos cobros; (2) un
  timeout de la pasarela se trata como "no respondió" y vuelve a permitir pagar,
  aunque el proveedor pudiera haber cobrado; (3) no se manda ninguna llave de
  operación única al proveedor ni hay un índice único sobre la referencia. La
  solución depende de que el banco acepte una llave de idempotencia o permita
  consultar una orden.
- **Los reembolsos se hacen fuera del sistema.** Cancelar un pedido pagado solo
  deja el pago marcado como reembolso pendiente; el sistema no devuelve el dinero
  ni se comunica con el banco o la pasarela.
- **El límite de stock del carrito usa el stock del momento en que se agregó.**
  Si otra persona reserva unidades después, el carrito no se entera hasta que se
  vuelve al catálogo o se llega a pagar; allí el servidor lo vuelve a comprobar y
  es la barrera real.
- **Pedidos cancelados antes de la devolución automática** (cuando cancelar solo
  cambiaba la etiqueta) no recuperaron su inventario; su detalle dice "Su
  inventario no se devolvió" y hay que corregirlo con un ingreso manual.
- **Los cambios de cuentas bancarias no avisan por correo**, solo quedan en el
  historial. Los avisos por correo dependen del proveedor SMTP (con Gmail, unos
  500 al día).
- **`smtp.json` guarda la contraseña en texto plano**, igual que `config.json`:
  con permisos 600, fuera de git y en los respaldos cifrados, pero legible por
  quien tenga acceso al servidor.
- **Los reportes de ventas no cuentan el envío como ingreso aparte:** el total
  de un pedido lo incluye, pero las ventas por producto no.
- **Miniaturas.** La tienda y la administración muestran la foto completa
  (reducida a 1200 px al subirla) incluso en miniaturas de 44 px. Es aceptable
  hoy, pero generar miniaturas de verdad reduciría mucho el peso de las
  páginas con muchos productos.

---

## Roadmap

- **Tienda — Fase 2 (bodega)**: pantalla de pedidos pendientes, checklist de
  productos a preparar por pedido (ya hay todo lo necesario en
  `pedido_items` — producto, cantidad, y acceso a sus fotos), botones para
  avanzar `estado` (`pagado` → `preparando` → `listo` → `entregado`).
- **Pasar a `manualidadescristal.com`**: cuando el inventario esté cargado, apuntar
  el dominio principal a la tienda (en lugar de la página "En construcción"),
  poner la dirección pública en Correo y cambiar `-UrlPublica` del despliegue.
  Antes conviene proteger `/admin` con Cloudflare Access, restringir el acceso SSH
  y avisar cuando falle un respaldo.
- **Tienda — Fase 3 (pasarela real)**: reemplazar `pasarela.BaseURL()`
  (`internal/pasarela/cliente.go`) por la URL de un proveedor real — el
  contrato (`Cobrar`, `RespuestaCobro`, `ErrNoDisponible`) ya está pensado
  para eso, y `TiendaCheckoutConfirmar` no debería necesitar cambios.
  Candidato a evaluar cuando llegue el momento: proveedores con soporte en
  Guatemala/Centroamérica.
- **Idempotencia de los cobros con tarjeta**: parte de la Fase 3. Una llave de
  operación única por intento hacia el proveedor, un identificador generado por
  el navegador para el primer envío, tratar un timeout como "incierto" y
  consultarlo antes de permitir otro intento, y un índice único sobre la
  referencia del proveedor.
- **Avisar por correo cuando cambia una cuenta bancaria**, además del historial.
- **Validación de NIT con SAT**: una llamada de validación en el checkout
  antes de confirmar el pedido; el campo `cliente_nit` ya existe.
- **Backend de almacenamiento S3-compatible**: solo si algún día se abandona la
  máquina única (hoy las fotos viven en su disco, con respaldo).
- **Aviso cuando falla un respaldo**: hoy un fallo (local o en Backblaze) solo se
  ve en `systemctl status manualidades-respaldo` y en el registro; un servicio
  externo de latido (por ejemplo healthchecks.io) avisaría por correo.
