package sshcfg

import (
	"os"
	"path/filepath"
	"testing"
)

// write creates a file with content inside dir, creating parents.
func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigFilesFollowsIncludes(t *testing.T) {
	dir := t.TempDir()
	root := write(t, dir, "config", "Include conf.d/*.conf\nHost rootHost\n")
	work := write(t, dir, "conf.d/work.conf", "Host workHost\n")
	files, warnings := ConfigFiles(root, dir)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(files) != 2 || files[0] != root || files[1] != work {
		t.Fatalf("got %v, want [%s %s]", files, root, work)
	}
}

func TestConfigFilesRelativeIncludeResolvesAgainstSSHDir(t *testing.T) {
	dir := t.TempDir()
	// Included file references another file relative to sshDir, not its own dir.
	root := write(t, dir, "config", "Include conf.d/a.conf\n")
	a := write(t, dir, "conf.d/a.conf", "Include extra.conf\n")
	extra := write(t, dir, "extra.conf", "Host extraHost\n")
	files, _ := ConfigFiles(root, dir)
	want := []string{root, a, extra}
	if len(files) != 3 || files[0] != want[0] || files[1] != want[1] || files[2] != want[2] {
		t.Fatalf("got %v, want %v", files, want)
	}
}

func TestConfigFilesBreaksIncludeCycles(t *testing.T) {
	dir := t.TempDir()
	root := write(t, dir, "config", "Include a.conf\n")
	write(t, dir, "a.conf", "Include config\n") // cycle back to root
	files, _ := ConfigFiles(root, dir)
	if len(files) != 2 {
		t.Fatalf("cycle not broken, got %v", files)
	}
}

func TestConfigFilesSkipsMissingIncludeSilently(t *testing.T) {
	dir := t.TempDir()
	root := write(t, dir, "config", "Include nonexistent/*.conf\nHost h\n")
	files, warnings := ConfigFiles(root, dir)
	if len(files) != 1 || len(warnings) != 0 {
		t.Fatalf("got files=%v warnings=%v, want just root and no warnings", files, warnings)
	}
}

func TestExpandIncludeHandlesQuotesAndAbsolute(t *testing.T) {
	dir := t.TempDir()
	target := write(t, dir, "x.conf", "")
	got := expandInclude(`"`+target+`"`, dir)
	if len(got) != 1 || got[0] != target {
		t.Fatalf("got %v, want [%s]", got, target)
	}
}
