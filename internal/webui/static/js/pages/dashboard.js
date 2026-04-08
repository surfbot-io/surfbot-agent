// Dashboard page
const DashboardPage = {
  async render(app) {
    app.innerHTML = '<div class="loading">Loading dashboard...</div>';

    try {
      const data = await API.overview();
      app.innerHTML = this.template(data);
      updateSidebarBadge(data.findings_by_severity);
      updateLastRefresh();
      // SPEC-X3.1 — top-of-dashboard agent card. Mounts and starts its
      // own poll loop, independent of the dashboard refresh.
      const slot = document.getElementById('agent-card-slot');
      if (slot && typeof AgentCard !== 'undefined') AgentCard.mount(slot);
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
      ? `<div class="detail-grid">
           <span class="detail-label">Target</span>
           <span class="detail-value mono">${escapeHtml(d.last_scan.target)}</span>
           <span class="detail-label">Status</span>
           <span class="detail-value">${Components.statusBadge(d.last_scan.status)}</span>
           <span class="detail-label">Duration</span>
           <span class="detail-value">${Components.formatDuration(d.last_scan.duration_seconds)}</span>
           <span class="detail-label">Findings</span>
           <span class="detail-value">${d.last_scan.findings_count}</span>
           <span class="detail-label">Finished</span>
           <span class="detail-value">${Components.timeAgo(d.last_scan.finished_at)}</span>
         </div>`
      : '<p class="text-muted">No scans yet. Run <code>surfbot scan &lt;target&gt;</code> to get started.</p>';

    const changesHtml = d.changes_since_last
      ? `<div style="display:flex;gap:24px;padding:8px 0">
           <div><span style="font-size:20px;font-weight:700;color:var(--success)">+${d.changes_since_last.new_assets}</span><br><span class="text-muted" style="font-size:12px">New assets</span></div>
           <div><span style="font-size:20px;font-weight:700;color:var(--danger)">-${d.changes_since_last.disappeared_assets}</span><br><span class="text-muted" style="font-size:12px">Disappeared</span></div>
           <div><span style="font-size:20px;font-weight:700;color:var(--sev-medium)">${d.changes_since_last.new_findings || 0}</span><br><span class="text-muted" style="font-size:12px">New findings</span></div>
         </div>`
      : '';

    return `
      <div class="page-header">
        <h2>Dashboard</h2>
        <p>Security posture overview</p>
      </div>

      <div id="agent-card-slot"></div>

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
          <div class="card-label">Total Findings</div>
          <div class="card-value">${d.total_findings}</div>
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
