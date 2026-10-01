# One-click update of the mirror server.
#
# Downloads the latest stable + long-term RouterOS packages on THIS machine (through
# the proxy below) and uploads them to the mirror server over SSH/SFTP. The server never
# downloads anything itself. Run it whenever MikroTik releases a new version; it skips a
# channel whose version on the server is already the latest.
#
#   .\push-mirror.ps1                 update both channels
#   .\push-mirror.ps1 -Force          re-download and re-upload even if up to date
#   .\push-mirror.ps1 -Channels stable
#
# Needs: an SSH key that logs in to the server without a password, and either Go
# installed (the tool is rebuilt each run) or an existing mirror-push.exe.

param(
    [string[]]$Channels = @('stable', 'long-term'),
    [string]$Arches = '',          # empty = all architectures; a subset REPLACES the server dir
    [string]$RemoteDir = '',       # override for testing; default is $Config.RemoteDir
    [switch]$Force
)

$ErrorActionPreference = 'Stop'

# ---- settings: edit here -------------------------------------------------------------
$Config = @{
    Host      = 'mirror.example.com'
    User      = 'root'
    RemoteDir = '/opt/mikrotik-mirror/packages'
    Proxy     = 'http://127.0.0.1:7890'   # '' = no proxy (HTTP_PROXY/HTTPS_PROXY still honoured)
}
# --------------------------------------------------------------------------------------

# scripts live in scripts/; build and run from the repository root
$Root = Split-Path -Parent $PSScriptRoot
Set-Location -Path $Root
if ($RemoteDir -eq '') { $RemoteDir = $Config.RemoteDir }

# Build the tool (fast) so it always matches the source; fall back to an existing exe.
$goExe = $null
$found = Get-Command go -ErrorAction SilentlyContinue
if ($found) { $goExe = $found.Source }
elseif (Test-Path 'C:\Program Files\Go\bin\go.exe') { $goExe = 'C:\Program Files\Go\bin\go.exe' }
$exe = Join-Path $Root 'mirror-push.exe'
if ($goExe) {
    Write-Host '== building mirror-push'
    & $goExe build -o $exe ./cmd/mirror-push
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
} elseif (-not (Test-Path $exe)) {
    throw 'Go is not installed and mirror-push.exe does not exist. Install Go from https://go.dev/dl/'
}

$argsList = @(
    '--host', $Config.Host,
    '--user', $Config.User,
    '--remote-dir', $RemoteDir,
    '--channels', ($Channels -join ',')
)
if ($Config.Proxy -ne '') { $argsList += @('--proxy', $Config.Proxy) }
if ($Arches -ne '')       { $argsList += @('--arches', $Arches) }
if ($Force)               { $argsList += '--force' }

Write-Host "== pushing to $($Config.User)@$($Config.Host):$RemoteDir  (channels: $($Channels -join ', '))"
& $exe @argsList
$code = $LASTEXITCODE
if ($code -ne 0) {
    Write-Host "mirror-push failed with exit code $code" -ForegroundColor Red
    exit $code
}

# Trust but verify: ask the server what is really there.
$ssh = Get-Command ssh -ErrorAction SilentlyContinue
if ($ssh -and $RemoteDir.StartsWith('/')) {
    Write-Host '== server contents'
    $remote = @'
for c in __CH__; do d=__DIR__/$c; echo "$c: version=$(cat $d/.version 2>/dev/null) packages=$(ls $d/*.npk 2>/dev/null | wc -l) main-packages=$(ls $d 2>/dev/null | grep -c '^routeros-')"; done
'@
    $remote = $remote.Replace('__DIR__', $RemoteDir).Replace('__CH__', ($Channels -join ' '))
    try { & $ssh.Source "$($Config.User)@$($Config.Host)" $remote } catch { Write-Host "(could not verify over ssh: $_)" }
}
Write-Host '== done' -ForegroundColor Green
