package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/proxy"
	"github.com/maleolabs/avior/internal/trust"
	"github.com/spf13/cobra"
)

// trustCmd mengelola CA HTTPS lokal. Install/remove adalah jalur privileged
// (`caddy trust`/`caddy untrust` bisa memanggil sudo) — binary WAJIB lolos
// VerifyManaged (hash-before-exec, review M-1) sebelum dieksekusi;
// VerifyPATH tidak pernah menjadi gate di sini.
var trustCmd = &cobra.Command{
	Use:   "trust",
	Short: "kelola CA HTTPS lokal",
	Args:  cobra.NoArgs,
	// Tanpa subcommand dikenal, cobra menjalankan parent tanpa error —
	// tolak eksplisit agar typo tidak diam-diam print help.
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("perintah trust tidak dikenal: %s (gunakan status, install, atau remove)", args[0])
		}
		return cmd.Help()
	},
}

var trustJSON bool

// trustManager membangun manager dari binary Caddy terkelola + storage
// state per-user (brand.StateDir()/caddy), resource root = root proyek.
func trustManager() (*trust.TrustManager, error) {
	caddy, err := proxy.CaddyPath(flagRoot)
	if err != nil {
		return nil, err
	}
	return trust.NewTrustManager(caddy, filepath.Join(brand.StateDir(), "caddy"), flagRoot), nil
}

// trustConfig menulis Caddyfile trust: storage block menunjuk storage tool
// (CA yang di-trust adalah milik tool ini, bukan storage default Caddy),
// lalu site localhost tls internal agar `caddy trust` mem-provision CA.
func trustConfig(storage string) (string, error) {
	f, err := os.CreateTemp("", brand.Name+"-trust-Caddyfile-")
	if err != nil {
		return "", err
	}
	config := "{\n\tstorage file_system " + strconv.Quote(storage) + "\n}\n\nlocalhost {\n\ttls internal\n}\n"
	if _, err := f.WriteString(config); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}

var trustStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "tampilkan status trust",
	RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := trustManager()
		if err != nil {
			return err
		}
		s := m.StatusDetailed()
		out := cmd.OutOrStdout()
		if trustJSON {
			b, err := json.Marshal(s)
			if err != nil {
				return err
			}
			fmt.Fprintln(out, string(b))
			return nil
		}
		if s.Fingerprint == "" {
			fmt.Fprintln(out, "trust belum terpasang")
			return nil
		}
		fmt.Fprintf(out, "trust terpasang\n  fingerprint: %s\n  root: %s\n", s.Fingerprint, s.Root)
		return nil
	},
}

var trustInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "pasang CA lokal ke trust store",
	RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := trustManager()
		if err != nil {
			return err
		}
		// Gate hash-before-exec (review M-1): binary Caddy yang akan
		// menjalankan `caddy trust` (privileged) harus binary terkelola
		// yang terverifikasi — bukan fallback PATH.
		if err := verifyTrustBinary(); err != nil {
			return err
		}
		storage := filepath.Join(brand.StateDir(), "caddy")
		config, err := trustConfig(storage)
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(config) }()
		results, err := m.InstallTargets(config)
		for _, r := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", r.Target, r.Status)
		}
		return err
	},
}

var trustRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "hapus CA lokal dari trust store",
	RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := trustManager()
		if err != nil {
			return err
		}
		// Remove juga menjalankan binary Caddy (`caddy untrust`) — gate
		// yang sama dengan install (review M-1).
		if err := verifyTrustBinary(); err != nil {
			return err
		}
		results := m.RemoveTargets()
		failed := 0
		for _, r := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", r.Target, r.Status)
			if r.Status == trust.TrustTargetFailed {
				failed++
			}
		}
		if failed > 0 {
			return fmt.Errorf("%d target trust gagal dihapus", failed)
		}
		return nil
	},
}

// verifyTrustBinary adalah gate hash-before-exec bersama install/remove.
func verifyTrustBinary() error {
	caddy, err := proxy.CaddyPath(flagRoot)
	if err != nil {
		return err
	}
	if err := proxy.VerifyManaged(caddy); err != nil {
		return fmt.Errorf("trust menolak binary yang tidak terverifikasi: %w", err)
	}
	return nil
}

func init() {
	trustCmd.PersistentFlags().BoolVar(&trustJSON, "json", false, "output JSON")
	trustCmd.AddCommand(trustStatusCmd, trustInstallCmd, trustRemoveCmd)
}
