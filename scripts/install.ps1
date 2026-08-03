# One-line installer for nodered-mcp (Windows).
#
#   irm https://raw.githubusercontent.com/fgjcarlos/nodered-mcp/main/scripts/install.ps1 | iex
#
# Downloads the right prebuilt binary from GitHub Releases into
# %LOCALAPPDATA%\Programs\nodered-mcp, adds it to your user PATH, then points
# you at `nodered-mcp init --write`.
#
# NOTE: requires the repo to be published with a Release (see issues/301).
# Until then there is nothing to download and this script will 404.
$ErrorActionPreference = "Stop"

$repo = "fgjcarlos/nodered-mcp"
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$asset = "nodered-mcp_windows_$arch.zip"
$url = "https://github.com/$repo/releases/latest/download/$asset"

$dest = "$env:LOCALAPPDATA\Programs\nodered-mcp"
New-Item -ItemType Directory -Force -Path $dest | Out-Null

$zip = Join-Path $env:TEMP "nodered-mcp.zip"
Write-Host "downloading $url"
Invoke-WebRequest -Uri $url -OutFile $zip
Expand-Archive -Path $zip -DestinationPath $dest -Force
Remove-Item $zip

Write-Host "installed: $dest\nodered-mcp.exe"

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$dest*") {
	[Environment]::SetEnvironmentVariable("Path", "$userPath;$dest", "User")
	Write-Host "added $dest to your PATH (open a new terminal to pick it up)"
}
Write-Host "next: nodered-mcp init --write"
