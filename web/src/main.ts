// Entrypoint for the eCharge frontend. Ported from the design prototype
// (charge-map/project/app.js) to TypeScript, with the in-memory mock dataset
// replaced by fetches to the real Go backend at /api/*.
//
// No module imports: Leaflet and Leaflet.markercluster are loaded from CDN
// <script> tags before this bundle, so `L` is a global. esbuild bundles this
// file to /assets/main.js.

declare const L: any;

// --- Types (inlined; mirrors web/src/types.ts) -----------------------------

type Tier = "ac" | "dc" | "hpc" | "ultra";
type Availability = "available" | "occupied" | "offline";
type SuggestKind = "Ort" | "Betreiber" | "Adresse";

interface Ladepunkt {
  idx: number;
  steckertypen?: string;
  nennleistung?: number;
  evseId?: string;
}

interface Station {
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

interface Suggestion {
  kind: SuggestKind;
  label: string;
  value: string;
  stationId?: number;
}

// --- Small utilities -------------------------------------------------------

async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(url, { signal });
  if (!res.ok) throw new Error(`HTTP ${res.status} for ${url}`);
  return (await res.json()) as T;
}

function debounce<F extends (...a: any[]) => any>(fn: F, ms: number): F {
  let t: ReturnType<typeof setTimeout> | null = null;
  return function (this: unknown, ...args: any[]) {
    if (t !== null) clearTimeout(t);
    t = setTimeout(() => {
      t = null;
      fn.apply(this, args);
    }, ms);
  } as F;
}

function escapeHtml(v: string): string {
  return v
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// --- Derived fields --------------------------------------------------------

function tierOf(kw: number): Tier {
  if (kw >= 300) return "ultra";
  if (kw >= 150) return "hpc";
  if (kw >= 50) return "dc";
  return "ac";
}

function tierLabel(t: Tier): string {
  return (
    {
      ac: "AC · Normal",
      dc: "DC · Schnell",
      hpc: "HPC · Hochleistung",
      ultra: "Ultra · 300+ kW",
    } as Record<Tier, string>
  )[t];
}

function availLabel(a: Availability): string {
  return { available: "Verfügbar", occupied: "Belegt", offline: "Offline" }[a];
}

function availSub(a: Availability): string {
  return {
    available: "Bereit zum Laden",
    occupied: "Gerade in Benutzung",
    offline: "Derzeit nicht erreichbar",
  }[a];
}

function plugAbbr(p: string): string {
  return (
    ({ CCS: "CCS", CHAdeMO: "CHA", "Typ 2": "T2", Schuko: "SCH" } as Record<
      string,
      string
    >)[p] || p
  );
}

function operatorShort(full: string): string {
  const cleaned = full
    .replace(/\s+(GmbH|AG|KG|Co\. ?KG|und Co\. ?KG|eG|e\.V\.|mbH|SE)(\s|$)/gi, " ")
    .trim();
  return cleaned.split(/\s+/)[0].slice(0, 18);
}

function availabilityOf(status?: string): Availability {
  if (status && /außer|nicht|gekündigt|gestört/i.test(status)) return "offline";
  if (status === "In Betrieb") return "available";
  return "offline";
}

function addressOf(s: Station): string {
  const street = [s.strasse, s.hausnummer].filter(Boolean).join(" ");
  const cityPart = [s.plz, s.ort].filter(Boolean).join(" ");
  return [street, cityPart].filter(Boolean).join(", ");
}

// --- Main ------------------------------------------------------------------

document.addEventListener("DOMContentLoaded", () => {
  // ---------- State ----------
  const state = {
    tab: "search" as "search" | "route",
    query: "" as string,
    operator: "all" as string,
    tiers: new Set<string>(["ac", "dc", "hpc", "ultra"]),
    plugs: new Set<string>(),
    availability: new Set<string>(),
    selectedId: null as number | null,
    acFocused: -1 as number,
  };

  let currentStations: Station[] = [];
  let acItems: Suggestion[] = [];

  // Abort controllers (one per fetch kind; new request cancels previous)
  let stationsAbort: AbortController | null = null;
  let suggestAbort: AbortController | null = null;
  let betreiberAbort: AbortController | null = null;

  // Guards a programmatic fitBounds so its moveend doesn't trigger a refetch.
  let suppressNextMoveend = false;

  // ---------- DOM ----------
  const $ = <T extends HTMLElement = HTMLElement>(sel: string): T => {
    const el = document.querySelector<T>(sel);
    if (!el) throw new Error(`${sel} not found`);
    return el;
  };

  const searchInput = $<HTMLInputElement>("#search");
  const searchClear = $<HTMLButtonElement>("#search-clear");
  const autocomplete = $<HTMLDivElement>("#autocomplete");
  const operatorSelect = $<HTMLSelectElement>("#operator");
  const tierChips = document.querySelectorAll<HTMLElement>(".chip[data-tier]");
  const plugChips = document.querySelectorAll<HTMLElement>(".chip[data-plug]");
  const availChips = document.querySelectorAll<HTMLElement>(".chip[data-avail]");
  const resultsEl = $<HTMLDivElement>("#results");
  const resultsCount = $<HTMLElement>("#results-count");
  const stationCountEl = $<HTMLElement>("#station-count");
  const detailPanel = $<HTMLDivElement>("#detail-panel");
  const tabs = document.querySelectorAll<HTMLElement>(".tab");
  const panels = document.querySelectorAll<HTMLElement>(".panel");
  const routeStart = $<HTMLInputElement>("#route-start");
  const routeEnd = $<HTMLInputElement>("#route-end");
  const rangeSlider = $<HTMLInputElement>("#range-slider");
  const rangeValue = $<HTMLElement>("#range-value");
  const socSlider = $<HTMLInputElement>("#soc-slider");
  const socValue = $<HTMLElement>("#soc-value");
  const planBtn = $<HTMLButtonElement>("#plan-btn");
  const routeSummary = $<HTMLDivElement>("#route-summary");

  // Read total station count that templ has embedded in the DOM at page load.
  const totalStations = (() => {
    const s = stationCountEl.textContent || "";
    const m = s.match(/[\d.]+/);
    return m ? parseInt(m[0].replace(/\./g, ""), 10) : 0;
  })();

  // ---------- Map ----------
  const map = L.map("map", {
    center: [51.1657, 10.4515],
    zoom: 6,
    zoomControl: false,
  });
  L.control.zoom({ position: "topright" }).addTo(map);

  L.tileLayer("https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png", {
    attribution: "© OpenStreetMap · © CARTO",
    subdomains: "abcd",
    maxZoom: 19,
  }).addTo(map);

  // ---------- Marker icons ----------
  const boltSVG =
    '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M13 2L3 14h7l-1 8 10-12h-7l1-8z"/></svg>';

  function makeStationIcon(tier: Tier, selected: boolean): any {
    return L.divIcon({
      className: "station-marker-wrap",
      html: `<div class="station-marker${selected ? " selected" : ""}" data-tier="${tier}">${boltSVG}</div>`,
      iconSize: [30, 30],
      iconAnchor: [15, 30],
      popupAnchor: [0, -28],
    });
  }
  function makeClusterIcon(count: number): any {
    const size =
      count >= 1000 ? "huge" : count >= 100 ? "large" : count >= 20 ? "" : "small";
    const label = count >= 1000 ? (count / 1000).toFixed(1) + "k" : String(count);
    return L.divIcon({
      className: "station-marker-wrap",
      html: `<div class="cluster-marker ${size}">${label}</div>`,
      iconSize: [40, 40],
    });
  }

  // Single cluster group — the old dual-layer split is gone.
  const markerCluster = L.markerClusterGroup({
    showCoverageOnHover: false,
    spiderfyOnMaxZoom: true,
    maxClusterRadius: 60,
    iconCreateFunction: (cluster: any) => makeClusterIcon(cluster.getChildCount()),
  });
  map.addLayer(markerCluster);

  const markers = new Map<number, any>();

  // ---------- Filter active helper ----------
  function isFilterActive(): boolean {
    if (state.operator !== "all") return true;
    // Tiers: only "active" (restricts results) if a strict subset is chosen.
    if (state.tiers.size > 0 && state.tiers.size < 4) return true;
    if (state.plugs.size > 0) return true;
    if (state.availability.size > 0) return true;
    if (state.query.trim().length >= 2) return true;
    return false;
  }

  // ---------- Query param builder ----------
  function buildStationsQuery(includeBbox: boolean): string {
    const p = new URLSearchParams();
    if (state.operator !== "all") p.set("betreiber", state.operator);
    const q = state.query.trim();
    if (q.length >= 2) p.set("q", q);
    if (state.tiers.size > 0 && state.tiers.size < 4) {
      p.set("tiers", Array.from(state.tiers).join(","));
    }
    if (state.plugs.size > 0) {
      p.set("plugs", Array.from(state.plugs).join(","));
    }
    if (state.availability.size > 0) {
      const statuses: string[] = [];
      if (state.availability.has("available")) statuses.push("In Betrieb");
      if (state.availability.has("offline")) statuses.push("Außer Betrieb");
      // "occupied" isn't a concept in the dataset: send a sentinel that never matches.
      if (state.availability.has("occupied")) statuses.push("__NEVER_MATCHES__");
      if (statuses.length > 0) p.set("status", statuses.join(","));
    }
    if (includeBbox) {
      const b = map.getBounds();
      p.set(
        "bbox",
        `${b.getSouth()},${b.getWest()},${b.getNorth()},${b.getEast()}`,
      );
    }
    p.set("limit", "2000");
    return p.toString();
  }

  // ---------- Apply filters ----------
  async function applyFilters(): Promise<void> {
    const filterActive = isFilterActive();

    // Low-zoom gate when nothing is filtered: don't flood the map.
    if (!filterActive && map.getZoom() < 7) {
      markerCluster.clearLayers();
      markers.clear();
      currentStations = [];
      resultsCount.innerHTML = `<b>0</b> von ${totalStations.toLocaleString("de-DE")}`;
      resultsEl.innerHTML =
        '<div class="results-empty">' +
        '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>' +
        "<div>Bitte weiter hineinzoomen oder filtern</div>" +
        '<div style="font-size:11px;margin-top:6px">Ab Zoomstufe 7 werden Stationen angezeigt</div>' +
        "</div>";
      return;
    }

    const url = `/api/stations?${buildStationsQuery(!filterActive)}`;

    if (stationsAbort) stationsAbort.abort();
    stationsAbort = new AbortController();

    let stations: Station[];
    try {
      stations = await getJSON<Station[]>(url, stationsAbort.signal);
    } catch (err) {
      if ((err as DOMException | Error).name === "AbortError") return;
      console.error("stations fetch failed", err);
      return;
    }

    currentStations = stations;
    rebuildMarkers(stations);
    renderResults(stations);

    if (filterActive && stations.length > 0) {
      const pts: Array<[number, number]> = [];
      for (const s of stations) {
        if (s.lat !== 0 && s.lng !== 0) pts.push([s.lat, s.lng]);
      }
      if (pts.length > 0) {
        suppressNextMoveend = true;
        map.fitBounds(pts, { padding: [40, 40], maxZoom: 14 });
      }
    }
  }

  // ---------- Render markers ----------
  function rebuildMarkers(stations: Station[]): void {
    markerCluster.clearLayers();
    markers.clear();
    const bulk: any[] = [];
    for (const s of stations) {
      if (s.lat === 0 && s.lng === 0) continue;
      const marker = L.marker([s.lat, s.lng], {
        icon: makeStationIcon(tierOf(s.nennleistungKw), s.id === state.selectedId),
      });
      marker.stationId = s.id;
      marker.on("click", () => void selectStation(s.id, true));
      markers.set(s.id, marker);
      bulk.push(marker);
    }
    markerCluster.addLayers(bulk);
  }

  // ---------- Render results list ----------
  function renderResults(stations: Station[]): void {
    resultsCount.innerHTML = `<b>${stations.length.toLocaleString("de-DE")}</b> von ${totalStations.toLocaleString("de-DE")}`;

    if (stations.length === 0) {
      resultsEl.innerHTML =
        '<div class="results-empty">' +
        '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>' +
        "<div>Keine Treffer</div>" +
        '<div style="font-size:11px;margin-top:6px">Filter oder Suchbegriff anpassen</div>' +
        "</div>";
      return;
    }

    const show = stations.slice(0, 200);
    const frag = document.createDocumentFragment();

    for (const s of show) {
      const tier = tierOf(s.nennleistungKw);
      const avail = availabilityOf(s.status);
      const opShort = operatorShort(s.betreiber || "");
      const plugs = s.plugs || [];
      const el = document.createElement("div");
      el.className = "result" + (s.id === state.selectedId ? " selected" : "");
      el.dataset.id = String(s.id);
      const addrLine = [
        [s.strasse, s.hausnummer].filter(Boolean).join(" "),
        [s.plz, s.ort].filter(Boolean).join(" "),
      ]
        .filter(Boolean)
        .join(", ");
      el.innerHTML =
        `<div class="result-icon" data-tier="${tier}">${boltSVG}</div>` +
        '<div class="result-body">' +
        '<div class="result-top">' +
        `<div class="result-operator">${escapeHtml(opShort)}</div>` +
        `<div class="result-kw" data-tier="${tier}">${s.nennleistungKw} kW</div>` +
        "</div>" +
        `<div class="result-addr">${escapeHtml(addrLine)}</div>` +
        '<div class="result-meta">' +
        `<span class="avail ${avail}">${availLabel(avail)}</span>` +
        `<span class="plugs">${plugs.map((p) => escapeHtml(plugAbbr(p))).join(" · ")}</span>` +
        `<span style="margin-left:auto">${s.anzahlLadepunkte}×</span>` +
        "</div>" +
        "</div>";
      el.addEventListener("click", () => void selectStation(s.id, true));
      frag.appendChild(el);
    }
    resultsEl.innerHTML = "";
    resultsEl.appendChild(frag);

    if (stations.length > 200) {
      const more = document.createElement("div");
      more.style.cssText =
        "text-align:center;padding:12px;font-size:11px;color:var(--fg-3);";
      more.textContent = `+ ${(stations.length - 200).toLocaleString(
        "de-DE",
      )} weitere (für volle Liste eingrenzen)`;
      resultsEl.appendChild(more);
    }
  }

  // ---------- Selection / detail panel ----------
  async function selectStation(
    id: number,
    flyTo: boolean,
    opts: { preserveZoom?: boolean } = {},
  ): Promise<void> {
    state.selectedId = id;

    // Update marker highlight for currently-rendered batch.
    for (const [sid, m] of markers) {
      const st = currentStations.find((x) => x.id === sid);
      if (!st) continue;
      m.setIcon(makeStationIcon(tierOf(st.nennleistungKw), sid === id));
    }

    document
      .querySelectorAll<HTMLElement>(".result")
      .forEach((r) => r.classList.toggle("selected", Number(r.dataset.id) === id));
    const selEl = document.querySelector<HTMLElement>(
      `.result[data-id="${id}"]`,
    );
    if (selEl) selEl.scrollIntoView({ block: "nearest", behavior: "smooth" });

    // Fetch full station detail.
    let station: Station;
    try {
      station = await getJSON<Station>(`/api/stations/${id}`);
    } catch (err) {
      console.error("station detail failed", err);
      return;
    }

    if (flyTo) {
      if (opts.preserveZoom) {
        // Clicking a route stop: keep the route-overview zoom so the whole
        // polyline stays in frame. Just pan.
        map.panTo([station.lat, station.lng], { animate: true, duration: 0.6 });
      } else {
        const targetZoom = Math.max(map.getZoom(), 14);
        map.flyTo([station.lat, station.lng], targetZoom, { duration: 0.8 });
        setTimeout(() => {
          const m = markers.get(id);
          if (m && markerCluster.hasLayer(m)) {
            markerCluster.zoomToShowLayer(m, () => {});
          }
        }, 400);
      }
    }

    openDetail(station);
  }

  function openDetail(s: Station): void {
    const tier = tierOf(s.nennleistungKw);
    const avail = availabilityOf(s.status);
    const plugs = s.plugs || [];
    const stromart = s.art || (tier === "ac" ? "AC" : "DC");
    const inbetrieb = s.inbetriebnahme || "–";
    const addr = addressOf(s);

    const availIconSvg =
      avail === "available"
        ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>'
        : avail === "occupied"
          ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>'
          : '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18.36 6.64A9 9 0 0 1 20.77 15"/><path d="M6.16 6.16a9 9 0 1 0 12.68 12.68"/><path d="M12 2v4"/><line x1="2" y1="2" x2="22" y2="22"/></svg>';

    const plugListHtml = plugs
      .map(
        (p) =>
          '<div class="plug-item">' +
          `<div class="plug-icon">${plugIconSVG(p)}</div>` +
          '<div class="plug-info">' +
          `<div class="n">${escapeHtml(p)}</div>` +
          `<div class="s">${escapeHtml(plugDescription(p, s.nennleistungKw))}</div>` +
          "</div>" +
          "</div>",
      )
      .join("");

    detailPanel.dataset.tier = tier;
    detailPanel.innerHTML =
      `<div class="detail-hero" data-tier="${tier}">` +
      '<div class="detail-hero-bg"></div>' +
      '<button class="detail-close" id="detail-close" aria-label="Schließen">' +
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>' +
      "</button>" +
      '<div class="detail-hero-content">' +
      `<span class="detail-tier">${tierLabel(tier)}</span>` +
      `<div class="detail-kw-big">${s.nennleistungKw}<span class="unit">kW</span></div>` +
      "</div>" +
      "</div>" +
      '<div class="detail-body">' +
      '<div class="detail-head">' +
      `<h2>${escapeHtml(s.anzeigename || s.betreiber || `Station ${s.id}`)}</h2>` +
      `<p>${escapeHtml(addr)}</p>` +
      "</div>" +
      '<div class="detail-avail">' +
      `<div class="detail-avail-icon ${avail}">${availIconSvg}</div>` +
      '<div class="detail-avail-text">' +
      `<div class="t">${availLabel(avail)}</div>` +
      `<div class="s">${availSub(avail)} · ${s.anzahlLadepunkte} Ladepunkt${s.anzahlLadepunkte === 1 ? "" : "e"}</div>` +
      "</div>" +
      "</div>" +
      '<div class="detail-section">' +
      "<h3>Stecker-Typen</h3>" +
      `<div class="plug-list">${plugListHtml}</div>` +
      "</div>" +
      '<div class="detail-section">' +
      "<h3>Station</h3>" +
      '<div class="meta-grid">' +
      `<div class="meta-cell"><div class="l">Leistung</div><div class="v">${s.nennleistungKw} kW</div></div>` +
      `<div class="meta-cell"><div class="l">Ladepunkte</div><div class="v">${s.anzahlLadepunkte}</div></div>` +
      `<div class="meta-cell"><div class="l">Stromart</div><div class="v">${escapeHtml(stromart)}</div></div>` +
      `<div class="meta-cell"><div class="l">Inbetriebnahme</div><div class="v">${escapeHtml(inbetrieb)}</div></div>` +
      "</div>" +
      "</div>" +
      '<div class="detail-actions">' +
      '<button class="btn-secondary" id="btn-route">' +
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="6" cy="19" r="3"/><path d="M9 19h8.5a3.5 3.5 0 0 0 0-7h-11a3.5 3.5 0 0 1 0-7H15"/><circle cx="18" cy="5" r="3"/></svg>' +
      "Route" +
      "</button>" +
      '<button class="btn-primary">' +
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>' +
      "Navigieren" +
      "</button>" +
      "</div>" +
      "</div>";

    detailPanel.classList.add("open");

    detailPanel
      .querySelector<HTMLButtonElement>("#detail-close")
      ?.addEventListener("click", closeDetail);

    detailPanel
      .querySelector<HTMLButtonElement>("#btn-route")
      ?.addEventListener("click", () => {
        switchTab("route");
        routeEnd.value = addr;
      });
  }

  function closeDetail(): void {
    detailPanel.classList.remove("open");
    state.selectedId = null;
    for (const [sid, m] of markers) {
      const st = currentStations.find((x) => x.id === sid);
      if (!st) continue;
      m.setIcon(makeStationIcon(tierOf(st.nennleistungKw), false));
    }
    document
      .querySelectorAll<HTMLElement>(".result.selected")
      .forEach((r) => r.classList.remove("selected"));
  }

  function plugIconSVG(_type: string): string {
    // All plug types use the same bolt glyph in the prototype.
    return '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M13 2 3 14h7l-1 8 10-12h-7l1-8z"/></svg>';
  }
  function plugDescription(p: string, kw: number): string {
    if (p === "CCS") return "DC bis " + kw + " kW";
    if (p === "CHAdeMO") return "DC Japan Standard";
    if (p === "Typ 2") return "AC Mennekes";
    if (p === "Schuko") return "Haushaltssteckdose";
    return "";
  }

  // ---------- Autocomplete ----------
  async function fetchSuggestions(): Promise<void> {
    const q = state.query.trim();
    if (q.length < 2) {
      acItems = [];
      renderAutocomplete(acItems);
      return;
    }

    if (suggestAbort) suggestAbort.abort();
    suggestAbort = new AbortController();

    let items: Suggestion[];
    try {
      items = await getJSON<Suggestion[]>(
        `/api/suggest?q=${encodeURIComponent(q)}&limit=8`,
        suggestAbort.signal,
      );
    } catch (err) {
      if ((err as DOMException | Error).name === "AbortError") return;
      console.error("suggest fetch failed", err);
      return;
    }
    acItems = items;
    renderAutocomplete(items);
  }

  function suggestIconKey(kind: SuggestKind): "city" | "op" | "pin" {
    if (kind === "Ort") return "city";
    if (kind === "Betreiber") return "op";
    return "pin";
  }

  function renderAutocomplete(items: Suggestion[]): void {
    if (items.length === 0) {
      autocomplete.style.display = "none";
      autocomplete.innerHTML = "";
      return;
    }
    const icons: Record<"city" | "op" | "pin", string> = {
      city:
        '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="16" height="20" x="4" y="2" rx="2"/><path d="M9 22v-4h6v4"/><path d="M8 6h.01M16 6h.01M12 6h.01M12 10h.01M12 14h.01M16 10h.01M16 14h.01M8 10h.01M8 14h.01"/></svg>',
      op:
        '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>',
      pin:
        '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 10c0 6-8 12-8 12s-8-6-8-12a8 8 0 0 1 16 0"/><circle cx="12" cy="10" r="3"/></svg>',
    };
    autocomplete.innerHTML = items
      .map(
        (it, i) =>
          `<div class="ac-item${i === state.acFocused ? " focused" : ""}" data-idx="${i}">` +
          `<div class="ac-icon">${icons[suggestIconKey(it.kind)]}</div>` +
          '<div class="ac-text">' +
          `<div class="ac-primary">${highlight(it.label, state.query)}</div>` +
          "</div>" +
          `<div class="ac-kind">${escapeHtml(it.kind)}</div>` +
          "</div>",
      )
      .join("");
    autocomplete.style.display = "block";
    autocomplete.querySelectorAll<HTMLElement>(".ac-item").forEach((el, i) => {
      el.addEventListener("click", () => void applySuggestion(items[i]));
    });
  }

  function highlight(text: string, q: string): string {
    q = q.trim();
    if (!q) return escapeHtml(text);
    const idx = text.toLowerCase().indexOf(q.toLowerCase());
    if (idx < 0) return escapeHtml(text);
    return (
      escapeHtml(text.slice(0, idx)) +
      '<b style="color:var(--brand)">' +
      escapeHtml(text.slice(idx, idx + q.length)) +
      "</b>" +
      escapeHtml(text.slice(idx + q.length))
    );
  }

  async function applySuggestion(it: Suggestion): Promise<void> {
    if (it.kind === "Betreiber") {
      operatorSelect.value = it.value;
      state.operator = it.value;
      searchInput.value = "";
      state.query = "";
      searchClear.style.display = "none";
    } else {
      searchInput.value = it.value;
      state.query = it.value;
      searchClear.style.display = "grid";
    }
    autocomplete.style.display = "none";
    await applyFilters();
    if (it.kind === "Adresse" && it.stationId) {
      await selectStation(it.stationId, true);
    }
  }

  // ---------- Tabs ----------
  function switchTab(t: "search" | "route"): void {
    state.tab = t;
    tabs.forEach((x) => x.classList.toggle("active", x.dataset.tab === t));
    panels.forEach((x) => {
      x.style.display = x.dataset.panel === t ? "" : "none";
    });
    const resultsWrap = document.getElementById("results-wrap");
    const routeSummaryWrap = document.getElementById("route-summary-wrap");
    if (resultsWrap) resultsWrap.style.display = t === "search" ? "" : "none";
    if (routeSummaryWrap)
      routeSummaryWrap.style.display = t === "route" ? "" : "none";
  }

  // ---------- Route planning ----------
  interface GeoPoint {
    name: string;
    coord: [number, number];
  }

  async function geocode(text: string): Promise<GeoPoint | null> {
    const q = text.trim();
    if (!q) return null;
    try {
      const res = await fetch(
        `/api/geocode?q=${encodeURIComponent(q)}`,
      );
      if (res.status === 404) return null;
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const loc = (await res.json()) as {
        name: string;
        lat: number;
        lng: number;
      };
      return { name: loc.name, coord: [loc.lat, loc.lng] };
    } catch (err) {
      console.error("geocode failed", err);
      return null;
    }
  }

  function distanceKm(a: [number, number], b: [number, number]): number {
    const R = 6371;
    const dLat = ((b[0] - a[0]) * Math.PI) / 180;
    const dLon = ((b[1] - a[1]) * Math.PI) / 180;
    const h =
      Math.sin(dLat / 2) ** 2 +
      Math.cos((a[0] * Math.PI) / 180) *
        Math.cos((b[0] * Math.PI) / 180) *
        Math.sin(dLon / 2) ** 2;
    return 2 * R * Math.asin(Math.sqrt(h));
  }
  function projectTkm(
    p: [number, number],
    a: [number, number],
    b: [number, number],
  ): number {
    const ax = a[1],
      ay = a[0],
      bx = b[1],
      by = b[0];
    const px = p[1],
      py = p[0];
    const dx = bx - ax,
      dy = by - ay;
    const denom = dx * dx + dy * dy;
    if (denom === 0) return 0;
    const t = ((px - ax) * dx + (py - ay) * dy) / denom;
    return Math.max(0, Math.min(1, t));
  }
  function distanceToSegmentKm(
    p: [number, number],
    a: [number, number],
    b: [number, number],
  ): number {
    const t = projectTkm(p, a, b);
    const proj: [number, number] = [
      a[0] + t * (b[0] - a[0]),
      a[1] + t * (b[1] - a[1]),
    ];
    return distanceKm(p, proj);
  }

  let routeLayer: any = null;
  let routeStopMarkers: any[] = [];

  // OSRM public demo endpoint (rate-limited; fine for a small app).
  const OSRM_URL = "https://router.project-osrm.org/route/v1/driving";

  interface OSRMRoute {
    polyline: [number, number][]; // [lat,lng] pairs
    cumul: number[]; // cumulative km at each polyline vertex
    distanceKm: number;
    durationMin: number;
  }

  async function fetchOSRMRoute(
    start: [number, number],
    end: [number, number],
  ): Promise<OSRMRoute | null> {
    const url =
      `${OSRM_URL}/${start[1]},${start[0]};${end[1]},${end[0]}` +
      `?overview=full&geometries=geojson&steps=false`;
    try {
      const res = await fetch(url);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const body = (await res.json()) as {
        code: string;
        routes: Array<{
          distance: number;
          duration: number;
          geometry: { coordinates: Array<[number, number]> };
        }>;
      };
      if (body.code !== "Ok" || !body.routes[0]) return null;
      const r = body.routes[0];
      // GeoJSON is [lng,lat] — flip for Leaflet.
      const polyline: [number, number][] = r.geometry.coordinates.map(
        (c) => [c[1], c[0]],
      );
      const cumul: number[] = new Array(polyline.length);
      cumul[0] = 0;
      for (let i = 1; i < polyline.length; i++) {
        cumul[i] =
          cumul[i - 1] + distanceKm(polyline[i - 1], polyline[i]);
      }
      return {
        polyline,
        cumul,
        distanceKm: r.distance / 1000,
        durationMin: r.duration / 60,
      };
    } catch (err) {
      console.error("OSRM fetch failed", err);
      return null;
    }
  }

  function pointAtDistance(
    polyline: [number, number][],
    cumul: number[],
    d: number,
  ): [number, number] {
    if (d <= 0) return polyline[0];
    if (d >= cumul[cumul.length - 1]) return polyline[polyline.length - 1];
    let lo = 0,
      hi = cumul.length - 1;
    while (hi - lo > 1) {
      const mid = (lo + hi) >> 1;
      if (cumul[mid] <= d) lo = mid;
      else hi = mid;
    }
    const segLen = cumul[hi] - cumul[lo];
    const t = segLen > 0 ? (d - cumul[lo]) / segLen : 0;
    return [
      polyline[lo][0] + t * (polyline[hi][0] - polyline[lo][0]),
      polyline[lo][1] + t * (polyline[hi][1] - polyline[lo][1]),
    ];
  }

  async function planRoute(): Promise<void> {
    const [startG, endG] = await Promise.all([
      geocode(routeStart.value || "Berlin"),
      geocode(routeEnd.value || "München"),
    ]);
    if (!startG) {
      alert(`Startort nicht gefunden: "${routeStart.value}"`);
      return;
    }
    if (!endG) {
      alert(`Zielort nicht gefunden: "${routeEnd.value}"`);
      return;
    }
    const range = +rangeSlider.value;
    const soc = +socSlider.value;

    if (routeLayer) map.removeLayer(routeLayer);
    routeStopMarkers.forEach((m) => map.removeLayer(m));
    routeStopMarkers = [];

    // UX hint while OSRM crunches.
    routeSummary.innerHTML =
      '<div style="font-size:13px;color:var(--fg-3);text-align:center;padding:16px">Route wird berechnet…</div>';
    routeSummary.style.display = "";

    const route = await fetchOSRMRoute(startG.coord, endG.coord);
    if (!route) {
      alert("Route konnte nicht berechnet werden (OSRM-Dienst nicht erreichbar).");
      routeSummary.style.display = "none";
      return;
    }

    routeLayer = L.polyline(route.polyline, {
      color: "#4ad1ff",
      weight: 4,
      opacity: 0.9,
      smoothFactor: 1.5,
    }).addTo(map);

    const totalDist = route.distanceKm;
    const initialRange = range * (soc / 100);
    const usableStep = range * 0.7;
    const stepsCount = Math.max(
      0,
      Math.ceil((totalDist - initialRange + range * 0.2) / usableStep),
    );

    const targetDistances: number[] = [];
    let travelled = 0;
    let remaining = initialRange;
    for (let i = 0; i < stepsCount; i++) {
      const td = Math.min(totalDist - 30, travelled + remaining - 20);
      targetDistances.push(td);
      travelled = td;
      remaining = range * 0.7;
    }

    // Corridor candidates: HPC/Ultra stations. Project each onto the polyline
    // via a sampled anchor grid so we know both (a) how far off-route it is,
    // and (b) where it sits along the route (km from start).
    let candidates: Station[] = [];
    try {
      candidates = await getJSON<Station[]>(
        "/api/stations?tiers=hpc,ultra&limit=2000",
      );
    } catch (err) {
      console.error("route corridor fetch failed", err);
    }

    const anchorStepKm = 2; // finer sampling → better off-route estimation
    const anchorCount = Math.max(
      2,
      Math.ceil(totalDist / anchorStepKm) + 1,
    );
    const anchors: Array<{ d: number; pt: [number, number] }> = [];
    for (let i = 0; i < anchorCount; i++) {
      const d = Math.min(totalDist, i * anchorStepKm);
      anchors.push({ d, pt: pointAtDistance(route.polyline, route.cumul, d) });
    }

    interface Corridor {
      s: Station;
      offroute: number; // km from nearest anchor (≈ straight-line detour one-way)
      progress: number; // km from start along route
    }

    // Corridor radius scales with range so short-range cars aren't sent on
    // 30 km detours. Always keep an upper bound — a HPC 12 km off a 500 km
    // trip is fine; 12 km off a 120 km trip is already a big detour.
    const corridorRadius = Math.max(5, Math.min(12, range * 0.08));

    const corridor: Corridor[] = [];
    for (const s of candidates) {
      if (s.lat === 0 && s.lng === 0) continue;
      let bestD = Infinity;
      let bestProgress = 0;
      for (const a of anchors) {
        const d = distanceKm([s.lat, s.lng], a.pt);
        if (d < bestD) {
          bestD = d;
          bestProgress = a.d;
        }
      }
      if (
        bestD < corridorRadius &&
        bestProgress > totalDist * 0.05 &&
        bestProgress < totalDist * 0.95
      ) {
        corridor.push({ s, offroute: bestD, progress: bestProgress });
      }
    }

    // Pick stops. For each target distance td, only consider candidates we
    // can actually reach (progress ≤ td) and that we aren't already past
    // (progress > last chosen). Then rank by a weighted cost that heavily
    // penalises off-route detours — a station directly on the motorway beats
    // a station 10 km away even if the latter sits closer to target distance.
    const chosen: Corridor[] = [];
    for (const td of targetDistances) {
      const lastProgress =
        chosen.length > 0 ? chosen[chosen.length - 1].progress : 0;
      const windowMin = lastProgress + 20; // don't pick a station right next to the previous
      const windowMax = td; // must reach it before running out
      const eligible = corridor.filter(
        (c) =>
          c.progress >= windowMin &&
          c.progress <= windowMax &&
          !chosen.includes(c),
      );
      if (eligible.length === 0) continue;
      // Cost: off-route distance dominates (×3), then arrival window slack.
      eligible.sort(
        (a, b) =>
          a.offroute * 3 +
          (td - a.progress) -
          (b.offroute * 3 + (td - b.progress)),
      );
      chosen.push(eligible[0]);
    }
    chosen.sort((a, b) => a.progress - b.progress);

    chosen.forEach((c, i) => {
      const icon = L.divIcon({
        className: "station-marker-wrap",
        html: `<div class="cluster-marker" style="background:var(--hpc);color:#0b0f14;border-color:var(--hpc);width:34px;height:34px;font-size:13px">${i + 1}</div>`,
        iconSize: [34, 34],
      });
      const m = L.marker([c.s.lat, c.s.lng], { icon }).addTo(map);
      m.on("click", () =>
        void selectStation(c.s.id, true, { preserveZoom: true }),
      );
      routeStopMarkers.push(m);
    });
    const sM = L.circleMarker(startG.coord, {
      radius: 8,
      color: "#53e08a",
      fillColor: "#53e08a",
      fillOpacity: 1,
      weight: 3,
    }).addTo(map);
    const eM = L.circleMarker(endG.coord, {
      radius: 8,
      color: "#4ad1ff",
      fillColor: "#4ad1ff",
      fillOpacity: 1,
      weight: 3,
    }).addTo(map);
    routeStopMarkers.push(sM, eM);

    map.flyToBounds(L.latLngBounds(route.polyline).pad(0.15), {
      duration: 0.8,
    });

    const drivingMin = Math.round(route.durationMin);
    const chargingMin = chosen.length * 22;
    routeSummary.innerHTML =
      '<div class="route-summary-top">' +
      `<div class="route-summary-stat"><div class="v">${Math.round(totalDist)} km</div><div class="l">Distanz</div></div>` +
      `<div class="route-summary-stat"><div class="v">${Math.floor(drivingMin / 60)} h ${drivingMin % 60} min</div><div class="l">Fahrzeit</div></div>` +
      `<div class="route-summary-stat"><div class="v">${chosen.length}</div><div class="l">Ladestopps</div></div>` +
      "</div>" +
      (chosen.length > 0
        ? '<div class="route-stops-list">' +
          chosen
            .map(
              (c, i) =>
                `<div class="route-stop-row" data-id="${c.s.id}" style="cursor:pointer">` +
                `<div class="num">${i + 1}</div>` +
                `<div class="name">${escapeHtml(operatorShort(c.s.betreiber || ""))} · ${escapeHtml(c.s.ort || "")}</div>` +
                `<div class="mini">${c.s.nennleistungKw} kW · ~22 min</div>` +
                "</div>",
            )
            .join("") +
          "</div>"
        : '<div style="font-size:12px;color:var(--fg-3);text-align:center;padding:6px">Keine Ladestopps notwendig</div>') +
      '<div style="display:flex;gap:8px;font-size:11px;color:var(--fg-3);border-top:1px solid var(--border-1);padding-top:10px">' +
      `<span>+${chargingMin} min Ladezeit</span>` +
      `<span style="margin-left:auto">Gesamt: ${Math.floor((drivingMin + chargingMin) / 60)} h ${(drivingMin + chargingMin) % 60} min</span>` +
      "</div>";
    routeSummary.querySelectorAll<HTMLElement>(".route-stop-row").forEach((r) => {
      r.addEventListener("click", () => {
        const id = Number(r.dataset.id);
        if (Number.isFinite(id))
          void selectStation(id, true, { preserveZoom: true });
      });
    });
  }

  // ---------- Operator select population ----------
  async function loadOperators(): Promise<void> {
    if (betreiberAbort) betreiberAbort.abort();
    betreiberAbort = new AbortController();
    let names: string[];
    try {
      names = await getJSON<string[]>(
        "/api/betreiber?limit=500",
        betreiberAbort.signal,
      );
    } catch (err) {
      if ((err as DOMException | Error).name === "AbortError") return;
      console.error("betreiber fetch failed", err);
      return;
    }
    const opts = [
      '<option value="all">Alle Betreiber</option>',
      ...names.map(
        (n) => `<option value="${escapeHtml(n)}">${escapeHtml(n)}</option>`,
      ),
    ];
    operatorSelect.innerHTML = opts.join("");
  }

  // ---------- Event wiring ----------
  const debouncedApplyFilters = debounce(() => void applyFilters(), 250);
  const debouncedSuggest = debounce(() => void fetchSuggestions(), 250);
  const debouncedApplyFiltersMap = debounce(() => void applyFilters(), 300);

  searchInput.addEventListener("input", () => {
    state.query = searchInput.value;
    state.acFocused = -1;
    searchClear.style.display = state.query ? "grid" : "none";
    debouncedSuggest();
    debouncedApplyFilters();
  });
  searchInput.addEventListener("focus", () => {
    void fetchSuggestions();
  });
  searchInput.addEventListener("keydown", (e) => {
    const items = acItems;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      state.acFocused = Math.min(items.length - 1, state.acFocused + 1);
      renderAutocomplete(items);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      state.acFocused = Math.max(-1, state.acFocused - 1);
      renderAutocomplete(items);
    } else if (e.key === "Enter" && state.acFocused >= 0) {
      e.preventDefault();
      void applySuggestion(items[state.acFocused]);
    } else if (e.key === "Escape") {
      autocomplete.style.display = "none";
    }
  });
  document.addEventListener("click", (e) => {
    const target = e.target as HTMLElement | null;
    if (!target?.closest(".search-wrap")) autocomplete.style.display = "none";
  });
  searchClear.addEventListener("click", () => {
    searchInput.value = "";
    state.query = "";
    searchClear.style.display = "none";
    autocomplete.style.display = "none";
    void applyFilters();
  });

  operatorSelect.addEventListener("change", () => {
    state.operator = operatorSelect.value;
    void applyFilters();
  });

  tierChips.forEach((c) => {
    c.classList.add("active"); // prototype: start all active
    c.addEventListener("click", () => {
      const t = c.dataset.tier as string;
      if (state.tiers.has(t)) state.tiers.delete(t);
      else state.tiers.add(t);
      c.classList.toggle("active", state.tiers.has(t));
      void applyFilters();
    });
  });

  plugChips.forEach((c) => {
    c.addEventListener("click", () => {
      const p = c.dataset.plug as string;
      if (state.plugs.has(p)) state.plugs.delete(p);
      else state.plugs.add(p);
      c.classList.toggle("active", state.plugs.has(p));
      void applyFilters();
    });
  });

  availChips.forEach((c) => {
    c.addEventListener("click", () => {
      const a = c.dataset.avail as string;
      if (state.availability.has(a)) state.availability.delete(a);
      else state.availability.add(a);
      c.classList.toggle("active", state.availability.has(a));
      void applyFilters();
    });
  });

  tabs.forEach((t) => {
    t.addEventListener("click", () => {
      const name = (t.dataset.tab || "search") as "search" | "route";
      switchTab(name);
    });
  });

  rangeSlider.addEventListener("input", () => {
    rangeValue.textContent = rangeSlider.value + " km";
  });
  socSlider.addEventListener("input", () => {
    socValue.textContent = socSlider.value + "%";
  });
  planBtn.addEventListener("click", () => void planRoute());

  map.on("moveend", () => {
    if (suppressNextMoveend) {
      suppressNextMoveend = false;
      return;
    }
    // Only refetch on viewport change when no filter is active; filtered
    // searches are global and don't depend on the bbox.
    if (!isFilterActive()) debouncedApplyFiltersMap();
  });

  // ---------- Init ----------
  switchTab("search");
  void loadOperators();
  void applyFilters();
});

// Make this file a module so `declare global` works under strict tsconfig.
export {};
