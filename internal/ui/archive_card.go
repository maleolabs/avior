// Kartu intro/completion untuk perintah archive (port codebase pendahulu,
// writer-injected: fungsi menulis ke io.Writer, bukan mengembalikan string
// yang dicetak via fmt.Print global).
package ui

import (
	"fmt"
	"io"
	"strings"
)

// CompletionCard merender ringkasan hasil archive ke w.
func CompletionCard(w io.Writer, files []string, production bool) {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(PrimaryText("╭─ "))
	b.WriteString(SuccessText("✓ Archive complete"))
	b.WriteString("\n")
	b.WriteString(PrimaryText("│ "))
	b.WriteString(DimText("Distribusi berhasil dibuat dan siap digunakan."))
	b.WriteString("\n")
	b.WriteString(PrimaryText("│"))
	b.WriteString("\n")
	if len(files) >= 2 {
		b.WriteString(PrimaryText("│ "))
		b.WriteString(InfoText("Archives "))
		b.WriteString(DimText("(tar.gz + zip)"))
		b.WriteString("\n")
		for _, file := range files[:2] {
			b.WriteString(PrimaryText("│   "))
			b.WriteString(DimText(file))
			b.WriteString("\n")
		}
	}
	if production && len(files) > 2 {
		b.WriteString(PrimaryText("│ "))
		b.WriteString(InfoText("Production binaries "))
		b.WriteString(DimText("(Linux / macOS / Windows)"))
		b.WriteString("\n")
		for _, file := range files[2:] {
			b.WriteString(PrimaryText("│   "))
			b.WriteString(DimText(file))
			b.WriteString("\n")
		}
	}
	b.WriteString(PrimaryText("╰─"))
	b.WriteString("\n")
	_, _ = fmt.Fprint(w, b.String())
}

// ArchiveIntro merender rencana archive ke w.
func ArchiveIntro(w io.Writer, root, output, mode string, dryRun bool) {
	modeName := mode
	if modeName == "development" {
		modeName = "main-source"
	}
	status := ""
	if dryRun {
		status = " · dry-run"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s\n", PrimaryText("╭─ Archive plan"))
	fmt.Fprintf(&b, "%s\n", PrimaryText("│ ")+InfoText("Create curated project distributions"+status))
	fmt.Fprintf(&b, "%s\n", PrimaryText("│ ")+DimText("Mode   ")+DefaultText(modeName))
	fmt.Fprintf(&b, "%s\n", PrimaryText("│ ")+DimText("Root   ")+DefaultText(root))
	fmt.Fprintf(&b, "%s\n", PrimaryText("│ ")+DimText("Output ")+DefaultText(output))
	fmt.Fprintf(&b, "%s\n", PrimaryText("│ ")+DimText("Formats ")+DefaultText("tar.gz + zip"))
	fmt.Fprintf(&b, "%s\n", PrimaryText("╰─"))
	_, _ = fmt.Fprint(w, b.String())
}
