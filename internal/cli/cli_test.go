package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kejrak/hopper/internal/audit"
	"github.com/kejrak/hopper/internal/host"
)

var testHosts = []host.Host{
	{Name: "web", User: "deploy", Hostname: "10.0.0.1", Port: "2222", IdentityFile: "~/.ssh/web", Source: "/h/.ssh/config", Group: "config",
		LastConnected: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)},
	{Name: "db", Hostname: "10.0.0.2", Port: "22", Source: "/h/.ssh/work", Group: "work"},
}

func TestListText(t *testing.T) {
	var b bytes.Buffer
	if err := List(&b, testHosts, false); err != nil {
		t.Fatal(err)
	}
	want := "web\tdeploy@10.0.0.1:2222\tconfig\ndb\t10.0.0.2:22\twork\n"
	if b.String() != want {
		t.Fatalf("got %q, want %q", b.String(), want)
	}
}

func TestListJSON(t *testing.T) {
	var b bytes.Buffer
	if err := List(&b, testHosts, true); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", b.String(), err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d hosts, want 2", len(got))
	}
	for _, key := range []string{"name", "user", "hostname", "port", "identity_file", "source", "group", "last_connected"} {
		if _, ok := got[0][key]; !ok {
			t.Errorf("missing key %q in %v", key, got[0])
		}
	}
	if got[0]["name"] != "web" || got[0]["port"] != "2222" || got[0]["last_connected"] != "2026-09-01T12:00:00Z" {
		t.Errorf("unexpected first host %v", got[0])
	}
	if got[1]["last_connected"] != nil {
		t.Errorf("never-connected host: last_connected = %v, want null", got[1]["last_connected"])
	}
}

func TestListJSONEmptyIsArray(t *testing.T) {
	var b bytes.Buffer
	if err := List(&b, nil, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(b.String()); got != "[]" {
		t.Fatalf("got %q, want []", got)
	}
}

func TestShowText(t *testing.T) {
	var b bytes.Buffer
	if err := Show(&b, testHosts[1], false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"name:", "db", "hostname:", "10.0.0.2", "port:", "22", "group:", "work", "last_connected:", "never"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestShowJSON(t *testing.T) {
	var b bytes.Buffer
	if err := Show(&b, testHosts[0], true); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", b.String(), err)
	}
	if got["name"] != "web" || got["user"] != "deploy" {
		t.Fatalf("unexpected host %v", got)
	}
}

func TestFind(t *testing.T) {
	h, err := Find(testHosts, "db")
	if err != nil || h.Name != "db" {
		t.Fatalf("got %v, %v", h, err)
	}
	if _, err := Find(testHosts, "DB"); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("match must be exact, got %v", err)
	}
}

func TestParseFlags(t *testing.T) {
	pos, asJSON, err := ParseFlags([]string{"web", "--json"})
	if err != nil || !asJSON || !slices.Equal(pos, []string{"web"}) {
		t.Fatalf("got %v %v %v", pos, asJSON, err)
	}
	pos, asJSON, err = ParseFlags([]string{"-json"})
	if err != nil || !asJSON || len(pos) != 0 {
		t.Fatalf("got %v %v %v", pos, asJSON, err)
	}
	if _, _, err := ParseFlags([]string{"--yaml"}); !errors.Is(err, ErrUsage) {
		t.Fatalf("unknown flag: got %v, want ErrUsage", err)
	}
}

func TestParseExec(t *testing.T) {
	cases := []struct {
		args    []string
		name    string
		command []string
	}{
		{[]string{"web", "--", "uptime", "-p"}, "web", []string{"uptime", "-p"}},
		{[]string{"web", "ls", "-la"}, "web", []string{"ls", "-la"}},
		{[]string{"web", "--", "--", "x"}, "web", []string{"--", "x"}},
	}
	for _, c := range cases {
		name, command, err := ParseExec(c.args)
		if err != nil || name != c.name || !slices.Equal(command, c.command) {
			t.Errorf("ParseExec(%v) = %q %v %v, want %q %v", c.args, name, command, err, c.name, c.command)
		}
	}
	for _, bad := range [][]string{nil, {"web"}, {"web", "--"}, {"--", "uptime"}, {"-x", "uptime"}} {
		if _, _, err := ParseExec(bad); !errors.Is(err, ErrUsage) {
			t.Errorf("ParseExec(%v): got %v, want ErrUsage", bad, err)
		}
	}
}

func TestAllowedGroups(t *testing.T) {
	if got := AllowedGroups(""); got != nil {
		t.Fatalf("empty spec: got %v, want nil", got)
	}
	if got := AllowedGroups(" , ,"); got != nil {
		t.Fatalf("only separators: got %v, want nil", got)
	}
	got := AllowedGroups(" work , default,,")
	if len(got) != 2 || !got["work"] || !got["default"] {
		t.Fatalf("got %v, want work+default", got)
	}
	if AllowedGroups("Work")["work"] {
		t.Fatal("group match must be case-sensitive")
	}
}

func TestFilterGroups(t *testing.T) {
	if got := FilterGroups(testHosts, nil); len(got) != 2 {
		t.Fatalf("nil allowlist: got %d hosts, want 2", len(got))
	}
	got := FilterGroups(testHosts, map[string]bool{"work": true})
	if len(got) != 1 || got[0].Name != "db" {
		t.Fatalf("got %v, want only db", got)
	}
	if got := FilterGroups(testHosts, map[string]bool{"nope": true}); len(got) != 0 {
		t.Fatalf("no match: got %v, want none", got)
	}
}

func TestResolve(t *testing.T) {
	if h, err := Resolve(testHosts, "web", nil); err != nil || h.Name != "web" {
		t.Fatalf("nil allowlist: got %v, %v", h, err)
	}
	if h, err := Resolve(testHosts, "db", map[string]bool{"work": true}); err != nil || h.Name != "db" {
		t.Fatalf("allowed: got %v, %v", h, err)
	}
	_, err := Resolve(testHosts, "web", map[string]bool{"work": true})
	if !errors.Is(err, ErrGroupNotAllowed) || !strings.Contains(err.Error(), `"web"`) || !strings.Contains(err.Error(), `"config"`) {
		t.Fatalf("disallowed: got %v, want ErrGroupNotAllowed naming host and group", err)
	}
	if _, err := Resolve(testHosts, "nope", map[string]bool{"work": true}); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("unknown: got %v, want ErrUnknownHost", err)
	}
}

func testRuns() []audit.Run {
	t0 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	zero, one := 0, 1
	ms := int64(1500)
	return []audit.Run{
		{ID: "a", Time: t0, Host: "web", Group: "config", Command: []string{"uptime"}, Status: audit.StatusOK, ExitCode: &zero, DurationMS: &ms},
		{ID: "b", Time: t0.Add(time.Minute), Host: "db", Group: "work", Command: []string{"sh", "-c", "echo a\n  echo b"}, Status: audit.StatusFailed, ExitCode: &one, DurationMS: &ms},
		{ID: "c", Time: t0.Add(2 * time.Minute), Host: "nope", Command: []string{"ls"}, Status: audit.StatusRefused, Reason: "unknown host"},
	}
}

func TestParseLog(t *testing.T) {
	n, asJSON, err := ParseLog(nil)
	if err != nil || n != DefaultLogRuns || asJSON {
		t.Fatalf("defaults: got %d %v %v", n, asJSON, err)
	}
	n, asJSON, err = ParseLog([]string{"--json", "-n", "5"})
	if err != nil || n != 5 || !asJSON {
		t.Fatalf("got %d %v %v", n, asJSON, err)
	}
	for _, bad := range [][]string{{"-n"}, {"-n", "0"}, {"-n", "x"}, {"-n", "-3"}, {"web"}, {"--yaml"}} {
		if _, _, err := ParseLog(bad); !errors.Is(err, ErrUsage) {
			t.Errorf("ParseLog(%v): got %v, want ErrUsage", bad, err)
		}
	}
}

func TestFilterRuns(t *testing.T) {
	if got := FilterRuns(testRuns(), nil); len(got) != 3 {
		t.Fatalf("nil allowlist: got %d runs", len(got))
	}
	got := FilterRuns(testRuns(), map[string]bool{"work": true})
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("got %+v, want only b", got)
	}
}

func TestLogJSONLastN(t *testing.T) {
	var b bytes.Buffer
	if err := Log(&b, testRuns(), 2, true); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", b.String(), err)
	}
	if len(got) != 2 || got[0]["id"] != "b" || got[1]["id"] != "c" || got[1]["status"] != "refused" {
		t.Fatalf("got %v, want runs b, c (oldest first)", got)
	}
}

func TestLogJSONEmptyIsArray(t *testing.T) {
	var b bytes.Buffer
	if err := Log(&b, nil, 20, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(b.String()); got != "[]" {
		t.Fatalf("got %q, want []", got)
	}
}

func TestLogTextEmptyWritesNothing(t *testing.T) {
	var b bytes.Buffer
	if err := Log(&b, nil, 20, false); err != nil || b.Len() != 0 {
		t.Fatalf("got %q, %v", b.String(), err)
	}
}

func TestLogTextFlattensCommand(t *testing.T) {
	var b bytes.Buffer
	if err := Log(&b, testRuns(), 20, false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want header + 3 rows:\n%s", len(lines), b.String())
	}
	for _, want := range []string{"TIME", "HOST", "STATUS", "EXIT", "DURATION", "COMMAND"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("header %q missing %s", lines[0], want)
		}
	}
	if !strings.Contains(lines[1], "web") || !strings.Contains(lines[1], "ok") || !strings.Contains(lines[1], "1.5s") {
		t.Errorf("row 1 %q", lines[1])
	}
	if !strings.Contains(lines[2], "sh -c echo a echo b") {
		t.Errorf("row 2 should flatten whitespace: %q", lines[2])
	}
	if !strings.Contains(lines[3], "refused") || !strings.Contains(lines[3], " - ") {
		t.Errorf("row 3 should show refused with '-' for exit/duration: %q", lines[3])
	}
}

func TestLogTextTruncatesLongCommand(t *testing.T) {
	runs := testRuns()[:1]
	runs[0].Command = []string{strings.Repeat("x", 200)}
	var b bytes.Buffer
	if err := Log(&b, runs, 20, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), strings.Repeat("x", 81)) || !strings.Contains(b.String(), "…") {
		t.Fatalf("long command not truncated to 80 runes + …:\n%s", b.String())
	}
}
