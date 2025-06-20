package hosts

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kevinburke/ssh_config"
)

type SSHHost struct {
	Name         string
	User         string
	Hostname     string
	Port         string
	IdentityFile string
}

func (h *SSHHost) Connect() {

	fmt.Printf("Connecting to %s@%s:%s using %s...\n", h.User, h.Hostname, h.Port, h.IdentityFile)

	cmd := exec.Command("ssh", fmt.Sprintf("%s@%s", h.User, h.Hostname), "-p", h.Port, "-i", h.IdentityFile)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		fmt.Println("Failed to connect:", err)
	}
}

func GetSSHHosts(cfg *ssh_config.Config) []SSHHost {
	var hosts []SSHHost
	seen := make(map[string]struct{})

	for _, host := range cfg.Hosts {
		for _, pat := range host.Patterns {
			name := pat.String()
			if name == "*" || strings.ContainsAny(name, "*?") {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}

			user := ssh_config.Get(name, "User")
			hostname := ssh_config.Get(name, "Hostname")
			port := ssh_config.Get(name, "Port")
			identityFile := ssh_config.Get(name, "IdentityFile")

			if hostname == "" {
				hostname = name
			}
			if port == "" {
				port = "22"
			}
			if identityFile == "" {
				identityFile = "~/.ssh/id_rsa"
			}

			hosts = append(hosts, SSHHost{
				Name:         name,
				User:         user,
				Hostname:     hostname,
				Port:         port,
				IdentityFile: identityFile,
			})
		}
	}

	return hosts
}
