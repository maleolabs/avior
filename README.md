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

Instalasi simpskba lama di mesin dev perlu dibersihkan manual **satu kali**:
`caddy untrust`, hapus entry hosts bertanda `# simpskba-managed`, hapus
`~/.local/share/simpskba`. Detail: `dec:product-scope-avior`.
