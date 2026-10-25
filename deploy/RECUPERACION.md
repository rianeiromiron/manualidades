# Cómo recuperar manualidades desde un respaldo

Guía paso a paso para cuando algo sale mal: se borraron datos, se dañó la base
o se perdió el servidor. Está pensada para leerse con calma y seguirse en orden.

Complementa a [RESPALDOS.md](RESPALDOS.md) (cómo funcionan los respaldos) y a
[DESPLIEGUE.md](DESPLIEGUE.md) (cómo se monta el servidor desde cero).

## 1. Antes de empezar

### Qué hay en un respaldo

- La base de datos completa (productos, categorías, usuarios, pedidos, etc.).
- Las fotos (`/opt/manualidades/media`).
- Los comprobantes de transferencia (`/opt/manualidades/comprobantes`), si algún
  cliente ya subió alguno.
- `admin.json` y `config.json` (usuario administrador raíz y acceso a Postgres), y
  `smtp.json` (servidor de correo saliente) si se configuró el correo.

No incluye el programa en sí: el binario y `web/` se vuelven a instalar con
`/desplegar`.

### Dónde están las copias

| Copia | Dónde | Cuándo usarla |
|---|---|---|
| **Local** | En el servidor, `/var/backups/restic-manualidades` | El servidor funciona pero se perdieron o dañaron datos |
| **Externa** | Backblaze B2, bucket `manualidades-respaldo-abc`, carpeta `restic` | Se perdió el servidor entero |

Las dos tienen el mismo contenido y la misma contraseña. Se hace una copia cada
noche a las 02:00 (hora de Guatemala). Se conservan 7 diarias, 4 semanales y 6
mensuales.

### Qué necesitas tener a mano

| Cosa | Dónde la tienes | Si la pierdes |
|---|---|---|
| **Contraseña de restic** | Tu gestor de contraseñas | **No tiene arreglo**: las copias quedan ilegibles para siempre |
| Llave SSH `~/.ssh/oracle_manualidades` y su frase | Tu PC | Se puede crear otra desde la consola de Oracle |
| `keyID` y `applicationKey` de Backblaze (solo caso B) | Tu gestor de contraseñas | Se crea una clave nueva en Backblaze, limitada al bucket |

> Lo que se ve en Backblaze (`restic/data`, `snapshots`, `index`, `keys`,
> `config`) no se puede leer ni descargar a mano: son trozos cifrados. Solo
> restic con la contraseña sabe armarlos.

### Reglas de seguridad

- Nunca pegues la contraseña de restic ni la `applicationKey` en un chat, un
  correo o un archivo del proyecto.
- Antes de reemplazar algo, mira qué hay. La restauración de la base **borra lo
  actual**.
- Si dudas, restaura primero a una carpeta temporal y revisa. No toca nada.

## 2. Qué caso es el mío

```
¿Puedes entrar al servidor por SSH y la base existe?
   │
   ├─ Sí ─► ¿Perdiste TODO o solo algo?
   │          ├─ Solo algo (una foto, un producto) ─► Caso C
   │          └─ Todo / la base está dañada        ─► Caso A
   │
   └─ No (servidor perdido o irrecuperable)        ─► Caso B
```

## 3. Entrar al servidor

En PowerShell, en tu PC:

```powershell
ssh -i ~/.ssh/oracle_manualidades ubuntu@160.34.210.207
```

Te pide la frase de la llave. Todos los comandos de abajo se corren ya dentro
del servidor.

## 4. Caso A: restaurar todo desde la copia local

El servidor funciona, pero los datos están perdidos o dañados.

### Paso 1. Ver qué copias hay

```bash
export RESTIC_REPOSITORY=/var/backups/restic-manualidades
export RESTIC_PASSWORD_FILE=/root/.restic-pass
sudo -E restic snapshots
```

Verás algo así (una fila por respaldo):

```
ID        Time                 Host        Tags        Paths
------------------------------------------------------------------
3f2a91c0  2026-10-12 02:00:04  manualidades            /var/backups/manualidades ...
b81d77e5  2026-10-13 02:00:03  manualidades            ...
c4e0a2d9  2026-10-14 02:00:05  manualidades            ...
```

Anota el `ID` de la copia que quieres. `latest` significa "la más reciente".
Si el daño ocurrió hoy, elige la de ayer, no la de esta madrugada.

### Paso 2. Detener la app

```bash
sudo systemctl stop manualidades
```

La tienda dejará de responder hasta que la vuelvas a arrancar.

### Paso 3. Sacar la copia a una carpeta temporal

```bash
sudo -E restic restore latest --target /var/tmp/restaurar
# o una copia concreta:
# sudo -E restic restore c4e0a2d9 --target /var/tmp/restaurar
```

Comprueba que salió lo esperado:

```bash
sudo ls -lh /var/tmp/restaurar/var/backups/manualidades/manualidades.dump
sudo ls /var/tmp/restaurar/opt/manualidades/
```

Debes ver el archivo `.dump` con un tamaño razonable y las carpetas `media` (y
`comprobantes`, si ya hay), más `admin.json`, `config.json` y, si configuraste el
correo, `smtp.json`.

### Paso 4. Restaurar la base de datos

**Esto reemplaza todo el contenido actual de la base.**

```bash
sudo -u postgres dropdb manualidades
sudo -u postgres createdb -O manualidades manualidades
sudo -u postgres pg_restore --no-owner --role=manualidades -d manualidades \
     < /var/tmp/restaurar/var/backups/manualidades/manualidades.dump
```

Comprueba que hay datos (esperado hoy: 190 productos):

```bash
sudo -u postgres psql -d manualidades -c "select count(*) from productos;"
```

### Paso 5. Restaurar fotos y configuración

```bash
sudo rsync -a /var/tmp/restaurar/opt/manualidades/media/ /opt/manualidades/media/
# comprobantes/ y smtp.json solo existen si ya se usaron; si no están, se omiten sin error:
sudo rsync -a /var/tmp/restaurar/opt/manualidades/comprobantes/ /opt/manualidades/comprobantes/ 2>/dev/null || true
sudo cp /var/tmp/restaurar/opt/manualidades/smtp.json /opt/manualidades/smtp.json 2>/dev/null || true
sudo cp /var/tmp/restaurar/opt/manualidades/admin.json  /opt/manualidades/admin.json
sudo cp /var/tmp/restaurar/opt/manualidades/config.json /opt/manualidades/config.json
sudo chown -R manualidades:manualidades /opt/manualidades/media \
     /opt/manualidades/admin.json /opt/manualidades/config.json
sudo chmod 600 /opt/manualidades/admin.json /opt/manualidades/config.json
sudo chown -R manualidades:manualidades /opt/manualidades/comprobantes 2>/dev/null || true
sudo chown manualidades:manualidades /opt/manualidades/smtp.json 2>/dev/null && sudo chmod 600 /opt/manualidades/smtp.json || true
```

### Paso 6. Arrancar y limpiar

```bash
sudo systemctl start manualidades
sudo systemctl status manualidades --no-pager
sudo rm -rf /var/tmp/restaurar
```

### Paso 7. Verificar

- Abre https://prueba.manualidadescristal.com y mira que cargan productos y
  fotos.
- Entra a `/admin` con tu usuario y revisa productos, categorías y usuarios.

## 5. Caso B: el servidor se perdió, restaurar desde Backblaze

### Paso 1. Montar un servidor nuevo

Sigue [DESPLIEGUE.md](DESPLIEGUE.md): VM, Ubuntu 24.04, Postgres 16, usuario de
sistema `manualidades`, la app en `/opt/manualidades`, Caddy y el certificado
de Cloudflare. Instala también restic (`sudo apt install restic`).

Si la IP pública cambió, actualiza el registro DNS en Cloudflare y la IP en
`deploy/desplegar.ps1`.

En el servidor nuevo, para que la restauración no choque con datos vacíos,
deja parada la app: `sudo systemctl stop manualidades`.

### Paso 2. Conectar con Backblaze

Si ya no tienes el `keyID` y la `applicationKey`, crea una clave nueva en
Backblaze (App Keys), limitada al bucket `manualidades-respaldo-abc`, con
lectura y escritura. No hace falta la anterior.

```bash
# Contraseña de restic (sustituye el texto; el espacio inicial evita guardarla en el historial)
 echo 'LA-CONTRASEÑA-DE-RESTIC' | sudo tee /root/.restic-pass > /dev/null && sudo chmod 600 /root/.restic-pass

export RESTIC_REPOSITORY=b2:manualidades-respaldo-abc:restic
export B2_ACCOUNT_ID=EL-KEYID
export B2_ACCOUNT_KEY=LA-APPLICATIONKEY
export RESTIC_PASSWORD_FILE=/root/.restic-pass

sudo -E restic snapshots
```

Si `snapshots` muestra la lista de copias, la conexión y la contraseña están
bien. Si dice `wrong password or no key found`, la contraseña está mal escrita.

### Paso 3. Sacar la copia

```bash
sudo -E restic restore latest --target /var/tmp/restaurar
```

### Paso 4. Restaurar base, fotos y configuración

Si la base `manualidades` y su rol no existen en el servidor nuevo, créalos
antes (la contraseña del rol debe coincidir con la de `config.json`):

```bash
sudo -u postgres psql -c "create role manualidades login password 'LA-CONTRASEÑA-DE-LA-BD';"
```

Después, igual que en el caso A, **pasos 4, 5 y 6** (base de datos, fotos y
configuración, arrancar). Como `config.json` restaurado trae la contraseña de
Postgres de la copia, si creaste el rol con otra, usa la misma que aparece en
ese archivo:

```bash
sudo grep -i pass /var/tmp/restaurar/opt/manualidades/config.json
```

Para que la nueva contraseña del servidor y la de `config.json` coincidan, lo
más simple es crear el rol con la contraseña que tiene el `config.json`.

### Paso 5. Dejar al servidor haciendo respaldos otra vez

Instala los scripts y el temporizador, y configura de nuevo la copia externa
(ver [RESPALDOS.md](RESPALDOS.md), sección "Configurarla"). Luego prueba:

```bash
sudo systemctl start manualidades-respaldo.service
sudo journalctl -u manualidades-respaldo -n 30 --no-pager
```

Debe terminar con `copia externa terminada`.

## 6. Caso C: recuperar solo una cosa

No hace falta reemplazar todo. Restaura a la carpeta temporal y copia solo lo
que necesitas.

### Una foto o una carpeta de fotos

```bash
export RESTIC_REPOSITORY=/var/backups/restic-manualidades
export RESTIC_PASSWORD_FILE=/root/.restic-pass
sudo -E restic restore latest --target /var/tmp/restaurar \
     --include /opt/manualidades/media/productos

sudo cp /var/tmp/restaurar/opt/manualidades/media/productos/NOMBRE-DE-LA-FOTO.jpg \
        /opt/manualidades/media/productos/
sudo chown manualidades:manualidades /opt/manualidades/media/productos/NOMBRE-DE-LA-FOTO.jpg
sudo rm -rf /var/tmp/restaurar
```

### Un producto o una tabla borrados por error

Restaura el volcado a una base temporal, sin tocar la real:

```bash
sudo -E restic restore latest --target /var/tmp/restaurar \
     --include /var/backups/manualidades

sudo -u postgres createdb manualidades_copia
sudo -u postgres pg_restore --no-owner -d manualidades_copia \
     < /var/tmp/restaurar/var/backups/manualidades/manualidades.dump
```

Ahora puedes mirar `manualidades_copia` con DBeaver (por el túnel SSH) o con
`psql`, y copiar las filas que faltan a la base real. Cuando termines:

```bash
sudo -u postgres dropdb manualidades_copia
sudo rm -rf /var/tmp/restaurar
```

### Ver qué cambió entre dos copias

```bash
sudo -E restic diff 3f2a91c0 c4e0a2d9
```

## 7. Ensayar sin riesgo

Conviene hacerlo de vez en cuando (por ejemplo, una vez al mes). Una copia que
nunca se ha restaurado no es una garantía.

```bash
sudo /usr/local/sbin/manualidades-probar-restauracion
```

Restaura el último volcado a una base temporal y comprueba que tiene tablas.
No toca la base real.

Para ensayar la copia de Backblaze, usa las variables del caso B, paso 2, y
`restic restore latest --target /var/tmp/ensayo` con `--include` sobre el
volcado.

## 8. Si algo falla

| Síntoma | Qué significa | Qué hacer |
|---|---|---|
| `wrong password or no key found` | La contraseña de restic es incorrecta | Copiarla del gestor de contraseñas, sin espacios ni saltos de línea sobrantes |
| `unable to open config file` | Ruta del repositorio mal escrita | Revisar `RESTIC_REPOSITORY` |
| `repository is already locked` | Hay un respaldo en curso o quedó un candado viejo | Esperar unos minutos; si persiste, `sudo -E restic unlock` |
| `permission denied` al restaurar | Falta `sudo` o `-E` | Usar `sudo -E` para conservar las variables |
| Backblaze: `401 Unauthorized` | `keyID` o `applicationKey` mal | Crear una clave nueva en Backblaze |
| `pg_restore` muestra errores de "already exists" | La base no estaba vacía | Repetir el paso de `dropdb` y `createdb` antes |
| La tienda carga pero sin fotos | `media/` no se restauró o con permisos incorrectos | Repetir `rsync` y `chown` del paso 5 |
| No hay copias de hoy | El respaldo nocturno falló | `sudo journalctl -u manualidades-respaldo -n 50 --no-pager` |

## 9. Lista de comprobación final

- [ ] La tienda carga y se ven productos y fotos.
- [ ] Puedes entrar a `/admin` con tu usuario.
- [ ] Los conteos coinciden con lo esperado (productos, fotos, usuarios).
- [ ] La app está arrancada: `systemctl status manualidades`.
- [ ] No quedó nada en `/var/tmp/restaurar`.
- [ ] Hiciste un respaldo nuevo después de restaurar y terminó con `copia
      externa terminada`.
