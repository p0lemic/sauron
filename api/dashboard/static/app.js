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

// ── Slowest Requests (US-29) ──────────────────────────────────────────────────
function buildRequestRows(records) {
  if (records.length === 0) {
    return `<tr><td colspan="5" class="no-data">No data</td></tr>`;
  }
  return records.map(r => {
    const t   = new Date(r.timestamp);
    const hms = t.toLocaleTimeString('en-US', { hour12: false });
    const ms  = String(t.getMilliseconds()).padStart(3, '0');
    const cls = METHOD_COLORS[r.method] || 'badge-other';
    const sc  = statusClass(r.status_code);
    const durCls = r.duration_ms >= 1000 ? 'danger' : '';
    return `<tr>
      <td class="cell-time">${hms}.${ms}</td>
      <td><span class="method-badge ${cls}">${escHtml(r.method)}</span></td>
      <td><span class="cell-path">${escHtml(r.path)}</span></td>
      <td class="cell-status ${sc}">${r.status_code}</td>
      <td class="cell-dur ${durCls}">${Number(r.duration_ms).toFixed(1)}<span class="cell-unit">ms</span></td>
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
    const body = el('slowest-requests').querySelector('.section-body');
    if (!body) return;
    body.innerHTML = `
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr>
            <th>Time</th><th>Method</th><th>Path</th><th>Status</th><th>Duration</th>
          </tr></thead>
          <tbody>${buildRequestRows(records)}</tbody>
        </table>
      </div>`;
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
  const body = el('request-log').querySelector('.section-body');
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
          <th>Time</th><th>Method</th><th>Path</th><th>Status</th><th>Duration</th>
        </tr></thead>
        <tbody>${trRows}</tbody>
      </table>
    </div>
    <div class="pagination">
      <button class="page-btn" id="rl-prev" ${_rlPage === 0 ? 'disabled' : ''}>← Prev</button>
      <span class="page-info">${paginationInfo}</span>
      <button class="page-btn" id="rl-next" ${_rlPage >= totalPages - 1 ? 'disabled' : ''}>Next →</button>
    </div>`;

  el('rl-filter').addEventListener('input', e => { _rlPath = e.target.value; _rlPage = 0; renderRequestLog(); });
  el('rl-method').addEventListener('change', e => { _rlMethod = e.target.value; _rlPage = 0; renderRequestLog(); });
  el('rl-status').addEventListener('change', e => { _rlStatus = e.target.value; _rlPage = 0; renderRequestLog(); });
  el('rl-prev').addEventListener('click', () => { _rlPage--; renderRequestLog(); });
  el('rl-next').addEventListener('click', () => { _rlPage++; renderRequestLog(); });
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
async function refresh() {
  await Promise.all([fetchSummary(), fetchStatusBreakdown(), fetchEndpoints(), fetchAlerts(), fetchSlowestRequests(), fetchRequests(), fetchHealth()]);
}

// ── Boot ──────────────────────────────────────────────────────────────────────
initTimeRange();
refresh();
startPolling();

// ── Status bar clock ──────────────────────────────────────────────────────────
function updateClock() {
  const e = el('last-updated');
  if (e) e.textContent = 'last refresh ' + new Date().toLocaleTimeString();
}
updateClock();
setInterval(updateClock, 1000);
