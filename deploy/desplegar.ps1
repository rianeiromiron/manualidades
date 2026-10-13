<#
.SYNOPSIS
  Despliega manualidades en el servidor de Oracle (ver deploy/DESPLIEGUE.md).

.DESCRIPTION
  1. Comprueba que no haya cambios sin commit.
  2. Ejecuta las pruebas (go vet y go test).
  3. Compila para Linux ARM64 y empaqueta el ejecutable y la carpeta web/.
  4. Sube el paquete e instala (deploy/instalar-remoto.sh): respaldo previo,
     copia de la versión actual, reinicio y comprobación. Si la versión nueva
     no responde, el servidor vuelve solo a la anterior.
  5. Comprueba la dirección pública.

  Pide la frase de la llave SSH dos veces (subir e instalar). Nunca toca
  config.json, admin.json ni media/ del servidor.

.EXAMPLE
  .\deploy\desplegar.ps1

.EXAMPLE
  .\deploy\desplegar.ps1 -SoloPaquete -SinPruebas   # compila y empaqueta, sin subir nada
#>
[CmdletBinding()]
param(
    [string]$Servidor = "ubuntu@160.34.210.207",
    [string]$Llave = (Join-Path $env:USERPROFILE ".ssh\oracle_manualidades"),
    [string]$UrlPublica = "https://prueba.manualidadescristal.com",
    # Salta go vet y go test.
    [switch]$SinPruebas,
    # Compila y empaqueta, pero no sube nada al servidor.
    [switch]$SoloPaquete,
    # Permite desplegar aunque haya cambios sin commit.
    [switch]$Forzar
)

$ErrorActionPreference = "Stop"
$raiz = Split-Path -Parent $PSScriptRoot
Set-Location $raiz

function Paso([string]$texto) { Write-Host "`n== $texto" -ForegroundColor Cyan }

# Ejecuta un programa externo y falla si termina con error. En PowerShell 5.1,
# lo que un programa escribe por stderr (avisos de ssh, por ejemplo) se
# convierte en error con $ErrorActionPreference = "Stop"; por eso se relaja
# solo mientras corre y se mira el código de salida.
function Nativo([scriptblock]$bloque, [string]$mensaje) {
    $previo = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try { & $bloque } finally { $ErrorActionPreference = $previo }
    if ($LASTEXITCODE -ne 0) { throw $mensaje }
}

foreach ($programa in "git", "go", "ssh", "scp") {
    if (-not (Get-Command $programa -ErrorAction SilentlyContinue)) { throw "No encuentro '$programa' en el PATH." }
}
# El tar de Windows (bsdtar). Si en el PATH va antes el de Git for Windows (GNU),
# trata "C:\..." como si fuera un servidor remoto y falla.
$tar = Join-Path $env:SystemRoot "System32\tar.exe"
if (-not (Test-Path $tar)) { throw "No encuentro el tar de Windows en $tar." }
if (-not $SoloPaquete -and -not (Test-Path $Llave)) { throw "No existe la llave SSH: $Llave" }

# 1. Estado del repositorio -------------------------------------------------
Paso "Revisando el repositorio"
$sucio = git status --porcelain --untracked-files=no
if ($sucio -and -not $Forzar) {
    Write-Host ($sucio -join "`n")
    throw "Hay cambios sin commit (arriba). Haz commit primero, o usa -Forzar para desplegar de todos modos."
}
$commit = (git rev-parse --short HEAD).Trim()
$sinSubir = [int](git rev-list --count "origin/master..HEAD" 2>$null)
Write-Host "Commit a desplegar: $commit"
if ($sucio) { Write-Host "AVISO: se despliega el contenido actual del disco, que incluye cambios sin commit." -ForegroundColor Yellow }
if ($sinSubir -gt 0) { Write-Host "AVISO: hay $sinSubir commit(s) que aún no están en GitHub." -ForegroundColor Yellow }

# 2. Pruebas ------------------------------------------------------------------
if ($SinPruebas) {
    Write-Host "`n(pruebas omitidas por -SinPruebas)" -ForegroundColor Yellow
} else {
    Paso "Ejecutando las pruebas (tardan un par de minutos; necesitan Docker y Chrome)"
    Nativo { go vet ./... } "go vet encontró problemas."
    Nativo { go test ./... } "Las pruebas fallaron: no se despliega."
}

# 3. Compilar y empaquetar ----------------------------------------------------
Paso "Compilando para Linux ARM64"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("manualidades-deploy-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
$contenido = Join-Path $tmp "contenido"
New-Item -ItemType Directory -Force $contenido | Out-Null
$env:GOOS = "linux"; $env:GOARCH = "arm64"; $env:CGO_ENABLED = "0"
try {
    Nativo { go build -trimpath -ldflags "-s -w" -o (Join-Path $contenido "manualidades") ./cmd/server } "La compilación falló."
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
}

# Comprobar que es de verdad un ejecutable Linux ARM64 (ELF, e_machine 183).
$cabecera = [IO.File]::ReadAllBytes((Join-Path $contenido "manualidades"))[0..19]
if ($cabecera[0] -ne 0x7F -or $cabecera[1] -ne 0x45 -or $cabecera[18] -ne 183) { throw "El ejecutable no es Linux ARM64." }

Copy-Item -Recurse (Join-Path $raiz "web") (Join-Path $contenido "web")
Copy-Item (Join-Path $raiz "deploy\instalar-remoto.sh") (Join-Path $contenido "instalar-remoto.sh")

Paso "Empaquetando"
$paquete = Join-Path $tmp "despliegue.tar.gz"
Nativo { & $tar -czf $paquete -C $contenido manualidades web instalar-remoto.sh } "No se pudo crear el paquete."
$sha = (Get-FileHash $paquete -Algorithm SHA256).Hash.ToLower()
"{0}  ({1:N1} MB, sha256 {2}...)" -f $paquete, ((Get-Item $paquete).Length / 1MB), $sha.Substring(0, 16)

if ($SoloPaquete) {
    Write-Host "`nListo (-SoloPaquete): no se subió nada. El paquete queda en $paquete" -ForegroundColor Green
    return
}

# 4. Subir e instalar -------------------------------------------------------
try {
    Paso "Subiendo al servidor (te pedirá la frase de la llave)"
    Nativo { scp -i $Llave $paquete "${Servidor}:/tmp/despliegue.tar.gz" } "No se pudo subir el paquete."

    Paso "Instalando en el servidor (te volverá a pedir la frase)"
    $remoto = 'rm -rf /tmp/despliegue && mkdir /tmp/despliegue && tar -xzf /tmp/despliegue.tar.gz -C /tmp/despliegue && sed -i ''s/\r$//'' /tmp/despliegue/instalar-remoto.sh && sudo bash /tmp/despliegue/instalar-remoto.sh ' + $sha
    Nativo { ssh -i $Llave $Servidor $remoto } "El servidor no pudo completar la instalación (ver mensajes de arriba). Si decía que volvió a la versión anterior, la tienda sigue como estaba."
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

# 5. Comprobación desde fuera ---------------------------------------------
Paso "Comprobando $UrlPublica"
$mal = $false
foreach ($ruta in "/", "/admin/login") {
    try {
        $r = Invoke-WebRequest -Uri ($UrlPublica + $ruta) -UseBasicParsing -TimeoutSec 30
        Write-Host ("  {0,-14} HTTP {1}" -f $ruta, $r.StatusCode)
        if ($r.StatusCode -ne 200) { $mal = $true }
    } catch {
        Write-Host ("  {0,-14} ERROR: {1}" -f $ruta, $_.Exception.Message) -ForegroundColor Red
        $mal = $true
    }
}
if ($mal) { throw "La dirección pública no responde bien. Revisa: ssh -i `"$Llave`" $Servidor `"sudo journalctl -u manualidades -n 50 --no-pager`"" }

Write-Host "`nDesplegado el commit $commit." -ForegroundColor Green
Write-Host "Si algo se ve mal, la versión anterior queda en el servidor (manualidades.anterior y web.anterior; ver deploy/DESPLIEGUE.md)."
