// Targets page
const TargetsPage = {
  async render(app) {
    app.innerHTML = '<div class="loading">Loading targets...</div>';

    try {
      const data = await API.targets();
      app.innerHTML = this.template(data);
      this.bindEvents(app);
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load targets: ' + err.message);
    }
  },

  template(data) {
    const targets = data.targets || [];

    const addForm = `
      <div class="add-form">
        <form id="add-target-form" class="inline-form">
          <input type="text" id="target-value" class="form-input" placeholder="domain, IP, or CIDR (e.g. example.com)" required>
          <select id="target-scope" class="filter-select">
            <option value="external">external</option>
            <option value="internal">internal</option>
            <option value="both">both</option>
          </select>
          <button type="submit" class="btn btn-primary">Add Target</button>
        </form>
        <div id="add-target-error" class="form-error" style="display:none"></div>
      </div>
    `;

    if (targets.length === 0) {
      return `
        <div class="page-header"><h2>Targets</h2></div>
        ${addForm}
        ${Components.emptyState('No targets', 'Add a target above to start scanning.')}
      `;
    }

    const rows = targets.map(t => `
      <tr>
        <td class="mono">${escapeHtml(t.value)}</td>
        <td><span class="badge badge-info">${t.type}</span></td>
        <td>${t.scope}</td>
        <td>${t.finding_count}</td>
        <td>${t.asset_count}</td>
        <td>${t.scan_count}</td>
        <td class="text-muted">${t.last_scan_at ? Components.timeAgo(t.last_scan_at) : 'never'}</td>
        <td>${t.enabled ? '<span style="color:var(--success)">active</span>' : '<span class="text-muted">disabled</span>'}</td>
        <td class="actions-cell">
          <button class="btn btn-sm btn-accent" onclick="TargetsPage.scanTarget('${t.id}', '${escapeHtml(t.value)}')" title="Scan">
            <svg width="14" height="14" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M4 2a1 1 0 011 1v2.101a7.002 7.002 0 0111.601 2.566 1 1 0 11-1.885.666A5.002 5.002 0 005.999 7H9a1 1 0 010 2H4a1 1 0 01-1-1V3a1 1 0 011-1zm.008 9.057a1 1 0 011.276.61A5.002 5.002 0 0014.001 13H11a1 1 0 110-2h5a1 1 0 011 1v5a1 1 0 11-2 0v-2.101a7.002 7.002 0 01-11.601-2.566 1 1 0 01.61-1.276z" clip-rule="evenodd"/></svg>
          </button>
          <button class="btn btn-sm btn-ghost" onclick="TargetsPage.viewFindings('${t.id}')" title="Findings">
            <svg width="14" height="14" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M8.257 3.099c.765-1.36 2.722-1.36 3.486 0l5.58 9.92c.75 1.334-.213 2.98-1.742 2.98H4.42c-1.53 0-2.493-1.646-1.743-2.98l5.58-9.92zM11 13a1 1 0 11-2 0 1 1 0 012 0zm-1-8a1 1 0 00-1 1v3a1 1 0 002 0V6a1 1 0 00-1-1z" clip-rule="evenodd"/></svg>
          </button>
          <button class="btn btn-sm btn-danger" onclick="TargetsPage.deleteTarget('${t.id}', '${escapeHtml(t.value)}')" title="Remove">
            <svg width="14" height="14" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M9 2a1 1 0 00-.894.553L7.382 4H4a1 1 0 000 2v10a2 2 0 002 2h8a2 2 0 002-2V6a1 1 0 100-2h-3.382l-.724-1.447A1 1 0 0011 2H9zM7 8a1 1 0 012 0v6a1 1 0 11-2 0V8zm5-1a1 1 0 00-1 1v6a1 1 0 102 0V8a1 1 0 00-1-1z" clip-rule="evenodd"/></svg>
          </button>
        </td>
      </tr>
    `).join('');

    return `
      <div class="page-header">
        <h2>Targets</h2>
        <p>${targets.length} configured target${targets.length !== 1 ? 's' : ''}</p>
      </div>
      ${addForm}
      ${Components.table(
        ['Target', 'Type', 'Scope', 'Findings', 'Assets', 'Scans', 'Last Scan', 'Status', ''],
        [rows]
      )}
    `;
  },

  bindEvents(app) {
    const form = document.getElementById('add-target-form');
    if (!form) return;

    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const value = document.getElementById('target-value').value.trim();
      const scope = document.getElementById('target-scope').value;
      const errorEl = document.getElementById('add-target-error');
      const btn = form.querySelector('button[type="submit"]');

      if (!value) return;

      btn.disabled = true;
      btn.textContent = 'Adding...';
      errorEl.style.display = 'none';

      try {
        await API.createTarget(value, '', scope);
        TargetsPage.render(app);
      } catch (err) {
        errorEl.textContent = err.message;
        errorEl.style.display = 'block';
        btn.disabled = false;
        btn.textContent = 'Add Target';
      }
    });
  },

  viewFindings(targetId) {
    location.hash = '#/findings?target_id=' + targetId;
  },

  async scanTarget(targetId, targetValue) {
    if (!confirm('Start a full scan on ' + targetValue + '?')) return;

    try {
      await API.startScan(targetId, 'full');
      location.hash = '#/scans';
    } catch (err) {
      alert('Failed to start scan: ' + err.message);
    }
  },

  async deleteTarget(id, value) {
    if (!confirm('Remove target "' + value + '" and all associated data? This cannot be undone.')) return;

    try {
      await API.deleteTarget(id);
      TargetsPage.render(document.getElementById('app'));
    } catch (err) {
      alert('Failed to delete target: ' + err.message);
    }
  },
};
