// Dashboard page
const DashboardPage = {
  async render(app) {
    app.innerHTML = '<div class="loading">Loading dashboard...</div>';

    try {
      const data = await API.overview();
      app.innerHTML = this.template(data);
      updateSidebarBadge(data.findings_by_severity);
      updateLastRefresh();
      // PR2 #35: the SPEC-X3.1 agent card is gone from the dashboard.
      // It now lives compact in the sidebar footer (mounted by app.js)
      // so the user sees agent state from every route.

      // SPEC-SCHED1.4c R5: restore the "Run scan now" button removed
      // with the /api/daemon/trigger endpoint in 1.4a. Opens the 1.4b
      // ad-hoc modal with no prefill — same behavior as the sidebar
      // button, but in the more visible dashboard header location.
      const runBtn = document.getElementById('dashboard-run-scan-btn');
      if (runBtn) runBtn.addEventListener('click', () => {
        if (typeof AdHocPage !== 'undefined' && AdHocPage.open) AdHocPage.open();
      });
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load dashboard: ' + err.message);
    }
  },

  template(d) {
    const scoreClass = Components.scoreClass(d.security_score);

    const breakdownHtml = d.score_breakdown && d.score_breakdown.length > 0
      ? `<div class="score-breakdown">${d.score_breakdown.map(b =>
          `<span style="color:${Components.sevColor(b.severity)}">-${b.penalty} (${b.count} ${b.severity})</span>`
        ).join('')}</div>`
      : '<div class="score-breakdown">No findings</div>';

    const lastScanHtml = d.last_scan
      ? renderLastScanCard(d.last_scan)
      : '<p class="text-muted">No scans yet. Run <code>surfbot scan &lt;target&gt;</code> to get started.</p>';

    // changes_since_last reads ScanDelta from the persisted scan row (no
    // longer recomputed from asset_changes). new_findings is now correct
    // — the previous getChangeSummary always returned 0 here.
    const changesHtml = renderChangesCard(d.changes_since_last);

    return `
      <div class="page-header">
        <h2>Dashboard</h2>
        <p>Security posture overview</p>
        <div style="margin-left:auto">
          <button type="button" class="btn btn-accent" id="dashboard-run-scan-btn" data-action="run-scan-now">Run scan now</button>
        </div>
      </div>

      <div class="refresh-bar">
        <span id="last-refresh"></span>
        <button class="refresh-btn" onclick="Router.navigate()">Refresh</button>
      </div>

      <div class="card-grid">
        <div class="card">
          <div class="score-container">
            <div class="score-value ${scoreClass}">${d.security_score}</div>
            <div class="score-label">Security Score</div>
            ${breakdownHtml}
          </div>
        </div>
        <div class="card">
          <div class="card-label">Findings by Severity</div>
          ${Components.severityBars(d.findings_by_severity)}
        </div>
        <div class="card">
          <div class="card-label">Unique Findings</div>
          <div class="card-value">${d.unique_findings != null ? d.unique_findings : d.total_findings}</div>
          ${d.unique_findings != null && d.unique_findings !== d.total_findings
            ? `<div class="card-sub">${d.total_findings} total across all ports</div>`
            : ''}
        </div>
        <div class="card">
          <div class="card-label">Total Assets</div>
          <div class="card-value">${d.total_assets}</div>
          <div class="card-sub">${formatAssetTypes(d.assets_by_type)}</div>
        </div>
      </div>

      <div style="display:grid;grid-template-columns:1fr 1fr;gap:16px">
        <div class="card">
          <div class="card-label">Last Scan</div>
          ${lastScanHtml}
        </div>
        <div class="card">
          <div class="card-label">Changes Since Last Scan</div>
          ${changesHtml || '<p class="text-muted">No changes detected</p>'}
        </div>
      </div>

      <div class="card" style="margin-top:16px">
        <div class="card-label">Agent Info</div>
        <div class="detail-grid" style="margin-top:8px">
          <span class="detail-label">Version</span>
          <span class="detail-value mono">${d.agent.version}</span>
          <span class="detail-label">Database</span>
          <span class="detail-value mono">${escapeHtml(d.agent.db_path)}</span>
          <span class="detail-label">DB Size</span>
          <span class="detail-value">${Components.formatBytes(d.agent.db_size_bytes)}</span>
          <span class="detail-label">Targets</span>
          <span class="detail-value">${d.agent.targets_count}</span>
        </div>
      </div>
    `;
  },
};

function formatAssetTypes(types) {
  if (!types) return '';
  return Object.entries(types)
    .map(([k, v]) => `${v} ${k}`)
    .join(', ');
}

// renderLastScanCard renders the dashboard's "Last Scan" card. Reads
// last_scan.target_state for asset/finding counts so the user sees the
// shape of the target the scan observed, not just a flat findings number.
function renderLastScanCard(s) {
  const state = s.target_state || {};
  const assets = state.assets_by_type || {};
  const ports = state.ports_by_status || {};

  // Compose a single-line "what the target looks like" summary. Skip
  // zero-valued buckets so the line stays readable.
  const stateParts = [];
  if (assets.subdomain > 0) stateParts.push(`${assets.subdomain} subdomain${assets.subdomain === 1 ? '' : 's'}`);
  if ((assets.ipv4 || 0) + (assets.ipv6 || 0) > 0) stateParts.push(`${(assets.ipv4 || 0) + (assets.ipv6 || 0)} IPs`);
  if (ports.open > 0) {
    let portsLine = `${ports.open} open port${ports.open === 1 ? '' : 's'}`;
    if (ports.filtered > 0) portsLine += ` (${ports.filtered} filtered)`;
    stateParts.push(portsLine);
  }
  if (assets.url > 0) stateParts.push(`${assets.url} endpoint${assets.url === 1 ? '' : 's'}`);
  if (assets.technology > 0) stateParts.push(`${assets.technology} tech`);

  const stateLine = stateParts.length > 0
    ? `<div class="text-muted" style="font-size:13px;margin-top:4px">${stateParts.join(' · ')}</div>`
    : '';

  const work = s.work || {};
  const workLine = work.tools_run > 0
    ? `<div class="text-muted" style="font-size:12px;margin-top:4px">${work.tools_run} tools${work.tools_failed > 0 ? `, ${work.tools_failed} failed` : ''}</div>`
    : '';

  return `<div class="detail-grid">
    <span class="detail-label">Target</span>
    <span class="detail-value mono">${escapeHtml(s.target)}</span>
    <span class="detail-label">Status</span>
    <span class="detail-value">${Components.statusBadge(s.status)}</span>
    <span class="detail-label">Duration</span>
    <span class="detail-value">${Components.formatDuration(s.duration_seconds)}</span>
    <span class="detail-label">Findings open</span>
    <span class="detail-value">${s.findings_count}</span>
    <span class="detail-label">Finished</span>
    <span class="detail-value">${Components.timeAgo(s.finished_at)}</span>
  </div>${stateLine}${workLine}`;
}

// renderChangesCard renders the "Changes Since Last Scan" card. Reads from
// the persisted ScanDelta (same source as the CLI's PrintChangeSummary), so
// dashboard and CLI report the same numbers.
function renderChangesCard(c) {
  if (!c) return '';
  if (c.is_baseline) {
    return '<p class="text-muted" style="margin:8px 0 0">Baseline scan — first run. Subsequent scans will show what changed.</p>';
  }
  const total = (c.new_assets || 0) + (c.disappeared_assets || 0) + (c.modified_assets || 0)
              + (c.new_findings || 0) + (c.resolved_findings || 0);
  if (total === 0) {
    return '<p class="text-muted" style="margin:8px 0 0">No changes since last scan.</p>';
  }

  const cells = [];
  if ((c.new_assets || 0) > 0) {
    cells.push(`<div><span style="font-size:20px;font-weight:700;color:var(--success)">+${c.new_assets}</span><br><span class="text-muted" style="font-size:12px">New assets</span></div>`);
  }
  if ((c.disappeared_assets || 0) > 0) {
    cells.push(`<div><span style="font-size:20px;font-weight:700;color:var(--danger)">−${c.disappeared_assets}</span><br><span class="text-muted" style="font-size:12px">Disappeared</span></div>`);
  }
  if ((c.modified_assets || 0) > 0) {
    cells.push(`<div><span style="font-size:20px;font-weight:700;color:var(--sev-medium)">~${c.modified_assets}</span><br><span class="text-muted" style="font-size:12px">Modified</span></div>`);
  }
  if ((c.new_findings || 0) > 0) {
    cells.push(`<div><span style="font-size:20px;font-weight:700;color:var(--success)">+${c.new_findings}</span><br><span class="text-muted" style="font-size:12px">New findings</span></div>`);
  }
  if ((c.resolved_findings || 0) > 0) {
    cells.push(`<div><span style="font-size:20px;font-weight:700;color:var(--success)">✓${c.resolved_findings}</span><br><span class="text-muted" style="font-size:12px">Resolved</span></div>`);
  }
  return `<div style="display:flex;gap:24px;padding:8px 0;flex-wrap:wrap">${cells.join('')}</div>`;
}

function updateSidebarBadge(sevCounts) {
  if (!sevCounts) return;
  const critical = sevCounts.critical || 0;
  const high = sevCounts.high || 0;
  const total = critical + high;
  const el = document.getElementById('findings-badge');
  if (el) {
    if (total > 0) {
      el.textContent = total;
      el.style.display = '';
    } else {
      el.style.display = 'none';
    }
  }
}

function updateLastRefresh() {
  const el = document.getElementById('last-refresh');
  if (el) {
    el.textContent = 'Updated: ' + new Date().toLocaleTimeString();
  }
}
