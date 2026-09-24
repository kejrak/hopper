# Exec Audit Log and Group Allowlist Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a human restrict which ssh-config groups hopper's non-interactive commands may use (`HOPPER_ALLOW_GROUPS`) and keep an append-only audit log of every `hopper exec`, readable with `hopper log`.

**Architecture:** Group filtering lives in `internal/cli` (`AllowedGroups`, `FilterGroups`, `Resolve`) and is wired into `list`/`show`/`exec`/`log` in `main.go`; the TUI ignores it. A new `internal/audit` package owns the JSON Lines log format (`Record`, `Append`, `Load`, `Runs`); `main.go` writes start/end/refused records around `exec`, and `cli.Log` renders runs for `hopper log`.

**Tech Stack:** Go 1.24 stdlib only (`crypto/rand`, `encoding/json`, `text/tabwriter`, `bufio`), existing `internal/{cli,host,history,sshcfg}`.

**Spec:** In-chat design approved 2026-09-25 (answers: "Yes: `hopper log`", "Env var"). Requirements reproduced under Global Constraints.

## Global Constraints

- **Go command on this machine:** always `env -u GOROOT go …`; lint is `env -u GOROOT golangci-lint run` and must print `0 issues.` If errcheck flags unchecked writes, use `_, _ = …` — never disable or configure a linter.
- No new third-party dependencies.
- Env var name: `HOPPER_ALLOW_GROUPS`, comma-separated, whitespace around names ignored, exact (case-sensitive) group match. Unset or empty (or only commas/spaces) → all groups allowed (current behavior unchanged).
- A host's group is `host.Host.Group` as produced by `sshcfg` (root `~/.ssh/config` → `default`, other files → basename without extension).
- The allowlist applies to `list`, `show`, `exec`, and `log`; the TUI (bare `hopper`) ignores it.
- Disallowed host in `show`/`exec` → exit 1, stderr line naming host, its group, and the env var value, e.g. `hopper: group not allowed: host "web" is in group "default"; HOPPER_ALLOW_GROUPS="work"`. `exec` never runs ssh for it. `list` silently omits disallowed hosts.
- Audit log path: `exec.log` in the same directory as the history file (`filepath.Join(filepath.Dir(history.Path()), "exec.log")`). Directory mode 0700, file mode 0600, JSON Lines, append-only, never rotated.
- Audit records: `start` (written before ssh runs: time, id, host, group, command argv, working dir), `end` (id, host, exit_code, duration_ms), `refused` (id, host, command, dir, reason — for unknown or disallowed hosts). Usage errors are not logged.
- Audit write failure → a `hopper: warning: audit log: …` line on stderr for each failed write; the command still runs and hopper still exits with ssh's code.
- `hopper log [-n N] [--json]`: last N runs (default 20), oldest first; table for humans, JSON array for machines (`[]` when empty); `-h`/`--help` anywhere → usage on stdout, exit 0; bad args → exit 2.
- stdout carries only command output; errors/warnings go to stderr prefixed `hopper:`. Exit codes: 0 ok, 1 runtime error, 2 usage error, otherwise ssh's code.
- Doc comments on every exported identifier; wrapped errors (`%w`); follow existing style.
- Commit messages: conventional style, **no** `Co-Authored-By` or "Generated with" footers.

## Review Focus

1. Audit log unwritable (read-only disk, path is a file) → exec must still run and return ssh's exit code, with an audit warning on stderr. (Task 3: `TestExecAuditFailureOnlyWarns`)
2. An agent's timeout kills hopper mid-run → the `start` record already exists and `hopper log` shows the run as `unfinished`. (Task 2: `TestRunsPairsRecords`; start written before ssh in Task 3: `TestExecWritesAuditStartAndEnd`)
3. Several agent `exec` calls in parallel → every line intact, none lost. (Task 2: `TestAppendConcurrent`)
4. Multi-line / heredoc commands (common for agents) → `hopper log` table stays one row per run. (Task 3: `TestLogTextFlattensCommand`)
5. Refusal message lets an agent tell a typo from a restriction → names the group and the allowlist. (Task 1: `TestAllowGroupsShowDisallowed`, `TestResolve`)

---

### Task 1: Group allowlist for list/show/exec

**Files:**
- Modify: `internal/cli/cli.go` (add `ErrGroupNotAllowed`, `AllowedGroups`, `FilterGroups`, `Resolve`; change `Show` signature)
- Modify: `internal/cli/cli_test.go`
- Modify: `main.go` (usage text, `allowGroupsEnv`, `allowedGroups`, `resolveErrorMessage`, wire into runList/runShow/runExec)
- Modify: `main_test.go` (`setupHome` returns home; new group tests)
- Modify: `README.md`

**Interfaces:**
- Consumes: existing `cli.Find`, `cli.List`, `cli.ParseFlags`, `cli.ParseExec`, `host.Host` (field `Group`).
- Produces (later tasks rely on these exact names):
  - `var cli.ErrGroupNotAllowed error`
  - `func cli.AllowedGroups(spec string) map[string]bool` — nil means all allowed
  - `func cli.FilterGroups(hosts []host.Host, allowed map[string]bool) []host.Host`
  - `func cli.Resolve(hosts []host.Host, name string, allowed map[string]bool) (host.Host, error)`
  - `func cli.Show(w io.Writer, h host.Host, asJSON bool) error` (signature change)
  - in `package main`: `const allowGroupsEnv = "HOPPER_ALLOW_GROUPS"`, `func allowedGroups() map[string]bool`, `func resolveErrorMessage(err error) string`, `func setupHome(t *testing.T, config string) string` (test helper now returns the temp home dir)

- [ ] **Step 1: Write failing cli tests**

In `internal/cli/cli_test.go`:
- In `TestShowText` replace `Show(&b, testHosts, "db", false)` with `Show(&b, testHosts[1], false)`.
- In `TestShowJSON` replace `Show(&b, testHosts, "web", true)` with `Show(&b, testHosts[0], true)`.
- Delete `TestShowUnknownHost` entirely (unknown-host handling moves to `Resolve`, covered below).
- Append:

```go
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
```

(`testHosts[0]` is `web` in group `config`; `testHosts[1]` is `db` in group `work` — see the top of `cli_test.go`.)

- [ ] **Step 2: Run to verify failure**

Run: `env -u GOROOT go test ./internal/cli/`
Expected: compile FAIL — `undefined: AllowedGroups` / Show argument mismatch.

- [ ] **Step 3: Implement in `internal/cli/cli.go`**

Add below `ErrUsage`:

```go
// ErrGroupNotAllowed is returned when a host exists but its group is not
// in the allowlist.
var ErrGroupNotAllowed = errors.New("group not allowed")
```

Replace the existing `Show` function with:

```go
// Show writes one host's details as aligned "key: value" lines, or with
// asJSON a single JSON object.
func Show(w io.Writer, h host.Host, asJSON bool) error {
	if asJSON {
		return writeJSON(w, toJSON(h))
	}
	last := "never"
	if !h.LastConnected.IsZero() {
		last = h.LastConnected.Format(time.RFC3339)
	}
	fields := [][2]string{
		{"name", h.Name},
		{"user", h.User},
		{"hostname", h.Hostname},
		{"port", h.Port},
		{"identity_file", h.IdentityFile},
		{"source", h.Source},
		{"group", h.Group},
		{"last_connected", last},
	}
	var b strings.Builder
	for _, f := range fields {
		_, _ = fmt.Fprintf(&b, "%-16s%s\n", f[0]+":", f[1])
	}
	_, err := io.WriteString(w, b.String())
	return err
}
```

(Keep whatever `_, _ =` style the current file uses for `fmt.Fprintf` into the builder.)

Add after `Find`:

```go
// AllowedGroups parses a comma-separated group allowlist such as
// "bizznote, chutno". It returns nil, meaning every group is allowed,
// when spec names no group. Matching is exact and case-sensitive.
func AllowedGroups(spec string) map[string]bool {
	var allowed map[string]bool
	for _, group := range strings.Split(spec, ",") {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		if allowed == nil {
			allowed = make(map[string]bool)
		}
		allowed[group] = true
	}
	return allowed
}

// FilterGroups returns the hosts whose group is allowed, in their original
// order. A nil allowlist allows every host.
func FilterGroups(hosts []host.Host, allowed map[string]bool) []host.Host {
	if allowed == nil {
		return hosts
	}
	out := make([]host.Host, 0, len(hosts))
	for _, h := range hosts {
		if allowed[h.Group] {
			out = append(out, h)
		}
	}
	return out
}

// Resolve finds the named host and checks it against the allowlist. The
// error wraps ErrUnknownHost or ErrGroupNotAllowed; the latter names the
// host's group so a caller can tell a typo from a restriction.
func Resolve(hosts []host.Host, name string, allowed map[string]bool) (host.Host, error) {
	h, err := Find(hosts, name)
	if err != nil {
		return host.Host{}, err
	}
	if allowed != nil && !allowed[h.Group] {
		return host.Host{}, fmt.Errorf("%w: host %q is in group %q", ErrGroupNotAllowed, h.Name, h.Group)
	}
	return h, nil
}
```

Run: `env -u GOROOT go test ./internal/cli/` → PASS. (`main.go` will not compile yet because of the Show signature — fixed in Step 5.)

- [ ] **Step 4: Write failing main tests**

In `main_test.go`, change `setupHome` to return the home directory: signature `func setupHome(t *testing.T, config string) string`, and add `return home` as its last line. Existing callers that ignore the result keep compiling.

Append:

```go
// groupedConfig defines web in ~/.ssh/config (group "default") and pulls
// db from ~/.ssh/work (group "work").
const groupedConfig = `Include work

Host web
  HostName 10.0.0.1
`

func setupGroupedHome(t *testing.T) {
	t.Helper()
	home := setupHome(t, groupedConfig)
	if err := os.WriteFile(filepath.Join(home, ".ssh", "work"), []byte("Host db\n  HostName 10.0.0.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAllowGroupsFiltersList(t *testing.T) {
	setupGroupedHome(t)
	t.Setenv("HOPPER_ALLOW_GROUPS", "work")
	code, out, errOut := runArgs("list", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var hosts []map[string]any
	if err := json.Unmarshal([]byte(out), &hosts); err != nil {
		t.Fatalf("stdout is not JSON: %q: %v", out, err)
	}
	if len(hosts) != 1 || hosts[0]["name"] != "db" {
		t.Fatalf("got %v, want only db", hosts)
	}
}

func TestAllowGroupsUnsetListsAll(t *testing.T) {
	setupGroupedHome(t)
	t.Setenv("HOPPER_ALLOW_GROUPS", "")
	code, out, _ := runArgs("list")
	if code != 0 || !strings.Contains(out, "web\t") || !strings.Contains(out, "db\t") {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
}

func TestAllowGroupsShowDisallowed(t *testing.T) {
	setupGroupedHome(t)
	t.Setenv("HOPPER_ALLOW_GROUPS", "work")
	code, out, errOut := runArgs("show", "web")
	if code != 1 || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	for _, want := range []string{`"web"`, `group "default"`, `HOPPER_ALLOW_GROUPS="work"`} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q missing %s", errOut, want)
		}
	}
}

func TestAllowGroupsShowAllowed(t *testing.T) {
	setupGroupedHome(t)
	t.Setenv("HOPPER_ALLOW_GROUPS", "work")
	code, out, errOut := runArgs("show", "db")
	if code != 0 || !strings.Contains(out, "10.0.0.2") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestAllowGroupsExecDisallowedDoesNotRunSSH(t *testing.T) {
	setupGroupedHome(t)
	argsFile := fakeSSH(t, 0)
	t.Setenv("HOPPER_ALLOW_GROUPS", "work")
	code, _, errOut := runArgs("exec", "web", "--", "true")
	if code != 1 || !strings.Contains(errOut, "group not allowed") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Fatal("ssh must not run for a host outside the allowed groups")
	}
}

func TestAllowGroupsExecAllowed(t *testing.T) {
	setupGroupedHome(t)
	argsFile := fakeSSH(t, 0)
	t.Setenv("HOPPER_ALLOW_GROUPS", "work")
	if code, _, errOut := runArgs("exec", "db", "--", "true"); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(argsFile); err != nil {
		t.Fatalf("ssh stub was not run: %v", err)
	}
}
```

Run: `env -u GOROOT go test .` → FAIL (compile error from Show signature, then assertion failures).

- [ ] **Step 5: Wire into `main.go`**

Replace the `usage` constant with:

```go
const usage = `hopper - pick an SSH host from ~/.ssh/config and connect to it.

Usage:
  hopper                          interactive picker (needs a terminal)
  hopper list [--json]            list all hosts
  hopper show <host> [--json]     show one host's details
  hopper exec <host> [--] <cmd>   run a command on a host without prompts
                                  (ssh -o BatchMode=yes -T); exits with the
                                  remote command's code, 255 on ssh errors
  hopper help                     show this help

Environment:
  HOPPER_ALLOW_GROUPS             comma-separated groups (ssh config file
                                  names; "default" is ~/.ssh/config) that
                                  list, show and exec may use; unset = all
`
```

Add below `isTTY`:

```go
// allowGroupsEnv names the environment variable holding the group
// allowlist for the non-interactive commands.
const allowGroupsEnv = "HOPPER_ALLOW_GROUPS"

// allowedGroups returns the group allowlist; nil allows every group.
func allowedGroups() map[string]bool {
	return cli.AllowedGroups(os.Getenv(allowGroupsEnv))
}

// resolveErrorMessage formats a cli.Resolve error, naming the allowlist
// when a group restriction (not a typo) refused the host.
func resolveErrorMessage(err error) string {
	if errors.Is(err, cli.ErrGroupNotAllowed) {
		return fmt.Sprintf("%v; %s=%q", err, allowGroupsEnv, os.Getenv(allowGroupsEnv))
	}
	return err.Error()
}
```

In `runList`, change the output call to filter first:

```go
	if err := cli.List(stdout, cli.FilterGroups(hosts, allowedGroups()), asJSON); err != nil {
```

In `runShow`, replace the final `cli.Show` block with:

```go
	h, err := cli.Resolve(hosts, positional[0], allowedGroups())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper:", resolveErrorMessage(err))
		return 1
	}
	if err := cli.Show(stdout, h, asJSON); err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper:", err)
		return 1
	}
	return 0
```

In `runExec`, replace

```go
	h, err := cli.Find(hosts, name)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper:", err)
		return 1
	}
```

with

```go
	h, err := cli.Resolve(hosts, name, allowedGroups())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper:", resolveErrorMessage(err))
		return 1
	}
```

`runTUI` is unchanged (the TUI ignores the allowlist).

- [ ] **Step 6: Verify**

Run: `env -u GOROOT go test ./... && env -u GOROOT go vet ./... && gofmt -l . && env -u GOROOT golangci-lint run`
Expected: all PASS, vet clean, gofmt prints nothing, `0 issues.`

- [ ] **Step 7: README**

In `README.md`, insert immediately before the line `## Configuration`:

````markdown
### Limiting an agent to some host groups

A host's group is the name of the ssh config file it is defined in (`~/.ssh/config.d/bizznote` → `bizznote`; hosts in `~/.ssh/config` itself are `default`). Set `HOPPER_ALLOW_GROUPS` to a comma-separated list and `list`, `show` and `exec` only work with those groups: other hosts are left out of `list` and refused by `show`/`exec` (exit 1, with a message naming the host's group). The interactive TUI ignores it.

Set it where the agent can't change it per call — for example in a project's `.claude/settings.json`, so an agent working in that repository only reaches that project's servers:

```json
{
  "env": { "HOPPER_ALLOW_GROUPS": "bizznote" },
  "permissions": {
    "allow": ["Bash(hopper list:*)", "Bash(hopper show:*)"],
    "ask": ["Bash(hopper exec:*)"],
    "deny": ["Bash(ssh:*)", "Bash(scp:*)"]
  }
}
```

The allowlist is a guard rail, not a sandbox: an agent could still run plain `ssh` or prefix the command with its own `HOPPER_ALLOW_GROUPS=…`. The permission rules above close both gaps — raw `ssh` is denied, and a prefixed command no longer matches the allow rules, so you are asked first.

````

- [ ] **Step 8: Commit**

```bash
git add internal/cli main.go main_test.go README.md
git commit -m "feat: restrict list/show/exec to HOPPER_ALLOW_GROUPS"
```

---

### Task 2: `internal/audit` package — JSON Lines exec log

**Files:**
- Create: `internal/audit/audit.go`
- Test: `internal/audit/audit_test.go`

**Interfaces:**
- Consumes: stdlib only.
- Produces:
  - consts `EventStart = "start"`, `EventEnd = "end"`, `EventRefused = "refused"`; `StatusOK = "ok"`, `StatusFailed = "failed"`, `StatusRefused = "refused"`, `StatusUnfinished = "unfinished"`
  - `type Record struct { Time time.Time; Event, ID, Host, Group string; Command []string; Dir string; ExitCode *int; DurationMS *int64; Reason string }` (JSON tags below)
  - `type Run struct { ID string; Time time.Time; Host, Group string; Command []string; Dir, Status string; ExitCode *int; DurationMS *int64; Reason string }`
  - `func NewID() string`
  - `func Append(path string, r Record) error`
  - `func Load(path string) ([]Record, error)`
  - `func Runs(records []Record) []Run`

- [ ] **Step 1: Write the failing tests**

Create `internal/audit/audit_test.go`:

```go
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

func intp(v int) *int       { return &v }
func i64p(v int64) *int64   { return &v }

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
```

- [ ] **Step 2: Run to verify failure**

Run: `env -u GOROOT go test ./internal/audit/`
Expected: FAIL — `undefined: Append` etc.

- [ ] **Step 3: Implement**

Create `internal/audit/audit.go`:

```go
// Package audit keeps an append-only JSON Lines log of hopper exec runs so
// a human can review what scripts and AI agents ran on which host. Each run
// writes a "start" record before ssh starts — so a run killed midway still
// leaves a trace — and an "end" record with its exit code; a host refused
// before connecting gets a "refused" record.
package audit

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Record events.
const (
	EventStart   = "start"
	EventEnd     = "end"
	EventRefused = "refused"
)

// Run statuses.
const (
	StatusOK         = "ok"
	StatusFailed     = "failed"
	StatusRefused    = "refused"
	StatusUnfinished = "unfinished" // still running, or hopper was killed
)

// Record is one line of the log.
type Record struct {
	Time       time.Time `json:"time"`
	Event      string    `json:"event"`
	ID         string    `json:"id"`
	Host       string    `json:"host"`
	Group      string    `json:"group,omitempty"`
	Command    []string  `json:"command,omitempty"`
	Dir        string    `json:"dir,omitempty"`
	ExitCode   *int      `json:"exit_code,omitempty"`
	DurationMS *int64    `json:"duration_ms,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}

// Run is one exec invocation assembled from its records.
type Run struct {
	ID         string    `json:"id"`
	Time       time.Time `json:"time"`
	Host       string    `json:"host"`
	Group      string    `json:"group"`
	Command    []string  `json:"command"`
	Dir        string    `json:"dir"`
	Status     string    `json:"status"`
	ExitCode   *int      `json:"exit_code"`
	DurationMS *int64    `json:"duration_ms"`
	Reason     string    `json:"reason,omitempty"`
}

// NewID returns a random 16-hex-digit identifier pairing a run's start and
// end records.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error since Go 1.24
	return hex.EncodeToString(b)
}

// Append writes r as one JSON line, creating the directory (0700) and file
// (0600) as needed. Each record is a single O_APPEND write, so concurrent
// runs never interleave within a line.
func Append(path string, r Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Load reads every record in file order. A missing file yields no records
// and no error; blank or malformed lines are skipped.
func Load(path string) ([]Record, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var records []Record
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var r Record
			if json.Unmarshal(line, &r) == nil && r.Event != "" {
				records = append(records, r)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return records, nil
		}
		if readErr != nil {
			return records, readErr
		}
	}
}

// Runs pairs start and end records by ID and returns one Run per start or
// refused record, in log order. A start without an end is unfinished; an
// end without a start is ignored.
func Runs(records []Record) []Run {
	var runs []Run
	index := make(map[string]int)
	for _, r := range records {
		switch r.Event {
		case EventStart:
			index[r.ID] = len(runs)
			runs = append(runs, Run{ID: r.ID, Time: r.Time, Host: r.Host, Group: r.Group,
				Command: r.Command, Dir: r.Dir, Status: StatusUnfinished})
		case EventRefused:
			runs = append(runs, Run{ID: r.ID, Time: r.Time, Host: r.Host, Group: r.Group,
				Command: r.Command, Dir: r.Dir, Status: StatusRefused, Reason: r.Reason})
		case EventEnd:
			i, ok := index[r.ID]
			if !ok {
				continue
			}
			runs[i].ExitCode = r.ExitCode
			runs[i].DurationMS = r.DurationMS
			runs[i].Status = StatusFailed
			if r.ExitCode != nil && *r.ExitCode == 0 {
				runs[i].Status = StatusOK
			}
		}
	}
	return runs
}
```

- [ ] **Step 4: Verify**

Run: `env -u GOROOT go test -race ./internal/audit/ && env -u GOROOT go vet ./... && gofmt -l . && env -u GOROOT golangci-lint run`
Expected: PASS (race detector clean), vet clean, gofmt empty, `0 issues.` (If `-race` is unsupported in this environment, run without it and say so in the report.)

- [ ] **Step 5: Commit**

```bash
git add internal/audit
git commit -m "feat(audit): add JSON Lines exec audit log"
```

---

### Task 3: Audit `exec`, add `hopper log`, docs

**Files:**
- Modify: `internal/cli/cli.go` (add `DefaultLogRuns`, `ParseLog`, `FilterRuns`, `Log`)
- Modify: `internal/cli/cli_test.go`
- Modify: `main.go` (usage, `auditPath`, `appendAudit`, audited `runExec`, new `runLog`, dispatch `log`)
- Modify: `main_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: Task 1 `cli.Resolve`, `allowedGroups()`, `resolveErrorMessage()`, `setupHome(...) string`, `setupGroupedHome(t)`; Task 2 `audit.Record`, `audit.Run`, `audit.Append`, `audit.Load`, `audit.Runs`, `audit.NewID`, `audit.Event*`, `audit.Status*`; existing `history.Path`.
- Produces: `const cli.DefaultLogRuns = 20`; `func cli.ParseLog(args []string) (n int, asJSON bool, err error)`; `func cli.FilterRuns(runs []audit.Run, allowed map[string]bool) []audit.Run`; `func cli.Log(w io.Writer, runs []audit.Run, n int, asJSON bool) error`; in main: `func auditPath() (string, error)`.

- [ ] **Step 1: Write failing cli tests**

Append to `internal/cli/cli_test.go` (add imports `"github.com/kejrak/hopper/internal/audit"` and keep the others):

```go
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
```

Run: `env -u GOROOT go test ./internal/cli/` → FAIL (`undefined: ParseLog` …).

- [ ] **Step 2: Implement in `internal/cli/cli.go`**

Add imports `"strconv"`, `"text/tabwriter"`, `"unicode/utf8"`, and `"github.com/kejrak/hopper/internal/audit"`. Append:

```go
// DefaultLogRuns is how many runs `hopper log` shows without -n.
const DefaultLogRuns = 20

// maxLogCommand is the display width, in runes, of a command in the log
// table; longer commands are truncated with "…" (JSON output keeps them).
const maxLogCommand = 80

// ParseLog parses log arguments: [-n N] [--json]. N must be a positive
// integer; anything else is a usage error.
func ParseLog(args []string) (n int, asJSON bool, err error) {
	n = DefaultLogRuns
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--json", "-json":
			asJSON = true
		case "-n":
			if i+1 >= len(args) {
				return 0, false, fmt.Errorf("%w: -n needs a number", ErrUsage)
			}
			i++
			v, convErr := strconv.Atoi(args[i])
			if convErr != nil || v < 1 {
				return 0, false, fmt.Errorf("%w: -n needs a positive number, got %q", ErrUsage, args[i])
			}
			n = v
		default:
			return 0, false, fmt.Errorf("%w: unexpected log argument %q", ErrUsage, arg)
		}
	}
	return n, asJSON, nil
}

// FilterRuns returns the runs whose group is allowed. A nil allowlist
// keeps every run; runs with no group (e.g. refused unknown hosts) are
// hidden whenever an allowlist is set.
func FilterRuns(runs []audit.Run, allowed map[string]bool) []audit.Run {
	if allowed == nil {
		return runs
	}
	out := make([]audit.Run, 0, len(runs))
	for _, r := range runs {
		if allowed[r.Group] {
			out = append(out, r)
		}
	}
	return out
}

// Log writes the last n runs, oldest first: an aligned table for humans
// (nothing when there are no runs), or with asJSON a JSON array ("[]" when
// there are none). The table flattens whitespace in commands and truncates
// long ones so each run stays on one line.
func Log(w io.Writer, runs []audit.Run, n int, asJSON bool) error {
	if len(runs) > n {
		runs = runs[len(runs)-n:]
	}
	if asJSON {
		if runs == nil {
			runs = []audit.Run{}
		}
		return writeJSON(w, runs)
	}
	if len(runs) == 0 {
		return nil
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TIME\tHOST\tSTATUS\tEXIT\tDURATION\tCOMMAND")
	for _, r := range runs {
		exit, duration := "-", "-"
		if r.ExitCode != nil {
			exit = strconv.Itoa(*r.ExitCode)
		}
		if r.DurationMS != nil {
			duration = (time.Duration(*r.DurationMS) * time.Millisecond).String()
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Time.Local().Format("2006-01-02 15:04:05"), r.Host, r.Status, exit, duration, displayCommand(r.Command))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// displayCommand joins argv for the log table, collapsing all whitespace
// (including newlines from heredoc scripts) and truncating to maxLogCommand
// runes.
func displayCommand(command []string) string {
	s := strings.Join(strings.Fields(strings.Join(command, " ")), " ")
	if utf8.RuneCountInString(s) <= maxLogCommand {
		return s
	}
	return string([]rune(s)[:maxLogCommand]) + "…"
}
```

Run: `env -u GOROOT go test ./internal/cli/` → PASS.

- [ ] **Step 3: Write failing main tests**

Append to `main_test.go` (add import `"github.com/kejrak/hopper/internal/audit"`):

```go
func loadAudit(t *testing.T) []audit.Record {
	t.Helper()
	path, err := auditPath()
	if err != nil {
		t.Fatal(err)
	}
	records, err := audit.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func TestExecWritesAuditStartAndEnd(t *testing.T) {
	setupHome(t, testConfig)
	fakeSSH(t, 7)
	if code, _, errOut := runArgs("exec", "web", "--", "uptime", "-p"); code != 7 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	records := loadAudit(t)
	if len(records) != 2 {
		t.Fatalf("got %d records, want start+end: %+v", len(records), records)
	}
	start, end := records[0], records[1]
	if start.Event != audit.EventStart || start.Host != "web" || start.Group != "default" ||
		!slices.Equal(start.Command, []string{"uptime", "-p"}) || start.Dir == "" || start.ID == "" {
		t.Errorf("start record %+v", start)
	}
	if end.Event != audit.EventEnd || end.ID != start.ID || end.ExitCode == nil || *end.ExitCode != 7 || end.DurationMS == nil {
		t.Errorf("end record %+v", end)
	}
}

func TestExecRefusedIsAudited(t *testing.T) {
	setupHome(t, testConfig)
	fakeSSH(t, 0)
	if code, _, _ := runArgs("exec", "nope", "--", "true"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	records := loadAudit(t)
	if len(records) != 1 || records[0].Event != audit.EventRefused || records[0].Host != "nope" ||
		!strings.Contains(records[0].Reason, "unknown host") || !slices.Equal(records[0].Command, []string{"true"}) {
		t.Fatalf("got %+v, want one refused record", records)
	}
}

func TestExecUsageErrorIsNotAudited(t *testing.T) {
	setupHome(t, testConfig)
	if code, _, _ := runArgs("exec", "web"); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if records := loadAudit(t); len(records) != 0 {
		t.Fatalf("usage errors must not be audited: %+v", records)
	}
}

func TestExecAuditFailureOnlyWarns(t *testing.T) {
	setupHome(t, testConfig)
	argsFile := fakeSSH(t, 5)
	path, err := auditPath()
	if err != nil {
		t.Fatal(err)
	}
	// Make exec.log itself a directory so every append fails.
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runArgs("exec", "web", "--", "true")
	if code != 5 {
		t.Fatalf("exit %d, want ssh's 5; stderr %q", code, errOut)
	}
	if _, err := os.Stat(argsFile); err != nil {
		t.Fatal("ssh must still run when the audit log is unwritable")
	}
	if !strings.Contains(errOut, "hopper: warning: audit log:") {
		t.Fatalf("stderr %q, want audit warning", errOut)
	}
}

func TestLogJSONAndLimit(t *testing.T) {
	setupHome(t, testConfig)
	fakeSSH(t, 0)
	runArgs("exec", "web", "--", "first")
	runArgs("exec", "db", "--", "second")
	code, out, errOut := runArgs("log", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var runs []map[string]any
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		t.Fatalf("stdout is not JSON: %q: %v", out, err)
	}
	if len(runs) != 2 || runs[0]["host"] != "web" || runs[1]["host"] != "db" || runs[1]["status"] != "ok" {
		t.Fatalf("got %v", runs)
	}
	_, out, _ = runArgs("log", "-n", "1", "--json")
	if err := json.Unmarshal([]byte(out), &runs); err != nil || len(runs) != 1 || runs[0]["host"] != "db" {
		t.Fatalf("-n 1: got %v (%v)", runs, err)
	}
}

func TestLogTextAndEmpty(t *testing.T) {
	setupHome(t, testConfig)
	if code, out, _ := runArgs("log"); code != 0 || out != "" {
		t.Fatalf("empty log: exit %d, stdout %q", code, out)
	}
	if code, out, _ := runArgs("log", "--json"); code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty log json: exit %d, stdout %q", code, out)
	}
	fakeSSH(t, 0)
	runArgs("exec", "web", "--", "uptime")
	code, out, _ := runArgs("log")
	if code != 0 || !strings.Contains(out, "HOST") || !strings.Contains(out, "web") || !strings.Contains(out, "uptime") {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
}

func TestLogRespectsAllowGroups(t *testing.T) {
	setupGroupedHome(t)
	fakeSSH(t, 0)
	runArgs("exec", "web", "--", "a")
	runArgs("exec", "db", "--", "b")
	t.Setenv("HOPPER_ALLOW_GROUPS", "work")
	_, out, _ := runArgs("log", "--json")
	var runs []map[string]any
	if err := json.Unmarshal([]byte(out), &runs); err != nil || len(runs) != 1 || runs[0]["host"] != "db" {
		t.Fatalf("got %v (%v), want only db", runs, err)
	}
}

func TestLogUsage(t *testing.T) {
	setupHome(t, testConfig)
	for _, args := range [][]string{{"log", "-n", "0"}, {"log", "extra"}} {
		if code, _, _ := runArgs(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	for _, args := range [][]string{{"log", "--help"}, {"log", "-n", "5", "-h"}} {
		code, out, errOut := runArgs(args...)
		if code != 0 || !strings.Contains(out, "hopper log") || errOut != "" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}
```

Also extend `TestSubcommandHelp`'s expectations only if needed — no change required (it checks for "hopper exec", which stays in usage).

Run: `env -u GOROOT go test .` → FAIL (`undefined: auditPath`, unknown command "log").

- [ ] **Step 4: Implement in `main.go`**

Add import `"github.com/kejrak/hopper/internal/audit"`.

Replace the `usage` constant with:

```go
const usage = `hopper - pick an SSH host from ~/.ssh/config and connect to it.

Usage:
  hopper                          interactive picker (needs a terminal)
  hopper list [--json]            list all hosts
  hopper show <host> [--json]     show one host's details
  hopper exec <host> [--] <cmd>   run a command on a host without prompts
                                  (ssh -o BatchMode=yes -T); exits with the
                                  remote command's code, 255 on ssh errors
  hopper log [-n N] [--json]      show the last N (default 20) exec runs
  hopper help                     show this help

Environment:
  HOPPER_ALLOW_GROUPS             comma-separated groups (ssh config file
                                  names; "default" is ~/.ssh/config) that
                                  list, show, exec and log may use; unset = all

Every exec is recorded in exec.log next to hopper's history file.
`
```

In `run`, add a case before `"help"`:

```go
	case "log":
		return runLog(args[1:], stdout, stderr)
```

Add after `recordHistory`:

```go
// auditPath returns the exec audit log location: exec.log beside the
// history file.
func auditPath() (string, error) {
	historyPath, err := history.Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(historyPath), "exec.log"), nil
}

// appendAudit writes one audit record; failures are only warnings so an
// unwritable log never blocks a command.
func appendAudit(r audit.Record, stderr io.Writer) {
	path, err := auditPath()
	if err == nil {
		err = audit.Append(path, r)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper: warning: audit log:", err)
	}
}
```

Replace `runExec` entirely with:

```go
func runExec(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	}
	name, command, err := cli.ParseExec(args)
	if err != nil {
		return usageError(stderr, err)
	}
	hosts, ok := loadForCLI(stderr)
	if !ok {
		return 1
	}
	dir, _ := os.Getwd() // best effort: an empty dir is still a useful record
	h, err := cli.Resolve(hosts, name, allowedGroups())
	if err != nil {
		appendAudit(audit.Record{Time: time.Now(), Event: audit.EventRefused, ID: audit.NewID(),
			Host: name, Command: command, Dir: dir, Reason: err.Error()}, stderr)
		_, _ = fmt.Fprintln(stderr, "hopper:", resolveErrorMessage(err))
		return 1
	}

	id := audit.NewID()
	start := time.Now()
	appendAudit(audit.Record{Time: start, Event: audit.EventStart, ID: id,
		Host: h.Name, Group: h.Group, Command: command, Dir: dir}, stderr)
	recordHistory(h.Name, stderr)
	err = host.ExecCommand(h.Name, command).Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		_, _ = fmt.Fprintln(stderr, "hopper:", err) // e.g. ssh binary missing
	}
	code := host.ExitCode(err)
	durationMS := time.Since(start).Milliseconds()
	appendAudit(audit.Record{Time: time.Now(), Event: audit.EventEnd, ID: id,
		Host: h.Name, ExitCode: &code, DurationMS: &durationMS}, stderr)
	return code
}
```

Add after `runExec`:

```go
func runLog(args []string, stdout, stderr io.Writer) int {
	if wantsHelp(args) {
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	}
	n, asJSON, err := cli.ParseLog(args)
	if err != nil {
		return usageError(stderr, err)
	}
	path, err := auditPath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper:", err)
		return 1
	}
	records, err := audit.Load(path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper: reading audit log:", err)
		return 1
	}
	runs := cli.FilterRuns(audit.Runs(records), allowedGroups())
	if err := cli.Log(stdout, runs, n, asJSON); err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper:", err)
		return 1
	}
	return 0
}
```

- [ ] **Step 5: Verify**

Run: `env -u GOROOT go test ./... && env -u GOROOT go vet ./... && gofmt -l . && env -u GOROOT golangci-lint run`
Expected: all PASS, vet clean, gofmt empty, `0 issues.`

Smoke test without a TTY (uses the real ~/.ssh/config; do NOT exec against real hosts):

```bash
env -u GOROOT go build -o bin/hopper .
HOPPER_ALLOW_GROUPS=bizznote ./bin/hopper list < /dev/null
./bin/hopper exec __nope__ -- true < /dev/null; echo "exit=$?"
./bin/hopper log -n 3 < /dev/null
```
Expected: only bizznote hosts; `unknown host` + `exit=1`; a table whose last row is the refused `__nope__` run.

- [ ] **Step 6: README**

In `README.md`, insert immediately before the line `## Configuration` (i.e. after the group-allowlist subsection added in Task 1):

````markdown
### Exec audit log

Every `hopper exec` is appended to `exec.log` (JSON Lines, file mode 0600) next to hopper's history file:

- a `start` record — time, host, group, command, working directory — written *before* ssh runs, so a run killed by an agent's timeout still shows up,
- an `end` record with the exit code and duration,
- a `refused` record for hosts that are unknown or outside `HOPPER_ALLOW_GROUPS`.

Review it with:

```sh
hopper log            # last 20 runs as a table
hopper log -n 100     # more
hopper log --json     # machine-readable
```

A run shown as `unfinished` never wrote its end record (still running, or hopper was killed). `hopper log` honours `HOPPER_ALLOW_GROUPS` too. The log is never rotated — delete it any time. Failing to write it is only a warning; the command still runs.

````

- [ ] **Step 7: Commit**

```bash
git add internal/cli main.go main_test.go README.md
git commit -m "feat: audit exec runs and add hopper log"
```
