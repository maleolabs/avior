// Package ui adalah leaf (arc:avior-architecture): style, progress, cards.
// Dilarang import paket internal lain. Semua render menerima io.Writer —
// tidak pernah menyentuh os.Stdout/os.Stderr sendiri (sto:p1-writer-injection).
package ui

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// ReportedError adalah satu-satunya kontrak render error CLI (sto:p1-writer-injection).
// Error yang tampil ke pengguna membawa pesan aman (tanpa secret) dan tahu
// cara me-render dirinya ke io.Writer yang di-inject.
type ReportedError interface {
	error
	UserMessage() string
	Render(w io.Writer)
}

type reportedError struct {
	err     error
	message string
}

func (e *reportedError) Error() string       { return e.err.Error() }
func (e *reportedError) Unwrap() error       { return e.err }
func (e *reportedError) UserMessage() string { return e.message }
func (e *reportedError) Render(w io.Writer)  { ErrorCard(w, e.message) }

// NewReportedError membungkus err dengan pesan aman yang ditampilkan pengguna.
func NewReportedError(err error, userMessage string) ReportedError {
	return &reportedError{err: err, message: userMessage}
}

// RenderError menulis err ke w: ReportedError pakai Render miliknya, error
// polosa dibungkus kartu error standar.
// ponytail: mapping Code()→exit-code saat cmd/ layer mendarat.
func RenderError(w io.Writer, err error) {
	var re ReportedError
	if errors.As(err, &re) {
		re.Render(w)
		return
	}
	ErrorCard(w, err.Error())
}

// ErrorCard menulis kartu error bentuk legacy codebase pendahulu — setiap baris pesan
// di-frame independen (penting untuk pesan validasi multi-blok).
// ponytail: warna/ANSI ditunda sampai layer style (tanpa golang.org/x/term)
// ada; kartu polosa ini sudah aman untuk buffer & pipe.
func ErrorCard(w io.Writer, message string) {
	fmt.Fprint(w, "\n╭─ Error\n")
	for _, line := range strings.Split(strings.TrimRight(message, "\n"), "\n") {
		fmt.Fprintf(w, "│ %s\n", line)
	}
	fmt.Fprint(w, "│ Periksa detail langkah di atas lalu coba lagi.\n╰─\n")
}
