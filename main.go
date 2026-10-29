package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/diagnostic"
	"github.com/maleolabs/avior/internal/mode"
	"github.com/maleolabs/avior/internal/processgroup"
	"github.com/maleolabs/avior/internal/state"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, brand.Name+":", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: %s monitor [project] | attach [project] | stop [project]", brand.Name)
	}
	project := func() (string, string, error) {
		root := ""
		if len(args) > 1 {
			root = args[1]
		} else {
			var err error
			root, err = os.Getwd()
			if err != nil {
				return "", "", err
			}
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return "", "", err
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return "", "", fmt.Errorf("project path must exist: %w", err)
		}
		abs = resolved
		statePath, processPath, err := state.Paths(abs)
		return statePath, processPath, err
	}
	if args[0] == "doctor" || args[0] == "verify" {
		return runDiagnostic(args, out)
	}
	switch args[0] {
	case "monitor":
		if len(args) > 2 {
			return fmt.Errorf("monitor takes at most one project selector")
		}
		sp, _, err := project()
		if err != nil {
			return err
		}
		st, exists, err := state.LoadState(sp)
		if err != nil {
			return err
		}
		if exists && st.MainPID.PID != 0 {
			fmt.Fprintf(out, "%s\t%d\t%d\n", st.ProjectRoot, st.MainPID.PID, st.Port)
		}
		return nil
	case "attach":
		if len(args) > 2 {
			return fmt.Errorf("attach takes at most one project selector")
		}
		sp, _, err := project()
		if err != nil {
			return err
		}
		st, exists, err := state.LoadState(sp)
		if err != nil {
			return err
		}
		if !exists || st.MainPID.PID == 0 {
			return fmt.Errorf("no runtime for selected project")
		}
		fmt.Fprintf(out, "%s\t%d\t%d\n", st.ProjectRoot, st.MainPID.PID, st.Port)
		return nil
	case "stop":
		if len(args) > 2 {
			return fmt.Errorf("stop takes at most one project selector")
		}
		sp, rp, err := project()
		if err != nil {
			return err
		}
		st, exists, err := state.LoadState(sp)
		if err != nil {
			return err
		}
		if !exists || st.MainPID.PID == 0 {
			return fmt.Errorf("no runtime for selected project")
		}
		if !processgroup.StopManagedRuntime(rp, st.MainPID.PID) {
			return fmt.Errorf("runtime for selected project not safely stoppable")
		}
		return nil
	default:
		return fmt.Errorf("unknown command %q; usage: %s monitor [project] | attach [project] | stop [project]", args[0], brand.Name)
	}
}

func runDiagnostic(args []string, out io.Writer) error {
	command := args[0]
	selected := "development"
	jsonOutput := false
	root := ""
	for _, arg := range args[1:] {
		switch arg {
		case "development", "production":
			selected = arg
		case "--json":
			jsonOutput = true
		default:
			if len(root) != 0 {
				return fmt.Errorf("%s accepts at most one project path", command)
			}
			root = arg
		}
	}
	if command == "verify" && len(args) > 1 {
		for _, arg := range args[1:] {
			if arg != "--json" && arg != "development" && arg != "production" {
				return fmt.Errorf("verify accepts only [development|production] [--json]")
			}
		}
	}
	m, err := mode.ParseMode(selected)
	if err != nil {
		return err
	}
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("project path must exist: %w", err)
	}
	report := diagnostic.Check(root, m)
	if jsonOutput {
		if err := json.NewEncoder(out).Encode(report); err != nil {
			return err
		}
	} else if err := diagnostic.Write(out, report, false); err != nil {
		return err
	}
	if !report.OK {
		if command == "verify" {
			return fmt.Errorf("verify gagal: %s", report.Missing[0])
		}
		return fmt.Errorf("doctor gagal: prerequisite belum tersedia")
	}
	return nil
}
