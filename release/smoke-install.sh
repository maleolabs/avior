#!/bin/sh
# Smoke install (m3-install-channels): go install dari module lokal harus
# menghasilkan binary CLI yang bisa dijalankan. Ini memverifikasi jalur
# `go install github.com/maleolabs/avior@latest` (module path + build) tanpa
# jaringan eksternal — register modul publik diverifikasi saat rilis asli.
set -eu
case $0 in */*) here=${0%/*} ;; *) here=. ;; esac
cd -- "$here/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
GOBIN="$tmp" go install .
"$tmp/avior" --help >/dev/null
echo "go install smoke ok"
