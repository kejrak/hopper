// Package sshcfg discovers ssh config files and enumerates the hosts they
// define. It is read-only over the config files: attribute values are
// informational (for display) — connection is delegated to ssh itself.
package sshcfg

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ConfigFiles returns root plus every file reachable via Include directives,
// in the order OpenSSH reads them. sshDir is the base for relative include
// paths (normally ~/.ssh — per OpenSSH, relative includes resolve against
// ~/.ssh, not the including file's directory). Include patterns that match
// nothing are skipped silently (OpenSSH behavior); include cycles are broken
// with a visited set. warnings is reserved for downstream callers and is
// always empty here.
func ConfigFiles(root, sshDir string) (files []string, warnings []string) {
	visited := make(map[string]bool)
	var walk func(path string)
	walk = func(path string) {
		if visited[path] {
			return
		}
		visited[path] = true
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		files = append(files, path)
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			fields := strings.Fields(strings.TrimSpace(scanner.Text()))
			if len(fields) < 2 || !strings.EqualFold(fields[0], "Include") {
				continue
			}
			for _, pat := range fields[1:] {
				for _, match := range expandInclude(pat, sshDir) {
					walk(match)
				}
			}
		}
	}
	walk(root)
	return files, nil
}

// expandInclude resolves one Include token to concrete file paths: strips
// quotes, expands a leading ~/, anchors relative paths at sshDir, and globs.
func expandInclude(pat, sshDir string) []string {
	pat = strings.Trim(pat, `"`)
	if strings.HasPrefix(pat, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		pat = filepath.Join(home, pat[2:])
	}
	if !filepath.IsAbs(pat) {
		pat = filepath.Join(sshDir, pat)
	}
	matches, _ := filepath.Glob(pat)
	return matches
}
