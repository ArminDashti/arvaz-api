package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr               string
	DatabaseURL            string
	JWTSecret              string
	DefaultUsername        string
	DefaultPassword        string
	CORSOrigins            []string
	PublicIP               string
	HAProxyConfigPath      string
	MullvadContainer       string
	SoftEtherContainer     string
	SoftEtherPassword      string
	SoftEtherHub           string
	SoftEtherEnabled       bool
	SoftEtherVpncmdTimeout time.Duration
	SoftEtherPollEvery     time.Duration
	AsipBaseURL            string
}

func Load() Config {
	return Config{
		HTTPAddr:               getenv("HTTP_ADDR", "127.0.0.1:8090"),
		DatabaseURL:            getenv("DATABASE_URL", "postgres://arvaz:arvaz@127.0.0.1:55433/arvaz?sslmode=disable"),
		JWTSecret:              getenv("JWT_SECRET", "change-me-in-production"),
		DefaultUsername:        getenv("DEFAULT_USERNAME", "armin"),
		DefaultPassword:        getenv("DEFAULT_PASSWORD", "dopadopa1234"),
		CORSOrigins:            splitCSV(getenv("CORS_ORIGINS", "http://127.0.0.1:5173,http://localhost:5173")),
		PublicIP:               getenv("PUBLIC_IP", "2.144.27.74"),
		HAProxyConfigPath:      getenv("HAPROXY_CONFIG_PATH", "/cloud-admin/docker-volumes/reverse-proxy/haproxy/config/haproxy.cfg"),
		MullvadContainer:       getenv("MULLVAD_CONTAINER", "mullvad-1"),
		SoftEtherContainer:     getenv("SOFTETHER_CONTAINER", "softether"),
		SoftEtherPassword:      getenv("SOFTETHER_PASSWORD", "dopadopa123"),
		SoftEtherHub:           getenv("SOFTETHER_HUB", "DEFAULT"),
		SoftEtherEnabled:       getenvBool("SOFTETHER_ENABLED", true),
		SoftEtherVpncmdTimeout: time.Duration(getenvInt("SOFTETHER_VPNCMD_TIMEOUT_SECONDS", 20)) * time.Second,
		SoftEtherPollEvery:     time.Duration(getenvInt("SOFTETHER_POLL_SECONDS", 30)) * time.Second,
		AsipBaseURL:            getenv("ASIP_BASE_URL", "http://127.0.0.1:3000"),
	}
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getenvBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	return v == "1" || v == "true" || v == "yes"
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
