# avior

CLI universal setup & runtime untuk project Laravel — penerus `simpskba`.

Repo ini lahir lewat **staged rewrite** (lihat `adr:staged-rewrite-in-new-repo`):
kode tidak dicopy mentah; migrasi fase demi fase mengikuti roadmap EKA.

## Sumber kebenaran

Seluruh keputusan produk & arsitektur hidup di EKA KMS (workspace `~/.eka`, project `avior`):

```sh
eka get discovery      # visi, strategi, requirement MVP, findings review simpskba
eka get architecture   # arsitektur target + ADR/decision
eka get containers     # peta fase eksekusi (phase-0 .. phase-5)
eka view execution     # board eksekusi
```

Plan induk: `avior/plan:avior-transformation` (approved) — 6 fase:

| Fase | Container | Isi |
|---|---|---|
| F0 | `ctr:phase-0-stabilization` | fix HIGH review simpskba (H1–H3, H5, H6) |
| F1 | `ctr:phase-1-foundation` | pecah god package runtime, satu parser .env, tipe Mode, step-pipeline, writer injection |
| F2 | `ctr:phase-2-rebrand` | brand contract + rename sweep + retire identitas lama |
| F3 | `ctr:phase-3-generalization` | DB driver pilihan, setup generic, manifest opt-in, verifikasi Windows |
| F4 | `ctr:phase-4-multi-project` | registry keyed per project, monitor/attach/stop |
| F5 | `ctr:phase-5-distribution` | release CI, checksum manifest, channel install |

Eksekusi via `/eka-execute` (aktifkan container: `eka transition ctr:phase-0-stabilization active`).

## Build

```sh
go build ./...
go test ./...
```

### Queue flag (serve production)

Untuk `serve production`, flag `--queue` bersifat otoritatif saat diisi:

- `--queue=true` → Horizon selalu jalan.
- `--queue=false` → Horizon **tidak pernah** jalan (fix review H1: dulu flag ini no-op).
- flag tidak diisi → auto-detect dari evidence project (`plan.Horizon`).

## Migrasi sekali-jalan dari simpskba

> Belum pernah menginstal simpskba? Lewati seksi ini.

Instalasi simpskba lama di mesin dev perlu dibersihkan manual **satu kali**
sebelum memakai avior:

1. **Untrust CA lama** — hapus sertifikat root bernama `SIM-PSKBA Local CA`
   dari semua trust store. Jika binary `caddy` milik simpskba masih ada,
   `caddy untrust` adalah cara paling mudah; jika tidak, bersihkan manual:
   - macOS:
     - System keychain (domain admin — jalur yang dipakai `caddy trust`):
       `sudo security delete-certificate -c "SIM-PSKBA Local CA" /Library/Keychains/System.keychain`
     - Login keychain (domain user): `security delete-certificate -c "SIM-PSKBA Local CA" ~/Library/Keychains/login.keychain-db`
     - atau via Keychain Access → cari `SIM-PSKBA Local CA` → hapus.
   - Firefox/Chromium (NSS): `certutil -D -n "SIM-PSKBA Local CA" -d sql:<direktori>`
     — direktori profil Firefox, atau `~/.pki/nssdb` untuk Chromium/Brave
     (mekanisme sama yang dipakai avior, lihat `internal/trust`),
     lalu restart browser.
   - Windows: `certmgr.msc` → **Trusted Root Certification Authorities** →
     **Certificates** → klik kanan `SIM-PSKBA Local CA` → **Delete**.
2. **Hosts** — hapus baris yang memuat komentar `# simpskba-managed`
   (formatnya: `127.0.0.1 <domain> # simpskba-managed <domain>`)
   dari file hosts OS: `/etc/hosts` (macOS/Linux) atau
   `C:\Windows\System32\drivers\etc\hosts` (Windows).
3. **State dir lama** — hapus `~/.local/share/simpskba` (berisi manifest
   Caddy & sertifikat bekas).
4. **Staging Caddy bekas** — hapus direktori `tools/simpskba/` di project
   jika ada (layout staging lama `tools/<nama>/bin`).

**Verifikasi**: buka salah satu domain dev di browser — tidak boleh muncul
sertifikat dari `SIM-PSKBA Local CA` (yang tampil harusnya `Avior Local CA`).

Identitas avior yang baru: CA `Avior Local CA`, marker hosts
`# avior-managed`, state dir `~/.local/share/avior` (lihat `internal/brand`).
Latar keputusan: `dec:product-scope-avior`.
