# Builds the DRS Windows agent.
#
# CGO is mandatory: the VP8 encoder is a libvpx binding, and a CGO_ENABLED=0 build
# still compiles but silently produces a binary that cannot encode video. The checks at
# the end exist so that failure is caught here rather than discovered when a session
# refuses to start on a user's machine.
#
# Requires MSYS2 with: pacman -S mingw-w64-x86_64-gcc mingw-w64-x86_64-libvpx mingw-w64-x86_64-pkgconf

[CmdletBinding()]
param(
    [string]$MingwBin = 'C:\msys64\mingw64\bin',
    # Defaulted in the body, not here: $PSScriptRoot is EMPTY while a param block's
    # defaults are evaluated under `powershell -File script.ps1`, though it is populated
    # everywhere else (`& .\script.ps1`, `powershell -Command`, and the script body). A
    # `Join-Path $PSScriptRoot` default therefore fails with "Cannot bind argument to
    # parameter 'Path' because it is an empty string" for exactly the invocation the
    # README recommends, and works when you test it from an open session.
    [string]$OutDir,
    [string]$Name     = 'drs-agent.exe'
)

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

if (-not $OutDir) { $OutDir = Join-Path $PSScriptRoot 'build' }

if (-not (Test-Path (Join-Path $MingwBin 'gcc.exe'))) {
    throw "gcc not found in $MingwBin. Install MSYS2 and the mingw-w64 toolchain, or pass -MingwBin."
}

$env:PATH = "$MingwBin;$env:PATH"
$env:CGO_ENABLED = '1'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'

if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir | Out-Null }
$exe = Join-Path $OutDir $Name

Write-Host 'Building agent...' -ForegroundColor Cyan

# Symbols are deliberately NOT stripped (-s -w): the vpx_codec check below reads them,
# and they make a crash report from a user's machine actionable.
# -H windowsgui       no console window when the user double-clicks it
# -linkmode external  hand linking to gcc, which CGO requires
# -extldflags -static statically link libvpx so no DLL has to ship alongside
go build -trimpath -ldflags '-H windowsgui -linkmode external -extldflags -static' -o $exe ./cmd/agent
if ($LASTEXITCODE -ne 0) { throw 'go build failed' }

Write-Host "Built $exe" -ForegroundColor Green

# --- Verification ---------------------------------------------------------------

Write-Host 'Verifying VP8 encoder is linked in...' -ForegroundColor Cyan
$symbols = & go tool nm $exe 2>$null | Select-String -Pattern 'vpx_codec' -SimpleMatch
if (-not $symbols) {
    Remove-Item $exe -Force
    throw 'No vpx_codec symbols found: this binary cannot encode video. Check that CGO is enabled and libvpx is installed.'
}
Write-Host "  libvpx symbols present ($($symbols.Count) matches)" -ForegroundColor Green

Write-Host 'Verifying no console window...' -ForegroundColor Cyan
# The PE subsystem byte lives at the offset named by the PE header pointer at 0x3C.
# 2 = GUI, 3 = console.
$bytes = [System.IO.File]::ReadAllBytes($exe)
$peOffset = [System.BitConverter]::ToInt32($bytes, 0x3C)
$subsystem = [System.BitConverter]::ToUInt16($bytes, $peOffset + 0x5C)
if ($subsystem -ne 2) {
    throw "PE subsystem is $subsystem, expected 2 (GUI). The agent would open a console window on every login."
}
Write-Host '  subsystem = GUI' -ForegroundColor Green

Write-Host 'Verifying libvpx is statically linked...' -ForegroundColor Cyan
$objdump = Join-Path $MingwBin 'objdump.exe'
if (Test-Path $objdump) {
    $dllRefs = & $objdump -p $exe | Select-String -Pattern 'DLL Name:\s*libvpx'
    if ($dllRefs) {
        throw "The binary depends on an external libvpx DLL: $dllRefs. It will not run on a machine without MSYS2."
    }
    Write-Host '  no libvpx DLL dependency' -ForegroundColor Green
} else {
    Write-Host '  objdump not found, skipping DLL check' -ForegroundColor Yellow
}

$sizeMB = [math]::Round((Get-Item $exe).Length / 1MB, 1)
Write-Host ''
Write-Host "Agent ready: $exe ($sizeMB MB)" -ForegroundColor Green
Write-Host 'Enroll it with:' -ForegroundColor Gray
Write-Host "  $Name enroll -server http://localhost:8080 -token DRS-XXXXXX" -ForegroundColor Gray
