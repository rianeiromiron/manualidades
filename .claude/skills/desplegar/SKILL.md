---
name: desplegar
description: Despliega manualidades en el servidor de Oracle (producción) con deploy/desplegar.ps1 - pruebas, compilación Linux ARM64, respaldo previo, instalación, reinicio, verificación y reversión automática. Solo se usa cuando el usuario lo pide con /desplegar.
version: 1.0.0
disable-model-invocation: true
---
# Desplegar manualidades al servidor

Este skill sube a **producción** el código que hay en el disco. Procede con
cuidado y en este orden. Documentación completa: `deploy/DESPLIEGUE.md`.

Datos fijos: servidor `ubuntu@160.34.210.207`, llave `~/.ssh/oracle_manualidades`
(protegida con frase), dirección pública `https://prueba.manualidadescristal.com`.
El script es `deploy/desplegar.ps1` y se ejecuta con PowerShell desde la raíz del
repositorio.

## Qué hacer

1. **Mirar qué se va a desplegar.** Ejecuta `git status --short` y
   `git log origin/master..HEAD --oneline`. Resume al usuario: el commit actual,
   si hay cambios sin commit y si hay commits sin subir a GitHub.
   - Si hay cambios sin commit, **pregunta** si quiere hacer commit antes. No uses
     `-Forzar` sin que lo pida.
   - Si el cambio toca el esquema de la base de datos (funciones `Migrate`,
     `schema.go`), avísalo: se aplica solo al arrancar y no se deshace al volver
     atrás; el script hace un respaldo antes.

2. **Ensayo, sin tocar el servidor.** Desde la raíz del repositorio:

   ```powershell
   powershell -NoProfile -ExecutionPolicy Bypass -File deploy/desplegar.ps1 -SoloPaquete
   ```

   Corre `go vet`, `go test` (tardan un par de minutos; necesitan Docker y Chrome),
   compila para Linux ARM64 y empaqueta. Si falla algo, **para** y explica el
   motivo; no despliegues.

3. **Subir e instalar.** Esta parte pide la **frase de la llave SSH**, que es
   interactiva.
   - Nunca pidas la frase por el chat ni la escribas tú.
   - Comprueba si la llave ya está cargada en el agente: `ssh-add -l`. Si aparece
     `oracle_manualidades`, puedes ejecutar tú el despliegue completo
     (`-SinPruebas`, porque ya pasaron en el paso 2):

     ```powershell
     powershell -NoProfile -ExecutionPolicy Bypass -File deploy/desplegar.ps1 -SinPruebas
     ```

   - Si no está cargada, dile al usuario que lo ejecute **en su propia terminal
     de PowerShell**, en la raíz del proyecto, y que te pegue el resultado:

     ```powershell
     .\deploy\desplegar.ps1 -SinPruebas
     ```

4. **Interpretar el resultado.**
   - Termina con `Desplegado el commit ...` y las dos direcciones en HTTP 200: éxito.
   - Si dice que **volvió a la versión anterior**, la tienda sigue como estaba y el
     despliegue no se aplicó. Pide al usuario el registro
     (`sudo journalctl -u manualidades -n 50 --no-pager`), diagnostica la causa y
     corrígela antes de reintentar. No reintentes sin cambiar nada.
   - Si falla la subida o el SSH, revisa la llave, la IP y la conexión.

## Reglas

- No copies nunca `config.json`, `admin.json` ni `media/` al servidor: son datos de
  producción. El script ya los deja intactos.
- No abras puertos ni toques el firewall, Caddy o Cloudflare desde este skill.
- Si el usuario pide volver atrás a mano, usa el procedimiento de
  `deploy/DESPLIEGUE.md` ("Si algo sale mal").
- Después de un despliegue correcto, sugiere probar la tienda y el login de
  administración en el navegador.
