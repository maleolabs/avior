# Execution State
scope: ctr:phase-2-rebrand
updated: 2026-10-29
status: completed
mode: delegated
items:
- avior/sto:p2-brand-contract: done  # 2196d8e+6a4031a merged to main (225f0ac); qa/code approve (fix 1 MAJOR + 2 MINOR); worktree cleaned
- avior/sto:p2-rename-sweep: done  # a961e1b+ce0ec49 merged to main (0e0ccdf); qa/code/security approve; worktree cleaned
- avior/sto:p2-retire-legacy-identity: done  # a09492f+9150386 merged to main (6dd1c8c); qa/product approve, code review REQUEST_CHANGES -> fixed -> approve; worktree cleaned
- avior/sto:p2-e2e-smoke: done  # 4708da8+3291b98 merged to main (31b470b); qa/code approve (4 Low fixed); worktree cleaned
current: (none — container completed)
next: ctr:phase-3-generalization (activate when ready)
decisions:
- carried: integration branch = main; worktree path ABSOLUTE ../avior-worktrees/ from repo root; sequential (semua item sentuh file identitas yang sama)
- p2-brand-contract: gate grep count-based (allowlist map[string]int pin jumlah baris literal per file) — sweep turunkan count ke 0 lalu hapus entri; stripSkipped utk import path + arc tag; pin exact StateDir ~/.local/share/avior
- p2-brand-contract: sisa literal utk sweep = module path go.mod+import, komentar historis simpskba (detect/env/setup/ui), fixtures test, komentar roadmap main.go + nss.go; NITPICK reviewer: dead condition + reason text di contract_test.go ikut dihabiskan saat sweep
- p2-retire-legacy-identity: README migrasi 4 langkah + verifikasi (System+login keychain macOS, certmgr.msc Windows, hosts per-OS, NSS 2 path); rename C sim_add_trust/sim_remove_trust -> add_trust/remove_trust; RISK: build darwin+cgo belum terverifikasi (host linux) — verifikasi di host macOS sebelum rilis
- p2-e2e-smoke: harness e2e/e2e_test.go (tag e2e) reusable utk cmd/ — go test -tags e2e ./e2e; hosts privileged opt-in E2E_ALLOW_HOSTS=1; Caddy v2.10.2 terverifikasi HTTP+HTTPS; FINDING WAJIB cmd/serve: konflik schema runtime-processes-<tok>.json (state.SaveProcesses 0644 vs processgroup.RegisterProcess 0600, satu path) — pilih satu pemilik file sebelum wiring
- SECURITY CARRIED MAJOR: VerifyManaged belum ter-wire ke InstallCaddy (trust.go:106, zero call site produksi) — wire + test struktural saat item cmd layer/sudo-trust; manual verify caddy trust macOS saat item macOS trust (dari p1 + security-review p2-rename-sweep)
- p2-rename-sweep: gate isBrandLiteral kini deteksi simpskba+sim-pskba+avior; fixture hosts_test = EnsureHost(upper(Name)+'.test.', true)
- gates butuh GOFLAGS=-buildvcs=false (repo tanpa tag — pre-existing; opsional tag v0.0.1 nanti)
resume: (nothing to resume — scope completed; next: /eka-execute ctr:phase-3-generalization)
