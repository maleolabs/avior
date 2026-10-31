#!/usr/bin/env bash
set -euo pipefail

stage=$1
archive=$(mkdir -p "$(dirname "$2")" && cd "$(dirname "$2")" && pwd)/$(basename "$2")
# Windows runner (Git Bash) tidak punya `zip`; fallback 7z agar CI e2e Windows
# memakai skrip packaging yang sama dengan pipeline rilis. ubuntu: zip.
if command -v zip >/dev/null 2>&1; then
  (cd "$stage" && zip -q "$archive" avior.exe bin/caddy_windows_amd64.exe setup.ps1 setup.bat)
else
  (cd "$stage" && 7z a -tzip -bso0 "$archive" avior.exe bin/caddy_windows_amd64.exe setup.ps1 setup.bat)
fi
