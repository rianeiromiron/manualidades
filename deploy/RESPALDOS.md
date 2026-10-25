# Respaldos de manualidades

Respaldo cifrado e incremental con [restic](https://restic.net). Corre cada
noche a las 02:00 de Guatemala (08:00 UTC) en el servidor.

## Qué se guarda

- Volcado de la base de datos (`pg_dump`, formato custom).
- Las fotos: `/opt/manualidades/media`.
- Los comprobantes de transferencia (imágenes que suben los clientes):
  `/opt/manualidades/comprobantes`. Esa carpeta existe desde el primer
  comprobante; mientras no haya ninguno, el respaldo la omite sin error.
- `admin.json` (usuario admin y llave de sesiones), `config.json` (acceso a
  Postgres) y `smtp.json` (servidor de correo saliente, solo si se configuró).

Historial: 7 copias diarias, 4 semanales y 6 mensuales.

## Dónde está

| Cosa | Ruta en el servidor |
|---|---|
| Repositorio local de restic | `/var/backups/restic-manualidades` |
| Contraseña del repositorio | `/root/.restic-pass` (solo root) |
| Volcado de la base (el último) | `/var/backups/manualidades/manualidades.dump` |
| Script | `/usr/local/sbin/manualidades-respaldo` |
| Temporizador | `manualidades-respaldo.timer` |
| Copia externa | `/usr/local/sbin/manualidades-copia-externa`; claves en `/etc/manualidades-respaldo-offsite.env` |

**La contraseña del repositorio debe estar también en el gestor de
contraseñas.** Sin ella la copia no se puede abrir, ni siquiera por quien la
hizo.

> El repositorio local solo protege de borrados accidentales y de datos
> dañados. Si se pierde el servidor se pierde también esta copia: por eso cada
> respaldo se copia además a Backblaze B2 (sección siguiente).

## Copia fuera del servidor (Backblaze B2)

Al final de cada respaldo, `manualidades-copia-externa` envía a un bucket de
Backblaze B2 las copias que aún no tiene (`restic copy`) y aplica la misma
retención. Lo que viaja ya está cifrado con la contraseña de restic: Backblaze
no puede leerlo. El servidor, el dominio y las copias quedan en tres empresas
distintas (Oracle, Cloudflare, Backblaze).

Si la copia externa falla, el servicio `manualidades-respaldo` queda en
**fallido** (el respaldo local sí se hizo). Se ve con
`systemctl status manualidades-respaldo` y en el registro.

### Configurarla (una sola vez)

1. En Backblaze: crear un bucket **privado** (sin Object Lock) y una clave de
   aplicación limitada a ese bucket, con lectura y escritura. La
   `applicationKey` se muestra una sola vez.
2. Instalar los scripts en el servidor (desde la carpeta `deploy/` del proyecto,
   subidos con `scp`):

   ```bash
   sudo install -m 755 respaldo.sh /usr/local/sbin/manualidades-respaldo
   sudo install -m 755 copia-externa.sh /usr/local/sbin/manualidades-copia-externa
   sudo install -m 755 configurar-offsite.sh /usr/local/sbin/manualidades-configurar-offsite
   ```

3. Ejecutar el asistente. Pide el bucket, el `keyID` y la `applicationKey` (esta
   última sin mostrarse), crea el repositorio cifrado y hace la primera copia:

   ```bash
   sudo /usr/local/sbin/manualidades-configurar-offsite
   ```

   Guarda las claves en `/etc/manualidades-respaldo-offsite.env` (solo root,
   permisos 600). Nunca las pegues en un chat ni las dejes en el historial.

### Restaurar desde Backblaze (servidor perdido)

En un servidor nuevo, con restic instalado y la contraseña de restic del gestor
de contraseñas:

```bash
echo 'LA-CONTRASEÑA-DE-RESTIC' | sudo tee /root/.restic-pass > /dev/null && sudo chmod 600 /root/.restic-pass
export RESTIC_REPOSITORY=b2:NOMBRE-DEL-BUCKET:restic
export B2_ACCOUNT_ID=EL-KEYID
export B2_ACCOUNT_KEY=LA-APPLICATIONKEY
export RESTIC_PASSWORD_FILE=/root/.restic-pass
sudo -E restic snapshots
sudo -E restic restore latest --target /var/tmp/restaurar
```

y seguir con los pasos 3 a 5 de "Restaurar de verdad" más abajo (base de datos,
fotos y archivos de configuración). Si ya no tienes el `keyID` y la
`applicationKey`, crea una clave nueva en Backblaze: no hace falta la anterior.

Los scripts se probaron en un Ubuntu desechable (copia, repetición sin
duplicar, restauración desde el destino con la misma contraseña, contraseña
incorrecta, destino que falla y datos inválidos en el asistente).

## Comandos útiles

```bash
# Lanzar un respaldo ahora y ver cómo va
sudo systemctl start manualidades-respaldo.service
sudo journalctl -u manualidades-respaldo -n 30 --no-pager

# Cuándo corre el siguiente
systemctl list-timers manualidades-respaldo.timer

# Lista de copias guardadas
sudo RESTIC_REPOSITORY=/var/backups/restic-manualidades \
     RESTIC_PASSWORD_FILE=/root/.restic-pass restic snapshots

# Probar que se puede restaurar (no toca la base real)
sudo /usr/local/sbin/manualidades-probar-restauracion
```

Conviene repetir la prueba de restauración de vez en cuando: una copia que
nunca se ha restaurado no es una garantía.

## Restaurar de verdad (servidor roto o datos perdidos)

1. Detener la app: `sudo systemctl stop manualidades`.
2. Sacar la copia en una carpeta temporal:

   ```bash
   export RESTIC_REPOSITORY=/var/backups/restic-manualidades
   export RESTIC_PASSWORD_FILE=/root/.restic-pass
   sudo -E restic restore latest --target /var/tmp/restaurar
   ```

3. Base de datos (esto **reemplaza** el contenido actual):

   ```bash
   sudo -u postgres dropdb manualidades
   sudo -u postgres createdb -O manualidades manualidades
   sudo -u postgres pg_restore --no-owner --role=manualidades -d manualidades \
        < /var/tmp/restaurar/var/backups/manualidades/manualidades.dump
   ```

4. Fotos y archivos de configuración:

   ```bash
   sudo rsync -a /var/tmp/restaurar/opt/manualidades/media/ /opt/manualidades/media/
   # comprobantes/ solo existe si algún cliente subió un comprobante:
   sudo rsync -a /var/tmp/restaurar/opt/manualidades/comprobantes/ /opt/manualidades/comprobantes/ 2>/dev/null || true
   sudo cp /var/tmp/restaurar/opt/manualidades/admin.json  /opt/manualidades/admin.json
   sudo cp /var/tmp/restaurar/opt/manualidades/config.json /opt/manualidades/config.json
   # smtp.json solo existe si se configuró el correo saliente:
   sudo cp /var/tmp/restaurar/opt/manualidades/smtp.json /opt/manualidades/smtp.json 2>/dev/null || true
   sudo chown -R manualidades:manualidades /opt/manualidades/media \
        /opt/manualidades/admin.json /opt/manualidades/config.json
   sudo chmod 600 /opt/manualidades/admin.json /opt/manualidades/config.json
   sudo chown manualidades:manualidades /opt/manualidades/smtp.json 2>/dev/null && sudo chmod 600 /opt/manualidades/smtp.json || true
   sudo chown -R manualidades:manualidades /opt/manualidades/comprobantes 2>/dev/null || true
   ```

5. Arrancar la app: `sudo systemctl start manualidades` y borrar
   `/var/tmp/restaurar`.

Si el servidor es nuevo, la contraseña de la base de datos de `config.json`
restaurado puede no coincidir con el rol nuevo de Postgres: crea el rol con
esa misma contraseña o actualiza `config.json`.
