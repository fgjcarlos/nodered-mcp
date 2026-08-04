# scripts/cleanup-npm-shims.ps1 — idempotent cleanup of orphaned
# npm wrapper launcher files on Windows.
#
# Background: versions of @fgjcarlos/nodered-mcp before 0.5.16
# (issue #192) left three launcher files in %APPDATA%\npm\ on a
# failed install:
#
#   nodered-mcp         (POSIX shim — no extension)
#   nodered-mcp.cmd     (cmd.exe shim)
#   nodered-mcp.ps1     (PowerShell shim)
#
# Those files are not removed by `npm uninstall -g`. They shadow
# any later install made via scripts/install.ps1 and produce the
# "binary not found" error from bin/nodered-mcp.js.
#
# After 0.5.16 fresh installs do not reproduce this; this script
# is for users who inherited the shims from a pre-0.5.16 install.
# Safe to re-run: every Remove-Item uses -ErrorAction SilentlyContinue
# so a missing file is not an error.
#
# Run from any PowerShell prompt:
#
#   .\scripts\cleanup-npm-shims.ps1
#
# The script does not require admin and does not modify PATH.

$ErrorActionPreference = 'Continue'

# Resolve %APPDATA%\npm\ whether the variable is set or not.
if (-not $env:APPDATA) {
    $env:APPDATA = Join-Path $env:USERPROFILE 'AppData\Roaming'
}
$npmBinDir = Join-Path $env:APPDATA 'npm'

Write-Host "Cleaning orphaned nodered-mcp launchers in $npmBinDir ..."

$targets = @(
    (Join-Path $npmBinDir 'nodered-mcp'),
    (Join-Path $npmBinDir 'nodered-mcp.cmd'),
    (Join-Path $npmBinDir 'nodered-mcp.ps1'),
)

foreach ($file in $targets) {
    if (Test-Path $file) {
        try {
            Remove-Item -LiteralPath $file -Force -ErrorAction SilentlyContinue
            Write-Host "  removed: $file"
        } catch {
            Write-Warning "  failed to remove $file : $_"
        }
    }
}

Write-Host "Done. Verify with: Get-ChildItem $npmBinDir\nodered-mcp*"