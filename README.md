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
