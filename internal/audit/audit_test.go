package audit

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func intp(v int) *int     { return &v }
func i64p(v int64) *int64 { return &v }

func TestAppendCreatesPrivateFileAndLoadsInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "hopper", "exec.log")
	t0 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	if err := Append(path, Record{Time: t0, Event: EventStart, ID: "a", Host: "web", Group: "default", Command: []string{"uptime", "-p"}, Dir: "/tmp"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, Record{Time: t0.Add(time.Second), Event: EventEnd, ID: "a", Host: "web", ExitCode: intp(0), DurationMS: i64p(1000)}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("file mode %o, want 600", perm)
		}
		dirInfo, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if perm := dirInfo.Mode().Perm(); perm != 0o700 {
			t.Fatalf("dir mode %o, want 700", perm)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", lines, data)
	}
	records, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Event != EventStart || records[1].Event != EventEnd {
		t.Fatalf("unexpected records %+v", records)
	}
	if got := records[0].Command; len(got) != 2 || got[1] != "-p" {
		t.Fatalf("command %v", got)
	}
	if records[1].ExitCode == nil || *records[1].ExitCode != 0 || records[1].DurationMS == nil || *records[1].DurationMS != 1000 {
		t.Fatalf("end record %+v", records[1])
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	records, err := Load(filepath.Join(t.TempDir(), "nope.log"))
	if err != nil || records != nil {
		t.Fatalf("got %v, %v; want nil, nil", records, err)
	}
}

func TestLoadSkipsMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exec.log")
	content := `{"time":"2026-09-25T10:00:00Z","event":"start","id":"a","host":"web"}
not json
{"no_event":true}

{"time":"2026-09-25T10:00:01Z","event":"end","id":"a","host":"web","exit_code":3}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	records, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1].ExitCode == nil || *records[1].ExitCode != 3 {
		t.Fatalf("got %+v, want start + end (last line has no trailing newline)", records)
	}
}

func TestAppendConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exec.log")
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Append(path, Record{Time: time.Now(), Event: EventStart, ID: NewID(), Host: "web", Command: []string{strings.Repeat("x", 200)}}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	records, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != n {
		t.Fatalf("got %d intact records, want %d", len(records), n)
	}
}

func TestNewIDIsUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := NewID()
		if len(id) != 16 || seen[id] {
			t.Fatalf("bad or duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestRunsPairsRecords(t *testing.T) {
	t0 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	records := []Record{
		{Time: t0, Event: EventStart, ID: "ok", Host: "web", Group: "default", Command: []string{"true"}, Dir: "/w"},
		{Time: t0.Add(1 * time.Second), Event: EventStart, ID: "fail", Host: "db", Group: "work", Command: []string{"false"}},
		{Time: t0.Add(2 * time.Second), Event: EventEnd, ID: "ok", Host: "web", ExitCode: intp(0), DurationMS: i64p(5)},
		{Time: t0.Add(3 * time.Second), Event: EventRefused, ID: "ref", Host: "nope", Command: []string{"ls"}, Reason: `unknown host "nope"`},
		{Time: t0.Add(4 * time.Second), Event: EventEnd, ID: "fail", Host: "db", ExitCode: intp(1), DurationMS: i64p(7)},
		{Time: t0.Add(5 * time.Second), Event: EventStart, ID: "killed", Host: "web", Group: "default", Command: []string{"sleep", "999"}},
		{Time: t0.Add(6 * time.Second), Event: EventEnd, ID: "orphan", Host: "web", ExitCode: intp(0)},
	}
	runs := Runs(records)
	if len(runs) != 4 {
		t.Fatalf("got %d runs, want 4: %+v", len(runs), runs)
	}
	want := []struct{ id, status string }{{"ok", StatusOK}, {"fail", StatusFailed}, {"ref", StatusRefused}, {"killed", StatusUnfinished}}
	for i, w := range want {
		if runs[i].ID != w.id || runs[i].Status != w.status {
			t.Errorf("run %d: got %s/%s, want %s/%s", i, runs[i].ID, runs[i].Status, w.id, w.status)
		}
	}
	if runs[0].ExitCode == nil || *runs[0].ExitCode != 0 || runs[0].DurationMS == nil || *runs[0].DurationMS != 5 || runs[0].Dir != "/w" || runs[0].Group != "default" {
		t.Errorf("ok run %+v", runs[0])
	}
	if runs[2].Reason == "" || runs[2].ExitCode != nil {
		t.Errorf("refused run %+v", runs[2])
	}
	if runs[3].ExitCode != nil || runs[3].DurationMS != nil {
		t.Errorf("unfinished run %+v", runs[3])
	}
}
