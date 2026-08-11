package sshcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kevinburke/ssh_config"

	"github.com/kejrak/hopper/internal/host"
)

// Hosts parses root and every included file and returns each concrete host
// exactly once, in definition order (first definition wins, the OpenSSH
// rule). Wildcard patterns (*, ?, !) are skipped. Attribute values come
// from the merged configuration and are informational only; wildcard-derived
// defaults (e.g., from "Host *") are intentionally excluded from displayed
// attributes to prevent silent inheritance. warnings lists included files
// that were skipped; the returned error is non-nil only when the root config
// itself is unreadable.
func Hosts(root, sshDir string) ([]host.Host, []string, error) {
	// Verify root is readable by attempting to open it.
	f, err := os.Open(root)
	if err != nil {
		return nil, nil, fmt.Errorf("reading ssh config: %w", err)
	}
	_ = f.Close()
	files, warnings := ConfigFiles(root, sshDir)

	type sourcedConfig struct {
		cfg  *ssh_config.Config
		path string
	}
	var configs []sourcedConfig
	merged := &ssh_config.Config{}
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipping %s: %v", path, err))
			continue
		}
		cfg, err := ssh_config.Decode(f)
		_ = f.Close()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipping %s: %v", path, err))
			continue
		}
		configs = append(configs, sourcedConfig{cfg: cfg, path: path})
		// Add blocks to merged config, but strip wildcard patterns to prevent
		// wildcard-derived attributes from leaking to unrelated concrete hosts.
		for _, block := range cfg.Hosts {
			var concretePatterns []*ssh_config.Pattern
			for _, pattern := range block.Patterns {
				if !isWildcardPattern(pattern.String()) {
					concretePatterns = append(concretePatterns, pattern)
				}
			}
			if len(concretePatterns) > 0 {
				// Create a copy of the block with only concrete patterns.
				concreteBlock := *block
				concreteBlock.Patterns = concretePatterns
				merged.Hosts = append(merged.Hosts, &concreteBlock)
			}
		}
	}

	var hosts []host.Host
	seen := make(map[string]bool)
	for _, sc := range configs {
		for _, block := range sc.cfg.Hosts {
			for _, pattern := range block.Patterns {
				name := pattern.String()
				if isWildcardPattern(name) || seen[name] {
					continue
				}
				seen[name] = true
				h := host.Host{
					Name:         name,
					User:         get(merged, name, "User"),
					Hostname:     get(merged, name, "Hostname"),
					Port:         get(merged, name, "Port"),
					IdentityFile: get(merged, name, "IdentityFile"),
					Source:       sc.path,
					Group:        group(sc.path, root),
				}
				if h.Hostname == "" {
					h.Hostname = name
				}
				if h.Port == "" {
					h.Port = "22"
				}
				hosts = append(hosts, h)
			}
		}
	}
	return hosts, warnings, nil
}

// get resolves one key from the merged config, treating lookup errors as
// "not set" — the value is display-only.
func get(cfg *ssh_config.Config, alias, key string) string {
	value, err := cfg.Get(alias, key)
	if err != nil {
		return ""
	}
	return value
}

// group derives the section name from the file a host is defined in: the
// root config maps to "default", any other file to its basename without
// extension ("conf.d/work.conf" → "work").
func group(path, root string) string {
	if path == root {
		return "default"
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// isWildcardPattern returns true if the pattern string contains wildcard
// characters (*, ?, or !), which indicates it is not a concrete host name.
func isWildcardPattern(pattern string) bool {
	return strings.ContainsAny(pattern, "*?!")
}
