package cmd

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/maleolabs/avior/internal/archive"
	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/ui"
	"github.com/maleolabs/avior/internal/version"
	"github.com/spf13/cobra"
)

// archiveCmd membuat distribusi terkurasi. Mode "development" diterima
// sebagai alias main-source (paritas CLI pendahulu).
var archiveCmd = &cobra.Command{
	Use:     "archive <main-source|production>",
	Aliases: []string{"dist"},
	Short:   "buat distribusi proyek tar.gz dan zip",
	Long: "Buat arsip terkurasi. main-source berisi source aplikasi dan source tools (tanpa dependency/build/generated); " +
		"production berisi runtime vendor/public/build dan binary CLI Linux/macOS/Windows (tanpa source tools/node_modules). " +
		"Alias development tetap didukung untuk main-source.",
	Example: "  " + brand.Name + " archive main-source\n  " + brand.Name + " archive production --dry-run",
	Args: func(_ *cobra.Command, args []string) error {
		if len(args) != 1 || (args[0] != "main-source" && args[0] != "development" && args[0] != "production") {
			return fmt.Errorf("archive membutuhkan mode main-source atau production")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		m := archive.MainSource
		if args[0] == "production" {
			m = archive.Production
		}
		output := filepath.Join(flagRoot, "dist")
		out := cmd.OutOrStdout()
		ui.ArchiveIntro(out, flagRoot, output, string(m), flagDryRun)
		renderer := ui.NewProgressRenderer(out)
		defer renderer.Close()
		progress := func(event archive.ProgressEvent) {
			switch event.Status {
			case "phase":
				renderer.Phase(event.Label)
			case "started":
				renderer.Start(event.Label)
			case "skipped":
				renderer.Skipped(event.Label)
			default:
				renderer.Complete(event.Label)
			}
		}
		// Fase 3: binary CLI bersumber dari release (cliassets). Versi dipin
		// ke versi CLI berjalan (D2); OS default = OS berjalan sampai prompt
		// interaktif Fase 4 memilih.
		opts := archive.Options{Root: flagRoot, Mode: m, DryRun: flagDryRun, Progress: progress}
		if m == archive.Production && !flagDryRun {
			opts.CLIVersion = version.Version()
			opts.CLIOS = runtime.GOOS
		}
		files, err := archive.Create(opts)
		if err == nil {
			ui.CompletionCard(out, files, m == archive.Production)
		}
		return err
	},
}
