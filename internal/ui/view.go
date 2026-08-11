package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/kejrak/hopper/internal/host"
)

var (
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	selectedStyle = lipgloss.NewStyle().Bold(true)
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	borderStyle   = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).PaddingLeft(1)
)

// ago renders a compact relative time: "just now", "5m ago", "2h ago", "2d ago".
func ago(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// statusLabel renders a KeyStatus for the detail panel.
func statusLabel(st host.KeyStatus) string {
	switch st {
	case host.KeyStatusLoaded:
		return "● loaded"
	case host.KeyStatusNotLoaded:
		return "○ not loaded"
	case host.KeyStatusNoAgent:
		return "no agent"
	default:
		return "—"
	}
}

// detailLines renders the right-pane rows for the highlighted host.
func detailLines(h *host.Host, st host.KeyStatus, now time.Time) []string {
	if h == nil {
		return []string{dimStyle.Render("no host selected")}
	}
	row := func(label, value string) string {
		if value == "" {
			value = "—"
		}
		return fmt.Sprintf("%-9s %s", label, value)
	}
	source := h.Source
	if i := strings.LastIndex(source, "/.ssh/"); i >= 0 {
		source = source[i+len("/.ssh/"):]
	}
	return []string{
		row("Host", h.Name),
		row("User", h.User),
		row("Hostname", h.Hostname),
		row("Port", h.Port),
		row("Identity", h.IdentityFile),
		row("Agent", statusLabel(st)),
		row("Source", source),
		row("Last", ago(h.LastConnected, now)),
	}
}

// helpLine is the footer; Task 11/12 extend the keybindings shown here.
const helpLine = "enter connect · ctrl+a add key · ctrl+e edit · esc quit"

// View implements tea.Model: filter on top, sectioned list left, details right.
func (m model) View() string {
	var left strings.Builder
	for i, it := range m.items {
		if it.header != "" {
			left.WriteString(headerStyle.Render(it.header) + "\n")
			continue
		}
		line := "  " + it.h.Name
		if i == m.cursor {
			line = selectedStyle.Render("▸ " + it.h.Name)
		}
		left.WriteString(line + "\n")
	}
	right := strings.Join(detailLines(m.selected(), m.agent[m.selectedName()], time.Now()), "\n")

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftWidth(m.width)).Render(left.String()),
		borderStyle.Render(right),
	)
	footer := dimStyle.Render(helpLine)
	if m.status != "" {
		footer = m.status + "\n" + footer
	}
	return m.filter.View() + "\n" + body + "\n" + footer
}

// selectedName returns the highlighted host's name, or "".
func (m model) selectedName() string {
	if h := m.selected(); h != nil {
		return h.Name
	}
	return ""
}

// leftWidth gives the list pane a stable width with a sane floor.
func leftWidth(total int) int {
	if total == 0 {
		return 30
	}
	w := total / 2
	if w < 20 {
		w = 20
	}
	return w
}
