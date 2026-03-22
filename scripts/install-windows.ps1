param(
    [string]$Version = "latest",
    [string]$Repo = "aurokin/clip-remote-sync",
    [string]$InstallPath = "C:\Program Files\clip-remote-sync\crs.exe",
    [string]$BridgeDir = "C:\ProgramData\clip-remote-sync",
    [string]$DownloadDir = "$env:TEMP\clip-remote-sync"
)

$ErrorActionPreference = "Stop"

function Get-ReleaseBaseUrl {
    param([string]$Repo, [string]$Version)
    if ($Version -eq "latest") {
        return "https://github.com/$Repo/releases/latest/download"
    }
    return "https://github.com/$Repo/releases/download/$Version"
}

function Get-ExpectedSha256 {
    param([string]$ChecksumsPath, [string]$AssetName)
    $line = Select-String -Path $ChecksumsPath -Pattern ("\s{0}$" -f [regex]::Escape($AssetName)) | Select-Object -First 1
    if (-not $line) {
        throw "Missing checksum entry for $AssetName in $ChecksumsPath"
    }
    return ($line.Line -split '\s+')[0].ToLowerInvariant()
}

$assetName = "crs-windows-amd64.exe"
$baseUrl = Get-ReleaseBaseUrl -Repo $Repo -Version $Version
$assetUrl = "$baseUrl/$assetName"
$checksumsUrl = "$baseUrl/SHA256SUMS"

New-Item -ItemType Directory -Force $DownloadDir | Out-Null
$assetPath = Join-Path $DownloadDir $assetName
$checksumsPath = Join-Path $DownloadDir "SHA256SUMS"

Invoke-WebRequest $assetUrl -OutFile $assetPath
Invoke-WebRequest $checksumsUrl -OutFile $checksumsPath

$expectedHash = Get-ExpectedSha256 -ChecksumsPath $checksumsPath -AssetName $assetName
$actualHash = (Get-FileHash -Algorithm SHA256 $assetPath).Hash.ToLowerInvariant()
if ($actualHash -ne $expectedHash) {
    throw "SHA256 mismatch for $assetName. Expected $expectedHash but got $actualHash"
}

$installDir = Split-Path -Parent $InstallPath
New-Item -ItemType Directory -Force $installDir | Out-Null
New-Item -ItemType Directory -Force (Join-Path $BridgeDir "requests") | Out-Null
New-Item -ItemType Directory -Force (Join-Path $BridgeDir "results") | Out-Null
Copy-Item $assetPath $InstallPath -Force

Write-Host "Installed $InstallPath"
Write-Host "SHA256: $actualHash"
Write-Host "BridgeDir: $BridgeDir"
