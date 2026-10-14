#!/bin/bash
# Asistente, para hacer UNA vez: conecta los respaldos con un bucket de
# Backblaze B2 y hace la primera copia.
#
#   sudo /usr/local/sbin/manualidades-configurar-offsite
#
# Pide el nombre del bucket, el keyID y la applicationKey. La applicationKey se
# escribe sin que se vea y nunca queda en el historial de comandos ni en el
# chat: solo en /etc/manualidades-respaldo-offsite.env (root, permisos 600).
set -euo pipefail

[ "$(id -u)" = 0 ] || { echo "Ejecútalo con sudo."; exit 1; }

ENV_EXTERNO=/etc/manualidades-respaldo-offsite.env
LOCAL=/var/backups/restic-manualidades
CLAVE_RESTIC=/root/.restic-pass

if [ ! -d "$LOCAL" ] || [ ! -f "$CLAVE_RESTIC" ]; then
  echo "No encuentro el repositorio local de restic: ¿ya hay respaldos?"
  exit 1
fi

if [ -f "$ENV_EXTERNO" ]; then
  read -r -p "Ya hay una copia externa configurada. ¿Reemplazarla? [s/N] " r
  [ "$r" = "s" ] || [ "$r" = "S" ] || { echo "Sin cambios."; exit 0; }
fi

read -r -p "Nombre del bucket de Backblaze: " BUCKET
read -r -p "keyID: " KEYID
read -r -s -p "applicationKey (no se ve al escribir): " APPKEY
echo

if [ -z "$BUCKET" ] || [ -z "$KEYID" ] || [ -z "$APPKEY" ]; then
  echo "Faltan datos. No se cambió nada."
  exit 1
fi
# Los valores se guardan entre comillas simples: no pueden llevarlas dentro.
case "$BUCKET$KEYID$APPKEY" in
  *\'* | *\\* | *' '*) echo "Algún dato tiene un carácter no permitido (comilla, barra o espacio). Revisa que lo pegaste bien."; exit 1;;
esac

TMP=$(mktemp /etc/manualidades-offsite.XXXXXX)
trap 'rm -f "$TMP"' EXIT
chmod 600 "$TMP"
{
  echo "RESTIC_REPOSITORY_OFFSITE='b2:${BUCKET}:restic'"
  echo "B2_ACCOUNT_ID='${KEYID}'"
  echo "B2_ACCOUNT_KEY='${APPKEY}'"
} > "$TMP"

set -a
# shellcheck disable=SC1090
. "$TMP"
set +a
export RESTIC_PASSWORD_FILE="$CLAVE_RESTIC"

echo
echo "Comprobando el acceso al bucket..."
if restic -r "$RESTIC_REPOSITORY_OFFSITE" cat config > /dev/null 2>&1; then
  echo "Ya existe un repositorio en ese bucket; se reutiliza."
else
  echo "Creando el repositorio cifrado en Backblaze..."
  restic -r "$RESTIC_REPOSITORY_OFFSITE" init \
    --from-repo "$LOCAL" --from-password-file "$CLAVE_RESTIC" --copy-chunker-params
fi

# Solo ahora, con el acceso comprobado, se deja la configuración definitiva.
mv "$TMP" "$ENV_EXTERNO"
chmod 600 "$ENV_EXTERNO"
trap - EXIT

echo
echo "Primera copia (puede tardar unos minutos)..."
/usr/local/sbin/manualidades-copia-externa

echo
echo "Copias que hay ahora en Backblaze:"
restic -r "$RESTIC_REPOSITORY_OFFSITE" snapshots
echo
echo "Listo. Desde ahora cada respaldo nocturno se copia también a Backblaze."
