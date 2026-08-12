package dockerx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type ImageInfo struct {
	ID        string    `json:"id"`
	Tags      []string  `json:"tags"`
	SizeBytes int64     `json:"sizeBytes"`
	CreatedAt time.Time `json:"createdAt"`
}

type ContainerInfo struct {
	ContainerName     string  `json:"containerName"`
	Image             string  `json:"image"`
	UptimeSeconds     int64   `json:"uptimeSeconds"`
	MemoryBytes       uint64  `json:"memoryBytes"`
	DiskBytes         uint64  `json:"diskBytes"`
	CPUPercent        float64 `json:"cpuPercent"`
	InternalEndpoint  string  `json:"internalEndpoint"`
	ReverseProxyRoute string  `json:"reverseProxyRoute"`
	StackName         string  `json:"stackName"`
	State             string  `json:"state"`
}

type StackGroup struct {
	StackName  string          `json:"stackName"`
	Containers []ContainerInfo `json:"containers"`
}

type Client struct{}

func New() (*Client, error) {
	if err := exec.Command("docker", "version").Run(); err != nil {
		return nil, fmt.Errorf("docker unavailable: %w", err)
	}
	return &Client{}, nil
}

func (c *Client) Close() error { return nil }

func (c *Client) ListImages(ctx context.Context) ([]ImageInfo, error) {
	out, err := runDocker(ctx, "images", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	lines := splitLines(out)
	result := make([]ImageInfo, 0, len(lines))
	for _, line := range lines {
		var row struct {
			ID         string `json:"ID"`
			Repository string `json:"Repository"`
			Tag        string `json:"Tag"`
			Size       string `json:"Size"`
			CreatedAt  string `json:"CreatedAt"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		tag := row.Repository
		if row.Tag != "" {
			tag = row.Repository + ":" + row.Tag
		}
		result = append(result, ImageInfo{
			ID:        row.ID,
			Tags:      []string{tag},
			SizeBytes: parseDockerSize(row.Size),
			CreatedAt: parseDockerTime(row.CreatedAt),
		})
	}
	return result, nil
}

func (c *Client) ListContainersByStack(ctx context.Context) ([]StackGroup, error) {
	out, err := runDocker(ctx, "ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	lines := splitLines(out)
	groups := map[string][]ContainerInfo{}
	order := []string{}

	for _, line := range lines {
		var row struct {
			ID      string `json:"ID"`
			Names   string `json:"Names"`
			Image   string `json:"Image"`
			Status  string `json:"Status"`
			State   string `json:"State"`
			Ports   string `json:"Ports"`
			Labels  string `json:"Labels"`
			RunningFor string `json:"RunningFor"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		labels := parseLabels(row.Labels)
		stack := labels["com.docker.compose.project"]
		if stack == "" {
			stack = labels["com.docker.stack.namespace"]
		}
		if stack == "" {
			stack = "standalone"
		}
		route := firstNonEmpty(labels["arvaz.proxy.host"], labels["arvaz.reverse_proxy"], labels["haproxy.host"])

		memBytes, cpuPct := containerStats(ctx, row.ID)
		info := ContainerInfo{
			ContainerName:     row.Names,
			Image:             row.Image,
			UptimeSeconds:     parseRunningFor(row.RunningFor, row.Status),
			MemoryBytes:       memBytes,
			DiskBytes:         0,
			CPUPercent:        cpuPct,
			InternalEndpoint:  firstPort(row.Ports),
			ReverseProxyRoute: route,
			StackName:         stack,
			State:             row.State,
		}
		if _, ok := groups[stack]; !ok {
			order = append(order, stack)
		}
		groups[stack] = append(groups[stack], info)
	}

	outGroups := make([]StackGroup, 0, len(order))
	for _, name := range order {
		outGroups = append(outGroups, StackGroup{StackName: name, Containers: groups[name]})
	}
	return outGroups, nil
}

func (c *Client) ListContainersFlat(ctx context.Context) ([]ContainerInfo, error) {
	stacks, err := c.ListContainersByStack(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ContainerInfo, 0)
	for _, stack := range stacks {
		for _, container := range stack.Containers {
			out = append(out, container)
		}
	}
	return out, nil
}

func (c *Client) Overview(ctx context.Context) (map[string]any, error) {
	containers, err := c.ListContainersFlat(ctx)
	if err != nil {
		return nil, err
	}
	images, err := c.ListImages(ctx)
	if err != nil {
		return nil, err
	}
	running, stopped := 0, 0
	for _, ctn := range containers {
		if strings.EqualFold(ctn.State, "running") {
			running++
		} else {
			stopped++
		}
	}
	var totalImageSize int64
	for _, img := range images {
		totalImageSize += img.SizeBytes
	}
	return map[string]any{
		"runningCount":     running,
		"stoppedCount":     stopped,
		"imagesCount":      len(images),
		"totalImageSizeBytes": totalImageSize,
		"containersCount":  len(containers),
	}, nil
}

func containerStats(ctx context.Context, id string) (uint64, float64) {
	out, err := runDocker(ctx, "stats", "--no-stream", "--format", "{{json .}}", id)
	if err != nil {
		return 0, 0
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return 0, 0
	}
	var row struct {
		MemUsage string `json:"MemUsage"`
		CPUPerc  string `json:"CPUPerc"`
	}
	if err := json.Unmarshal([]byte(line), &row); err != nil {
		return 0, 0
	}
	mem := parseMemUsage(row.MemUsage)
	cpu, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(row.CPUPerc), "%"), 64)
	return mem, cpu
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
		return "", fmt.Errorf(msg)
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

func parseLabels(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			out[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return out
}

func firstPort(ports string) string {
	ports = strings.TrimSpace(ports)
	if ports == "" {
		return ""
	}
	return strings.Split(ports, ", ")[0]
}

func parseRunningFor(runningFor, status string) int64 {
	src := runningFor
	if src == "" {
		src = status
	}
	src = strings.ToLower(src)
	src = strings.TrimPrefix(src, "up ")
	// best-effort; leave 0 if unknown
	_ = src
	return 0
}

func parseDockerSize(s string) int64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	mult := float64(1)
	switch {
	case strings.HasSuffix(s, "GB"):
		mult = 1 << 30
		s = strings.TrimSuffix(s, "GB")
	case strings.HasSuffix(s, "MB"):
		mult = 1 << 20
		s = strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "KB"):
		mult = 1 << 10
		s = strings.TrimSuffix(s, "KB")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return int64(v * mult)
}

func parseMemUsage(s string) uint64 {
	// "123.4MiB / 7.8GiB"
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

func parseDockerTime(s string) time.Time {
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05 -0700 MSK",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
