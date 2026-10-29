package cmd

import (
	"fmt"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/diagnostic"
	"github.com/maleolabs/avior/internal/mode"
	"github.com/spf13/cobra"
)

// doctor/verify mempertahankan perilaku main.go sebelumnya: laporan
// diagnostic per mode, --json untuk konsumsi mesin, exit 1 saat
// prerequisite belum tersedia.

// jsonOnceFlag menolak --json ganda — paritas dengan parser hand-rolled
// sebelumnya ("accepts --json only once"); cobra bool biasa diam menerima
// pengulangan.
type jsonOnceFlag struct{ set bool }

func (f *jsonOnceFlag) String() string { return "false" }
func (f *jsonOnceFlag) Set(string) error {
	if f.set {
		return fmt.Errorf("--json hanya boleh sekali")
	}
	f.set = true
	return nil
}
func (f *jsonOnceFlag) Type() string     { return "bool" }
func (f *jsonOnceFlag) IsBoolFlag() bool { return true }

var doctorJSON jsonOnceFlag
var verifyJSON jsonOnceFlag

var doctorCmd = &cobra.Command{
	Use:   "doctor [development|production]",
	Short: "periksa prerequisite proyek",
	Long:  "Periksa prerequisite proyek untuk mode terpilih.\n\nUsage: " + brand.Name + " doctor [development|production]",
	Args: func(_ *cobra.Command, args []string) error {
		if len(args) > 1 {
			return fmt.Errorf("doctor menerima paling banyak satu mode")
		}
		if len(args) == 1 && args[0] != "development" && args[0] != "production" {
			return fmt.Errorf("mode harus development atau production")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		selected := "development"
		if len(args) == 1 {
			selected = args[0]
		}
		m, err := mode.ParseMode(selected)
		if err != nil {
			return err
		}
		report := diagnostic.Check(flagRoot, m)
		if doctorJSON.set {
			if err := diagnostic.Write(cmd.OutOrStdout(), report, true); err != nil {
				return err
			}
		} else if err := diagnostic.Write(cmd.OutOrStdout(), report, false); err != nil {
			return err
		}
		if !report.OK {
			return fmt.Errorf("doctor gagal: prerequisite belum tersedia")
		}
		return nil
	},
}

var verifyCmd = &cobra.Command{
	Use:   "verify [development|production]",
	Short: "verifikasi konfigurasi proyek",
	Long:  "Verifikasi konfigurasi proyek untuk mode terpilih; gagal bila ada requirement yang belum terpenuhi.\n\nUsage: " + brand.Name + " verify [development|production]",
	Args:  doctorCmd.Args,
	RunE: func(cmd *cobra.Command, args []string) error {
		selected := "development"
		if len(args) == 1 {
			selected = args[0]
		}
		m, err := mode.ParseMode(selected)
		if err != nil {
			return err
		}
		report := diagnostic.Check(flagRoot, m)
		if verifyJSON.set {
			if err := diagnostic.Write(cmd.OutOrStdout(), report, true); err != nil {
				return err
			}
			if !report.OK {
				return fmt.Errorf("verify failed; see JSON report (ok=false)")
			}
			return nil
		}
		if err := diagnostic.Write(cmd.OutOrStdout(), report, false); err != nil {
			return err
		}
		if !report.OK {
			return fmt.Errorf("verify gagal: %s", report.Missing[0])
		}
		return nil
	},
}

func init() {
	// NoOptDefVal eksplisit: flag custom Var() tidak otomatis bool-flag,
	// sehingga --json tanpa nilai ditolak "needs an argument".
	f := doctorCmd.Flags().VarPF(&doctorJSON, "json", "", "output JSON")
	f.NoOptDefVal = "true"
	f = verifyCmd.Flags().VarPF(&verifyJSON, "json", "", "output JSON")
	f.NoOptDefVal = "true"
}
