package runtime

import "github.com/maleolabs/avior/internal/mode"

// NormalizeServeOptions menerapkan policy mode terhadap opsi serve.
//
// Default flag CLI berorientasi production (deploy-like); policy di sini
// yang menurunkannya untuk development / non-interactive, sehingga cmd/
// tetap wiring tipis tanpa policy domain [arc:avior-architecture].
//
// changed berisi flag yang eksplisit di-set pemanggil (Cobra Changed);
// virtual/https/trust adalah nilai flag terkini (default production).
// Perilaku identik dengan normalizeServeOptions di CLI sebelumnya.
func NormalizeServeOptions(m mode.Mode, changed map[string]bool, nonInteractive bool, virtual, https, trust bool) (bool, bool, bool) {
	if m == mode.ModeDevelopment {
		if !changed["virtual-domain"] {
			virtual = false
		}
		if !changed["https"] {
			https = false
		}
		if !changed["trust-local-ca"] {
			trust = false
		}
	}
	if nonInteractive && !changed["trust-local-ca"] {
		trust = false
	}
	if https {
		virtual = true
	}
	return virtual, https, trust
}
