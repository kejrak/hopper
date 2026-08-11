package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kejrak/hopper/internal/host"
)

func fixtures() []host.Host {
	return []host.Host{
		{Name: "web-prod", Group: "work", Hostname: "10.0.1.20"},
		{Name: "gitlab", Group: "work", Hostname: "git.example.com"},
		{Name: "nas", Group: "home", Hostname: "nas.local"},
	}
}

func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		needle, hay string
		want        bool
	}{
		{"wpr", "web-prod", true},
		{"WEB", "web-prod", true},
		{"xyz", "web-prod", false},
		{"", "anything", true},
	}
	for _, c := range cases {
		if got := fuzzyMatch(c.needle, c.hay); got != c.want {
			t.Errorf("fuzzyMatch(%q,%q)=%v, want %v", c.needle, c.hay, got, c.want)
		}
	}
}

func TestBuildItemsSectionsAndFilter(t *testing.T) {
	items := buildItems(fixtures(), []string{"nas"}, "")
	// RECENT first, then groups; every host row preceded by its section header.
	if items[0].header != "RECENT" || items[1].h == nil || items[1].h.Name != "nas" {
		t.Fatalf("RECENT section wrong: %+v", items[:2])
	}
	filtered := buildItems(fixtures(), []string{"nas"}, "git")
	for _, it := range filtered {
		if it.h != nil && it.h.Name != "gitlab" {
			t.Fatalf("filter leaked host %q", it.h.Name)
		}
	}
}

func TestEnterSelectsHighlightedHost(t *testing.T) {
	m := newModel(fixtures(), []string{"nas"}, nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	res := updated.(model).result
	if res.Action != ActionConnect || res.Host.Name == "" {
		t.Fatalf("got %+v, want ActionConnect with a host", res)
	}
}

func TestEscQuitsWithoutConnect(t *testing.T) {
	m := newModel(fixtures(), nil, nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(model).result.Action != ActionQuit {
		t.Fatal("esc must quit")
	}
}

func TestCursorSkipsHeaders(t *testing.T) {
	m := newModel(fixtures(), []string{"nas"}, nil)
	if m.selected() == nil {
		t.Fatal("initial cursor must sit on a host row, not a header")
	}
	m.moveCursor(1)
	if m.selected() == nil {
		t.Fatal("cursor moved onto a header row")
	}
}

func TestCtrlAWithoutIdentityShowsStatus(t *testing.T) {
	hosts := []host.Host{{Name: "bare", Group: "default"}}
	m := newModel(hosts, nil, nil)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("no command should run without an identity file")
	}
	if got := updated.(model).status; got == "" {
		t.Fatal("expected a status message")
	}
}

func TestCtrlAAlreadyLoadedShowsStatusWithoutPrompt(t *testing.T) {
	hosts := []host.Host{{Name: "web", Group: "default", IdentityFile: "~/.ssh/id"}}
	m := newModel(hosts, nil, nil)
	m.agent["web"] = host.KeyStatusLoaded
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("no command should run when the key is already loaded")
	}
	if got := updated.(model).status; got == "" {
		t.Fatal("expected a status message")
	}
}

func TestKeyAddedRefreshesAgentStatus(t *testing.T) {
	hosts := []host.Host{{Name: "web", Group: "default", IdentityFile: "~/.ssh/id"}}
	m := newModel(hosts, nil, nil)
	updated, _ := m.Update(keyAddedMsg{hostName: "web", err: nil})
	if got := updated.(model).status; got == "" {
		t.Fatal("expected a success status message")
	}
}

func TestCtrlEWithoutEditorShowsStatus(t *testing.T) {
	t.Setenv("EDITOR", "")
	m := newModel(fixtures(), nil, nil)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if cmd != nil {
		t.Fatal("no command should run with $EDITOR unset")
	}
	if updated.(model).status == "" {
		t.Fatal("expected a status message")
	}
}

func TestEditorDoneTriggersReload(t *testing.T) {
	reloaded := false
	reload := func() ([]host.Host, []string, error) {
		reloaded = true
		return []host.Host{{Name: "fresh", Group: "default"}}, nil, nil
	}
	m := newModel(fixtures(), nil, reload)
	updated, _ := m.Update(editorDoneMsg{})
	if !reloaded {
		t.Fatal("reload not called")
	}
	if h := updated.(model).selected(); h == nil || h.Name != "fresh" {
		t.Fatalf("host list not refreshed: %+v", h)
	}
}
