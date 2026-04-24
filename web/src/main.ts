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

// Rough 10→80 % charging time based on the station's rated power. Per-tier
// constants match typical real-world values; the car's acceptance curve
// dominates reality, but this is close enough for trip planning.
function chargeMinutes(kw: number): number {
  if (kw >= 300) return 15;
  if (kw >= 150) return 22;
  if (kw >= 50) return 35;
  return 90;
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
    // Route-tab-specific filters (independent of the search-tab chips).
    // Start with ALL tiers active so Typ-2 / Schuko plug filters still find
    // candidates (most Typ-2 chargers are AC, not HPC).
    routeTiers: new Set<string>(["ac", "dc", "hpc", "ultra"]),
    routePlugs: new Set<string>(),
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
  const routePlugChips = document.querySelectorAll<HTMLElement>(
    ".chip[data-route-plug]",
  );
  const routeTierChips = document.querySelectorAll<HTMLElement>(
    ".chip[data-route-tier]",
  );
  const resultsEl = $<HTMLDivElement>("#results");
  const resultsCount = $<HTMLElement>("#results-count");
  const stationCountEl = $<HTMLElement>("#station-count");
  const detailPanel = $<HTMLDivElement>("#detail-panel");
  const tabs = document.querySelectorAll<HTMLElement>(".tab");
  const panels = document.querySelectorAll<HTMLElement>(".panel");
  const routeStart = $<HTMLInputElement>("#route-start");
  const routeEnd = $<HTMLInputElement>("#route-end");
  const sidebarEl = document.querySelector<HTMLElement>(".sidebar");
  const sidebarToggle = document.getElementById("sidebar-toggle");
  const sidebarBackdrop = document.getElementById("sidebar-backdrop");
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

  // Stadia "alidade_smooth_dark" — dark theme with prominent motorways and
  // road labels. Free tier covers localhost + reasonable production traffic;
  // if you deploy to a non-local domain, register a free API key at
  // stadiamaps.com and append it as ?api_key=... below.
  L.tileLayer(
    "https://tiles.stadiamaps.com/tiles/alidade_smooth_dark/{z}/{x}/{y}{r}.png",
    {
      attribution:
        '© <a href="https://stadiamaps.com/" target="_blank">Stadia Maps</a> · © <a href="https://openmaptiles.org/" target="_blank">OpenMapTiles</a> · © <a href="https://www.openstreetmap.org/copyright" target="_blank">OpenStreetMap</a>',
      maxZoom: 20,
      detectRetina: true,
    },
  ).addTo(map);

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
      // On mobile the sidebar covers the map; slide it away so the user
      // actually sees where we just panned to.
      if (isMobile()) closeSidebar();
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
    const resultsList = document.getElementById("results");
    const routeSummaryWrap = document.getElementById("route-summary-wrap");
    if (resultsWrap) resultsWrap.style.display = t === "search" ? "" : "none";
    if (resultsList) resultsList.style.display = t === "search" ? "" : "none";
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


  let routeLayer: any = null;
  let routeStopMarkers: any[] = [];

  // The whole route — polyline, distance, duration and ladestopps — is
  // computed by the Go backend at GET /api/route. The frontend only renders.
  interface RoutePlan {
    polyline: [number, number][];
    distanceKm: number;
    durationMin: number;
    stops: Array<{
      progressKm: number;
      offrouteKm: number;
      station: Station;
    }>;
  }

  // ---------- Route-input autocomplete (Orte only) ----------
  // Wires an input + its dropdown to /api/suggest, filtered to kind="Ort".
  function wireRouteAutocomplete(
    input: HTMLInputElement,
    dropdown: HTMLElement,
  ): void {
    let abortCtl: AbortController | null = null;
    const hide = (): void => {
      dropdown.style.display = "none";
    };
    const fetchAndRender = async (): Promise<void> => {
      const q = input.value.trim();
      if (q.length < 2) {
        hide();
        return;
      }
      if (abortCtl) abortCtl.abort();
      abortCtl = new AbortController();
      let items: Suggestion[];
      try {
        items = await getJSON<Suggestion[]>(
          `/api/suggest?q=${encodeURIComponent(q)}&limit=12`,
          abortCtl.signal,
        );
      } catch (err) {
        if ((err as DOMException | Error).name === "AbortError") return;
        return;
      }
      const orte = items.filter((x) => x.kind === "Ort");
      if (orte.length === 0) {
        hide();
        return;
      }
      const cityIcon =
        '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="16" height="20" x="4" y="2" rx="2"/><path d="M9 22v-4h6v4"/><path d="M8 6h.01M16 6h.01M12 6h.01M12 10h.01M12 14h.01M16 10h.01M16 14h.01M8 10h.01M8 14h.01"/></svg>';
      dropdown.innerHTML = orte
        .map(
          (it) =>
            `<div class="ac-item" data-value="${escapeHtml(it.value)}">` +
            `<div class="ac-icon">${cityIcon}</div>` +
            `<div class="ac-text"><div class="ac-primary">${escapeHtml(it.label)}</div></div>` +
            `<div class="ac-kind">Ort</div>` +
            `</div>`,
        )
        .join("");
      dropdown.style.display = "block";
      dropdown.querySelectorAll<HTMLElement>(".ac-item").forEach((el) => {
        el.addEventListener("mousedown", (ev) => {
          // mousedown (not click) so it fires before input's blur hides us.
          ev.preventDefault();
          const v = el.dataset.value || "";
          input.value = v;
          hide();
          input.dispatchEvent(new Event("change", { bubbles: true }));
        });
      });
    };
    const debounced = debounce(() => void fetchAndRender(), 200);
    input.addEventListener("input", debounced);
    input.addEventListener("focus", () => void fetchAndRender());
    input.addEventListener("blur", () => setTimeout(hide, 150));
  }
  wireRouteAutocomplete(routeStart, $<HTMLElement>("#route-start-suggest"));
  wireRouteAutocomplete(routeEnd, $<HTMLElement>("#route-end-suggest"));

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

    // Status while the backend is crunching OSRM + picking stops.
    routeSummary.innerHTML =
      '<div style="font-size:13px;color:var(--fg-3);text-align:center;padding:16px">Route wird berechnet…</div>';
    routeSummary.style.display = "";

    const params = new URLSearchParams({
      start: `${startG.coord[0]},${startG.coord[1]}`,
      end: `${endG.coord[0]},${endG.coord[1]}`,
      range: String(range),
      soc: String(soc),
    });
    // Route-tab chips drive the route plan (independent of the search tab).
    // Always send tiers when at least one is active — backend default is
    // HPC+Ultra only, which excludes Typ-2 AC chargers.
    if (state.routeTiers.size > 0) {
      params.set("tiers", Array.from(state.routeTiers).join(","));
    }
    if (state.routePlugs.size > 0) {
      params.set("plugs", Array.from(state.routePlugs).join(","));
    }

    let plan: RoutePlan;
    try {
      plan = await getJSON<RoutePlan>(`/api/route?${params.toString()}`);
    } catch (err) {
      console.error("route plan failed", err);
      alert("Route konnte nicht berechnet werden.");
      routeSummary.style.display = "none";
      return;
    }

    routeLayer = L.polyline(plan.polyline, {
      color: "#4ad1ff",
      weight: 4,
      opacity: 0.9,
      smoothFactor: 1.5,
    }).addTo(map);

    plan.stops.forEach((stop, i) => {
      const icon = L.divIcon({
        className: "station-marker-wrap",
        html: `<div class="cluster-marker" style="background:var(--hpc);color:#0b0f14;border-color:var(--hpc);width:34px;height:34px;font-size:13px">${i + 1}</div>`,
        iconSize: [34, 34],
      });
      const m = L.marker([stop.station.lat, stop.station.lng], { icon }).addTo(
        map,
      );
      m.on("click", () =>
        void selectStation(stop.station.id, true, { preserveZoom: true }),
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

    map.flyToBounds(L.latLngBounds(plan.polyline).pad(0.15), { duration: 0.8 });

    const drivingMin = Math.round(plan.durationMin);
    // Per-stop charging time is a per-tier estimate of a 10→80% DC charge
    // (plus AC is just what you can do in 90 min). Good enough for trip
    // planning; real duration depends on the car's acceptance curve.
    const perStopMin = plan.stops.map((s) => chargeMinutes(s.station.nennleistungKw));
    const chargingMin = perStopMin.reduce((a, b) => a + b, 0);
    const firstStopKm =
      plan.stops.length > 0 ? plan.stops[0].progressKm : 0;
    const plugsBadge =
      state.routePlugs.size > 0
        ? ` <span style="margin-left:8px;font-size:10px;color:var(--brand);text-transform:uppercase;letter-spacing:.06em">nur ${Array.from(state.routePlugs).join(" · ")}</span>`
        : "";
    routeSummary.innerHTML =
      '<div class="route-summary-top">' +
      `<div class="route-summary-stat"><div class="v">${Math.round(plan.distanceKm)} km</div><div class="l">Distanz</div></div>` +
      `<div class="route-summary-stat"><div class="v">${Math.floor(drivingMin / 60)} h ${drivingMin % 60} min</div><div class="l">Fahrzeit</div></div>` +
      `<div class="route-summary-stat"><div class="v">${plan.stops.length}</div><div class="l">Ladestopps${plugsBadge}</div></div>` +
      "</div>" +
      (plan.stops.length > 0
        ? `<div class="route-first-hint">Erster Stopp nach <b>${firstStopKm} km</b></div>` +
          '<div class="route-stops-list">' +
          plan.stops
            .map((stop, i) => {
              const mins = perStopMin[i];
              const distFromPrev =
                i === 0
                  ? stop.progressKm
                  : Math.round((stop.progressKm - plan.stops[i - 1].progressKm) * 10) / 10;
              return (
                `<div class="route-stop-row" data-id="${stop.station.id}" style="cursor:pointer">` +
                `<div class="num">${i + 1}</div>` +
                `<div class="stop-body">` +
                `<div class="name">${escapeHtml(operatorShort(stop.station.betreiber || ""))} · ${escapeHtml(stop.station.ort || "")}</div>` +
                `<div class="mini">nach ${stop.progressKm} km · ${mins} min laden · ${stop.station.nennleistungKw} kW` +
                (i === 0 ? "" : ` · +${distFromPrev} km zum Vorstopp`) +
                (stop.offrouteKm > 0.5
                  ? ` · <span style="color:var(--fg-4)">${stop.offrouteKm} km ab Route</span>`
                  : "") +
                `</div>` +
                `</div>` +
                "</div>"
              );
            })
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

  routePlugChips.forEach((c) => {
    c.addEventListener("click", () => {
      const p = c.dataset.routePlug as string;
      if (state.routePlugs.has(p)) state.routePlugs.delete(p);
      else state.routePlugs.add(p);
      c.classList.toggle("active", state.routePlugs.has(p));
    });
  });

  routeTierChips.forEach((c) => {
    c.addEventListener("click", () => {
      const t = c.dataset.routeTier as string;
      if (state.routeTiers.has(t)) state.routeTiers.delete(t);
      else state.routeTiers.add(t);
      c.classList.toggle("active", state.routeTiers.has(t));
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

  // ---------- Mobile sidebar drawer ----------
  const isMobile = (): boolean =>
    window.matchMedia("(max-width: 860px)").matches;
  function openSidebar(): void {
    sidebarEl?.classList.add("open");
    sidebarBackdrop?.classList.add("open");
  }
  function closeSidebar(): void {
    sidebarEl?.classList.remove("open");
    sidebarBackdrop?.classList.remove("open");
  }
  // Document-level delegation: survives even if specific nodes are rebuilt
  // or initially missing. pointerup is more reliable than click on iOS Safari.
  document.addEventListener("pointerup", (e) => {
    const t = e.target as HTMLElement;
    if (t.closest("#sidebar-toggle")) {
      if (sidebarEl?.classList.contains("open")) closeSidebar();
      else openSidebar();
    } else if (t.closest("#sidebar-backdrop")) {
      closeSidebar();
    } else if (t.closest(".result") && isMobile()) {
      closeSidebar();
    }
  });

  map.on("moveend", () => {
    if (suppressNextMoveend) {
      suppressNextMoveend = false;
      return;
    }
    // Only refetch on viewport change when no filter is active; filtered
    // searches are global and don't depend on the bbox.
    if (!isFilterActive()) debouncedApplyFiltersMap();
  });

  // ---------- URL state persistence ----------
  // The URL is the source of truth for what to restore on reload. We
  // update it (replaceState, so no history noise) whenever a user-facing
  // knob changes, and we read it once at startup.
  function syncURL(): void {
    const p = new URLSearchParams();
    if (state.tab === "route") p.set("tab", "route");
    if (routeStart.value) p.set("from", routeStart.value);
    if (routeEnd.value) p.set("to", routeEnd.value);
    if (rangeSlider.value && rangeSlider.value !== "120") {
      p.set("range", rangeSlider.value);
    }
    if (socSlider.value && socSlider.value !== "85") {
      p.set("soc", socSlider.value);
    }
    if (state.routeTiers.size > 0 && state.routeTiers.size < 4) {
      p.set("rt", Array.from(state.routeTiers).join(","));
    }
    if (state.routePlugs.size > 0) {
      p.set("rp", Array.from(state.routePlugs).join(","));
    }
    if (state.query) p.set("q", state.query);
    if (state.operator && state.operator !== "all") {
      p.set("op", state.operator);
    }
    if (state.tiers.size > 0 && state.tiers.size < 4) {
      p.set("t", Array.from(state.tiers).join(","));
    }
    if (state.plugs.size > 0) {
      p.set("pl", Array.from(state.plugs).join(","));
    }
    if (state.availability.size > 0) {
      p.set("av", Array.from(state.availability).join(","));
    }
    const qs = p.toString();
    const next = qs ? "?" + qs : location.pathname;
    if (location.search !== (qs ? "?" + qs : "")) {
      history.replaceState(null, "", next);
    }
  }
  const debouncedSyncURL = debounce(syncURL, 300);

  function restoreFromURL(): boolean {
    const p = new URLSearchParams(location.search);
    let hasAny = false;

    const from = p.get("from");
    const to = p.get("to");
    if (from) {
      routeStart.value = from;
      hasAny = true;
    }
    if (to) {
      routeEnd.value = to;
      hasAny = true;
    }
    const rg = p.get("range");
    if (rg) {
      rangeSlider.value = rg;
      rangeValue.textContent = rg + " km";
    }
    const sc = p.get("soc");
    if (sc) {
      socSlider.value = sc;
      socValue.textContent = sc + "%";
    }
    const rt = p.get("rt");
    if (rt !== null) {
      state.routeTiers = new Set(rt ? rt.split(",") : []);
      routeTierChips.forEach((c) => {
        c.classList.toggle(
          "active",
          state.routeTiers.has(c.dataset.routeTier as string),
        );
      });
    }
    const rp = p.get("rp");
    if (rp !== null) {
      state.routePlugs = new Set(rp ? rp.split(",") : []);
      routePlugChips.forEach((c) => {
        c.classList.toggle(
          "active",
          state.routePlugs.has(c.dataset.routePlug as string),
        );
      });
    }

    const q = p.get("q");
    if (q) {
      searchInput.value = q;
      state.query = q;
      searchClear.style.display = "grid";
    }
    // Operator is applied after loadOperators resolves.
    const t = p.get("t");
    if (t !== null) {
      state.tiers = new Set(t ? t.split(",") : []);
      tierChips.forEach((c) => {
        c.classList.toggle("active", state.tiers.has(c.dataset.tier as string));
      });
    }
    const pl = p.get("pl");
    if (pl !== null) {
      state.plugs = new Set(pl ? pl.split(",") : []);
      plugChips.forEach((c) => {
        c.classList.toggle("active", state.plugs.has(c.dataset.plug as string));
      });
    }
    const av = p.get("av");
    if (av !== null) {
      state.availability = new Set(av ? av.split(",") : []);
      availChips.forEach((c) => {
        c.classList.toggle(
          "active",
          state.availability.has(c.dataset.avail as string),
        );
      });
    }

    if (p.get("tab") === "route") switchTab("route");
    return hasAny;
  }

  // Hook URL sync to user-facing changes. All of these paths already mutate
  // state; we just piggy-back a replaceState.
  routeStart.addEventListener("input", debouncedSyncURL);
  routeEnd.addEventListener("input", debouncedSyncURL);
  rangeSlider.addEventListener("input", debouncedSyncURL);
  socSlider.addEventListener("input", debouncedSyncURL);
  routeTierChips.forEach((c) => c.addEventListener("click", debouncedSyncURL));
  routePlugChips.forEach((c) => c.addEventListener("click", debouncedSyncURL));
  tierChips.forEach((c) => c.addEventListener("click", debouncedSyncURL));
  plugChips.forEach((c) => c.addEventListener("click", debouncedSyncURL));
  availChips.forEach((c) => c.addEventListener("click", debouncedSyncURL));
  searchInput.addEventListener("input", debouncedSyncURL);
  operatorSelect.addEventListener("change", debouncedSyncURL);
  tabs.forEach((t) => t.addEventListener("click", debouncedSyncURL));

  // ---------- Init ----------
  const hasRouteFromURL = restoreFromURL();
  if (!new URLSearchParams(location.search).has("tab")) {
    switchTab("search");
  }
  void loadOperators().then(() => {
    const opFromURL = new URLSearchParams(location.search).get("op");
    if (opFromURL) {
      state.operator = opFromURL;
      operatorSelect.value = opFromURL;
    }
  });
  void applyFilters();
  if (hasRouteFromURL && state.tab === "route") {
    // Auto-plan the route the user had saved in the URL.
    void planRoute();
  }
});

// Make this file a module so `declare global` works under strict tsconfig.
export {};
