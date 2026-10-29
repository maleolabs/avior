package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/env"
)

// Manifest carries optional setup values.
type Manifest struct {
	Services    []string      `json:"services,omitempty"`
	Setup       ManifestSetup `json:"setup,omitempty"`
	EnvRequired []string      `json:"env_required,omitempty"`
}

type ManifestSetup struct {
	Skip  []string       `json:"skip,omitempty"`
	Steps []ManifestStep `json:"steps,omitempty"`
}

type ManifestStep struct {
	Name string   `json:"name"`
	Run  string   `json:"run"`
	Args []string `json:"args,omitempty"`
}

func readManifest(root string) (Manifest, error) {
	path := filepath.Join(root, brand.ManifestFile)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Manifest{}, nil
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest %s: %w", path, err)
	}
	var m Manifest
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("manifest %s: %w", path, err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Manifest{}, fmt.Errorf("manifest %s: trailing JSON data", path)
	}
	for _, name := range m.Setup.Skip {
		if !knownSetupStep(name) {
			return Manifest{}, fmt.Errorf("manifest %s: setup.skip contains unknown step %q", path, name)
		}
	}
	for _, name := range m.EnvRequired {
		if err := env.ValidateKey(name); err != nil {
			return Manifest{}, fmt.Errorf("manifest %s: env_required: %w", path, err)
		}
	}
	for i, step := range m.Setup.Steps {
		if strings.TrimSpace(step.Name) == "" || strings.TrimSpace(step.Run) == "" {
			return Manifest{}, fmt.Errorf("manifest %s: setup.steps[%d] requires name and run", path, i)
		}
		if strings.ContainsAny(step.Run, `/\\`) || filepath.Base(step.Run) != step.Run || step.Run == "." || step.Run == ".." {
			return Manifest{}, fmt.Errorf("manifest %s: setup.steps[%d].run must be executable name, not path", path, i)
		}
	}
	for _, service := range m.Services {
		if service != "queue" && service != "scheduler" && service != "frontend" {
			return Manifest{}, fmt.Errorf("manifest %s: services contains unsupported value %q", path, service)
		}
	}
	for _, key := range m.EnvRequired {
		if err := env.ValidateKey(key); err != nil {
			return Manifest{}, fmt.Errorf("manifest %s: env_required: %w", path, err)
		}
	}
	return m, nil
}

func knownSetupStep(name string) bool {
	switch name {
	case "deps:php", "deps:js", "assets:build", "db:migrate", "db:seed", "storage:link", "cache:clear", "cache:optimize":
		return true
	default:
		return false
	}
}
