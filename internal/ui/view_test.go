package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/kejrak/hopper/internal/host"
)

func TestAgo(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		t    time.Time
		want string
	}{
		{time.Time{}, "never"},
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-2 * time.Hour), "2h ago"},
		{now.Add(-49 * time.Hour), "2d ago"},
	}
	for _, c := range cases {
		if got := ago(c.t, now); got != c.want {
			t.Errorf("ago(%v)=%q, want %q", c.t, got, c.want)
		}
	}
}

func TestStatusLabel(t *testing.T) {
	cases := map[host.KeyStatus]string{
		host.KeyStatusLoaded:    "● loaded",
		host.KeyStatusNotLoaded: "○ not loaded",
		host.KeyStatusNoAgent:   "no agent",
		host.KeyStatusUnknown:   "—",
	}
	for st, want := range cases {
		if got := statusLabel(st); got != want {
			t.Errorf("statusLabel(%v)=%q, want %q", st, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"shorter than max unchanged", "short", 10, "short"},
		{"exact max unchanged", "exact", 5, "exact"},
		{"longer truncated with ellipsis", "this is too long", 8, "this is…"},
		{"max of zero", "anything", 0, ""},
		{"max of one", "anything", 1, "…"},
		{"negative max", "anything", -1, ""},
		{"multi-byte runes not split", "日本語のテキスト", 4, "日本語…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := truncate(c.s, c.max); got != c.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", c.s, c.max, got, c.want)
			}
		})
	}
}

func TestWindowBounds(t *testing.T) {
	cases := []struct {
		name               string
		cursor, total, vs  int
		wantStart, wantEnd int
	}{
		{"cursor at top", 0, 20, 5, 0, 5},
		{"cursor in middle", 10, 20, 5, 8, 13},
		{"cursor at bottom", 19, 20, 5, 15, 20},
		{"list shorter than window", 1, 3, 10, 0, 3},
		{"zero height", 5, 20, 0, 0, 20},
		{"empty list", 0, 0, 5, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, end := windowBounds(c.cursor, c.total, c.vs)
			if start != c.wantStart || end != c.wantEnd {
				t.Errorf("windowBounds(%d, %d, %d) = (%d, %d), want (%d, %d)",
					c.cursor, c.total, c.vs, start, end, c.wantStart, c.wantEnd)
			}
			// The cursor row must always land inside the returned window
			// whenever the list is non-empty and the cursor is in range.
			if c.total > 0 && c.cursor >= 0 && c.cursor < c.total {
				if c.cursor < start || c.cursor >= end {
					t.Errorf("cursor %d outside window [%d, %d)", c.cursor, start, end)
				}
			}
		})
	}
}

func TestDetailLines(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	h := &host.Host{Name: "web-prod", User: "deploy", Hostname: "10.0.1.20",
		Port: "22", IdentityFile: "~/.ssh/id_work", Source: "/home/u/.ssh/conf.d/work.conf",
		LastConnected: now.Add(-2 * time.Hour)}
	joined := strings.Join(detailLines(h, host.KeyStatusLoaded, now), "\n")
	for _, want := range []string{"web-prod", "deploy", "10.0.1.20", "22",
		"~/.ssh/id_work", "● loaded", "work.conf", "2h ago"} {
		if !strings.Contains(joined, want) {
			t.Errorf("detail missing %q in:\n%s", want, joined)
		}
	}
}
