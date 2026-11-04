#!/bin/sh
# install.sh — pasang binary avior tanpa Go.
#
# Usage:
#   curl -fsSL https://github.com/maleolabs/avior/releases/latest/download/install.sh | sh
#   curl -fsSL ... | sh -s -- --version v1.2.3 --to ~/.local/bin
#
# Unduh binary CLI untuk OS/arsitektur saat ini dari GitHub Release,
# verifikasi checksum SHA256SUMS, lalu pasang ke target direktori.
# AVIOR_RELEASE_BASE_URL mengarahkan ke mirror (mis. GitHub Enterprise).
set -eu

REPO=maleolabs/avior
VERSION=latest
TO="${PREFIX:-${HOME}/.local/bin}"
BASE_URL="${AVIOR_RELEASE_BASE_URL:-https://github.com}"

# ── UI ───────────────────────────────────────────────────────────────
# Palette + kotak rounded mengikuti internal/ui CLI supaya output
# installer tidak kontras dengan CLI. NO_COLOR / non-TTY monokrom.
if [ -n "${NO_COLOR:-}" ] || [ ! -t 1 ]; then
    C_PRIMARY=''; C_SUCCESS=''; C_DANGER=''; C_INFO=''; C_DIM=''; C_RESET=''
else
    C_PRIMARY='\033[94m'; C_SUCCESS='\033[92m'; C_DANGER='\033[91m'
    C_INFO='\033[96m'; C_DIM='\033[90m'; C_RESET='\033[0m'
fi

box_top() {
    printf "${C_PRIMARY}╭─ ${C_RESET}${C_SUCCESS}%s${C_RESET}\n" "$1"
}
box_line() {
    printf "${C_PRIMARY}│${C_RESET} %s\n" "$1"
}
box_bottom() {
    printf "${C_PRIMARY}╰─${C_RESET}\n"
}
ok()   { printf "  ${C_SUCCESS}✓${C_RESET} %s\n" "$1"; }
fail() { printf "  ${C_DANGER}✗${C_RESET} %s\n" "$1" >&2; }
die()  { fail "$1"; exit 1; }

# ── Argumen ──────────────────────────────────────────────────────────
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version) VERSION="$2"; shift 2 ;;
        --to)      TO="$2"; shift 2 ;;
        *) die "argumen tidak dikenal: $1" ;;
    esac
done

# ── Platform ─────────────────────────────────────────────────────────
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in
    linux) ;;
    darwin) ;;
    *) die "OS tidak didukung: $os (hanya linux/macOS)" ;;
esac
case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "arsitektur tidak didukung: $arch (hanya amd64/arm64)" ;;
esac
asset="avior_${os}_${arch}"

# ── Header ───────────────────────────────────────────────────────────
echo ""
box_top "avior installer"
box_line "$(printf "${C_DIM}%s/%s${C_RESET}" "$os" "$arch")"
box_bottom

# ── URL ──────────────────────────────────────────────────────────────
if [ "$VERSION" = latest ]; then
    base="${BASE_URL%/}/${REPO}/releases/latest/download"
else
    base="${BASE_URL%/}/${REPO}/releases/download/${VERSION}"
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

# ── Unduh + progress bar ─────────────────────────────────────────────
# sha256sum memaksa size diketahui: HEAD dulu via -I (server wajib kirim
# Content-Length; GitHub supporter). Baris tunggal printf update.
# AVIOR_INSTALL_INSECURE=y mematikan verifikasi TLS — hanya untuk test
# server self-signed; jangan dipakai di instalasi nyata.
_insecure=""
if [ "${AVIOR_INSTALL_INSECURE:-}" = y ]; then
    _insecure="-k"
fi
download() {
    _url="$1"; _dest="$2"; _label="$3"
    _size=$(curl -fsSI $_insecure --proto =https "$_url" 2>/dev/null \
        | tr -d '\r' | awk 'tolower($1)=="content-length:"{print $2}')
    # Background curl; polling size file sementara via stat portable.
    curl -fsSL $_insecure --proto =https -o "$_dest" "$_url" &
    _pid=$!
    if [ -n "$_size" ] && [ "$_size" -gt 0 ] 2>/dev/null; then
        while kill -0 "$_pid" 2>/dev/null; do
            _have=$(_file_size "$_dest")
            pct=$(( _have * 100 / _size ))
            [ "$pct" -gt 100 ] && pct=100
            _bar=$(_render_bar "$pct")
            printf "\r  ${C_PRIMARY}%s${C_RESET} %3d%%" "$_bar" "$pct"
            sleep 0.2
        done
    fi
    wait "$_pid" || die "gagal mengunduh $_label"
    printf "\r  ${C_PRIMARY}%s${C_RESET} 100%%\n" "$(_render_bar 100)"
}

_file_size() {
    # stat portable: Linux -c%s, macOS/BSD -f%z.
    stat -c%s "$1" 2>/dev/null || stat -f%z "$1" 2>/dev/null || echo 0
}

_render_bar() {
    # Bar 20-kolom: filled #, empty -.
    _pct=$1; _filled=$(( _pct / 5 )); _empty=$(( 20 - _filled ))
    _out=""
    _i=0; while [ "$_i" -lt "$_filled" ]; do _out="#$_out"; _i=$((_i+1)); done
    _i=0; while [ "$_i" -lt "$_empty" ]; do _out="-$_out"; _i=$((_i+1)); done
    echo "[$_out]"
}

echo ""
download "${base}/SHA256SUMS" "${tmpdir}/SHA256SUMS" "SHA256SUMS"
download "${base}/${asset}" "${tmpdir}/${asset}" "binary avior"

# ── Verifikasi checksum ──────────────────────────────────────────────
# Hasil binary harus ada di SHA256SUMS. Gagal = berhenti total, tidak
# memasang binary yang tidak terverifikasi. shasum -a 256 portabel di
# macOS; sha256sum di Linux.
sha256digest() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    else
        shasum -a 256 "$1" | cut -d' ' -f1
    fi
}
expected=$(awk -v f="$asset" '$2 == f {print $1}' "${tmpdir}/SHA256SUMS")
[ -n "$expected" ] || die "checksum untuk $asset tidak ada di SHA256SUMS"
actual=$(sha256digest "${tmpdir}/${asset}")
[ "$expected" = "$actual" ] || die "checksum tidak cocok: file rusak/diubah"

# ── Pasang ───────────────────────────────────────────────────────────
mkdir -p "$TO" || die "tidak bisa membuat direktori target: $TO"
mv "${tmpdir}/${asset}" "${TO}/avior"
chmod 755 "${TO}/avior"

echo ""
ok "avior terpasang di ${TO}/avior"
if command -v avior >/dev/null 2>&1; then
    box_top "siap digunakan"
    box_line "$(printf "${C_DIM}avior --help${C_RESET}")"
    box_bottom
else
    box_top "tambahkan ke PATH"
    box_line "$(printf "${C_DIM}%s belum di PATH — restart shell atau ekspor PATH:${C_RESET}" "$TO")"
    box_line "$(printf "${C_DIM}export PATH=\"%s:\$PATH\"${C_RESET}" "$TO")"
    box_bottom
fi
