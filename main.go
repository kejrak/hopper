package main

import (
	"fmt"
	"os"

	"github.com/kejrak/hopper/config"
	"github.com/kejrak/hopper/hosts"

	"github.com/ktr0731/go-fuzzyfinder"
)

func main() {
	cfg, err := config.ParseAllConfigs()
	if err != nil {
		fmt.Println("Could not parse SSH config:", err)
		os.Exit(1)
	}

	hosts := hosts.GetSSHHosts(cfg)
	if len(hosts) == 0 {
		fmt.Println("No valid hosts found.")
		return
	}

	idx, err := fuzzyfinder.Find(hosts, func(i int) string {
		h := hosts[i]
		return fmt.Sprintf("%s (%s@%s:%s)", h.Name, h.User, h.Hostname, h.Port)
	})
	if err != nil {
		fmt.Println("Host selection canceled.")
		return
	}

	hosts[idx].Connect()
}
