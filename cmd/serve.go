package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/maleolabs/avior/internal/mode"
	"github.com/maleolabs/avior/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	serveHost          string
	servePort          int
	serveVitePort      int
	serveCaddyPort     int
	serveDomain        string
	serveVirtualDomain bool
	serveHTTPS         bool
	serveTrustLocalCA  bool
	serveCertStorage   string
	serveHosts         bool
	serveQueue         bool
	serveScheduler     bool
	serveOutput        string
)

// serveCmd mengekspos runtime supervisor (dev: Laravel+Horizon+scheduler+
// Pail+Vite; prod: Caddy virtual domain + worker). Kebijakan ada di
// internal/runtime.Serve — command ini wiring flag + consent gate saja.
var serveCmd = &cobra.Command{
	Use:   "serve [development|production]",
	Short: "jalankan runtime aplikasi",
	Long:  "Menjalankan proses runtime Laravel secara terkoordinasi. Default flag berorientasi production; mode development menurunkan virtual-domain/HTTPS/trust kecuali di-set eksplisit.\n\nUsage: serve [development|production]",
	Args: func(_ *cobra.Command, args []string) error {
		if len(args) > 1 {
			return fmt.Errorf("serve menerima paling banyak satu mode")
		}
		if len(args) == 1 && args[0] != "development" && args[0] != "production" {
			return fmt.Errorf("mode harus development atau production")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		selected := "development"
		if len(args) == 1 {
			selected = args[0]
		}
		m, err := mode.ParseMode(selected)
		if err != nil {
			return err
		}
		// Consent hosts fail-closed: mengubah hosts butuh --yes eksplisit.
		if serveHosts && !flagYes {
			return fmt.Errorf("--hosts membutuhkan --yes untuk mengubah hosts")
		}
		if serveOutput != "concise" && serveOutput != "detail" {
			return fmt.Errorf("--output harus concise atau detail")
		}
		// Fix p0: flag queue/scheduler otoritatif saat di-set (termasuk
		// --queue=false); unset = auto-detect dari evidence proyek.
		var queue, scheduler *bool
		if cmd.Flags().Changed("queue") {
			queue = &serveQueue
		}
		if cmd.Flags().Changed("scheduler") {
			scheduler = &serveScheduler
		}
		nonInteractive := !stdinInteractive(cmd.InOrStdin())
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return runtime.Serve(ctx, runtime.ServeOptions{
			Root:           flagRoot,
			Mode:           m,
			Host:           serveHost,
			Port:           servePort,
			VitePort:       serveVitePort,
			CaddyPort:      serveCaddyPort,
			Domain:         serveDomain,
			VirtualDomain:  serveVirtualDomain,
			HTTPS:          serveHTTPS,
			TrustLocalCA:   serveTrustLocalCA,
			CertStorage:    serveCertStorage,
			Hosts:          serveHosts,
			Queue:          queue,
			Scheduler:      scheduler,
			Changed:        map[string]bool{"virtual-domain": cmd.Flags().Changed("virtual-domain"), "https": cmd.Flags().Changed("https"), "trust-local-ca": cmd.Flags().Changed("trust-local-ca")},
			NonInteractive: nonInteractive,
			OutputMode:     runtime.OutputMode(serveOutput),
			Out:            cmd.OutOrStdout(),
		})
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveHost, "host", "127.0.0.1", "alamat bind server")
	serveCmd.Flags().IntVar(&servePort, "port", 8000, "port Laravel")
	serveCmd.Flags().IntVar(&serveVitePort, "vite-port", 5173, "port Vite development")
	serveCmd.Flags().StringVar(&serveDomain, "domain", runtime.DefaultDomain(), "virtual domain lokal (.localhost tanpa hosts; .test perlu --hosts)")
	serveCmd.Flags().IntVar(&serveCaddyPort, "caddy-port", 8080, "port proxy Caddy (localhost-only)")
	// Default production (deploy-like); NormalizeServeOptions menurunkannya
	// untuk development kecuali flag di-set eksplisit.
	serveCmd.Flags().BoolVar(&serveVirtualDomain, "virtual-domain", true, "aktifkan Caddy reverse proxy")
	serveCmd.Flags().BoolVar(&serveHTTPS, "https", true, "HTTPS lokal via Caddy internal CA")
	serveCmd.Flags().BoolVar(&serveTrustLocalCA, "trust-local-ca", true, "setujui penyimpanan CA/cert lokal (eksplisit)")
	serveCmd.Flags().StringVar(&serveCertStorage, "cert-storage", "", "direktori storage sertifikat lokal (mode 0700)")
	serveCmd.Flags().BoolVar(&serveHosts, "hosts", false, "tambahkan domain ke hosts (eksplisit; gunakan --yes)")
	serveCmd.Flags().BoolVar(&serveQueue, "queue", false, "jalankan queue worker (otoritatif saat di-set)")
	serveCmd.Flags().BoolVar(&serveScheduler, "scheduler", false, "jalankan scheduler (otoritatif saat di-set)")
	serveCmd.Flags().StringVar(&serveOutput, "output", "concise", "output runtime: concise atau detail")
}
