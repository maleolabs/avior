# avior

CLI universal setup & runtime untuk project Laravel — satu binary, lintas OS
(Linux/macOS/Windows). One-command: `doctor`, `verify`, `setup`, `serve`,
`archive`, `trust`, plus `monitor`/`attach`/`stop` untuk runtime multi-project.

## Sumber kebenaran

Seluruh keputusan produk & arsitektur hidup di EKA KMS (workspace `~/.eka`,
project `avior`):

```sh
eka get discovery      # visi, strategi, requirement MVP, findings review legacy
eka get architecture   # arsitektur target + ADR/decision
eka get containers     # peta fase eksekusi
eka view execution     # board eksekusi aktif
```

Plan induk: `avior/plan:avior-migration-v2` (approved) — 3 container:

| Container | Isi |
|---|---|
| `ctr:m1-identity-purge` | identitas murni avior di seluruh dokumentasi & log |
| `ctr:m2-cli-parity` | paket `cmd/` (cobra) + port command setup/serve/archive/trust |
| `ctr:m3-distribution-v2` | checksum manifest, install channels, hygiene worktree |

Kontrak eksekusi: `dec:execution-context-contract` — setiap work item memuat
**Sumber / Target / Perubahan** secara eksplisit.

Eksekusi via `/eka-execute` (aktifkan container:
`eka transition ctr:m2-cli-parity active`).

## Build

```sh
go build ./...
go test ./...
go test -race ./...
```

## Instalasi

### go install (butuh Go 1.24+)

```sh
go install github.com/maleolabs/avior@latest
```

Binary Caddy tidak ikut terpasang lewat jalur ini — CLI mengunduhnya
otomatis saat pertama dibutuhkan dan memverifikasinya terhadap pin checksum
yang tertanam saat build (lihat "Checksum distribusi" di bawah).

### Arsip rilis (tanpa Go)

Unduh `avior-<versi>-<os>-<arch>.tar.gz` (Linux/macOS) atau `.zip`
(Windows) dari halaman releases, lalu ekstrak:

- **Linux/macOS** — `./setup.sh <perintah>` (mis. `./setup.sh doctor`)
- **Windows** — `setup.ps1 <perintah>` atau `setup.bat <perintah>`

Launcher menjalankan binary `avior` dari direktori arsipnya sendiri —
tidak perlu menambah apa pun ke PATH. Arsip juga memuat binary Caddy
terpin di `bin/`.

### Update binary CLI (`selfupdate`)

```sh
avior selfupdate       # tanya konfirmasi
avior selfupdate --yes # non-interaktif
avior selfupdate --gc  # bersihkan cache binary versi lama
```

`selfupdate` mengganti binary CLI host dengan release terbaru (verified
SHA256SUMS, pemasangan atomik). Berbeda dengan `archive production` yang
**memin** binary ke versi CLI berjalan — update di luar perintah arsip
menjaga arsip tetap reproduksibel.

### Binary CLI pada arsip produksi

`avior archive production` tidak meng-compile CLI di mesin pengguna —
binary diambil dari GitHub Releases ke cache lokal
(`~/.local/share/<name>/cli-binaries/`), lalu dibundle. Versi selalu
sesuai CLI yang berjalan (pinned). Saat perintah berjalan interaktif:

1. Tanya apakah binary CLI disertakan (default: ya).
2. Pilih target OS: Windows / macOS / Linux.
3. Dua binary (amd64 + arm64) OS terpilih dibundle ke arsip.

Mode non-interaktif wajib putusan eksplisit (fail-closed):

```sh
avior archive production --bundle-cli=<windows|macos|linux|none>
```

Mirror internal / CI offline dapat mengganti sumber unduhan lewat
`<NAME>_RELEASE_BASE_URL` (base URL GitHub Releases).

**Batas yang jujur:** versi CLI yang bukan tag rilis (build dari checkout,
`0.0.0-dev`) tidak dapat membundle binary — release belum menyediakannya.
Gunakan `--bundle-cli=none` atau pasang CLI dari rilis resmi.

## Checksum distribusi

Setiap rilis menyertakan `SHA256SUMS` berisi checksum seluruh arsip
(`.tar.gz`/`.zip`). Verifikasi direktori hasil unduhan:

```sh
avior verify-release <direktori-rilis>
```

Perintah ini fail-closed: entri manifest yang hilang, digest yang tidak
cocok, dan file ekstra di luar manifest semuanya gagal.

**Batas yang jujur:** checksum ini unsigned — ia membuktikan **integritas**
(artefak utuh dan konsisten dengan manifestnya), **bukan autentisitas**
(bukti siapa penerbitnya). Ia bukan pengganti tanda tangan digital.

Binary Caddy yang dibundel arsip rilis (atau diunduh otomatis pada jalur
`go install`) diverifikasi terhadap pin checksum yang tertanam di binary
CLI saat build (`internal/proxy/pins.go`); manifest lokal di StateDir hanya
ditulis setelah checksum cocok — manifest adalah cache, bukan sumber
kebenaran.

## Migrasi dari instalasi tool sebelumnya

Sudah pernah memasang tool pendahulu di mesin dev? Bersihkan sekali:

1. **CA lokal** — hapus CA "tool lama" dari trust store (`caddy untrust` dari
   instalasi lama, atau hapus manual dari keychain/trust store OS).
2. **Hosts** — hapus baris yang memuat komentar penanda tool lama
   (`# <nama-lama>-managed`).
3. **State dir lama** — hapus direktori state user-level milik tool lama
   (`~/.local/share/<nama-lama>`).

avior menggunakan identitasnya sendiri (`# avior-managed`,
`~/.local/share/avior`, CA "Avior Local CA") dan tidak membaca artefak lama.
