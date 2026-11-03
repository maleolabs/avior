# Plan: Distribusi Binary CLI Arsip Produksi via GitHub Releases

Status: **plan disetujui (Q1–Q5), siap implementasi**
Tanggal: 2026-11-03
Konteks: diskusi "mekanisme bundle binary avior per-OS ke archive" —
mengganti compile-from-source (`DefaultCLIModuleLocator`, commit 01ed87c)
dengan download dari GitHub Releases.

## Latar belakang

`archive production` mewarisi asumsi pendahulu: modul Go CLI tinggal di
`tools/<brand>/` dalam checkout proyek. avior adalah tool standalone, jadi
asumsi itu melahirkan error `CLI module tidak ditemukan`. Fix sebelumnya
(`DefaultCLIModuleLocator`) menyelesaikan gejala untuk pengguna yang punya
source — tetapi pengguna yang hanya punya binary avior (tanpa Go toolchain,
tanpa checkout) tetap tidak bisa menjalankan `archive production`. Plan ini
menyelesaikan akar masalahnya: binary tidak pernah di-compile di mesin
pengguna, melainkan diambil dari release yang dibangun CI.

## Mekanisme inti

GitHub Actions (job `release:` di `.github/workflows/ci.yml`, trigger tag
`v*`) yang sudah membangun arsip install per-OS ditambah **raw binary**
`avior_<os>_<arch>` sebagai aset release. Saat pengguna menjalankan
`archive production`, CLI mengambil binary dari cache lokal, atau download
dari release — **versi selalu sesuai CLI yang sedang berjalan (pinned),
bukan latest** — lalu membundle ke arsip sesuai pilihan interaktif.

## Keputusan (Q1–Q5)

| # | Pertanyaan | Keputusan |
|---|---|---|
| Q1 | Pin atau follow latest? | **Pinning** — versi binary = versi CLI berjalan |
| Q2 | Cache multi-versi + GC? | Ya + perintah `avior selfupdate --gc` |
| Q3 | Mirror/CI offline? | `AVIOR_RELEASE_BASE_URL` env override |
| Q4 | Jumlah binary yang dibundle? | **Bukan 6 melainkan 2**, dipilih interaktif |
| Q5 | Bundle semua atau OS berjalan? | Interaktif (lihat Q4) |

### Q4 — alur interaktif baru

Dipasang sebagai step setelah "Install production dependencies":

1. **Tanya**: "Sertakan binary avior pada arsip?" (default: ya)
2. **Tidak** → tidak ada binary CLI dibundle (arsip murni Laravel; deploy
   server tanpa avior).
3. **Ya** → tampilkan pilihan target OS:
   - Windows
   - macOS
   - Linux
4. Setelah OS dipilih → ambil **2 binary** (amd64 + arm64) untuk OS
   tersebut dari release (cache lokal dulu, lalu download) → bundle.

Konsekuensi: arsip tidak lagi bisa multi-OS. Itu sengaja — arsip
production lama membakar ~72 MB (12 MB × 6) padahal server target butuh
satu arsitektur. Versi pendahului membundel 6 untuk kompatibilitas
`server.sh` (memilih binary via `uname`); alur Q4 menggantikan asumsi
itu dengan pilihan eksplisit. `server.sh`/`server.ps1`/`server.bat`
karena itu berpotensi tidak lagi relevan — lihat "Verifikasi tersisa".

Non-interactive mode: prompt diturunkan ke flag `--bundle-cli=<os|none>`
(default: prompt saat TTY; error fail-closed saat non-TTY tanpa flag,
paritas consent gate lain di repo).

### Q2 — `avior selfupdate`

**Peringatan ambiguitas:** "selfupdate" dengan pinning punya dua arti.
Yang dibangun adalah (a):

- **(a) Perbarui binary CLI *host* itu sendiri** ke latest release
  (download + verifikasi + ganti binary sendiri). Pakai follow-latest
  di sini, di luar perintah `archive` — justru karena `archive` dipin.
- **(b) Perbarui binary yang akan dibundle ke versi yang lebih baru dari
  CLI — DITOLAK.** Itu memutus reproduksibilitas arsip (D2).

`selfupdate --gc` menghapus folder versi lama di cache binari
(`StateDir/cli-binaries/v*/`); folder `--gc` tidak ada saat ini → no-op
yang aman sampai Fase 3 ada cache-nya.

## Prinsip desain

- **D1 Fail-closed.** Download gagal / checksum mismatch / tag tidak ada →
  arsip dibatalkan, tidak ada file parsial yang tertinggal.
- **D2 Reproduksibilitas.** Arsip dengan label sama berisi binary versi
  sama, kapan pun dibuat. (Alasan penolakan follow-latest di `archive`.)
- **D3 Cache sebelum network.** Ada di cache → tidak ada request.
- **D4 Remote preview.** Sertakan binary hanya bila diminta — tidak
  membakar 72 MB per arsip.
- **D5 Kontrak checksum_existing.** SHA256SUMS per release (Kontrak A,
  `cmd/verify_release.go`) memvalidasi download. Pola `FetchCaddy`
  (`internal/proxy/download.go`) adalah template: streaming, anti-bomb,
  `O_EXCL` concurrent, delete-on-fail.

## Verifikasi teknis (riset)

- Repo **tidak punya version const** — hambatan nyata; Fase 1 menyelesaikan.
- `/releases/latest` → 302 ke tag; tag dari redirect Location header
  (1 request, no JSON, no API rate limit). Dipakai `selfupdate` (a).
- Asset download domain (`/releases/download/...`) tidak kena rate limit
  API (60 req/jam untuk API JSON unauthenticated). Download binary aman.
- Pin build-time (pola `internal/proxy/pins.go`) **opsional** — SHA256SUMS
  sudah cukup untuk archive; pin tambahan membeku versi tapi memaksa bump
  tiap rilis. Sementara: SHA256SUMS saja.
- CI release saat ini: 4 target; Q4 menggantinya jadi prompt 3 OS × 2 arch.
- CI memakai `-buildvcs=false`; Fase 1 memilih ldflags `-X` (bukan
  `debug.ReadBuildInfo`) + fallback `VERSION` file untuk build lokal.
- `brand.StateDir()` = `~/.local/share/avior` — tempat cache binari yang
  konsisten dengan konvensi state tool yang sudah ada (`caddy-manifest.json`
  juga di sana). Dipakai sebagai cache, bukan config.
- Helper prompt ada: `internal/setup.setup.go` (`confirm`,
  `promptValue`) dan pola `fmt.Fscanln` di `cmd/serve.go:103` — prompt
  baru mengikuti pola ini.

## Verifikasi tersisa (sebelum/dalam implementasi)

- **`server.sh`/`server.ps1`/`server.bat`** ada di `roots` manifest
  (`internal/archive/archive.go:70`) dan memilih binary via `uname`
  (template pendahulu). Dengan Q4, prompt menggantikan asumsi ini.
  Cek apakah file-file ini masih digunakan di output `archive` atau
  hanya dead weight sebelum memutuskan hapus/retain.
- **Periode transisi**: commit `01ed87c` (`DefaultCLIModuleLocator`)
  menjadi dead code saat Fase 3 selesai — hapus bersih, bukan dibiarkan.

## Fase implementasi

### Fase 1 — Fondasi versi (prasyarat)

- Paket `internal/version`: `Version()` via ldflags `-X main.version`
  di CI, fallback membaca `VERSION` file di root modul untuk build
  lokal, fallback `0.0.0-dev`.
- CI workflow: set `-X main.version=${GITHUB_REF_NAME#v}` pada step
  build (baik job `release:` maupun build arsip e2e Windows).
- Buat `VERSION` file di repo tool (fallback build lokal).
- Output test: unit test `Version()` untuk ketiga jalur (ldflags,
  file, dev fallback).

### Fase 2 — Aset release dari CI

- Murni perubahan workflow, belum menyentuh kode Go.
- Job `release:` yang ada menambah raw binary `avior_<os>_<arch>`
  (6 target: linux/darwin/windows × amd64/arm64) sebagai aset
  release — walaupun Q4 hanya bundle 2, cache download perlu semua
  target karena pengguna bisa memilih OS apa pun.
- Raw binary tidak usah dibungkus arsip (launcher setup.sh ada di aset
  arsip install yang sudah ada); SHA256SUMS yang sudah ditulis CI
  otomatis memuatnya.

### Fase 3 — Layer download + cache

- Paket `internal/cliassets` (jangan perluas `proxy` — beda domain):
  `EnsureBinaries(ctx, version, cacheDir, osName) → []string` (2 binary:
  amd64 + arm64), `LatestVersion(ctx) → string` (redirect Location dari
  `/releases/latest`), download + verifikasi SHA256SUMS pola `FetchCaddy`.
- `AVIOR_RELEASE_BASE_URL` override pola `urlOverride` di `FetchCaddy`.
- Test: fake server (`httptest`) yang menyajikan manifest rusak /
  binary rusak / EEXIST; fail-closed pada setiap kasus.
- **Hapus `DefaultCLIModuleLocator` + `buildBinaries` `go build` path**
  (commit 01ed87c menjadi dead code).

### Fase 4 — Wiring `archive production`

- Step interaktif Q4 setelah "Install production dependencies";
  helper prompt ikut pola `internal/setup.setup.go` (`confirm`).
- Flag `--bundle-cli=<windows|macos|linux|none>` untuk non-interactive;
  default prompt saat TTY, error fail-closed saat non-TTY tanpa flag.
- `dry-run` harus tetap bekerja tanpa download (prompt `--dry-run`
  mensimulasikan; default "ya" untuk build CI).
- Manifest path tetap `tools/<brand>/bin/avior_<os>_<arch>` — paritas
  dengan layout release yang sudah ada.
- `paths` di manifest arsip jadi conditional: hanya 2 binary OS dipilih
  (atau 0 bila "tidak").

### Fase 5 — `avior selfupdate`

- Subcommand baru: download latest release binary CLI host (2 arch untuk
  OS berjalan — ambil yang cocok `runtime.GOARCH`), verifikasi
  SHA256SUMS, ganti binary sendiri (atomic rename setelah checksum).
- `--gc`: hapus folder versi lama di `StateDir/cli-binaries/`.
- Consent: default non-interactive (jalur `--yes`), prompt saat TTY
  untuk ganti binary sendiri — ini menulis binary yang sedang berjalan.

### Fase 6 — UX error + docs

- Error "tidak ada koneksi, cache kosong" mencantumkan path cache +
  saran `selfupdate` atau `--bundle-cli=none`.
- README: bagian distribusi diperbarui (pinteran binary CLI sekarang
  dari release, bukan compile lokal; `AVIOR_RELEASE_BASE_URL` untuk
  mirror).
- `verify-release` tidak berubah (Kontrak A tetap memvalidasi arsip
  akhir).

## Urutan dependensi

```
Fase 1 (version) → Fase 3 (download) → Fase 4 (wiring archive)
Fase 2 (CI aset)  ↗ (paralel; butuh oleh Fase 3 untuk test e2e)
Fase 5 (selfupdate) → butuh Fase 3 (download layer) + Fase 1 (version)
Fase 6 → setelah Fase 4
```

## Yang tidak dikerjakan (out of scope)

- Penandatanganan release (cosign/sigstore) — tetap sha256-only
  (integritas, bukan autentisitas); dirinci di README batas kejujuran.
- Pin build-time untuk binary CLI (pola `pins.go`) — SHA256SUMS saja
  cukup; pin bisa ditambahkan nanti bila mau membeku versi lebih ketat.
- Build untuk arsitektur lain (riscv, 386) — 6 target tetap.
