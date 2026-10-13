#!/bin/bash
# Comprueba que el último respaldo se puede restaurar de verdad: lo restaura
# en una base temporal y compara las filas de cada tabla con la base real.
# No modifica la base real. Ejecutar como root:
#   sudo /usr/local/sbin/manualidades-probar-restauracion
set -euo pipefail

export RESTIC_REPOSITORY=/var/backups/restic-manualidades
export RESTIC_PASSWORD_FILE=/root/.restic-pass

TMP_DB=manualidades_restauracion
DIR=$(mktemp -d /var/tmp/restauracion.XXXXXX)
trap 'rm -rf "$DIR"; runuser -u postgres -- dropdb --if-exists "$TMP_DB" >/dev/null 2>&1 || true' EXIT

echo "Restaurando el último respaldo en $DIR ..."
restic restore latest --target "$DIR" --include /var/backups/manualidades/manualidades.dump
DUMP="$DIR/var/backups/manualidades/manualidades.dump"

runuser -u postgres -- dropdb --if-exists "$TMP_DB"
runuser -u postgres -- createdb "$TMP_DB"
# Por stdin: el usuario postgres no puede entrar en la carpeta de root.
runuser -u postgres -- pg_restore --no-owner --dbname="$TMP_DB" < "$DUMP"

CONTEO="SELECT table_name, (xpath('/row/c/text()', query_to_xml(format('select count(*) as c from %I.%I', table_schema, table_name), false, true, '')))[1]::text AS filas
        FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE' ORDER BY 1;"

runuser -u postgres -- psql -At -d manualidades      -c "$CONTEO" > "$DIR/real.txt"
runuser -u postgres -- psql -At -d "$TMP_DB"         -c "$CONTEO" > "$DIR/restaurada.txt"

echo "Tablas en la base real: $(wc -l < "$DIR/real.txt")"
if diff -u "$DIR/real.txt" "$DIR/restaurada.txt"; then
  echo "OK: la restauración coincide con la base real (tablas y número de filas)."
else
  echo "DIFERENCIAS: puede ser normal si hubo cambios después del respaldo; revisa la lista de arriba."
  exit 1
fi
