package diagnostic

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/detect"
	"github.com/maleolabs/avior/internal/mode"
	"github.com/maleolabs/avior/internal/ui"
	"github.com/maleolabs/avior/internal/version"
)

type Report struct {
	Mode mode.Mode `json:"mode"`
	// Version versi CLI (diagnostik cepat; kosong bila pemanggil tidak
	// ingin menampilkannya). Tidak di-omit agar JSON tetap stabil.
	Version string          `json:"version"`
	OK      bool            `json:"ok"`
	Checks  []detect.Result `json:"checks"`
	Missing []string        `json:"missing,omitempty"`
}

// Check menjalankan prerequisite check untuk mode terpilih. Report.Version
// diisi dari paket version agar header doctor/verify selalu menampakkan
// versi CLI (sumber tunggal — lihat doc internal/version).
func Check(root string, m mode.Mode) Report {
	checks := detect.ProjectMode(root, m)
	report := Report{Mode: m, OK: true, Checks: checks, Version: version.Version()}
	for _, check := range checks {
		if !check.OK {
			report.OK = false
			report.Missing = append(report.Missing, check.Name)
		}
	}
	return report
}

func Write(w io.Writer, report Report, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(report)
	}
	width := ui.TermWidth()
	header := brand.Name + " · " + string(report.Mode)
	if report.Version != "" {
		header += " · v" + report.Version
	}
	if _, err := fmt.Fprintf(w, "\n╭─ %s\n╰─\n", header); err != nil {
		return err
	}
	nameW := 24
	markW := 10
	detailW := width - nameW - markW - 6
	if detailW < 20 {
		detailW = 20
	}
	for _, check := range report.Checks {
		mark := ui.SuccessText("✓ READY")
		if !check.OK {
			mark = ui.DangerText("✗ MISSING")
		}
		detail := ui.Truncate(check.Detail, detailW)
		if _, err := fmt.Fprintf(w, "  %-*s %-*s %s\n", nameW, ui.Truncate(check.Name, nameW), markW, mark, detail); err != nil {
			return err
		}
	}
	return nil
}
