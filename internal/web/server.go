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
	"net/http"
	"strconv"
	"strings"
	"time"

	"echarge/internal/data"
	"echarge/internal/web/templates"
)

// Server is the HTTP application. Construct it directly; the zero value is
// NOT usable because Store must be set. JSBundle should hold the esbuild
// output produced once at startup by echarge/internal/build.BuildJS.
type Server struct {
	Store    *data.Store
	JSBundle []byte
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
	mux.HandleFunc("GET /api/betreiber", s.handleBetreiber)
	mux.HandleFunc("GET /api/suggest", s.handleSuggest)
	mux.HandleFunc("GET /api/geocode", s.handleGeocode)

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
