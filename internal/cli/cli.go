// Package cli implements hopper's non-interactive subcommands for scripts
// and AI agents that have no terminal to drive the TUI: host listing and
// details (text or JSON) and argument parsing for list/show/exec.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/kejrak/hopper/internal/audit"
	"github.com/kejrak/hopper/internal/host"
)

// ErrUnknownHost is returned when a name matches no host in the ssh config.
var ErrUnknownHost = errors.New("unknown host")

// ErrUsage marks a malformed command line; callers exit with code 2.
var ErrUsage = errors.New("usage")

// ErrGroupNotAllowed is returned when a host exists but its group is not
// in the allowlist.
var ErrGroupNotAllowed = errors.New("group not allowed")

// hostJSON is the stable machine-readable shape of a host.
type hostJSON struct {
	Name          string     `json:"name"`
	User          string     `json:"user"`
	Hostname      string     `json:"hostname"`
	Port          string     `json:"port"`
	IdentityFile  string     `json:"identity_file"`
	Source        string     `json:"source"`
	Group         string     `json:"group"`
	LastConnected *time.Time `json:"last_connected"` // null if never connected
}

func toJSON(h host.Host) hostJSON {
	out := hostJSON{
		Name:         h.Name,
		User:         h.User,
		Hostname:     h.Hostname,
		Port:         h.Port,
		IdentityFile: h.IdentityFile,
		Source:       h.Source,
		Group:        h.Group,
	}
	if !h.LastConnected.IsZero() {
		last := h.LastConnected
		out.LastConnected = &last
	}
	return out
}

// Find returns the host whose name matches exactly.
func Find(hosts []host.Host, name string) (host.Host, error) {
	for _, h := range hosts {
		if h.Name == name {
			return h, nil
		}
	}
	return host.Host{}, fmt.Errorf("%w %q", ErrUnknownHost, name)
}

// List writes every host in config order: one "name<TAB>target<TAB>group"
// line each, or with asJSON a JSON array ("[]" when there are none).
func List(w io.Writer, hosts []host.Host, asJSON bool) error {
	if asJSON {
		out := make([]hostJSON, 0, len(hosts))
		for _, h := range hosts {
			out = append(out, toJSON(h))
		}
		return writeJSON(w, out)
	}
	var b strings.Builder
	for _, h := range hosts {
		_, _ = fmt.Fprintf(&b, "%s\t%s\t%s\n", h.Name, h.Target(), h.Group)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

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

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ParseFlags splits list/show arguments into positional arguments and the
// --json flag (also -json), which may appear anywhere. Any other
// dash-prefixed argument is a usage error.
func ParseFlags(args []string) (positional []string, asJSON bool, err error) {
	for _, arg := range args {
		switch {
		case arg == "--json" || arg == "-json":
			asJSON = true
		case strings.HasPrefix(arg, "-"):
			return nil, false, fmt.Errorf("%w: unknown flag %q", ErrUsage, arg)
		default:
			positional = append(positional, arg)
		}
	}
	return positional, asJSON, nil
}

// ParseExec parses exec arguments: <host> [--] <command...>. Only the first
// "--" after the host is consumed; everything after it belongs to the remote
// command. A command is required: exec never opens an interactive shell.
func ParseExec(args []string) (name string, command []string, err error) {
	if len(args) == 0 {
		return "", nil, fmt.Errorf("%w: exec needs a host and a command", ErrUsage)
	}
	name, command = args[0], args[1:]
	if strings.HasPrefix(name, "-") {
		return "", nil, fmt.Errorf("%w: exec needs a host before the command, got %q", ErrUsage, name)
	}
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if len(command) == 0 {
		return "", nil, fmt.Errorf("%w: exec needs a command to run on %q", ErrUsage, name)
	}
	return name, command, nil
}

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
