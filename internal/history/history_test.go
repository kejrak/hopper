package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestRecordThenLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "history.json")
	if err := Record(path, "web-prod", ts("2026-08-10T10:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if err := Record(path, "nas", ts("2026-08-10T12:00:00Z")); err != nil {
		t.Fatal(err)
	}
	entries := Load(path)
	if len(entries) != 2 || entries[0].Host != "nas" || entries[1].Host != "web-prod" {
		t.Fatalf("got %+v, want nas first (newest)", entries)
	}
}

func TestLoadMissingOrCorruptIsEmpty(t *testing.T) {
	if got := Load(filepath.Join(t.TempDir(), "nope.json")); got != nil {
		t.Fatalf("missing file: got %+v", got)
	}
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(path); got != nil {
		t.Fatalf("corrupt file: got %+v", got)
	}
}

func TestRecordCapsAtHundredEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	base := ts("2026-08-10T00:00:00Z")
	for i := 0; i < 105; i++ {
		if err := Record(path, "h", base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(Load(path)); got != 100 {
		t.Fatalf("got %d entries, want 100", got)
	}
}

func TestRecentDedupes(t *testing.T) {
	entries := []Entry{
		{Host: "a", Timestamp: ts("2026-08-10T12:00:00Z")},
		{Host: "b", Timestamp: ts("2026-08-10T11:00:00Z")},
		{Host: "a", Timestamp: ts("2026-08-10T10:00:00Z")},
		{Host: "c", Timestamp: ts("2026-08-10T09:00:00Z")},
	}
	got := Recent(entries, 2)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v, want [a b]", got)
	}
}

func TestRecentWithNZeroReturnsNil(t *testing.T) {
	entries := []Entry{
		{Host: "a", Timestamp: ts("2026-08-10T12:00:00Z")},
		{Host: "b", Timestamp: ts("2026-08-10T11:00:00Z")},
	}
	got := Recent(entries, 0)
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestRecentWithNegativeReturnsNil(t *testing.T) {
	entries := []Entry{
		{Host: "a", Timestamp: ts("2026-08-10T12:00:00Z")},
		{Host: "b", Timestamp: ts("2026-08-10T11:00:00Z")},
	}
	got := Recent(entries, -1)
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestLastConnected(t *testing.T) {
	entries := []Entry{
		{Host: "a", Timestamp: ts("2026-08-10T10:00:00Z")},
		{Host: "a", Timestamp: ts("2026-08-10T12:00:00Z")},
	}
	if got := LastConnected(entries)["a"]; !got.Equal(ts("2026-08-10T12:00:00Z")) {
		t.Fatalf("got %v", got)
	}
}
