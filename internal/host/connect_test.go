package host

import (
	"errors"
	"os/exec"
	"slices"
	"testing"
)

func TestCommandPassesOnlyTheAlias(t *testing.T) {
	cmd := Command("web-prod")
	if len(cmd.Args) != 3 || cmd.Args[0] != "ssh" || cmd.Args[1] != "--" || cmd.Args[2] != "web-prod" {
		t.Fatalf("got args %v, want [ssh -- web-prod]", cmd.Args)
	}
	if cmd.Stdin == nil || cmd.Stdout == nil || cmd.Stderr == nil {
		t.Fatal("stdio must be inherited")
	}
}

func TestCommandHardensAgainstDashPrefixedName(t *testing.T) {
	cmd := Command("-oProxyCommand=evil")
	if len(cmd.Args) != 3 || cmd.Args[1] != "--" {
		t.Fatalf("got args %v, want the -- marker before the name", cmd.Args)
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

func TestExecCommandArgs(t *testing.T) {
	cmd := ExecCommand("web-prod", []string{"uptime"})
	want := []string{"ssh", "-o", "BatchMode=yes", "-T", "--", "web-prod", "uptime"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("got args %v, want %v", cmd.Args, want)
	}
	if cmd.Stdin == nil || cmd.Stdout == nil || cmd.Stderr == nil {
		t.Fatal("stdio must be inherited")
	}
}

func TestExecCommandKeepsRemoteArgsAfterAlias(t *testing.T) {
	cmd := ExecCommand("-oProxyCommand=evil", []string{"ls", "-la"})
	want := []string{"ssh", "-o", "BatchMode=yes", "-T", "--", "-oProxyCommand=evil", "ls", "-la"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("got args %v, want %v", cmd.Args, want)
	}
}
