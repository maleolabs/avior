package diagnostic

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/detect"
	"github.com/maleolabs/avior/internal/mode"
)

type Report struct {
	Mode    mode.Mode       `json:"mode"`
	OK      bool            `json:"ok"`
	Checks  []detect.Result `json:"checks"`
	Missing []string        `json:"missing,omitempty"`
}

func Check(root string, m mode.Mode) Report {
	checks := detect.ProjectMode(root, m)
	report := Report{Mode: m, OK: true, Checks: checks}
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
	if _, err := fmt.Fprintf(w, "\n╭─ %s · %s\n╰─\n", brand.Name, report.Mode); err != nil {
		return err
	}
	for _, check := range report.Checks {
		mark := "✓ READY"
		if !check.OK {
			mark = "✗ MISSING"
		}
		if _, err := fmt.Fprintf(w, "  %-24s %-10s %s\n", check.Name, mark, check.Detail); err != nil {
			return err
		}
	}
	return nil
}
