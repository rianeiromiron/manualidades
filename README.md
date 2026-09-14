# Manualidades

Sistema de gestión y tienda en línea para un negocio de manualidades (bolsas,
monederos y artículos típicos tejidos a mano). Un solo binario en Go sirve
tanto el **panel de administración** como la **tienda pública**, contra una
base de datos Postgres.

No usa ningún framework de frontend: las páginas se renderizan del lado del
servidor con `html/template`, y el JavaScript que existe es vanilla,
escrito a mano, y deliberadamente mínimo (filtros, un carrito en
`localStorage`, y un par de formularios con campos condicionales).

---

## Estado del proyecto

| Fase | Qué es | Estado |
|---|---|---|
| Mantenimiento 1 | Conexión a la base de datos (Postgres) | ✅ Hecho |
| Mantenimiento 2 | Inventario: categorías, productos, fotos, kardex, reporte | ✅ Hecho |
| Mantenimiento 3 | Configuración del sitio: datos del negocio, logo, colores | ✅ Hecho |
| Tienda — Fase 1 | Catálogo, carrito, checkout con pago **simulado** | ✅ Hecho |
| Estado del pedido | Pagado → Procesando en bodega / Entregado / Cancelado, manejado desde admin | ✅ Hecho |
| Seguridad del admin | Login con usuario/contraseña, todo `/admin/*` protegido | ✅ Hecho |
| Usuarios y permisos | Superusuarios y usuarios administrativos con módulos asignables | ✅ Hecho |
| Pasarela de pago | Registro de pagos (tabla `pagos`) + pasarela simulada (`pasarela-simulada`) con 3 modos: aprobar/rechazar/fallar | ✅ Hecho (simulada) |
| Tienda — Fase 2 | Preparación de pedidos en bodega (flujo de picking/empaque) | ⬜ Pendiente (estados ya modelados) |
| Tienda — Fase 3 | Pasarela de pago real | ⬜ Pendiente (el punto de reemplazo ya está aislado en `internal/pasarela`) |
| Validación de NIT contra SAT | — | ⬜ Pendiente |

---

## Stack tecnológico

- **Go 1.24** — `net/http` + [gorilla/mux](https://github.com/gorilla/mux) para rutas
- **Postgres** (probado contra un contenedor Docker local) vía [lib/pq](https://github.com/lib/pq)
- **html/template** de la librería estándar — sin React/Vue/htmx
- **JavaScript vanilla** — sin build step, sin npm, sin bundlers
- **CSS puro** — variables CSS (`:root`) y `color-mix()` para temas derivados; sin Tailwind ni preprocesadores

Dependencias (`go.mod`):

```
github.com/gorilla/mux v1.8.1
github.com/lib/pq v1.10.9
github.com/google/uuid v1.6.0   (nombres de archivo únicos al subir fotos)
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
   - Corre automáticamente las migraciones de los 3 esquemas (inventario,
     sitio, tienda) — son idempotentes, se puede reconectar cuantas veces
     haga falta sin duplicar nada.

5. **Ya está**: `http://localhost:8090/admin` es el panel de administración,
   `http://localhost:8090/` es la tienda pública (sin autenticación, como debe
   ser).

6. **Pasarela de pago simulada** (necesaria para completar un checkout):
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
	conn        *sql.DB           // protegido por mutex; puede cambiar en caliente
	Storage     storage.Storage   // fotos de producto
	LogoStorage storage.Storage   // logo del sitio
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
`internal/web/handlers_tienda.go`):

1. `tienda.CotizarCarrito` calcula precios reales desde el catálogo y
   valida stock — evita cobrar por algo que ya sabemos que no hay.
2. `pasarela.Cobrar(...)` — esta llamada de red **nunca** ocurre dentro de
   una transacción SQL (una transacción no debe quedar abierta esperando a
   un tercero).
3. Según el resultado:
   - Falla técnica o rechazo → no se crea ningún pedido, pero sí se guarda
     el intento en `pagos` (con `pedido_id NULL`) para que quede rastro.
   - Aprobado → `tienda.CrearPedido(...)` hace, en una sola transacción
     atómica, lo de siempre (pedido + líneas + kardex) más el registro del
     pago ya aprobado (`pagos` con `pedido_id` seteado y la referencia real
     que dio el proveedor).
4. El admin puede ver **todos** los intentos (incluidos los que no
   generaron pedido) en `/admin/pedidos/pagos`.

Esto es exactamente el punto que la Fase 3 original del proyecto (ver
Roadmap) señalaba como "el único lugar a reemplazar por una pasarela real":
hoy ya está aislado ahí, solo faltaría cambiar `pasarela.BaseURL()` por la
URL de un proveedor de verdad.
esquema de base de datos ni los handlers.

### Config de base de datos (`internal/config`)

`config.json` (gitignored) guarda host/puerto/usuario/contraseña/nombre de
base/`sslmode`. Vive en un archivo plano, no en la base de datos misma —
evita la paradoja de necesitar la conexión para leer los datos de conexión.
`config.DSNMaintenance()` construye un DSN contra la base `postgres` (que
siempre existe) para poder emitir `CREATE DATABASE` la primera vez.

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

`inventario.CreateMovimiento` y `inventario.StockActual` reciben una
interfaz `querier` (no `*sql.DB` directamente):

```go
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}
```

Tanto `*sql.DB` como `*sql.Tx` la satisfacen. Esto permite que
`tienda.CrearPedido` registre el pedido, sus líneas, **y** el movimiento de
consumo correspondiente en el kardex, todo dentro de una sola transacción
SQL — si algo falla a la mitad, no queda nada guardado.

### Seguridad del panel de administración

Todo `/admin/*` requiere haber iniciado sesión, excepto `/admin/login`,
`/admin/logout` y `/admin/setup`. El middleware que lo aplica es
`(a *App) RequireAdminAuth` (`internal/web/handlers_auth.go`), montado sobre
el subrouter `admin` en `main.go` con `admin.Use(app.RequireAdminAuth)`.

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
- Al iniciar sesión correctamente, se firma una cookie
  (`admin_session`, `HttpOnly`, `SameSite=Lax`) con HMAC-SHA256 usando la
  llave guardada en `admin.json` — no hay tabla de sesiones ni estado en
  memoria del servidor: la cookie misma es la prueba de la sesión, y
  cualquier reinicio del proceso la sigue aceptando mientras no haya
  expirado (12 horas) ni cambiado la llave.
- `internal/config/admin.go` tiene toda la lógica de credenciales
  (`CreateAdmin`, `VerifyPassword`, `UpdatePassword`, con `bcrypt`);
  `handlers_auth.go` tiene la lógica de la cookie de sesión (firma/
  verificación) y los handlers (`AdminSetup`, `AdminLogin`, `AdminLogout`,
  `AdminCambiarPassword`).
- **Cambiar la contraseña** (`/admin/cambiar-password`, link "Cuenta" en el
  menú, ya logueado): pide la contraseña actual, y al guardar rota también
  la llave de firma de sesión — eso invalida la sesión actual (y cualquier
  otra) a propósito, forzando a iniciar sesión de nuevo con la contraseña
  nueva. No hay flujo de "olvidé mi contraseña"; si se pierde, hay que
  borrar `admin.json` a mano y volver a pasar por `/admin/setup`.

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
  Inventario, Sitio web, Pedidos) **excepto** el mantenimiento de usuarios.
- **Administrativo**: acceso solo a los módulos que se le asignen uno por
  uno desde `/admin/usuarios`.

El mantenimiento de usuarios (`/admin/usuarios`) es exclusivo del usuario
`admin` — ni siquiera un superusuario puede entrar ahí
(`(a *App) RequireOnlyAdmin`, `handlers_auth.go`). Cada módulo del panel
vive en su propio subrouter en `main.go` protegido con
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
cmd/server/main.go          punto de entrada: wiring de storage, DB, rutas

internal/
  config/                   config.json (host/puerto/usuario/contraseña de Postgres)
  db/                        Open/Test/EnsureDatabase
  inventario/                categorías, productos, fotos, movimientos (kardex)
  sitio/                     configuración del negocio (Mantenimiento 3)
  tienda/                    pedidos + integración con el kardex
  storage/                   interfaz Storage + implementación Local
  web/                       App struct + un archivo de handlers por área

web/
  templates/
    layout.html              layout del admin (paleta terracota fija)
    layout_tienda.html        layout de la tienda (colores inyectados desde Sitio)
    *.html                    una plantilla por página
  static/
    css/style.css            estilos del admin
    css/tienda.css            estilos de la tienda pública (con color-mix())
    js/carrito.js             carrito en localStorage (compartido por 4 páginas)

media/
  productos/                 fotos de producto (gitignored)
  sitio/                     logo del negocio (gitignored)

config.json                  gitignored — credenciales de Postgres
config.json.example          plantilla de referencia
```

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
dirección, teléfono, email, Facebook, Instagram, WhatsApp, ruta del logo, y
4 colores (`color_fondo`, `color_texto`, `color_marco`, `color_acento`).

### Tienda (`internal/tienda/schema.go`)

| Tabla | Columnas clave | Notas |
|---|---|---|
| `pedidos` | `cliente_nombre`, `cliente_telefono`, `cliente_email`, `cliente_nit` (default `'CF'`), `metodo_entrega` (`recoger`/`domicilio`), `direccion_entrega`, `estado`, `total` | `estado` nace siempre en `'pagado'`; desde `/admin/pedidos/{id}` se cambia a `'procesando'` (en bodega), `'entregado'` o `'cancelado'` — ver `tienda.EstadosValidos` |
| `pedido_items` | `pedido_id`, `producto_id`, `nombre_producto`, `cantidad`, `precio_unitario`, `subtotal` | Nombre y precio son una **copia** del momento de la compra — si el producto cambia de nombre o precio después, el pedido histórico no se altera |
| `pagos` | `pedido_id` (**nullable**), `metodo`, `monto`, `estado` (`aprobado`/`rechazado`/`fallo`), `referencia`, `tarjeta_marca`, `tarjeta_ultimos4`, `motivo_rechazo` | Registro de **todo intento de pago**, separado a propósito de `pedidos.estado` (ese es el estado *logístico*, no el del pago). `pedido_id` es nulo cuando el intento nunca llegó a generar un pedido (`rechazado` por el proveedor o `fallo` técnico de la pasarela) — ver `internal/pasarela` y `/admin/pedidos/pagos` |

### Usuarios y permisos (`internal/usuarios/schema.go`)

| Tabla | Columnas clave | Notas |
|---|---|---|
| `modulos` | `clave` (único: `bd`/`inventario`/`sitio`/`pedidos`), `nombre`, `orden` | Catálogo fijo, sembrado por la migración; no se edita desde la UI |
| `usuarios` | `usuario` (único), `password_hash`, `secret_key`, `rol` (`superusuario`/`administrativo`), `activo` | `secret_key` es propia de cada fila — cambiar su contraseña solo cierra su propia sesión |
| `usuario_modulos` | `usuario_id`, `modulo_id` | Solo se usa para `rol='administrativo'`; un `superusuario` no necesita filas aquí |

---

## Rutas

### Panel de administración (`/admin/...`)

Todas requieren sesión iniciada, excepto las 3 primeras.

| Método | Ruta | Qué hace |
|---|---|---|
| GET/POST | `/admin/setup` | Crear el usuario admin (solo si todavía no existe uno) |
| GET/POST | `/admin/login` | Iniciar sesión |
| GET/POST | `/admin/logout` | Cerrar sesión |
| GET/POST | `/admin/cambiar-password` | Cambiar la contraseña (requiere sesión) |
| GET | `/admin` | Panel principal (accesos a los 4 mantenimientos) |
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
| GET | `/admin/mantenimiento/inventario/reporte` | Reporte por rango de fechas, con filtros e impresión |
| GET/POST | `/admin/sitio` | Mantenimiento 3: datos del negocio, logo, colores |
| GET | `/admin/pedidos` | Lista de pedidos de la tienda |
| GET | `/admin/pedidos/pagos` | Todos los intentos de pago, incluidos los rechazados/fallidos sin pedido |
| GET/POST | `/admin/pedidos/{id}` | Detalle de un pedido; el POST cambia su estado |
| GET | `/admin/usuarios` | Lista de usuarios (superusuario/administrativo) — **exclusivo del usuario `admin`** |
| GET/POST | `/admin/usuarios/nuevo` | Crear usuario, con rol y módulos asignados |
| GET/POST | `/admin/usuarios/{id}/editar` | Editar rol/módulos; contraseña opcional (en blanco = no cambiarla) |
| POST | `/admin/usuarios/{id}/activar` \| `/desactivar` | Activar/desactivar sin pasar por el formulario completo |
| POST | `/admin/usuarios/{id}/eliminar` | Eliminar usuario |

### Tienda pública (`/...`)

| Método | Ruta | Qué hace |
|---|---|---|
| GET | `/` | Catálogo (filtro de categorías, tarjetas de producto) |
| GET | `/producto/{id}` | Detalle de producto (galería, cantidad, agregar al carrito) |
| GET | `/carrito` | Carrito (leído de `localStorage` por JS) |
| GET | `/checkout` | Formulario de cliente + entrega + resumen |
| POST | `/checkout/confirmar` | Recibe el carrito completo (JSON), valida stock, crea el pedido |
| GET | `/pedido/{id}/confirmacion` | Página de gracias; vacía el carrito del navegador |

### Recursos estáticos

`/static/...` (CSS/JS) y `/media/productos/...` + `/media/sitio/...` (fotos y
logo) — compartidas entre admin y tienda, sin prefijo.

---

## Los 3 mantenimientos, explicados

### Mantenimiento 1 — Base de datos

Formulario con host/puerto/usuario/contraseña/nombre de base/`sslmode`. Al
guardar, prueba la conexión real; el botón "Crear base de datos si no
existe" se conecta primero a la base `postgres` (que siempre existe en
cualquier servidor) para poder emitir `CREATE DATABASE` sin depender de la
base destino. Pensado para que, al pasar a producción, sea lo **único** que
cambie.

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
acento) con **vista previa en vivo** antes de guardar. Estos 4 colores se
inyectan como variables CSS (`--t-fondo`, `--t-texto`, `--t-marco`,
`--t-acento`) en el `<head>` de cada página de la tienda pública; todos los
demás tonos (sombras, hover, fondos de tarjeta) se **derivan** de esos 4 con
`color-mix()` en `tienda.css`, así que cualquier combinación de colores que
el negocio elija se ve coherente, no solo la paleta de ejemplo.

---

## La tienda pública — Fase 1

### Flujo de compra

1. **Catálogo** (`/`): productos activos, filtrables por categoría (dropdown
   con checkboxes, igual mecánica que el admin). Cada tarjeta tiene un botón
   "Agregar al carrito" directo (cantidad 1) además del link al detalle.
2. **Detalle** (`/producto/{id}`): galería completa (clic en miniatura para
   ampliar), selector de cantidad (limitado al stock disponible), "Agregar
   al carrito".
3. **Carrito** (`/carrito`): vive enteramente en `localStorage` del
   navegador — no hay sesión de servidor. Permite cambiar cantidades y
   quitar productos.
4. **Checkout** (`/checkout`): nombre, teléfono, email, NIT (opcional,
   default `"CF"` = Consumidor Final), método de entrega — **Recoger en
   tienda** o **Entrega a domicilio** (la dirección solo se habilita si
   eligen domicilio) — y notas.
5. Al presionar **Pagar**, el navegador manda el carrito completo (con los
   datos de tarjeta) por `fetch()` a `POST /checkout/confirmar`. El servidor:
   - Recalcula los precios desde la base de datos (nunca confía en lo que
     mande el navegador) y valida que haya stock suficiente.
   - Le pide el cobro a `pasarela-simulada` (ver "Pasarela de pago
     simulada" más arriba) — esa llamada nunca ocurre dentro de una
     transacción SQL.
   - Si el proveedor rechaza el pago o falla, no se crea ningún pedido,
     pero el intento queda registrado en `pagos`.
   - Si aprueba, en **una sola transacción** inserta el pedido, sus
     líneas, **y un movimiento de consumo por producto en el kardex**
     (marcado como venta, con el precio real) — la misma tabla que usa el
     Mantenimiento 2 — más el registro del pago con la referencia real del
     proveedor. Si algo falla, no se guarda nada.
6. **Confirmación** (`/pedido/{id}/confirmacion`): resumen del pedido, y el
   JS de esta página vacía el carrito del navegador.

Como el checkout reusa exactamente la misma función que el kardex manual
(`inventario.CreateMovimiento`), **el Reporte de inventario ya muestra las
ventas online mezcladas con las manuales**, sin haber tocado ese código.

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
  `admin.json` a mano en el servidor y volver a pasar por `/admin/setup`.
  Para un usuario de la tabla `usuarios`, el `admin` puede resetearle la
  contraseña desde `/admin/usuarios/{id}/editar`, pero no hay flujo de
  autoservicio ("te mandamos un correo").
- **Sin límite de intentos de login.** No hay bloqueo tras varios intentos
  fallidos (fuerza bruta) — razonable para un panel interno de bajo tráfico,
  pero a reforzar si el admin llega a exponerse directamente a internet sin
  nada delante.
- **Datos anteriores a una migración no se pueden completar retroactivamente
  con precisión.** Ejemplos concretos ya ocurridos en este proyecto:
  - Movimientos registrados antes de separar `fecha` de `creado_en` quedaron
    con `creado_en` igual al momento de la migración, no a su hora real de
    registro (esa información no se guardaba antes).
  - Ventas marcadas como tal antes de agregar `precio_venta` al kardex
    quedaron con precio `0` — cuentan como venta en los reportes, pero no
    suman al "Total vendido ($)".
- **El pago es simulado.** `pasarela-simulada` es un proyecto propio que
  hace de proveedor falso (con 3 modos: aprobar/rechazar/fallar); no hay
  integración real con ningún procesador de pagos todavía (Fase 3). El
  punto de reemplazo ya está aislado en `internal/pasarela`.
- **Sin preparación de pedidos en bodega todavía** (Fase 2): un pedido pasa
  directo a `estado = 'pagado'` y ahí se queda; no hay flujo de
  picking/empaque/envío.
- **Sin validación de NIT contra la SAT.** El campo existe y se guarda, pero
  no se verifica contra ningún servicio externo.
- **Fotos y logo en disco local.** Correcto para desarrollo o un hosting con
  disco persistente; en una plataforma de contenedores efímeros (Railway,
  Render, Fly.io sin volumen) se perderían en cada redeploy — ahí hace falta
  el backend S3-compatible mencionado arriba antes de salir a producción.

---

## Roadmap

- **Tienda — Fase 2 (bodega)**: pantalla de pedidos pendientes, checklist de
  productos a preparar por pedido (ya hay todo lo necesario en
  `pedido_items` — producto, cantidad, y acceso a sus fotos), botones para
  avanzar `estado` (`pagado` → `preparando` → `listo` → `entregado`).
- **Tienda — Fase 3 (pasarela real)**: reemplazar `pasarela.BaseURL()`
  (`internal/pasarela/cliente.go`) por la URL de un proveedor real — el
  contrato (`Cobrar`, `RespuestaCobro`, `ErrNoDisponible`) ya está pensado
  para eso, y `TiendaCheckoutConfirmar` no debería necesitar cambios.
  Candidato a evaluar cuando llegue el momento: proveedores con soporte en
  Guatemala/Centroamérica.
- **Validación de NIT con SAT**: una llamada de validación en el checkout
  antes de confirmar el pedido; el campo `cliente_nit` ya existe.
- **Backend de almacenamiento S3-compatible**: cuando se elija el hosting de
  producción, si no tiene disco persistente.
