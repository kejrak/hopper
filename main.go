// Command hopper is a TUI for picking an SSH host from ~/.ssh/config
// (including Include files) and connecting to it. Connection is delegated
// entirely to ssh: hopper runs `ssh <name>` and mirrors its exit code.
// The list, show and exec subcommands expose the same hosts to scripts
// and AI agents that have no terminal to drive the TUI.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/kejrak/hopper/internal/audit"
	"github.com/kejrak/hopper/internal/cli"
	"github.com/kejrak/hopper/internal/history"
	"github.com/kejrak/hopper/internal/host"
	"github.com/kejrak/hopper/internal/sshcfg"
	"github.com/kejrak/hopper/internal/ui"
)

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

// isTerminal reports whether stdin and stdout are both terminals, which
// the TUI needs. It is a variable so tests can override it.
var isTerminal = func() bool {
	return isTTY(os.Stdin.Fd()) && isTTY(os.Stdout.Fd())
}

func isTTY(fd uintptr) bool {
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

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

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runTUI(stderr)
	}
	switch args[0] {
	case "list":
		return runList(args[1:], stdout, stderr)
	case "show":
		return runShow(args[1:], stdout, stderr)
	case "exec":
		return runExec(args[1:], stdout, stderr)
	case "log":
		return runLog(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	default:
		return usageError(stderr, fmt.Errorf("%w: unknown command %q", cli.ErrUsage, args[0]))
	}
}

// wantsHelp reports whether -h or --help appears anywhere in args.
func wantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func usageError(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "hopper: %v\n\n%s", err, usage)
	return 2
}

// sshPaths returns the ssh directory and its root config file.
func sshPaths() (sshDir, root string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	sshDir = filepath.Join(home, ".ssh")
	return sshDir, filepath.Join(sshDir, "config"), nil
}

// loadHosts parses the ssh config and annotates each host with its last
// connection time from history.
func loadHosts(root, sshDir string) ([]host.Host, []string, error) {
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

// loadForCLI loads hosts for a subcommand, reporting warnings and errors
// on stderr. ok is false after a fatal error has been reported.
func loadForCLI(stderr io.Writer) (hosts []host.Host, ok bool) {
	sshDir, root, err := sshPaths()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper:", err)
		return nil, false
	}
	hosts, warnings, err := loadHosts(root, sshDir)
	for _, warning := range warnings {
		_, _ = fmt.Fprintln(stderr, "hopper: warning:", warning)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper:", err)
		return nil, false
	}
	return hosts, true
}

// recordHistory notes a connection to name; failures are only warnings.
func recordHistory(name string, stderr io.Writer) {
	path, err := history.Path()
	if err != nil {
		return
	}
	if err := history.Record(path, name, time.Now()); err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper: warning: recording history:", err)
	}
}

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

func runList(args []string, stdout, stderr io.Writer) int {
	if wantsHelp(args) {
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	}
	positional, asJSON, err := cli.ParseFlags(args)
	if err == nil && len(positional) > 0 {
		err = fmt.Errorf("%w: list takes no arguments", cli.ErrUsage)
	}
	if err != nil {
		return usageError(stderr, err)
	}
	hosts, ok := loadForCLI(stderr)
	if !ok {
		return 1
	}
	if err := cli.List(stdout, cli.FilterGroups(hosts, allowedGroups()), asJSON); err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper:", err)
		return 1
	}
	return 0
}

func runShow(args []string, stdout, stderr io.Writer) int {
	if wantsHelp(args) {
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	}
	positional, asJSON, err := cli.ParseFlags(args)
	if err == nil && len(positional) != 1 {
		err = fmt.Errorf("%w: show takes exactly one host name", cli.ErrUsage)
	}
	if err != nil {
		return usageError(stderr, err)
	}
	hosts, ok := loadForCLI(stderr)
	if !ok {
		return 1
	}
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
}

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
		var group string
		if errors.Is(err, cli.ErrGroupNotAllowed) {
			if refused, findErr := cli.Find(hosts, name); findErr == nil {
				group = refused.Group
			}
		}
		appendAudit(audit.Record{Time: time.Now(), Event: audit.EventRefused, ID: audit.NewID(),
			Host: name, Group: group, Command: command, Dir: dir, Reason: err.Error()}, stderr)
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

func runTUI(stderr io.Writer) int {
	if !isTerminal() {
		_, _ = fmt.Fprint(stderr, "hopper: interactive mode needs a terminal; for scripts and AI agents use\n"+
			"  hopper list --json | hopper show <host> | hopper exec <host> -- <cmd>\n"+
			"(see 'hopper help')\n")
		return 1
	}
	sshDir, root, err := sshPaths()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper:", err)
		return 1
	}
	reload := func() ([]host.Host, []string, error) { return loadHosts(root, sshDir) }
	hosts, warnings, err := reload()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper:", err)
		return 1
	}
	for _, warning := range warnings {
		_, _ = fmt.Fprintln(stderr, "hopper: warning:", warning)
	}
	if len(hosts) == 0 {
		_, _ = fmt.Fprintln(stderr, "hopper: no hosts found in", root)
		return 1
	}

	var entries []history.Entry
	if histPath, histErr := history.Path(); histErr == nil {
		entries = history.Load(histPath)
	}

	result, err := ui.Run(hosts, history.Recent(entries, 5), reload)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hopper:", err)
		return 1
	}
	if result.Action != ui.ActionConnect {
		return 0
	}
	recordHistory(result.Host.Name, stderr)
	return host.ExitCode(host.Command(result.Host.Name).Run())
}
