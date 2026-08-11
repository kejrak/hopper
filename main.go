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
		hosts, warnings, err := sshcfg.Hosts(root, sshDir)
		if err != nil {
			return nil, warnings, err
		}
		if path, pathErr := history.Path(); pathErr == nil {
			last := history.LastConnected(history.Load(path))
			for i := range hosts {
				hosts[i].LastConnected = last[hosts[i].Name]
			}
		}
		return hosts, warnings, nil
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
