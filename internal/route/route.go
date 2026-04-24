// Package route composes the OSRM driving route with the charging-station
// registry to produce a complete trip plan: polyline, distance, duration, and
// the charging stops required given the user's range and current SOC.
package route

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"echarge/internal/data"
	"echarge/internal/osrm"
)

// Request parameters for PlanRoute.
type Request struct {
	Start      [2]float64 // lat, lng
	End        [2]float64 // lat, lng
	RangeKm    float64    // full-charge range of the EV (km)
	SocPercent float64    // current state of charge (0-100)
	Tiers      []string   // power-class filter, e.g. ["hpc","ultra"]
	Plugs      []string   // connector filter, e.g. ["Typ 2"]
	Status     []string   // station status filter, e.g. ["In Betrieb"]
}

// Stop is one recommended charging stop along the route.
type Stop struct {
	ProgressKm float64      `json:"progressKm"` // distance from start along the actual road
	OffrouteKm float64      `json:"offrouteKm"` // straight-line km from the route at the station's closest-anchor point
	Station    data.Station `json:"station"`    // full station record
}

// Plan is the response returned to the frontend.
type Plan struct {
	Polyline    [][2]float64 `json:"polyline"`
	DistanceKm  float64      `json:"distanceKm"`
	DurationMin float64      `json:"durationMin"`
	Stops       []Stop       `json:"stops"`
}

// ErrNoRoute bubbles up from the OSRM client when no driving route exists.
var ErrNoRoute = osrm.ErrNoRoute

// Planner wires the data store to an OSRM client.
type Planner struct {
	Store *data.Store
	OSRM  *osrm.Client
}

// Plan produces a full route plan. It:
//  1. Calls OSRM for the driving polyline + distance + duration.
//  2. Queries candidate stations (HPC/Ultra by default, tighter with filters).
//  3. Projects each candidate onto the polyline and keeps those in a corridor
//     whose width scales with the vehicle's range.
//  4. Distributes charging stops along the route, preferring stations close
//     to the road over stations that happen to sit at the ideal distance.
func (p *Planner) Plan(ctx context.Context, req Request) (*Plan, error) {
	if p.OSRM == nil {
		return nil, errors.New("route: no OSRM client configured")
	}
	if req.RangeKm <= 0 {
		return nil, errors.New("route: range must be > 0")
	}
	if req.SocPercent < 0 || req.SocPercent > 100 {
		return nil, errors.New("route: socPercent must be in [0, 100]")
	}

	// 1. Driving route.
	r, err := p.OSRM.Driving(ctx, req.Start, req.End)
	if err != nil {
		return nil, fmt.Errorf("route: osrm: %w", err)
	}
	polyline := r.Polyline
	if len(polyline) < 2 {
		return nil, osrm.ErrNoRoute
	}
	cumul := cumulativeDistances(polyline)
	totalKm := cumul[len(cumul)-1]

	// 2. Candidate stations. Default to fast chargers if the caller didn't pin
	// the power class — users planning a road trip rarely want an AC-only stop.
	tiers := req.Tiers
	if len(tiers) == 0 {
		tiers = []string{"hpc", "ultra"}
	}
	// Restrict the candidate pool to a bounding box that covers the whole
	// route plus a 0.4° padding (~45 km at German latitudes). Without this
	// filter we'd hit the 5000-row limit on the first stations by id and
	// miss anything outside that global prefix — e.g. Saarland when planning
	// a short trip there.
	bbox := routeBoundingBox(polyline, 0.4)
	candidates, err := p.Store.SearchStations(ctx, data.SearchParams{
		Tiers:  tiers,
		Plugs:  req.Plugs,
		Status: req.Status,
		BBox:   &bbox,
		Limit:  5000,
	})
	if err != nil {
		return nil, fmt.Errorf("route: candidates: %w", err)
	}

	// 3. Off-route projection via a 2-km anchor grid.
	const anchorStepKm = 2.0
	anchors := sampleAnchors(polyline, cumul, anchorStepKm)

	// Corridor radius: hard cap on how far a station may sit from the road
	// (straight line). Generous enough to always yield candidates — the cost
	// function weights off-route distance very heavily, so on-corridor
	// stations still win even when a large pool is available.
	corridorRadius := 15.0

	type corridor struct {
		st       data.Station
		offroute float64
		progress float64
	}
	var cands []corridor
	for _, st := range candidates {
		if st.Breitengrad == 0 && st.Laengengrad == 0 {
			continue
		}
		bestD := math.MaxFloat64
		bestProg := 0.0
		for _, a := range anchors {
			d := haversineKm(st.Breitengrad, st.Laengengrad, a.pt[0], a.pt[1])
			if d < bestD {
				bestD = d
				bestProg = a.d
			}
		}
		if bestD > corridorRadius {
			continue
		}
		if bestProg < totalKm*0.05 || bestProg > totalKm*0.95 {
			continue
		}
		cands = append(cands, corridor{st: st, offroute: bestD, progress: bestProg})
	}

	// 4. Decide how many stops we need and roughly where.
	initialRange := req.RangeKm * (req.SocPercent / 100.0)
	usableStep := req.RangeKm * 0.7
	stepsCount := 0
	if totalKm > initialRange {
		// Reserve 20% buffer at the destination (range*0.2).
		stepsCount = int(math.Ceil((totalKm - initialRange + req.RangeKm*0.2) / usableStep))
	}

	targets := make([]float64, 0, stepsCount)
	travelled, remaining := 0.0, initialRange
	for range stepsCount {
		td := math.Min(totalKm-30, travelled+remaining-20)
		if td <= 0 {
			break
		}
		targets = append(targets, td)
		travelled = td
		remaining = req.RangeKm * 0.7
	}

	// 5. Pick one station per target. Cost heavily penalises off-route
	// distance (×3) so the picker prefers stations directly on the road.
	var chosen []corridor
	used := map[int64]bool{}
	for _, td := range targets {
		lastProg := 0.0
		if n := len(chosen); n > 0 {
			lastProg = chosen[n-1].progress
		}
		minP := lastProg + 20
		type scored struct {
			c    corridor
			cost float64
		}
		var eligible []scored
		for _, c := range cands {
			if used[c.st.ID] {
				continue
			}
			if c.progress < minP || c.progress > td {
				continue
			}
			// Cost: strongly prefer stations that sit directly on the road.
			// offroute is weighted ×8 so a 1-km-detour station at 10 km from
			// the ideal target still beats a 5-km-detour station exactly at
			// the target (8+10=18 vs 40+0=40).
			eligible = append(eligible, scored{
				c:    c,
				cost: c.offroute*8 + math.Abs(td-c.progress),
			})
		}
		if len(eligible) == 0 {
			continue
		}
		sort.Slice(eligible, func(i, j int) bool {
			return eligible[i].cost < eligible[j].cost
		})
		pick := eligible[0].c
		chosen = append(chosen, pick)
		used[pick.st.ID] = true
	}
	sort.Slice(chosen, func(i, j int) bool {
		return chosen[i].progress < chosen[j].progress
	})

	stops := make([]Stop, len(chosen))
	for i, c := range chosen {
		stops[i] = Stop{
			ProgressKm: round1(c.progress),
			OffrouteKm: round1(c.offroute),
			Station:    c.st,
		}
	}

	return &Plan{
		Polyline:    polyline,
		DistanceKm:  round1(r.DistanceKm),
		DurationMin: math.Round(r.DurationMin),
		Stops:       stops,
	}, nil
}

type anchor struct {
	d  float64 // km from start
	pt [2]float64
}

// cumulativeDistances returns a slice the same length as polyline, with each
// entry holding the total km from the start along the polyline.
func cumulativeDistances(polyline [][2]float64) []float64 {
	out := make([]float64, len(polyline))
	for i := 1; i < len(polyline); i++ {
		out[i] = out[i-1] + haversineKm(
			polyline[i-1][0], polyline[i-1][1],
			polyline[i][0], polyline[i][1],
		)
	}
	return out
}

// sampleAnchors walks the polyline and emits evenly-spaced anchor points.
func sampleAnchors(polyline [][2]float64, cumul []float64, stepKm float64) []anchor {
	total := cumul[len(cumul)-1]
	n := max(int(math.Ceil(total/stepKm))+1, 2)
	out := make([]anchor, 0, n)
	for i := range n {
		d := math.Min(total, float64(i)*stepKm)
		out = append(out, anchor{d: d, pt: pointAt(polyline, cumul, d)})
	}
	return out
}

// pointAt returns the interpolated [lat,lng] at distance d along the polyline.
func pointAt(polyline [][2]float64, cumul []float64, d float64) [2]float64 {
	if d <= 0 {
		return polyline[0]
	}
	if d >= cumul[len(cumul)-1] {
		return polyline[len(polyline)-1]
	}
	// Binary search for the segment that contains d.
	lo, hi := 0, len(cumul)-1
	for hi-lo > 1 {
		mid := (lo + hi) >> 1
		if cumul[mid] <= d {
			lo = mid
		} else {
			hi = mid
		}
	}
	segLen := cumul[hi] - cumul[lo]
	t := 0.0
	if segLen > 0 {
		t = (d - cumul[lo]) / segLen
	}
	return [2]float64{
		polyline[lo][0] + t*(polyline[hi][0]-polyline[lo][0]),
		polyline[lo][1] + t*(polyline[hi][1]-polyline[lo][1]),
	}
}

// haversineKm is great-circle distance in kilometres.
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

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// routeBoundingBox returns the axis-aligned box enclosing the polyline,
// padded by `pad` degrees on each side so stations near but not exactly on
// the route still show up in the candidate pool.
func routeBoundingBox(polyline [][2]float64, pad float64) data.BBox {
	minLat, minLng := polyline[0][0], polyline[0][1]
	maxLat, maxLng := minLat, minLng
	for _, p := range polyline[1:] {
		if p[0] < minLat {
			minLat = p[0]
		}
		if p[0] > maxLat {
			maxLat = p[0]
		}
		if p[1] < minLng {
			minLng = p[1]
		}
		if p[1] > maxLng {
			maxLng = p[1]
		}
	}
	return data.BBox{
		MinLat: minLat - pad,
		MinLng: minLng - pad,
		MaxLat: maxLat + pad,
		MaxLng: maxLng + pad,
	}
}
