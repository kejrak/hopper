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
