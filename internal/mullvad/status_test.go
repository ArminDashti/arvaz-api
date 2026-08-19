package mullvad

import "testing"

func TestParseVisibleIPv4(t *testing.T) {
	got := parseVisibleIPv4("Sweden, Stockholm. IPv4: 185.213.154.10, IPv6: 2a03:1b20::1")
	if got != "185.213.154.10" {
		t.Fatalf("got %q", got)
	}
	if parseVisibleIPv4("Sweden, Stockholm") != "" {
		t.Fatal("expected empty")
	}
}
