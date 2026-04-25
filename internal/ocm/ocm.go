// Package ocm is a tiny HTTP client for the Open Charge Map REST API.
// It fetches the live-ish status of a charging station so we can show
// "available / in use / not operational" in the UI — alongside the static
// Bundesnetzagentur registry data which has no real-time information.
//
// OCM data is community-maintained. "Live" is best-effort: some stations
// are reported minutely by aggregator apps, many sit hours or days old.
// The DateLastStatusUpdate field is what we expose to the frontend so users
// can judge freshness for themselves.
package ocm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrAuthRequired is returned when OCM rejects the request because no API key
// (or an invalid one) was provided. The frontend uses this to show a key-setup
// hint instead of a generic "not reachable" message.
var ErrAuthRequired = errors.New("ocm: API key required")

// Client is a thread-safe wrapper around the OCM /poi endpoint with a small
// in-memory result cache (5 min TTL) to stay well within the free-tier limits.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client

	mu    sync.Mutex
	cache map[string]cacheItem
	ttl   time.Duration
}

// New returns a client. apiKey may be empty; OCM works without one but
// rate-limits more aggressively. baseURL defaults to the official endpoint.
func New(baseURL, apiKey string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = "https://api.openchargemap.io/v3"
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 8 * time.Second}
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    httpClient,
		cache:   make(map[string]cacheItem),
		ttl:     5 * time.Minute,
	}
}

// POI is one Open Charge Map record reduced to the fields we actually use.
type POI struct {
	Title         string     `json:"title,omitempty"`
	OperatorTitle string     `json:"operator,omitempty"`
	StatusTitle   string     `json:"status,omitempty"` // "Operational", "Currently In Use", "Not Operational", …
	IsOperational *bool      `json:"isOperational,omitempty"`
	LastUpdated   *time.Time `json:"lastUpdated,omitempty"`
	DistanceKm    float64    `json:"distanceKm"`
	Latitude      float64    `json:"lat"`
	Longitude     float64    `json:"lng"`
}

type cacheItem struct {
	pois    []POI
	expires time.Time
}

// NearbyPOIs returns up to `max` charging stations within `radiusKm` of the
// given coordinates, sorted by distance ascending.
func (c *Client) NearbyPOIs(ctx context.Context, lat, lng, radiusKm float64, max int) ([]POI, error) {
	key := fmt.Sprintf("%.5f,%.5f,%.2f,%d", lat, lng, radiusKm, max)
	if cached, ok := c.cacheGet(key); ok {
		return cached, nil
	}

	q := url.Values{}
	q.Set("output", "json")
	q.Set("latitude", fmt.Sprintf("%.6f", lat))
	q.Set("longitude", fmt.Sprintf("%.6f", lng))
	q.Set("distance", fmt.Sprintf("%g", radiusKm))
	q.Set("distanceunit", "KM")
	q.Set("maxresults", fmt.Sprintf("%d", max))
	q.Set("compact", "true")
	q.Set("verbose", "false")
	if c.apiKey != "" {
		q.Set("key", c.apiKey)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/poi/?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("ocm: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "echarge/1.0 (+github.com/SamTV12345/echarge)")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ocm: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			return nil, ErrAuthRequired
		}
		return nil, fmt.Errorf("ocm: HTTP %d", resp.StatusCode)
	}

	var raw []struct {
		AddressInfo struct {
			Latitude  float64 `json:"Latitude"`
			Longitude float64 `json:"Longitude"`
			Distance  float64 `json:"Distance"`
		} `json:"AddressInfo"`
		OperatorInfo *struct {
			Title string `json:"Title"`
		} `json:"OperatorInfo"`
		StatusType *struct {
			Title         string `json:"Title"`
			IsOperational *bool  `json:"IsOperational"`
		} `json:"StatusType"`
		DateLastStatusUpdate string `json:"DateLastStatusUpdate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("ocm: decode: %w", err)
	}

	out := make([]POI, 0, len(raw))
	for _, r := range raw {
		p := POI{
			DistanceKm: r.AddressInfo.Distance,
			Latitude:   r.AddressInfo.Latitude,
			Longitude:  r.AddressInfo.Longitude,
		}
		if r.OperatorInfo != nil {
			p.OperatorTitle = r.OperatorInfo.Title
		}
		if r.StatusType != nil {
			p.StatusTitle = r.StatusType.Title
			p.IsOperational = r.StatusType.IsOperational
		}
		if r.DateLastStatusUpdate != "" {
			if t, err := time.Parse(time.RFC3339, r.DateLastStatusUpdate); err == nil {
				p.LastUpdated = &t
			}
		}
		out = append(out, p)
	}

	c.cacheSet(key, out)
	return out, nil
}

func (c *Client) cacheGet(key string) ([]POI, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.cache[key]
	if !ok || time.Now().After(it.expires) {
		return nil, false
	}
	return it.pois, true
}

func (c *Client) cacheSet(key string, pois []POI) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Defensive cap so a long-running process doesn't grow the cache forever.
	if len(c.cache) > 500 {
		c.cache = make(map[string]cacheItem)
	}
	c.cache[key] = cacheItem{pois: pois, expires: time.Now().Add(c.ttl)}
}
