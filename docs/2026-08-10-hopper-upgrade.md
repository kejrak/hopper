# Hopper Upgrade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor hopper into a testable `internal/` package layout that delegates connection to OpenSSH, then build a full-manager Bubble Tea TUI with grouping, recent-hosts history, ssh-add, and $EDITOR integration.

**Architecture:** Hopper is a thin orchestrator: `internal/sshcfg` enumerates hosts from `~/.ssh/config` + `Include`s (display attributes only), `internal/host` builds the `ssh <name>` handoff plus ssh-agent queries, `internal/history` owns the JSON state file, and `internal/ui` renders the two-pane TUI. Connection resolution is fully delegated to OpenSSH — hopper passes only the host alias.

**Tech Stack:** Go 1.24, `kevinburke/ssh_config` v1.2.0 (parsing), `charmbracelet/bubbletea` + `bubbles` + `lipgloss` (TUI, added in Task 9), `go-fuzzyfinder` (removed in Task 13).

**Spec:** `~/.letco/planning/analyze_622948d4-dac0-4700-a7c0-865dd388b788.md`

## Global Constraints

- Module path: `github.com/kejrak/hopper`; Go `1.24.0`.
- Connection is always exactly `ssh <name>` — never add `-p`, `-i`, or `user@` flags.
- Hopper never writes to any ssh config file; `ctrl+e` delegates to `$EDITOR`.
- Hopper never reads, stores, or handles passphrases — `ssh-add` prompts on the tty itself.
- All hopper errors/warnings go to **stderr**; exit 0 on user cancel, 1 on fatal errors, otherwise mirror ssh's exit code.
- Unreadable/unparsable *included* config files: skip with a warning. Unreadable *root* config: fatal.
- Wildcard host patterns (`*`, `?`, `!`) never appear in the host list; duplicate names: first definition wins.
- Doc comments on every exported identifier; no package/variable shadowing; wrapped errors (`%w`).
- History file schema: `{"version":1,"entries":[{"host":"...","timestamp":"RFC3339"}]}`, max 100 entries, file mode 0600, dir 0700. Missing/corrupt history is never an error.

---

### Task 1: `internal/sshcfg` — include walker

**Files:**
- Create: `internal/sshcfg/walker.go`
- Test: `internal/sshcfg/walker_test.go`

**Interfaces:**
- Consumes: nothing (stdlib only).
- Produces: `func ConfigFiles(root, sshDir string) (files []string, warnings []string)` — root plus every file reachable via `Include`, OpenSSH read order, cycle-safe; `func expandInclude(pat, sshDir string) []string` (unexported helper).

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sshcfg/ -v`
Expected: FAIL — `undefined: ConfigFiles`, `undefined: expandInclude` (compile error counts as failing).

- [ ] **Step 3: Write the implementation**

```go
// Package sshcfg discovers ssh config files and enumerates the hosts they
// define. It is read-only over the config files: attribute values are
// informational (for display) — connection is delegated to ssh itself.
package sshcfg

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ConfigFiles returns root plus every file reachable via Include directives,
// in the order OpenSSH reads them. sshDir is the base for relative include
// paths (normally ~/.ssh — per OpenSSH, relative includes resolve against
// ~/.ssh, not the including file's directory). Include patterns that match
// nothing are skipped silently (OpenSSH behavior); include cycles are broken
// with a visited set. warnings is reserved for downstream callers and is
// always empty here.
func ConfigFiles(root, sshDir string) (files []string, warnings []string) {
	visited := make(map[string]bool)
	var walk func(path string)
	walk = func(path string) {
		if visited[path] {
			return
		}
		visited[path] = true
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		files = append(files, path)
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			fields := strings.Fields(strings.TrimSpace(scanner.Text()))
			if len(fields) < 2 || !strings.EqualFold(fields[0], "Include") {
				continue
			}
			for _, pat := range fields[1:] {
				for _, match := range expandInclude(pat, sshDir) {
					walk(match)
				}
			}
		}
	}
	walk(root)
	return files, nil
}

// expandInclude resolves one Include token to concrete file paths: strips
// quotes, expands a leading ~/, anchors relative paths at sshDir, and globs.
func expandInclude(pat, sshDir string) []string {
	pat = strings.Trim(pat, `"`)
	if strings.HasPrefix(pat, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		pat = filepath.Join(home, pat[2:])
	}
	if !filepath.IsAbs(pat) {
		pat = filepath.Join(sshDir, pat)
	}
	matches, _ := filepath.Glob(pat)
	return matches
}
```

Note: `defer f.Close()` here is inside a *recursive function*, not a loop — each call closes its own file when its walk finishes. That satisfies the readability convention.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sshcfg/ -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/sshcfg/
git commit -m "feat: add include-aware ssh config file walker"
```

---

### Task 2: `internal/host` — Host model

**Files:**
- Create: `internal/host/host.go`
- Test: `internal/host/host_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Host struct { Name, User, Hostname, Port, IdentityFile, Source, Group string; LastConnected time.Time }`; `func (h Host) Display() string`.

- [ ] **Step 1: Write the failing test**

```go
package host

import "testing"

func TestDisplayWithUser(t *testing.T) {
	h := Host{Name: "web-prod", User: "deploy", Hostname: "10.0.1.20", Port: "22"}
	if got, want := h.Display(), "web-prod (deploy@10.0.1.20:22)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDisplayWithoutUserOmitsAt(t *testing.T) {
	h := Host{Name: "nas", Hostname: "nas.local", Port: "2222"}
	if got, want := h.Display(), "nas (nas.local:2222)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/host/ -v`
Expected: FAIL — `undefined: Host`.

- [ ] **Step 3: Write the implementation**

```go
// Package host models a concrete ssh host and hopper's handoffs to the
// OpenSSH tools (ssh, ssh-add).
package host

import (
	"fmt"
	"time"
)

// Host is one concrete (non-wildcard) entry from the ssh config files.
// All attribute fields are informational — connection uses only Name.
type Host struct {
	Name          string
	User          string
	Hostname      string
	Port          string
	IdentityFile  string
	Source        string    // config file the host is defined in
	Group         string    // derived from Source
	LastConnected time.Time // zero if never connected
}

// Display returns the one-line picker label, e.g. "web-prod (deploy@10.0.1.20:22)".
// The user@ part is omitted when no User is configured.
func (h Host) Display() string {
	target := h.Hostname
	if h.User != "" {
		target = h.User + "@" + h.Hostname
	}
	return fmt.Sprintf("%s (%s:%s)", h.Name, target, h.Port)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/host/ -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/host/
git commit -m "feat: add Host model with display formatting"
```

---

### Task 3: `internal/sshcfg` — host enumeration with groups

**Files:**
- Create: `internal/sshcfg/hosts.go`
- Test: `internal/sshcfg/hosts_test.go`

**Interfaces:**
- Consumes: `ConfigFiles(root, sshDir)` (Task 1); `host.Host` (Task 2); `ssh_config.Decode`, `(*ssh_config.Config).Get`.
- Produces: `func Hosts(root, sshDir string) ([]host.Host, []string, error)` — hosts in definition order, warnings for skipped included files, error only for unreadable root; `func group(path, root string) string` (unexported).

- [ ] **Step 1: Write the failing test**

```go
package sshcfg

import (
	"path/filepath"
	"testing"
)

func TestHostsAcrossIncludesWithGroups(t *testing.T) {
	dir := t.TempDir()
	root := write(t, dir, "config",
		"Include conf.d/*.conf\nHost rootHost\n  Hostname 10.0.0.1\n  User admin\n")
	write(t, dir, "conf.d/work.conf",
		"Host gitlab\n  Hostname git.example.com\n  Port 2222\n  IdentityFile ~/.ssh/id_work\n")
	hosts, warnings, err := Hosts(root, dir)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("err=%v warnings=%v", err, warnings)
	}
	if len(hosts) != 2 {
		t.Fatalf("got %d hosts, want 2: %+v", len(hosts), hosts)
	}
	rh, gl := hosts[0], hosts[1]
	if rh.Name != "rootHost" || rh.User != "admin" || rh.Group != "default" {
		t.Errorf("rootHost wrong: %+v", rh)
	}
	if gl.Name != "gitlab" || gl.Port != "2222" || gl.Group != "work" ||
		gl.IdentityFile != "~/.ssh/id_work" || gl.Source != filepath.Join(dir, "conf.d/work.conf") {
		t.Errorf("gitlab wrong: %+v", gl)
	}
}

func TestHostsSkipsWildcardsAndDedupes(t *testing.T) {
	dir := t.TempDir()
	root := write(t, dir, "config",
		"Host *\n  User fallback\nHost web-?\n  Port 8022\nHost dup\n  User first\nHost dup\n  User second\n")
	hosts, _, err := Hosts(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Name != "dup" {
		t.Fatalf("got %+v, want just dup", hosts)
	}
	if hosts[0].User != "first" {
		t.Errorf("first definition should win, got User=%q", hosts[0].User)
	}
}

func TestHostsDefaultsHostnameAndPort(t *testing.T) {
	dir := t.TempDir()
	root := write(t, dir, "config", "Host bare\n")
	hosts, _, err := Hosts(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	h := hosts[0]
	if h.Hostname != "bare" || h.Port != "22" || h.User != "" || h.IdentityFile != "" {
		t.Fatalf("bad defaults: %+v", h)
	}
}

func TestHostsMissingRootIsError(t *testing.T) {
	if _, _, err := Hosts(filepath.Join(t.TempDir(), "nope"), t.TempDir()); err == nil {
		t.Fatal("want error for missing root config")
	}
}

func TestHostsUnparsableIncludeWarnsAndContinues(t *testing.T) {
	dir := t.TempDir()
	root := write(t, dir, "config", "Include bad.conf\nHost ok\n")
	write(t, dir, "bad.conf", "Host broken\n  Port not-a-port \x00\n")
	hosts, _, err := Hosts(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if h.Name == "ok" {
			return
		}
	}
	t.Fatalf("host from root missing after bad include: %+v", hosts)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sshcfg/ -v`
Expected: FAIL — `undefined: Hosts`.

- [ ] **Step 3: Write the implementation**

```go
package sshcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kevinburke/ssh_config"

	"github.com/kejrak/hopper/internal/host"
)

// Hosts parses root and every included file and returns each concrete host
// exactly once, in definition order (first definition wins, the OpenSSH
// rule). Wildcard patterns (*, ?, !) are skipped. Attribute values come
// from the merged configuration and are informational only. warnings lists
// included files that were skipped; the returned error is non-nil only when
// the root config itself is unreadable.
func Hosts(root, sshDir string) ([]host.Host, []string, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, nil, fmt.Errorf("reading ssh config: %w", err)
	}
	files, warnings := ConfigFiles(root, sshDir)

	type sourcedConfig struct {
		cfg  *ssh_config.Config
		path string
	}
	var configs []sourcedConfig
	merged := &ssh_config.Config{}
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipping %s: %v", path, err))
			continue
		}
		cfg, err := ssh_config.Decode(f)
		f.Close()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipping %s: %v", path, err))
			continue
		}
		configs = append(configs, sourcedConfig{cfg: cfg, path: path})
		merged.Hosts = append(merged.Hosts, cfg.Hosts...)
	}

	var hosts []host.Host
	seen := make(map[string]bool)
	for _, sc := range configs {
		for _, block := range sc.cfg.Hosts {
			for _, pattern := range block.Patterns {
				name := pattern.String()
				if strings.ContainsAny(name, "*?!") || seen[name] {
					continue
				}
				seen[name] = true
				h := host.Host{
					Name:         name,
					User:         get(merged, name, "User"),
					Hostname:     get(merged, name, "Hostname"),
					Port:         get(merged, name, "Port"),
					IdentityFile: get(merged, name, "IdentityFile"),
					Source:       sc.path,
					Group:        group(sc.path, root),
				}
				if h.Hostname == "" {
					h.Hostname = name
				}
				if h.Port == "" {
					h.Port = "22"
				}
				hosts = append(hosts, h)
			}
		}
	}
	return hosts, warnings, nil
}

// get resolves one key from the merged config, treating lookup errors as
// "not set" — the value is display-only.
func get(cfg *ssh_config.Config, alias, key string) string {
	value, err := cfg.Get(alias, key)
	if err != nil {
		return ""
	}
	return value
}

// group derives the section name from the file a host is defined in: the
// root config maps to "default", any other file to its basename without
// extension ("conf.d/work.conf" → "work").
func group(path, root string) string {
	if path == root {
		return "default"
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sshcfg/ -v`
Expected: PASS. If `TestHostsUnparsableIncludeWarnsAndContinues` fails because `ssh_config.Decode` tolerates the malformed file (no error), that is acceptable — the host `ok` must still be present; adjust the fixture to a genuinely undecodable input only if the library errors on something else, otherwise keep the test asserting `ok` survives.

- [ ] **Step 5: Commit**

```bash
git add internal/sshcfg/
git commit -m "feat: enumerate hosts across includes with source-file groups"
```

---

### Task 4: `internal/host` — connection handoff

**Files:**
- Create: `internal/host/connect.go`
- Test: `internal/host/connect_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `func Command(name string) *exec.Cmd` (plain `ssh <name>`, inherited stdio); `func ExitCode(err error) int` (0 / ssh's code / 1).

- [ ] **Step 1: Write the failing test**

```go
package host

import (
	"errors"
	"os/exec"
	"testing"
)

func TestCommandPassesOnlyTheAlias(t *testing.T) {
	cmd := Command("web-prod")
	if len(cmd.Args) != 2 || cmd.Args[0] != "ssh" || cmd.Args[1] != "web-prod" {
		t.Fatalf("got args %v, want [ssh web-prod]", cmd.Args)
	}
	if cmd.Stdin == nil || cmd.Stdout == nil || cmd.Stderr == nil {
		t.Fatal("stdio must be inherited")
	}
}

func TestExitCodeMirrorsSSH(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Fatalf("nil error: got %d, want 0", got)
	}
	// Produce a real *exec.ExitError with code 3.
	err := exec.Command("sh", "-c", "exit 3").Run()
	if got := ExitCode(err); got != 3 {
		t.Fatalf("exit 3: got %d", got)
	}
	if got := ExitCode(errors.New("ssh not found")); got != 1 {
		t.Fatalf("other error: got %d, want 1", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/host/ -v`
Expected: FAIL — `undefined: Command`, `undefined: ExitCode`.

- [ ] **Step 3: Write the implementation**

```go
package host

import (
	"errors"
	"os"
	"os/exec"
)

// Command builds the connection command. Only the host alias is passed:
// OpenSSH resolves User/Port/IdentityFile/ProxyJump itself from its own
// configuration, so hopper can never contradict it.
func Command(name string) *exec.Cmd {
	cmd := exec.Command("ssh", name)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// ExitCode maps a Run error to the code hopper should exit with: 0 on
// success, ssh's own exit code when it exited non-zero, 1 for anything
// else (e.g. the ssh binary is missing).
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/host/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/host/
git commit -m "feat: delegate connection to plain ssh with mirrored exit code"
```

---

### Task 5: Rewire `main.go`, delete legacy packages, fix makefile

Milestone: the app works end-to-end on the new packages (still with go-fuzzyfinder as the picker — the TUI replaces it in Task 13).

**Files:**
- Modify: `main.go` (full rewrite)
- Delete: `config/config.go`, `hosts/hosts.go`
- Modify: `makefile`

**Interfaces:**
- Consumes: `sshcfg.Hosts`, `host.Host`, `host.Display`, `host.Command`, `host.ExitCode`.
- Produces: `func run() int` in package main (later tasks extend it).

- [ ] **Step 1: Rewrite `main.go`**

```go
// Command hopper is a TUI for picking an SSH host from ~/.ssh/config
// (including Include files) and connecting to it.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ktr0731/go-fuzzyfinder"

	"github.com/kejrak/hopper/internal/host"
	"github.com/kejrak/hopper/internal/sshcfg"
)

func main() {
	os.Exit(run())
}

func run() int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hopper:", err)
		return 1
	}
	sshDir := filepath.Join(home, ".ssh")
	root := filepath.Join(sshDir, "config")

	hosts, warnings, err := sshcfg.Hosts(root, sshDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hopper:", err)
		return 1
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "hopper: warning:", w)
	}
	if len(hosts) == 0 {
		fmt.Fprintln(os.Stderr, "hopper: no hosts found in", root)
		return 1
	}

	idx, err := fuzzyfinder.Find(hosts, func(i int) string {
		return hosts[i].Display()
	})
	if err != nil {
		// User cancelled the picker (esc / ctrl+c).
		return 0
	}
	return host.ExitCode(host.Command(hosts[idx].Name).Run())
}
```

- [ ] **Step 2: Delete the legacy packages**

```bash
git rm -r config hosts
```

- [ ] **Step 3: Fix the makefile**

Replace the whole file with:

```make
BIN := bin/hopper

build:
	go build -o $(BIN) .

run: build
	./$(BIN)

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

.PHONY: build run fmt vet test
```

(The old `go build -o bin/hopper main.go` breaks the moment code spans packages; `go build .` does not.)

- [ ] **Step 4: Verify**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all pass, no references to the deleted packages remain (`grep -r "kejrak/hopper/config\|kejrak/hopper/hosts" --include='*.go' .` finds nothing).
Manual smoke test: `make run` — picker shows hosts from your real config including included files; selecting one runs `ssh <name>`; esc exits 0 (`echo $?`).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: rewire main onto internal packages, delegate to plain ssh"
```

---

### Task 6: Lint tooling and readability sweep

**Files:**
- Create: `.golangci.yml`
- Modify: `makefile`
- Modify: any file golangci-lint flags

**Interfaces:** none (tooling only).

- [ ] **Step 1: Add `.golangci.yml`**

```yaml
version: "2"
linters:
  enable:
    - errcheck
    - govet
    - staticcheck
    - revive
```

- [ ] **Step 2: Add the lint target to the makefile**

```make
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not installed — see https://golangci-lint.run/usage/install/"; exit 1; }
	golangci-lint run
```

Also add `lint` to the `.PHONY` line.

- [ ] **Step 3: Run and fix findings**

Run: `gofmt -l .` (expect no output), `go vet ./...`, and — if golangci-lint is installed — `make lint`. Fix every finding it reports in the touched files (typical: unchecked `f.Close()` returns in tests, missing doc comments on exported identifiers). If golangci-lint is not installed locally, install it with `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`; if the network forbids that, note it in the commit message and rely on `go vet`.

- [ ] **Step 4: Verify**

Run: `make fmt vet test` — all pass; `git diff` shows only mechanical fixes.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: add golangci-lint config and make targets"
```

---

### Task 7: `internal/history` — state file

**Files:**
- Create: `internal/history/history.go`
- Test: `internal/history/history_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Entry struct { Host string; Timestamp time.Time }` (JSON tags `host`, `timestamp`); `func Path() (string, error)`; `func Load(path string) []Entry` (newest first, nil on missing/corrupt); `func Record(path, hostName string, now time.Time) error`; `func Recent(entries []Entry, n int) []string` (distinct hosts, newest first); `func LastConnected(entries []Entry) map[string]time.Time`.

- [ ] **Step 1: Write the failing test**

```go
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

func TestLastConnected(t *testing.T) {
	entries := []Entry{
		{Host: "a", Timestamp: ts("2026-08-10T10:00:00Z")},
		{Host: "a", Timestamp: ts("2026-08-10T12:00:00Z")},
	}
	if got := LastConnected(entries)["a"]; !got.Equal(ts("2026-08-10T12:00:00Z")) {
		t.Fatalf("got %v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/history/ -v`
Expected: FAIL — undefined identifiers.

- [ ] **Step 3: Write the implementation**

```go
// Package history persists hopper's connection history in a small JSON
// state file. History powers the RECENT section and last-connected display;
// a missing or corrupt file is never an error.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

// Entry records one host selection.
type Entry struct {
	Host      string    `json:"host"`
	Timestamp time.Time `json:"timestamp"`
}

type stateFile struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

const maxEntries = 100

// Path returns the platform-appropriate history file location: on Linux
// $XDG_STATE_HOME/hopper/history.json (default ~/.local/state/...), on
// macOS/Windows the os.UserConfigDir equivalent.
func Path() (string, error) {
	if runtime.GOOS == "linux" {
		if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
			return filepath.Join(dir, "hopper", "history.json"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state", "hopper", "history.json"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hopper", "history.json"), nil
}

// Load reads entries newest-first. Missing or corrupt files yield nil.
func Load(path string) []Entry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f stateFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil
	}
	sort.SliceStable(f.Entries, func(i, j int) bool {
		return f.Entries[i].Timestamp.After(f.Entries[j].Timestamp)
	})
	return f.Entries
}

// Record prepends an entry and rewrites the file, capped at maxEntries.
func Record(path, hostName string, now time.Time) error {
	entries := append([]Entry{{Host: hostName, Timestamp: now}}, Load(path)...)
	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(stateFile{Version: 1, Entries: entries}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Recent returns up to n distinct host names, newest first.
func Recent(entries []Entry, n int) []string {
	var out []string
	seen := make(map[string]bool)
	for _, e := range entries {
		if seen[e.Host] {
			continue
		}
		seen[e.Host] = true
		out = append(out, e.Host)
		if len(out) == n {
			break
		}
	}
	return out
}

// LastConnected returns each host's most recent timestamp.
func LastConnected(entries []Entry) map[string]time.Time {
	out := make(map[string]time.Time)
	for _, e := range entries {
		if e.Timestamp.After(out[e.Host]) {
			out[e.Host] = e.Timestamp
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/history/ -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/history/
git commit -m "feat: add JSON connection-history state file"
```

---

### Task 8: `internal/host` — ssh-agent operations

**Files:**
- Create: `internal/host/agent.go`
- Test: `internal/host/agent_test.go`

**Interfaces:**
- Consumes: `Host` (Task 2).
- Produces: `type KeyStatus int` with `KeyStatusUnknown` (no identity configured), `KeyStatusNoAgent`, `KeyStatusNotLoaded`, `KeyStatusLoaded`; `func AgentStatuses(hosts []Host) map[string]KeyStatus` (one `ssh-add -l` call for all hosts, keyed by host name); `func AddKeyCommand(identityFile string) *exec.Cmd`; `func ExpandPath(p string) string`; `func keyListed(list, fp, path string) bool` (unexported, pure).

- [ ] **Step 1: Write the failing test**

```go
package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const agentList = `256 SHA256:AbCdEf123 /home/u/.ssh/id_work (ED25519)
3072 SHA256:XyZ987 someone@laptop (RSA)
`

func TestKeyListedByFingerprint(t *testing.T) {
	if !keyListed(agentList, "SHA256:AbCdEf123", "") {
		t.Fatal("fingerprint present but not matched")
	}
	if keyListed(agentList, "SHA256:Missing", "") {
		t.Fatal("absent fingerprint matched")
	}
}

func TestKeyListedFallsBackToPathWhenNoFingerprint(t *testing.T) {
	if !keyListed(agentList, "", "/home/u/.ssh/id_work") {
		t.Fatal("path in comment but not matched")
	}
	if keyListed(agentList, "", "/home/u/.ssh/other") {
		t.Fatal("absent path matched")
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got, want := ExpandPath("~/.ssh/id_work"), filepath.Join(home, ".ssh", "id_work"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := ExpandPath("/abs/path"); got != "/abs/path" {
		t.Fatalf("absolute path changed: %q", got)
	}
}

func TestAddKeyCommand(t *testing.T) {
	cmd := AddKeyCommand("~/.ssh/id_work")
	if cmd.Args[0] != "ssh-add" || len(cmd.Args) != 2 {
		t.Fatalf("got %v", cmd.Args)
	}
	if strings.HasPrefix(cmd.Args[1], "~") {
		t.Fatalf("path not expanded: %v", cmd.Args)
	}
}

func TestAgentStatusesUnknownWithoutIdentity(t *testing.T) {
	statuses := AgentStatuses([]Host{{Name: "h"}})
	if statuses["h"] != KeyStatusUnknown {
		t.Fatalf("got %v, want KeyStatusUnknown", statuses["h"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/host/ -v`
Expected: FAIL — undefined identifiers.

- [ ] **Step 3: Write the implementation**

```go
package host

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// KeyStatus describes whether a host's identity file is loaded in the
// ssh-agent.
type KeyStatus int

// Key statuses, from least to most known.
const (
	KeyStatusUnknown   KeyStatus = iota // no IdentityFile configured
	KeyStatusNoAgent                    // agent unreachable (SSH_AUTH_SOCK)
	KeyStatusNotLoaded                  // agent up, key not loaded
	KeyStatusLoaded                     // key loaded
)

// ExpandPath expands a leading ~/ to the user's home directory; other paths
// pass through unchanged.
func ExpandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// AgentStatuses queries the agent once (ssh-add -l) and reports each host's
// key status, keyed by host name. Hosts without an IdentityFile map to
// KeyStatusUnknown; if no agent is reachable every keyed host maps to
// KeyStatusNoAgent.
func AgentStatuses(hosts []Host) map[string]KeyStatus {
	statuses := make(map[string]KeyStatus, len(hosts))
	list, agentOK := agentKeyList()
	for _, h := range hosts {
		switch {
		case h.IdentityFile == "":
			statuses[h.Name] = KeyStatusUnknown
		case !agentOK:
			statuses[h.Name] = KeyStatusNoAgent
		case keyListed(list, fingerprint(h.IdentityFile), ExpandPath(h.IdentityFile)):
			statuses[h.Name] = KeyStatusLoaded
		default:
			statuses[h.Name] = KeyStatusNotLoaded
		}
	}
	return statuses
}

// agentKeyList runs ssh-add -l. ok is false only when no agent is
// reachable (ssh-add exit code 2 or the binary is missing); an empty agent
// (exit code 1) returns "" with ok true.
func agentKeyList() (list string, ok bool) {
	out, err := exec.Command("ssh-add", "-l").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", true
		}
		return "", false
	}
	return string(out), true
}

// fingerprint returns the SHA256 fingerprint of the identity's public key
// via ssh-keygen -lf on the .pub file, or "" when unavailable.
func fingerprint(identityFile string) string {
	out, err := exec.Command("ssh-keygen", "-lf", ExpandPath(identityFile)+".pub").Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// keyListed reports whether the ssh-add -l output contains the key,
// matching by fingerprint when one is known, otherwise by the identity
// path appearing in the key comment.
func keyListed(list, fp, path string) bool {
	for _, line := range strings.Split(list, "\n") {
		if fp != "" && strings.Contains(line, fp) {
			return true
		}
		if fp == "" && path != "" && strings.Contains(line, path) {
			return true
		}
	}
	return false
}

// AddKeyCommand returns the interactive ssh-add invocation. The caller must
// run it with the terminal released so the passphrase prompt reaches the
// tty — hopper never handles the passphrase itself.
func AddKeyCommand(identityFile string) *exec.Cmd {
	cmd := exec.Command("ssh-add", ExpandPath(identityFile))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}
```

Note: `TestAgentStatusesUnknownWithoutIdentity` runs `ssh-add -l` for real; the assertion only covers the no-identity host, which never depends on the agent's answer.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/host/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/host/
git commit -m "feat: add ssh-agent status query and ssh-add handoff"
```

---

### Task 9: `internal/ui` — TUI skeleton (filter + list + select)

**Files:**
- Create: `internal/ui/ui.go`, `internal/ui/items.go`
- Test: `internal/ui/ui_test.go`
- Modify: `go.mod` (add bubbletea, bubbles, lipgloss)

**Interfaces:**
- Consumes: `host.Host`, `host.KeyStatus`, `host.AgentStatuses`.
- Produces: `type Action int` (`ActionQuit`, `ActionConnect`); `type Result struct { Action Action; Host host.Host }`; `type ReloadFunc func() ([]host.Host, []string, error)`; `func Run(hosts []host.Host, recent []string, reload ReloadFunc) (Result, error)`; internals used by later tasks: `type item struct { header string; h *host.Host }`, `func buildItems(hosts []host.Host, recent []string, filter string) []item`, `func fuzzyMatch(needle, hay string) bool`, `type model struct{...}` with `newModel`, `(model).selected() *host.Host`, `(model).moveCursor(delta int)`.

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/charmbracelet/bubbletea@latest \
       github.com/charmbracelet/bubbles@latest \
       github.com/charmbracelet/lipgloss@latest
```

- [ ] **Step 2: Write the failing test**

```go
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
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/ui/ -v`
Expected: FAIL — undefined identifiers.

- [ ] **Step 4: Write the implementation**

`internal/ui/items.go`:

```go
// Package ui renders hopper's two-pane host picker: a fuzzy-filtered,
// sectioned host list on the left and host details on the right.
package ui

import (
	"sort"
	"strings"

	"github.com/kejrak/hopper/internal/host"
)

// item is one row of the left pane: either a section header or a host.
type item struct {
	header string     // non-empty → section header row
	h      *host.Host // non-nil → host row
}

// fuzzyMatch reports whether needle's bytes appear in hay in order,
// case-insensitively — "wpr" matches "web-prod". ASCII-oriented, which
// covers ssh host aliases.
func fuzzyMatch(needle, hay string) bool {
	needle = strings.ToLower(needle)
	hay = strings.ToLower(hay)
	i := 0
	for j := 0; j < len(hay) && i < len(needle); j++ {
		if needle[i] == hay[j] {
			i++
		}
	}
	return i == len(needle)
}

// matches applies the filter to a host's name, hostname and user.
func matches(h host.Host, filter string) bool {
	return fuzzyMatch(filter, h.Name) || fuzzyMatch(filter, h.Hostname) || fuzzyMatch(filter, h.User)
}

// buildItems assembles the sectioned rows: RECENT first (hosts named in
// recent, in that order), then one section per group — "default" first,
// the rest alphabetical. The filter applies to every section.
func buildItems(hosts []host.Host, recent []string, filter string) []item {
	byName := make(map[string]*host.Host, len(hosts))
	for i := range hosts {
		byName[hosts[i].Name] = &hosts[i]
	}

	var items []item
	var recentRows []item
	for _, name := range recent {
		if h, ok := byName[name]; ok && matches(*h, filter) {
			recentRows = append(recentRows, item{h: h})
		}
	}
	if len(recentRows) > 0 {
		items = append(items, item{header: "RECENT"})
		items = append(items, recentRows...)
	}

	groups := make(map[string][]*host.Host)
	for i := range hosts {
		h := &hosts[i]
		if matches(*h, filter) {
			groups[h.Group] = append(groups[h.Group], h)
		}
	}
	names := make([]string, 0, len(groups))
	for g := range groups {
		if g != "default" {
			names = append(names, g)
		}
	}
	sort.Strings(names)
	if _, ok := groups["default"]; ok {
		names = append([]string{"default"}, names...)
	}
	for _, g := range names {
		items = append(items, item{header: strings.ToUpper(g)})
		for _, h := range groups[g] {
			items = append(items, item{h: h})
		}
	}
	return items
}
```

`internal/ui/ui.go`:

```go
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

type model struct {
	filter  textinput.Model
	hosts   []host.Host
	recent  []string
	items   []item
	cursor  int
	agent   map[string]host.KeyStatus
	status  string
	reload  ReloadFunc
	width   int
	height  int
	result  Result
}

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

// View implements tea.Model. Task 10 replaces this with the two-pane view.
func (m model) View() string {
	s := m.filter.View() + "\n"
	for i, it := range m.items {
		if it.header != "" {
			s += it.header + "\n"
			continue
		}
		marker := "  "
		if i == m.cursor {
			marker = "▸ "
		}
		s += marker + it.h.Display() + "\n"
	}
	return s
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
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/ui/ -v && go build ./...`
Expected: PASS. (`host.AgentStatuses` runs a real `ssh-add -l` in `newModel` — harmless in tests.)

- [ ] **Step 6: Commit**

```bash
git add internal/ui/ go.mod go.sum
git commit -m "feat: add Bubble Tea picker skeleton with sections and fuzzy filter"
```

---

### Task 10: `internal/ui` — two-pane view with detail panel

**Files:**
- Create: `internal/ui/view.go`
- Modify: `internal/ui/ui.go` (replace `View`, delete the placeholder body)
- Test: `internal/ui/view_test.go`

**Interfaces:**
- Consumes: model internals from Task 9; `host.KeyStatus` values.
- Produces: `func detailLines(h *host.Host, st host.KeyStatus, now time.Time) []string`; `func ago(t, now time.Time) string`; `func statusLabel(st host.KeyStatus) string`.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -v`
Expected: FAIL — undefined identifiers.

- [ ] **Step 3: Write the implementation**

`internal/ui/view.go`:

```go
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
```

In `internal/ui/ui.go`, delete the placeholder `View` method (view.go now owns it).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/
git commit -m "feat: two-pane view with host detail panel"
```

---

### Task 11: `internal/ui` — ctrl+a ssh-add action

**Files:**
- Modify: `internal/ui/ui.go`
- Test: append to `internal/ui/ui_test.go`

**Interfaces:**
- Consumes: `host.AddKeyCommand`, `host.AgentStatuses`, `KeyStatus` values.
- Produces: `type keyAddedMsg struct { hostName string; err error }`; `case "ctrl+a"` in `Update`; status messages.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -v`
Expected: FAIL — `undefined: keyAddedMsg`; ctrl+a currently falls into the filter branch.

- [ ] **Step 3: Write the implementation**

Add to `internal/ui/ui.go`:

```go
// keyAddedMsg reports the outcome of an interactive ssh-add run.
type keyAddedMsg struct {
	hostName string
	err      error
}
```

Add a `case "ctrl+a":` in `Update`'s key switch, before the `default:`:

```go
		case "ctrl+a":
			h := m.selected()
			if h == nil {
				return m, nil
			}
			if h.IdentityFile == "" {
				m.status = "no identity file configured for " + h.Name
				return m, nil
			}
			switch m.agent[h.Name] {
			case host.KeyStatusLoaded:
				m.status = "key already loaded for " + h.Name
				return m, nil
			case host.KeyStatusNoAgent:
				m.status = "no ssh-agent available (SSH_AUTH_SOCK)"
				return m, nil
			}
			name := h.Name
			// Release the terminal so ssh-add's passphrase prompt reaches the tty.
			return m, tea.ExecProcess(host.AddKeyCommand(h.IdentityFile), func(err error) tea.Msg {
				return keyAddedMsg{hostName: name, err: err}
			})
```

Add a message case in `Update`'s outer switch:

```go
	case keyAddedMsg:
		if msg.err != nil {
			m.status = "ssh-add failed: " + msg.err.Error()
		} else {
			m.status = "key loaded for " + msg.hostName
		}
		m.agent = host.AgentStatuses(m.hosts)
		return m, nil
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/
git commit -m "feat: ctrl+a loads the highlighted host's key via ssh-add"
```

---

### Task 12: `internal/ui` — ctrl+e edit-in-$EDITOR action

**Files:**
- Modify: `internal/ui/ui.go`
- Test: append to `internal/ui/ui_test.go`

**Interfaces:**
- Consumes: `ReloadFunc` (Task 9), `os.Getenv("EDITOR")`.
- Produces: `type editorDoneMsg struct{ err error }`; `case "ctrl+e"` in `Update`.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -v`
Expected: FAIL — `undefined: editorDoneMsg`.

- [ ] **Step 3: Write the implementation**

Add to `internal/ui/ui.go`:

```go
// editorDoneMsg reports that $EDITOR exited; the config is re-read either way.
type editorDoneMsg struct {
	err error
}
```

Add a `case "ctrl+e":` in `Update`'s key switch:

```go
		case "ctrl+e":
			h := m.selected()
			if h == nil {
				return m, nil
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				m.status = "$EDITOR is not set"
				return m, nil
			}
			cmd := exec.Command(editor, h.Source)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return editorDoneMsg{err: err}
			})
```

Add the message case:

```go
	case editorDoneMsg:
		if msg.err != nil {
			m.status = "editor: " + msg.err.Error()
		}
		if m.reload != nil {
			hosts, warnings, err := m.reload()
			switch {
			case err != nil:
				m.status = "reload failed: " + err.Error()
			case len(warnings) > 0:
				m.status = warnings[0]
				m.hosts = hosts
			default:
				m.hosts = hosts
			}
			m.agent = host.AgentStatuses(m.hosts)
			m.refilter()
		}
		return m, nil
```

Add `"os"` and `"os/exec"` to the imports of `ui.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/
git commit -m "feat: ctrl+e edits the host's source config in \$EDITOR and reloads"
```

---

### Task 13: Final wiring — TUI in main, history recording, drop fuzzyfinder, README

**Files:**
- Modify: `main.go`
- Modify: `go.mod`/`go.sum` (via `go mod tidy` — removes go-fuzzyfinder)
- Modify: `README.md`

**Interfaces:**
- Consumes: `ui.Run`, `ui.ActionConnect`, `history.Path/Load/Record/Recent/LastConnected`, `host.Command`, `host.ExitCode`.
- Produces: the final binary behavior.

- [ ] **Step 1: Rewrite `run()` in `main.go`**

```go
// Command hopper is a TUI for picking an SSH host from ~/.ssh/config
// (including Include files) and connecting to it. Connection is delegated
// entirely to ssh: hopper runs `ssh <name>` and mirrors its exit code.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kejrak/hopper/internal/history"
	"github.com/kejrak/hopper/internal/host"
	"github.com/kejrak/hopper/internal/sshcfg"
	"github.com/kejrak/hopper/internal/ui"
)

func main() {
	os.Exit(run())
}

func run() int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hopper:", err)
		return 1
	}
	sshDir := filepath.Join(home, ".ssh")
	root := filepath.Join(sshDir, "config")

	loadHosts := func() ([]host.Host, []string, error) {
		return sshcfg.Hosts(root, sshDir)
	}
	hosts, warnings, err := loadHosts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hopper:", err)
		return 1
	}
	for _, warning := range warnings {
		fmt.Fprintln(os.Stderr, "hopper: warning:", warning)
	}
	if len(hosts) == 0 {
		fmt.Fprintln(os.Stderr, "hopper: no hosts found in", root)
		return 1
	}

	histPath, histErr := history.Path()
	var entries []history.Entry
	if histErr == nil {
		entries = history.Load(histPath)
	}
	last := history.LastConnected(entries)
	for i := range hosts {
		hosts[i].LastConnected = last[hosts[i].Name]
	}

	result, err := ui.Run(hosts, history.Recent(entries, 5), loadHosts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hopper:", err)
		return 1
	}
	if result.Action != ui.ActionConnect {
		return 0
	}
	if histErr == nil {
		if err := history.Record(histPath, result.Host.Name, time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, "hopper: warning: recording history:", err)
		}
	}
	return host.ExitCode(host.Command(result.Host.Name).Run())
}
```

- [ ] **Step 2: Drop go-fuzzyfinder**

```bash
go mod tidy
```

Verify `github.com/ktr0731/go-fuzzyfinder` is gone from `go.mod`.

- [ ] **Step 3: Update `README.md`**

Replace the **Features** and **Usage** sections with:

```markdown
## Features

-   Parses your `~/.ssh/config` file, including `Include` directives — hosts from all config files appear, grouped by the file they live in.
-   Two-pane TUI: fuzzy-filtered host list with a RECENT section, plus a detail panel (user, hostname, port, identity file, agent status, source file, last connected).
-   Connects with plain `ssh <host>` — OpenSSH resolves users, ports, keys, and ProxyJump itself, and hopper mirrors ssh's exit code.
-   `ctrl+a` loads the highlighted host's key into your ssh-agent (`ssh-add`, passphrase prompted by ssh-add itself).
-   `ctrl+e` opens the host's config file in `$EDITOR` and reloads the list afterwards.
-   Cross-platform (macOS, Linux, Windows).

## Usage

Run `hopper` in your terminal:

```sh
hopper
```

Type to fuzzy-filter, `↑`/`↓` to move, then:

| Key | Action |
|---|---|
| `enter` | Connect to the highlighted host |
| `ctrl+a` | Add the host's key to the ssh-agent |
| `ctrl+e` | Edit the host's config file in `$EDITOR` |
| `esc` / `ctrl+c` | Quit |

Connection history is stored in a small local state file (`~/.local/state/hopper/history.json` on Linux, platform equivalent elsewhere) to power the RECENT section — delete it any time.
```

- [ ] **Step 4: Full verification**

Run: `make fmt vet test && go build ./...`
Expected: everything passes.
Manual acceptance pass (mirrors the spec's §7 scenarios):
1. `make run` — grouped list with RECENT (empty on first run), detail panel populated.
2. Select a host with `enter` — connects via `ssh <name>`; after exit, `echo $?` mirrors ssh.
3. Run again — RECENT shows the host, "Last" shows a fresh relative time.
4. `ctrl+a` on a host with a passphrase-protected key — prompt appears, panel flips to `● loaded`.
5. `ctrl+e` — `$EDITOR` opens the right file; edits appear after quitting.
6. `esc` — exits 0, no history entry.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat: full-manager TUI with history, ssh-add, and editor integration"
```

---

## Plan Self-Review Notes

- **Spec coverage:** §1/§4 refactor → Tasks 1–5; readability/tooling → Task 6 (+ conventions applied throughout); history/grouping → Tasks 3, 7; TUI full manager → Tasks 9–12; ssh-add → Tasks 8, 11; final wiring/README → Task 13. Acceptance scenarios in §7 map to the unit tests plus Task 13's manual pass.
- **Deviation from the analyze doc:** history lives in `internal/history` rather than inside `internal/host` — the state file has its own lifecycle and keeps `host` focused on OpenSSH handoffs. The analyze doc's three-package sketch predates the history feature.
- **Type consistency check:** `Result`/`Action`/`ReloadFunc` (Task 9) match their uses in Tasks 11–13; `KeyStatus` constants (Task 8) match `statusLabel` (Task 10) and the ctrl+a flow (Task 11); `history.Entry` fields (Task 7) match main's wiring (Task 13).
