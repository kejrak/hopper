package sshcfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostsAcrossIncludesWithGroups(t *testing.T) {
	dir := t.TempDir()
	root := write(t, dir, "config",
		"Include conf.d/*.conf\nHost rootHost\n  Hostname 10.0.0.1\n  User admin\n")
	write(t, dir, "conf.d/work.conf",
		"Host gitlab\n  Hostname git.example.com\n  Port 2222\n  IdentityFile ~/.ssh/id_work\n")
	hosts, warnings, err := Hosts(root, dir)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("err=%v warnings=%v", err, warnings)
	}
	if len(hosts) != 2 {
		t.Fatalf("got %d hosts, want 2: %+v", len(hosts), hosts)
	}
	rh, gl := hosts[0], hosts[1]
	if rh.Name != "rootHost" || rh.User != "admin" || rh.Group != "default" {
		t.Errorf("rootHost wrong: %+v", rh)
	}
	if gl.Name != "gitlab" || gl.Port != "2222" || gl.Group != "work" ||
		gl.IdentityFile != "~/.ssh/id_work" || gl.Source != filepath.Join(dir, "conf.d/work.conf") {
		t.Errorf("gitlab wrong: %+v", gl)
	}
}

func TestHostsSkipsWildcardsAndDedupes(t *testing.T) {
	dir := t.TempDir()
	root := write(t, dir, "config",
		"Host *\n  User fallback\nHost web-?\n  Port 8022\nHost dup\n  User first\nHost dup\n  User second\n")
	hosts, _, err := Hosts(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Name != "dup" {
		t.Fatalf("got %+v, want just dup", hosts)
	}
	if hosts[0].User != "first" {
		t.Errorf("first definition should win, got User=%q", hosts[0].User)
	}
}

func TestHostsDefaultsHostnameAndPort(t *testing.T) {
	dir := t.TempDir()
	root := write(t, dir, "config", "Host bare\n")
	hosts, _, err := Hosts(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	h := hosts[0]
	if h.Hostname != "bare" || h.Port != "22" || h.User != "" || h.IdentityFile != "" {
		t.Fatalf("bad defaults: %+v", h)
	}
}

func TestHostsMissingRootIsError(t *testing.T) {
	if _, _, err := Hosts(filepath.Join(t.TempDir(), "nope"), t.TempDir()); err == nil {
		t.Fatal("want error for missing root config")
	}
}

func TestHostsUnparsableIncludeWarnsAndContinues(t *testing.T) {
	dir := t.TempDir()
	root := write(t, dir, "config", "Include bad.conf\nHost ok\n")
	write(t, dir, "bad.conf", "Host broken\n  Port not-a-port \x00\n")
	hosts, _, err := Hosts(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if h.Name == "ok" {
			return
		}
	}
	t.Fatalf("host from root missing after bad include: %+v", hosts)
}

func TestHostsUnreadableRootIsError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, cannot test permission denial")
	}
	dir := t.TempDir()
	root := write(t, dir, "config", "Host h\n")
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatalf("failed to chmod root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o644) })
	if _, _, err := Hosts(root, dir); err == nil {
		t.Fatal("want error for unreadable root config")
	}
}

func TestHostsMixedConcreteWildcardDoesNotLeak(t *testing.T) {
	dir := t.TempDir()
	root := write(t, dir, "config",
		"Host bastion *.corp\n  User jump\n  Port 2222\nHost db.corp\n")
	hosts, _, err := Hosts(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Fatalf("got %d hosts, want 2: %+v", len(hosts), hosts)
	}
	bastion, db := hosts[0], hosts[1]
	if bastion.Name != "bastion" {
		t.Errorf("first host should be bastion, got %s", bastion.Name)
	}
	if db.Name != "db.corp" {
		t.Errorf("second host should be db.corp, got %s", db.Name)
	}
	// db.corp must NOT inherit User from the wildcard *.corp pattern in the bastion block.
	if db.User != "" {
		t.Errorf("db.corp must not inherit User from *.corp wildcard, got User=%q", db.User)
	}
	if db.Port != "22" {
		t.Errorf("db.corp must have default Port, got %q", db.Port)
	}
}
