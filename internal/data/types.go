package data

// Station is a single Ladeeinrichtung from the Bundesnetzagentur registry.
type Station struct {
	ID                  int64   `json:"id"`
	Betreiber           string  `json:"betreiber"`
	Anzeigename         string  `json:"anzeigename,omitempty"`
	Status              string  `json:"status,omitempty"`
	Art                 string  `json:"art,omitempty"`
	AnzahlLadepunkte    int     `json:"anzahlLadepunkte"`
	NennleistungKW      float64 `json:"nennleistungKw"`
	Inbetriebnahme      string  `json:"inbetriebnahme,omitempty"`
	Strasse             string  `json:"strasse,omitempty"`
	Hausnummer          string  `json:"hausnummer,omitempty"`
	Adresszusatz        string  `json:"adresszusatz,omitempty"`
	PLZ                 string  `json:"plz,omitempty"`
	Ort                 string  `json:"ort,omitempty"`
	Kreis               string  `json:"kreis,omitempty"`
	Bundesland          string  `json:"bundesland,omitempty"`
	Breitengrad         float64 `json:"lat"`
	Laengengrad         float64 `json:"lng"`
	Standortbezeichnung string  `json:"standortbezeichnung,omitempty"`
	Parkraum            string  `json:"parkraum,omitempty"`
	Bezahlsysteme       string  `json:"bezahlsysteme,omitempty"`
	Oeffnungszeiten     string  `json:"oeffnungszeiten,omitempty"`
	// Plugs holds the normalised design-plug set present at the station.
	// Values are drawn from {"CCS","CHAdeMO","Typ 2","Schuko"}. Derived from
	// the per-Ladepunkt Steckertypen columns during CSV import.
	Plugs      []string    `json:"plugs,omitempty"`
	Ladepunkte []Ladepunkt `json:"ladepunkte,omitempty"`
}

// Ladepunkt is one charging point belonging to a Station (1..6 per station).
type Ladepunkt struct {
	Idx          int     `json:"idx"`
	Steckertypen string  `json:"steckertypen,omitempty"`
	Nennleistung float64 `json:"nennleistung,omitempty"`
	EvseID       string  `json:"evseId,omitempty"`
}

// SearchParams drives SearchStations.
// BBox (if set) filters by lat/lng rectangle [minLat, minLng, maxLat, maxLng].
// Betreiber filters exact on operator name. Q is a free-text prefix filter
// across anzeigename/ort/strasse. Limit caps the row count (0 => default).
type SearchParams struct {
	Betreiber string
	Q         string
	BBox      *BBox
	// Tiers filters by derived power class. Values: "ac","dc","hpc","ultra".
	// An empty slice means "all tiers". Tiers are OR-combined.
	Tiers []string
	// Plugs filters by normalised plug set. Values: "CCS","CHAdeMO","Typ 2","Schuko".
	// A station matches when ANY of its plugs appears in the requested set.
	Plugs []string
	// Status filters by raw Bundesnetzagentur status (e.g. "In Betrieb").
	// Empty slice means "any status".
	Status []string
	Limit  int
}

// Location is the result of Store.Geocode — a named geographic point derived
// from the registry (typically the first station matching a city name).
type Location struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
}

// Suggestion is one entry returned by Store.Suggest for the search autocomplete.
// Kind is a human-readable category label ("Ort", "Betreiber", "Adresse").
// Value is what the client should paste into the search input; StationID (if
// non-zero) lets the client navigate directly to that station.
type Suggestion struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Value     string `json:"value"`
	StationID int64  `json:"stationId,omitempty"`
}

type BBox struct {
	MinLat, MinLng, MaxLat, MaxLng float64
}
