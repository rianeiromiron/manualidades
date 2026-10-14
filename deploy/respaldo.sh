#!/bin/bash
# Respaldo cifrado de manualidades con restic. Lo ejecuta el temporizador
# manualidades-respaldo.timer como root; también se puede lanzar a mano:
#   sudo systemctl start manualidades-respaldo.service
#
# Qué guarda: volcado de la base de datos, fotos (media/), admin.json y
# config.json. restic cifra y deduplica: las fotos que no cambian no se
# vuelven a copiar cada noche.
set -euo pipefail

APP_DIR=/opt/manualidades
STAGE=/var/backups/manualidades
export RESTIC_REPOSITORY=/var/backups/restic-manualidades
export RESTIC_PASSWORD_FILE=/root/.restic-pass

install -d -m 700 "$STAGE"

# 1. Volcado de la base. El formato "custom" va comprimido y permite
#    restaurar tablas sueltas. La redirección la hace root para que postgres
#    no necesite escribir en $STAGE.
runuser -u postgres -- pg_dump --format=custom manualidades > "$STAGE/manualidades.dump.tmp"
# Si el volcado salió corrupto, falla aquí y no sustituye al anterior.
pg_restore --list "$STAGE/manualidades.dump.tmp" > /dev/null
mv "$STAGE/manualidades.dump.tmp" "$STAGE/manualidades.dump"

# 2. Copia cifrada.
restic backup --host manualidades --tag diario \
  "$STAGE/manualidades.dump" \
  "$APP_DIR/media" \
  "$APP_DIR/admin.json" \
  "$APP_DIR/config.json"

# 3. Historial: 7 diarias, 4 semanales y 6 mensuales.
restic forget --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune

# 4. Los domingos, comprobar que el repositorio no está dañado.
if [ "$(date +%u)" = 7 ]; then
  restic check
fi

# 5. Copia fuera del servidor, si está configurada. Un fallo aquí no deshace el
#    respaldo local (ya está hecho), pero deja el servicio en "fallido" para que
#    se note en `systemctl status` y en el registro.
if ! /usr/local/sbin/manualidades-copia-externa; then
  echo "ERROR: falló la copia externa. El respaldo local sí se hizo."
  exit 1
fi

echo "respaldo terminado"
