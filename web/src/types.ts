// Shared type declarations for the eCharge frontend. These mirror the Go
// backend JSON shapes plus a couple of derived client-side enums.

export type Tier = "ac" | "dc" | "hpc" | "ultra";

export type Availability = "available" | "occupied" | "offline";

export type SuggestKind = "Ort" | "Betreiber" | "Adresse";

export interface Ladepunkt {
  idx: number;
  steckertypen?: string;
  nennleistung?: number;
  evseId?: string;
}

export interface Station {
  id: number;
  betreiber: string;
  anzeigename?: string;
  status?: string;
  art?: string;
  anzahlLadepunkte: number;
  nennleistungKw: number;
  inbetriebnahme?: string;
  strasse?: string;
  hausnummer?: string;
  adresszusatz?: string;
  plz?: string;
  ort?: string;
  kreis?: string;
  bundesland?: string;
  lat: number;
  lng: number;
  standortbezeichnung?: string;
  parkraum?: string;
  bezahlsysteme?: string;
  oeffnungszeiten?: string;
  plugs?: string[];
  ladepunkte?: Ladepunkt[];
}

export interface Suggestion {
  kind: SuggestKind;
  label: string;
  value: string;
  stationId?: number;
}
