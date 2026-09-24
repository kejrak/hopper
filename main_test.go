package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/kejrak/hopper/internal/history"
)

const testConfig = `Host web
  HostName 10.0.0.1
  User deploy
  Port 2222

Host db
  HostName 10.0.0.2
`

// setupHome points HOME (and the history location) at a temp dir holding
// an ssh config with the given contents.
func setupHome(t *testing.T, config string) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
}

// fakeSSH puts an ssh stub first on PATH that writes its argv, one per
// line, to the returned file and exits with code.
func fakeSSH(t *testing.T, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub needs a POSIX shell")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > '%s'\nexit %d\n", argsFile, code)
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

func runArgs(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestListJSON(t *testing.T) {
	setupHome(t, testConfig)
	code, out, errOut := runArgs("list", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var hosts []map[string]any
	if err := json.Unmarshal([]byte(out), &hosts); err != nil {
		t.Fatalf("stdout is not JSON: %q: %v", out, err)
	}
	if len(hosts) != 2 || hosts[0]["name"] != "web" || hosts[1]["name"] != "db" {
		t.Fatalf("unexpected hosts %v", hosts)
	}
}

func TestListText(t *testing.T) {
	setupHome(t, testConfig)
	code, out, _ := runArgs("list")
	if code != 0 || !strings.HasPrefix(out, "web\tdeploy@10.0.0.1:2222\t") {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
}

func TestListRejectsArguments(t *testing.T) {
	setupHome(t, testConfig)
	if code, _, _ := runArgs("list", "web"); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestShowJSONFlagAfterHost(t *testing.T) {
	setupHome(t, testConfig)
	code, out, errOut := runArgs("show", "web", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var h map[string]any
	if err := json.Unmarshal([]byte(out), &h); err != nil || h["hostname"] != "10.0.0.1" {
		t.Fatalf("stdout %q, err %v", out, err)
	}
}

func TestShowUnknownHostExits1(t *testing.T) {
	setupHome(t, testConfig)
	code, out, errOut := runArgs("show", "nope")
	if code != 1 || out != "" || !strings.Contains(errOut, "unknown host") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestShowNeedsExactlyOneHost(t *testing.T) {
	setupHome(t, testConfig)
	if code, _, _ := runArgs("show"); code != 2 {
		t.Fatalf("no host: exit %d, want 2", code)
	}
	if code, _, _ := runArgs("show", "web", "db"); code != 2 {
		t.Fatalf("two hosts: exit %d, want 2", code)
	}
}

func TestExecPassesArgsAndExitCode(t *testing.T) {
	setupHome(t, testConfig)
	argsFile := fakeSSH(t, 7)
	code, _, errOut := runArgs("exec", "web", "--", "uptime", "-p")
	if code != 7 {
		t.Fatalf("exit %d, want ssh's 7; stderr %q", code, errOut)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ssh stub was not run: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	want := []string{"-o", "BatchMode=yes", "-T", "--", "web", "uptime", "-p"}
	if !slices.Equal(got, want) {
		t.Fatalf("ssh argv %v, want %v", got, want)
	}
	histPath, err := history.Path()
	if err != nil {
		t.Fatal(err)
	}
	if entries := history.Load(histPath); len(entries) == 0 || entries[0].Host != "web" {
		t.Fatalf("history not recorded: %v", entries)
	}
}

func TestExecUnknownHostDoesNotRunSSH(t *testing.T) {
	setupHome(t, testConfig)
	argsFile := fakeSSH(t, 0)
	code, _, errOut := runArgs("exec", "nope", "--", "true")
	if code != 1 || !strings.Contains(errOut, "unknown host") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Fatal("ssh must not run for a host missing from the config")
	}
}

func TestExecWithoutCommandIsUsageError(t *testing.T) {
	setupHome(t, testConfig)
	if code, _, _ := runArgs("exec", "web"); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	setupHome(t, testConfig)
	code, _, errOut := runArgs("frobnicate")
	if code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, out, _ := runArgs(arg)
		if code != 0 || !strings.Contains(out, "hopper exec") {
			t.Fatalf("%s: exit %d, stdout %q", arg, code, out)
		}
	}
}

func TestNoArgsWithoutTerminalExplains(t *testing.T) {
	setupHome(t, testConfig)
	orig := isTerminal
	isTerminal = func() bool { return false }
	t.Cleanup(func() { isTerminal = orig })
	code, _, errOut := runArgs()
	if code != 1 || !strings.Contains(errOut, "hopper list") || !strings.Contains(errOut, "hopper exec") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestMissingConfigIsRuntimeError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	code, _, errOut := runArgs("list")
	if code != 1 || !strings.Contains(errOut, "hopper:") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}
