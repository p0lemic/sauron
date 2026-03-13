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

function rangeParams() {
  if (_range.live) return '';
  const from = _range.from ? new Date(_range.from).toISOString() : null;
  const to   = _range.to   ? new Date(_range.to).toISOString()   : new Date().toISOString();
  return from ? `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}` : '';
}

function initTimeRange() {
  const btnApply = el('tr-apply');
  const btnLive  = el('tr-live');
  const inputFrom = el('tr-from');
  const inputTo   = el('tr-to');
  const bar       = el('timerange-bar');

  btnLive.addEventListener('click', () => {
    _range.live = true;
    _range.from = null;
    _range.to   = null;
    inputFrom.value = '';
    inputTo.value   = '';
    btnLive.classList.add('active');
    bar.classList.remove('historical');
    startPolling();
    refresh();
  });

  btnApply.addEventListener('click', () => {
    const f = inputFrom.value;
    const t = inputTo.value;
    if (!f) { inputFrom.focus(); return; }
    _range.live = false;
    _range.from = f;
    _range.to   = t || null; // null = now
    btnLive.classList.remove('active');
    bar.classList.add('historical');
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
  body.querySelectorAll('th.sortable').forEach(th => {
    th.addEventListener('click', () => {
      const col = th.dataset.col;
      if (_sortCol === col) { _sortDir *= -1; }
      else { _sortCol = col; _sortDir = -1; }
      renderTable();
    });
  });
  body.querySelectorAll('tr.row-clickable').forEach(tr => {
    tr.addEventListener('click', () => {
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
          </div>
          <button class="chart-close" id="chart-close">×</button>
        </div>
      </div>
      <div id="tab-chart" class="panel-tab-content">
        <div class="chart-body">${buildSVG(timeBuckets)}</div>
      </div>
      <div id="tab-histogram" class="panel-tab-content" style="display:none;padding:16px 20px">
        ${renderHistogram(histStat)}
      </div>`;

    panel.style.display = '';
    panel.scrollIntoView({ behavior: 'smooth', block: 'nearest' });

    el('chart-close').addEventListener('click', () => {
      panel.style.display = 'none';
      _chartEndpoint = null;
    });
    panel.querySelectorAll('.panel-tab').forEach(btn => {
      btn.addEventListener('click', () => {
        panel.querySelectorAll('.panel-tab').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        el('tab-chart').style.display     = btn.dataset.tab === 'chart'     ? '' : 'none';
        el('tab-histogram').style.display = btn.dataset.tab === 'histogram' ? '' : 'none';
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
      const ratio     = a.baseline_p99 > 0 ? (a.current_p99 / a.baseline_p99).toFixed(1) : '—';
      const triggered = new Date(a.triggered_at).toLocaleTimeString();
      return `<tr>
        <td>${escHtml(a.method)}</td>
        <td><span class="cell-path">${escHtml(a.path)}</span></td>
        <td class="danger">${fmt(a.current_p99,1)}<span class="cell-unit">ms</span></td>
        <td>${fmt(a.baseline_p99,1)}<span class="cell-unit">ms</span></td>
        <td class="danger">${ratio}×</td>
        <td class="cell-time">${triggered}</td>
      </tr>`;
    }).join('');

    section.querySelector('.section-body').innerHTML = `
      <div class="table-wrap">
        <table class="data-table alerts-table">
          <thead><tr>
            <th>Method</th><th>Path</th>
            <th>Current P99</th><th>Baseline P99</th>
            <th>Ratio</th><th>Triggered</th>
          </tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>`;
    section.style.display = '';
  } catch (_) { /* keep last known state */ }
}

// ── Refresh loop ──────────────────────────────────────────────────────────────
async function refresh() {
  await Promise.all([fetchSummary(), fetchEndpoints(), fetchAlerts()]);
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
