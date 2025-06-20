package config

import (
	"bufio"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/kevinburke/ssh_config"
)

func configPaths() ([]string, error) {
	usr, err := user.Current()
	if err != nil {
		return nil, err
	}
	mainConfig := filepath.Join(usr.HomeDir, ".ssh", "config")
	paths := []string{mainConfig}
	visited := make(map[string]struct{})
	stack := []string{mainConfig}

	for len(stack) > 0 {
		curr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := visited[curr]; seen {
			continue
		}
		visited[curr] = struct{}{}

		file, err := os.Open(curr)
		if err != nil {
			continue
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "Include ") {
				parts := strings.Fields(line)[1:]
				for _, inc := range parts {
					glob := filepath.Join(filepath.Dir(curr), inc)
					matches, _ := filepath.Glob(glob)
					for _, match := range matches {
						stack = append(stack, match)
						paths = append(paths, match)
					}
				}
			}
		}
	}

	return paths, nil
}

func ParseAllConfigs() (*ssh_config.Config, error) {
	paths, err := configPaths()
	if err != nil {
		return nil, err
	}
	var merged ssh_config.Config
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer f.Close()
		cfg, err := ssh_config.Decode(f)
		if err != nil {
			continue
		}
		merged.Hosts = append(merged.Hosts, cfg.Hosts...)
	}
	return &merged, nil
}
