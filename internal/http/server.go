package httpserver

import (
	"net/http"
	"strings"
	"time"

	"github.com/ArminDashti/arvaz-api/internal/asn"
	"github.com/ArminDashti/arvaz-api/internal/auth"
	"github.com/ArminDashti/arvaz-api/internal/config"
	"github.com/ArminDashti/arvaz-api/internal/dockerx"
	"github.com/ArminDashti/arvaz-api/internal/mullvad"
	"github.com/ArminDashti/arvaz-api/internal/softether"
	"github.com/ArminDashti/arvaz-api/internal/store"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

type Server struct {
	cfg       config.Config
	docker    *dockerx.Client
	mullvad   *mullvad.Client
	softether *softether.Client
	auth      *auth.Service
	store     *store.Store
}

func New(cfg config.Config, d *dockerx.Client, mv *mullvad.Client, se *softether.Client, authSvc *auth.Service, st *store.Store) *Server {
	return &Server{cfg: cfg, docker: d, mullvad: mv, softether: se, auth: authSvc, store: st}
}

func (s *Server) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())
	r.Use(cors.New(cors.Config{
		AllowOrigins:     s.cfg.CORSOrigins,
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
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
		protected.GET("/docker/containers", s.getDockerContainers)
		protected.GET("/mullvad/status", s.getMullvadStatus)
		protected.GET("/mullvad/relays", s.getMullvadRelays)
		protected.POST("/mullvad/relay", s.postMullvadRelay)
		protected.GET("/mullvad/anti-censorship", s.getMullvadAnti)
		protected.POST("/mullvad/anti-censorship", s.postMullvadAnti)
		protected.POST("/mullvad/ping", s.postMullvadPing)
		protected.POST("/mullvad/speedtest", s.postMullvadSpeedtest)
		protected.GET("/softether/sessions", s.getSoftEtherSessions)
		protected.GET("/softether/users", s.getSoftEtherUsers)
		protected.GET("/softether/users/:username/sessions", s.getSoftEtherUserSessions)
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

func (s *Server) getDockerContainers(c *gin.Context) {
	if s.docker == nil {
		c.JSON(http.StatusOK, gin.H{"containers": []any{}, "error": "docker unavailable"})
		return
	}
	containers, err := s.docker.ListContainers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"containers": []any{}, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"containers": containers})
}

func (s *Server) getMullvadStatus(c *gin.Context) {
	st, err := s.mullvad.Status(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": st})
}

func (s *Server) getMullvadRelays(c *gin.Context) {
	relays, err := s.mullvad.ListRelays(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"relays": []any{}, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"relays": relays})
}

func (s *Server) postMullvadRelay(c *gin.Context) {
	var req struct {
		Country  string `json:"country" binding:"required"`
		City     string `json:"city"`
		Hostname string `json:"hostname"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := s.mullvad.SetRelay(c.Request.Context(), req.Country, req.City, req.Hostname); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) getMullvadAnti(c *gin.Context) {
	mode, err := s.mullvad.GetAntiCensorship(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"mode": mode})
}

func (s *Server) postMullvadAnti(c *gin.Context) {
	var req struct {
		Mode string `json:"mode" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := s.mullvad.SetAntiCensorship(c.Request.Context(), req.Mode); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) postMullvadPing(c *gin.Context) {
	var req struct {
		Target string `json:"target"`
		Count  int    `json:"count"`
	}
	_ = c.ShouldBindJSON(&req)
	res, err := s.mullvad.Ping(c.Request.Context(), req.Target, req.Count)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": res})
}

func (s *Server) postMullvadSpeedtest(c *gin.Context) {
	var req struct {
		Mode string `json:"mode"`
	}
	_ = c.ShouldBindJSON(&req)
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "parallel"
	}
	res, err := s.mullvad.Speedtest(c.Request.Context(), mode)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": res})
}

func (s *Server) getSoftEtherSessions(c *gin.Context) {
	if s.softether == nil {
		c.JSON(http.StatusOK, gin.H{"sessions": []any{}, "error": "softether unavailable"})
		return
	}
	sessions, err := s.softether.ListOnlineSessions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"sessions": []any{}, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

func (s *Server) getSoftEtherUsers(c *gin.Context) {
	if s.softether == nil {
		c.JSON(http.StatusOK, gin.H{"users": []any{}, "error": "softether unavailable"})
		return
	}
	users, err := s.softether.ListUsers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"users": []any{}, "error": err.Error()})
		return
	}

	stats := map[string]store.UserStat{}
	if s.store != nil {
		if m, err := s.store.GetUserStatMap(c.Request.Context()); err == nil {
			stats = m
		}
	}

	out := make([]softether.HubUser, 0, len(users))
	for _, u := range users {
		row := u
		row.LastISP = asn.OrgName(row.LastISP)
		if st, ok := stats[u.Username]; ok {
			if row.DownloadBytes == 0 && row.UploadBytes == 0 {
				row.DownloadBytes = st.DownloadBytes
				row.UploadBytes = st.UploadBytes
			}
			if row.LastIP == "" {
				row.LastIP = st.ClientIP
			}
			if row.LastISP == "" {
				row.LastISP = st.ISP
			}
		}
		// Prefer live public IP from online enrichment when stats still hold a private bridge IP.
		if row.LastIP != "" && strings.HasPrefix(row.LastIP, "172.") {
			row.LastIP = ""
		}
		out = append(out, row)
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

func (s *Server) getSoftEtherUserSessions(c *gin.Context) {
	if s.store == nil {
		c.JSON(http.StatusOK, gin.H{"sessions": []any{}, "error": "store unavailable"})
		return
	}
	username := strings.TrimSpace(c.Param("username"))
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username required"})
		return
	}
	sessions, err := s.store.ListSessionsByUsername(c.Request.Context(), username)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"sessions": []any{}, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"username": username, "sessions": sessions})
}
