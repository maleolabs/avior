#!/bin/sh
# avior launcher (arsip rilis Linux/macOS) — menjalankan CLI dari direktori
# arsipnya sendiri, tanpa perlu PATH (builtin saja; tanpa dirname/cd eksternal).
# Dipakai: ./setup.sh <perintah> ...
case $0 in
  */*) dir=${0%/*} ;;
  *) dir=. ;;
esac
dir=$(cd -- "$dir" && pwd)
exec "$dir/avior" "$@"
