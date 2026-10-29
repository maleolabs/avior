package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/maleolabs/avior/internal/brand"
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
		return fmt.Errorf("usage: %s monitor | attach [project] | stop [project]", brand.Name)
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
		statePath, processPath, err := state.Paths(abs)
		return statePath, processPath, err
	}
	switch args[0] {
	case "monitor":
		if len(args) != 1 {
			return fmt.Errorf("monitor takes no project selector")
		}
		matches, err := filepath.Glob(filepath.Join(brand.StateDir(), "runtime-state-*.json"))
		if err != nil {
			return err
		}
		for _, path := range matches {
			st, exists, err := state.LoadState(path)
			if err != nil {
				return err
			}
			if exists && st.MainPID.PID != 0 {
				fmt.Fprintf(out, "%s\t%d\t%d\n", st.ProjectRoot, st.MainPID.PID, st.Port)
			}
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
		return fmt.Errorf("unknown command %q; usage: %s monitor | attach [project] | stop [project]", args[0], brand.Name)
	}
}
