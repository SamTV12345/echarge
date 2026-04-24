// Package osrm is a tiny HTTP client for the OSRM routing service.
// It returns the driving route between two WGS84 coordinates as a list of
// [lat, lng] vertices plus total distance and duration.
//
// The public demo at router.project-osrm.org is rate-limited and "not for
// production" — fine for a personal app, swap to a self-hosted OSRM for
// real use.
package osrm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Client talks to one OSRM HTTP endpoint.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a client against the given base URL (no trailing slash),
// e.g. "https://router.project-osrm.org". httpClient may be nil to use a
// reasonable default with a 15s timeout.
func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{baseURL: baseURL, http: httpClient}
}

// Route is one driving route between start and end.
type Route struct {
	// Polyline is an ordered list of [lat, lng] vertices along the road.
	Polyline [][2]float64 `json:"polyline"`
	// DistanceKm is the total driving distance.
	DistanceKm float64 `json:"distanceKm"`
	// DurationMin is the estimated driving time in minutes, excluding traffic.
	DurationMin float64 `json:"durationMin"`
}

// ErrNoRoute is returned when OSRM cannot find a route between the points.
var ErrNoRoute = errors.New("osrm: no route")

// Driving computes a car route between two coordinates.
// Coordinates are in lat,lng order.
func (c *Client) Driving(ctx context.Context, start, end [2]float64) (*Route, error) {
	// OSRM path coords are lng,lat.
	url := fmt.Sprintf(
		"%s/route/v1/driving/%f,%f;%f,%f?overview=full&geometries=geojson&steps=false",
		c.baseURL,
		start[1], start[0], end[1], end[0],
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("osrm: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("osrm: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osrm: HTTP %d", resp.StatusCode)
	}

	var body struct {
		Code   string `json:"code"`
		Routes []struct {
			Distance float64 `json:"distance"` // meters
			Duration float64 `json:"duration"` // seconds
			Geometry struct {
				Coordinates [][2]float64 `json:"coordinates"` // lng,lat
			} `json:"geometry"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("osrm: decode: %w", err)
	}
	if body.Code != "Ok" || len(body.Routes) == 0 {
		return nil, ErrNoRoute
	}

	r := body.Routes[0]
	poly := make([][2]float64, len(r.Geometry.Coordinates))
	for i, c := range r.Geometry.Coordinates {
		// Flip to lat,lng.
		poly[i] = [2]float64{c[1], c[0]}
	}
	return &Route{
		Polyline:    poly,
		DistanceKm:  r.Distance / 1000,
		DurationMin: r.Duration / 60,
	}, nil
}
