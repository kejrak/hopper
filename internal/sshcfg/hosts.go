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
// from the merged configuration and are informational only. warnings lists
// included files that were skipped; the returned error is non-nil only when
// the root config itself is unreadable.
func Hosts(root, sshDir string) ([]host.Host, []string, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, nil, fmt.Errorf("reading ssh config: %w", err)
	}
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
		f.Close()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipping %s: %v", path, err))
			continue
		}
		configs = append(configs, sourcedConfig{cfg: cfg, path: path})
		// Only add blocks with concrete patterns to merged config
		for _, block := range cfg.Hosts {
			hasConcretePattern := false
			for _, pattern := range block.Patterns {
				if !strings.ContainsAny(pattern.String(), "*?!") {
					hasConcretePattern = true
					break
				}
			}
			if hasConcretePattern {
				merged.Hosts = append(merged.Hosts, block)
			}
		}
	}

	var hosts []host.Host
	seen := make(map[string]bool)
	for _, sc := range configs {
		for _, block := range sc.cfg.Hosts {
			for _, pattern := range block.Patterns {
				name := pattern.String()
				if strings.ContainsAny(name, "*?!") || seen[name] {
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
