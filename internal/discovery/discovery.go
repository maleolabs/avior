// Package discovery membaca evidence proyek (manifest + konfigurasi) untuk
// memutuskan service runtime mana yang layak dijalankan. Port perilaku
// codebase pendahulu; inferensi sengaja konservatif — Reverb tidak
// di-infer dari broadcast events.
package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ServicePlan menggambarkan service yang aman dijalankan proyek.
type ServicePlan struct {
	Horizon, Scheduler, Reverb, Vite bool
	Diagnostics                      []string
}

type composerManifest struct {
	Require    map[string]string `json:"require"`
	RequireDev map[string]string `json:"require-dev"`
}

type packageManifest struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Scripts         map[string]string `json:"scripts"`
}

func has(m composerManifest, name string) bool {
	_, a := m.Require[name]
	_, b := m.RequireDev[name]
	return a || b
}

func hasPackage(m packageManifest, name string) bool {
	_, a := m.Dependencies[name]
	_, b := m.DevDependencies[name]
	return a || b
}

func readComposer(path string) (composerManifest, error) {
	var m composerManifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(b, &m)
	return m, err
}

// Discover memakai manifest dan evidence Laravel sisi source/config.
func Discover(root string) ServicePlan {
	if root == "" {
		// Command unit test / embedder tanpa root proyek: pertahankan plan
		// historis; root nyata selalu memakai evidence.
		return ServicePlan{Horizon: true, Scheduler: true, Vite: true}
	}
	p := ServicePlan{}
	composer, err := readComposer(filepath.Join(root, "composer.json"))
	if err != nil {
		p.Diagnostics = append(p.Diagnostics, "composer.json tidak terbaca")
	}
	p.Horizon = has(composer, "laravel/horizon") && (exists(root, "config/horizon.php") || exists(root, "vendor/laravel/horizon"))
	p.Reverb = has(composer, "laravel/reverb") && (exists(root, "config/reverb.php") || env(root, "BROADCAST_CONNECTION") == "reverb")
	var packageJSON packageManifest
	if b, err := os.ReadFile(filepath.Join(root, "package.json")); err != nil {
		p.Diagnostics = append(p.Diagnostics, "package.json tidak terbaca")
	} else if err := json.Unmarshal(b, &packageJSON); err != nil {
		p.Diagnostics = append(p.Diagnostics, "package.json tidak valid")
	}
	p.Vite = packageJSON.Scripts["dev"] != "" && (hasPackage(packageJSON, "vite") || exists(root, "node_modules/.bin/vite"))
	p.Scheduler = scheduleEvidence(root)
	return p
}

func exists(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}

// env membaca process environment lebih dulu (CLI environment eksplisit
// menang), lalu fallback ke subset KEY=VALUE .env Laravel.
func env(root, key string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	b, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), "\"'")
		}
	}
	return ""
}

func scheduleEvidence(root string) bool {
	patterns := []string{"->daily(", "->hourly(", "->weekly(", "->monthly(", "->every", "schedule->", "withSchedule", "Schedule::call", "Schedule::job", "Schedule::command"}
	for _, dir := range []string{"routes", "app", "bootstrap", "config"} {
		found := false
		filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error { //nolint:errcheck -- penelusuran evidence best-effort
			if err == nil && info != nil && !info.IsDir() {
				b, readErr := os.ReadFile(path)
				if readErr == nil {
					for _, pattern := range patterns {
						if strings.Contains(string(b), pattern) {
							found = true
							return filepath.SkipDir
						}
					}
				}
			}
			return nil
		})
		if found {
			return true
		}
	}
	return false
}
