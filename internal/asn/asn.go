package asn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var reASPrefix = regexp.MustCompile(`(?i)^AS\d+\s*`)

// OrgName returns an ISP/org display name with any leading ASnnnn prefix removed.
func OrgName(s string) string {
	return strings.TrimSpace(reASPrefix.ReplaceAllString(strings.TrimSpace(s), ""))
}

// Resolver returns a display label for an IP's ISP/org (empty when unknown).
type Resolver interface {
	Lookup(ip string) string
}

type NullResolver struct{}

func (NullResolver) Lookup(string) string { return "" }

type cacheEntry struct {
	label     string
	expiresAt time.Time
}

// AsipResolver looks up ASN via asip-api GET /api/v1/ip/info/{ip}.
type AsipResolver struct {
	baseURL    string
	httpClient *http.Client
	ttl        time.Duration
	mu         sync.Mutex
	cache      map[string]cacheEntry
}

type ipInfoResponse struct {
	IP      string `json:"ip"`
	ASN     int    `json:"asn"`
	AS      string `json:"as"`
	Country string `json:"country"`
}

func NewAsipResolver(baseURL string) *AsipResolver {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:3000"
	}
	return &AsipResolver{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
		ttl:   30 * time.Minute,
		cache: make(map[string]cacheEntry),
	}
}

func (r *AsipResolver) Lookup(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	r.mu.Lock()
	if e, ok := r.cache[ip]; ok && time.Now().Before(e.expiresAt) {
		label := e.label
		r.mu.Unlock()
		return label
	}
	r.mu.Unlock()

	label := r.fetch(ip)
	r.mu.Lock()
	r.cache[ip] = cacheEntry{label: label, expiresAt: time.Now().Add(r.ttl)}
	r.mu.Unlock()
	return label
}

func (r *AsipResolver) fetch(ip string) string {
	url := fmt.Sprintf("%s/api/v1/ip/info/%s", r.baseURL, ip)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	res, err := r.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ""
	}
	var body ipInfoResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return ""
	}
	name := OrgName(body.AS)
	if name == "" {
		return ""
	}
	return name
}
