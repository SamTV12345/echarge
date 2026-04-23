package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Load reads the embedded CSV, builds an in-memory SQLite database and returns
// a ready-to-query Store. The operation is roughly O(rows) and takes a few
// seconds for the ~130k-row BNetzA registry.
func Load(ctx context.Context) (*Store, error) {
	start := time.Now()

	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Keep a single connection — the in-memory DB lives only on its
	// owning connection when cache=shared is off, and even with shared
	// cache the cost of concurrency is not worth it for read-mostly use.
	db.SetMaxOpenConns(1)

	if err := applyPragmas(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := createSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	ok, skipped, err := importCSV(ctx, db, RegistryCSV())
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("import csv: %w", err)
	}

	if _, err := db.ExecContext(ctx, "ANALYZE;"); err != nil {
		// Non-fatal — continue.
		log.Printf("data: ANALYZE failed: %v", err)
	}

	log.Printf("data: loaded %d stations, skipped %d rows", ok, skipped)
	log.Printf("data: load completed in %s", time.Since(start).Round(time.Millisecond))

	return &Store{db: db}, nil
}

// applyPragmas sets performance PRAGMAs appropriate for an ephemeral
// in-memory DB that's bulk-loaded once and then read many times.
func applyPragmas(ctx context.Context, db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=MEMORY;",
		"PRAGMA synchronous=OFF;",
		"PRAGMA temp_store=MEMORY;",
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	return nil
}

const schemaSQL = `
CREATE TABLE stations (
    id INTEGER PRIMARY KEY,
    betreiber TEXT NOT NULL,
    anzeigename TEXT,
    status TEXT,
    art TEXT,
    anzahl_ladepunkte INTEGER,
    nennleistung_kw REAL,
    inbetriebnahme TEXT,
    strasse TEXT,
    hausnummer TEXT,
    adresszusatz TEXT,
    plz TEXT,
    ort TEXT,
    kreis TEXT,
    bundesland TEXT,
    lat REAL,
    lng REAL,
    standortbezeichnung TEXT,
    parkraum TEXT,
    bezahlsysteme TEXT,
    oeffnungszeiten TEXT,
    plugs TEXT NOT NULL DEFAULT ''
);
CREATE TABLE ladepunkte (
    station_id INTEGER NOT NULL,
    idx INTEGER NOT NULL,
    steckertypen TEXT,
    nennleistung REAL,
    evse_id TEXT,
    PRIMARY KEY (station_id, idx)
);
CREATE INDEX idx_stations_betreiber ON stations(betreiber COLLATE NOCASE);
CREATE INDEX idx_stations_coords ON stations(lat, lng);
CREATE INDEX idx_stations_ort ON stations(ort COLLATE NOCASE);
CREATE INDEX idx_stations_plugs ON stations(plugs);
`

func createSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	return nil
}

const insertStationSQL = `
INSERT INTO stations (
    id, betreiber, anzeigename, status, art, anzahl_ladepunkte, nennleistung_kw,
    inbetriebnahme, strasse, hausnummer, adresszusatz, plz, ort, kreis,
    bundesland, lat, lng, standortbezeichnung, parkraum, bezahlsysteme,
    oeffnungszeiten, plugs
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const insertLadepunktSQL = `
INSERT INTO ladepunkte (station_id, idx, steckertypen, nennleistung, evse_id)
VALUES (?, ?, ?, ?, ?)`

// importCSV parses the embedded registry and bulk-inserts everything inside a
// single transaction. Returns the count of stations imported and skipped.
func importCSV(ctx context.Context, db *sql.DB, raw []byte) (ok, skipped int, err error) {
	parseStart := time.Now()
	cr := newCSVReader(raw)
	if err := skipPreamble(cr); err != nil {
		return 0, 0, fmt.Errorf("find header: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmtStation, err := tx.PrepareContext(ctx, insertStationSQL)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare station insert: %w", err)
	}
	defer stmtStation.Close()
	stmtLadepunkt, err := tx.PrepareContext(ctx, insertLadepunktSQL)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare ladepunkt insert: %w", err)
	}
	defer stmtLadepunkt.Close()

	loggedSkip := false
	err = iterRows(cr, func(rec []string) error {
		row, good := parseRow(rec)
		if !good {
			skipped++
			if !loggedSkip {
				loggedSkip = true
				first := ""
				if len(rec) > 0 {
					first = rec[0]
				}
				log.Printf("data: skipping unparseable row (first cell=%q)", first)
			}
			return nil
		}
		s := row.Station
		if _, err := stmtStation.ExecContext(ctx,
			s.ID, s.Betreiber, s.Anzeigename, s.Status, s.Art,
			s.AnzahlLadepunkte, s.NennleistungKW, s.Inbetriebnahme,
			s.Strasse, s.Hausnummer, s.Adresszusatz, s.PLZ, s.Ort,
			s.Kreis, s.Bundesland, s.Breitengrad, s.Laengengrad,
			s.Standortbezeichnung, s.Parkraum, s.Bezahlsysteme,
			s.Oeffnungszeiten, strings.Join(s.Plugs, ","),
		); err != nil {
			return fmt.Errorf("insert station %d: %w", s.ID, err)
		}
		for _, lp := range row.Ladepunkte {
			if _, err := stmtLadepunkt.ExecContext(ctx,
				s.ID, lp.Idx, lp.Steckertypen, lp.Nennleistung, lp.EvseID,
			); err != nil {
				return fmt.Errorf("insert ladepunkt %d/%d: %w", s.ID, lp.Idx, err)
			}
		}
		ok++
		return nil
	})
	if err != nil {
		return ok, skipped, err
	}

	if err = tx.Commit(); err != nil {
		return ok, skipped, fmt.Errorf("commit: %w", err)
	}

	log.Printf("data: parsed+imported CSV in %s", time.Since(parseStart).Round(time.Millisecond))
	return ok, skipped, nil
}

// -- queries ---------------------------------------------------------------

const (
	defaultSearchLimit    = 500
	maxSearchLimit        = 5000
	defaultBetreiberLimit = 50
)

// SearchStations runs a dynamic WHERE against the stations table.
// The returned slice intentionally omits Ladepunkte to keep map payloads
// small — use GetStation for the full detail view.
func (s *Store) SearchStations(ctx context.Context, params SearchParams) ([]Station, error) {
	var (
		where []string
		args  []any
	)

	if b := strings.TrimSpace(params.Betreiber); b != "" {
		where = append(where, "betreiber LIKE ? ESCAPE '\\' COLLATE NOCASE")
		args = append(args, "%"+escapeLike(b)+"%")
	}
	if q := strings.TrimSpace(params.Q); q != "" {
		like := "%" + escapeLike(q) + "%"
		where = append(where, "(anzeigename LIKE ? ESCAPE '\\' COLLATE NOCASE OR ort LIKE ? ESCAPE '\\' COLLATE NOCASE OR strasse LIKE ? ESCAPE '\\' COLLATE NOCASE)")
		args = append(args, like, like, like)
	}
	if bb := params.BBox; bb != nil {
		where = append(where, "lat BETWEEN ? AND ? AND lng BETWEEN ? AND ? AND lat != 0 AND lng != 0")
		args = append(args, bb.MinLat, bb.MaxLat, bb.MinLng, bb.MaxLng)
	}

	// Tier filter — OR the per-tier kW range predicates. If every tier is
	// selected (or none) the filter is a no-op and we skip it entirely.
	if tiers := params.Tiers; len(tiers) > 0 && len(tiers) < 4 {
		tierPredicates := map[string]string{
			"ac":    "nennleistung_kw < 50",
			"dc":    "nennleistung_kw >= 50 AND nennleistung_kw < 150",
			"hpc":   "nennleistung_kw >= 150 AND nennleistung_kw < 300",
			"ultra": "nennleistung_kw >= 300",
		}
		var ors []string
		for _, t := range tiers {
			if p, ok := tierPredicates[t]; ok {
				ors = append(ors, "("+p+")")
			}
		}
		if len(ors) > 0 {
			where = append(where, "("+strings.Join(ors, " OR ")+")")
		}
	}

	// Plug filter — OR LIKE clauses against the comma-separated plugs column.
	if plugs := params.Plugs; len(plugs) > 0 {
		var ors []string
		for _, p := range plugs {
			ors = append(ors, "plugs LIKE ? ESCAPE '\\'")
			args = append(args, "%"+escapeLike(p)+"%")
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}

	// Status filter — exact membership against raw BNetzA status values.
	if status := params.Status; len(status) > 0 {
		placeholders := make([]string, len(status))
		for i, s := range status {
			placeholders[i] = "?"
			args = append(args, s)
		}
		where = append(where, "(status IN ("+strings.Join(placeholders, ", ")+"))")
	}

	limit := params.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT id, betreiber, anzeigename, status, art, anzahl_ladepunkte,
        nennleistung_kw, inbetriebnahme, strasse, hausnummer, adresszusatz,
        plz, ort, kreis, bundesland, lat, lng, standortbezeichnung, parkraum,
        bezahlsysteme, oeffnungszeiten, plugs
        FROM stations`)
	if len(where) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(where, " AND "))
	}
	sb.WriteString(" ORDER BY id LIMIT ?")
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("search stations: %w", err)
	}
	defer rows.Close()

	var out []Station
	for rows.Next() {
		var st Station
		if err := scanStation(rows, &st); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListBetreiber returns the distinct Betreiber names matching the given
// prefix (case-insensitive), sorted alphabetically.
func (s *Store) ListBetreiber(ctx context.Context, prefix string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = defaultBetreiberLimit
	}
	pattern := escapeLike(strings.TrimSpace(prefix)) + "%"

	rows, err := s.db.QueryContext(ctx, `
        SELECT DISTINCT betreiber FROM stations
        WHERE betreiber LIKE ? ESCAPE '\' COLLATE NOCASE
        ORDER BY betreiber COLLATE NOCASE
        LIMIT ?`, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("list betreiber: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetStation fetches the full station record plus its Ladepunkte (ordered by
// idx). Returns ErrNotFound if no station has the given ID.
func (s *Store) GetStation(ctx context.Context, id int64) (*Station, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, betreiber, anzeigename, status, art, anzahl_ladepunkte,
               nennleistung_kw, inbetriebnahme, strasse, hausnummer,
               adresszusatz, plz, ort, kreis, bundesland, lat, lng,
               standortbezeichnung, parkraum, bezahlsysteme, oeffnungszeiten,
               plugs
        FROM stations WHERE id = ?`, id)

	var st Station
	if err := scanStation(row, &st); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get station: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
        SELECT idx, steckertypen, nennleistung, evse_id
        FROM ladepunkte WHERE station_id = ? ORDER BY idx`, id)
	if err != nil {
		return nil, fmt.Errorf("get ladepunkte: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var lp Ladepunkt
		var steckertypen, evseID sql.NullString
		var nennleistung sql.NullFloat64
		if err := rows.Scan(&lp.Idx, &steckertypen, &nennleistung, &evseID); err != nil {
			return nil, err
		}
		lp.Steckertypen = steckertypen.String
		lp.Nennleistung = nennleistung.Float64
		lp.EvseID = evseID.String
		st.Ladepunkte = append(st.Ladepunkte, lp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &st, nil
}

// Suggest returns a ranked list of autocomplete entries for a free-text query.
// It mirrors the design prototype: up to 4 Ort matches (prefix), up to 4
// Betreiber matches (substring), and up to 4 address matches (substring on
// strasse+ort). The three groups are concatenated in that order and then
// truncated to limit.
func (s *Store) Suggest(ctx context.Context, q string, limit int) ([]Suggestion, error) {
	q = strings.TrimSpace(q)
	if len(q) < 2 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}

	escaped := escapeLike(q)
	prefix := escaped + "%"
	substr := "%" + escaped + "%"

	const groupLimit = 4
	var out []Suggestion

	// 1. Orte (prefix match, case-insensitive).
	orteRows, err := s.db.QueryContext(ctx, `
        SELECT DISTINCT ort FROM stations
        WHERE ort LIKE ? ESCAPE '\' COLLATE NOCASE
        ORDER BY ort
        LIMIT ?`, prefix, groupLimit)
	if err != nil {
		return nil, fmt.Errorf("suggest orte: %w", err)
	}
	for orteRows.Next() {
		var ort string
		if err := orteRows.Scan(&ort); err != nil {
			orteRows.Close()
			return nil, err
		}
		if ort == "" {
			continue
		}
		out = append(out, Suggestion{Kind: "Ort", Label: ort, Value: ort})
	}
	orteRows.Close()
	if err := orteRows.Err(); err != nil {
		return nil, err
	}

	// 2. Betreiber (substring match).
	betrRows, err := s.db.QueryContext(ctx, `
        SELECT DISTINCT betreiber FROM stations
        WHERE betreiber LIKE ? ESCAPE '\' COLLATE NOCASE
        ORDER BY betreiber
        LIMIT ?`, substr, groupLimit)
	if err != nil {
		return nil, fmt.Errorf("suggest betreiber: %w", err)
	}
	for betrRows.Next() {
		var b string
		if err := betrRows.Scan(&b); err != nil {
			betrRows.Close()
			return nil, err
		}
		if b == "" {
			continue
		}
		out = append(out, Suggestion{Kind: "Betreiber", Label: b, Value: b})
	}
	betrRows.Close()
	if err := betrRows.Err(); err != nil {
		return nil, err
	}

	// 3. Adressen (substring match on strasse+ort).
	addrRows, err := s.db.QueryContext(ctx, `
        SELECT id, strasse, hausnummer, plz, ort FROM stations
        WHERE (strasse || ' ' || ort) LIKE ? ESCAPE '\' COLLATE NOCASE
        ORDER BY id
        LIMIT ?`, substr, groupLimit)
	if err != nil {
		return nil, fmt.Errorf("suggest adressen: %w", err)
	}
	for addrRows.Next() {
		var (
			id                            int64
			strasse, hausnummer, plz, ort sql.NullString
		)
		if err := addrRows.Scan(&id, &strasse, &hausnummer, &plz, &ort); err != nil {
			addrRows.Close()
			return nil, err
		}
		label := formatAddressLabel(strasse.String, hausnummer.String, plz.String, ort.String)
		if label == "" {
			continue
		}
		out = append(out, Suggestion{
			Kind:      "Adresse",
			Label:     label,
			Value:     label,
			StationID: id,
		})
	}
	addrRows.Close()
	if err := addrRows.Err(); err != nil {
		return nil, err
	}

	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Geocode returns a coordinate for the given free-text place name, derived
// from the registry (the first station whose ort matches). Exact match wins,
// then prefix, then substring. Returns ErrNotFound if nothing matches.
//
// The input is cleaned of common German descriptors ("an der Saar", "b. X",
// "i.d. Y") and tried both as-is and trimmed — this makes queries like
// "Bous an der Saar" resolve when the registry only carries "Bous".
func (s *Store) Geocode(ctx context.Context, q string) (*Location, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, ErrNotFound
	}

	// Try a sequence of progressively looser candidates. We stop at the first
	// one that yields any coordinate-bearing station.
	candidates := []string{q}
	// Trim common trailing qualifiers to widen the match (e.g. "Bous an der Saar" → "Bous").
	trimmed := trimPlaceQualifiers(q)
	if trimmed != "" && trimmed != q {
		candidates = append(candidates, trimmed)
	}

	for _, cand := range candidates {
		loc, err := s.geocodeOne(ctx, cand)
		if err != nil {
			return nil, err
		}
		if loc != nil {
			return loc, nil
		}
	}
	return nil, ErrNotFound
}

// geocodeOne runs one ranked lookup: exact → prefix → substring, in that order,
// returning the first match with non-zero coordinates. Nil result = no match.
func (s *Store) geocodeOne(ctx context.Context, cand string) (*Location, error) {
	escaped := escapeLike(cand)
	// ORDER BY orders exact-match rows first, then prefix, then substring.
	// We filter out (0,0) coords so missing-coord stations don't win.
	const query = `
        SELECT ort, lat, lng FROM stations
        WHERE (ort = ? COLLATE NOCASE
               OR ort LIKE ? ESCAPE '\' COLLATE NOCASE
               OR ort LIKE ? ESCAPE '\' COLLATE NOCASE)
          AND lat != 0 AND lng != 0
        ORDER BY
            CASE
                WHEN ort = ? COLLATE NOCASE THEN 0
                WHEN ort LIKE ? ESCAPE '\' COLLATE NOCASE THEN 1
                ELSE 2
            END,
            LENGTH(ort), ort
        LIMIT 1`
	prefix := escaped + "%"
	substr := "%" + escaped + "%"
	row := s.db.QueryRowContext(ctx, query, cand, prefix, substr, cand, prefix)

	var loc Location
	if err := row.Scan(&loc.Name, &loc.Lat, &loc.Lng); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("geocode: %w", err)
	}
	return &loc, nil
}

// trimPlaceQualifiers removes common trailing regional descriptors that are
// not present in the registry's ort column (e.g. "Bous an der Saar" → "Bous",
// "Frankfurt am Main" → "Frankfurt"). It is conservative: if no known marker
// is found, the input is returned unchanged.
func trimPlaceQualifiers(s string) string {
	// Keep markers lower-cased and whitespace-normalised for the scan.
	lower := strings.ToLower(s)
	markers := []string{
		" an der ", " am ", " a.d. ", " a. d. ",
		" im ", " i. ", " i.d. ", " i. d. ",
		" bei ", " b. ", " b ",
		" in der ", " unter ", " ob der ",
		"/", ",",
	}
	cut := len(s)
	for _, m := range markers {
		if idx := strings.Index(lower, m); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	if cut == len(s) {
		return s
	}
	return strings.TrimSpace(s[:cut])
}

// formatAddressLabel renders "{strasse} {hausnummer}, {plz} {ort}" while
// gracefully skipping empty pieces so we don't emit stray punctuation.
func formatAddressLabel(strasse, hausnummer, plz, ort string) string {
	strasse = strings.TrimSpace(strasse)
	hausnummer = strings.TrimSpace(hausnummer)
	plz = strings.TrimSpace(plz)
	ort = strings.TrimSpace(ort)

	var street string
	switch {
	case strasse != "" && hausnummer != "":
		street = strasse + " " + hausnummer
	case strasse != "":
		street = strasse
	case hausnummer != "":
		street = hausnummer
	}

	var city string
	switch {
	case plz != "" && ort != "":
		city = plz + " " + ort
	case plz != "":
		city = plz
	case ort != "":
		city = ort
	}

	switch {
	case street != "" && city != "":
		return street + ", " + city
	case street != "":
		return street
	case city != "":
		return city
	}
	return ""
}

// Count returns the total number of stations in the store.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM stations").Scan(&n); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

// scanner matches both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanStation materialises a stations-table row into a Station. NULL text
// columns become empty strings, NULL numeric columns become 0.
func scanStation(sc scanner, st *Station) error {
	var (
		anzeigename, status, art, inbetriebnahme, strasse, hausnummer,
		adresszusatz, plz, ort, kreis, bundesland, standortbezeichnung,
		parkraum, bezahlsysteme, oeffnungszeiten sql.NullString
		plugsCSV         sql.NullString
		anzahlLadepunkte sql.NullInt64
		nennleistungKW   sql.NullFloat64
		lat, lng         sql.NullFloat64
	)
	if err := sc.Scan(
		&st.ID, &st.Betreiber, &anzeigename, &status, &art,
		&anzahlLadepunkte, &nennleistungKW, &inbetriebnahme,
		&strasse, &hausnummer, &adresszusatz, &plz, &ort, &kreis,
		&bundesland, &lat, &lng, &standortbezeichnung, &parkraum,
		&bezahlsysteme, &oeffnungszeiten, &plugsCSV,
	); err != nil {
		return err
	}
	st.Anzeigename = anzeigename.String
	st.Status = status.String
	st.Art = art.String
	st.AnzahlLadepunkte = int(anzahlLadepunkte.Int64)
	st.NennleistungKW = nennleistungKW.Float64
	st.Inbetriebnahme = inbetriebnahme.String
	st.Strasse = strasse.String
	st.Hausnummer = hausnummer.String
	st.Adresszusatz = adresszusatz.String
	st.PLZ = plz.String
	st.Ort = ort.String
	st.Kreis = kreis.String
	st.Bundesland = bundesland.String
	st.Breitengrad = lat.Float64
	st.Laengengrad = lng.Float64
	st.Standortbezeichnung = standortbezeichnung.String
	st.Parkraum = parkraum.String
	st.Bezahlsysteme = bezahlsysteme.String
	st.Oeffnungszeiten = oeffnungszeiten.String
	if csv := plugsCSV.String; csv != "" {
		var plugs []string
		for _, p := range strings.Split(csv, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			plugs = append(plugs, p)
		}
		st.Plugs = plugs
	}
	return nil
}

// escapeLike escapes the wildcard characters used by SQL LIKE so that a
// user-supplied search term doesn't inadvertently match extra rows. The
// returned string is intended to be wrapped with '%' and passed alongside
// an ESCAPE '\' clause.
func escapeLike(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\', '%', '_':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
