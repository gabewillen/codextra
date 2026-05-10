$ErrorActionPreference = "Stop"

$Repo = "gabewillen/codextra"
$Binary = "codextra"

function Test-Codex {
    if ($env:CODEXTRA_CODEX_BIN) {
        $command = Get-Command $env:CODEXTRA_CODEX_BIN -ErrorAction SilentlyContinue
        if ($command) {
            return
        }
        throw "CODEXTRA_CODEX_BIN does not point to an executable codex binary: $env:CODEXTRA_CODEX_BIN"
    }

    if (Get-Command codex -ErrorAction SilentlyContinue) {
        return
    }

    throw "codex is not installed or is not on PATH. Install OpenAI Codex first, or set CODEXTRA_CODEX_BIN."
}

function Get-LatestVersion {
    $response = Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest"
    $releaseUri = $null
    if ($response.BaseResponse.ResponseUri) {
        $releaseUri = $response.BaseResponse.ResponseUri
    }
    elseif ($response.BaseResponse.RequestMessage -and $response.BaseResponse.RequestMessage.RequestUri) {
        $releaseUri = $response.BaseResponse.RequestMessage.RequestUri
    }
    if (-not $releaseUri) {
        throw "could not resolve latest release"
    }
    $tag = $releaseUri.Segments[$releaseUri.Segments.Length - 1].Trim("/")
    if (-not $tag -or $tag -eq "latest") {
        throw "could not resolve latest release"
    }
    return $tag.TrimStart("v")
}

function Get-Arch {
    switch ($env:PROCESSOR_ARCHITECTURE) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { throw "unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
    }
}

function Get-InstallDir {
    if ($env:INSTALL_DIR) {
        return $env:INSTALL_DIR
    }

    $candidates = @(
        Join-Path $HOME ".local\bin",
        Join-Path $HOME "bin"
    )
    foreach ($dir in $candidates) {
        if ((Test-Path $dir) -and (Test-Path $dir -PathType Container)) {
            return $dir
        }
    }
    return $candidates[0]
}

Test-Codex

$Version = if ($env:VERSION) { $env:VERSION } else { Get-LatestVersion }
$Arch = Get-Arch
$InstallDir = Get-InstallDir
$Archive = "${Binary}_${Version}_windows_${Arch}.tar.gz"
$Url = "https://github.com/$Repo/releases/download/v$Version/$Archive"
$Temp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())

New-Item -ItemType Directory -Force -Path $Temp | Out-Null
try {
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $ArchivePath = Join-Path $Temp $Archive
    Invoke-WebRequest -Uri $Url -OutFile $ArchivePath
    tar -xzf $ArchivePath -C $Temp "$Binary.exe"
    Copy-Item -Force (Join-Path $Temp "$Binary.exe") (Join-Path $InstallDir "$Binary.exe")

    Write-Host "installed $Binary $Version to $(Join-Path $InstallDir "$Binary.exe")"

    $pathEntries = $env:PATH -split ";"
    if ($pathEntries -notcontains $InstallDir) {
        Write-Warning "$InstallDir is not on PATH. Add it to your user PATH to run codextra from any shell."
    }
}
finally {
    Remove-Item -Recurse -Force $Temp -ErrorAction SilentlyContinue
}
