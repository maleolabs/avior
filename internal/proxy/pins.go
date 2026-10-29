package proxy

// Pin checksum build-time (Kontrak B — dependency-binary, review finding H3):
// tabel ini memuat sha256 binary `caddy_<os>_<arch>` hasil ekstraksi arsip
// rilis resmi Caddy versi CaddyVersion. Nilai dihitung saat pinning dari
// asset upstream yang terlebih dulu divalidasi terhadap checksums.txt
// resmi (sha512), lalu dibekukan di sini — CLI yang dibuild membawa pin
// itu, sehingga hasil download diverifikasi terhadap nilai tertanam, bukan
// terhadap file konfigurasi yang bisa ditimpa.
//
// Konsekuensi kontrak: manifest StateDir (caddy-manifest.json) HANYA ditulis
// setelah hasil download cocok dengan pin di sini → manifest adalah cache
// install-time, bukan root of trust. Root of trust = binary yang dibuild.
var caddyBinaryPins = map[string]string{
	"linux/amd64":   "ae87f566523d744731c0832d97b009b5cbdd69f5825e6e3c7de380c295a4ef68",
	"linux/arm64":   "b65d86ac3cce796908c1a7b9107a4a5bbf5b9ba4ab4cc96ee3bf41799c3d6764",
	"darwin/amd64":  "da50df5f04ae321d4612a082120aa423bee5e850d8a131b5a60181ba3acf5a58",
	"darwin/arm64":  "d04469662d2d7aa5a12623c1157d9a149e2a588713fcf39c850546d63c0f39b2",
	"windows/amd64": "9b0b71248e8d5739c1cce39141a905833c4a500e655bac6fa5c9672aca0bec9c",
	"windows/arm64": "affdff772e19dd272a59fcd5bc8156a6726b359e950ef6909830c828efe5f847",
}

// CaddyBinaryPin mengembalikan pin binary untuk pasangan OS/arch. ok=false
// berarti pasangan tidak dipin pada build ini — caller WAJIB fail-closed
// dengan pesan actionable; tidak ada fallback silent.
func CaddyBinaryPin(goos, goarch string) (string, bool) {
	pin, ok := caddyBinaryPins[goos+"/"+goarch]
	return pin, ok
}

// pinOverride adalah hook test (paritas urlOverride di download.go).
var pinOverride func(goos, goarch string) (string, bool)

// caddyPin mem-resolve pin runtime; test bisa meng-inject via pinOverride.
func caddyPin(goos, goarch string) (string, bool) {
	if pinOverride != nil {
		return pinOverride(goos, goarch)
	}
	return CaddyBinaryPin(goos, goarch)
}
