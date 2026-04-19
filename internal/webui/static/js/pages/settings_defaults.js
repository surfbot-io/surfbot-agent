// SPEC-SCHED1.4a: Schedule defaults (singleton) page. Read-only.
//
// The API returns DefaultDefaults with a zero UpdatedAt when no row is
// saved — detected here by the "0001-01-01" year prefix so the UI can
// clearly label "server defaults" vs "saved by operator".
const SettingsDefaultsPage = {
  async render(app) {
    app.innerHTML = '<div class="loading">Loading defaults...</div>';
    try {
      const d = await API.getScheduleDefaults();
      app.innerHTML = this.template(d);
    } catch (err) {
      app.innerHTML = `
        <div class="page-header"><h2>Schedule defaults</h2></div>
        ${Components.errorBanner(err)}
      `;
    }
  },

  template(d) {
    const unsaved = !d.updated_at || String(d.updated_at).startsWith('0001');
    const banner = unsaved
      ? `<div class="card" style="border-color:var(--border)">
           <span class="text-muted">These are server defaults. No custom defaults have been saved yet.</span>
         </div>`
      : `<div class="card" style="border-color:var(--border)">
           <span class="text-muted">Last updated ${Components.formatDate(d.updated_at)}.</span>
         </div>`;

    const tmplCell = d.default_template_id
      ? `<a href="#/templates/${encodeURIComponent(d.default_template_id)}" class="mono">${escapeHtml(d.default_template_id)}</a>`
      : '<span class="text-muted">—</span>';

    const mw = d.default_maintenance_window
      ? `<pre class="json-block">${escapeHtml(JSON.stringify(d.default_maintenance_window, null, 2))}</pre>`
      : '<span class="text-muted">none</span>';

    return `
      <div class="page-header"><h2>Schedule defaults</h2></div>
      ${banner}
      <div class="scan-detail-stack">
        <div class="detail-panel">
          <div class="detail-grid" style="margin-top:12px">
            <span class="detail-label">Default template</span><span class="detail-value">${tmplCell}</span>
            <span class="detail-label">Default RRULE</span><span class="detail-value mono" style="word-break:break-all">${escapeHtml(d.default_rrule || '-')}</span>
            <span class="detail-label">Default timezone</span><span class="detail-value mono">${escapeHtml(d.default_timezone || '-')}</span>
            <span class="detail-label">Max concurrent scans</span><span class="detail-value mono">${d.max_concurrent_scans}</span>
            <span class="detail-label">Run on start</span><span class="detail-value">${d.run_on_start ? '<span style="color:var(--success)">yes</span>' : '<span class="text-muted">no</span>'}</span>
            <span class="detail-label">Jitter</span><span class="detail-value mono">${d.jitter_seconds}s</span>
          </div>
        </div>

        <div class="card">
          <div class="card-label">Default maintenance window</div>
          <div style="margin-top:8px">${mw}</div>
        </div>

        <div class="card">
          <div class="card-label">Default tool config</div>
          <pre class="json-block">${escapeHtml(JSON.stringify(d.default_tool_config || {}, null, 2))}</pre>
        </div>
      </div>
    `;
  },
};
