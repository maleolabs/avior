package env

import (
	"strings"
)

// ValidateSecret mengembalikan daftar alasan mengapa secret berisiko ditulis
// ke .env (rawan rusak saat dibaca parser dotenv lain / disalin manual).
// Slice kosong berarti aman. Dipakai CLI prompt password untuk memperingatkan
// atau menolak input sebelum memanggil Update; Quote di env.go menjamin
// round-trip walau karakter-karakter ini lolos.
func ValidateSecret(secret string) []string {
	var reasons []string
	if strings.ContainsAny(secret, " \t") {
		reasons = append(reasons, "mengandung spasi/tab — terpotong bila ditulis tanpa kutip")
	}
	if strings.ContainsAny(secret, "\n\r") {
		reasons = append(reasons, "mengandung newline — melanggar format .env satu baris per entri")
	}
	if strings.Contains(secret, "#") {
		reasons = append(reasons, "mengandung '#' — terbaca sebagai komentar oleh banyak parser dotenv")
	}
	if strings.ContainsRune(secret, '"') {
		reasons = append(reasons, `mengandung '"' — perlu escaping, rawan salah kutip`)
	}
	if strings.ContainsRune(secret, '\'') {
		reasons = append(reasons, "mengandung \"'\" — rawan tertukar dengan kutip tunggal")
	}
	return reasons
}
