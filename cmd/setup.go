package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/env"
	"github.com/maleolabs/avior/internal/executor"
	"github.com/maleolabs/avior/internal/mode"
	"github.com/maleolabs/avior/internal/setup"
	"github.com/spf13/cobra"
)

const setupUsage = `Usage:  ` + brand.Name + ` setup [development|production]

Modes:
  (tanpa mode) pilih mode lewat prompt
  development  Setup lokal untuk development
  production   Setup environment production`

const setupExamples = `  ` + brand.Name + ` setup development
  ` + brand.Name + ` setup production --yes`

var setupCmd = &cobra.Command{
	Use:       "setup [development|production]",
	Short:     "setup environment development atau production",
	Long:      "Menjalankan pipeline setup proyek untuk mode development atau production.\n\n" + setupUsage,
	Example:   setupExamples,
	ValidArgs: []string{"development", "production"},
	Args: func(_ *cobra.Command, args []string) error {
		if len(args) > 1 {
			return fmt.Errorf("setup menerima paling banyak satu mode (development atau production)\n\n%s", setupUsage)
		}
		if len(args) == 1 && args[0] != "development" && args[0] != "production" {
			return fmt.Errorf("mode harus development atau production\n\n%s\n\nExamples:\n%s", setupUsage, setupExamples)
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		selected := ""
		if len(args) == 1 {
			selected = args[0]
		} else {
			var err error
			selected, err = selectMode(cmd.InOrStdin(), cmd.OutOrStdout(), stdinInteractive(cmd.InOrStdin()))
			if err != nil {
				return err
			}
		}
		m, err := mode.ParseMode(selected)
		if err != nil {
			return err
		}
		return setup.Run(setup.Options{
			Root:    flagRoot,
			Mode:    m,
			Dry:     flagDryRun,
			Yes:     flagYes,
			In:      cmd.InOrStdin(),
			Out:     cmd.OutOrStdout(),
			ReadEnv: env.ReadFile,
			Runner: executor.Runner{
				Root:   flagRoot,
				DryRun: flagDryRun,
				Out:    cmd.OutOrStdout(),
				ErrOut: cmd.ErrOrStderr(),
			},
		})
	},
}

// stdinInteractive menentukan apakah stdin adalah terminal interaktif.
// Non-*os.File (pipe/buffer, termasuk inject test) dianggap non-interaktif.
func stdinInteractive(in io.Reader) bool {
	f, ok := in.(*os.File)
	return ok && isTTY(f)
}

// selectMode prompt teks development/production bila argumen tidak
// diberikan. Non-TTY ditolak fail-closed (paritas codebase pendahulu).
// ponytail: selector interaktif ↑/↓ codebase pendahulu butuh raw-terminal
// UI (dependency TUI); prompt teks setara fungsinya tanpa dependency —
// naikkan ke selector TUI bila item TUI mendarat.
func selectMode(in io.Reader, out io.Writer, interactive bool) (string, error) {
	if !interactive {
		return "", fmt.Errorf("setup tanpa mode membutuhkan terminal interaktif; gunakan 'setup development' atau 'setup production'")
	}
	fmt.Fprint(out, "Mode [development/production] (development): ")
	var line string
	_, err := fmt.Fscanln(in, &line)
	line = strings.ToLower(strings.TrimSpace(line))
	if err != nil {
		// Enter kosong = default; error lain (EOF/pipe tertutup) ditolak.
		if line == "" && (strings.Contains(err.Error(), "unexpected newline") || err == io.EOF) {
			return "development", nil
		}
		return "", fmt.Errorf("baca mode: %w", err)
	}
	switch line {
	case "development", "production":
		return line, nil
	default:
		return "", fmt.Errorf("mode harus development atau production")
	}
}
