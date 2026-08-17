package softether

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ArminDashti/arvaz-api/internal/asn"
	"github.com/ArminDashti/arvaz-api/internal/haproxy"
)

type OnlineSession struct {
	Username               string     `json:"username"`
	ClientIP               string     `json:"clientIp"`
	LastISP                string     `json:"lastIsp,omitempty"`
	IspLogo                string     `json:"ispLogo,omitempty"`
	SessionName            string     `json:"sessionName,omitempty"`
	ConnectionName         string     `json:"connectionName,omitempty"`
	DownloadBytes          uint64     `json:"downloadBytes"`
	UploadBytes            uint64     `json:"uploadBytes"`
	TransferBytes          uint64     `json:"transferBytes,omitempty"`
	SessionDurationSeconds int64      `json:"sessionDurationSeconds,omitempty"`
	ConnectedAt            *time.Time `json:"connectedAt,omitempty"`
	SessionKey             string     `json:"sessionKey,omitempty"`
}

type HubUser struct {
	Username      string `json:"username"`
	NumLogins     int64  `json:"numLogins"`
	LastLogin     string `json:"lastLogin"`
	DownloadBytes uint64 `json:"downloadBytes"`
	UploadBytes   uint64 `json:"uploadBytes"`
	LastIP        string `json:"lastIp,omitempty"`
	LastISP       string `json:"lastIsp,omitempty"`
	IspLogo       string `json:"ispLogo,omitempty"`
	GroupName     string `json:"groupName,omitempty"`
	AuthMethod    string `json:"authMethod,omitempty"`
	TransferBytes uint64 `json:"transferBytes,omitempty"`
}

type Client struct {
	Container     string
	Password      string
	Hub           string
	Enabled       bool
	VpncmdTimeout time.Duration
	ASN           asn.Resolver
	HAProxy       *haproxy.Client
	mu            sync.Mutex
}

func New(container, password, hub string, enabled bool, vpncmdTimeout time.Duration, resolver asn.Resolver, hap *haproxy.Client) *Client {
	if vpncmdTimeout <= 0 {
		vpncmdTimeout = 20 * time.Second
	}
	if hub == "" {
		hub = "DEFAULT"
	}
	if container == "" {
		container = "softether"
	}
	if resolver == nil {
		resolver = asn.NullResolver{}
	}
	return &Client{
		Container:     container,
		Password:      password,
		Hub:           hub,
		Enabled:       enabled,
		VpncmdTimeout: vpncmdTimeout,
		ASN:           resolver,
		HAProxy:       hap,
	}
}

func (c *Client) ListOnlineSessions(ctx context.Context) ([]OnlineSession, error) {
	if !c.Enabled {
		return []OnlineSession{}, nil
	}
	out, err := c.vpncmd(ctx, "/HUB:"+c.Hub, "/CMD", "SessionList")
	if err != nil {
		return nil, err
	}
	sessions := parseSessionList(out, time.Now().UTC())
	connList, _ := c.vpncmd(ctx, "/CMD", "ConnectionList")
	connPorts := parseConnectionListPorts(connList)

	for i := range sessions {
		s := &sessions[i]
		if s.SessionName == "" {
			continue
		}
		detail, err := c.sessionGet(ctx, s.SessionName)
		clientPort := 0
		if err == nil {
			enrichFromSessionGet(s, detail, time.Now().UTC())
			clientPort = parseClientPort(detail)
		}
		// Never keep private/docker bridge IPs in the API response.
		if s.ClientIP != "" && !isPublicIP(s.ClientIP) {
			s.ClientIP = ""
		}
		if s.ClientIP == "" && c.HAProxy != nil {
			ports := make([]int, 0, 2)
			if clientPort > 0 {
				ports = append(ports, clientPort)
			}
			if s.ConnectedAt != nil {
				if p := lookupPortByConnectedAt(connPorts, *s.ConnectedAt); p > 0 {
					ports = append(ports, p)
				}
			}
			for _, port := range ports {
				if ip := c.HAProxy.LookupByBackendPort(ctx, port); ip != "" {
					s.ClientIP = ip
					break
				}
			}
			if s.ClientIP == "" {
				if ip := c.HAProxy.LookupByConnectedAt(ctx, s.ConnectedAt); ip != "" {
					s.ClientIP = ip
				}
			}
		}
		if s.ClientIP != "" {
			if label := c.ASN.Lookup(s.ClientIP); label != "" {
				s.LastISP = label
			}
		}
		s.SessionKey = s.Username + "|" + s.ClientIP + "|" + s.SessionName
	}
	return sessions, nil
}

func (c *Client) ListUsers(ctx context.Context) ([]HubUser, error) {
	if !c.Enabled {
		return []HubUser{}, nil
	}
	out, err := c.vpncmd(ctx, "/HUB:"+c.Hub, "/CMD", "UserList")
	if err != nil {
		return nil, err
	}
	users := parseUserList(out)
	for i := range users {
		detail, err := c.userGet(ctx, users[i].Username)
		if err != nil {
			continue
		}
		enrichUserFromUserGet(&users[i], detail)
	}
	return users, nil
}

func (c *Client) sessionGet(ctx context.Context, sessionName string) (string, error) {
	name := strings.TrimSpace(sessionName)
	if name == "" {
		return "", fmt.Errorf("empty session name")
	}
	return c.vpncmd(ctx, "/HUB:"+c.Hub, "/CMD", "SessionGet", name)
}

func (c *Client) userGet(ctx context.Context, username string) (string, error) {
	name := strings.TrimSpace(username)
	if name == "" {
		return "", fmt.Errorf("empty username")
	}
	return c.vpncmd(ctx, "/HUB:"+c.Hub, "/CMD", "UserGet", name)
}

func (c *Client) vpncmd(ctx context.Context, args ...string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// SoftEther already uses thousands of threads (each counts toward cgroup pids).
	// Do not wrap with the `timeout` binary — that extra fork fails under pressure
	// and can leave zombie [timeout] tasks when killed. Enforce deadline via context.
	runTimeout := c.VpncmdTimeout
	if runTimeout < time.Second {
		runTimeout = 20 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	cmdArgs := []string{
		"exec", "-i", c.Container,
		"vpncmd", "localhost", "/SERVER", "/PASSWORD:" + c.Password,
	}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.CommandContext(runCtx, "docker", cmdArgs...)
	cmd.Stdin = strings.NewReader(c.Password + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(msg, "Password:") || strings.Contains(msg, "Access has been denied") {
			return "", fmt.Errorf("softether auth failed (check SOFTETHER_PASSWORD)")
		}
		if runCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("vpncmd failed: timed out after %s", runTimeout)
		}
		return "", fmt.Errorf("vpncmd failed: %s", truncate(msg, 240))
	}
	return stdout.String(), nil
}

var (
	reSessionName    = regexp.MustCompile(`(?i)Session\s*Name\s*\|\s*(.+)`)
	reUserName       = regexp.MustCompile(`(?i)User\s*Name\s*\|\s*(.+)`)
	reConnectionName = regexp.MustCompile(`(?i)Connection\s*Name\s*\|\s*(.+)`)
	reIPField        = regexp.MustCompile(`(?i)(Client\s*IP(?:\s*Address)?|Source\s*IP(?:\s*Address)?|Source\s*Host\s*Name)\s*\|\s*(.+)`)
	reAnyIPv4        = regexp.MustCompile(`\b((?:(?:25[0-5]|2[0-4]\d|[01]?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d?\d))(?:[:/]\d+)?\b`)
	reTransferBytes  = regexp.MustCompile(`(?i)Transfer\s*Bytes\s*\|\s*(.+)`)
	reOutgoingData   = regexp.MustCompile(`(?i)Outgoing\s*Data\s*Size\s*\|\s*(.+)`)
	reIncomingData   = regexp.MustCompile(`(?i)Incoming\s*Data\s*Size\s*\|\s*(.+)`)
	reOutgoingUni    = regexp.MustCompile(`(?i)Outgoing\s*Unicast\s*Total\s*Size\s*\|\s*(.+)`)
	reIncomingUni    = regexp.MustCompile(`(?i)Incoming\s*Unicast\s*Total\s*Size\s*\|\s*(.+)`)
	reOutgoingBcast  = regexp.MustCompile(`(?i)Outgoing\s*Broadcast\s*Total\s*Size\s*\|\s*(.+)`)
	reIncomingBcast  = regexp.MustCompile(`(?i)Incoming\s*Broadcast\s*Total\s*Size\s*\|\s*(.+)`)
	reConnStarted    = regexp.MustCompile(`(?i)(Connection\s*Started\s*at|Current\s*Session\s*has\s*been\s*Established\s*since|First\s*Session\s*has\s*been\s*Established\s*since)\s*\|\s*(.+)`)
	reDuration       = regexp.MustCompile(`(?i)(Session\s*Duration|Connection\s*Time|Time\s*Connected|Duration)\s*\|\s*(.+)`)
	reNumLogins      = regexp.MustCompile(`(?i)Num(?:ber)?\s*of\s*Logins\s*\|\s*([0-9,]+)`)
	reLastLogin      = regexp.MustCompile(`(?i)Last\s*Login\s*\|\s*(.+)`)
	reGroupName      = regexp.MustCompile(`(?i)Group\s*Name\s*\|\s*(.+)`)
	reAuthMethod     = regexp.MustCompile(`(?i)Auth\s*(?:Type|Method)\s*\|\s*(.+)`)
	reClientPort     = regexp.MustCompile(`(?i)Client\s*Port(?:\s*\(Reported\))?\s*\|\s*([0-9,]+)`)
)

func parseClientPort(detail string) int {
	raw := matchFirst(reClientPort, detail)
	if raw == "" {
		return 0
	}
	n, _ := strconv.Atoi(strings.ReplaceAll(raw, ",", ""))
	return n
}

type connPortEntry struct {
	Port int
	At   time.Time
}

var reConnListRow = regexp.MustCompile(`(?i)(CID-\d+)\s*\|\s*([0-9.]+):\s*([0-9]+)\s*\|\s*([^|]+)`)

func parseConnectionListPorts(raw string) []connPortEntry {
	out := make([]connPortEntry, 0, 32)
	for _, line := range strings.Split(raw, "\n") {
		m := reConnListRow.FindStringSubmatch(line)
		if len(m) < 5 {
			continue
		}
		port, _ := strconv.Atoi(m[3])
		if port <= 0 {
			continue
		}
		at := parseSoftEtherTime(strings.TrimSpace(m[4]))
		if at == nil {
			continue
		}
		out = append(out, connPortEntry{Port: port, At: *at})
	}
	return out
}

func lookupPortByConnectedAt(entries []connPortEntry, connectedAt time.Time) int {
	bestPort := 0
	bestDelta := time.Duration(1<<63 - 1)
	for _, e := range entries {
		d := e.At.Sub(connectedAt)
		if d < 0 {
			d = -d
		}
		if d <= 3*time.Second && d < bestDelta {
			bestDelta = d
			bestPort = e.Port
		}
	}
	return bestPort
}

func enrichFromSessionGet(s *OnlineSession, detail string, now time.Time) {
	if cn := matchFirst(reConnectionName, detail); cn != "" {
		s.ConnectionName = cn
	}
	dl, ul := parseTraffic(detail)
	s.DownloadBytes = dl
	s.UploadBytes = ul
	s.TransferBytes = dl + ul
	if t := parseSoftEtherTime(matchFirstGroup(reConnStarted, detail, 2)); t != nil {
		s.ConnectedAt = t
		s.SessionDurationSeconds = int64(now.Sub(*t).Seconds())
		if s.SessionDurationSeconds < 0 {
			s.SessionDurationSeconds = 0
		}
	} else if dur := parseDurationSeconds(detail); dur > 0 {
		s.SessionDurationSeconds = dur
		tt := now.Add(-time.Duration(dur) * time.Second)
		s.ConnectedAt = &tt
	}
	if ip := pickPublicIP(detail); ip != "" {
		s.ClientIP = ip
	}
}

func enrichUserFromUserGet(u *HubUser, detail string) {
	dl, ul := parseTraffic(detail)
	u.DownloadBytes = dl
	u.UploadBytes = ul
	u.TransferBytes = dl + ul
	if n := parseUintComma(matchFirst(reNumLogins, detail)); n > 0 {
		u.NumLogins = int64(n)
	}
}

func parseSessionList(raw string, now time.Time) []OnlineSession {
	blocks := splitBlocks(raw)
	out := make([]OnlineSession, 0, len(blocks))
	for _, block := range blocks {
		name := matchFirst(reSessionName, block)
		user := matchFirst(reUserName, block)
		if user == "" {
			user = name
		}
		if user == "" || strings.EqualFold(user, "SecureNAT") || strings.EqualFold(user, "Local Bridge") {
			continue
		}
		if strings.Contains(strings.ToUpper(name), "LOCALBRIDGE") {
			continue
		}
		ip := pickPublicIP(block)
		dl, ul := parseTraffic(block)
		out = append(out, OnlineSession{
			Username:      user,
			ClientIP:      ip,
			SessionName:   name,
			DownloadBytes: dl,
			UploadBytes:   ul,
			TransferBytes: dl + ul,
		})
	}
	_ = now
	return out
}

func parseUserList(raw string) []HubUser {
	blocks := splitBlocks(raw)
	out := make([]HubUser, 0, len(blocks))
	for _, block := range blocks {
		user := matchFirst(reUserName, block)
		if user == "" {
			continue
		}
		group := matchFirst(reGroupName, block)
		if group == "-" {
			group = ""
		}
		transfer := parseSizeValue(matchFirst(reTransferBytes, block))
		out = append(out, HubUser{
			Username:      user,
			GroupName:     group,
			AuthMethod:    matchFirst(reAuthMethod, block),
			NumLogins:     int64(parseUintComma(matchFirst(reNumLogins, block))),
			LastLogin:     matchFirst(reLastLogin, block),
			TransferBytes: transfer,
		})
	}
	return out
}

func pickPublicIP(block string) string {
	var candidates []string
	for _, m := range reIPField.FindAllStringSubmatch(block, -1) {
		if len(m) < 3 {
			continue
		}
		val := strings.TrimSpace(m[2])
		candidates = append(candidates, extractIPs(val)...)
	}
	for _, c := range candidates {
		if isPublicIP(c) {
			return c
		}
	}
	return ""
}

func extractIPs(s string) []string {
	ms := reAnyIPv4.FindAllStringSubmatch(s, -1)
	out := make([]string, 0, len(ms))
	seen := map[string]struct{}{}
	for _, m := range ms {
		if len(m) < 2 {
			continue
		}
		ip := m[1]
		if _, ok := seen[ip]; ok {
			continue
		}
		seen[ip] = struct{}{}
		out = append(out, ip)
	}
	return out
}

func isPublicIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 10 {
			return false
		}
		if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
			return false
		}
		if ip4[0] == 192 && ip4[1] == 168 {
			return false
		}
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return false
		}
	}
	return true
}

// parseTraffic returns client download (SoftEther incoming) and upload (outgoing).
// Prefers Data Size; otherwise Unicast Total Size + Broadcast Total Size.
// Packet-count lines are ignored.
func parseTraffic(block string) (download, upload uint64) {
	outData := parseSizeValue(matchFirst(reOutgoingData, block))
	inData := parseSizeValue(matchFirst(reIncomingData, block))
	if outData > 0 || inData > 0 {
		return inData, outData
	}
	out := parseSizeValue(matchFirst(reOutgoingUni, block)) + parseSizeValue(matchFirst(reOutgoingBcast, block))
	in := parseSizeValue(matchFirst(reIncomingUni, block)) + parseSizeValue(matchFirst(reIncomingBcast, block))
	if out > 0 || in > 0 {
		return in, out
	}
	if n := parseSizeValue(matchFirst(reTransferBytes, block)); n > 0 {
		return n, 0
	}
	return 0, 0
}

func parseDurationSeconds(block string) int64 {
	m := reDuration.FindStringSubmatch(block)
	raw := ""
	if len(m) >= 3 {
		raw = strings.TrimSpace(m[2])
	}
	if raw == "" {
		return 0
	}
	if secs, err := strconv.ParseInt(strings.ReplaceAll(raw, ",", ""), 10, 64); err == nil {
		return secs
	}
	var days, hours, mins, secs int64
	rePart := regexp.MustCompile(`(?i)(\d+)\s*(day|hour|min|sec)`)
	for _, mm := range rePart.FindAllStringSubmatch(raw, -1) {
		if len(mm) < 3 {
			continue
		}
		n, _ := strconv.ParseInt(mm[1], 10, 64)
		unit := strings.ToLower(mm[2])
		switch {
		case strings.HasPrefix(unit, "day"):
			days = n
		case strings.HasPrefix(unit, "hour"):
			hours = n
		case strings.HasPrefix(unit, "min"):
			mins = n
		case strings.HasPrefix(unit, "sec"):
			secs = n
		}
	}
	return days*86400 + hours*3600 + mins*60 + secs
}

// SoftEther time like: 2026-08-12 (Wed) 15:07:37
func parseSoftEtherTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" {
		return nil
	}
	re := regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2}).*?(\d{1,2}):(\d{2}):(\d{2})`)
	m := re.FindStringSubmatch(raw)
	if len(m) < 7 {
		return nil
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	h, _ := strconv.Atoi(m[4])
	mi, _ := strconv.Atoi(m[5])
	s, _ := strconv.Atoi(m[6])
	// SoftEther timestamps on T3 are local server time.
	loc := time.Local
	t := time.Date(y, time.Month(mo), d, h, mi, s, 0, loc)
	return &t
}

func splitBlocks(raw string) []string {
	lines := strings.Split(raw, "\n")
	var blocks []string
	var cur []string
	for _, line := range lines {
		if strings.Contains(line, "-----") && len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
			continue
		}
		if strings.TrimSpace(line) == "" {
			if len(cur) > 0 {
				blocks = append(blocks, strings.Join(cur, "\n"))
				cur = nil
			}
			continue
		}
		cur = append(cur, line)
	}
	if len(cur) > 0 {
		blocks = append(blocks, strings.Join(cur, "\n"))
	}
	return blocks
}

func matchFirst(re *regexp.Regexp, block string) string {
	m := re.FindStringSubmatch(block)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func matchFirstGroup(re *regexp.Regexp, block string, group int) string {
	m := re.FindStringSubmatch(block)
	if len(m) <= group {
		return ""
	}
	return strings.TrimSpace(m[group])
}

func parseUintComma(s string) uint64 {
	return parseSizeValue(s)
}

var reSizeValue = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*([KMGT]i?B(?:ytes?)?)?`)
var reThousandsComma = regexp.MustCompile(`(\d),(\d{3})`)

func parseSizeValue(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	for reThousandsComma.MatchString(s) {
		s = reThousandsComma.ReplaceAllString(s, "$1$2")
	}
	m := reSizeValue.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil || v < 0 {
		return 0
	}
	unit := strings.ToLower(strings.TrimSpace(m[2]))
	mult := 1.0
	switch unit {
	case "", "b", "byte", "bytes":
		mult = 1
	case "kb", "kbyte", "kbytes":
		mult = 1000
	case "kib":
		mult = 1024
	case "mb", "mbyte", "mbytes":
		mult = 1_000_000
	case "mib":
		mult = 1024 * 1024
	case "gb", "gbyte", "gbytes":
		mult = 1_000_000_000
	case "gib":
		mult = 1024 * 1024 * 1024
	case "tb", "tbyte", "tbytes":
		mult = 1_000_000_000_000
	case "tib":
		mult = 1024 * 1024 * 1024 * 1024
	}
	return uint64(v*mult + 0.5)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
