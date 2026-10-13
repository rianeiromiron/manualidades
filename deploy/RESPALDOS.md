# Respaldos de manualidades

Respaldo cifrado e incremental con [restic](https://restic.net). Corre cada
noche a las 02:00 de Guatemala (08:00 UTC) en el servidor.

## Qué se guarda

- Volcado de la base de datos (`pg_dump`, formato custom).
- Las fotos: `/opt/manualidades/media`.
- `admin.json` (usuario admin y llave de sesiones) y `config.json` (acceso a
  Postgres).

Historial: 7 copias diarias, 4 semanales y 6 mensuales.

## Dónde está

| Cosa | Ruta en el servidor |
|---|---|
| Repositorio local de restic | `/var/backups/restic-manualidades` |
| Contraseña del repositorio | `/root/.restic-pass` (solo root) |
| Volcado de la base (el último) | `/var/backups/manualidades/manualidades.dump` |
| Script | `/usr/local/sbin/manualidades-respaldo` |
| Temporizador | `manualidades-respaldo.timer` |

**La contraseña del repositorio debe estar también en el gestor de
contraseñas.** Sin ella la copia no se puede abrir, ni siquiera por quien la
hizo.

> El repositorio local solo protege de borrados accidentales y de datos
> dañados. Si se pierde el servidor se pierde también esta copia: hace falta
> además una copia fuera del servidor (fase 2, pendiente de elegir destino).

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
   sudo cp /var/tmp/restaurar/opt/manualidades/admin.json  /opt/manualidades/admin.json
   sudo cp /var/tmp/restaurar/opt/manualidades/config.json /opt/manualidades/config.json
   sudo chown -R manualidades:manualidades /opt/manualidades/media \
        /opt/manualidades/admin.json /opt/manualidades/config.json
   sudo chmod 600 /opt/manualidades/admin.json /opt/manualidades/config.json
   ```

5. Arrancar la app: `sudo systemctl start manualidades` y borrar
   `/var/tmp/restaurar`.

Si el servidor es nuevo, la contraseña de la base de datos de `config.json`
restaurado puede no coincidir con el rol nuevo de Postgres: crea el rol con
esa misma contraseña o actualiza `config.json`.
