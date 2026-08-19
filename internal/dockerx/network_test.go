package dockerx

import "testing"

func TestFormatNetworkNamesAndIPPort(t *testing.T) {
	networks := map[string]struct {
		IPAddress string `json:"IPAddress"`
	}{
		"bridge": {IPAddress: "172.18.0.5"},
		"extra":  {IPAddress: "10.0.0.2"},
	}
	ports := map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}{
		"443/tcp": {{HostIP: "0.0.0.0", HostPort: "8443"}},
		"80/tcp":  {},
	}
	if got := formatNetworkNames(networks); got != "bridge, extra" {
		t.Fatalf("names=%q", got)
	}
	if got := formatIPPort(networks, ports); got != "172.18.0.5:443" {
		t.Fatalf("ipPort=%q", got)
	}
	if got := formatIPPort(nil, nil); got != "-" {
		t.Fatalf("empty=%q", got)
	}
}
