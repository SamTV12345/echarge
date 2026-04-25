// Package web wires the HTTP layer for the German EV charging station
// registry. It is intentionally thin: it owns routing, request parsing,
// JSON/HTML marshalling, and a small logging+recovery middleware.
// Persistence lives in echarge/internal/data, templates in
// echarge/internal/web/templates, JS bundling in echarge/internal/build.
package web

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"echarge/internal/data"
	"echarge/internal/ocm"
	"echarge/internal/route"
	"echarge/internal/web/templates"
)

// Server is the HTTP application. Construct it directly; the zero value is
// NOT usable because Store must be set. JSBundle should hold the esbuild
// output produced once at startup by echarge/internal/build.BuildJS.
// Planner is optional — when nil the /api/route endpoint returns 503.
type Server struct {
	Store    *data.Store
	JSBundle []byte
	Planner  *route.Planner
	OCM      *ocm.Client // optional; if nil, /api/stations/{id}/availability returns "no data"
}

// Handler returns the fully wired ServeMux, including logging and panic
// recovery middleware. Timeouts are the caller's responsibility — they own
// the http.Server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /assets/main.js", s.handleJSBundle)
	mux.HandleFunc("GET /assets/app.css", s.handleCSS)
	mux.HandleFunc("GET /api/stations", s.handleStations)
	mux.HandleFunc("GET /api/stations/{id}", s.handleStationByID)
	mux.HandleFunc("GET /api/stations/{id}/availability", s.handleAvailability)
	mux.HandleFunc("GET /api/betreiber", s.handleBetreiber)
	mux.HandleFunc("GET /api/suggest", s.handleSuggest)
	mux.HandleFunc("GET /api/geocode", s.handleGeocode)
	mux.HandleFunc("GET /api/route", s.handleRoute)

	return withMiddleware(mux)
}

// ---------------------------------------------------------------------------
// Route handlers
// ---------------------------------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	total, err := s.Store.Count(r.Context())
	if err != nil {
		log.Printf("index: count failed: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.Index(templates.IndexProps{TotalStations: total}).Render(r.Context(), w); err != nil {
		// Headers may already be flushed; just log.
		log.Printf("index: template render failed: %v", err)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleJSBundle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(s.JSBundle)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.JSBundle)
}

func (s *Server) handleCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(templates.AppCSS)))
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, templates.AppCSS)
}

func (s *Server) handleStations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := data.SearchParams{
		Betreiber: strings.TrimSpace(q.Get("betreiber")),
		Q:         strings.TrimSpace(q.Get("q")),
	}

	if raw := strings.TrimSpace(q.Get("bbox")); raw != "" {
		bbox, err := parseBBox(raw)
		if err != nil {
			http.Error(w, "invalid bbox: "+err.Error(), http.StatusBadRequest)
			return
		}
		params.BBox = bbox
	}

	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		params.Limit = n
	}

	// Tier codes are always lower-case on the wire.
	if tiers := splitCSV(q.Get("tiers")); len(tiers) > 0 {
		for i, t := range tiers {
			tiers[i] = strings.ToLower(t)
		}
		params.Tiers = tiers
	}
	// Plug labels are matched verbatim against the stored CSV.
	if plugs := splitCSV(q.Get("plugs")); len(plugs) > 0 {
		params.Plugs = plugs
	}
	// Status comes straight from the BNetzA registry (e.g. "In Betrieb").
	if status := splitCSV(q.Get("status")); len(status) > 0 {
		params.Status = status
	}

	stations, err := s.Store.SearchStations(r.Context(), params)
	if err != nil {
		log.Printf("stations: search failed: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	// Never emit a bare `null` for an empty list — frontend iterates it.
	if stations == nil {
		stations = []data.Station{}
	}
	writeJSON(w, http.StatusOK, stations)
}

func (s *Server) handleStationByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	station, err := s.Store.GetStation(r.Context(), id)
	if err != nil {
		if errors.Is(err, data.ErrNotFound) {
			http.Error(w, "station not found", http.StatusNotFound)
			return
		}
		log.Printf("station %d: lookup failed: %v", id, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, station)
}

// availabilityResponse is what the frontend renders in the detail panel.
// Source is always set so the UI can credit it; matched=false means we
// queried but found no nearby OCM record for this station.
type availabilityResponse struct {
	Source        string  `json:"source"`     // e.g. "openchargemap"
	Configured    bool    `json:"configured"` // false → no OCM client wired
	Matched       bool    `json:"matched"`    // true → a POI matched within match radius
	Status        string  `json:"status,omitempty"`
	IsOperational *bool   `json:"isOperational,omitempty"`
	LastUpdated   string  `json:"lastUpdated,omitempty"` // ISO8601, may be ""
	DistanceKm    float64 `json:"distanceKm,omitempty"`  // straight-line km from registry coord to OCM POI
	OperatorTitle string  `json:"operator,omitempty"`
	Note          string  `json:"note,omitempty"` // human-readable disclaimer
}

func (s *Server) handleAvailability(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	st, err := s.Store.GetStation(r.Context(), id)
	if err != nil {
		if errors.Is(err, data.ErrNotFound) {
			http.Error(w, "station not found", http.StatusNotFound)
			return
		}
		log.Printf("availability %d: lookup failed: %v", id, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := availabilityResponse{Source: "openchargemap"}

	if s.OCM == nil {
		resp.Note = "Live-Status nicht konfiguriert."
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Configured = true

	if st.Breitengrad == 0 || st.Laengengrad == 0 {
		resp.Note = "Station hat keine Koordinaten — Live-Status nicht abrufbar."
		writeJSON(w, http.StatusOK, resp)
		return
	}

	pois, err := s.OCM.NearbyPOIs(r.Context(), st.Breitengrad, st.Laengengrad, 0.5, 5)
	if err != nil {
		log.Printf("availability %d: OCM query failed: %v", id, err)
		switch {
		case errors.Is(err, ocm.ErrAuthRequired):
			resp.Note = "Open Charge Map verlangt einen API-Key. Setze die Umgebungsvariable OCM_API_KEY (kostenfrei unter openchargemap.io registrieren)."
		default:
			resp.Note = "Open Charge Map ist gerade nicht erreichbar."
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Pick the closest POI within 150 m as the match. OCM coordinates often
	// drift a bit from the BNetzA registry; 150 m is forgiving enough.
	const matchRadiusKm = 0.15
	var best *ocm.POI
	bestDist := math.MaxFloat64
	for i := range pois {
		d := haversineKm(st.Breitengrad, st.Laengengrad, pois[i].Latitude, pois[i].Longitude)
		if d < matchRadiusKm && d < bestDist {
			bestDist = d
			best = &pois[i]
		}
	}
	if best == nil {
		resp.Note = "Kein passender Open-Charge-Map-Eintrag in 150 m Umkreis gefunden."
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp.Matched = true
	resp.Status = best.StatusTitle
	resp.IsOperational = best.IsOperational
	resp.OperatorTitle = best.OperatorTitle
	resp.DistanceKm = math.Round(bestDist*1000) / 1000
	if best.LastUpdated != nil {
		resp.LastUpdated = best.LastUpdated.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}

func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371.0
	const toRad = math.Pi / 180
	dLat := (lat2 - lat1) * toRad
	dLng := (lng2 - lng1) * toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * R * math.Asin(math.Sqrt(a))
}

func (s *Server) handleBetreiber(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prefix := strings.TrimSpace(q.Get("q"))

	limit := 50
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = n
	}

	names, err := s.Store.ListBetreiber(r.Context(), prefix, limit)
	if err != nil {
		log.Printf("betreiber: list failed: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, names)
}

func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request) {
	if s.Planner == nil {
		http.Error(w, "route planning not configured", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()

	start, err := parseLatLng(q.Get("start"))
	if err != nil {
		http.Error(w, "invalid start: "+err.Error(), http.StatusBadRequest)
		return
	}
	end, err := parseLatLng(q.Get("end"))
	if err != nil {
		http.Error(w, "invalid end: "+err.Error(), http.StatusBadRequest)
		return
	}
	rangeKm, err := parsePositiveFloat(q.Get("range"), 300)
	if err != nil {
		http.Error(w, "invalid range: "+err.Error(), http.StatusBadRequest)
		return
	}
	soc, err := parsePositiveFloat(q.Get("soc"), 80)
	if err != nil || soc > 100 {
		http.Error(w, "invalid soc (0-100 expected)", http.StatusBadRequest)
		return
	}

	req := route.Request{
		Start:      start,
		End:        end,
		RangeKm:    rangeKm,
		SocPercent: soc,
		Tiers:      splitCSV(q.Get("tiers")),
		Plugs:      splitCSV(q.Get("plugs")),
		Status:     splitCSV(q.Get("status")),
	}
	// Match the /api/stations convention for tier codes.
	for i, t := range req.Tiers {
		req.Tiers[i] = strings.ToLower(t)
	}

	plan, err := s.Planner.Plan(r.Context(), req)
	if err != nil {
		if errors.Is(err, route.ErrNoRoute) {
			http.Error(w, "no route between the given points", http.StatusNotFound)
			return
		}
		log.Printf("route: planning failed: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// parseLatLng parses "lat,lng" with at least one decimal place.
func parseLatLng(raw string) ([2]float64, error) {
	var zero [2]float64
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return zero, errors.New("empty")
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return zero, errors.New("expected \"lat,lng\"")
	}
	lat, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return zero, errors.New("non-numeric latitude")
	}
	lng, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return zero, errors.New("non-numeric longitude")
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return zero, errors.New("out of range")
	}
	return [2]float64{lat, lng}, nil
}

// parsePositiveFloat returns the parsed float or dflt if raw is empty.
// Returns an error if raw is non-numeric or not strictly positive.
func parsePositiveFloat(raw string, dflt float64) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return dflt, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, errors.New("non-numeric")
	}
	if v <= 0 {
		return 0, errors.New("must be > 0")
	}
	return v, nil
}

func (s *Server) handleGeocode(w http.ResponseWriter, r *http.Request) {
	term := strings.TrimSpace(r.URL.Query().Get("q"))
	if term == "" {
		http.Error(w, "missing q", http.StatusBadRequest)
		return
	}
	loc, err := s.Store.Geocode(r.Context(), term)
	if err != nil {
		if errors.Is(err, data.ErrNotFound) {
			http.Error(w, "location not found", http.StatusNotFound)
			return
		}
		log.Printf("geocode: query failed: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, loc)
}

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	term := strings.TrimSpace(q.Get("q"))

	limit := 8
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = n
	}

	results, err := s.Store.Suggest(r.Context(), term, limit)
	if err != nil {
		log.Printf("suggest: query failed: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if results == nil {
		results = []data.Suggestion{}
	}
	writeJSON(w, http.StatusOK, results)
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

// withMiddleware adds panic recovery and per-request access logging around
// the given handler. Recovery logs the panic value and returns 500.
func withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v", rec)
				// Only write the 500 header if nothing has been sent yet.
				if !rw.wroteHeader {
					rw.WriteHeader(http.StatusInternalServerError)
					_, _ = rw.Write([]byte("internal server error"))
				}
			}
			log.Printf("%s %s %d %s",
				r.Method, r.URL.Path, rw.status, time.Since(start))
		}()

		next.ServeHTTP(rw, r)
	})
}

// statusRecorder captures the response status for logging.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		// Mirror net/http's implicit 200 behaviour so logging is accurate.
		r.wroteHeader = true
	}
	return r.ResponseWriter.Write(b)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// writeJSON writes v as JSON with the given status. On encode failure it
// logs but cannot recover the response body (headers are already flushed).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		log.Printf("writeJSON: encode failed: %v", err)
	}
}

// parseBBox parses "minLat,minLng,maxLat,maxLng" and validates ordering.
func parseBBox(s string) (*data.BBox, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return nil, errors.New("expected 4 comma-separated values")
	}
	vals := make([]float64, 4)
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, errors.New("non-numeric component")
		}
		vals[i] = f
	}
	bb := &data.BBox{
		MinLat: vals[0],
		MinLng: vals[1],
		MaxLat: vals[2],
		MaxLng: vals[3],
	}
	if bb.MinLat > bb.MaxLat || bb.MinLng > bb.MaxLng {
		return nil, errors.New("min must be <= max")
	}
	return bb, nil
}

// splitCSV splits a comma-separated query-string value into trimmed, non-empty
// components. An empty or all-whitespace input produces a nil slice so callers
// can test `len(...) > 0` to decide whether to apply the filter.
func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
