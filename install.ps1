# install.ps1 — pasang binary avior tanpa Go (Windows).
#
# Usage (PowerShell):
#   irm https://github.com/maleolabs/avior/releases/latest/download/install.ps1 | iex
#   & install.ps1 -Version v1.2.3 -To "$env:USERPROFILE\.local\bin"
#
# Unduh binary CLI untuk OS/arsitektur saat ini dari GitHub Release,
# verifikasi checksum SHA256SUMS, lalu pasang ke target direktori.
# AVIOR_RELEASE_BASE_URL mengarahkan ke mirror (mis. GitHub Enterprise).
# AVIOR_INSTALL_INSECURE=y mematikan verifikasi TLS — hanya untuk test
# server self-signed; jangan dipakai di instalasi nyata.
[CmdletBinding()]
param(
    [string]$Version = "latest",
    [string]$To = "$env:USERPROFILE\.local\bin"
)

$ErrorActionPreference = "Stop"
$Repo = "maleolabs/avior"
$BaseUrl = if ($env:AVIOR_RELEASE_BASE_URL) { $env:AVIOR_RELEASE_BASE_URL } else { "https://github.com" }
# AVIOR_INSTALL_INSECURE=y mematikan verifikasi TLS — hanya untuk test
# server self-signed; jangan dipakai di instalasi nyata. PS 7 (CoreCLR)
# andal -SkipCertificateCheck; PS 5.1 (.NET Framework) butuh
# ServicePointManager policy (type tidak ada di CoreCLR — di-skip).
$SkipCert = $env:AVIOR_INSTALL_INSECURE -eq "y"
if ($SkipCert) {
    try {
        [ServicePointManager]::ServerCertificateValidationCallback = { $true }
    } catch { }
}

# ── UI ───────────────────────────────────────────────────────────────
# Palette mengikuti internal/ui CLI (primary biru, success hijau, danger
# merah). NO_COLOR / non-interaktif = monokrom otomatis. escape char via
# [char]27 supaya kompatibel Windows PowerShell 5.1 (backtick-e baru
# ada di PowerShell 6+).
$NoColor = -not [string]::IsNullOrEmpty($env:NO_COLOR) -or -not $Host.UI.SupportsVirtualTerminal
if ($NoColor) {
    $CPrimary = ""; $CSuccess = ""; $CDanger = ""; $CInfo = ""; $CDim = ""; $CReset = ""
} else {
    $esc = [char]27
    $CPrimary = "$esc[94m"; $CSuccess = "$esc[92m"; $CDanger = "$esc[91m"
    $CInfo = "$esc[96m"; $CDim = "$esc[90m"; $CReset = "$esc[0m"
}

function Write-BoxTop($title) {
    Write-Host "${CPrimary}╭─ ${CReset}${CSuccess}${title}${CReset}"
}
function Write-BoxLine($line) {
    Write-Host "${CPrimary}│${CReset} $line"
}
function Write-BoxBottom {
    Write-Host "${CPrimary}╰─${CReset}"
}
function Write-Ok($msg) {
    Write-Host "  ${CSuccess}✓${CReset} $msg"
}
function Die($msg) {
    Write-Host "  ${CDanger}✗${CReset} $msg"
    exit 1
}

# ── Platform ─────────────────────────────────────────────────────────
$os = "windows"
$arch = if ([Environment]::Is64BitProcess) {
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
} else {
    Die "proses 32-bit tidak didukung (hanya amd64/arm64)"
}
$asset = "avior_${os}_${arch}.exe"

# ── Header ───────────────────────────────────────────────────────────
Write-Host ""
Write-BoxTop "avior installer"
Write-BoxLine "${CDim}${os}/${arch}${CReset}"
Write-BoxBottom

# ── URL ──────────────────────────────────────────────────────────────
$base = if ($Version -eq "latest") {
    "$($BaseUrl.TrimEnd('/'))/$Repo/releases/latest/download"
} else {
    "$($BaseUrl.TrimEnd('/'))/$Repo/releases/download/$Version"
}

$tmp = New-Item -ItemType Directory -Path (Join-Path $env:TEMP "avior-install-$(Get-Random)") -Force
try {
    # ── Unduh + progress bar ─────────────────────────────────────────
    function Download-File($url, $dest, $label) {
        # Splat kompatibilitas: -SkipCertificateCheck baru ada PS 7;
        # PS 5.1 andal pada ServicePointManager policy di atas.
        $iwrSplat = @{ Uri = $url; UseBasicParsing = $true }
        if ($SkipCert) { $iwrSplat.SkipCertificateCheck = $true }
        try {
            $response = Invoke-WebRequest @iwrSplat -Method Head
            $size = [int64]$response.Headers["Content-Length"]
        } catch {
            $size = 0
        }
        # Write-Progress native bar; ukuran file sementara dipantau
        # selama job unduh berjalan.
        $job = Start-Job -ScriptBlock {
            param($u, $d, $skipCert)
            $splat = @{ Uri = $u; OutFile = $d; UseBasicParsing = $true }
            if ($skipCert) { $splat.SkipCertificateCheck = $true }
            Invoke-WebRequest @splat
        } -ArgumentList $url, $dest, $SkipCert
        if ($size -gt 0) {
            while ($job.State -eq "Running") {
                $have = if (Test-Path $dest) { (Get-Item $dest).Length } else { 0 }
                $pct = [math]::Min(100, [int]($have * 100 / $size))
                Write-Progress -Activity "Mengunduh $label" -PercentComplete $pct
                Start-Sleep -Milliseconds 200
            }
        }
        Wait-Job $job | Out-Null
        if ($job.State -ne "Completed") {
            $jobError = $job.ChildJobs[0].JobStateInfo.Reason
            Remove-Job $job -Force
            Die "gagal mengunduh ${label}: $jobError"
        }
        Remove-Job $job
        if (-not (Test-Path $dest)) { Die "gagal mengunduh $label" }
    }

    Write-Host ""
    Download-File "$base/SHA256SUMS" (Join-Path $tmp "SHA256SUMS") "SHA256SUMS"
    Download-File "$base/$asset" (Join-Path $tmp $asset) "binary avior"
    Write-Progress -Activity "Mengunduh" -Completed

    # ── Verifikasi checksum ──────────────────────────────────────────
    # Entry $asset harus ada di SHA256SUMS; mismatch = berhenti total,
    # tidak memasang binary yang tidak terverifikasi.
    $sums = Get-Content (Join-Path $tmp "SHA256SUMS")
    $expected = ($sums | Where-Object { $_ -match "\s$asset$" } | ForEach-Object { ($_ -split "\s+")[0] } | Select-Object -First 1)
    if (-not $expected) { Die "checksum untuk $asset tidak ada di SHA256SUMS" }
    $actual = (Get-FileHash -Path (Join-Path $tmp $asset) -Algorithm SHA256).Hash.ToLower()
    if ($expected -ne $actual) { Die "checksum tidak cocok: file rusak/diubah" }

    # ── Pasang ───────────────────────────────────────────────────────
    New-Item -ItemType Directory -Force -Path $To | Out-Null
    # Move-Item cross-volume menjadi copy+delete; atribut read-only bisa
    # membuat delete gagal — bersihkan target dulu.
    $dest = Join-Path $To "avior.exe"
    if (Test-Path $dest) { Remove-Item $dest -Force }
    Move-Item -Force (Join-Path $tmp $asset) $dest

    Write-Host ""
    Write-Ok "avior terpasang di $(Join-Path $To 'avior.exe')"
    $cmd = Get-Command avior -ErrorAction SilentlyContinue
    if ($cmd) {
        Write-BoxTop "siap digunakan"
        Write-BoxLine "${CDim}avior --help${CReset}"
        Write-BoxBottom
    } else {
        Write-BoxTop "tambahkan ke PATH"
        Write-BoxLine "${CDim}$To belum di PATH — buka Environment Variables atau:${CReset}"
        Write-BoxLine "${CDim}[Environment]::SetEnvironmentVariable('PATH', `"$To;`$([Environment]::GetEnvironmentVariable('PATH', 'User'))`", 'User')${CReset}"
        Write-BoxBottom
    }
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
