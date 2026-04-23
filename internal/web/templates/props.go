package templates

// IndexProps carries the data required to render the index page.
type IndexProps struct {
	TotalStations int
}

// AppCSS is the application stylesheet. It is served verbatim by the Go
// server at /assets/app.css (templ cannot interpolate inside <style> tags,
// so we ship it as a separate asset).
const AppCSS = `/* ====== Tokens ====== */
:root {
  /* Dark surfaces */
  --bg-0: #0b0f14;
  --bg-1: #11161d;
  --bg-2: #171d26;
  --bg-3: #1f2631;
  --bg-4: #2a3240;
  --bg-hover: #232b37;

  /* Text */
  --fg-1: #e7ecf3;
  --fg-2: #a8b2c1;
  --fg-3: #6b7689;
  --fg-4: #49515e;

  /* Borders */
  --border-1: #222a36;
  --border-2: #2e3744;
  --border-strong: #3a4556;

  /* Power tiers — brand accent = cyan */
  --ac: #53e08a;         /* AC — green (up to 22 kW) */
  --ac-dim: #53e08a33;
  --dc: #4ad1ff;         /* DC fast (50–149 kW) — cyan */
  --dc-dim: #4ad1ff33;
  --hpc: #ffc947;        /* HPC (≥150 kW) — amber/yellow */
  --hpc-dim: #ffc94733;
  --ultra: #ff7a59;      /* Ultra (≥300 kW) — coral */
  --ultra-dim: #ff7a5933;

  /* Semantic (availability) */
  --avail-free: #53e08a;
  --avail-busy: #ff7a59;
  --avail-off: #6b7689;

  /* Brand */
  --brand: #4ad1ff;
  --brand-2: #7aa8ff;
  --brand-glow: #4ad1ff55;

  --radius-sm: 8px;
  --radius-md: 12px;
  --radius-lg: 16px;
  --radius-xl: 20px;

  --shadow-sm: 0 1px 2px rgba(0,0,0,.4);
  --shadow-md: 0 8px 24px rgba(0,0,0,.35);
  --shadow-lg: 0 20px 60px rgba(0,0,0,.55);

  --ease: cubic-bezier(.4,0,.2,1);
}

* { box-sizing: border-box; }
html, body { margin: 0; padding: 0; height: 100%; overflow: hidden; }
body {
  font-family: 'Inter', system-ui, -apple-system, sans-serif;
  font-feature-settings: 'cv11', 'ss01';
  background: var(--bg-0);
  color: var(--fg-1);
  font-size: 14px;
  line-height: 1.45;
  -webkit-font-smoothing: antialiased;
}

button { font-family: inherit; cursor: pointer; border: none; background: none; color: inherit; }
input { font-family: inherit; }

/* ====== App layout ====== */
.app {
  display: grid;
  grid-template-columns: 420px 1fr;
  height: 100vh;
  width: 100vw;
}

/* ====== Sidebar ====== */
.sidebar {
  background: var(--bg-1);
  border-right: 1px solid var(--border-1);
  display: flex;
  flex-direction: column;
  min-height: 0;
  position: relative;
  z-index: 10;
}

/* Header */
.header {
  padding: 18px 20px 14px;
  border-bottom: 1px solid var(--border-1);
  display: flex;
  align-items: center;
  gap: 12px;
}
.logo {
  width: 36px; height: 36px;
  border-radius: 10px;
  background: linear-gradient(135deg, #4ad1ff 0%, #7aa8ff 100%);
  display: grid; place-items: center;
  box-shadow: 0 0 0 1px rgba(255,255,255,.08) inset, 0 8px 24px -10px var(--brand-glow);
  flex-shrink: 0;
}
.logo svg { width: 20px; height: 20px; color: #0b0f14; }
.brand-text { display: flex; flex-direction: column; line-height: 1.1; min-width: 0; }
.brand-title { font-size: 15px; font-weight: 700; letter-spacing: -0.01em; }
.brand-sub { font-size: 11px; color: var(--fg-3); letter-spacing: .04em; text-transform: uppercase; }
.station-count {
  margin-left: auto;
  font-variant-numeric: tabular-nums;
  font-size: 11px;
  color: var(--fg-2);
  background: var(--bg-2);
  padding: 5px 9px;
  border-radius: 999px;
  border: 1px solid var(--border-1);
}

/* Tabs */
.tabs {
  display: flex;
  padding: 10px 12px 0;
  gap: 4px;
  border-bottom: 1px solid var(--border-1);
}
.tab {
  flex: 1;
  padding: 10px 12px;
  font-size: 13px;
  font-weight: 500;
  color: var(--fg-3);
  border-radius: 8px 8px 0 0;
  display: flex; align-items: center; justify-content: center; gap: 8px;
  position: relative;
  transition: color .15s var(--ease), background .15s var(--ease);
}
.tab svg { width: 15px; height: 15px; }
.tab:hover { color: var(--fg-1); background: var(--bg-2); }
.tab.active { color: var(--fg-1); }
.tab.active::after {
  content: '';
  position: absolute;
  left: 12px; right: 12px; bottom: -1px;
  height: 2px;
  background: var(--brand);
  border-radius: 2px 2px 0 0;
  box-shadow: 0 0 12px var(--brand-glow);
}

/* Tab panels */
.panel {
  padding: 16px 20px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* Search input */
.search-wrap { position: relative; }
.search-input {
  width: 100%;
  padding: 12px 14px 12px 40px;
  background: var(--bg-2);
  border: 1px solid var(--border-2);
  border-radius: var(--radius-md);
  color: var(--fg-1);
  font-size: 14px;
  outline: none;
  transition: border-color .15s var(--ease), box-shadow .15s var(--ease);
}
.search-input::placeholder { color: var(--fg-3); }
.search-input:focus {
  border-color: var(--brand);
  box-shadow: 0 0 0 3px var(--brand-glow);
}
.search-icon {
  position: absolute;
  left: 13px; top: 50%;
  transform: translateY(-50%);
  width: 16px; height: 16px;
  color: var(--fg-3);
  pointer-events: none;
}
.search-clear {
  position: absolute;
  right: 8px; top: 50%;
  transform: translateY(-50%);
  width: 26px; height: 26px;
  display: grid; place-items: center;
  color: var(--fg-3);
  border-radius: 6px;
  transition: background .15s var(--ease), color .15s var(--ease);
}
.search-clear:hover { background: var(--bg-3); color: var(--fg-1); }
.search-clear svg { width: 14px; height: 14px; }

/* Autocomplete */
.autocomplete {
  position: absolute;
  top: calc(100% + 6px);
  left: 0; right: 0;
  background: var(--bg-2);
  border: 1px solid var(--border-2);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-md);
  max-height: 280px;
  overflow-y: auto;
  z-index: 30;
  padding: 4px;
}
.ac-item {
  padding: 9px 10px;
  border-radius: 8px;
  display: flex; align-items: center; gap: 10px;
  cursor: pointer;
  font-size: 13px;
}
.ac-item:hover, .ac-item.focused { background: var(--bg-3); }
.ac-icon { width: 28px; height: 28px; border-radius: 8px; background: var(--bg-3); display: grid; place-items: center; color: var(--fg-2); flex-shrink: 0; }
.ac-icon svg { width: 14px; height: 14px; }
.ac-text { display: flex; flex-direction: column; line-height: 1.2; min-width: 0; }
.ac-primary { color: var(--fg-1); font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.ac-secondary { color: var(--fg-3); font-size: 11px; }
.ac-kind { margin-left: auto; font-size: 10px; color: var(--fg-3); text-transform: uppercase; letter-spacing: .05em; }

/* Filter chips */
.filter-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.filter-label {
  font-size: 11px;
  color: var(--fg-3);
  text-transform: uppercase;
  letter-spacing: .06em;
  font-weight: 500;
}
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chip {
  padding: 6px 11px;
  background: var(--bg-2);
  border: 1px solid var(--border-2);
  border-radius: 999px;
  font-size: 12px;
  font-weight: 500;
  color: var(--fg-2);
  display: inline-flex;
  align-items: center;
  gap: 6px;
  transition: all .15s var(--ease);
}
.chip:hover { background: var(--bg-3); color: var(--fg-1); border-color: var(--border-strong); }
.chip .dot { width: 8px; height: 8px; border-radius: 50%; }
.chip[data-tier="ac"] .dot { background: var(--ac); }
.chip[data-tier="dc"] .dot { background: var(--dc); }
.chip[data-tier="hpc"] .dot { background: var(--hpc); }
.chip[data-tier="ultra"] .dot { background: var(--ultra); }
.chip.active {
  background: var(--bg-3);
  color: var(--fg-1);
  border-color: var(--fg-3);
}
.chip[data-tier="ac"].active { border-color: var(--ac); box-shadow: 0 0 0 1px var(--ac-dim); }
.chip[data-tier="dc"].active { border-color: var(--dc); box-shadow: 0 0 0 1px var(--dc-dim); }
.chip[data-tier="hpc"].active { border-color: var(--hpc); box-shadow: 0 0 0 1px var(--hpc-dim); }
.chip[data-tier="ultra"].active { border-color: var(--ultra); box-shadow: 0 0 0 1px var(--ultra-dim); }
.chip[data-avail="available"] .dot { background: var(--avail-free); }
.chip[data-avail="occupied"] .dot { background: var(--avail-busy); }
.chip[data-avail="offline"] .dot { background: var(--avail-off); }
.chip[data-plug] { font-variant-numeric: tabular-nums; }

/* Operator select */
.select-wrap { position: relative; }
.select {
  width: 100%;
  padding: 10px 36px 10px 12px;
  background: var(--bg-2);
  border: 1px solid var(--border-2);
  border-radius: var(--radius-md);
  color: var(--fg-1);
  font-size: 13px;
  appearance: none;
  cursor: pointer;
  outline: none;
}
.select:focus { border-color: var(--brand); box-shadow: 0 0 0 3px var(--brand-glow); }
.select-wrap::after {
  content: '';
  position: absolute;
  right: 14px; top: 50%;
  width: 8px; height: 8px;
  border-right: 1.5px solid var(--fg-3);
  border-bottom: 1.5px solid var(--fg-3);
  transform: translateY(-70%) rotate(45deg);
  pointer-events: none;
}

/* Results list */
.results-header {
  padding: 10px 20px 6px;
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  border-top: 1px solid var(--border-1);
}
.results-title { font-size: 11px; letter-spacing: .06em; text-transform: uppercase; color: var(--fg-3); font-weight: 500; }
.results-meta { font-size: 11px; color: var(--fg-3); font-variant-numeric: tabular-nums; }
.results-meta b { color: var(--fg-1); font-weight: 600; }

.results {
  overflow-y: auto;
  flex: 1;
  padding: 4px 12px 16px;
  min-height: 0;
}
.results::-webkit-scrollbar { width: 8px; }
.results::-webkit-scrollbar-track { background: transparent; }
.results::-webkit-scrollbar-thumb { background: var(--bg-3); border-radius: 4px; border: 2px solid var(--bg-1); }
.results::-webkit-scrollbar-thumb:hover { background: var(--bg-4); }

.result {
  padding: 12px;
  border-radius: var(--radius-md);
  cursor: pointer;
  display: flex;
  gap: 12px;
  transition: background .12s var(--ease);
  border: 1px solid transparent;
}
.result:hover { background: var(--bg-2); }
.result.selected { background: var(--bg-2); border-color: var(--border-2); }
.result-icon {
  width: 40px; height: 40px;
  border-radius: 10px;
  display: grid; place-items: center;
  flex-shrink: 0;
  position: relative;
  background: var(--bg-3);
}
.result-icon svg { width: 18px; height: 18px; color: var(--fg-1); }
.result-icon[data-tier="ac"] { background: color-mix(in srgb, var(--ac) 14%, var(--bg-3)); color: var(--ac); }
.result-icon[data-tier="ac"] svg { color: var(--ac); }
.result-icon[data-tier="dc"] { background: color-mix(in srgb, var(--dc) 14%, var(--bg-3)); }
.result-icon[data-tier="dc"] svg { color: var(--dc); }
.result-icon[data-tier="hpc"] { background: color-mix(in srgb, var(--hpc) 14%, var(--bg-3)); }
.result-icon[data-tier="hpc"] svg { color: var(--hpc); }
.result-icon[data-tier="ultra"] { background: color-mix(in srgb, var(--ultra) 14%, var(--bg-3)); }
.result-icon[data-tier="ultra"] svg { color: var(--ultra); }

.result-body { flex: 1; min-width: 0; }
.result-top {
  display: flex; align-items: baseline; justify-content: space-between; gap: 8px;
}
.result-operator { font-size: 13px; font-weight: 600; color: var(--fg-1); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.result-kw {
  font-size: 11px;
  font-weight: 700;
  color: var(--fg-2);
  background: var(--bg-3);
  padding: 2px 7px;
  border-radius: 999px;
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}
.result-kw[data-tier="ac"] { color: var(--ac); background: color-mix(in srgb, var(--ac) 10%, transparent); }
.result-kw[data-tier="dc"] { color: var(--dc); background: color-mix(in srgb, var(--dc) 10%, transparent); }
.result-kw[data-tier="hpc"] { color: var(--hpc); background: color-mix(in srgb, var(--hpc) 10%, transparent); }
.result-kw[data-tier="ultra"] { color: var(--ultra); background: color-mix(in srgb, var(--ultra) 10%, transparent); }
.result-addr { font-size: 12px; color: var(--fg-3); margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.result-meta {
  margin-top: 6px;
  display: flex;
  gap: 10px;
  font-size: 11px;
  color: var(--fg-3);
  align-items: center;
}
.result-meta .avail {
  display: inline-flex; align-items: center; gap: 5px;
  font-weight: 500;
}
.result-meta .avail::before {
  content: ''; width: 6px; height: 6px; border-radius: 50%; display: inline-block;
}
.result-meta .avail.available { color: var(--avail-free); }
.result-meta .avail.available::before { background: var(--avail-free); box-shadow: 0 0 6px var(--avail-free); }
.result-meta .avail.occupied { color: var(--avail-busy); }
.result-meta .avail.occupied::before { background: var(--avail-busy); }
.result-meta .avail.offline { color: var(--avail-off); }
.result-meta .avail.offline::before { background: var(--avail-off); }
.result-meta .plugs { color: var(--fg-3); }

.results-empty {
  padding: 40px 20px;
  text-align: center;
  color: var(--fg-3);
  font-size: 13px;
}
.results-empty svg { width: 40px; height: 40px; color: var(--fg-4); margin-bottom: 12px; }

/* Route planner */
.route-stops {
  display: flex;
  flex-direction: column;
  gap: 8px;
  position: relative;
}
.route-stop {
  display: flex; gap: 10px; align-items: stretch;
  position: relative;
}
.route-stop-marker {
  width: 22px;
  display: flex; flex-direction: column; align-items: center; padding-top: 14px;
  flex-shrink: 0;
  position: relative;
}
.route-stop-dot {
  width: 12px; height: 12px;
  border-radius: 50%;
  background: var(--bg-3);
  border: 2px solid var(--fg-3);
  flex-shrink: 0;
}
.route-stop-marker[data-type="start"] .route-stop-dot { background: var(--ac); border-color: var(--ac); box-shadow: 0 0 8px var(--ac-dim); }
.route-stop-marker[data-type="end"] .route-stop-dot {
  border-radius: 3px; background: transparent; border-color: var(--brand);
  width: 12px; height: 12px; position: relative;
}
.route-stop-marker[data-type="end"] .route-stop-dot::after {
  content: ''; position: absolute; inset: 2px; background: var(--brand); border-radius: 1px;
}
.route-stop-line {
  width: 2px; flex: 1;
  background: repeating-linear-gradient(to bottom, var(--border-strong) 0, var(--border-strong) 3px, transparent 3px, transparent 6px);
  margin-top: 2px;
}
.route-stop:last-child .route-stop-line { display: none; }
.route-input {
  flex: 1;
  padding: 10px 12px;
  background: var(--bg-2);
  border: 1px solid var(--border-2);
  border-radius: var(--radius-md);
  color: var(--fg-1);
  font-size: 13px;
  outline: none;
}
.route-input:focus { border-color: var(--brand); box-shadow: 0 0 0 3px var(--brand-glow); }

.route-options {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}
.route-field {
  background: var(--bg-2);
  border: 1px solid var(--border-2);
  border-radius: var(--radius-md);
  padding: 10px 12px;
}
.route-field-label { font-size: 10px; color: var(--fg-3); text-transform: uppercase; letter-spacing: .06em; margin-bottom: 4px; }
.route-field-value { font-size: 14px; font-weight: 600; color: var(--fg-1); font-variant-numeric: tabular-nums; }
.route-slider { width: 100%; margin-top: 4px; accent-color: var(--brand); }

.btn-primary {
  padding: 12px 16px;
  background: var(--brand);
  color: #0b0f14;
  border-radius: var(--radius-md);
  font-size: 14px;
  font-weight: 600;
  display: flex; align-items: center; justify-content: center; gap: 8px;
  transition: all .15s var(--ease);
  box-shadow: 0 0 0 1px rgba(255,255,255,.08) inset, 0 0 20px -4px var(--brand-glow);
}
.btn-primary:hover { filter: brightness(1.08); box-shadow: 0 0 0 1px rgba(255,255,255,.08) inset, 0 0 24px -2px var(--brand-glow); }
.btn-primary svg { width: 16px; height: 16px; }

.btn-secondary {
  padding: 10px 14px;
  background: var(--bg-2);
  border: 1px solid var(--border-2);
  color: var(--fg-1);
  border-radius: var(--radius-md);
  font-size: 13px;
  font-weight: 500;
  display: flex; align-items: center; justify-content: center; gap: 6px;
  transition: all .15s var(--ease);
}
.btn-secondary:hover { background: var(--bg-3); border-color: var(--border-strong); }
.btn-secondary svg { width: 15px; height: 15px; }

/* Route summary */
.route-summary {
  background: var(--bg-2);
  border: 1px solid var(--border-2);
  border-radius: var(--radius-md);
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.route-summary-top {
  display: flex; gap: 14px;
}
.route-summary-stat { flex: 1; min-width: 0; }
.route-summary-stat .v { font-size: 18px; font-weight: 700; color: var(--fg-1); font-variant-numeric: tabular-nums; }
.route-summary-stat .l { font-size: 11px; color: var(--fg-3); text-transform: uppercase; letter-spacing: .05em; margin-top: 2px; }
.route-stops-list { display: flex; flex-direction: column; gap: 6px; margin-top: 2px; border-top: 1px solid var(--border-1); padding-top: 10px; }
.route-stop-row { display: flex; align-items: center; gap: 10px; font-size: 12px; padding: 4px 0; }
.route-stop-row .num { width: 20px; height: 20px; border-radius: 999px; background: var(--hpc-dim); color: var(--hpc); font-weight: 700; font-size: 10px; display: grid; place-items: center; flex-shrink: 0; }
.route-stop-row .name { flex: 1; color: var(--fg-1); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-weight: 500; }
.route-stop-row .mini { color: var(--fg-3); font-variant-numeric: tabular-nums; }

/* ====== Map ====== */
.map-wrap {
  position: relative;
  background: #0a0e13;
}
#map { width: 100%; height: 100%; background: #0a0e13; }

/* Leaflet dark tweaks */
.leaflet-container { background: #0a0e13; font-family: inherit; }
.leaflet-control-attribution { background: rgba(17, 22, 29, 0.85) !important; color: var(--fg-3) !important; backdrop-filter: blur(8px); padding: 2px 8px !important; border-radius: 6px 0 0 0 !important; font-size: 10px !important; }
.leaflet-control-attribution a { color: var(--fg-2) !important; }
.leaflet-control-zoom { border: none !important; margin: 16px !important; box-shadow: var(--shadow-md) !important; border-radius: 10px !important; overflow: hidden; }
.leaflet-control-zoom a {
  background: var(--bg-2) !important;
  color: var(--fg-1) !important;
  border: none !important;
  border-bottom: 1px solid var(--border-1) !important;
  width: 36px !important; height: 36px !important; line-height: 36px !important;
  font-size: 18px !important;
  transition: background .15s var(--ease);
}
.leaflet-control-zoom a:last-child { border-bottom: none !important; }
.leaflet-control-zoom a:hover { background: var(--bg-3) !important; color: var(--fg-1) !important; }

/* Markers */
.station-marker {
  width: 30px; height: 30px;
  border-radius: 50% 50% 50% 0;
  transform: rotate(-45deg);
  background: var(--bg-2);
  display: grid; place-items: center;
  box-shadow: 0 4px 14px rgba(0,0,0,.5), 0 0 0 2px var(--bg-0);
  border: 2px solid currentColor;
  transition: transform .18s var(--ease), box-shadow .18s var(--ease);
}
.station-marker svg { transform: rotate(45deg); width: 14px; height: 14px; color: currentColor; }
.station-marker[data-tier="ac"] { color: var(--ac); }
.station-marker[data-tier="dc"] { color: var(--dc); }
.station-marker[data-tier="hpc"] { color: var(--hpc); }
.station-marker[data-tier="ultra"] { color: var(--ultra); }
.station-marker.selected {
  transform: rotate(-45deg) scale(1.25);
  box-shadow: 0 6px 20px rgba(0,0,0,.6), 0 0 0 2px var(--bg-0), 0 0 24px currentColor;
  z-index: 1000 !important;
}
.leaflet-marker-icon.station-marker-wrap { background: transparent !important; border: none !important; }

/* Cluster */
.cluster-marker {
  width: 40px; height: 40px;
  border-radius: 999px;
  background: var(--bg-2);
  color: var(--fg-1);
  display: grid; place-items: center;
  font-weight: 700;
  font-size: 13px;
  font-variant-numeric: tabular-nums;
  box-shadow: 0 4px 14px rgba(0,0,0,.5);
  border: 2px solid var(--border-strong);
  position: relative;
}
.cluster-marker.small { width: 36px; height: 36px; font-size: 12px; }
.cluster-marker.large { width: 48px; height: 48px; font-size: 14px; }
.cluster-marker.huge { width: 54px; height: 54px; font-size: 15px; }
.cluster-marker::before {
  content: '';
  position: absolute; inset: -4px;
  border-radius: 999px;
  border: 2px solid var(--brand);
  opacity: .3;
}

/* Popup */
.leaflet-popup-content-wrapper {
  background: var(--bg-2) !important;
  color: var(--fg-1) !important;
  border-radius: var(--radius-md) !important;
  border: 1px solid var(--border-2);
  box-shadow: var(--shadow-md) !important;
  padding: 0 !important;
}
.leaflet-popup-content { margin: 0 !important; padding: 14px 16px !important; font-family: inherit; min-width: 220px; }
.leaflet-popup-tip { background: var(--bg-2) !important; border: 1px solid var(--border-2); }
.leaflet-popup-close-button {
  color: var(--fg-3) !important;
  padding: 8px !important;
  font-size: 18px !important;
}

.popup-operator { font-weight: 600; font-size: 14px; }
.popup-addr { font-size: 12px; color: var(--fg-3); margin-top: 2px; }
.popup-row { display: flex; gap: 8px; margin-top: 10px; align-items: center; }
.popup-kw {
  font-size: 11px;
  font-weight: 700;
  padding: 3px 8px;
  border-radius: 999px;
  font-variant-numeric: tabular-nums;
}
.popup-kw[data-tier="ac"] { color: var(--ac); background: color-mix(in srgb, var(--ac) 12%, transparent); }
.popup-kw[data-tier="dc"] { color: var(--dc); background: color-mix(in srgb, var(--dc) 12%, transparent); }
.popup-kw[data-tier="hpc"] { color: var(--hpc); background: color-mix(in srgb, var(--hpc) 12%, transparent); }
.popup-kw[data-tier="ultra"] { color: var(--ultra); background: color-mix(in srgb, var(--ultra) 12%, transparent); }
.popup-details-btn {
  margin-top: 12px;
  width: 100%;
  padding: 8px;
  background: var(--bg-3);
  border: 1px solid var(--border-2);
  border-radius: 8px;
  color: var(--fg-1);
  font-size: 12px;
  font-weight: 500;
  display: flex; align-items: center; justify-content: center; gap: 6px;
}
.popup-details-btn:hover { background: var(--bg-4); }

/* ====== Detail panel (slide-in over map) ====== */
.detail-panel {
  position: absolute;
  top: 16px; bottom: 16px; right: 16px;
  width: 380px;
  background: var(--bg-1);
  border: 1px solid var(--border-2);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-lg);
  display: flex;
  flex-direction: column;
  transform: translateX(calc(100% + 32px));
  transition: transform .28s var(--ease);
  overflow: hidden;
  z-index: 500;
}
.detail-panel.open { transform: translateX(0); }
.detail-hero {
  height: 120px;
  position: relative;
  background: linear-gradient(135deg, var(--bg-3) 0%, var(--bg-2) 100%);
  border-bottom: 1px solid var(--border-1);
  display: flex; align-items: flex-end; padding: 14px 18px;
  overflow: hidden;
}
.detail-hero-bg {
  position: absolute; inset: 0;
  opacity: .7;
  background-image: radial-gradient(circle at 20% 30%, currentColor 0%, transparent 50%);
  filter: blur(30px);
}
.detail-hero[data-tier="ac"] { color: var(--ac); }
.detail-hero[data-tier="dc"] { color: var(--dc); }
.detail-hero[data-tier="hpc"] { color: var(--hpc); }
.detail-hero[data-tier="ultra"] { color: var(--ultra); }
.detail-hero-content { position: relative; z-index: 1; width: 100%; display: flex; justify-content: space-between; align-items: flex-end; }
.detail-tier {
  display: inline-flex; align-items: center; gap: 6px;
  font-size: 10px; text-transform: uppercase; letter-spacing: .08em;
  font-weight: 600;
  padding: 4px 9px;
  background: rgba(0,0,0,.4);
  backdrop-filter: blur(6px);
  color: currentColor;
  border-radius: 999px;
  border: 1px solid color-mix(in srgb, currentColor 30%, transparent);
}
.detail-kw-big {
  font-size: 32px; font-weight: 800; color: var(--fg-1);
  line-height: 1; font-variant-numeric: tabular-nums;
  letter-spacing: -0.02em;
}
.detail-kw-big .unit { font-size: 16px; font-weight: 600; color: var(--fg-3); margin-left: 2px; }
.detail-close {
  position: absolute; top: 12px; right: 12px; z-index: 2;
  width: 32px; height: 32px;
  border-radius: 8px;
  background: rgba(0,0,0,.5);
  backdrop-filter: blur(6px);
  color: var(--fg-1);
  display: grid; place-items: center;
  transition: background .15s var(--ease);
}
.detail-close:hover { background: rgba(0,0,0,.7); }
.detail-close svg { width: 16px; height: 16px; }

.detail-body {
  padding: 18px;
  display: flex; flex-direction: column; gap: 18px;
  overflow-y: auto;
  flex: 1;
  min-height: 0;
}
.detail-head h2 { margin: 0; font-size: 19px; font-weight: 700; letter-spacing: -0.01em; line-height: 1.2; }
.detail-head p { margin: 4px 0 0; font-size: 13px; color: var(--fg-3); }

.detail-avail {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  background: var(--bg-2);
  border: 1px solid var(--border-2);
  border-radius: var(--radius-md);
}
.detail-avail-icon { width: 32px; height: 32px; border-radius: 50%; display: grid; place-items: center; flex-shrink: 0; }
.detail-avail-icon.available { background: color-mix(in srgb, var(--avail-free) 18%, transparent); color: var(--avail-free); box-shadow: 0 0 16px -2px var(--avail-free); }
.detail-avail-icon.occupied { background: color-mix(in srgb, var(--avail-busy) 18%, transparent); color: var(--avail-busy); }
.detail-avail-icon.offline { background: var(--bg-3); color: var(--fg-3); }
.detail-avail-icon svg { width: 16px; height: 16px; }
.detail-avail-text .t { font-size: 13px; font-weight: 600; color: var(--fg-1); }
.detail-avail-text .s { font-size: 11px; color: var(--fg-3); }

.detail-section h3 {
  margin: 0 0 10px;
  font-size: 11px;
  color: var(--fg-3);
  text-transform: uppercase;
  letter-spacing: .06em;
  font-weight: 500;
}
.plug-list {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}
.plug-item {
  padding: 10px 12px;
  background: var(--bg-2);
  border: 1px solid var(--border-2);
  border-radius: var(--radius-md);
  display: flex; align-items: center; gap: 10px;
}
.plug-icon { width: 32px; height: 32px; border-radius: 8px; background: var(--bg-3); display: grid; place-items: center; color: var(--fg-2); flex-shrink: 0; }
.plug-icon svg { width: 16px; height: 16px; }
.plug-info { display: flex; flex-direction: column; line-height: 1.2; }
.plug-info .n { font-size: 13px; font-weight: 600; color: var(--fg-1); }
.plug-info .s { font-size: 11px; color: var(--fg-3); }

.meta-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1px;
  background: var(--border-1);
  border: 1px solid var(--border-1);
  border-radius: var(--radius-md);
  overflow: hidden;
}
.meta-cell {
  background: var(--bg-2);
  padding: 10px 12px;
}
.meta-cell .l { font-size: 10px; color: var(--fg-3); text-transform: uppercase; letter-spacing: .06em; }
.meta-cell .v { font-size: 13px; font-weight: 600; color: var(--fg-1); margin-top: 3px; }

.detail-actions { display: flex; gap: 8px; margin-top: auto; padding-top: 8px; }
.detail-actions .btn-secondary, .detail-actions .btn-primary { flex: 1; }

/* Legend floater */
.legend {
  position: absolute;
  bottom: 16px; left: 16px;
  background: rgba(17, 22, 29, 0.88);
  backdrop-filter: blur(12px);
  border: 1px solid var(--border-2);
  border-radius: var(--radius-md);
  padding: 10px 14px;
  display: flex;
  gap: 14px;
  font-size: 11px;
  z-index: 400;
  box-shadow: var(--shadow-md);
}
.legend-title { font-size: 10px; color: var(--fg-3); text-transform: uppercase; letter-spacing: .06em; font-weight: 500; border-right: 1px solid var(--border-1); padding-right: 14px; display: flex; align-items: center; }
.legend-item { display: inline-flex; align-items: center; gap: 6px; color: var(--fg-2); }
.legend-dot { width: 10px; height: 10px; border-radius: 50%; }
.legend-dot.ac { background: var(--ac); box-shadow: 0 0 6px var(--ac-dim); }
.legend-dot.dc { background: var(--dc); box-shadow: 0 0 6px var(--dc-dim); }
.legend-dot.hpc { background: var(--hpc); box-shadow: 0 0 6px var(--hpc-dim); }
.legend-dot.ultra { background: var(--ultra); box-shadow: 0 0 6px var(--ultra-dim); }

/* Scrollbar global */
::-webkit-scrollbar { width: 8px; height: 8px; }
::-webkit-scrollbar-track { background: transparent; }
::-webkit-scrollbar-thumb { background: var(--bg-3); border-radius: 4px; }
::-webkit-scrollbar-thumb:hover { background: var(--bg-4); }
`
