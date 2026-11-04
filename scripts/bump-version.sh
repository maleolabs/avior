#!/bin/sh
# bump-version.sh — naikkan versi, jalankan CI lokal, lalu tag + push.
#
# Alur: hitung versi baru → anvil pipeline ci (gagal = berhenti, tanpa
# tag) → tulis VERSION + anvil.yaml project.version → commit → tag v<x.y.z>
# → push source + tag (tag push memicu workflow Release).
#
# Versi di anvil.yaml murni SemVer (dalaman anvil menolak suffix), sedang
# VERSION memakai suffix -dev saat build-non-rilis. Saat rilis, keduanya
# disinkronkan ke x.y.z polos.
#
# Usage: scripts/bump-version.sh {major|minor|patch}
set -eu

usage() {
    echo "Usage: $0 {major|minor|patch}" >&2
    exit 2
}

[ "$#" -eq 1 ] || usage
KIND="$1"
case "$KIND" in
    major|minor|patch) ;;
    *) usage ;;
esac

# Deteksi root repo dari direktori skrip (bukan PWD) supaya skrip bisa
# dipanggil dari mana saja.
case "$0" in */*) root=$(cd -- "${0%/*}/.." && pwd) ;; *) root=$(pwd) ;;
esac
cd -- "$root"

if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
    echo "worktree kotor: commit dulu perubahan sebelum bump" >&2
    exit 1
fi

# Versi saat ini: VERSION file sebagai sumber (suffix -dev dibuang).
cur=$(sed -E 's/-.*$//' VERSION)
case "$cur" in
    [0-9]*.[0-9]*.[0-9]*) ;;
    *) echo "VERSION tidak kompatibel SemVer: $cur" >&2; exit 1 ;;
esac

major=$(printf '%s' "$cur" | cut -d. -f1)
minor=$(printf '%s' "$cur" | cut -d. -f2)
patch=$(printf '%s' "$cur" | cut -d. -f3)

case "$KIND" in
    major) major=$((major + 1)); minor=0; patch=0 ;;
    minor) minor=$((minor + 1)); patch=0 ;;
    patch) patch=$((patch + 1)) ;;
esac
new="$major.$minor.$patch"
echo "bump: $cur -> $new ($KIND)"

# Gate: CI lokal berdasarkan pipeline anvil. Gagal = tidak ada penulisan
# versi, tidak ada tag.
if command -v anvil >/dev/null 2>&1; then
    anvil pipeline ci
else
    echo "anvil tidak ditemukan: install dari https://github.com/maleolabs/anvil/releases" >&2
    exit 1
fi

# Sinkronisasi dua sumber versi: VERSION (dibaca internal/version saat
# build lokal) + anvil.yaml (project metadata anvil).
printf '%s\n' "$new" > VERSION
if command -v anvil >/dev/null 2>&1; then
    anvil project version set "$new" >/dev/null
fi

git add VERSION anvil.yaml
git commit -m "chore: release v$new" >/dev/null
git tag "v$new"
echo "tagged v$new"

# Push source + tags. Remote ada; jika tidak, berhenti sebelum push
# supaya keadaan lokal dapat diperbaiki manual.
git push
git push origin "v$new"
echo "release v$new dipublikasi (workflow Release dipicu oleh tag)"
