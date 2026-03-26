// API Profiler Dashboard — client-side logic

// ── Helpers ───────────────────────────────────────────────────────────────────
function el(id) { return document.getElementById(id); }

function fmt(n, decimals = 0) {
  return Number(n).toLocaleString('en-US', { maximumFractionDigits: decimals });
}

function escHtml(s) {
  return String(s)
    .replace(/&/g,'&amp;').replace(/</g,'&lt;')
    .replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// ── Time range state ──────────────────────────────────────────────────────────
const _range = { from: null, to: null, live: true };
let _pollTimer = null;
let _preset = null; // { label: '1h', ms: 3600000 } or null

const PRESET_MS = { '15m': 15*60e3, '1h': 3600e3, '6h': 6*3600e3, '24h': 24*3600e3, '7d': 7*24*3600e3 };

function rangeParams() {
  if (_preset) {
    const now  = new Date();
    const from = new Date(now - _preset.ms);
    return `?from=${encodeURIComponent(from.toISOString())}&to=${encodeURIComponent(now.toISOString())}`;
  }
  if (_range.live) return '';
  const from = _range.from ? new Date(_range.from).toISOString() : null;
  const to   = _range.to   ? new Date(_range.to).toISOString()   : new Date().toISOString();
  return from ? `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}` : '';
}

function setActivePreset(label) {
  document.querySelectorAll('.tr-preset').forEach(b => {
    b.classList.toggle('active', b.dataset.preset === label);
  });
}

function initTimeRange() {
  const btnApply  = el('tr-apply');
  const btnLive   = el('tr-live');
  const inputFrom = el('tr-from');
  const inputTo   = el('tr-to');
  const bar       = el('timerange-bar');

  // Preset buttons
  document.querySelectorAll('.tr-preset').forEach(btn => {
    btn.addEventListener('click', () => {
      const label = btn.dataset.preset;
      _preset     = { label, ms: PRESET_MS[label] };
      _range.live = false;
      inputFrom.value = '';
      inputTo.value   = '';
      btnLive.classList.remove('active');
      bar.classList.add('historical');
      setActivePreset(label);
      startPolling();
      refresh();
    });
  });

  btnLive.addEventListener('click', () => {
    _preset     = null;
    _range.live = true;
    _range.from = null;
    _range.to   = null;
    inputFrom.value = '';
    inputTo.value   = '';
    btnLive.classList.add('active');
    bar.classList.remove('historical');
    setActivePreset(null);
    startPolling();
    refresh();
  });

  btnApply.addEventListener('click', () => {
    const f = inputFrom.value;
    const t = inputTo.value;
    if (!f) { inputFrom.focus(); return; }
    _preset     = null;
    _range.live = false;
    _range.from = f;
    _range.to   = t || null;
    btnLive.classList.remove('active');
    bar.classList.add('historical');
    setActivePreset(null);
    stopPolling();
    refresh();
  });

  // Enter key on either input triggers apply.
  [inputFrom, inputTo].forEach(inp =>
    inp.addEventListener('keydown', e => { if (e.key === 'Enter') btnApply.click(); })
  );
}

function startPolling() {
  stopPolling();
  _pollTimer = setInterval(refresh, 5000);
}

function stopPolling() {
  if (_pollTimer) { clearInterval(_pollTimer); _pollTimer = null; }
}

// ── Heatmap (US-47) ───────────────────────────────────────────────────────────
let _heatmapMetric = 'rps';

function initHeatmapTabs() {
  const tabs = el('heatmap-tabs');
  if (!tabs) return;
  tabs.addEventListener('click', e => {
    const btn = e.target.closest('.panel-tab');
    if (!btn) return;
    tabs.querySelectorAll('.panel-tab').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
    _heatmapMetric = btn.dataset.metric;
    fetchHeatmap();
  });
}

async function fetchHeatmap() {
  try {
    const rp  = rangeParams();
    const res = await fetch(`/metrics/heatmap${rp}${rp ? '&' : '?'}metric=${_heatmapMetric}`);
    if (!res.ok) return;
    const data = await res.json();
    const body = el('heatmap')?.querySelector('.section-body');
    if (!body) return;

    const DAYS = ['Sun','Mon','Tue','Wed','Thu','Fri','Sat'];
    const unit = _heatmapMetric === 'error_rate' ? '%' : 'rps';
    const color = _heatmapMetric === 'error_rate' ? 'var(--danger)' : 'var(--accent)';
    const maxVal = data.max || 1;

    // Build cell lookup: weekday*24+hour → value
    const vals = new Array(168).fill(0);
    for (const c of data.cells) { vals[c.weekday * 24 + c.hour] = c.value; }

    // Header row: empty corner + hour labels
    const hourRow = '<div class="hm-row">' +
      '<div class="hm-day-label"></div>' +
      Array.from({length:24}, (_,h) =>
        `<div class="hm-hour-label">${String(h).padStart(2,'0')}</div>`
      ).join('') +
      '</div>';

    // Rows per day
    const rows = DAYS.map((day, wd) => {
      const cells = Array.from({length:24}, (_,h) => {
        const v = vals[wd*24+h];
        const intensity = maxVal > 0 ? v / maxVal : 0;
        const alpha = (0.08 + intensity * 0.92).toFixed(3);
        const tip = `${day} ${String(h).padStart(2,'0')}:00 — ${v.toFixed(2)} ${unit}`;
        return `<div class="hm-cell" style="background:${color};opacity:${alpha}" title="${tip}"></div>`;
      }).join('');
      return `<div class="hm-row"><div class="hm-day-label">${day}</div>${cells}</div>`;
    }).join('');

    body.innerHTML = `<div class="hm-wrap">
      <div class="hm-grid">${hourRow}${rows}</div>
      <div class="hm-legend">
        <span class="hm-legend-label">0 ${unit}</span>
        <div class="hm-legend-bar" style="background:linear-gradient(90deg,transparent,${color})"></div>
        <span class="hm-legend-label">${data.max.toFixed(2)} ${unit}</span>
      </div>
    </div>`;
  } catch (_) {}
}

// ── Anomaly scores state (US-48) ──────────────────────────────────────────────
let _anomalyMap = {}; // key: "METHOD|path" → z_score

async function fetchAnomalyScores() {
  try {
    const res = await fetch('/metrics/anomaly-scores');
    if (!res.ok) return;
    const scores = await res.json();
    _anomalyMap = {};
    for (const s of (scores || [])) {
      if (s.has_baseline) _anomalyMap[s.method + '|' + s.path] = s.z_score;
    }
    renderTable();
  } catch (_) {}
}

// ── Apdex state ───────────────────────────────────────────────────────────────
let _apdexMap    = {}; // key: "METHOD|path" → apdex score (0-1)
let _globalApdex = null; // last computed global apdex (0-1)

function fmtGlobalApdex() {
  if (_globalApdex === null) return '<span class="cell-dim">—</span>';
  const cls = _globalApdex >= 0.85 ? 'apdex-great' : _globalApdex >= 0.70 ? 'apdex-good' : 'apdex-poor';
  return `<span class="apdex-badge apdex-val-lg ${cls}">${_globalApdex.toFixed(2)}</span>`;
}

// ── Summary (US-19) ───────────────────────────────────────────────────────────
async function fetchSummary() {
  try {
    const res = await fetch('/metrics/summary' + rangeParams());
    if (!res.ok) return;
    const d = await res.json();

    const errorClass = d.global_error_rate >= 5 ? 'danger'
                     : d.global_error_rate >= 1 ? 'warning' : 'ok';
    const p99Class   = d.global_p99 >= 1000 ? 'danger' : 'ok';

    el('summary').querySelector('.section-body').innerHTML = `
      <div class="stat-grid">
        <div class="stat-card">
          <div class="stat-label">Total Requests</div>
          <div class="stat-value">${fmt(d.total_requests)}</div>
        </div>
        <div class="stat-card stat-${errorClass}">
          <div class="stat-label">Error Rate</div>
          <div class="stat-value">${fmt(d.global_error_rate,1)}<span class="stat-unit">%</span></div>
        </div>
        <div class="stat-card stat-${p99Class}">
          <div class="stat-label">Global P99</div>
          <div class="stat-value">${fmt(d.global_p99,1)}<span class="stat-unit">ms</span></div>
        </div>
        <div class="stat-card">
          <div class="stat-label">Active Endpoints</div>
          <div class="stat-value">${d.active_endpoints}</div>
        </div>
        <div class="stat-card apdex-stat-card">
          <div class="stat-label">Global Apdex</div>
          <div class="stat-value" id="global-apdex-value">${fmtGlobalApdex()}</div>
        </div>
      </div>`;
  } catch (_) { /* keep last known value */ }
}

// ── Endpoints table (US-20) ───────────────────────────────────────────────────
let _tableData  = [];
let _sortCol    = 'p99';
let _sortDir    = -1;     // -1 = desc, 1 = asc
let _filterText = '';

const COLS = [
  { key: 'method',      label: 'Method', fmt: v => escHtml(v) },
  { key: 'path',        label: 'Path',   fmt: v => `<span class="cell-path">${escHtml(v)}</span>` },
  { key: 'p50',         label: 'P50',    fmt: v => fmt(v,1) + '<span class="cell-unit">ms</span>' },
  { key: 'p95',         label: 'P95',    fmt: v => fmt(v,1) + '<span class="cell-unit">ms</span>' },
  { key: 'p99',         label: 'P99',    fmt: v => {
      const cls = v >= 1000 ? 'danger' : '';
      return `<span class="${cls}">${fmt(v,1)}<span class="cell-unit">ms</span></span>`;
    }
  },
  { key: 'rps_current', label: 'RPS',   fmt: v => fmt(v,2) },
  { key: 'error_rate',  label: 'Err%',  fmt: v => {
      const cls = v >= 5 ? 'danger' : v >= 1 ? 'warning' : '';
      return `<span class="${cls}">${fmt(v,1)}%</span>`;
    }
  },
  { key: 'apdex', label: 'Apdex', fmt: (_, r) => {
      const a = _apdexMap[r.method + '|' + r.path];
      if (a === undefined) return '<span class="cell-dim">—</span>';
      const cls = a >= 0.85 ? 'apdex-great' : a >= 0.70 ? 'apdex-good' : 'apdex-poor';
      return `<span class="apdex-badge ${cls}">${a.toFixed(2)}</span>`;
    }
  },
  { key: 'z_score', label: 'Z', fmt: (_, r) => {
      const z = _anomalyMap[r.method + '|' + r.path];
      if (z === undefined) return '<span class="cell-dim">—</span>';
      const cls = z >= 2.0 ? 'danger' : z >= 1.5 ? 'warning' : '';
      return `<span class="${cls}">${z.toFixed(1)}σ</span>`;
    }
  },
  { key: 'count',       label: 'Count', fmt: v => fmt(v) },
];

function renderTable() {
  const body = el('endpoints').querySelector('.section-body');
  if (!body) return;

  const filter = _filterText.toLowerCase();
  const rows = _tableData
    .filter(r => r.path.toLowerCase().includes(filter))
    .sort((a, b) => {
      const av = a[_sortCol], bv = b[_sortCol];
      return typeof av === 'string' ? av.localeCompare(bv) * _sortDir : (av - bv) * _sortDir;
    });

  const thCells = COLS.map(c => {
    const active = c.key === _sortCol;
    const arrow  = active ? (_sortDir === -1 ? ' ↓' : ' ↑') : '';
    return `<th class="sortable${active ? ' active' : ''}" data-col="${c.key}">${c.label}${arrow}</th>`;
  }).join('');

  const trRows = rows.length === 0
    ? `<tr><td colspan="${COLS.length}" class="no-data">No data</td></tr>`
    : rows.map(r =>
        `<tr class="row-clickable" data-method="${escHtml(r.method)}" data-path="${escHtml(r.path)}">` +
        COLS.map(c => `<td>${c.fmt(r[c.key], r)}</td>`).join('') + '</tr>'
      ).join('');

  // If the table already exists, only update thead/tbody to preserve input focus.
  const existingTable = body.querySelector('.data-table');
  if (existingTable) {
    existingTable.querySelector('thead tr').innerHTML = thCells;
    existingTable.querySelector('tbody').innerHTML = trRows;
    return;
  }

  // First render: build full structure and attach listeners via event delegation.
  body.innerHTML = `
    <div class="table-toolbar">
      <input id="ep-filter" class="filter-input" type="text"
             placeholder="Filter by path…" value="${escHtml(_filterText)}" autocomplete="off">
    </div>
    <div class="table-wrap">
      <table class="data-table">
        <thead><tr>${thCells}</tr></thead>
        <tbody>${trRows}</tbody>
      </table>
    </div>`;

  body.querySelector('#ep-filter').addEventListener('input', e => {
    _filterText = e.target.value;
    renderTable();
  });

  // Sort: single delegated listener on thead — survives innerHTML updates to tr.
  body.querySelector('thead').addEventListener('click', e => {
    const th = e.target.closest('th.sortable');
    if (!th) return;
    const col = th.dataset.col;
    if (_sortCol === col) { _sortDir *= -1; }
    else { _sortCol = col; _sortDir = -1; }
    renderTable();
  });

  // Row click: single delegated listener on tbody — survives tbody innerHTML updates.
  body.querySelector('tbody').addEventListener('click', e => {
    const tr = e.target.closest('tr.row-clickable');
    if (!tr) return;
    const method = tr.dataset.method;
    const path   = tr.dataset.path;
    if (_chartEndpoint && _chartEndpoint.method === method && _chartEndpoint.path === path) {
      const panel = el('chart-panel');
      if (panel) panel.style.display = 'none';
      _chartEndpoint = null;
    } else {
      _chartEndpoint = { method, path };
      fetchLatency(method, path);
    }
  });
}

async function fetchEndpoints() {
  try {
    const res = await fetch('/metrics/table' + rangeParams());
    if (!res.ok) return;
    _tableData = await res.json();
    renderTable();
  } catch (_) { /* keep last known table */ }
}

// ── Apdex (US-44) ────────────────────────────────────────────────────────────
async function fetchApdex() {
  try {
    const res = await fetch('/metrics/apdex' + rangeParams());
    if (!res.ok) return;
    const data = await res.json();
    const stats = data.endpoints || [];

    // Build per-endpoint lookup map and accumulate global totals.
    _apdexMap = {};
    let sumSat = 0, sumTol = 0, sumTotal = 0;
    for (const s of stats) {
      _apdexMap[s.method + '|' + s.path] = s.apdex;
      sumSat   += s.satisfied;
      sumTol   += s.tolerating;
      sumTotal += s.total;
    }

    // Re-render endpoints table so the Apdex column populates.
    renderTable();

    // Update cached global Apdex and refresh its element.
    if (sumTotal > 0) {
      _globalApdex = (sumSat + sumTol / 2) / sumTotal;
      const apdexEl = el('global-apdex-value');
      if (apdexEl) apdexEl.innerHTML = fmtGlobalApdex();
    }
  } catch (_) { /* keep last known value */ }
}

// ── Error Fingerprints (US-46) ────────────────────────────────────────────────
async function fetchErrorFingerprints() {
  try {
    const res = await fetch('/metrics/errors/fingerprints' + rangeParams());
    if (!res.ok) return;
    const data = await res.json();
    const fps = data.fingerprints || [];
    const body = el('error-fingerprints')?.querySelector('.section-body');
    if (!body) return;

    if (!fps || fps.length === 0) {
      body.innerHTML = '<div class="placeholder">No error fingerprints</div>';
      return;
    }

    const rows = fps.map(f => {
      const sc      = statusClass(f.status_code);
      const mcls    = METHOD_COLORS[f.method] || 'badge-other';
      const newBadge = f.is_new ? '<span class="fp-new-badge">new</span>' : '';
      const last    = new Date(f.last_seen).toLocaleTimeString();
      return `<tr>
        <td><span class="method-badge ${mcls}">${escHtml(f.method)}</span></td>
        <td><span class="cell-path">${escHtml(f.path)}</span></td>
        <td class="cell-status ${sc}">${f.status_code}</td>
        <td>${fmt(f.count)}</td>
        <td>${fmt(f.rate, 2)}<span class="cell-unit">%</span></td>
        <td>${fmt(f.p50_ms, 1)}<span class="cell-unit">ms</span></td>
        <td>${fmt(f.p95_ms, 1)}<span class="cell-unit">ms</span></td>
        <td class="cell-time">${last}</td>
        <td>${newBadge}</td>
      </tr>`;
    }).join('');

    body.innerHTML = `
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr>
            <th>Method</th><th>Path</th><th>Status</th>
            <th>Count</th><th>Rate</th><th>P50</th><th>P95</th>
            <th>Last Seen</th><th></th>
          </tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>`;
  } catch (_) { /* keep last known state */ }
}

// ── Latency chart (US-21) ─────────────────────────────────────────────────────
let _chartEndpoint = null;

// buildSVG returns an <svg> string for the P99 time-series.
function buildSVG(buckets) {
  const W = 800, H = 220;
  const pl = 58, pr = 16, pt = 16, pb = 32;
  const cw = W - pl - pr, ch = H - pt - pb;

  const maxP99 = Math.max(...buckets.map(b => b.p99), 1);
  const yMax   = maxP99 * 1.1;

  const xOf = i => pl + (i / (buckets.length - 1)) * cw;
  const yOf = v => pt + ch - (v / yMax) * ch;

  // Polyline segments split at zero-gaps.
  const segments = [];
  let cur = [];
  for (let i = 0; i < buckets.length; i++) {
    if (buckets[i].p99 > 0) {
      cur.push(`${xOf(i).toFixed(1)},${yOf(buckets[i].p99).toFixed(1)}`);
    } else {
      if (cur.length) { segments.push(cur); cur = []; }
    }
  }
  if (cur.length) segments.push(cur);

  const lines = segments.map(pts =>
    `<polyline points="${pts.join(' ')}" fill="none" stroke="var(--accent)" stroke-width="1.8" stroke-linejoin="round" stroke-linecap="round"/>`
  ).join('');

  const dots = buckets
    .map((b, i) => b.p99 > 0
      ? `<circle cx="${xOf(i).toFixed(1)}" cy="${yOf(b.p99).toFixed(1)}" r="2.5" fill="var(--accent)"/>`
      : '')
    .join('');

  const xLabels = [0, 15, 30, 45, 59].map(i => {
    const label = i === 59 ? 'now' : `${i - 59}m`;
    return `<text x="${xOf(i).toFixed(1)}" y="${H - 6}" text-anchor="middle" class="chart-label">${label}</text>`;
  }).join('');

  const yLabels = [
    `<text x="${pl - 6}" y="${yOf(0) + 4}" text-anchor="end" class="chart-label">0</text>`,
    `<text x="${pl - 6}" y="${yOf(yMax) + 4}" text-anchor="end" class="chart-label">${fmt(yMax, 0)}</text>`,
  ].join('');

  const grids = [0.25, 0.5, 0.75].map(f => {
    const y = yOf(yMax * f);
    return `<line x1="${pl}" y1="${y.toFixed(1)}" x2="${pl+cw}" y2="${y.toFixed(1)}" stroke="var(--border)" stroke-width="1"/>`;
  }).join('');

  return `<svg viewBox="0 0 ${W} ${H}" width="100%" xmlns="http://www.w3.org/2000/svg" class="chart-svg">
    ${grids}
    <line x1="${pl}" y1="${pt}" x2="${pl}" y2="${pt+ch}" stroke="var(--border-2)" stroke-width="1"/>
    <line x1="${pl}" y1="${pt+ch}" x2="${pl+cw}" y2="${pt+ch}" stroke="var(--border-2)" stroke-width="1"/>
    ${lines}${dots}${xLabels}${yLabels}
  </svg>`;
}

// ── Histogram (US-12) ─────────────────────────────────────────────────────────
function renderHistogram(stat) {
  if (!stat || stat.total_count === 0) {
    return '<div class="placeholder">No data for this endpoint</div>';
  }
  const buckets = stat.buckets;
  const counts  = buckets.map((b, i) => i === 0 ? b.count : b.count - buckets[i-1].count);
  const maxCount = Math.max(...counts, 1);
  const labels   = buckets.map(b => b.le === -1 ? '+Inf' : `≤${b.le} ms`);

  const bars = counts.map((c, i) => {
    const barPct  = (c / maxCount * 100).toFixed(1);
    const sharePct = (c / stat.total_count * 100).toFixed(1);
    return `
      <div class="hist-row">
        <div class="hist-label">${labels[i]}</div>
        <div class="hist-bar-wrap"><div class="hist-bar" style="width:${barPct}%"></div></div>
        <div class="hist-count">${fmt(c)} <span class="hist-pct">${sharePct}%</span></div>
      </div>`;
  }).join('');

  return `<div class="hist-grid">${bars}</div>
    <div class="hist-total">Total: ${fmt(stat.total_count)} requests</div>`;
}

function renderStatusTab(groups) {
  const cards = groups.map(g => {
    const { cls } = STATUS_COLORS[g.class] || { cls: '' };
    return `
      <div class="status-card">
        <div class="status-card-class ${cls}">${g.class}</div>
        <div class="status-card-count">${fmt(g.count)}</div>
        <div class="status-card-rate ${cls}">${fmt(g.rate, 1)}%</div>
        <div class="status-bar-wrap">
          <div class="status-bar ${cls}" style="width:${g.rate.toFixed(1)}%"></div>
        </div>
      </div>`;
  }).join('');
  return `<div class="status-grid" style="padding:16px 20px">${cards}</div>`;
}

async function fetchLatency(method, path) {
  try {
    const rp = rangeParams();
    const sep = rp ? '&' : '?';
    const [resL, resH] = await Promise.all([
      fetch(`/metrics/latency?method=${encodeURIComponent(method)}&path=${encodeURIComponent(path)}`),
      fetch(`/metrics/histogram${rp}${sep}method=${encodeURIComponent(method)}&path=${encodeURIComponent(path)}`),
    ]);
    if (!resL.ok || !resH.ok) return;
    const [timeBuckets, histStat] = await Promise.all([resL.json(), resH.json()]);

    let panel = el('chart-panel');
    if (!panel) {
      panel = document.createElement('div');
      panel.id = 'chart-panel';
      panel.className = 'chart-panel';
      el('endpoints').insertAdjacentElement('afterend', panel);
    }

    panel.innerHTML = `
      <div class="chart-header">
        <span class="chart-title">${escHtml(method)} <span class="cell-path">${escHtml(path)}</span></span>
        <div style="display:flex;gap:8px;align-items:center">
          <div class="panel-tabs">
            <button class="panel-tab active" data-tab="chart">Chart</button>
            <button class="panel-tab" data-tab="histogram">Histogram</button>
            <button class="panel-tab" data-tab="status">Status</button>
          </div>
          <button class="chart-close" id="chart-close">×</button>
        </div>
      </div>
      <div id="tab-chart" class="panel-tab-content">
        <div class="chart-body">${buildSVG(timeBuckets)}</div>
      </div>
      <div id="tab-histogram" class="panel-tab-content" style="display:none;padding:16px 20px">
        ${renderHistogram(histStat)}
      </div>
      <div id="tab-status" class="panel-tab-content" style="display:none">
        <div class="placeholder" style="padding:16px 20px">loading…</div>
      </div>`;

    panel.style.display = '';
    panel.scrollIntoView({ behavior: 'smooth', block: 'nearest' });

    el('chart-close').addEventListener('click', () => {
      panel.style.display = 'none';
      _chartEndpoint = null;
    });

    let _statusLoaded = false;
    panel.querySelectorAll('.panel-tab').forEach(btn => {
      btn.addEventListener('click', async () => {
        panel.querySelectorAll('.panel-tab').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        el('tab-chart').style.display     = btn.dataset.tab === 'chart'     ? '' : 'none';
        el('tab-histogram').style.display = btn.dataset.tab === 'histogram' ? '' : 'none';
        el('tab-status').style.display    = btn.dataset.tab === 'status'    ? '' : 'none';
        if (btn.dataset.tab === 'status' && !_statusLoaded) {
          _statusLoaded = true;
          try {
            const res = await fetch(`/metrics/status${rp}${sep}method=${encodeURIComponent(method)}&path=${encodeURIComponent(path)}`);
            if (res.ok) {
              el('tab-status').innerHTML = renderStatusTab(await res.json());
            }
          } catch (_) { /* ignore */ }
        }
      });
    });
  } catch (_) { /* ignore */ }
}

// ── Alerts badge (US-22) ──────────────────────────────────────────────────────
async function fetchAlerts() {
  try {
    const res = await fetch('/alerts/active');
    if (!res.ok) return;
    const alerts = await res.json();

    const badge   = el('alert-badge');
    const section = el('alerts');

    if (!alerts || alerts.length === 0) {
      badge.textContent = '';
      section.style.display = 'none';
      return;
    }

    badge.textContent = alerts.length + (alerts.length === 1 ? ' alert' : ' alerts');
    badge.onclick = () => section.scrollIntoView({ behavior: 'smooth', block: 'start' });

    const rows = alerts.map(a => {
      const triggered = new Date(a.triggered_at).toLocaleTimeString();
      const kindCls   = a.kind === 'error_rate' ? 'kind-error-rate'
                       : a.kind === 'throughput' ? 'kind-throughput' : 'kind-latency';
      const kindLabel = a.kind === 'error_rate' ? 'error rate'
                       : a.kind === 'throughput' ? 'throughput' : 'latency';
      let valueCells;
      if (a.kind === 'error_rate') {
        valueCells = `
          <td class="danger">${fmt(a.error_rate,1)}<span class="cell-unit">%</span></td>
          <td>&gt; ${fmt(a.error_rate_threshold,1)}<span class="cell-unit">%</span></td>
          <td class="cell-time">—</td>`;
      } else if (a.kind === 'throughput') {
        const dropActual = a.baseline_rps > 0 ? (100 - a.current_rps / a.baseline_rps * 100).toFixed(1) : '—';
        valueCells = `
          <td class="danger">${Number(a.current_rps).toFixed(2)}<span class="cell-unit">rps</span></td>
          <td>${Number(a.baseline_rps).toFixed(2)}<span class="cell-unit">rps</span></td>
          <td class="danger">${dropActual}%↓</td>`;
      } else {
        const ratio = a.baseline_p99 > 0 ? (a.current_p99 / a.baseline_p99).toFixed(1) : '—';
        valueCells = `
          <td class="danger">${fmt(a.current_p99,1)}<span class="cell-unit">ms</span></td>
          <td>${fmt(a.baseline_p99,1)}<span class="cell-unit">ms</span></td>
          <td class="danger">${ratio}×</td>`;
      }
      return `<tr>
        <td><span class="kind-badge ${kindCls}">${kindLabel}</span></td>
        <td>${escHtml(a.method)}</td>
        <td><span class="cell-path">${escHtml(a.path)}</span></td>
        ${valueCells}
        <td class="cell-time">${triggered}</td>
      </tr>`;
    }).join('');

    section.querySelector('.section-body').innerHTML = `
      <div class="table-wrap">
        <table class="data-table alerts-table">
          <thead><tr>
            <th>Kind</th><th>Method</th><th>Path</th>
            <th>Value</th><th>Threshold</th>
            <th>Ratio</th><th>Triggered</th>
          </tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>`;
    section.style.display = '';
  } catch (_) { /* keep last known state */ }
}

// ── Status Breakdown (US-28) ──────────────────────────────────────────────────
const STATUS_COLORS = {
  '2xx': { cls: 'status-2xx', label: '2xx' },
  '3xx': { cls: 'status-3xx', label: '3xx' },
  '4xx': { cls: 'status-4xx', label: '4xx' },
  '5xx': { cls: 'status-5xx', label: '5xx' },
};

async function fetchStatusBreakdown() {
  try {
    const res = await fetch('/metrics/status' + rangeParams());
    if (!res.ok) return;
    const groups = await res.json();
    const body = el('status-breakdown').querySelector('.section-body');
    if (!body) return;

    const cards = groups.map(g => {
      const { cls } = STATUS_COLORS[g.class] || { cls: '' };
      return `
        <div class="status-card">
          <div class="status-card-class ${cls}">${g.class}</div>
          <div class="status-card-count">${fmt(g.count)}</div>
          <div class="status-card-rate ${cls}">${fmt(g.rate, 1)}%</div>
          <div class="status-bar-wrap">
            <div class="status-bar ${cls}" style="width:${g.rate.toFixed(1)}%"></div>
          </div>
        </div>`;
    }).join('');

    body.innerHTML = `<div class="status-grid">${cards}</div>`;
  } catch (_) { /* keep last known state */ }
}

// ── Requests section tab switching ───────────────────────────────────────────
function initRequestsTabs() {
  const tabs = el('requests-tabs');
  if (!tabs) return;
  tabs.addEventListener('click', e => {
    const btn = e.target.closest('.panel-tab');
    if (!btn) return;
    tabs.querySelectorAll('.panel-tab').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
    el('requests-tab-log').style.display     = btn.dataset.tab === 'log'     ? '' : 'none';
    el('requests-tab-slowest').style.display = btn.dataset.tab === 'slowest' ? '' : 'none';
  });
}

// ── Slowest Requests (US-29) ──────────────────────────────────────────────────
function buildRequestRows(records) {
  if (records.length === 0) {
    return `<tr><td colspan="6" class="no-data">No data</td></tr>`;
  }
  return records.map(r => {
    const t   = new Date(r.timestamp);
    const hms = t.toLocaleTimeString('en-US', { hour12: false });
    const ms  = String(t.getMilliseconds()).padStart(3, '0');
    const cls = METHOD_COLORS[r.method] || 'badge-other';
    const sc  = statusClass(r.status_code);
    const durCls = r.duration_ms >= 1000 ? 'danger' : '';
    const traceCell = r.trace_id
      ? `<span class="trace-badge trace-badge-link" title="Click to view trace" data-traceid="${escHtml(r.trace_id)}">${escHtml(r.trace_id.slice(0, 8))}</span>`
      : '<span class="cell-dim">—</span>';
    return `<tr>
      <td class="cell-time">${hms}.${ms}</td>
      <td><span class="method-badge ${cls}">${escHtml(r.method)}</span></td>
      <td><span class="cell-path">${escHtml(r.path)}</span></td>
      <td class="cell-status ${sc}">${r.status_code}</td>
      <td class="cell-dur ${durCls}">${Number(r.duration_ms).toFixed(1)}<span class="cell-unit">ms</span></td>
      <td>${traceCell}</td>
    </tr>`;
  }).join('');
}

async function fetchSlowestRequests() {
  try {
    const rp  = rangeParams();
    const sep = rp ? '&' : '?';
    const res = await fetch('/metrics/slowest-requests' + rp + sep + 'n=10');
    if (!res.ok) return;
    const records = await res.json();
    const pane = el('requests-tab-slowest');
    if (!pane) return;
    pane.innerHTML = `
      <div class="section-body">
        <div class="table-wrap">
          <table class="data-table">
            <thead><tr>
              <th>Time</th><th>Method</th><th>Path</th><th>Status</th><th>Duration</th><th>Trace</th>
            </tr></thead>
            <tbody>${buildRequestRows(records)}</tbody>
          </table>
        </div>
      </div>`;
    pane.querySelectorAll('.trace-badge-link').forEach(badge => {
      badge.addEventListener('click', e => {
        e.stopPropagation();
        openTraceDrawer(badge.dataset.traceid);
      });
    });
  } catch (_) { /* keep last known state */ }
}

// ── Request Log (US-27) ───────────────────────────────────────────────────────
const RL_PAGE_SIZE = 20;
let _rlData   = [];
let _rlPath   = '';
let _rlMethod = 'all';
let _rlStatus = 'all';
let _rlPage   = 0;

const METHOD_COLORS = {
  GET:    'badge-get',
  POST:   'badge-post',
  PUT:    'badge-put',
  PATCH:  'badge-patch',
  DELETE: 'badge-delete',
};

function statusClass(code) {
  if (code >= 500) return 'status-5xx';
  if (code >= 400) return 'status-4xx';
  if (code >= 300) return 'status-3xx';
  return 'status-2xx';
}

function renderRequestLog() {
  const body = el('requests-tab-log');
  if (!body) return;

  const filtered = _rlData.filter(r => {
    if (_rlPath   && !r.path.toLowerCase().includes(_rlPath.toLowerCase())) return false;
    if (_rlMethod !== 'all' && r.method !== _rlMethod) return false;
    if (_rlStatus !== 'all') {
      if (Math.floor(r.status_code / 100) !== parseInt(_rlStatus[0])) return false;
    }
    return true;
  });

  const totalPages = Math.max(1, Math.ceil(filtered.length / RL_PAGE_SIZE));
  if (_rlPage >= totalPages) _rlPage = totalPages - 1;

  const start = _rlPage * RL_PAGE_SIZE;
  const pageRows = filtered.slice(start, start + RL_PAGE_SIZE);
  const trRows = buildRequestRows(pageRows);

  const rangeStart = filtered.length === 0 ? 0 : start + 1;
  const rangeEnd   = Math.min(start + RL_PAGE_SIZE, filtered.length);
  const paginationInfo = `${rangeStart}–${rangeEnd} of ${filtered.length}`;

  body.innerHTML = `
    <div class="section-body">
    <div class="table-toolbar rl-toolbar">
      <input id="rl-filter" class="filter-input" type="text"
             placeholder="Filter path…" value="${escHtml(_rlPath)}" autocomplete="off">
      <select id="rl-method" class="rl-select">
        <option value="all">All Methods</option>
        <option value="GET"    ${_rlMethod==='GET'    ?'selected':''}>GET</option>
        <option value="POST"   ${_rlMethod==='POST'   ?'selected':''}>POST</option>
        <option value="PUT"    ${_rlMethod==='PUT'    ?'selected':''}>PUT</option>
        <option value="PATCH"  ${_rlMethod==='PATCH'  ?'selected':''}>PATCH</option>
        <option value="DELETE" ${_rlMethod==='DELETE' ?'selected':''}>DELETE</option>
      </select>
      <select id="rl-status" class="rl-select">
        <option value="all">All Status</option>
        <option value="2xx" ${_rlStatus==='2xx'?'selected':''}>2xx</option>
        <option value="3xx" ${_rlStatus==='3xx'?'selected':''}>3xx</option>
        <option value="4xx" ${_rlStatus==='4xx'?'selected':''}>4xx</option>
        <option value="5xx" ${_rlStatus==='5xx'?'selected':''}>5xx</option>
      </select>
    </div>
    <div class="table-wrap">
      <table class="data-table">
        <thead><tr>
          <th>Time</th><th>Method</th><th>Path</th><th>Status</th><th>Duration</th><th>Trace</th>
        </tr></thead>
        <tbody>${trRows}</tbody>
      </table>
    </div>
    <div class="pagination">
      <button class="page-btn" id="rl-prev" ${_rlPage === 0 ? 'disabled' : ''}>← Prev</button>
      <span class="page-info">${paginationInfo}</span>
      <button class="page-btn" id="rl-next" ${_rlPage >= totalPages - 1 ? 'disabled' : ''}>Next →</button>
    </div>
    </div>`;

  el('rl-filter').addEventListener('input', e => { _rlPath = e.target.value; _rlPage = 0; renderRequestLog(); });
  el('rl-method').addEventListener('change', e => { _rlMethod = e.target.value; _rlPage = 0; renderRequestLog(); });
  el('rl-status').addEventListener('change', e => { _rlStatus = e.target.value; _rlPage = 0; renderRequestLog(); });
  el('rl-prev').addEventListener('click', () => { _rlPage--; renderRequestLog(); });
  el('rl-next').addEventListener('click', () => { _rlPage++; renderRequestLog(); });

  body.querySelectorAll('.trace-badge-link').forEach(badge => {
    badge.addEventListener('click', e => {
      e.stopPropagation();
      openTraceDrawer(badge.dataset.traceid);
    });
  });
}

async function fetchRequests() {
  try {
    const rp  = rangeParams();
    const sep = rp ? '&' : '?';
    const res = await fetch('/metrics/requests' + rp + sep + 'n=100');
    if (!res.ok) return;
    _rlData = await res.json();
    renderRequestLog();
  } catch (_) { /* keep last known state */ }
}

// ── Upstream Health (US-39) ───────────────────────────────────────────────────
const HEALTH_COLORS = { healthy: 'health-healthy', degraded: 'health-degraded', down: 'health-down', unknown: 'health-unknown' };

async function fetchHealth() {
  try {
    const res = await fetch('/health');
    if (!res.ok) return;
    const d = await res.json();
    const indicator = el('upstream-health');
    if (!indicator || !d.upstream) { if (indicator) indicator.style.display = 'none'; return; }
    const s = d.upstream;
    const cls = HEALTH_COLORS[s.status] || 'health-unknown';
    const latency = s.latency_ms > 0 ? ` <span class="health-latency">${Number(s.latency_ms).toFixed(0)}ms</span>` : '';
    indicator.innerHTML = `upstream: <span class="${cls}">●</span> <span class="${cls}">${s.status}</span>${latency}`;
    indicator.style.display = '';
  } catch (_) { /* keep last known state */ }
}

// ── Refresh loop ──────────────────────────────────────────────────────────────
// ── Alert History (US-50) ─────────────────────────────────────────────────────
let _ahKindFilter = 'all';

async function fetchAlertHistory() {
  try {
    const res = await fetch('/alerts/history');
    if (!res.ok) return;
    const records = await res.json();
    const section = el('alert-history');
    if (!section) return;
    if (!records || records.length === 0) {
      section.style.display = 'none';
      return;
    }
    renderAlertHistory(records);
    section.style.display = '';
  } catch (_) {}
}

function renderAlertHistory(records) {
  const section = el('alert-history');
  const body    = section.querySelector('.section-body');
  const tabsEl  = el('ah-kind-tabs');

  // Collect kinds present.
  const kindsPresent = [...new Set(records.map(r => r.kind))];
  const kindLabels   = { latency: 'Latency', error_rate: 'Error Rate', throughput: 'Throughput', statistical: 'Statistical' };

  // Build kind filter tabs.
  const allKinds = ['all', ...kindsPresent];
  tabsEl.innerHTML = allKinds.map(k =>
    `<button class="panel-tab${_ahKindFilter === k ? ' active' : ''}" data-kind="${k}">${k === 'all' ? 'All' : (kindLabels[k] || k)}</button>`
  ).join('');
  tabsEl.onclick = e => {
    const btn = e.target.closest('.panel-tab');
    if (!btn) return;
    _ahKindFilter = btn.dataset.kind;
    renderAlertHistory(records);
  };

  const filtered = _ahKindFilter === 'all' ? records : records.filter(r => r.kind === _ahKindFilter);

  // Compute timeline range.
  const tMin = Math.min(...filtered.map(r => new Date(r.triggered_at).getTime()));
  const tMax = Math.max(...filtered.map(r => r.resolved_at ? new Date(r.resolved_at).getTime() : Date.now()));
  const span = tMax - tMin || 1;

  const rows = filtered.map(r => {
    const kindCls = r.kind === 'error_rate' ? 'kind-error-rate'
                  : r.kind === 'throughput' ? 'kind-throughput'
                  : r.kind === 'statistical' ? 'kind-statistical'
                  : 'kind-latency';
    const kindLabel = kindLabels[r.kind] || r.kind;
    const triggered = new Date(r.triggered_at).toLocaleTimeString();
    const resolvedAt = r.resolved_at ? new Date(r.resolved_at) : null;
    const resolvedCell = resolvedAt
      ? resolvedAt.toLocaleTimeString()
      : '<span class="ah-ongoing">active</span>';

    const durMs = resolvedAt
      ? resolvedAt.getTime() - new Date(r.triggered_at).getTime()
      : Date.now() - new Date(r.triggered_at).getTime();
    const durStr = resolvedAt ? fmtDuration(durMs) : '<span class="ah-ongoing">ongoing</span>';

    // Timeline bar
    const tStart  = new Date(r.triggered_at).getTime();
    const tEnd    = resolvedAt ? resolvedAt.getTime() : Date.now();
    const left    = ((tStart - tMin) / span * 100).toFixed(1);
    const width   = Math.max(((tEnd - tStart) / span * 100), 1).toFixed(1);
    const barCls  = resolvedAt ? 'ah-resolved' : 'ah-active';

    return `<tr>
      <td><span class="kind-badge ${kindCls}">${kindLabel}</span></td>
      <td><span class="method-badge ${METHOD_COLORS[r.method]||'badge-other'}">${escHtml(r.method)}</span></td>
      <td><span class="cell-path">${escHtml(r.path)}</span></td>
      <td class="cell-time">${triggered}</td>
      <td class="cell-time">${resolvedCell}</td>
      <td>${durStr}</td>
      <td class="ah-timeline-cell">
        <div class="ah-timeline-wrap">
          <div class="ah-timeline-bar ${barCls}" style="left:${left}%;width:${width}%"></div>
        </div>
      </td>
    </tr>`;
  }).join('');

  body.innerHTML = `
    <div class="table-wrap">
      <table class="data-table">
        <thead><tr>
          <th>Kind</th><th>Method</th><th>Path</th>
          <th>Triggered</th><th>Resolved</th><th>Duration</th><th>Timeline</th>
        </tr></thead>
        <tbody>${rows || `<tr><td colspan="7" class="no-data">No data</td></tr>`}</tbody>
      </table>
    </div>`;
}

function fmtDuration(ms) {
  if (ms < 1000) return `${ms}ms`;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60), rs = s % 60;
  if (m < 60) return `${m}m ${rs}s`;
  const h = Math.floor(m / 60), rm = m % 60;
  return `${h}h ${rm}m`;
}

// ── Trace detail drawer ───────────────────────────────────────────────────────
function openTraceDrawer(traceId) {
  const overlay   = el('trace-drawer-overlay');
  const drawer    = el('trace-drawer');
  const body      = el('trace-drawer-body');
  const shortId   = el('trace-drawer-id');
  if (!overlay || !drawer || !body) return;

  overlay.style.display = '';
  drawer.style.display  = 'flex';
  shortId.textContent   = traceId.slice(0, 8) + '…' + traceId.slice(-4);
  body.innerHTML        = '<div class="wf-loading"><span class="cell-dim">loading…</span></div>';

  fetch('/traces/' + encodeURIComponent(traceId))
    .then(r => r.ok ? r.json() : null)
    .then(detail => {
      if (!detail) {
        body.innerHTML = '<div class="wf-loading"><span class="cell-dim">No spans found</span></div>';
        return;
      }
      renderWaterfall(detail, body);
    })
    .catch(() => {
      body.innerHTML = '<div class="wf-loading"><span class="danger">Failed to load trace</span></div>';
    });
}

function closeTraceDrawer() {
  const overlay = el('trace-drawer-overlay');
  const drawer  = el('trace-drawer');
  if (overlay) overlay.style.display = 'none';
  if (drawer)  drawer.style.display  = 'none';
}

// ── Traces (APM waterfall) ────────────────────────────────────────────────────
const TL_PAGE_SIZE = 20;
let _selectedTraceID = null;
let _traceList = [];
let _tlPage = 0;

async function fetchTraces() {
  try {
    const rp  = rangeParams();
    const res = await fetch('/traces' + (rp || ''));
    if (!res.ok) return;
    const fresh = await res.json();
    if (JSON.stringify(fresh.map(t => t.trace_id)) !== JSON.stringify(_traceList.map(t => t.trace_id))) {
      _tlPage = 0;
    }
    _traceList = fresh;
    renderTraceList();
    if (_selectedTraceID && _traceList.some(t => t.trace_id === _selectedTraceID)) {
      expandTrace(_selectedTraceID);
    } else {
      _selectedTraceID = null;
    }
  } catch (_) {}
}

function renderTraceList() {
  const list = el('traces-list');
  if (!list) return;

  if (!_traceList || _traceList.length === 0) {
    list.innerHTML = '<div class="placeholder">No traces in this window</div>';
    return;
  }

  const totalPages = Math.max(1, Math.ceil(_traceList.length / TL_PAGE_SIZE));
  if (_tlPage >= totalPages) _tlPage = totalPages - 1;

  const start    = _tlPage * TL_PAGE_SIZE;
  const pageData = _traceList.slice(start, start + TL_PAGE_SIZE);

  const rangeStart = start + 1;
  const rangeEnd   = Math.min(start + TL_PAGE_SIZE, _traceList.length);

  const rows = pageData.map(t => {
    const tid    = escHtml(t.trace_id);
    const short  = tid.slice(0, 8) + '…' + tid.slice(-4);
    const dur    = fmt(t.total_duration_ms, 1);
    const errCls = t.has_errors ? 'danger' : 'cell-dim';
    const errTxt = t.has_errors ? '✕ error' : '✓ ok';
    const ts     = new Date(t.start_time).toLocaleTimeString();
    const open   = _selectedTraceID === t.trace_id;
    const chevron = open ? '▾' : '▸';
    return `<tr class="trace-row${open ? ' trace-row-active' : ''}" data-traceid="${tid}" style="cursor:pointer">
      <td><span class="trace-chevron">${chevron}</span> <span class="trace-id" title="${tid}">${short}</span></td>
      <td class="cell-time">${ts}</td>
      <td>${dur}<span class="cell-unit">ms</span></td>
      <td>${t.span_count}</td>
      <td><span class="${errCls}">${errTxt}</span></td>
    </tr>
    <tr class="trace-spans-row" data-traceid="${tid}" style="display:${open ? '' : 'none'}">
      <td colspan="5" class="trace-spans-cell">
        <div class="wf-loading" id="wf-${tid}"><span class="cell-dim">loading…</span></div>
      </td>
    </tr>`;
  }).join('');

  list.innerHTML = `
    <div class="table-wrap">
      <table class="data-table">
        <thead><tr>
          <th>Trace ID</th><th>Start</th><th>Duration</th><th>Spans</th><th>Status</th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>
    <div class="pagination">
      <button class="page-btn" id="tl-prev" ${_tlPage === 0 ? 'disabled' : ''}>← Prev</button>
      <span class="page-info">${rangeStart}–${rangeEnd} of ${_traceList.length}</span>
      <button class="page-btn" id="tl-next" ${_tlPage >= totalPages - 1 ? 'disabled' : ''}>Next →</button>
    </div>`;

  el('tl-prev').addEventListener('click', () => { _tlPage--; renderTraceList(); });
  el('tl-next').addEventListener('click', () => { _tlPage++; renderTraceList(); });

  list.querySelectorAll('.trace-row').forEach(row => {
    row.addEventListener('click', () => {
      const tid = row.dataset.traceid;
      if (_selectedTraceID === tid) {
        collapseTrace(tid);
        _selectedTraceID = null;
      } else {
        if (_selectedTraceID) collapseTrace(_selectedTraceID);
        _selectedTraceID = tid;
        expandTrace(tid);
      }
    });
  });
}

function collapseTrace(tid) {
  const list = el('traces-list');
  if (!list) return;
  list.querySelectorAll(`.trace-row[data-traceid="${CSS.escape(tid)}"]`).forEach(r => {
    r.classList.remove('trace-row-active');
    r.querySelector('.trace-chevron').textContent = '▸';
  });
  list.querySelectorAll(`.trace-spans-row[data-traceid="${CSS.escape(tid)}"]`).forEach(r => {
    r.style.display = 'none';
  });
}

function expandTrace(tid) {
  const list = el('traces-list');
  if (!list) return;
  list.querySelectorAll(`.trace-row[data-traceid="${CSS.escape(tid)}"]`).forEach(r => {
    r.classList.add('trace-row-active');
    r.querySelector('.trace-chevron').textContent = '▾';
  });
  list.querySelectorAll(`.trace-spans-row[data-traceid="${CSS.escape(tid)}"]`).forEach(r => {
    r.style.display = '';
  });
  fetchTraceDetail(tid);
}

// kind → CSS class for the waterfall bar color
const KIND_COLORS = {
  proxy:      'wf-bar-proxy',
  controller: 'wf-bar-controller',
  db:         'wf-bar-db',
  cache:      'wf-bar-cache',
  event:      'wf-bar-event',
  view:       'wf-bar-view',
  rpc:        'wf-bar-rpc',
  routing:    'wf-bar-routing',
  boot:       'wf-bar-boot',
  send:       'wf-bar-send',
  custom:     'wf-bar-custom',
};

// kind → short display label
const KIND_LABELS = {
  proxy:      'HTTP',
  controller: 'ctrl',
  db:         'DB',
  cache:      'cache',
  event:      'event',
  view:       'view',
  rpc:        'RPC',
  routing:    'route',
  boot:       'BOOT',
  send:       'SEND',
  custom:     'SPAN',
};

async function fetchTraceDetail(traceID) {
  const wfEl = el('wf-' + traceID);
  if (!wfEl) return;
  try {
    const res = await fetch('/traces/' + encodeURIComponent(traceID));
    if (!res.ok) return;
    const detail = await res.json();
    renderWaterfall(detail, wfEl);
  } catch (_) {}
}

function renderWaterfall(detail, container) {
  // Support both new {trace_id, total_ms, spans:[]} and legacy []
  const spans  = Array.isArray(detail) ? detail : (detail.spans || []);
  const totalMs = Array.isArray(detail) ? null : detail.total_ms;

  spans.sort((a, b) => (a.start_ms || 0) - (b.start_ms || 0));

  if (!spans || spans.length === 0) {
    container.innerHTML = '<div class="wf-loading"><span class="cell-dim">No spans found</span></div>';
    return;
  }

  // Build depth map for indentation from parent_span_id chain.
  const depthMap = {};
  function spanDepth(s) {
    if (depthMap[s.span_id] !== undefined) return depthMap[s.span_id];
    if (!s.parent_span_id) return (depthMap[s.span_id] = 0);
    const parent = spans.find(p => p.span_id === s.parent_span_id);
    return (depthMap[s.span_id] = parent ? spanDepth(parent) + 1 : 0);
  }
  spans.forEach(s => spanDepth(s));

  // total duration: prefer server-provided, else compute from start_ms + duration_ms
  const total = totalMs != null
    ? totalMs
    : Math.max(...spans.map(s => (s.start_ms || 0) + s.duration_ms)) || 1;

  const bars = spans.map((s, i) => {
    const d       = depthMap[s.span_id] || 0;
    const indent  = d * 16;
    const left    = ((s.start_ms || 0) / (total || 1) * 100).toFixed(2);
    const width   = Math.max((s.duration_ms / (total || 1) * 100), 0.4).toFixed(2);

    const kind    = s.kind || 'proxy';
    const barCls  = s.status === 'error' ? 'wf-bar-error'
                  : (KIND_COLORS[kind] || 'wf-bar-proxy');
    const kindLbl = KIND_LABELS[kind] || kind;

    // Label line: for proxy spans show method+path+status, for inner spans show name+kind+duration
    let labelHtml;
    if (kind === 'proxy') {
      const parts  = (s.name || '').split(' ');
      const method = parts[0] || '';
      const path   = parts.slice(1).join(' ') || '';
      const stCls  = s.status_code >= 500 ? 'danger' : s.status_code >= 400 ? 'warning' : 'cell-dim';
      labelHtml = `<span class="wf-kind-badge wf-kind-proxy">${kindLbl}</span>
        <span class="wf-method">${escHtml(method)}</span>
        <span class="wf-path">${escHtml(path)}</span>
        <span class="wf-meta ${stCls}">${s.status_code} · ${fmt(s.duration_ms, 1)}ms</span>`;
    } else {
      const errMeta = s.status === 'error' ? ' <span class="danger">err</span>' : '';
      // Show first DB attribute inline (e.g. db.query truncated)
      const attrs   = s.attributes || {};
      const phpVer   = attrs['php.version'] ? `PHP ${attrs['php.version']}` + (attrs['php.peak_memory'] ? ` · ${attrs['php.peak_memory']}` : '') : '';
      const ctrlMeta = attrs['http.method'] ? `${attrs['http.method']}${attrs['http.route'] ? ' ' + attrs['http.route'] : ''}` : '';
      const snippet  = attrs['db.query'] || attrs['cache.key'] || attrs['http.url'] || attrs['twig.template'] || phpVer || ctrlMeta || '';
      const snipHtml = snippet
        ? `<span class="wf-attr-snippet" title="${escHtml(snippet)}">${escHtml(snippet.slice(0, 60))}${snippet.length > 60 ? '…' : ''}</span>`
        : '';
      labelHtml = `<span class="wf-kind-badge wf-kind-${escHtml(kind)}">${escHtml(kindLbl)}</span>
        <span class="wf-path">${escHtml(s.name)}</span>
        ${snipHtml}
        <span class="wf-meta cell-dim">${fmt(s.duration_ms, 1)}ms${errMeta}</span>`;
    }

    const tooltip = `${escHtml(s.name)} [${escHtml(kind)}] — ${fmt(s.duration_ms, 1)}ms`;
    return `<div class="wf-row" data-span-idx="${i}">
      <div class="wf-label" style="padding-left:${indent}px">${labelHtml}</div>
      <div class="wf-track" title="${tooltip}">
        <div class="wf-bar ${barCls}" style="left:${left}%;width:${width}%"></div>
      </div>
    </div>`;
  }).join('');

  const halfMs = fmt(total / 2, 0);
  const totStr = fmt(total, 1);
  container.innerHTML = `
    <div class="wf-header">
      <span class="wf-total">${spans.length} span${spans.length !== 1 ? 's' : ''} · ${totStr}ms total</span>
      <div class="wf-time-axis">
        <span>0ms</span><span>${halfMs}ms</span><span>${totStr}ms</span>
      </div>
    </div>
    <div class="wf-container">${bars}</div>`;

  // Attach click-to-expand for each span row.
  container.querySelectorAll('.wf-row').forEach(row => {
    row.addEventListener('click', () => {
      const existing = row.nextElementSibling;
      if (existing && existing.classList.contains('wf-span-detail')) {
        existing.remove();
        return;
      }
      const idx = parseInt(row.dataset.spanIdx, 10);
      const s   = spans[idx];
      if (!s) return;

      const attrs   = s.attributes || {};
      const attrKeys = Object.keys(attrs);
      const baseRows = [
        ['name',     s.name],
        ['kind',     s.kind || 'proxy'],
        ['status',   s.status || 'ok'],
        ['start',    `+${fmt(s.start_ms || 0, 2)}ms`],
        ['duration', `${fmt(s.duration_ms, 2)}ms`],
        ...(s.status_code ? [['http.status', String(s.status_code)]] : []),
      ];
      const allRows = [...baseRows, ...attrKeys.map(k => [k, attrs[k]])];
      const rowsHtml = allRows.map(([k, v]) =>
        `<tr><td>${escHtml(k)}</td><td>${escHtml(v)}</td></tr>`
      ).join('');

      const detail = document.createElement('div');
      detail.className = 'wf-span-detail';
      detail.innerHTML = `<table class="wf-span-detail-table"><tbody>${rowsHtml}</tbody></table>`;
      row.after(detail);
    });
  });
}

// ── Router ────────────────────────────────────────────────────────────────────
const PAGES = ['overview', 'endpoints', 'errors', 'requests', 'traces'];

function currentPage() {
  const h = location.hash.replace('#', '');
  return PAGES.includes(h) ? h : 'overview';
}

function navigate(page) {
  location.hash = page;
}

function showPage(page) {
  document.querySelectorAll('section[data-page]').forEach(s => {
    // sections with display:none managed by their own logic (alerts, alert-history)
    // keep them hidden even if they're on the active page, unless they have data
    if (s.dataset.page !== page) {
      s.style.display = 'none';
    } else {
      // restore display only if the section wasn't intentionally hidden by data logic
      // (alert-history and alerts manage their own display state)
      const managed = s.id === 'alerts' || s.id === 'alert-history';
      if (!managed) s.style.display = '';
    }
  });
  document.querySelectorAll('.nav-item').forEach(b => {
    b.classList.toggle('active', b.dataset.page === page);
  });
}

const PAGE_FETCHES = {
  overview:  () => Promise.all([fetchHeatmap(), fetchSummary(), fetchStatusBreakdown()]),
  endpoints: () => Promise.all([fetchEndpoints(), fetchApdex(), fetchAnomalyScores()]),
  errors:    () => Promise.all([fetchErrorFingerprints(), fetchAlerts(), fetchAlertHistory()]),
  requests:  () => Promise.all([fetchRequests(), fetchSlowestRequests()]),
  traces:    () => fetchTraces(),
};

window.addEventListener('hashchange', () => {
  const page = currentPage();
  showPage(page);
  PAGE_FETCHES[page]?.();
});

async function refresh() {
  fetchAlerts();   // always: keeps alert badge in header current
  fetchHealth();   // always: keeps statusbar current
  await PAGE_FETCHES[currentPage()]?.();
}

// ── Boot ──────────────────────────────────────────────────────────────────────
initTimeRange();
initHeatmapTabs();
initRequestsTabs();
const _initialPage = currentPage();
showPage(_initialPage);
PAGE_FETCHES[_initialPage]?.();
fetchAlerts();
fetchHealth();
startPolling();

// ── Status bar clock ──────────────────────────────────────────────────────────
function updateClock() {
  const e = el('last-updated');
  if (e) e.textContent = 'last refresh ' + new Date().toLocaleTimeString();
}
updateClock();
setInterval(updateClock, 1000);
