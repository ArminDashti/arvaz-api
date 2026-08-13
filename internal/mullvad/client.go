package mullvad

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	Container string
}

func New(container string) *Client {
	if strings.TrimSpace(container) == "" {
		container = "mullvad-1"
	}
	return &Client{Container: container}
}

type Status struct {
	Raw             string `json:"raw"`
	Connected       bool   `json:"connected"`
	Relay           string `json:"relay"`
	RelayIP         string `json:"relayIp,omitempty"`
	VisibleLocation string `json:"visibleLocation"`
	Country         string `json:"country,omitempty"`
	City            string `json:"city,omitempty"`
	Features        string `json:"features"`
	AntiCensorship  string `json:"antiCensorship"`
}

type Relay struct {
	Country     string `json:"country"`
	CountryCode string `json:"countryCode"`
	City        string `json:"city"`
	CityCode    string `json:"cityCode"`
	Hostname    string `json:"hostname"`
	IPv4        string `json:"ipv4"`
	Active      bool   `json:"active"`
}

type PingResult struct {
	Target            string  `json:"target"`
	Count             int     `json:"count"`
	PacketLossPercent float64 `json:"packetLossPercent"`
	AvgMs             float64 `json:"avgMs"`
	Raw               string  `json:"raw,omitempty"`
}

type SpeedtestResult struct {
	Mode         string  `json:"mode"`
	Raw          string  `json:"raw"`
	DownloadMbps float64 `json:"downloadMbps,omitempty"`
	UploadMbps   float64 `json:"uploadMbps,omitempty"`
	LatencyMs    float64 `json:"latencyMs,omitempty"`
	ParsedOK     bool    `json:"parsedOk"`
}

var (
	reCountry = regexp.MustCompile(`^([A-Za-z].*?)\s+\(([a-z]{2})\)$`)
	reCity    = regexp.MustCompile(`^\t([^\t@]+?)\s+\(([a-z]+)\)\s+@`)
	reHost    = regexp.MustCompile(`^\t\t([a-z0-9-]+)\s+\(([0-9.]+)`)
	reRelay   = regexp.MustCompile(`(?i)Relay:\s+(\S+)`)
	reLoc     = regexp.MustCompile(`(?i)Visible location:\s+(.+)`)
	reFeat    = regexp.MustCompile(`(?i)Features:\s+(.+)`)
)

func (c *Client) Status(ctx context.Context) (*Status, error) {
	raw, err := c.exec(ctx, 20*time.Second, "mullvad", "status", "-v")
	if err != nil {
		return nil, err
	}
	ac, _ := c.exec(ctx, 15*time.Second, "mullvad", "anti-censorship", "get")
	st := &Status{
		Raw:            strings.TrimSpace(raw),
		Connected:      strings.HasPrefix(strings.TrimSpace(raw), "Connected"),
		AntiCensorship: parseAntiMode(ac),
	}
	if m := reRelay.FindStringSubmatch(raw); len(m) == 2 {
		st.Relay = m[1]
	}
	// Relay: se-sto-wg-209 (170.62.100.217:20293/UDP)
	if m := regexp.MustCompile(`(?i)Relay:\s+(\S+)\s+\(([^):\s]+)`).FindStringSubmatch(raw); len(m) == 3 {
		st.Relay = m[1]
		st.RelayIP = m[2]
	}
	if m := reLoc.FindStringSubmatch(raw); len(m) == 2 {
		st.VisibleLocation = strings.TrimSpace(m[1])
		// Sweden, Stockholm. IPv4: ...
		loc := st.VisibleLocation
		if i := strings.Index(loc, ". IPv4"); i >= 0 {
			loc = strings.TrimSpace(loc[:i])
		}
		parts := strings.SplitN(loc, ",", 2)
		st.Country = strings.TrimSpace(parts[0])
		if len(parts) > 1 {
			st.City = strings.TrimSpace(parts[1])
		}
	}
	if m := reFeat.FindStringSubmatch(raw); len(m) == 2 {
		st.Features = strings.TrimSpace(m[1])
	}
	return st, nil
}

func (c *Client) ListRelays(ctx context.Context) ([]Relay, error) {
	raw, err := c.exec(ctx, 60*time.Second, "mullvad", "relay", "list")
	if err != nil {
		return nil, err
	}
	status, _ := c.Status(ctx)
	active := ""
	if status != nil {
		active = status.Relay
	}
	return parseRelayList(raw, active), nil
}

func (c *Client) SetRelay(ctx context.Context, country, city, hostname string) error {
	args := []string{"mullvad", "relay", "set", "location", country}
	if city != "" {
		args = append(args, city)
	}
	if hostname != "" {
		args = append(args, hostname)
	}
	if _, err := c.exec(ctx, 30*time.Second, args...); err != nil {
		return err
	}
	_, err := c.exec(ctx, 30*time.Second, "mullvad", "reconnect")
	return err
}

func (c *Client) GetAntiCensorship(ctx context.Context) (string, error) {
	raw, err := c.exec(ctx, 15*time.Second, "mullvad", "anti-censorship", "get")
	if err != nil {
		return "", err
	}
	return parseAntiMode(raw), nil
}

func (c *Client) SetAntiCensorship(ctx context.Context, mode string) error {
	mode = strings.TrimSpace(strings.ToLower(mode))
	allowed := map[string]bool{
		"auto": true, "off": true, "wireguard-port": true,
		"udp2tcp": true, "shadowsocks": true, "quic": true, "lwo": true,
	}
	if !allowed[mode] {
		return fmt.Errorf("invalid anti-censorship mode")
	}
	_, err := c.exec(ctx, 30*time.Second, "mullvad", "anti-censorship", "set", "mode", mode)
	return err
}

func (c *Client) Ping(ctx context.Context, target string, count int) (*PingResult, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		target = "1.1.1.1"
	}
	if !validPingTarget(target) {
		return nil, fmt.Errorf("invalid ping target")
	}
	if count < 1 {
		count = 4
	}
	if count > 20 {
		count = 20
	}
	timeout := time.Duration(count*2+10) * time.Second
	if timeout < 20*time.Second {
		timeout = 20 * time.Second
	}
	raw, err := c.exec(ctx, timeout, "ping", "-c", strconv.Itoa(count), target)
	if err != nil && raw == "" {
		return nil, err
	}
	loss, avg := parsePingStats(raw)
	return &PingResult{
		Target:            target,
		Count:             count,
		PacketLossPercent: loss,
		AvgMs:             avg,
		Raw:               strings.TrimSpace(raw),
	}, nil
}

var rePingTarget = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
var rePingLoss = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)%\s*packet\s+loss`)
var rePingAvg = regexp.MustCompile(`(?i)(?:rtt|round-trip)[^=]*=\s*[0-9.]+/([0-9.]+)`)

func validPingTarget(s string) bool {
	return rePingTarget.MatchString(s)
}

func parsePingStats(raw string) (lossPercent, avgMs float64) {
	if m := rePingLoss.FindStringSubmatch(raw); len(m) == 2 {
		lossPercent, _ = strconv.ParseFloat(m[1], 64)
	}
	if m := rePingAvg.FindStringSubmatch(raw); len(m) == 2 {
		avgMs, _ = strconv.ParseFloat(m[1], 64)
	}
	return lossPercent, avgMs
}

func (c *Client) Speedtest(ctx context.Context, mode string) (*SpeedtestResult, error) {
	mode = strings.TrimSpace(strings.ToLower(mode))
	args := []string{"speedtest-cli", "--secure", "--json"}
	if mode == "single" {
		args = []string{"speedtest-cli", "--secure", "--single", "--json"}
	} else {
		mode = "parallel"
	}
	raw, err := c.exec(ctx, 120*time.Second, args...)
	if err != nil && raw == "" {
		return nil, err
	}
	res := &SpeedtestResult{Mode: mode, Raw: strings.TrimSpace(raw)}
	parseSpeedtestJSON(res)
	if !res.ParsedOK {
		// Fallback to simple text
		simpleArgs := []string{"speedtest-cli", "--secure", "--simple"}
		if mode == "single" {
			simpleArgs = []string{"speedtest-cli", "--secure", "--single", "--simple"}
		}
		if sraw, serr := c.exec(ctx, 120*time.Second, simpleArgs...); serr == nil || sraw != "" {
			res.Raw = strings.TrimSpace(sraw)
			parseSpeedtestSimple(res)
		}
	}
	return res, nil
}

func parseSpeedtestJSON(res *SpeedtestResult) {
	raw := strings.TrimSpace(res.Raw)
	if raw == "" || raw[0] != '{' {
		return
	}
	// Minimal parse without encoding/json import churn for nested fields.
	// speedtest-cli JSON: download/upload in bit/s, ping in ms.
	reNum := func(key string) float64 {
		m := regexp.MustCompile(`"` + key + `"\s*:\s*([0-9.]+)`).FindStringSubmatch(raw)
		if len(m) < 2 {
			return 0
		}
		v, _ := strconv.ParseFloat(m[1], 64)
		return v
	}
	dl := reNum("download")
	ul := reNum("upload")
	ping := reNum("ping")
	if dl == 0 && ul == 0 && ping == 0 {
		return
	}
	res.DownloadMbps = dl / 1_000_000
	res.UploadMbps = ul / 1_000_000
	res.LatencyMs = ping
	res.ParsedOK = true
}

func parseSpeedtestSimple(res *SpeedtestResult) {
	// Ping: 12.3 ms / Download: 45.6 Mbit/s / Upload: 12.3 Mbit/s
	for _, line := range strings.Split(res.Raw, "\n") {
		low := strings.ToLower(strings.TrimSpace(line))
		re := regexp.MustCompile(`(?i)(ping|download|upload):\s*([0-9.]+)`)
		m := re.FindStringSubmatch(low)
		if len(m) < 3 {
			continue
		}
		v, _ := strconv.ParseFloat(m[2], 64)
		switch m[1] {
		case "ping":
			res.LatencyMs = v
		case "download":
			res.DownloadMbps = v
		case "upload":
			res.UploadMbps = v
		}
	}
	res.ParsedOK = res.DownloadMbps > 0 || res.UploadMbps > 0 || res.LatencyMs > 0
}

func (c *Client) exec(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	for _, a := range args {
		lower := strings.ToLower(a)
		if strings.Contains(lower, "disconnect") || strings.Contains(lower, "lockdown") || strings.Contains(lower, "restart") {
			return "", fmt.Errorf("forbidden mullvad operation")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmdArgs := append([]string{"exec", c.Container}, args...)
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(out)
		}
		if msg == "" {
			msg = err.Error()
		}
		if out != "" {
			return out, fmt.Errorf("%s", msg)
		}
		return "", fmt.Errorf("%s", msg)
	}
	return out, nil
}

func parseAntiMode(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "mode:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "mode:"))
		}
	}
	return strings.TrimSpace(raw)
}

func parseRelayList(raw, activeHostname string) []Relay {
	out := make([]Relay, 0, 256)
	country, countryCode, city, cityCode := "", "", "", ""
	for _, line := range strings.Split(raw, "\n") {
		if m := reCountry.FindStringSubmatch(line); len(m) == 3 && !strings.HasPrefix(line, "\t") {
			country = strings.TrimSpace(m[1])
			countryCode = m[2]
			city, cityCode = "", ""
			continue
		}
		if m := reCity.FindStringSubmatch(line); len(m) == 3 {
			city = strings.TrimSpace(m[1])
			cityCode = m[2]
			continue
		}
		if m := reHost.FindStringSubmatch(line); len(m) == 3 {
			host := m[1]
			out = append(out, Relay{
				Country:     country,
				CountryCode: countryCode,
				City:        city,
				CityCode:    cityCode,
				Hostname:    host,
				IPv4:        m[2],
				Active:      host == activeHostname,
			})
		}
	}
	return out
}
