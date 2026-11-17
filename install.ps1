$ErrorActionPreference = "Stop"

$REPO = "ankushT369/gossh"
$BINARY = "gossh.exe"
$ASSET = "gossh-windows-amd64.exe"

$INSTALL_DIR = Join-Path $HOME ".local\bin"
$URL = "https://github.com/$REPO/releases/latest/download/$ASSET"
$DEST = Join-Path $INSTALL_DIR $BINARY

Write-Host "[-] Installing gossh"

# Check architecture
if (-not [Environment]::Is64BitOperatingSystem) {
    Write-Error "64-bit Windows is required."
    exit 1
}

if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
    Write-Error "Windows ARM64 is not currently supported."
    exit 1
}

New-Item -ItemType Directory -Force -Path $INSTALL_DIR | Out-Null

try {
    Invoke-WebRequest -Uri $URL -OutFile $DEST

    # Add installation directory to user PATH
    $USER_PATH = [Environment]::GetEnvironmentVariable("Path", "User")

    $PATH_ENTRIES = @($USER_PATH -split ";" | Where-Object { $_ })

    if ($PATH_ENTRIES -notcontains $INSTALL_DIR) {
        $NEW_PATH = (@($PATH_ENTRIES) + $INSTALL_DIR) -join ";"
        [Environment]::SetEnvironmentVariable("Path", $NEW_PATH, "User")
    }

    # Update PATH in current PowerShell session
    if ($env:PATH -notlike "*$INSTALL_DIR*") {
        $env:PATH += ";$INSTALL_DIR"
    }

    Write-Host "[✓] Installed gossh to $DEST" -ForegroundColor Green
    Write-Host "[✓] Run: gossh version"

} catch {
    Remove-Item $DEST -ErrorAction SilentlyContinue
    Write-Error "Installation failed: $_"
    exit 1
}
