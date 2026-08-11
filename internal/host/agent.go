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
	fpCache := make(map[string]string) // memoize fingerprints by IdentityFile
	for _, h := range hosts {
		switch {
		case h.IdentityFile == "":
			statuses[h.Name] = KeyStatusUnknown
		case !agentOK:
			statuses[h.Name] = KeyStatusNoAgent
		default:
			// Get or compute fingerprint
			if _, cached := fpCache[h.IdentityFile]; !cached {
				fpCache[h.IdentityFile] = fingerprint(h.IdentityFile)
			}
			if keyListed(list, fpCache[h.IdentityFile], ExpandPath(h.IdentityFile)) {
				statuses[h.Name] = KeyStatusLoaded
			} else {
				statuses[h.Name] = KeyStatusNotLoaded
			}
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
		if fp == "" && path != "" {
			// Match path as a whole whitespace-delimited token
			for _, field := range strings.Fields(line) {
				if field == path {
					return true
				}
			}
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
