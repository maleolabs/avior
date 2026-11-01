// Package cmd adalah surface CLI tool (item m2-cli-cobra-structure): cobra
// command tree, help ter-style, error card, dan flag validation. Semua
// command menerima writer via cobra (OutOrStdout/ErrOrStderr) — tidak ada
// fmt.Print* ke os.Stdout langsung di paket ini; satu-satunya pengecualian
// adalah entry main.go.
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/ui"
	"github.com/spf13/cobra"
)

var (
	flagRoot   string
	flagDryRun bool
	flagYes    bool
)

const helpTemplate = `{{with .Long}}{{renderHelpText .}}{{end}}{{with .Example}}

{{styleDefault "Examples:"}}
{{renderExamples .}}{{end}}{{if .HasAvailableSubCommands}}
{{styleDefault "Commands:"}}
{{renderCommands .}}{{end}}{{if .HasAvailableLocalFlags}}

{{styleDefault "Flags:"}}
{{renderFlags .}}{{end}}

`

func renderHelpText(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "Usage:" || trimmed == "Modes:" || trimmed == "Examples:" || trimmed == "Commands:" || trimmed == "Flags:":
			lines[i] = ui.DefaultText(trimmed)
		case strings.HasPrefix(trimmed, "development") || strings.HasPrefix(trimmed, "production"):
			parts := strings.Fields(trimmed)
			lines[i] = ui.InfoText(parts[0]) + ui.DimText(strings.TrimPrefix(trimmed, parts[0]))
		case strings.HasPrefix(trimmed, "Usage:"):
			lines[i] = ui.DefaultText("Usage:") + ui.InfoText(strings.TrimPrefix(trimmed, "Usage:"))
		default:
			lines[i] = ui.DimText(line)
		}
	}
	return strings.Join(lines, "\n")
}

func renderExamples(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			lines[i] = ui.InfoText(line)
		}
	}
	return strings.Join(lines, "\n")
}

func renderCommands(cmd *cobra.Command) string {
	available := make([]*cobra.Command, 0, len(cmd.Commands()))
	width := 0
	for _, child := range cmd.Commands() {
		if child.IsAvailableCommand() {
			available = append(available, child)
			if len(child.Name()) > width {
				width = len(child.Name())
			}
		}
	}
	termW := ui.TermWidth()
	descW := termW - width - 6
	if descW < 20 {
		descW = 20
	}
	var b strings.Builder
	for _, child := range available {
		name := ui.InfoText(child.Name())
		pad := strings.Repeat(" ", width-len(child.Name()))
		descLines := ui.Wrap(child.Short, descW)
		if len(descLines) == 0 {
			descLines = []string{""}
		}
		fmt.Fprintf(&b, "  %s%s  %s\n", name, pad, ui.DimText(descLines[0]))
		indent := strings.Repeat(" ", width+4)
		for _, dl := range descLines[1:] {
			fmt.Fprintf(&b, "%s%s\n", indent, ui.DimText(dl))
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// renderFlags mempertahankan format flag Cobra (termasuk default), sambil
// men-style hanya kolom flag. Men-style seluruh output FlagUsages membuat
// nama dan default tidak terbaca sebagai teks redup.
func renderFlags(cmd *cobra.Command) string {
	lines := strings.Split(strings.TrimSuffix(cmd.Flags().FlagUsages(), "\n"), "\n")
	termW := ui.TermWidth()
	// cari kolom deskripsi bersama (cobra menyelaraskan)
	maxDesc := 0
	for _, line := range lines {
		if !strings.HasPrefix(line, "  ") || strings.TrimSpace(line) == "" {
			continue
		}
		j := len(line) - 1
		descStart := -1
		for j >= 2 {
			if line[j] != ' ' {
				j--
				continue
			}
			k := j
			for k >= 2 && line[k] == ' ' {
				k--
			}
			if j-k >= 2 {
				descStart = k + 1
				break
			}
			j = k
		}
		if descStart > maxDesc {
			maxDesc = descStart
		}
	}
	if maxDesc == 0 {
		maxDesc = 2
	}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !strings.HasPrefix(line, "  ") {
			lines[i] = ui.DimText(line)
			continue
		}
		j := len(line) - 1
		descStart := -1
		for j >= 2 {
			if line[j] != ' ' {
				j--
				continue
			}
			k := j
			for k >= 2 && line[k] == ' ' {
				k--
			}
			if j-k >= 2 {
				descStart = k + 1
				break
			}
			j = k
		}
		if descStart < 0 {
			lines[i] = ui.InfoText(strings.TrimRight(line, " "))
			continue
		}
		flagPart := strings.TrimRight(line[:descStart], " ")
		desc := strings.TrimSpace(line[descStart:])
		if desc == "" {
			lines[i] = ui.InfoText(flagPart)
			continue
		}
		descW := termW - maxDesc
		if descW < 20 {
			descW = 20
		}
		wrapped := ui.Wrap(desc, descW)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		out := ui.InfoText(flagPart) + strings.Repeat(" ", maxDesc-len(flagPart)) + "  " + ui.DimText(wrapped[0])
		indent := strings.Repeat(" ", maxDesc+2)
		for _, w := range wrapped[1:] {
			out += "\n" + indent + ui.DimText(w)
		}
		lines[i] = out
	}
	return strings.Join(lines, "\n")
}

var rootCmd = &cobra.Command{
	Use:   brand.Name,
	Short: "",
	Long:  "",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintln(cmd.OutOrStdout(), ui.Banner())
		_ = cmd.Help()
	},
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		// Perintah read-only lintas direktori (monitor) tidak butuh root.
		if rootless[cmd.Name()] {
			return nil
		}
		if flagRoot == "" {
			wd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("root tidak dapat ditentukan: %w", err)
			}
			flagRoot = wd
		}
		abs, err := filepath.Abs(flagRoot)
		if err != nil {
			return err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("root tidak dapat diakses: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("root bukan direktori: %s", abs)
		}
		flagRoot = abs
		return nil
	},
}

// rootless berisi command yang tidak memvalidasi --root.
var rootless = map[string]bool{"monitor": true, "verify-release": true}

func init() {
	rootCmd.SetHelpTemplate(helpTemplate)
	rootCmd.SetUsageTemplate(helpTemplate)
	cobra.AddTemplateFunc("stylePrimary", ui.PrimaryText)
	cobra.AddTemplateFunc("styleDim", ui.DimText)
	cobra.AddTemplateFunc("styleDefault", ui.DefaultText)
	cobra.AddTemplateFunc("renderHelpText", renderHelpText)
	cobra.AddTemplateFunc("renderExamples", renderExamples)
	cobra.AddTemplateFunc("renderCommands", renderCommands)
	cobra.AddTemplateFunc("renderFlags", renderFlags)
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return fmt.Errorf("%s\n%s", err, ui.DefaultText("Usage:")+ui.InfoText(" "+cmd.UseLine()))
	})
	rootCmd.PersistentFlags().StringVar(&flagRoot, "root", "", "root proyek Laravel (default: direktori saat ini)")
	rootCmd.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "tampilkan aksi tanpa menjalankan")
	rootCmd.PersistentFlags().BoolVarP(&flagYes, "yes", "y", false, "non-interactive, setujui semua konfirmasi")
	rootCmd.AddCommand(doctorCmd, verifyCmd, monitorCmd, attachCmd, stopCmd, setupCmd, serveCmd, archiveCmd, trustCmd, verifyReleaseCmd)
}

// isTTY mendeteksi file karakter (terminal interaktif). Stdlib saja.
func isTTY(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// RunIn seperti Run dengan stdin di-inject (testability: non-TTY paths).
func RunIn(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flagRoot, flagDryRun, flagYes, trustJSON = "", false, false, false
	doctorJSON, verifyJSON = jsonOnceFlag{}, jsonOnceFlag{}
	rootCmd.SetIn(stdin)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs(args)
	return rootCmd.Execute()
}

// Run mengeksekusi satu invocation CLI dengan writer yang di-inject.
// Dipakai main (writer = os.Stdout/Stderr) dan test (buffer). Flag package
// level di-reset agar test bisa memanggil berulang pada satu proses.
func Run(args []string, stdout, stderr io.Writer) error {
	return RunIn(args, os.Stdin, stdout, stderr)
}

// Execute adalah entry produksi: render error card untuk kegagalan
// user-facing lalu exit 1. Error ReportedError sudah me-render kartunya
// sendiri di titik gagal — jangan render dobel.
func Execute() {
	err := Run(os.Args[1:], os.Stdout, os.Stderr)
	if err == nil {
		return
	}
	var reported ui.ReportedError
	if !errors.As(err, &reported) {
		ui.RenderError(os.Stderr, err)
	}
	os.Exit(1)
}
