package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/kejrak/hopper/internal/host"
)

// Action is what the user chose to do when the TUI exited.
type Action int

// Actions returned in Result.
const (
	ActionQuit    Action = iota // user cancelled; nothing to run
	ActionConnect               // run ssh against Result.Host
)

// Result is the TUI's outcome, consumed by main.
type Result struct {
	Action Action
	Host   host.Host
}

// ReloadFunc re-reads the ssh configs; the TUI calls it after $EDITOR runs.
type ReloadFunc func() ([]host.Host, []string, error)

// model is the Bubble Tea model backing the picker.
type model struct {
	filter textinput.Model
	hosts  []host.Host
	recent []string
	items  []item
	cursor int
	agent  map[string]host.KeyStatus
	status string
	reload ReloadFunc
	width  int
	height int
	result Result
}

// newModel builds the initial picker state from the given hosts, recent
// names and reload callback.
func newModel(hosts []host.Host, recent []string, reload ReloadFunc) model {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Focus()
	m := model{filter: ti, hosts: hosts, recent: recent, reload: reload,
		agent: host.AgentStatuses(hosts)}
	m.refilter()
	return m
}

// refilter rebuilds the rows for the current filter text and clamps the
// cursor onto a host row.
func (m *model) refilter() {
	m.items = buildItems(m.hosts, m.recent, m.filter.Value())
	m.cursor = 0
	m.snapToHost(1)
}

// snapToHost moves the cursor in the given direction until it sits on a
// host row (or leaves it unchanged if none exists).
func (m *model) snapToHost(direction int) {
	for i := m.cursor; i >= 0 && i < len(m.items); i += direction {
		if m.items[i].h != nil {
			m.cursor = i
			return
		}
	}
}

// moveCursor advances the cursor by delta host rows, skipping headers.
func (m *model) moveCursor(delta int) {
	direction := 1
	if delta < 0 {
		direction = -1
	}
	for i := m.cursor + direction; i >= 0 && i < len(m.items); i += direction {
		if m.items[i].h != nil {
			m.cursor = i
			return
		}
	}
}

// selected returns the highlighted host, or nil when the list is empty.
func (m model) selected() *host.Host {
	if m.cursor >= 0 && m.cursor < len(m.items) {
		return m.items[m.cursor].h
	}
	return nil
}

// Init implements tea.Model.
func (m model) Init() tea.Cmd { return textinput.Blink }

// Update implements tea.Model.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.result = Result{Action: ActionQuit}
			return m, tea.Quit
		case "enter":
			if h := m.selected(); h != nil {
				m.result = Result{Action: ActionConnect, Host: *h}
				return m, tea.Quit
			}
			return m, nil
		case "up":
			m.moveCursor(-1)
			return m, nil
		case "down":
			m.moveCursor(1)
			return m, nil
		default:
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.refilter()
			return m, cmd
		}
	}
	return m, nil
}

// Run shows the picker and blocks until the user connects or quits.
func Run(hosts []host.Host, recent []string, reload ReloadFunc) (Result, error) {
	program := tea.NewProgram(newModel(hosts, recent, reload), tea.WithAltScreen())
	final, err := program.Run()
	if err != nil {
		return Result{}, fmt.Errorf("running picker: %w", err)
	}
	return final.(model).result, nil
}
