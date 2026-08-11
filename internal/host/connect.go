package host

import (
	"errors"
	"os"
	"os/exec"
)

// Command builds the connection command. Only the host alias is passed:
// OpenSSH resolves User/Port/IdentityFile/ProxyJump itself from its own
// configuration, so hopper can never contradict it. The "--" end-of-options
// marker guards against a config-derived alias that starts with "-" being
// parsed as an ssh option.
func Command(name string) *exec.Cmd {
	cmd := exec.Command("ssh", "--", name)
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
