package haproxy

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client maps SoftEther backend sessions to frontend public client IPs via HAProxy.
type Client struct {
	Container  string
	SocketPath string
	mu         sync.Mutex
	byPort     map[int]string
	entries    []sessEntry
	cacheAt    time.Time
	ttl        time.Duration
}

type sessEntry struct {
	IP  string
	Age time.Duration
}

func New(container, socketPath string) *Client {
	if container == "" {
		container = "haproxy"
	}
	if socketPath == "" {
		socketPath = "/var/lib/haproxy/admin.sock"
	}
	return &Client{
		Container:  container,
		SocketPath: socketPath,
		byPort:     map[int]string{},
		ttl:        5 * time.Second,
	}
}

var (
	reSessID      = regexp.MustCompile(`^(0x[0-9a-fA-F]+)`)
	reSourceIP    = regexp.MustCompile(`(?i)\b(?:src|source)=([0-9.]+):([0-9]+)`)
	reBackendAddr = regexp.MustCompile(`(?i)backend=\S+\s+\([^)]*\)\s+addr=([0-9.]+):([0-9]+)`)
	reSessAge     = regexp.MustCompile(`(?i)\bage=([0-9]+)([dhms])`)
	reConnSrc     = regexp.MustCompile(`(?i)(CID-\d+)\s*\|\s*([0-9.]+):\s*([0-9]+)`)
)

func (c *Client) LookupByBackendPort(ctx context.Context, backendPort int) string {
	if backendPort <= 0 {
		return ""
	}
	c.refresh(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byPort[backendPort]
}

func (c *Client) LookupByConnectedAt(ctx context.Context, connectedAt *time.Time) string {
	if connectedAt == nil {
		return ""
	}
	c.refresh(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	wantAge := time.Since(*connectedAt)
	if wantAge < 0 {
		wantAge = 0
	}
	const window = 45 * time.Second
	bestIP := ""
	bestDelta := time.Duration(1<<63 - 1)
	near := map[string]struct{}{}
	for _, e := range c.entries {
		d := e.Age - wantAge
		if d < 0 {
			d = -d
		}
		if d > window {
			continue
		}
		near[e.IP] = struct{}{}
		if d < bestDelta {
			bestDelta = d
			bestIP = e.IP
		}
	}
	if len(near) == 1 {
		for ip := range near {
			return ip
		}
	}
	return bestIP
}

func (c *Client) refresh(ctx context.Context) {
	c.mu.Lock()
	fresh := time.Since(c.cacheAt) < c.ttl && (len(c.byPort) > 0 || len(c.entries) > 0)
	c.mu.Unlock()
	if fresh {
		return
	}
	byPort, entries, err := c.loadMaps(ctx)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.byPort = byPort
	c.entries = entries
	c.cacheAt = time.Now()
	c.mu.Unlock()
}

func (c *Client) loadMaps(ctx context.Context) (map[int]string, []sessEntry, error) {
	list, err := c.runStats(ctx, "show sess")
	if err != nil {
		return nil, nil, err
	}
	ids := softetherSessIDs(list)
	byPort := map[int]string{}
	entries := []sessEntry{}
	for _, id := range ids {
		detail, err := c.runStats(ctx, "show sess "+id)
		if err != nil || detail == "" {
			continue
		}
		ip, port, age := parseSessDetail(detail)
		if ip == "" || !looksPublic(ip) {
			continue
		}
		entries = append(entries, sessEntry{IP: ip, Age: age})
		if port > 0 {
			byPort[port] = ip
		}
	}
	return byPort, entries, nil
}

func softetherSessIDs(raw string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 32)
	for _, line := range strings.Split(raw, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(low, "be_softether") && !strings.Contains(low, "softether") {
			continue
		}
		m := reSessID.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) < 2 {
			continue
		}
		id := m[1]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func parseSessDetail(raw string) (ip string, backendPort int, age time.Duration) {
	if sm := reSourceIP.FindStringSubmatch(raw); len(sm) >= 2 {
		ip = sm[1]
	}
	if bm := reBackendAddr.FindStringSubmatch(raw); len(bm) >= 3 {
		backendPort, _ = strconv.Atoi(bm[2])
	}
	age = parseAge(raw)
	return ip, backendPort, age
}

func (c *Client) runStats(ctx context.Context, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	script := fmt.Sprintf(`echo %s | socat STDIO UNIX-CONNECT:%s`, shellQuote(command), c.SocketPath)
	cmd := exec.CommandContext(ctx, "docker", "exec", c.Container, "sh", "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("haproxy %s: %s", command, msg)
	}
	return stdout.String(), nil
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func parseAge(line string) time.Duration {
	var total time.Duration
	for _, m := range reSessAge.FindAllStringSubmatch(line, -1) {
		if len(m) < 3 {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "d":
			total += time.Duration(n) * 24 * time.Hour
		case "h":
			total += time.Duration(n) * time.Hour
		case "m":
			total += time.Duration(n) * time.Minute
		case "s":
			total += time.Duration(n) * time.Second
		}
	}
	return total
}

func looksPublic(ip string) bool {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return false
	}
	if parts[0] == "10" || parts[0] == "127" {
		return false
	}
	if parts[0] == "192" && parts[1] == "168" {
		return false
	}
	if parts[0] == "172" {
		n, _ := strconv.Atoi(parts[1])
		if n >= 16 && n <= 31 {
			return false
		}
	}
	return true
}

// ParseConnectionSourcePort extracts HAProxy→SoftEther source port from ConnectionList text for a CID.
func ParseConnectionSourcePort(connectionList, connectionName string) int {
	connectionName = strings.TrimSpace(connectionName)
	if connectionName == "" {
		return 0
	}
	for _, line := range strings.Split(connectionList, "\n") {
		if !strings.Contains(line, connectionName) {
			continue
		}
		m := reConnSrc.FindStringSubmatch(line)
		if len(m) >= 4 && strings.EqualFold(m[1], connectionName) {
			p, _ := strconv.Atoi(m[3])
			return p
		}
		pm := regexp.MustCompile(`:\s*([0-9]+)`).FindStringSubmatch(line)
		if len(pm) >= 2 {
			p, _ := strconv.Atoi(pm[1])
			return p
		}
	}
	return 0
}
