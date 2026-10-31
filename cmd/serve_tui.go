package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/maleolabs/avior/internal/brand"
)

const (
	// serveTickInterval mengatur refresh status runtime di TUI.
	serveTickInterval = time.Second
	// serveStateBuffer menampung transisi state child; pengiriman non-blocking
	// supaya callback runtime tidak pernah menahan shutdown.
	serveStateBuffer = 32
	// serveStopTimeout membatasi tunggu shutdown setelah p.Run gagal. Jalur
	// normal (Ctrl+C) menunggu serveDoneMsg, jadi timeout ini hanya jaring
	// pengaman agar tidak pernah menggantung selamanya.
	serveStopTimeout = 30 * time.Second
)

type serveTUIModel struct {
	status       string
	services     []string
	states       map[string]string
	err          error
	started      time.Time
	stopped      time.Time
	ready        chan []string
	stateUpdates chan serveStateMsg
	cancel       context.CancelFunc
}

type serveStateMsg struct{ name, state string }
type serveDoneMsg struct{ err error }
type serveTickMsg time.Time

// Init hanya memulai tick; hasil serve dikirim sebagai tea.Msg oleh satu
// goroutine di runServeTUI (bukan dibaca sebagai Cmd kedua).
func (m serveTUIModel) Init() tea.Cmd {
	return func() tea.Msg { return serveTickMsg(time.Now()) }
}

func (m serveTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			// Cancel runtime lalu biarkan serveDoneMsg yang menutup program
			// setelah shutdown benar-benar selesai.
			if m.status != "STOPPING" && m.status != "STOPPED" {
				m.status = "STOPPING"
				if m.cancel != nil {
					m.cancel()
				}
			}
			return m, nil
		}
	case []string:
		m.services = msg
		if m.states == nil {
			m.states = make(map[string]string)
		}
		if m.status == "STARTING" {
			m.status = "RUNNING"
		}
		for _, service := range msg {
			if m.states[service] == "" {
				m.states[service] = "running"
			}
		}
		return m, nil
	case serveStateMsg:
		if m.states == nil {
			m.states = make(map[string]string)
		}
		m.states[msg.name] = msg.state
		m.noteService(msg.name)
		return m, nil
	case serveDoneMsg:
		m.drain()
		m.err = msg.err
		m.status = "STOPPED"
		if m.stopped.IsZero() {
			m.stopped = time.Now()
		}
		for _, service := range m.services {
			if m.states[service] != "failed" {
				m.states[service] = "exited"
			}
		}
		return m, tea.Quit
	case serveTickMsg:
		m.drain()
		return m, tickServe()
	}
	return m, nil
}

// noteService menambahkan service ke daftar tampil saat callback state
// pertama datang (product gap #2): user melihat service sejak startup,
// tidak menunggu OnReady. Duplikat di-skip — OnReady menimpa dengan daftar
// lengkap berurutan runtime.
func (m *serveTUIModel) noteService(name string) {
	for _, service := range m.services {
		if service == name {
			return
		}
	}
	m.services = append(m.services, name)
}

// drain mengambil semua update yang tertunda tanpa blocking. State terakhir
// per service yang menang, jadi UI tetap jujur saat shutdown. Pointer receiver:
// Update memutasi salinan model lokal, jadi slice services harus ikut berubah.
func (m *serveTUIModel) drain() {
drainReady:
	for {
		select {
		case services := <-m.ready:
			if len(services) > 0 {
				m.services = services
				if m.status == "STARTING" {
					m.status = "RUNNING"
				}
			}
		default:
			break drainReady
		}
	}
drainStates:
	for {
		select {
		case state := <-m.stateUpdates:
			if m.states == nil {
				m.states = make(map[string]string)
			}
			m.states[state.name] = state.state
			m.noteService(state.name)
		default:
			break drainStates
		}
	}
}

// View tidak boleh memutasi model (receiver value): waktu mulai di-set sekali
// saat model dibuat, waktu berhenti di-set saat serveDoneMsg.
func (m serveTUIModel) View() string {
	end := m.stopped
	if end.IsZero() {
		end = time.Now()
	}
	elapsed := time.Duration(0)
	if !m.started.IsZero() {
		elapsed = end.Sub(m.started)
	}
	view := fmt.Sprintf("%s · DEVELOPMENT\n\n● Runtime: %s\n", brand.Name, m.status)
	for _, service := range m.services {
		state := m.states[service]
		if state == "" {
			state = "starting"
		}
		view += fmt.Sprintf("● %s: %s\n", service, strings.ToUpper(state))
	}
	if m.err != nil {
		view += fmt.Sprintf("\nError: %s\n", m.err)
	}
	view += fmt.Sprintf("Elapsed: %s\n\n", elapsed.Round(time.Second))
	if m.status == "STOPPED" {
		return view
	}
	return view + "Ctrl+C stop runtime\n"
}

func tickServe() tea.Cmd {
	return tea.Tick(serveTickInterval, func(t time.Time) tea.Msg { return serveTickMsg(t) })
}

// serveOutcome menyimpan hasil serve untuk jalur error p.Run tanpa membaca
// ulang channel hasil (tidak ada pembaca kedua yang bisa deadlock).
type serveOutcome struct {
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func newServeOutcome() *serveOutcome {
	return &serveOutcome{done: make(chan struct{})}
}

func (o *serveOutcome) set(err error) {
	o.mu.Lock()
	o.err = err
	o.mu.Unlock()
	close(o.done)
}

func (o *serveOutcome) get() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.err
}

func runServeTUI(parent context.Context, serve func(context.Context, func([]string), func(string, string)) error, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	ready := make(chan []string, 1)
	states := make(chan serveStateMsg, serveStateBuffer)
	model := serveTUIModel{
		status:       "STARTING",
		states:       make(map[string]string),
		started:      time.Now(),
		ready:        ready,
		stateUpdates: states,
		cancel:       cancel,
	}
	p := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out))

	outcome := newServeOutcome()
	// Satu-satunya konsumen hasil serve: goroutine ini mengirim tepat satu
	// serveDoneMsg ke event loop. Tidak ada pembaca kedua setelah p.Run.
	go func() {
		err := serve(ctx, func(services []string) {
			select {
			case ready <- services:
			default:
			}
		}, func(name, state string) {
			select {
			case states <- serveStateMsg{name: name, state: state}:
			default:
			}
		})
		outcome.set(err)
		p.Send(serveDoneMsg{err: err})
	}()

	final, runErr := p.Run()
	cancel()
	if runErr != nil {
		// Program gagal (mis. input/render): pastikan runtime berhenti agar
		// child tidak yatim, dengan batas waktu agar tidak menggantung.
		select {
		case <-outcome.done:
		case <-time.After(serveStopTimeout):
		}
		return errors.Join(runErr, outcome.get())
	}
	if m, ok := final.(serveTUIModel); ok {
		return m.err
	}
	return outcome.get()
}
