package httpserver

import (
	"net/http"
	"time"

	"github.com/ArminDashti/arvaz-api/internal/auth"
	"github.com/ArminDashti/arvaz-api/internal/config"
	"github.com/ArminDashti/arvaz-api/internal/dockerx"
	"github.com/ArminDashti/arvaz-api/internal/fsx"
	"github.com/ArminDashti/arvaz-api/internal/metrics"
	"github.com/ArminDashti/arvaz-api/internal/softether"
	"github.com/ArminDashti/arvaz-api/internal/store"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v4/net"
)

type Server struct {
	cfg       config.Config
	metrics   *metrics.Collector
	docker    *dockerx.Client
	softether *softether.Client
	store     *store.Store
	fs        *fsx.Client
	auth      *auth.Service
}

func New(cfg config.Config, m *metrics.Collector, d *dockerx.Client, se *softether.Client, st *store.Store, fs *fsx.Client, authSvc *auth.Service) *Server {
	return &Server{cfg: cfg, metrics: m, docker: d, softether: se, store: st, fs: fs, auth: authSvc}
}

func (s *Server) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())
	r.Use(cors.New(cors.Config{
		AllowOrigins:     s.cfg.CORSOrigins,
		AllowMethods:     []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	v1 := r.Group("/api/v1")
	{
		v1.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
		v1.POST("/auth/login", s.postLogin)
	}

	protected := v1.Group("")
	protected.Use(jwtMiddleware(s.auth))
	{
		protected.GET("/dashboard", s.getDashboard)
		protected.GET("/server", s.getServer)
		protected.GET("/monitoring", s.getMonitoring)
		protected.GET("/cpu", s.getCPU)
		protected.GET("/memory", s.getMemory)
		protected.GET("/disk", s.getDisk)
		protected.GET("/bandwidth", s.getBandwidth)
		protected.GET("/docker/overview", s.getDockerOverview)
		protected.GET("/docker/containers", s.getDockerContainers)
		protected.GET("/docker/images", s.getDockerImages)
		protected.GET("/directories", s.getDirectories)
		protected.POST("/directories/mkdir", s.postDirectoriesMkdir)
		protected.POST("/directories/upload", s.postDirectoriesUpload)
		protected.DELETE("/directories", s.deleteDirectories)
		protected.GET("/softether/overview", s.getSoftEtherOverview)
		protected.GET("/softether/sessions", s.getSoftEtherSessions)
		protected.GET("/softether/users", s.getSoftEtherUsers)
		protected.GET("/softether/users/logs", s.getSoftEtherUserLogs)
		protected.GET("/softether/stats", s.getSoftEtherStats)
	}
	return r
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (s *Server) postLogin(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	resp, err := s.auth.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		if err == auth.ErrInvalidCredentials {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) getDashboard(c *gin.Context) {
	snap := s.metrics.Snapshot()
	diskUsed, diskTotal := uint64(0), uint64(0)
	for _, d := range snap.Disks {
		diskUsed += d.UsedBytes
		diskTotal += d.TotalBytes
	}
	onlineCount := 0
	runningContainers := 0
	if s.docker != nil {
		if ov, err := s.docker.Overview(c.Request.Context()); err == nil {
			if v, ok := ov["runningCount"].(int); ok {
				runningContainers = v
			}
		}
	}
	if sessions, err := s.softether.ListOnlineSessions(c.Request.Context()); err == nil {
		onlineCount = len(sessions)
	}
	c.JSON(http.StatusOK, gin.H{
		"cpuPercent":           snap.CPUPercent,
		"memoryUsedBytes":      snap.MemoryUsedBytes,
		"memoryTotalBytes":     snap.MemoryTotalBytes,
		"memoryUsedPercent":    snap.MemoryUsedPercent,
		"diskUsedBytes":        diskUsed,
		"diskTotalBytes":       diskTotal,
		"bandwidthRxBps":       snap.BandwidthRxBps,
		"bandwidthTxBps":       snap.BandwidthTxBps,
		"uptimeSeconds":        snap.UptimeSeconds,
		"publicIp":             s.cfg.PublicIP,
		"dockerRunningCount":   runningContainers,
		"softetherOnlineCount": onlineCount,
		"collectedAt":          snap.CollectedAt,
	})
}

func (s *Server) getServer(c *gin.Context) {
	snap := s.metrics.Snapshot()
	ifaces, _ := net.Interfaces()
	type ifaceView struct {
		Name        string   `json:"name"`
		Addresses   []string `json:"addresses"`
		BytesRecv   uint64   `json:"bytesRecv"`
		BytesSent   uint64   `json:"bytesSent"`
	}
	counters, _ := net.IOCounters(true)
	counterByName := map[string]net.IOCountersStat{}
	for _, counter := range counters {
		counterByName[counter.Name] = counter
	}
	network := make([]ifaceView, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs := make([]string, 0, len(iface.Addrs))
		for _, a := range iface.Addrs {
			addrs = append(addrs, a.Addr)
		}
		view := ifaceView{Name: iface.Name, Addresses: addrs}
		if counter, ok := counterByName[iface.Name]; ok {
			view.BytesRecv = counter.BytesRecv
			view.BytesSent = counter.BytesSent
		}
		network = append(network, view)
	}
	c.JSON(http.StatusOK, gin.H{
		"cpuPercent":           snap.CPUPercent,
		"cores":                snap.Cores,
		"memoryUsedBytes":      snap.MemoryUsedBytes,
		"memoryTotalBytes":     snap.MemoryTotalBytes,
		"memoryUsedPercent":    snap.MemoryUsedPercent,
		"publicIp":             s.cfg.PublicIP,
		"uptimeSeconds":        snap.UptimeSeconds,
		"network":              network,
		"disks":                snap.Disks,
		"bandwidthRxBps":       snap.BandwidthRxBps,
		"bandwidthTxBps":       snap.BandwidthTxBps,
		"collectedAt":          snap.CollectedAt,
	})
}

func (s *Server) getMonitoring(c *gin.Context) {
	snap := s.metrics.Snapshot()
	apps := []any{}
	if s.docker != nil {
		containers, err := s.docker.ListContainersFlat(c.Request.Context())
		if err == nil {
			for _, ctn := range containers {
				apps = append(apps, gin.H{
					"containerName": ctn.ContainerName,
					"cpuPercent":    ctn.CPUPercent,
					"memoryBytes":   ctn.MemoryBytes,
					"state":         ctn.State,
				})
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"cpuPercent":  snap.CPUPercent,
		"cpuSeries":   s.metrics.CPUHistory(),
		"memoryUsedPercent": snap.MemoryUsedPercent,
		"memorySeries": s.metrics.MemoryHistory(),
		"apps":        apps,
		"collectedAt": snap.CollectedAt,
	})
}

func (s *Server) getCPU(c *gin.Context) {
	snap := s.metrics.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"cpuPercent":  snap.CPUPercent,
		"cores":       snap.Cores,
		"series":      s.metrics.CPUHistory(),
		"collectedAt": snap.CollectedAt,
	})
}

func (s *Server) getMemory(c *gin.Context) {
	snap := s.metrics.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"memoryTotalBytes":     snap.MemoryTotalBytes,
		"memoryUsedBytes":      snap.MemoryUsedBytes,
		"memoryAvailableBytes": snap.MemoryAvailableBytes,
		"memoryUsedPercent":    snap.MemoryUsedPercent,
		"series":               s.metrics.MemoryHistory(),
		"collectedAt":          snap.CollectedAt,
	})
}

func (s *Server) getDisk(c *gin.Context) {
	snap := s.metrics.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"disks":       snap.Disks,
		"collectedAt": snap.CollectedAt,
	})
}

func (s *Server) getBandwidth(c *gin.Context) {
	snap := s.metrics.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"bandwidthRxBps": snap.BandwidthRxBps,
		"bandwidthTxBps": snap.BandwidthTxBps,
		"series":         s.metrics.BandwidthHistory(),
		"collectedAt":    snap.CollectedAt,
	})
}

func (s *Server) getDockerOverview(c *gin.Context) {
	if s.docker == nil {
		c.JSON(http.StatusOK, gin.H{"error": "docker unavailable"})
		return
	}
	ov, err := s.docker.Overview(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, ov)
}

func (s *Server) getDockerImages(c *gin.Context) {
	if s.docker == nil {
		c.JSON(http.StatusOK, gin.H{"images": []any{}, "error": "docker unavailable"})
		return
	}
	imgs, err := s.docker.ListImages(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"images": []any{}, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"images": imgs})
}

func (s *Server) getDockerContainers(c *gin.Context) {
	if s.docker == nil {
		c.JSON(http.StatusOK, gin.H{"containers": []any{}, "error": "docker unavailable"})
		return
	}
	containers, err := s.docker.ListContainersFlat(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"containers": []any{}, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"containers": containers})
}

func (s *Server) getDirectories(c *gin.Context) {
	if s.fs == nil {
		c.JSON(http.StatusOK, gin.H{"entries": []any{}, "error": "filesystem unavailable"})
		return
	}
	entries, path, err := s.fs.List(c.Request.Context(), c.Query("path"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"entries": []any{}, "path": c.Query("path"), "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries, "path": path})
}

type mkdirRequest struct {
	Path string `json:"path"`
	Name string `json:"name" binding:"required"`
}

func (s *Server) postDirectoriesMkdir(c *gin.Context) {
	if s.fs == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "filesystem unavailable"})
		return
	}
	var req mkdirRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := s.fs.Mkdir(c.Request.Context(), req.Path, req.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) postDirectoriesUpload(c *gin.Context) {
	if s.fs == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "filesystem unavailable"})
		return
	}
	path := c.PostForm("path")
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file required"})
		return
	}
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	defer src.Close()
	if err := s.fs.Upload(c.Request.Context(), path, file.Filename, src); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) deleteDirectories(c *gin.Context) {
	if s.fs == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "filesystem unavailable"})
		return
	}
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path required"})
		return
	}
	if err := s.fs.Delete(c.Request.Context(), path); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) getSoftEtherOverview(c *gin.Context) {
	sessions, err := s.softether.ListOnlineSessions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}
	ov, _ := s.softether.HubOverview(c.Request.Context(), len(sessions))
	c.JSON(http.StatusOK, gin.H{"overview": ov, "onlineCount": len(sessions)})
}

func (s *Server) getSoftEtherSessions(c *gin.Context) {
	sessions, err := s.softether.ListOnlineSessions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"sessions": []any{}, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

func (s *Server) getSoftEtherUsers(c *gin.Context) {
	if s.store == nil {
		c.JSON(http.StatusOK, gin.H{"users": []any{}, "error": "database unavailable"})
		return
	}
	users, err := s.store.ListUserStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"users": []any{}, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

func (s *Server) getSoftEtherUserLogs(c *gin.Context) {
	if s.store == nil {
		c.JSON(http.StatusOK, gin.H{"logs": []any{}, "error": "database unavailable"})
		return
	}
	logs, err := s.store.ListAllSessionLogs(c.Request.Context(), 500)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"logs": []any{}, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

func (s *Server) getSoftEtherStats(c *gin.Context) {
	sessions, _ := s.softether.ListOnlineSessions(c.Request.Context())
	if s.store == nil {
		c.JSON(http.StatusOK, gin.H{"stats": gin.H{"onlineCount": len(sessions)}, "error": "database unavailable"})
		return
	}
	stats, err := s.store.SoftEtherStats(c.Request.Context(), len(sessions))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"stats": stats})
}
