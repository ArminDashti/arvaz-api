package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ArminDashti/arvaz-api/internal/asn"
	"github.com/ArminDashti/arvaz-api/internal/auth"
	"github.com/ArminDashti/arvaz-api/internal/config"
	"github.com/ArminDashti/arvaz-api/internal/dockerx"
	"github.com/ArminDashti/arvaz-api/internal/haproxy"
	httpserver "github.com/ArminDashti/arvaz-api/internal/http"
	"github.com/ArminDashti/arvaz-api/internal/mullvad"
	"github.com/ArminDashti/arvaz-api/internal/softether"
	"github.com/ArminDashti/arvaz-api/internal/store"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	var dockerClient *dockerx.Client
	if dc, err := dockerx.New(cfg.PublicIP, cfg.HAProxyConfigPath); err != nil {
		log.Printf("docker client unavailable: %v", err)
	} else {
		dockerClient = dc
		defer dockerClient.Close()
	}

	mvClient := mullvad.New(cfg.MullvadContainer)
	asnResolver := asn.NewAsipResolver(cfg.AsipBaseURL)
	hapClient := haproxy.New("haproxy", "/var/lib/haproxy/admin.sock")
	seClient := softether.New(
		cfg.SoftEtherContainer,
		cfg.SoftEtherPassword,
		cfg.SoftEtherHub,
		cfg.SoftEtherEnabled,
		cfg.SoftEtherVpncmdTimeout,
		asnResolver,
		hapClient,
	)

	ctx := context.Background()
	db, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres required: %v", err)
	}
	defer db.Close()

	authSvc := auth.NewService(db, cfg.JWTSecret)
	if err := authSvc.EnsureDefaultUser(ctx, cfg.DefaultUsername, cfg.DefaultPassword); err != nil {
		log.Fatalf("seed default user: %v", err)
	}

	go pollSoftEther(cfg, seClient, db)

	srv := httpserver.New(cfg, dockerClient, mvClient, seClient, authSvc, db)
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("arvaz-api listening on http://%s", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

func pollSoftEther(cfg config.Config, se *softether.Client, db *store.Store) {
	if !cfg.SoftEtherEnabled {
		return
	}
	interval := cfg.SoftEtherPollEvery
	if interval < time.Second {
		interval = 30 * time.Second
	}
	run := func() {
		// ListOnlineSessions runs many sequential vpncmd calls (list + per-session get).
		pollTimeout := cfg.SoftEtherVpncmdTimeout*20 + 2*time.Minute
		if pollTimeout < 3*time.Minute {
			pollTimeout = 3 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
		defer cancel()
		sessions, err := se.ListOnlineSessions(ctx)
		if err != nil {
			log.Printf("softether poll: %v", err)
			return
		}
		if err := db.SyncOnlineSessions(ctx, sessions); err != nil {
			log.Printf("softether sync: %v", err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		run()
	}
}
