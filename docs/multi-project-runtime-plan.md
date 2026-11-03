# Plan: Runtime Multi-Project Concurrent (avior)

Dokumen ini adalah rencana penyesuaian agar satu binary `avior` dapat
menjalankan **N project Laravel secara bersamaan** pada satu device, di
Linux/macOS/Windows, dengan monitoring yang kontekstual per project.

Status: **Phase 1–7 DONE** (Phase 1–6 commit c7d4ae8..19ef58c; Phase 7 gateway clean URL terimplementasi). Sumber review: `internal/runtime`,
`internal/state`, `internal/processgroup`, `internal/hostname`,
`internal/proxy`, `internal/trust`, `cmd/`.

---

## 1. Tujuan & Non-Goals

**Tujuan**
- Satu device, satu binary `avior`, dapat `serve` beberapa project Laravel
  sekaligus tanpa collision port/domain/Caddy admin.
- `monitor` menampilkan daftar seluruh runtime aktif dengan konteks project
  (nama, root, status, pid, url, ports, services).
- `attach`/`stop` dapat menarget project tertentu (by root/name/token)
  tanpa menyentuh project lain.
- Aman lintas OS (Linux/macOS/Windows), termasuk Windows yang tidak
  mendukung identity-check proses.

**Non-Goals (fase ini)**
- Tidak membuat satu Caddy global shared proxy (tetap per-project Caddy).
- Tidak mengubah trust store CA menjadi per-project (tetap shared CA).
- Tidak menambah daemon/background service; tetap CLI foreground.

---

## 2. Deep Review: Kondisi Saat Ini

### 2.1 Yang Sudah Benar

- State & registry ditulis atomik (temp + rename).
- Process identity fail-closed (command + started-at) sebelum sinyal.
- `selectCaddyPort` preflight port + fallback deterministik.
- Hosts cleanup exact-line.
- Caddy config bind `127.0.0.1`.
- `Monitor()` sudah scan state dir dan verify project match (fail-closed).

### 2.2 Gap Kritis (Blocker Multi-Project)

1. **State path per-project SUDAH ada** (`state.Paths(root)` token sha256),
   tetapi `RuntimeState` belum membawa `ProjectToken`/`ProjectName`, dan
   `Monitor()` gagal total bila satu state file mismatch
   (`TestMonitorRejectsStatePathProjectMismatch` mengharapkan error).
   → Monitor tidak toleran terhadap satu entry rusak.

2. **Caddy admin port hardcoded `127.0.0.1:2019`**
   (`internal/proxy/caddy.go` global block, `internal/runtime/serve.go`
   `adminPort = 2019` + `WaitReady("127.0.0.1", adminPort, ...)`).
   → Instance kedua Caddy gagal bind admin API.

3. **Port default tetap**: Laravel 8000, Vite 5173, Caddy 8080.
   `SelectCaddyPort` hanya fallback Caddy; Laravel/Vite tidak auto-select.
   → Collision antar project.

4. **Domain default tetap**: `avior.localhost` (sebelumnya `simpskba.localhost`).
   → Semua project memakai domain yang sama; site/hosts saling menimpa.

5. **Hosts write tanpa lock + marker tidak project-scoped.**
   `EnsureHost` read-modify-write tanpa flock; marker hanya domain.
   → Concurrent project bisa clobber; cleanup satu project bisa menghapus
   entry project lain jika domain sama.

6. **Shared Caddy storage + trust metadata** (`brand.StateDir()/caddy`,
   `avior-trust.json`) dipakai semua project.
   → Race trust install / Caddy startup pada CA root.

7. **Monitor output minimal** (root + pid + port) — tidak kontekstual
   (status, url, services, ports breakdown).

8. **Registry `ProjectToken` belum ada** → sulit scoping cleanup lintas project
   (saat ini cleanup berbasis owner identity, tapi tidak menyimpan token).

9. **Windows tidak bisa identity-check** → cleanup stale dinonaktifkan,
   registry bisa menumpuk stale records.

### 2.3 Gap Medium

- Port availability TOCTOU (preflight, bukan authoritative bind).
- Selector UX hanya path; basename project tidak disimpan.
- `Monitor()` mengembalikan error fatal pada satu mismatch.
- Tidak ada guard "project yang sama sudah running" (duplicate runtime).
- Shared CA storage concurrency belum diuji.

---

## 3. Arsitektur Target

### 3.1 Identitas Project

- Canonical identity: absolute project root (sudah dipakai `state.Paths`).
- Token: `sha256(root)[:12]` (naik dari 8 hex → 12 hex untuk collision safety).
- `RuntimeState` v2 menambah: `Version`, `ProjectToken`, `ProjectName`,
  `StartedAt`, `Heartbeat`, `Domain`, `CaddyAdminPort`, `Ports`.
- `processRecord` menambah `ProjectToken`.

### 3.2 Project Index (`projects.json`)

Source of truth ringan untuk daftar project + alokasi port, atomik + lock.

```json
{
  "version": 1,
  "updated_at": "2026-...",
  "projects": [
    {
      "token": "a1b2c3d4e5f6",
      "root": "/abs/path",
      "name": "my-app",
      "domain": "my-app.localhost",
      "ports": {"app":8012,"vite":5189,"caddy":8090,"caddy_admin":2027},
      "last_pid": 12345,
      "last_seen": "2026-..."
    }
  ]
}
```

- Lokasi: `brand.StateDir()/projects.json`.
- Atomic write + global file lock (`brand.StateDir()/avior.lock`).
- Jika corrupt/missing → rebuild dari scan state dir (best-effort).

### 3.3 Port Allocator

- Range:
  - app: 8000–8099
  - vite: 5173–5272
  - caddy (site): 8080–8179
  - caddy_admin: 2019–2118
- Alur:
  1. Jika user override (`--port`, dll.), validasi availability + tidak dipakai
     index.
  2. Jika tidak override, cari port pertama yang `portAvailable()` dan belum
     ada di index.
  3. Simpan ke index di bawah lock.
- Authoritative bind failure dari Caddy/Laravel → retry allocate ulang
  (bukan silent).

### 3.4 Domain Slugger

- Slug dari basename project: lowercase, non-alphanumeric → `-`, trim `-`.
- Fallback `app` jika kosong.
- Jika domain sudah dipakai project lain → append `-<token[:4]>`.
- User `--domain` tetap override, tapi harus unique + `ValidLocalDomain`.

### 3.5 Caddy Admin Port Allocation

- `CaddyConfig` signature tambah `adminPort int`.
- Generate `admin 127.0.0.1:<adminPort>` (bukan 2019 tetap).
- `WaitReady("127.0.0.1", adminPort, ...)` pakai admin port.
- `adminPort` disimpan di `RuntimeState`/index.

### 3.6 Shared vs Per-Project CA Storage

- Default: shared global storage `brand.StateDir()/caddy` (satu CA untuk
  semua project → trust install sekali).
- Per-project storage hanya jika `--cert-storage` eksplisit.
- Trust manager tetap per-project root, tapi storage shared + idempotent.
- Install trust diserialisasi dengan global lock (hindari race NSS DB).

### 3.7 Concurrency Lock

- Global file lock `brand.StateDir()/avior.lock` (flock/`LockFileEx`, reuse
  pola `processgroup.lockRegistry`).
- Dipakai saat: baca/tulis index, alokasi port, create/update state entry,
  hosts write, trust install.
- State write per-project tetap atomic rename; lock menjaga index konsisten.

### 3.8 Hosts Safety

- Marker unik per domain: `# avior-managed <domain>` (sudah de-facto).
- `EnsureHost` serialisasi dengan global lock.
- Cleanup hanya exact managed line milik domain tersebut.
- Setiap project pakai domain unik → tidak saling menimpa.

---

## 4. Data Model Changes

### 4.1 RuntimeState v2

```go
type RuntimeState struct {
    Version       int             `json:"version"`        // 2
    ProjectToken  string          `json:"project_token"`
    ProjectRoot   string          `json:"project_root"`
    ProjectName   string          `json:"project_name"`
    Domain        string          `json:"domain"`
    Ports         PortSet         `json:"ports"`          // app/vite/caddy/caddy_admin
    MainPID       Process         `json:"main_pid"`
    StartedAt     time.Time       `json:"started_at"`
    UpdatedAt     time.Time       `json:"updated_at"`
    Heartbeat     time.Time       `json:"heartbeat"`
    Services      []ServiceState  `json:"services,omitempty"`
}
```

### 4.2 Process Registry Record

Tambah `ProjectToken string`.

### 4.3 Index Schema

Lihat §3.2.

### 4.4 V1 Compat

- Reader tetap support v1 (tanpa token/root) selama satu rilis.
- Jika v1 state ditemukan: migrate best-effort (root dari state.ProjectRoot
  bila ada), else quarantine ke `runtime-state-legacy-<ts>.json` + warning.
- Jangan hapus otomatis tanpa backup.

---

## 5. Command/UX Changes

### 5.1 `monitor` (default: semua project)

Output non-TTY (tab-separated, context-rich):

```
PROJECT    ROOT                          STATUS   PID     URL                               PORTS
my-app     /home/.../my-app              running  12345   https://my-app.localhost:8090     app=8012 caddy=8090 admin=2027 vite=5189
other-app  /home/.../other-app           running  12399   http://other-app.localhost:8081   app=8001 caddy=8081 admin=2020 vite=5174
```

- `monitor <project>` → filter satu project (path/name/token).
- TUI (opsional, fase backlog): list + key `a` attach, `s` stop, `r` refresh,
  `q` quit.

### 5.2 `attach <project>`

- Resolve project (path/name/token).
- Load per-project state; tampilkan `runtimeRow` (kontekstual).
- Error jelas bila tidak ada runtime.

### 5.3 `stop <project>`

- Verify state liveness + identity; stop hanya project tersebut.
- Tolak jika project mismatch.

### 5.4 `projects [--json]`

- List semua project dari index + liveness dari state.
- `projects prune` (opsional): hapus entry stale.

### 5.5 `serve`

- Auto slug domain + allocate ports bila tidak override.
- Print resolved config (project, url, ports).
- Guard: jika project yang sama sudah running → reject start dengan pesan
  actionable.

---

## 6. Migration / Backward Compat

1. Legacy global files (`runtime-state.json`, `runtime-processes.json`):
   - Jika ada & valid → baca, migrate ke per-project file bila root diketahui.
   - Jika root tidak diketahui → quarantine + warning.
2. State reader support v1 selama satu rilis.
3. Registry reader support record tanpa `ProjectToken`.
4. Caddy admin port default legacy 2019 tetap valid untuk project lama; new
   project allocate.

---

## 7. Risks & Open Questions

**Risks**
- Port allocation race jika lock gagal/unsupported (mitigasi: retry + fallback).
- Shared Caddy storage concurrency (perlu uji file_system locking).
- Index corruption → fallback scan state dir.
- Domain slug collision dengan user override (mitigasi: uniqueness check).
- Hosts safety: setiap domain unik, cleanup exact marker.
- Legacy state tanpa root tidak bisa di-associate.
- Cross-OS file lock complexity (sudah ada pola di processgroup).

**Open Questions**
1. Port persisten antar restart, atau auto-allocate ulang cukup?
2. Project name dari basename atau `APP_NAME` dari `.env`?
3. Boleh run project yang sama dua kali (biasanya tidak)?
4. Index jadi source of truth atau derived cache?
5. Shared CA storage aman untuk concurrent Caddy di semua OS?

---

## 8. Phased Implementation Plan

### Phase 1: State Identity + Per-Project Files (foundation) — DONE

**Scope**
- Tambah `ProjectToken`, `ProjectName`, `Version`, `Domain`,
  `CaddyAdminPort`, `Ports` ke `RuntimeState`.
- Token 8→12 hex.
- Reader/writer v1 compat.
- `Monitor()` toleran: skip entry invalid + log warning, jangan fatal.

**Acceptance**
- Dua project menulis state file berbeda.
- Monitor scan menemukan keduanya meskipun satu entry rusak.

**Tests**
- `TestRuntimeStatePathKeyedByRoot`
- `TestReadLegacyStateV1`
- `TestWriteReadStateV2`
- `TestMonitorSkipsInvalidEntry`

### Phase 2: Project Index + Monitor Context — DONE

**Scope**
- `projects.json` + global lock.
- `projects` command (+ `--json`).
- Monitor output context-rich (tabel).
- Index update saat start/stop.

**Acceptance**
- Monitor menampilkan project, root, status, pid, url, ports, services.
- Index update saat start/stop.

**Tests**
- `TestProjectIndexRoundTrip`
- `TestMonitorListsMultipleProjects`
- `TestProjectsCommandJSON`

### Phase 3: Port + Caddy Admin Allocator — DONE

**Scope**
- Port allocator + lock + range.
- Caddy admin port param di `CaddyConfig`.
- `WaitReady` pakai admin port.
- Retry pada bind failure.

**Acceptance**
- Dua project start tanpa port collision.
- Caddy admin port unik.
- Trust readiness pakai admin port.

**Tests**
- `TestAllocatePortAvoidsCollision`
- `TestCaddyConfigUsesAdminPort`
- `TestTwoProjectsNoPortCollision`

### Phase 4: Domain Slugger + Uniqueness — DONE

**Scope**
- Slug generation dari basename.
- Unique domain check.
- Hosts safety (lock + exact marker).

**Acceptance**
- Dua project basename sama → domain unik.
- Hosts cleanup tidak menyentuh entry lain.

**Tests**
- `TestDomainSlugCollision`
- `TestManagedHostRemovalOnlyOwnedLine` (existing)

### Phase 5: Attach/Stop/Select UX + Duplicate Guard — DONE

**Scope**
- Resolver project (path/name/token).
- `attach`/`stop` scoped.
- Guard duplicate runtime untuk root yang sama.

**Acceptance**
- Attach project A tidak menampilkan state B.
- Stop project A tidak menghentikan B.
- Duplicate serve ditolak.

**Tests**
- `TestAttachResolvesProject`
- `TestStopProjectScoped`
- `TestDuplicateRuntimeRejected`

### Phase 6: Shared CA + Trust Consistency — DONE

**Scope**
- Shared storage default tetap.
- Trust install diserialisasi lock.
- Status trust konsisten lintas project.

**Acceptance**
- Project kedua tidak install CA baru.
- Trust status konsisten.

**Tests**
- `TestSharedCAStorageDefault`
- `TestTrustStatusSharedStorage`

---

## 9. Rekomendasi Mulai

Mulai **Phase 1 + 2** dulu (state isolation + monitor kontekstual), karena
tanpa itu port/domain allocation tidak aman. Jangan ubah Caddy admin port
sebelum state isolation selesai.

Setiap phase wajib:
- test baru yang membuktikan acceptance criteria,
- `go test ./...`, `go vet ./...`, `gofmt -l .` (0),
- commit terpisah per phase.

---

## 10. Catatan Implementasi (Pragmatis)

- Reuse `processgroup.lockRegistry` pola untuk global lock; jangan buat
  abstraction baru.
- Token naik 8→12 hex (backward compat: reader accept both).
- Jangan ubah port default user-facing tanpa migrasi; allocate hanya bila
  tidak override.
- Shared CA storage tetap default; per-project hanya opt-in.
- Windows: jangan andalkan identity-check; fail-closed cleanup (sudah ada).

---

## 11. Proposal: URL Tanpa Port (Clean URL)

### 11.1 Pertanyaan

Bisa nggak url app cukup domain saja, mis. `feather.localhost` (tanpa `:8090`)?
Jawaban: **bisa, tapi butuh perubahan arsitektur** — tidak bisa dengan
desain per-project Caddy saat ini.

### 11.2 Kenapa Per-Project Caddy Tidak Bisa

Browser hanya menghilangkan port jika service berjalan di port standar:
HTTP `:80`, HTTPS `:443`. Port < 1024 butuh privilege (sudo/admin) di
Unix/Windows. Lebih penting: **hanya satu proses yang bisa bind ke :80/:443**.
Dengan per-project Caddy, instance kedua gagal bind. Jadi clean URL memaksa
**satu Caddy bersama (gateway)** yang merutekan berdasarkan `Host` header
ke upstream tiap project.

### 11.3 Opsi

**Opsi A — Gateway Caddy tunggal (direkomendasikan)**
- Satu Caddy instance bind `:80`/`:443` (privilege-gated, reuse pola sudo hosts).
- Saat `serve` project, Laravel tetap jalan di port tinggi (auto-allocated),
  dan gateway Caddy menambah site block `feather.localhost → 127.0.0.1:<appPort>`.
- Saat `stop`, site block dihapus; Caddy reload config.
- Shared CA storage (sudah direncanakan Phase 6) → satu CA untuk semua domain.
- Vite HMR di dev: proxy `/@vite` & websocket ke Vite port, atau tetap
  akses `:5173` langsung (trade-off).

Kelebihan: URL bersih, satu CA, satu listener.
Kekurangan: butuh sudo/admin untuk bind :80/:443; config gateway harus
di-regenerate atomik saat project start/stop; concurrency lebih kompleks.

**Opsi B — Tetap port tinggi (status quo)**
- Tidak butuh privilege untuk Caddy bind (port 8080).
- URL `feather.localhost:8090`.
- Lebih sederhana, sudah jalan.

**Opsi C — Hybrid (default high port, opt-in clean URL)**
- Default tetap high port (zero privilege surprise).
- Flag `--clean-url` (atau auto-deteksi bila sudo tersedia) mengaktifkan
  gateway mode.
- Migrasi bertahap: implement gateway sebagai phase tambahan setelah
  Phase 1–6 stabil.

### 11.4 Dampak pada Plan (jika Opsi A/C dipilih)

Tambah **Phase 7: Gateway Caddy (clean URL)** setelah Phase 6:

- Komponen baru: `internal/gateway/` (atau extend `proxy/`) — singleton
  Caddy config builder yang agregasi site blocks dari semua project aktif.
- Lock global untuk reload config (hindari race start/stop concurrent).
- Privilege gate: reuse `hostname.preflightSudo` pattern; fail-closed bila
  tidak ada sudo/admin → fallback ke high port + warning.
- State/index tambah field `GatewayRegistered bool` + `UpstreamPort`.
- Monitor tampilkan URL tanpa port bila gateway active, dengan port bila tidak.
- Vite: proxy HMR lewat gateway, atau dokumentasikan akses langsung `:vitePort`.

**Acceptance Phase 7**
- `serve feather` → browser buka `https://feather.localhost` (tanpa port).
- Dua project concurrent → kedua URL bersih, tidak collision.
- Tanpa sudo → fallback high port + pesan actionable.
- `stop` → site block dihapus, gateway reload.

**Tests**
- `TestGatewayConfigAggregatesSites`
- `TestGatewayReloadOnProjectStartStop`
- `TestCleanURLFallbackWithoutPrivilege`

### 11.4a Implementasi Phase 7 (selesai)

Komponen `internal/gateway/`:

- `gateway.go` — `Manager` (config aggregator): `Site{Domain, Upstream,
  HTTPS}`, `BuildConfig` deterministik (sort domain), `RegisterSite`/
  `UnregisterSite` (parse round-trip config yang kita generate sendiri),
  atomic render + validasi `caddy validate` (best-effort: cek binary ada).
- `process.go` — `Process`: start `caddy run`, `Reload` lewat `caddy
  reload --address <admin>` (zero-downtime, lintas proses), stop. Child
  terdaftar di registry global (`StateDir/gateway-processes.json`).
- `state.go` — `GatewayState` persisten (`StateDir/gateway/
  gateway-state.json`), `SelectAdminPort` (eksplisit > adopt gateway
  hidup > 2019 unik), `EnsureRunning` (start bila belum hidup),
  `RegisterSite`/`UnregisterSite` high-level (lock global
  `state.LockGateway()`), `StopGateway` (stop via registry global +
  verifikasi identitas), `CanBindStandardPorts` (probe bind :80/:443,
  fail-closed).

Wiring:

- `ServeOptions.CleanURL`/`GatewayAdminPort` + `StartOptions` + state v2
  field `CleanURL`/`GatewayAdmin` (monitor URL tanpa port saat CleanURL).
- `serve`: validasi dini fail-closed (butuh virtual domain + privilege),
  daftar site ke gateway setelah prepareCaddy (binary + admin port
  terverifikasi), cleanup otomatis saat Serve kembali (termasuk Ctrl+C):
  unregister site → gateway stop saat site terakhir pergi.
- `stop`: unregister site gateway setelah runtime berhenti.
- `cmd/serve`: flag `--clean-url`, `--gateway-admin-port`.
- URL output: `URL: https://feather.localhost` (gateway) vs
  `https://feather.localhost:8090` (high-port default).

### 11.5 Rekomendasi

Mulai dengan **Opsi C (hybrid)**: pertahankan high-port sebagai default
(tidak merusak workflow saat ini), tambahkan gateway mode sebagai opt-in
setelah Phase 1–6 stabil. Ini meminimalkan risiko regresi dan menjaga
backward compat.

Keputusan yang dibutuhkan user:
1. Apakah clean URL jadi default (butuh sudo) atau opt-in flag?
2. Apakah Vite HMR harus lewat gateway, atau boleh akses port langsung?
3. Apakah gateway Caddy hidup terus (daemon) atau di-spawn ulang per serve?

---

## 12. Bukti End-to-End (Phase 1–6)

`e2e/e2e_test.go::TestMultiProjectParallel` — proses nyata (php), dua
runtime bersamaan:

- kedua upstream php `-S` melayani traffic masing-masing,
- state file terpisah dengan identitas v2 (token/name/domain/ports),
- `projects.json` memuat kedua proyek,
- `Monitor()` melihat keduanya dengan URL berbeda,
- `RuntimeRunning` menolak serve ulang proyek yang masih hidup,
- `Stop(shop)` tidak menghentikan blog; blog tetap melayani hingga di-stop,
- setelah stop, kedua port benar-benar tertutup.

Perbaikan struktural yang ditemukan e2e: `RuntimeState.Owner` (proses CLI
pemilik runtime) — `StopManagedRuntime` mencari child berdasarkan owner,
bukan child pertama (MainPID tetap child pertama untuk readiness).
Pemanggil yang merupakan owner sendiri (e2e/TUI in-process) memakai
`StopManagedChildren` agar tidak men-sinyal diri sendiri.

---

## 13. Catatan Distribusi: Modul CLI Standalone

Pendahulu (tool sebagai bagian dari source proyek) mengasumsikan modul Go
CLI ada di `tools/<brand>/`. avior adalah repo terpisah — asumsi itu
memunculkan error `CLI module tidak ditemukan` saat `archive production`.

`internal/archive.DefaultCLIModuleLocator` sekarang menentukan modul Go
CLI secara eksplisit, urutan kandidat (masing-masing divalidasi via
`go.mod`, fail-closed tanpa fallback diam-diam):

1. `ARC_CLIMODULE` env — lokasi eksplisit (CI/test/user yang tahu
   checkout tool).
2. `tools/<brand>/` di dalam root proyek — layout monorepo pendahulu
   tetap didukung.
3. Root proyek sendiri — development: archive dijalankan dari checkout
   tool.
4. Direktori executable CLI yang berjalan — release/checkout tool
   standalone (kasus utama setelah pemisahan; `os.Executable()` +
   symlink-resolve).

`buildBinaries` menerima locator yang dapat di-inject (`Options.
CLIModuleLocator`); default `DefaultCLIModuleLocator`. Locator gagal →
build gagal dengan pesan yang menyebut SEMUA kandidat yang diperiksa
(actionable, bukan generik).

Kebutuhan binari per-OS tetap sama: `archive production` membangun 6
target (linux/darwin/windows × amd64/arm64) lewat `go build` cross-compile
dari modul yang di-resolve, lalu memasukkannya ke manifest produksi di
`tools/<brand>/bin/avior_<os>_<arch>`.
