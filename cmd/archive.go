package cmd

import (
	"fmt"
	"io"
	"path/filepath"

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
		"production berisi runtime vendor/public/build dan binary CLI (amd64+arm64) satu OS pilihan — diambil dari release, bukan compile lokal (tanpa source tools/node_modules). " +
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
		opts := archive.Options{Root: flagRoot, Mode: m, DryRun: flagDryRun, Progress: progress}
		if m == archive.Production {
			// Fase 3: binary CLI bersumber dari release (cliassets). Versi
			// dipin ke versi CLI berjalan (D2).
			opts.CLIVersion = version.Version()
			bundle, _ := cmd.Flags().GetString("bundle-cli")
			if bundle == "" && !flagDryRun {
				if flagYes {
					// --yes tidak menebak OS server target — putusan binary
					// wajib eksplisit meskipun semua konfirmasi disetujui.
					return fmt.Errorf("--yes tanpa --bundle-cli ambigu: pilih --bundle-cli=<windows|macos|linux|none>")
				}
				if !stdinInteractive(cmd.InOrStdin()) {
					// Non-TTY tanpa putusan eksplisit: fail-closed (D1),
					// bukan menebak OS target server.
					return fmt.Errorf("mode production non-interaktif wajib --bundle-cli=<windows|macos|linux|none> (input bukan terminal)")
				}
			}
			// Versi dev (0.0.0-dev) tidak punya release binary: pinning D2
			// membuat download 404. Tolak lebih dulu kecuali bundle=none
			// atau dry-run (tidak menyentuh network): error sesegera
			// mungkin, bukan setengah jalan staging.
			if bundle != "none" && !flagDryRun && opts.CLIVersion != "" && !version.IsRelease() {
				return fmt.Errorf("versi CLI %s bukan tag rilis — binary CLI tidak tersedia di release; gunakan --bundle-cli=none atau pasang dari rilis resmi (lihat perintah selfupdate di dokumentasi)", opts.CLIVersion)
			}
			opts.BundleCLI = bundle
			if bundle == "" && !flagDryRun {
				opts.Interactive = func(w io.Writer, in io.Reader) (string, bool, error) {
					return archive.PromptBundleCLI(out, in)
				}
			}
		}
		files, err := archive.Create(opts)
		if err == nil {
			ui.CompletionCard(out, files, m == archive.Production)
		}
		return err
	},
}

func init() {
	// --bundle-cli=<windows|macos|linux|none>: putusan non-interaktif
	// untuk pemilihan binary Q4. Kosong = prompt TTY (default interaktif);
	// non-TTY tanpa flag = fail-closed (tidak nebak OS server target).
	archiveCmd.Flags().String("bundle-cli", "", "sertakan binary CLI: windows, macos, linux, atau none (non-interaktif)")
}
