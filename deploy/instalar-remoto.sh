#!/bin/bash
# Se ejecuta EN EL SERVIDOR, como root. Lo lanza deploy/desplegar.ps1 después de
# subir el paquete a /tmp/despliegue.tar.gz y descomprimirlo en /tmp/despliegue:
#
#   sudo bash instalar-remoto.sh <sha256-del-paquete>
#
# Instala la versión nueva, la comprueba y, si no responde, vuelve sola a la
# anterior. No toca nunca config.json, admin.json ni media/.
set -euo pipefail

APP=/opt/manualidades
PAQUETE=/tmp/despliegue.tar.gz
DIR=/tmp/despliegue
SHA_ESPERADO="${1:-}"

trap 'rm -rf "$DIR" "$PAQUETE"' EXIT

[ -n "$SHA_ESPERADO" ] || { echo "ERROR: falta la huella (sha256) del paquete."; exit 2; }

SHA_REAL=$(sha256sum "$PAQUETE" | cut -d' ' -f1)
if [ "$SHA_REAL" != "$SHA_ESPERADO" ]; then
  echo "ERROR: el paquete llegó dañado (la huella no coincide). No se instaló nada."
  exit 1
fi
[ -f "$DIR/manualidades" ] && [ -d "$DIR/web/templates" ] || { echo "ERROR: el paquete está incompleto."; exit 1; }

# La app está sana cuando el servicio está activo y la portada responde 200.
esta_sana() {
  systemctl is-active --quiet manualidades || return 1
  local i code
  for i in $(seq 1 20); do
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 http://127.0.0.1:8090/ || true)
    [ "$code" = "200" ] && return 0
    sleep 1
  done
  return 1
}

echo "[1/5] Respaldo previo (si falla, no se despliega nada)..."
systemctl start manualidades-respaldo.service
echo "      listo"

echo "[2/5] Guardando la versión actual para poder volver atrás..."
cp -p "$APP/manualidades" "$APP/manualidades.anterior"
rm -rf "$APP/web.anterior"
cp -a "$APP/web" "$APP/web.anterior"

echo "[3/5] Preparando la versión nueva..."
install -o manualidades -g manualidades -m 755 "$DIR/manualidades" "$APP/manualidades.nuevo"
rm -rf "$APP/web.nuevo"
cp -a "$DIR/web" "$APP/web.nuevo"
chown -R manualidades:manualidades "$APP/web.nuevo"
find "$APP/web.nuevo" -type d -exec chmod 755 {} +
find "$APP/web.nuevo" -type f -exec chmod 644 {} +

echo "[4/5] Reiniciando la aplicación..."
systemctl stop manualidades
mv -f "$APP/manualidades.nuevo" "$APP/manualidades"
rm -rf "$APP/web"
mv "$APP/web.nuevo" "$APP/web"
systemctl start manualidades

echo "[5/5] Comprobando que la tienda responde..."
if esta_sana; then
  echo "OK: la versión nueva está en marcha."
  exit 0
fi

echo "FALLÓ la comprobación. Últimas líneas del registro:"
journalctl -u manualidades -n 15 --no-pager || true
echo "Volviendo a la versión anterior..."
systemctl stop manualidades || true
mv -f "$APP/manualidades.anterior" "$APP/manualidades"
rm -rf "$APP/web"
mv "$APP/web.anterior" "$APP/web"
systemctl start manualidades
if esta_sana; then
  echo "Se restauró la versión anterior y responde. La versión nueva NO quedó instalada."
else
  echo "ATENCIÓN: ni la versión anterior responde. Revisa: sudo journalctl -u manualidades -n 50"
fi
exit 1
