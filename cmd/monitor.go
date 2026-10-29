package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/maleolabs/avior/internal/runtime"
	"github.com/spf13/cobra"
)

// monitor/attach/stop di-wire penuh ke internal/runtime (review H-1/H-2):
// stop menunggu exit + membersihkan state milik owner, monitor/attach cek
// ProcessAlive dan menyertakan Port saat serve mencatatnya. Output tabular
// ringkas: project \t pid [\t port]; port 0 palsu tidak pernah dicetak.

func runtimeRow(st runtime.ProjectStatus) string {
	row := fmt.Sprintf("%s\t%d", st.State.ProjectRoot, st.State.MainPID.PID)
	if st.State.Port != 0 {
		row += "\t" + strconv.Itoa(st.State.Port)
	}
	return row
}

// resolveProject mengembalikan path absolut proyek dari argumen opsional
// (default: cwd). Selector dipakai langsung oleh runtime (state keyed per
// root, symlink-resolved).
func resolveProject(args []string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("menerima paling banyak satu selector proyek")
	}
	root := ""
	if len(args) == 1 {
		root = args[0]
	} else {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("path proyek harus ada: %w", err)
	}
	return resolved, nil
}

var monitorCmd = &cobra.Command{
	Use:   "monitor [project]",
	Short: "tampilkan status semua runtime yang dikelola",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			statuses, err := runtime.Monitor()
			if err != nil {
				return err
			}
			for _, st := range statuses {
				if st.Running {
					fmt.Fprintln(cmd.OutOrStdout(), runtimeRow(st))
				}
			}
			return nil
		}
		project, err := resolveProject(args)
		if err != nil {
			return err
		}
		st, err := runtime.Attach(project)
		if err != nil {
			return err
		}
		if st.Running {
			fmt.Fprintln(cmd.OutOrStdout(), runtimeRow(st))
		}
		return nil
	},
}

var attachCmd = &cobra.Command{
	Use:   "attach [project]",
	Short: "tampilkan runtime proyek terpilih",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := resolveProject(args)
		if err != nil {
			return err
		}
		st, err := runtime.Attach(project)
		if err != nil {
			return err
		}
		if !st.Running {
			return fmt.Errorf("tidak ada runtime untuk proyek terpilih")
		}
		fmt.Fprintln(cmd.OutOrStdout(), runtimeRow(st))
		return nil
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop [project]",
	Short: "hentikan runtime proyek terpilih",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := resolveProject(args)
		if err != nil {
			return err
		}
		// Attach dulu agar "tidak ada runtime" dilaporkan eksplisit;
		// runtime.Stop sendiri mengurus wait-for-exit + pembersihan state.
		st, err := runtime.Attach(project)
		if err != nil {
			return err
		}
		if !st.Running {
			return fmt.Errorf("tidak ada runtime untuk proyek terpilih")
		}
		return runtime.Stop(project)
	},
}
