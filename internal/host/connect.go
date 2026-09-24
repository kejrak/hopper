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

// ExecCommand builds a non-interactive remote command for scripts and AI
// agents. BatchMode makes ssh fail fast instead of prompting for a password
// or passphrase nobody is there to type, and -T skips PTY allocation. As
// with Command, only the alias is passed and "--" guards it; ssh joins args
// into one string that the remote shell interprets, exactly like plain ssh.
func ExecCommand(name string, args []string) *exec.Cmd {
	sshArgs := append([]string{"-o", "BatchMode=yes", "-T", "--", name}, args...)
	cmd := exec.Command("ssh", sshArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// ExitCode maps a Run error to the code hopper should exit with: 0 on
// success, ssh's own exit code when it exited non-zero, 255 when ssh was
// killed by a signal, 1 for anything else (e.g. the ssh binary is missing).
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
		return 255
	}
	return 1
}
