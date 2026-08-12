package softether

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ArminDashti/arvaz-api/internal/asn"
)

type OnlineSession struct {
	Username               string     `json:"username"`
	ClientIP               string     `json:"clientIp"`
	ASN                    *string    `json:"asn"`
	BandwidthBps           float64    `json:"bandwidthBps"`
	DownloadBytes          uint64     `json:"downloadBytes"`
	UploadBytes            uint64     `json:"uploadBytes"`
	SessionDurationSeconds int64      `json:"sessionDurationSeconds"`
	ConnectedAt            *time.Time `json:"connectedAt"`
	SessionKey             string     `json:"sessionKey"`
}

type Client struct {
	Container string
	Password  string
	Hub       string
	Enabled   bool
	ASN       asn.Resolver
}

func New(container, password, hub string, enabled bool, resolver asn.Resolver) *Client {
	if resolver == nil {
		resolver = asn.NullResolver{}
	}
	return &Client{
		Container: container,
		Password:  password,
		Hub:       hub,
		Enabled:   enabled,
		ASN:       resolver,
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
	return parseSessionList(out, c.ASN), nil
}

func (c *Client) vpncmd(ctx context.Context, args ...string) (string, error) {
	cmdArgs := []string{
		"exec", c.Container,
		"vpncmd", "localhost", "/SERVER", "/PASSWORD:" + c.Password,
	}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
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
		return "", fmt.Errorf("vpncmd failed: %s", msg)
	}
	return stdout.String(), nil
}

var (
	reSessionName = regexp.MustCompile(`(?i)Session\s*Name\s*\|\s*(.+)`)
	reUserName    = regexp.MustCompile(`(?i)User\s*Name\s*\|\s*(.+)`)
	reClientIP    = regexp.MustCompile(`(?i)(Client\s*IP|Source\s*IP|IP\s*Address)\s*\|\s*([0-9a-fA-F\.:]+)`)
	reTransfer    = regexp.MustCompile(`(?i)(Transfer|Traffic|Bytes).*\|\s*([0-9,]+)\s*(bytes)?`)
)

func parseSessionList(raw string, resolver asn.Resolver) []OnlineSession {
	blocks := splitBlocks(raw)
	out := make([]OnlineSession, 0, len(blocks))
	now := time.Now().UTC()
	for _, block := range blocks {
		name := matchFirst(reSessionName, block)
		user := matchFirst(reUserName, block)
		if user == "" {
			user = name
		}
		if user == "" || strings.EqualFold(user, "SecureNAT") {
			continue
		}
		ip := ""
		if m := reClientIP.FindStringSubmatch(block); len(m) >= 3 {
			ip = strings.TrimSpace(m[2])
		}
		dl, ul := parseTraffic(block)
		var asnPtr *string
		if a := resolver.Lookup(ip); a != "" {
			asnPtr = &a
		}
		key := user + "|" + ip + "|" + name
		connected := now.Add(-5 * time.Minute)
		out = append(out, OnlineSession{
			Username:               user,
			ClientIP:               ip,
			ASN:                    asnPtr,
			BandwidthBps:           0,
			DownloadBytes:          dl,
			UploadBytes:            ul,
			SessionDurationSeconds: 300,
			ConnectedAt:            &connected,
			SessionKey:             key,
		})
	}
	return out
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

func parseTraffic(block string) (uint64, uint64) {
	matches := reTransfer.FindAllStringSubmatch(block, -1)
	vals := make([]uint64, 0, 2)
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		n, err := strconv.ParseUint(strings.ReplaceAll(m[2], ",", ""), 10, 64)
		if err == nil {
			vals = append(vals, n)
		}
	}
	if len(vals) >= 2 {
		return vals[0], vals[1]
	}
	if len(vals) == 1 {
		return vals[0], 0
	}
	return 0, 0
}
