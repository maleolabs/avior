package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maleolabs/avior/internal/runtime"
	"github.com/maleolabs/avior/internal/state"
	"github.com/spf13/cobra"
)

// monitor/attach/stop di-wire penuh ke internal/runtime (review H-1/H-2):
// stop menunggu exit + membersihkan state milik owner, monitor/attach cek
// ProcessAlive dan menyertakan Port saat serve mencatatnya. Output tabular
// ringkas: project \t pid [\t port]; port 0 palsu tidak pernah dicetak.

// runtimeRow merender satu baris status kontekstual (monitor/attach):
// name<TAB>status<TAB>pid<TAB>url<TAB>ports<TAB>root.
func runtimeRow(st runtime.ProjectStatus) string {
	status := "stopped"
	if st.Running {
		status = "running"
	}
	row := fmt.Sprintf("%s\t%s\t%d", displayName(st), status, st.State.MainPID.PID)
	if st.URL != "" {
		row += "\t" + st.URL
	} else {
		row += "\t-"
	}
	row += "\t" + portsField(st.State)
	row += "\t" + st.State.ProjectRoot
	return row
}

// displayName memilih nama proyek (state v2) atau basename root (v1).
func displayName(st runtime.ProjectStatus) string {
	name := st.ProjectName
	if name == "" {
		name = filepath.Base(st.ProjectRoot)
	}
	if name == "" {
		name = "-"
	}
	return name
}

// portsField merender kumpulan port ("app=8000 caddy=8080 admin=2019").
// Legacy state v1 (hanya Port) dipetakan ke app.
func portsField(st state.RuntimeState) string {
	app := st.Ports.App
	if app == 0 {
		app = st.Port
	}
	var parts []string
	if app != 0 {
		parts = append(parts, "app="+strconv.Itoa(app))
	}
	if st.Ports.Vite != 0 {
		parts = append(parts, "vite="+strconv.Itoa(st.Ports.Vite))
	}
	if st.Ports.Caddy != 0 {
		parts = append(parts, "caddy="+strconv.Itoa(st.Ports.Caddy))
	}
	if st.Ports.CaddyAdmin != 0 {
		parts = append(parts, "admin="+strconv.Itoa(st.Ports.CaddyAdmin))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

// headerRow merender header tabel monitor.
func headerRow() string {
	return strings.Join([]string{"PROJECT", "STATUS", "PID", "URL", "PORTS", "ROOT"}, "\t")
}

// resolveProject mengembalikan path absolut proyek dari argumen opsional
// (default: cwd). Selector menerima: path, token, atau nama proyek
// (state.FindProject). Path harus ada (symlink-resolved); token/nama hanya
// bila proyek terdaftar di index.
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
		return resolvePathSelector(root)
	}
	// Selector non-path: cek index (token/name) sebelum memperlakukan
	// sebagai path.
	if entry, found, _ := state.FindProject(root); found {
		if entry.Root != "" {
			return entry.Root, nil
		}
	}
	return resolvePathSelector(root)
}

// resolvePathSelector menyelesaikan path absolut (symlink-resolved).
func resolvePathSelector(root string) (string, error) {
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
			out := cmd.OutOrStdout()
			anyRunning := false
			for _, st := range statuses {
				if st.Running {
					anyRunning = true
					break
				}
			}
			if !anyRunning {
				fmt.Fprintln(out, "tidak ada runtime aktif")
				return nil
			}
			fmt.Fprintln(out, headerRow())
			for _, st := range statuses {
				if st.Running {
					fmt.Fprintln(out, runtimeRow(st))
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
