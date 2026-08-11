// Package host models a concrete ssh host and hopper's handoffs to the
// OpenSSH tools (ssh, ssh-add).
package host

import (
	"fmt"
	"time"
)

// Host is one concrete (non-wildcard) entry from the ssh config files.
// All attribute fields are informational — connection uses only Name.
type Host struct {
	Name          string
	User          string
	Hostname      string
	Port          string
	IdentityFile  string
	Source        string    // config file the host is defined in
	Group         string    // derived from Source
	LastConnected time.Time // zero if never connected
}

// Display returns the one-line picker label, e.g. "web-prod (deploy@10.0.1.20:22)".
// The user@ part is omitted when no User is configured.
func (h Host) Display() string {
	target := h.Hostname
	if h.User != "" {
		target = h.User + "@" + h.Hostname
	}
	return fmt.Sprintf("%s (%s:%s)", h.Name, target, h.Port)
}
