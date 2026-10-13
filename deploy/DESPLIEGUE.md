# Desplegar manualidades en el servidor

`git push` solo guarda el código en GitHub: **el servidor no se entera**. Para
llevar un cambio al servidor de Oracle se usa `deploy/desplegar.ps1`.

## Cómo correrlo

Desde PowerShell, en la raíz del proyecto:

```powershell
.\deploy\desplegar.ps1
```

Con Claude Code, escribe `/desplegar` (skill del proyecto en
`.claude/skills/desplegar/`).

El script pide la frase de la llave SSH **dos veces** (subir e instalar).

### Qué hace, en orden

1. Se niega a seguir si hay cambios sin commit (sale con `-Forzar`).
2. Corre `go vet ./...` y `go test ./...`. Si algo falla, no se despliega.
   Las pruebas necesitan Docker (Postgres) y Chrome.
3. Compila para Linux ARM64 y empaqueta el ejecutable y la carpeta `web/`.
4. Sube el paquete e instala en el servidor (`instalar-remoto.sh`):
   1. Hace un respaldo previo; si falla, no despliega.
   2. Guarda la versión actual como `manualidades.anterior` y `web.anterior`.
   3. Instala la nueva, reinicia y comprueba que la portada responde 200.
   4. Si no responde, **vuelve sola a la versión anterior** y avisa.
5. Comprueba desde fuera `/` y `/admin/login` en la dirección pública.

### Opciones

| Opción | Efecto |
|---|---|
| `-SinPruebas` | Salta `go vet` y `go test` (por ejemplo, si ya pasaron hace un momento) |
| `-SoloPaquete` | Compila y empaqueta, pero no sube nada. Sirve para ensayar |
| `-Forzar` | Permite desplegar con cambios sin commit |
| `-Servidor`, `-Llave`, `-UrlPublica` | Cambian el destino. Por defecto: `ubuntu@160.34.210.207`, `~/.ssh/oracle_manualidades` y `https://prueba.manualidadescristal.com` |

Cuando la tienda pase al dominio principal, cambia el valor por defecto de
`-UrlPublica` en el script.

## Qué no toca nunca

`config.json`, `admin.json` y `media/` del servidor. Son los datos de
producción. No copies los de tu PC encima.

## Si algo sale mal

El propio script revierte si la tienda no arranca. Para volver atrás a mano más
tarde (la versión anterior queda guardada en el servidor):

```bash
sudo systemctl stop manualidades
sudo mv -f /opt/manualidades/manualidades.anterior /opt/manualidades/manualidades
sudo rm -rf /opt/manualidades/web && sudo mv /opt/manualidades/web.anterior /opt/manualidades/web
sudo systemctl start manualidades
```

Esto devuelve el código, **no deshace cambios de base de datos**. La app crea
y ajusta sus tablas al arrancar (`Migrate`); si una versión nueva cambió el
esquema, para volver atrás de verdad hay que restaurar el respaldo (ver
[RESPALDOS.md](RESPALDOS.md)). Por eso el script hace un respaldo antes de
cada despliegue.

Registro de la aplicación:

```bash
sudo journalctl -u manualidades -n 50 --no-pager
```

## Para no escribir la frase cada vez (opcional)

Con el agente de SSH de Windows, la frase se escribe una vez por sesión:

```powershell
# Una sola vez, como administrador:
Set-Service ssh-agent -StartupType Manual
# Cada sesión, como usuario normal:
Start-Service ssh-agent
ssh-add "$env:USERPROFILE\.ssh\oracle_manualidades"
```

## Pruebas del script

`instalar-remoto.sh` se ensayó en un Ubuntu desechable (Docker) con un
`systemctl` simulado: despliegue bueno, versión nueva rota (vuelve sola),
paquete dañado y respaldo previo fallido. En todos los casos `admin.json` y
`media/` quedaron intactos.
