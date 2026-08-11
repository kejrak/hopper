package host

import "testing"

func TestDisplayWithUser(t *testing.T) {
	h := Host{Name: "web-prod", User: "deploy", Hostname: "10.0.1.20", Port: "22"}
	if got, want := h.Display(), "web-prod (deploy@10.0.1.20:22)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDisplayWithoutUserOmitsAt(t *testing.T) {
	h := Host{Name: "nas", Hostname: "nas.local", Port: "2222"}
	if got, want := h.Display(), "nas (nas.local:2222)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
