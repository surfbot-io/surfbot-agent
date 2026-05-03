// Tools page — UI v2 (#44).
//
// Shows the agent's detection tools with version, templates count, status,
// last update and a contextual action button. The backend response today
// only carries {name, phase, available}; the new metadata fields
// (version, updated_at, metadata.templates_count, update_available) are
// optional and degrade gracefully — see _rowHtml for the null-safe paths.
const ToolsPage = {
  async render(app) {
    app.innerHTML = '<div class="loading">Loading tools...</div>';
    try {
      const data = await API.availableTools();
      app.innerHTML = this.template(data);
      this.bindEvents(app);
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load tools: ' + err.message);
    }
  },

  template(data) {
    const tools = (data && data.tools) || [];
    const total = (data && data.total != null) ? data.total : tools.length;
    const available = (data && data.available != null)
      ? data.available
      : tools.filter(t => t && t.available).length;
    const updateCount = tools.filter(t => t && t.available && t.update_available).length;
    const unavailableCount = Math.max(0, total - available);

    const subtitle = this._subtitleHtml(available, total, updateCount, unavailableCount);
    const headerActions = `
      <div class="tools-page-actions">
        <button type="button" class="btn btn-ghost" id="tools-refresh">Refresh status</button>
        <button type="button" class="btn btn-primary" id="tools-update-all">Update all</button>
      </div>
    `;

    if (tools.length === 0) {
      return `
        <div class="page-header">
          <div style="flex:1">
            <h2>Tools</h2>
            <p>${subtitle}</p>
          </div>
          ${headerActions}
        </div>
        ${Components.emptyState('No tools registered', 'No detection tools are registered in the pipeline.')}
      `;
    }

    const headers = [
      'Tool',
      'Phase',
      'Version',
      'Templates',
      'Status',
      'Last update',
      '<span class="tools-th-actions">Actions</span>',
    ];
    const rows = tools.map(t => this._rowHtml(t)).join('');

    return `
      <div class="page-header">
        <div style="flex:1">
          <h2>Tools</h2>
          <p>${subtitle}</p>
        </div>
        ${headerActions}
      </div>
      ${Components.table(headers, [rows])}
    `;
  },

  _subtitleHtml(available, total, updateCount, unavailableCount) {
    const parts = [`${available}/${total} available`];
    if (unavailableCount > 0) {
      parts.push(`run <code class="mono">surfbot tool install</code>`);
    }
    if (updateCount > 0) {
      parts.push(`${updateCount} update${updateCount === 1 ? '' : 's'} available`);
    }
    return parts.join(' · ');
  },

  _rowHtml(t) {
    const name = (t && t.name) || '';
    const phase = (t && t.phase) || '';
    const muted = (txt) => `<span class="text-muted">${txt}</span>`;
    const version = t && t.version
      ? `<span class="mono">${escapeHtml(t.version)}</span>`
      : muted('—');
    const updated = t && t.updated_at
      ? muted(escapeHtml(Components.timeAgo(t.updated_at)))
      : muted('—');

    let templates = '';
    const tc = t && t.metadata && t.metadata.templates_count;
    if (tc != null) {
      // Force en-US so the thousand separator stays consistent — es-ES
      // suppresses the separator for sub-10k numbers, which clashes with
      // the spec's "9,412" example.
      templates = `<span class="mono">${Number(tc).toLocaleString('en-US')}</span>`;
    } else if (name === 'nuclei') {
      templates = `<span class="text-muted" title="Backend metadata pending">—</span>`;
    }

    let statusClass, statusLabel, action;
    if (!t || !t.available) {
      statusClass = 'pill-tool-status-unavailable';
      statusLabel = 'unavailable';
      action = `<button type="button" class="pill pill-action" data-tool-action="install" data-tool="${escapeHtml(name)}" disabled title="Backend support coming in v0.6">Install</button>`;
    } else if (t.update_available) {
      statusClass = 'pill-tool-status-update';
      statusLabel = 'update available';
      action = `<button type="button" class="pill pill-action" data-tool-action="update" data-tool="${escapeHtml(name)}">Update</button>`;
    } else {
      statusClass = 'pill-tool-status-available';
      statusLabel = 'available';
      action = `<button type="button" class="pill pill-action" data-tool-action="test" data-tool="${escapeHtml(name)}">Test</button>`;
    }

    return `
      <tr>
        <td><code class="mono">${escapeHtml(name)}</code></td>
        <td><span class="pill pill-muted">${escapeHtml(phase)}</span></td>
        <td>${version}</td>
        <td>${templates}</td>
        <td><span class="pill ${statusClass}">${statusLabel}</span></td>
        <td>${updated}</td>
        <td class="tools-actions-cell">${action}</td>
      </tr>
    `;
  },

  bindEvents(app) {
    const refresh = document.getElementById('tools-refresh');
    if (refresh) refresh.addEventListener('click', () => this._refresh(app, refresh));

    const updateAll = document.getElementById('tools-update-all');
    if (updateAll) updateAll.addEventListener('click', () => this._updateAll(updateAll));

    document.querySelectorAll('[data-tool-action]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.preventDefault();
        if (btn.disabled) return;
        const action = btn.getAttribute('data-tool-action');
        const tool = btn.getAttribute('data-tool');
        this._toolAction(action, tool);
      });
    });
  },

  async _refresh(app, btn) {
    const orig = btn.textContent;
    btn.disabled = true;
    btn.textContent = 'Refreshing…';
    try {
      const data = await API.toolsRefresh();
      app.innerHTML = this.template(data);
      this.bindEvents(app);
    } catch (err) {
      btn.disabled = false;
      btn.textContent = orig;
      Components.toast.error('Failed to refresh tools: ' + err.message);
    }
  },

  async _updateAll(btn) {
    const orig = btn.textContent;
    btn.disabled = true;
    btn.textContent = 'Updating…';
    try {
      const r = await API.toolUpdate('all');
      const msg = (r && r.message) || 'Update dispatched';
      const kind = (r && r.status === 'mock') ? 'info' : 'success';
      Components.toast(msg, { kind });
    } catch (err) {
      Components.toast.error(err.message || 'Update failed');
    } finally {
      btn.disabled = false;
      btn.textContent = orig;
    }
  },

  async _toolAction(action, tool) {
    try {
      let r;
      if (action === 'update') r = await API.toolUpdate(tool);
      else if (action === 'test') r = await API.toolTest(tool);
      else return;
      const msg = (r && r.message) || 'Done';
      const kind = (r && r.status === 'mock') ? 'info' : 'success';
      Components.toast(msg, { kind });
    } catch (err) {
      Components.toast.error(err.message || 'Action failed');
    }
  },
};
