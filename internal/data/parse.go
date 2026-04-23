package data

import (
	"bytes"
	"encoding/csv"
	"io"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

// headerFirstCell is the value of the first cell in the CSV header row.
// Every preceding row is preamble/notes and must be skipped.
const headerFirstCell = "Ladeeinrichtungs-ID"

// parsedRow is what we get back from parseRow: a station plus its Ladepunkte.
type parsedRow struct {
	Station    Station
	Ladepunkte []Ladepunkt
}

// newCSVReader returns a csv.Reader over the embedded registry, decoded from
// Windows-1252 to UTF-8 on the fly.
func newCSVReader(raw []byte) *csv.Reader {
	dec := charmap.Windows1252.NewDecoder()
	r := transform.NewReader(bytes.NewReader(raw), dec)
	cr := csv.NewReader(r)
	cr.Comma = ';'
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.ReuseRecord = true
	return cr
}

// skipPreamble advances the reader until (and including) the header row.
// Returns nil when the header has been consumed.
func skipPreamble(cr *csv.Reader) error {
	for {
		rec, err := cr.Read()
		if err != nil {
			return err
		}
		if len(rec) > 0 && strings.TrimSpace(rec[0]) == headerFirstCell {
			return nil
		}
	}
}

// parseFloatDE parses a decimal string using German conventions (comma as the
// decimal separator). Empty or unparseable input returns 0.
func parseFloatDE(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	s = strings.ReplaceAll(s, ".", "") // thousand separators, if any
	s = strings.ReplaceAll(s, ",", ".")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseInt parses a plain integer, returning 0 on empty/invalid input.
func parseInt(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return v
}

// parseInt64 parses a 64-bit integer. Returns 0, false for empty/invalid input.
func parseInt64(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// field returns the i-th field of rec or "" if out of range.
func field(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

// parseRow turns a single data row into a parsedRow. Returns (row, true) on
// success or (zero, false) if the row is unusable (e.g. missing/invalid ID).
func parseRow(rec []string) (parsedRow, bool) {
	id, ok := parseInt64(field(rec, 0))
	if !ok || id == 0 {
		return parsedRow{}, false
	}
	st := Station{
		ID:                  id,
		Betreiber:           field(rec, 1),
		Anzeigename:         field(rec, 2),
		Status:              field(rec, 3),
		Art:                 field(rec, 4),
		AnzahlLadepunkte:    parseInt(field(rec, 5)),
		NennleistungKW:      parseFloatDE(field(rec, 6)),
		Inbetriebnahme:      field(rec, 7),
		Strasse:             field(rec, 8),
		Hausnummer:          field(rec, 9),
		Adresszusatz:        field(rec, 10),
		PLZ:                 field(rec, 11),
		Ort:                 field(rec, 12),
		Kreis:               field(rec, 13),
		Bundesland:          field(rec, 14),
		Breitengrad:         parseFloatDE(field(rec, 15)),
		Laengengrad:         parseFloatDE(field(rec, 16)),
		Standortbezeichnung: field(rec, 17),
		Parkraum:            field(rec, 18),
		Bezahlsysteme:       field(rec, 19),
		Oeffnungszeiten:     field(rec, 20),
		// Columns 21 and 22 are "Öffnungszeiten: Wochentage/Tageszeiten".
		// We fold them into the Oeffnungszeiten string when they add info.
	}
	if extra := joinOpeningExtras(field(rec, 21), field(rec, 22)); extra != "" {
		if st.Oeffnungszeiten == "" {
			st.Oeffnungszeiten = extra
		} else {
			st.Oeffnungszeiten = st.Oeffnungszeiten + " (" + extra + ")"
		}
	}

	// Ladepunkte 1..6 live in columns 23..46 in blocks of 4:
	//   steckertypen, nennleistung, evse_id, public_key (ignored).
	var lps []Ladepunkt
	for i := 0; i < 6; i++ {
		base := 23 + i*4
		stecker := field(rec, base)
		nl := field(rec, base+1)
		ev := field(rec, base+2)
		if stecker == "" && nl == "" && ev == "" {
			continue
		}
		lps = append(lps, Ladepunkt{
			Idx:          i + 1,
			Steckertypen: stecker,
			Nennleistung: parseFloatDE(nl),
			EvseID:       ev,
		})
	}

	// Build deduplicated, insertion-ordered list of normalised design plugs
	// from every Ladepunkt's Steckertypen string. A single cell may list
	// several plugs separated by ';' inside the quoted CSV field.
	seen := make(map[string]bool)
	var plugs []string
	for _, lp := range lps {
		if lp.Steckertypen == "" {
			continue
		}
		for _, raw := range strings.Split(lp.Steckertypen, ";") {
			norm := normalizePlug(raw)
			if norm == "" {
				continue
			}
			if seen[norm] {
				continue
			}
			seen[norm] = true
			plugs = append(plugs, norm)
		}
	}
	if len(plugs) > 0 {
		// Stable order: sort alphabetically so equal plug sets serialise identically.
		sortStrings(plugs)
		st.Plugs = plugs
	}

	return parsedRow{Station: st, Ladepunkte: lps}, true
}

// normalizePlug maps a raw Steckertypen value from the BNetzA registry to one
// of the four design buckets ("CCS","CHAdeMO","Typ 2","Schuko") or "" when the
// plug should be dropped (currently the CEE variants).
//
// The ordering of the checks is load-bearing: strings like
// "DC Fahrzeugkupplung Typ Combo 2 (CCS)" also contain "Typ 2" and must fall
// into the CCS bucket; conversely "DC Tesla Fahrzeugkupplung (Typ 2)" has no
// CCS/Combo/MCS marker and so ends up in "Typ 2".
func normalizePlug(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	switch {
	case strings.Contains(s, "Schuko"):
		return "Schuko"
	case strings.Contains(s, "CHAdeMO"):
		return "CHAdeMO"
	case strings.Contains(s, "CCS") || strings.Contains(s, "Combo") || strings.Contains(s, "MCS"):
		return "CCS"
	case strings.Contains(s, "Typ 2") || strings.Contains(s, "Tesla") || strings.Contains(s, "Typ 1"):
		return "Typ 2"
	case strings.Contains(s, "CEE"):
		return ""
	}
	return ""
}

// sortStrings sorts a small slice in place without pulling in the sort package
// at this call site — the slices here are bounded to 4 entries.
func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

// joinOpeningExtras merges the weekday and time-of-day columns into one short
// readable string. Returns "" if both are empty.
func joinOpeningExtras(wochentage, tageszeiten string) string {
	switch {
	case wochentage == "" && tageszeiten == "":
		return ""
	case wochentage == "":
		return tageszeiten
	case tageszeiten == "":
		return wochentage
	default:
		return wochentage + " " + tageszeiten
	}
}

// iterRows reads every data row from the CSV and invokes fn for each one.
// The header row must already be consumed by the caller (see skipPreamble).
// fn is called with the raw record; the slice is reused between calls because
// the underlying reader has ReuseRecord=true — do not retain it.
func iterRows(cr *csv.Reader, fn func(rec []string) error) error {
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// Malformed line — skip it but keep going.
			if _, ok := err.(*csv.ParseError); ok {
				continue
			}
			return err
		}
		if err := fn(rec); err != nil {
			return err
		}
	}
}
