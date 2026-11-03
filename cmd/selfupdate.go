package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/maleolabs/avior/internal/brand"
	"github.com/maleolabs/avior/internal/cliassets"
	"github.com/maleolabs/avior/internal/version"
	"github.com/spf13/cobra"
)

// selfupdateCmd memperbarui binary CLI host ke release terbaru
// (docs/archive-release-distribution-plan.md Q2):
//
//   - Versi target = LatestVersion (follow-latest, BERBEDA dengan
//     archive yang dipin — selfupdate di luar arsip supaya arsip
//     tetap reproduksibel D2).
//   - Binary diunduh dari aset release yang sudah diverifikasi
//     SHA256SUMS (Kontrak A), lalu dipasang dengan rename atomik.
//   - --gc membersihkan versi cache lama (StateDir/cli-binaries/v*/),
//     tanpa menyentuh binary host.
//
// Batas kejujuran: selfupdate mengganti binary yang sedang berjalan.
// Penghapusan/penggantian tidak bisa di-rollback setelah rename;
// konfirmasi default ON kecuali --yes.
var selfupdateCmd = &cobra.Command{
	Use:   "selfupdate",
	Short: "perbarui binary CLI ke release terbaru",
	Long: "Perbarui binary CLI ini ke release terbaru. Berbeda dengan archive production (yang memin versi binary ke versi CLI berjalan), selfupdate selalu mengambil release paling baru.\n\n" +
		"Flag --gc membersihkan cache binary versi lama tanpa melakukan update.",
	Example: "  " + brand.Name + " selfupdate\n  " + brand.Name + " selfupdate --gc\n  " + brand.Name + " selfupdate --yes",
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		gc, _ := cmd.Flags().GetBool("gc")
		// flagYes persistent root: --yes = konfirmasi update otomatis.
		yes := flagYes

		if gc {
			// GC berdiri sendiri: bersihkan cache tanpa update.
			removed, err := cliassets.GarbageCollect()
			if err != nil {
				return fmt.Errorf("pembersihan cache binary gagal: %w", err)
			}
			if len(removed) == 0 {
				fmt.Fprintln(out, "Cache binary bersih — tidak ada versi lama untuk dihapus.")
				return nil
			}
			fmt.Fprintf(out, "Cache binary dibersihkan (%d versi lama):\n", len(removed))
			for _, dir := range removed {
				fmt.Fprintf(out, "  %s\n", dir)
			}
			return nil
		}

		// Versi sumber selfupdate tidak perlu rilis resmi — update itu
		// sendiri yang menuju rilis. Yang dicek: tag target valid.
		cur := version.Version()
		if cur == "" {
			cur = "(dev)"
		}

		fmt.Fprintf(out, "Memeriksa release terbaru…\n")
		ctx := context.Background()
		tag, err := cliassets.LatestVersion(ctx)
		if err != nil {
			return fmt.Errorf("tidak dapat menemukan release terbaru: %w", err)
		}
		target := strings.TrimPrefix(tag, "v")
		if target == cur {
			fmt.Fprintf(out, "CLI %s sudah versi terbaru.\n", cur)
			return nil
		}

		if !yes {
			fmt.Fprintf(out, "\nCLI %s → %s\n", cur, target)
			fmt.Fprint(out, "Ganti binary CLI ini? [y/N]: ")
			var answer string
			_, err := fmt.Fscanln(cmd.InOrStdin(), &answer)
			if err != nil && err != io.EOF {
				return fmt.Errorf("membaca konfirmasi: %w", err)
			}
			if !isYes(answer) {
				fmt.Fprintln(out, "Update dibatalkan.")
				return nil
			}
		}

		if err := installLatest(ctx, out, target); err != nil {
			return err
		}
		fmt.Fprintf(out, "CLI diperbarui ke %s. Jalankan '%s --help' untuk verifikasi.\n", target, brand.Name)
		return nil
	},
}

func init() {
	selfupdateCmd.Flags().Bool("gc", false, "bersihkan cache binary versi lama (tanpa update)")
	// --yes persistent root flag dipakai bersama (paritas konfirmasi
	// lain); tidak ada shorthand lokal.
	rootCmd.AddCommand(selfupdateCmd)
}

// isYes menerima bentuk afirmasi umum (paritas confirm internal/setup).
func isYes(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}

// installLatest mengunduh binary host (arch berjalan) dari release target
// dan mengganti executable saat ini secara atomik. Fail-closed: binary
// host tidak disentuh sebelum checksum SHA256SUMS cocok.
func installLatest(ctx context.Context, out io.Writer, target string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("lokasi binary CLI tidak dapat ditentukan: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("menresolve path binary: %w", err)
	}

	// Unduh ke cache (diverifikasi di sana), lalu pasang dari cache.
	// Cache dulu juga memanfaatkan race-safety cliassets.
	cache := filepath.Join(brand.StateDir(), "cli-binaries")
	fmt.Fprintf(out, "Mengunduh binary %s_%s_%s…\n", brand.Name, runtime.GOOS, runtime.GOARCH)
	paths, err := cliassets.EnsureBinaries(ctx, target, cache, runtime.GOOS)
	if err != nil {
		return fmt.Errorf("mengunduh binary rilis: %w", err)
	}
	// EnsureBinaries mengembalikan [amd64, arm64] — pilih arch berjalan.
	src := paths[0]
	if runtime.GOARCH == "arm64" {
		src = paths[1]
	}

	tmp := exe + ".new"
	if err := copyTo(src, tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Rename atomik: sistem file mengganti referensi dalam satu langkah;
	// proses yang sedang berjalan tetap memakai inode lama (aman di
	// Linux/macOS; Windows memerlukan rename setelah reboot — ponytail:
	// tidak ditangani item ini, error os.Rename muncul jelas).
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mengganti binary %s: %w", exe, err)
	}
	_ = os.Chmod(exe, 0o755)
	return nil
}

// copyTo menyalin binary terverifikasi ke tujuan, mempertahankan mode
// executable asli.
func copyTo(src, dst, ref string) error {
	info, err := os.Stat(ref)
	if err != nil {
		return fmt.Errorf("membaca mode binary saat ini: %w", err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("membuka binary terunduh: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("menulis binary pengganti: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("menyalin binary: %w", err)
	}
	return out.Close()
}
