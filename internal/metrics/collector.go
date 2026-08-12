package metrics

import (
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

type CoreSample struct {
	CoreIndex     int     `json:"coreIndex"`
	UsagePercent  float64 `json:"usagePercent"`
}

type DiskSample struct {
	MountPoint  string  `json:"mountPoint"`
	Fstype      string  `json:"fstype,omitempty"`
	TotalBytes  uint64  `json:"totalBytes"`
	UsedBytes   uint64  `json:"usedBytes"`
	FreeBytes   uint64  `json:"freeBytes"`
	UsedPercent float64 `json:"usedPercent"`
}

type Point struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

type BandwidthPoint struct {
	Timestamp time.Time `json:"timestamp"`
	RxBps     float64   `json:"rxBps"`
	TxBps     float64   `json:"txBps"`
}

type Snapshot struct {
	CPUPercent           float64       `json:"cpuPercent"`
	Cores                []CoreSample  `json:"cores"`
	MemoryTotalBytes     uint64        `json:"memoryTotalBytes"`
	MemoryUsedBytes      uint64        `json:"memoryUsedBytes"`
	MemoryAvailableBytes uint64        `json:"memoryAvailableBytes"`
	MemoryUsedPercent    float64       `json:"memoryUsedPercent"`
	Disks                []DiskSample  `json:"disks"`
	BandwidthRxBps       float64       `json:"bandwidthRxBps"`
	BandwidthTxBps       float64       `json:"bandwidthTxBps"`
	UptimeSeconds        uint64        `json:"uptimeSeconds"`
	CollectedAt          time.Time     `json:"collectedAt"`
}

type Collector struct {
	mu            sync.RWMutex
	historySize   int
	cpuHistory    []Point
	memHistory    []Point
	bwHistory     []BandwidthPoint
	lastNet       net.IOCountersStat
	lastNetAt     time.Time
	hasLastNet    bool
	lastSnapshot  Snapshot
}

func NewCollector(historySize int) *Collector {
	if historySize < 10 {
		historySize = 10
	}
	c := &Collector{historySize: historySize}
	_ = c.Refresh()
	go c.loop()
	return c
}

func (c *Collector) loop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		_ = c.Refresh()
	}
}

func (c *Collector) Refresh() error {
	snap, err := c.collect()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastSnapshot = snap
	c.cpuHistory = appendPoint(c.cpuHistory, Point{Timestamp: snap.CollectedAt, Value: snap.CPUPercent}, c.historySize)
	c.memHistory = appendPoint(c.memHistory, Point{Timestamp: snap.CollectedAt, Value: snap.MemoryUsedPercent}, c.historySize)
	c.bwHistory = appendBW(c.bwHistory, BandwidthPoint{
		Timestamp: snap.CollectedAt,
		RxBps:     snap.BandwidthRxBps,
		TxBps:     snap.BandwidthTxBps,
	}, c.historySize)
	return nil
}

func (c *Collector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastSnapshot
}

func (c *Collector) CPUHistory() []Point {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Point(nil), c.cpuHistory...)
}

func (c *Collector) MemoryHistory() []Point {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Point(nil), c.memHistory...)
}

func (c *Collector) BandwidthHistory() []BandwidthPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]BandwidthPoint(nil), c.bwHistory...)
}

func (c *Collector) collect() (Snapshot, error) {
	now := time.Now().UTC()
	total, err := cpu.Percent(250*time.Millisecond, false)
	if err != nil {
		return Snapshot{}, err
	}
	perCore, err := cpu.Percent(0, true)
	if err != nil {
		return Snapshot{}, err
	}
	cores := make([]CoreSample, 0, len(perCore))
	for i, v := range perCore {
		cores = append(cores, CoreSample{CoreIndex: i, UsagePercent: round2(v)})
	}

	vm, err := mem.VirtualMemory()
	if err != nil {
		return Snapshot{}, err
	}

	parts, err := disk.Partitions(false)
	if err != nil {
		return Snapshot{}, err
	}
	disks := make([]DiskSample, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		if _, ok := seen[p.Mountpoint]; ok {
			continue
		}
		seen[p.Mountpoint] = struct{}{}
		u, err := disk.Usage(p.Mountpoint)
		if err != nil {
			continue
		}
		disks = append(disks, DiskSample{
			MountPoint:  p.Mountpoint,
			Fstype:      p.Fstype,
			TotalBytes:  u.Total,
			UsedBytes:   u.Used,
			FreeBytes:   u.Free,
			UsedPercent: round2(u.UsedPercent),
		})
	}

	uptime, _ := host.Uptime()
	rx, tx := c.bandwidthRates(now)

	cpuPct := 0.0
	if len(total) > 0 {
		cpuPct = round2(total[0])
	}

	return Snapshot{
		CPUPercent:           cpuPct,
		Cores:                cores,
		MemoryTotalBytes:     vm.Total,
		MemoryUsedBytes:      vm.Used,
		MemoryAvailableBytes: vm.Available,
		MemoryUsedPercent:    round2(vm.UsedPercent),
		Disks:                disks,
		BandwidthRxBps:       rx,
		BandwidthTxBps:       tx,
		UptimeSeconds:        uptime,
		CollectedAt:          now,
	}, nil
}

func (c *Collector) bandwidthRates(now time.Time) (float64, float64) {
	counters, err := net.IOCounters(false)
	if err != nil || len(counters) == 0 {
		return 0, 0
	}
	cur := counters[0]
	if !c.hasLastNet || !c.lastNetAt.Before(now) {
		c.lastNet = cur
		c.lastNetAt = now
		c.hasLastNet = true
		return 0, 0
	}
	sec := now.Sub(c.lastNetAt).Seconds()
	if sec <= 0 {
		return 0, 0
	}
	rx := float64(cur.BytesRecv-c.lastNet.BytesRecv) / sec
	tx := float64(cur.BytesSent-c.lastNet.BytesSent) / sec
	if rx < 0 {
		rx = 0
	}
	if tx < 0 {
		tx = 0
	}
	c.lastNet = cur
	c.lastNetAt = now
	return round2(rx), round2(tx)
}

func appendPoint(hist []Point, p Point, max int) []Point {
	hist = append(hist, p)
	if len(hist) > max {
		hist = hist[len(hist)-max:]
	}
	return hist
}

func appendBW(hist []BandwidthPoint, p BandwidthPoint, max int) []BandwidthPoint {
	hist = append(hist, p)
	if len(hist) > max {
		hist = hist[len(hist)-max:]
	}
	return hist
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
