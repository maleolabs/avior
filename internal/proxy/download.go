package proxy

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// FetchCaddy downloads official pinned Caddy archive without shell tools.
func FetchCaddy(ctx context.Context, destination string) error {
	url := fetchArchiveURL()
	if urlOverride != nil {
		url = urlOverride()
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download Caddy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download Caddy: HTTP %s", resp.Status)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("read Caddy archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	want := "caddy"
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(h.Name) != want || h.Typeflag != tar.TypeReg {
			continue
		}
		if h.Size <= 0 || h.Size > 100<<20 {
			return fmt.Errorf("Caddy binary size invalid: %d", h.Size)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0755)
		if runtime.GOOS == "windows" {
			mode = 0644
		}
		f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(f, tr, h.Size)
		closeErr := f.Close()
		if copyErr != nil {
			os.Remove(destination)
			return copyErr
		}
		if closeErr != nil {
			os.Remove(destination)
			return closeErr
		}
		return nil
	}
	return fmt.Errorf("Caddy archive does not contain %s", want)
}

var urlOverride func() string

func fetchArchiveURL() string {
	name := "caddy_" + CaddyVersion + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	return "https://github.com/caddyserver/caddy/releases/download/v" + CaddyVersion + "/" + name
}
