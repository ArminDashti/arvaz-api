package dockerx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ContainerInfo struct {
	StackName     string  `json:"stackName"`
	ContainerName string  `json:"containerName"`
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryBytes   uint64  `json:"memoryBytes"`
	MemoryGB      float64 `json:"memoryGb"`
	Network       string  `json:"network"`
	HAProxyURL    string  `json:"haproxyUrl"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
	State         string  `json:"state"`
}

type Client struct {
	PublicIP          string
	HAProxyConfigPath string
}

func New(publicIP, haproxyConfigPath string) (*Client, error) {
	if err := exec.Command("docker", "version").Run(); err != nil {
		return nil, fmt.Errorf("docker unavailable: %w", err)
	}
	return &Client{PublicIP: publicIP, HAProxyConfigPath: haproxyConfigPath}, nil
}

func (c *Client) Close() error { return nil }

func (c *Client) ListContainers(ctx context.Context) ([]ContainerInfo, error) {
	idsOut, err := runDocker(ctx, "ps", "-aq")
	if err != nil {
		return nil, err
	}
	ids := splitLines(idsOut)
	if len(ids) == 0 {
		return []ContainerInfo{}, nil
	}

	args := append([]string{"inspect"}, ids...)
	raw, err := runDocker(ctx, args...)
	if err != nil {
		return nil, err
	}

	var inspected []inspectRow
	if err := json.Unmarshal([]byte(raw), &inspected); err != nil {
		return nil, fmt.Errorf("docker inspect parse: %w", err)
	}

	statsByName := collectAllStats(ctx)
	routes := ParseHAProxyRoutes(c.HAProxyConfigPath)
	now := time.Now().UTC()

	result := make([]ContainerInfo, 0, len(inspected))
	for _, row := range inspected {
		name := strings.TrimPrefix(row.Name, "/")
		stack := row.Config.Labels["com.docker.compose.project"]
		if stack == "" {
			stack = row.Config.Labels["com.docker.stack.namespace"]
		}
		if stack == "" {
			stack = "standalone"
		}

		state := row.State.Status
		uptime := int64(0)
		if row.State.Running {
			if t, err := time.Parse(time.RFC3339Nano, row.State.StartedAt); err == nil {
				uptime = int64(now.Sub(t.UTC()).Seconds())
			}
		}

		cpu := 0.0
		mem := uint64(0)
		if s, ok := statsByName[name]; ok {
			cpu = s.cpu
			mem = s.mem
		}

		network := formatNetworks(row.NetworkSettings.Networks)
		haproxy := routes[name]
		if haproxy == "" {
			haproxy = publishedHostPort(row.NetworkSettings.Ports, c.PublicIP)
		}
		if haproxy == "" {
			haproxy = "-"
		}

		result = append(result, ContainerInfo{
			StackName:     stack,
			ContainerName: name,
			CPUPercent:    round2(cpu),
			MemoryBytes:   mem,
			MemoryGB:      round2(float64(mem) / (1024 * 1024 * 1024)),
			Network:       network,
			HAProxyURL:    haproxy,
			UptimeSeconds: uptime,
			State:         state,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].StackName == result[j].StackName {
			return result[i].ContainerName < result[j].ContainerName
		}
		return result[i].StackName < result[j].StackName
	})
	return result, nil
}

type inspectRow struct {
	Name            string `json:"Name"`
	Config          struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status    string `json:"Status"`
		Running   bool   `json:"Running"`
		StartedAt string `json:"StartedAt"`
	} `json:"State"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

type containerStat struct {
	cpu float64
	mem uint64
}

func collectAllStats(ctx context.Context) map[string]containerStat {
	out, err := runDocker(ctx, "stats", "--no-stream", "--format", "{{json .}}")
	if err != nil {
		return map[string]containerStat{}
	}
	result := map[string]containerStat{}
	for _, line := range splitLines(out) {
		var row struct {
			Name     string `json:"Name"`
			MemUsage string `json:"MemUsage"`
			CPUPerc  string `json:"CPUPerc"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		cpu, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(row.CPUPerc), "%"), 64)
		result[row.Name] = containerStat{cpu: cpu, mem: parseMemUsage(row.MemUsage)}
	}
	return result
}

func formatNetworks(networks map[string]struct {
	IPAddress string `json:"IPAddress"`
}) string {
	if len(networks) == 0 {
		return "-"
	}
	names := make([]string, 0, len(networks))
	for name := range networks {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		ip := strings.TrimSpace(networks[name].IPAddress)
		if ip == "" {
			parts = append(parts, name)
			continue
		}
		parts = append(parts, name+" "+ip)
	}
	return strings.Join(parts, ", ")
}

func publishedHostPort(ports map[string][]struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}, publicIP string) string {
	for _, bindings := range ports {
		for _, b := range bindings {
			if b.HostPort == "" {
				continue
			}
			host := b.HostIP
			if host == "" || host == "0.0.0.0" || host == "::" {
				host = publicIP
			}
			return host + ":" + b.HostPort
		}
	}
	return ""
}

func runDocker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return stdout.String(), nil
}

func splitLines(s string) []string {
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func parseMemUsage(s string) uint64 {
	part := strings.Split(s, "/")[0]
	part = strings.TrimSpace(part)
	part = strings.ToUpper(part)
	mult := float64(1)
	switch {
	case strings.HasSuffix(part, "GIB"):
		mult = 1 << 30
		part = strings.TrimSuffix(part, "GIB")
	case strings.HasSuffix(part, "MIB"):
		mult = 1 << 20
		part = strings.TrimSuffix(part, "MIB")
	case strings.HasSuffix(part, "KIB"):
		mult = 1 << 10
		part = strings.TrimSuffix(part, "KIB")
	case strings.HasSuffix(part, "GB"):
		mult = 1e9
		part = strings.TrimSuffix(part, "GB")
	case strings.HasSuffix(part, "MB"):
		mult = 1e6
		part = strings.TrimSuffix(part, "MB")
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(part), 64)
	return uint64(v * mult)
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
