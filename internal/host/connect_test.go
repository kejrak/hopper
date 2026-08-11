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
