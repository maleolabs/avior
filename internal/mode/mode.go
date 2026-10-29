// Package mode adalah satu-satunya sumber kebenaran lifecycle mode
// aplikasi (development/production). Validasi string mode hanya boleh ada
// di ParseMode — paket lain wajib memakai tipe Mode [arc:avior-architecture].
package mode

import "fmt"

// Mode adalah lifecycle mode aplikasi. Nilai nol tidak valid; selalu
// peroleh Mode dari ParseMode.
type Mode string

const (
	ModeDevelopment Mode = "development"
	ModeProduction  Mode = "production"
)

// ParseMode adalah gerbang validasi mode tunggal. Case-sensitive, tanpa
// trim — persis perilaku CLI sebelumnya ("mode harus development atau
// production").
func ParseMode(s string) (Mode, error) {
	switch s {
	case string(ModeDevelopment):
		return ModeDevelopment, nil
	case string(ModeProduction):
		return ModeProduction, nil
	default:
		return "", fmt.Errorf("mode harus development atau production")
	}
}
