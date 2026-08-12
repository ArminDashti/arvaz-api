package dockerx

import (
	"os"
	"regexp"
	"strings"
)

var (
	reHostACL  = regexp.MustCompile(`(?i)(?:hdr\(host\)|req\.hdr\(host\)|req_ssl_sni)\s+-i\s+([a-z0-9._-]+)`)
	reUseBackend = regexp.MustCompile(`(?i)use_backend\s+(\S+)\s+if`)
	reBackend  = regexp.MustCompile(`(?i)^backend\s+(\S+)`)
	reServer   = regexp.MustCompile(`(?i)^\s*server\s+\S+\s+([a-zA-Z0-9._-]+)(?::(\d+))?`)
)

// ParseHAProxyRoutes maps docker container hostname -> "public.host:443".
func ParseHAProxyRoutes(configPath string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return out
	}
	lines := strings.Split(string(raw), "\n")

	// hostname -> backend from use_backend ... if { host match }
	hostToBackend := map[string]string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(strings.ToLower(trimmed), "use_backend") {
			continue
		}
		bm := reUseBackend.FindStringSubmatch(trimmed)
		hm := reHostACL.FindStringSubmatch(trimmed)
		if len(bm) < 2 || len(hm) < 2 {
			continue
		}
		hostToBackend[strings.ToLower(hm[1])] = bm[1]
	}

	// backend -> container name from server lines
	backendToContainer := map[string]string{}
	currentBackend := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := reBackend.FindStringSubmatch(trimmed); len(m) == 2 {
			currentBackend = m[1]
			continue
		}
		if currentBackend == "" {
			continue
		}
		if m := reServer.FindStringSubmatch(trimmed); len(m) >= 2 {
			name := m[1]
			// skip loopback / IP-only backends for host gateway
			if name == "127.0.0.1" || name == "localhost" {
				continue
			}
			if strings.Count(name, ".") == 3 && isAllDigitsDots(name) {
				continue
			}
			backendToContainer[currentBackend] = name
		}
	}

	for host, backend := range hostToBackend {
		container, ok := backendToContainer[backend]
		if !ok {
			continue
		}
		// SoftEther TCP passthrough uses mullvad-1; map also as softether alias if hostname is vpn
		url := host + ":443"
		if existing, ok := out[container]; ok && existing != "-" {
			continue
		}
		out[container] = url
	}
	return out
}

func isAllDigitsDots(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}
