#!/bin/bash
# Copia los respaldos locales de restic a un repositorio fuera del servidor
# (Backblaze B2). Lo llama deploy/respaldo.sh al final de cada respaldo; también
# se puede lanzar a mano:  sudo /usr/local/sbin/manualidades-copia-externa
#
# Si no hay copia externa configurada (no existe el archivo de abajo), no hace
# nada y termina bien. Se configura una vez con manualidades-configurar-offsite.
#
# `restic copy` solo envía lo que el destino todavía no tiene. Lo que viaja ya
# está cifrado con la contraseña de /root/.restic-pass: Backblaze no puede leerlo.
set -euo pipefail

ENV_EXTERNO=/etc/manualidades-respaldo-offsite.env
LOCAL="${RESTIC_REPOSITORY:-/var/backups/restic-manualidades}"
export RESTIC_PASSWORD_FILE="${RESTIC_PASSWORD_FILE:-/root/.restic-pass}"

if [ ! -f "$ENV_EXTERNO" ]; then
  echo "(copia externa sin configurar: nada que hacer)"
  exit 0
fi

# Define RESTIC_REPOSITORY_OFFSITE y las credenciales del proveedor (B2_*).
set -a
# shellcheck disable=SC1090
. "$ENV_EXTERNO"
set +a
: "${RESTIC_REPOSITORY_OFFSITE:?falta RESTIC_REPOSITORY_OFFSITE en $ENV_EXTERNO}"

echo "Copiando al repositorio externo..."
restic -r "$RESTIC_REPOSITORY_OFFSITE" copy \
  --from-repo "$LOCAL" --from-password-file "$RESTIC_PASSWORD_FILE"

# Misma retención que el repositorio local.
restic -r "$RESTIC_REPOSITORY_OFFSITE" forget \
  --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune

# Los domingos, comprobar que el repositorio externo está sano.
if [ "$(date +%u)" = 7 ]; then
  restic -r "$RESTIC_REPOSITORY_OFFSITE" check
fi

echo "copia externa terminada"
