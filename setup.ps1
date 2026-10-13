[CmdletBinding()]
param (
    [string]$BinDir,
    [string]$ModelsDir,
    [string]$ModelName = "ggml-base.bin"
)

$ErrorActionPreference = "Stop"

$ScriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { (Get-Location).Path }
if (-not $BinDir) { $BinDir = Join-Path $ScriptDir "bin" }
if (-not $ModelsDir) { $ModelsDir = Join-Path $ScriptDir "models" }

# Ensure target directories exist
if (-not (Test-Path $BinDir)) {
    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
}
if (-not (Test-Path $ModelsDir)) {
    New-Item -ItemType Directory -Path $ModelsDir -Force | Out-Null
}

Write-Host "Fetching latest whisper.cpp release metadata from GitHub..." -ForegroundColor Cyan
$ApiUrl = "https://api.github.com/repos/ggml-org/whisper.cpp/releases/latest"

try {
    $Release = Invoke-RestMethod -Uri $ApiUrl -Headers @{ "User-Agent" = "PowerShell-Downloader" }
} catch {
    Write-Error "Failed to fetch release metadata: $_"
}

# If latest tag has no assets attached, fallback to most recent release with binary assets
if (-not $Release.assets -or $Release.assets.Count -eq 0) {
    Write-Host "Release $($Release.tag_name) has no binary assets. Checking recent releases..." -ForegroundColor Yellow
    $Releases = Invoke-RestMethod -Uri "https://api.github.com/repos/ggml-org/whisper.cpp/releases" -Headers @{ "User-Agent" = "PowerShell-Downloader" }
    $Release = $Releases | Where-Object { $_.assets.Count -gt 0 } | Select-Object -First 1
}

# Locate Windows CUDA x64 zip asset (prioritizing x64 CUDA and excluding ARM64)
$CudaAsset = $Release.assets | Where-Object { 
    ($_.name -match "cuda.*x64.*\.zip$" -or $_.name -match "win.*cuda.*x64.*\.zip$") -and ($_.name -notmatch "arm64")
} | Sort-Object name -Descending | Select-Object -First 1

if (-not $CudaAsset) {
    Write-Warning "No explicit CUDA archive matched. Falling back to default Windows x64 binary archive..."
    $CudaAsset = $Release.assets | Where-Object { 
        $_.name -match "bin-x64.*\.zip$" 
    } | Select-Object -First 1
}

if (-not $CudaAsset) {
    Write-Error "Could not find a compatible Windows prebuilt archive in the release."
}

$ZipPath = Join-Path $env:TEMP $CudaAsset.name
Write-Host "Downloading $($CudaAsset.name) from release $($Release.tag_name)..." -ForegroundColor Green
Invoke-WebRequest -Uri $CudaAsset.browser_download_url -OutFile $ZipPath

Write-Host "Extracting binaries into $BinDir..." -ForegroundColor Green
Expand-Archive -Path $ZipPath -DestinationPath $BinDir -Force
Remove-Item -Path $ZipPath -Force

# If extracted files are inside a Release/ subfolder, move them directly to $BinDir
$ReleaseSubdir = Join-Path $BinDir "Release"
if (Test-Path $ReleaseSubdir) {
    Get-ChildItem -Path (Join-Path $ReleaseSubdir "*") | Move-Item -Destination $BinDir -Force
    Remove-Item -Path $ReleaseSubdir -Recurse -Force
}

# Standardize server executable name for the Go runner
$ServerExe = Join-Path $BinDir "whisper-server.exe"
$AltServerExe = Join-Path $BinDir "server.exe"
if (-not (Test-Path $ServerExe) -and (Test-Path $AltServerExe)) {
    Move-Item -Path $AltServerExe -Destination $ServerExe -Force
}

# Download model weights from Hugging Face
$ModelTarget = Join-Path $ModelsDir $ModelName
if (-not (Test-Path $ModelTarget)) {
    $ModelUrl = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/$ModelName"
    Write-Host "Downloading Whisper model ($ModelName) from Hugging Face..." -ForegroundColor Green
    Invoke-WebRequest -Uri $ModelUrl -OutFile $ModelTarget
} else {
    Write-Host "Model $ModelName already exists in $ModelsDir, skipping download." -ForegroundColor Yellow
}

Write-Host "`nSetup complete. Assets ready in:" -ForegroundColor Cyan
Write-Host "Binaries: $BinDir"
Write-Host "Models:   $ModelsDir"

Get-ChildItem -Path $BinDir | Format-Table Name, Length
