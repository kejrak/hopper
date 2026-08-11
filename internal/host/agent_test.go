package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const agentList = `256 SHA256:AbCdEf123 /home/u/.ssh/id_work (ED25519)
3072 SHA256:XyZ987 someone@laptop (RSA)
`

func TestKeyListedByFingerprint(t *testing.T) {
	if !keyListed(agentList, "SHA256:AbCdEf123", "") {
		t.Fatal("fingerprint present but not matched")
	}
	if keyListed(agentList, "SHA256:Missing", "") {
		t.Fatal("absent fingerprint matched")
	}
}

func TestKeyListedFallsBackToPathWhenNoFingerprint(t *testing.T) {
	if !keyListed(agentList, "", "/home/u/.ssh/id_work") {
		t.Fatal("path in comment but not matched")
	}
	if keyListed(agentList, "", "/home/u/.ssh/other") {
		t.Fatal("absent path matched")
	}
}

func TestKeyListedPathMatchWholeToken(t *testing.T) {
	// Ensure exact path match, not substring/prefix match
	agentWithProd := `256 SHA256:ProdKey /home/u/.ssh/id_ed25519_prod (ED25519)
`
	// Shorter path should not match when longer path is in list
	if keyListed(agentWithProd, "", "/home/u/.ssh/id_ed25519") {
		t.Fatal("short path matched substring of longer path in agent")
	}
	// Exact path should match
	if !keyListed(agentWithProd, "", "/home/u/.ssh/id_ed25519_prod") {
		t.Fatal("exact path not matched")
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got, want := ExpandPath("~/.ssh/id_work"), filepath.Join(home, ".ssh", "id_work"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := ExpandPath("/abs/path"); got != "/abs/path" {
		t.Fatalf("absolute path changed: %q", got)
	}
}

func TestAddKeyCommand(t *testing.T) {
	cmd := AddKeyCommand("~/.ssh/id_work")
	if cmd.Args[0] != "ssh-add" || len(cmd.Args) != 3 || cmd.Args[1] != "--" {
		t.Fatalf("got %v, want [ssh-add -- <expanded path>]", cmd.Args)
	}
	if strings.HasPrefix(cmd.Args[2], "~") {
		t.Fatalf("path not expanded: %v", cmd.Args)
	}
}

func TestAgentStatusesUnknownWithoutIdentity(t *testing.T) {
	statuses := AgentStatuses([]Host{{Name: "h"}})
	if statuses["h"] != KeyStatusUnknown {
		t.Fatalf("got %v, want KeyStatusUnknown", statuses["h"])
	}
}
