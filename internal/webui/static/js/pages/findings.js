// Findings page
const FindingsPage = {
  async render(app, params) {
    if (params && params.id) {
      return this.renderDetail(app, params.id);
    }

    app.innerHTML = '<div class="loading">Loading findings...</div>';

    try {
      const data = await API.findings({
        severity: params?.severity,
        tool: params?.tool,
        status: params?.status,
        target_id: params?.target_id,
        page: params?.page || 1,
        limit: 50,
      });
      app.innerHTML = this.template(data, params);
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load findings: ' + err.message);
    }
  },

  template(data, params) {
    if (data.findings.length === 0 && !params?.severity && !params?.status) {
      return `
        <div class="page-header"><h2>Findings</h2></div>
        ${Components.emptyState('No findings', 'No vulnerabilities detected. Run a scan to discover findings.')}
      `;
    }

    const rows = data.findings.map(f => `
      <tr>
        <td>${Components.severityBadge(f.severity)}</td>
        <td class="text-truncate clickable" onclick="location.hash='#/findings/${f.id}'">${escapeHtml(f.title)}</td>
        <td class="mono">${escapeHtml(f.source_tool)}</td>
        <td>
          <select class="status-select status-${f.status}" onchange="FindingsPage.changeStatus('${f.id}', this.value, this)" data-original="${f.status}">
            <option value="open" ${f.status==='open'?'selected':''}>open</option>
            <option value="acknowledged" ${f.status==='acknowledged'?'selected':''}>acknowledged</option>
            <option value="resolved" ${f.status==='resolved'?'selected':''}>resolved</option>
            <option value="false_positive" ${f.status==='false_positive'?'selected':''}>false positive</option>
            <option value="ignored" ${f.status==='ignored'?'selected':''}>ignored</option>
          </select>
        </td>
        <td class="mono text-muted">${Components.timeAgo(f.last_seen)}</td>
      </tr>
    `).join('');

    const totalPages = Math.ceil(data.total / data.limit);
    const page = data.page;

    return `
      <div class="page-header">
        <h2>Findings</h2>
        <p>${data.total} finding${data.total !== 1 ? 's' : ''}${params?.severity ? ' (' + params.severity + ')' : ''}</p>
      </div>
      ${this.filtersHtml(params)}
      ${Components.table(['Severity', 'Title', 'Tool', 'Status', 'Last Seen'], [rows])}
      ${totalPages > 1 ? this.paginationHtml(page, totalPages, params) : ''}
    `;
  },

  filtersHtml(params) {
    const sev = params?.severity || '';
    const status = params?.status || '';
    return `<div class="filters">
      <select class="filter-select" id="filter-severity" onchange="FindingsPage.applyFilters()">
        <option value="">All severities</option>
        <option value="critical" ${sev==='critical'?'selected':''}>Critical</option>
        <option value="high" ${sev==='high'?'selected':''}>High</option>
        <option value="medium" ${sev==='medium'?'selected':''}>Medium</option>
        <option value="low" ${sev==='low'?'selected':''}>Low</option>
        <option value="info" ${sev==='info'?'selected':''}>Info</option>
      </select>
      <select class="filter-select" id="filter-status" onchange="FindingsPage.applyFilters()">
        <option value="">All statuses</option>
        <option value="open" ${status==='open'?'selected':''}>Open</option>
        <option value="acknowledged" ${status==='acknowledged'?'selected':''}>Acknowledged</option>
        <option value="resolved" ${status==='resolved'?'selected':''}>Resolved</option>
        <option value="false_positive" ${status==='false_positive'?'selected':''}>False Positive</option>
        <option value="ignored" ${status==='ignored'?'selected':''}>Ignored</option>
      </select>
    </div>`;
  },

  paginationHtml(page, totalPages, params) {
    let html = '<div style="display:flex;gap:8px;justify-content:center;margin-top:16px">';
    if (page > 1) {
      html += `<button class="refresh-btn" onclick="FindingsPage.goToPage(${page-1})">Prev</button>`;
    }
    html += `<span class="text-muted" style="padding:4px 8px">Page ${page} of ${totalPages}</span>`;
    if (page < totalPages) {
      html += `<button class="refresh-btn" onclick="FindingsPage.goToPage(${page+1})">Next</button>`;
    }
    html += '</div>';
    return html;
  },

  applyFilters() {
    const severity = document.getElementById('filter-severity')?.value || '';
    const status = document.getElementById('filter-status')?.value || '';
    const parts = ['#/findings'];
    const q = [];
    if (severity) q.push('severity=' + severity);
    if (status) q.push('status=' + status);
    location.hash = parts[0] + (q.length ? '?' + q.join('&') : '');
  },

  goToPage(page) {
    const hash = location.hash;
    const base = hash.split('?')[0];
    const params = parseQueryParams();
    params.page = page;
    const q = Object.entries(params).filter(([,v]) => v).map(([k,v]) => k+'='+v).join('&');
    location.hash = base + (q ? '?' + q : '');
  },

  async changeStatus(id, newStatus, selectEl) {
    const original = selectEl.dataset.original;
    try {
      await API.updateFindingStatus(id, newStatus);
      selectEl.dataset.original = newStatus;
      selectEl.className = 'status-select status-' + newStatus;
    } catch (err) {
      selectEl.value = original;
      alert('Failed to update status: ' + err.message);
    }
  },

  async changeStatusFromDetail(id, newStatus) {
    try {
      await API.updateFindingStatus(id, newStatus);
      // Re-render detail to reflect the change
      this.renderDetail(document.getElementById('app'), id);
    } catch (err) {
      alert('Failed to update status: ' + err.message);
    }
  },

  async renderDetail(app, id) {
    app.innerHTML = '<div class="loading">Loading finding...</div>';

    try {
      const data = await API.finding(id);
      const f = data.finding;
      const asset = data.asset;

      const statusActions = this.statusActionsHtml(f);

      app.innerHTML = `
        ${Components.backLink('#/findings', 'Back to findings')}
        <div class="detail-panel">
          <div class="detail-header">
            ${Components.severityBadge(f.severity)}
            <h3>${escapeHtml(f.title)}</h3>
          </div>

          ${statusActions}

          <div class="detail-grid">
            <span class="detail-label">ID</span>
            <span class="detail-value mono">${f.id}</span>
            <span class="detail-label">Template</span>
            <span class="detail-value mono">${escapeHtml(f.template_id)}</span>
            <span class="detail-label">Tool</span>
            <span class="detail-value">${escapeHtml(f.source_tool)}</span>
            <span class="detail-label">Status</span>
            <span class="detail-value">${Components.statusBadge(f.status)}</span>
            <span class="detail-label">Confidence</span>
            <span class="detail-value">${f.confidence}%</span>
            ${f.cvss ? `<span class="detail-label">CVSS</span><span class="detail-value">${f.cvss}</span>` : ''}
            ${f.cve ? `<span class="detail-label">CVE</span><span class="detail-value mono">${escapeHtml(f.cve)}</span>` : ''}
            <span class="detail-label">First seen</span>
            <span class="detail-value">${Components.formatDate(f.first_seen)}</span>
            <span class="detail-label">Last seen</span>
            <span class="detail-value">${Components.formatDate(f.last_seen)}</span>
            ${f.resolved_at ? `<span class="detail-label">Resolved</span><span class="detail-value">${Components.formatDate(f.resolved_at)}</span>` : ''}
          </div>
        </div>

        ${f.description ? `<div class="card" style="margin-bottom:16px">
          <div class="card-label">Description</div>
          <p style="margin-top:8px">${escapeHtml(f.description)}</p>
        </div>` : ''}

        ${asset ? `<div class="card" style="margin-bottom:16px">
          <div class="card-label">Related Asset</div>
          <div class="detail-grid" style="margin-top:8px">
            <span class="detail-label">Value</span>
            <span class="detail-value mono">${escapeHtml(asset.value)}</span>
            <span class="detail-label">Type</span>
            <span class="detail-value">${asset.type}</span>
          </div>
        </div>` : ''}

        ${f.remediation ? `<div class="card" style="margin-bottom:16px">
          <div class="card-label">Remediation</div>
          <p style="margin-top:8px">${escapeHtml(f.remediation)}</p>
        </div>` : ''}

        ${f.evidence ? `<div class="card" style="margin-bottom:16px">
          <div class="card-label">Evidence</div>
          ${Components.jsonViewer(f.evidence)}
        </div>` : ''}

        ${f.references && f.references.length > 0 ? `<div class="card" style="margin-bottom:16px">
          <div class="card-label">References</div>
          <ul style="margin-top:8px;padding-left:20px">
            ${f.references.map(r => safeLink(r)).join('')}
          </ul>
        </div>` : ''}
      `;
    } catch (err) {
      app.innerHTML = Components.emptyState('Not Found', 'Finding not found.');
    }
  },

  statusActionsHtml(f) {
    const actions = [];
    if (f.status === 'open') {
      actions.push({ label: 'Acknowledge', status: 'acknowledged', cls: 'btn-ghost' });
      actions.push({ label: 'Resolve', status: 'resolved', cls: 'btn-accent' });
      actions.push({ label: 'False Positive', status: 'false_positive', cls: 'btn-ghost' });
      actions.push({ label: 'Ignore', status: 'ignored', cls: 'btn-ghost' });
    } else if (f.status === 'acknowledged') {
      actions.push({ label: 'Resolve', status: 'resolved', cls: 'btn-accent' });
      actions.push({ label: 'Reopen', status: 'open', cls: 'btn-ghost' });
      actions.push({ label: 'False Positive', status: 'false_positive', cls: 'btn-ghost' });
    } else if (f.status === 'resolved') {
      actions.push({ label: 'Reopen', status: 'open', cls: 'btn-ghost' });
    } else if (f.status === 'false_positive') {
      actions.push({ label: 'Reopen', status: 'open', cls: 'btn-ghost' });
    } else if (f.status === 'ignored') {
      actions.push({ label: 'Reopen', status: 'open', cls: 'btn-ghost' });
    }

    if (actions.length === 0) return '';

    const btns = actions.map(a =>
      `<button class="btn btn-sm ${a.cls}" onclick="FindingsPage.changeStatusFromDetail('${f.id}', '${a.status}')">${a.label}</button>`
    ).join('');

    return `<div class="action-bar">${btns}</div>`;
  },
};
