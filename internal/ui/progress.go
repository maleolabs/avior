// ProgressRenderer merender satu operasi aktif pada satu waktu. Di terminal
// baris aktif di-refresh; di tempat lain ia mengeluarkan baris log biasa
// agar output tetap terpakai di CI dan log yang di-redirect. Port perilaku
// codebase pendahulu dengan writer injection penuh (sto:p1-writer-injection).
package ui

import (
	"fmt"
	"io"
	"sync"
	"time"
)

const marqueeTick = 45 * time.Millisecond

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type ProgressRenderer struct {
	out   io.Writer
	mu    sync.Mutex
	stop  chan struct{}
	done  chan struct{}
	label string
	frame int
}

func NewProgressRenderer(out io.Writer) *ProgressRenderer {
	return &ProgressRenderer{out: out}
}

func (r *ProgressRenderer) Start(label string) {
	r.finish()
	r.mu.Lock()
	r.label, r.frame = label, 0
	if !Enabled() {
		fmt.Fprintf(r.out, "  %s%s\n", InfoText("→ "), label)
		r.mu.Unlock()
		return
	}
	r.stop, r.done = make(chan struct{}), make(chan struct{})
	stop, done := r.stop, r.done
	r.drawLocked()
	r.mu.Unlock()
	go func() {
		ticker := time.NewTicker(marqueeTick)
		defer ticker.Stop()
		defer close(done)
		for {
			select {
			case <-ticker.C:
				r.mu.Lock()
				if r.stop == stop {
					r.frame++
					r.drawLocked()
				}
				r.mu.Unlock()
			case <-stop:
				return
			}
		}
	}()
}

// Phase mencetak heading section yang stabil tanpa mengganggu spinner.
func (r *ProgressRenderer) Phase(label string) {
	r.finish()
	fmt.Fprintf(r.out, "\n  %s\n", PrimaryText("▸ "+label))
}

func (r *ProgressRenderer) Complete(label string) {
	r.finish()
	fmt.Fprintf(r.out, "  %s%s\n", SuccessText("✓ "), label)
}

func (r *ProgressRenderer) Skipped(label string) {
	r.finish()
	fmt.Fprintf(r.out, "  %s%s\n", WarningText("→ "), DimText(label+" — planned (dry-run)"))
}

func (r *ProgressRenderer) Close() { r.finish() }

func (r *ProgressRenderer) finish() {
	r.mu.Lock()
	stop, done := r.stop, r.done
	r.stop, r.done = nil, nil
	active := stop != nil
	if active && Enabled() {
		fmt.Fprint(r.out, ClearLine())
	}
	r.mu.Unlock()
	if active {
		close(stop)
		<-done
	}
}

func (r *ProgressRenderer) drawLocked() {
	fmt.Fprintf(r.out, "\r%s%s %s", ClearLine(), InfoText(spinnerFrames[r.frame%len(spinnerFrames)]), r.label)
}
