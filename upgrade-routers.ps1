# Walk the Winbox address book and bring each router up to the mirror's version.
#
# Per router it asks once: connect? After a "y" it binds the local-update source to the mirror
# (account chosen by the router's channel), checks for updates and downloads them. At the end it
# lists the routers that need a manual reboot. -ConfirmSteps also asks before bind/download.
# It never reboots; downloaded packages install on the router's next reboot.
#
#   .\upgrade-routers.ps1                       interactive, one router at a time
#   .\upgrade-routers.ps1 -DryRun               look only, never bind or download
#   .\upgrade-routers.ps1 -Only 192.168.1.1    only entries whose address/note/group match
#   .\upgrade-routers.ps1 -Yes                  no questions, all routers (use with care)
#   .\upgrade-routers.ps1 -List                 list the address book and exit
#
# Needs: Go installed (the tool is rebuilt each run) or an existing mirror-upgrade.exe.
# Default transport is Winbox (TCP 8291, a terminal session) - the same port the address book
# already uses, so no extra service is needed. -Transport rest uses the REST API (www/www-ssl).

param(
    [string]$Only = '',
    [switch]$DryRun,
    [switch]$Yes,
    [switch]$List,
    [switch]$ConfirmSteps,
    [ValidateSet("winbox", "rest")][string]$Transport = "winbox"
)

$ErrorActionPreference = 'Stop'

# ---- settings: edit here -------------------------------------------------------------
$Config = @{
    AddressBook = 'C:\path\to\Addresses.cdb'
    Mirror      = '203.0.113.10'    # the mirror's address as the ROUTERS reach it
}
# --------------------------------------------------------------------------------------

Set-Location -Path $PSScriptRoot

$goExe = $null
$found = Get-Command go -ErrorAction SilentlyContinue
if ($found) { $goExe = $found.Source }
elseif (Test-Path 'C:\Program Files\Go\bin\go.exe') { $goExe = 'C:\Program Files\Go\bin\go.exe' }
$exe = Join-Path $PSScriptRoot 'mirror-upgrade.exe'
if ($goExe) {
    Write-Host '== 正在编译 mirror-upgrade'
    & $goExe build -o $exe ./cmd/mirror-upgrade
    if ($LASTEXITCODE -ne 0) { throw 'go build 编译失败' }
} elseif (-not (Test-Path $exe)) {
    throw '未安装 Go，也没有 mirror-upgrade.exe。请先从 https://go.dev/dl/ 安装 Go'
}

$argsList = @('--addressbook', $Config.AddressBook, '--mirror', $Config.Mirror)
if ($Only -ne '') { $argsList += @('--only', $Only) }
if ($DryRun)      { $argsList += '--dry-run' }
if ($Yes)         { $argsList += '--yes' }
if ($List)        { $argsList += '--list' }
if ($ConfirmSteps) { $argsList += '--confirm-steps' }
$argsList += @('--transport', $Transport)

& $exe @argsList
exit $LASTEXITCODE
