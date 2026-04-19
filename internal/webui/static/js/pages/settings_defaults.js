// SPEC-SCHED1.4a: Schedule defaults (singleton) page. Read-only.
//
// The API returns DefaultDefaults with a zero UpdatedAt when no row is
// saved — detected here by the "0001-01-01" year prefix so the UI can
// clearly label "server defaults" vs "saved by operator".
const SettingsDefaultsPage = {
  _mode: 'view',    // 'view' | 'edit'
  _current: null,   // last fetched defaults (for merge during save)

  async render(app) {
    app.innerHTML = '<div class="loading">Loading defaults...</div>';
    try {
      const d = await API.getScheduleDefaults();
      this._current = d;
      if (this._mode === 'edit') {
        app.innerHTML = this.editTemplate(d);
        this.bindEditActions(app);
      } else {
        app.innerHTML = this.template(d);
        this.bindViewActions(app);
      }
    } catch (err) {
      app.innerHTML = `
        <div class="page-header"><h2>Schedule defaults</h2></div>
        ${Components.errorBanner(err)}
      `;
    }
  },

  bindViewActions(app) {
    const editBtn = document.getElementById('defaults-edit-btn');
    if (editBtn) editBtn.addEventListener('click', () => {
      this._mode = 'edit';
      this.render(app);
    });
  },

  bindEditActions(app) {
    const cancelBtn = document.getElementById('defaults-cancel-btn');
    if (cancelBtn) cancelBtn.addEventListener('click', () => {
      this._mode = 'view';
      this.render(app);
    });
    const form = document.getElementById('defaults-form');
    if (form) form.addEventListener('submit', (e) => {
      e.preventDefault();
      this.save(app, form);
    });
  },

  async save(app, form) {
    const fd = new FormData(form);
    const body = {
      // PUT is a full-replace on the singleton: keep fields the UI
      // doesn't surface (tool_config, maintenance_window, template_id)
      // straight from the last fetched state so editing one field
      // doesn't silently reset the others.
      default_template_id: this._current && this._current.default_template_id ? this._current.default_template_id : undefined,
      default_rrule: (fd.get('default_rrule') || '').toString().trim(),
      default_timezone: (fd.get('default_timezone') || '').toString().trim() || 'UTC',
      default_tool_config: (this._current && this._current.default_tool_config) || {},
      default_maintenance_window: (this._current && this._current.default_maintenance_window) || undefined,
      max_concurrent_scans: parseInt((fd.get('max_concurrent_scans') || '4').toString(), 10) || 4,
      run_on_start: (fd.get('run_on_start') || 'false').toString() === 'true',
      jitter_seconds: parseInt((fd.get('jitter_seconds') || '0').toString(), 10) || 0,
    };
    if (!body.default_template_id) delete body.default_template_id;
    if (!body.default_maintenance_window) delete body.default_maintenance_window;

    const saveBtn = document.getElementById('defaults-save-btn');
    if (saveBtn) { saveBtn.disabled = true; saveBtn.textContent = '…'; }
    try {
      await API.updateDefaults(body);
      this._mode = 'view';
      this.render(app);
    } catch (err) {
      if (err && err.fieldErrors && err.fieldErrors.length) {
        Components.applyFieldErrors(form, err.fieldErrors);
      } else {
        const general = form.querySelector('[data-field-error="__general__"]');
        if (general) {
          const title = (err.problem && err.problem.title) || err.message || 'Save failed';
          const detail = err.problem && err.problem.detail ? ' — ' + err.problem.detail : '';
          general.textContent = title + detail;
          general.style.display = 'block';
        }
      }
      if (saveBtn) { saveBtn.disabled = false; saveBtn.textContent = 'Save'; }
    }
  },

  editTemplate(d) {
    return `
      <div class="page-header">
        <h2>Schedule defaults</h2>
        <div style="margin-left:auto"><span class="text-muted">Editing</span></div>
      </div>
      <form id="defaults-form" class="settings-form" novalidate>
        <div class="field-error" data-field-error="__general__" style="display:none;margin-bottom:12px;padding:8px 12px;background:var(--sev-critical-bg);border-radius:var(--radius)"></div>
        ${Components.formInput({ label: 'Default RRULE', name: 'default_rrule', value: d.default_rrule, required: true })}
        ${Components.formInput({ label: 'Default timezone', name: 'default_timezone', value: d.default_timezone || 'UTC', required: true })}
        ${Components.formInput({ label: 'Max concurrent scans', name: 'max_concurrent_scans', type: 'number', value: d.max_concurrent_scans, required: true, help: 'Minimum 1.' })}
        ${Components.formSelect({
          label: 'Run on start', name: 'run_on_start',
          options: [{ value: 'false', label: 'No' }, { value: 'true', label: 'Yes' }],
          value: d.run_on_start ? 'true' : 'false',
        })}
        ${Components.formInput({ label: 'Jitter (seconds)', name: 'jitter_seconds', type: 'number', value: d.jitter_seconds, help: 'Minimum 0.' })}
        <div style="display:flex;gap:8px;margin-top:16px">
          <button type="submit" class="btn btn-primary" id="defaults-save-btn">Save</button>
          <button type="button" class="btn btn-ghost" id="defaults-cancel-btn">Cancel</button>
        </div>
        <p class="text-muted" style="margin-top:12px;font-size:12px">
          Tool config and maintenance window editing is deferred to 1.4c.
          Saving preserves those fields from the current state.
        </p>
      </form>
    `;
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
      <div class="page-header">
        <h2>Schedule defaults</h2>
        <div style="margin-left:auto">
          <button type="button" class="btn btn-primary" id="defaults-edit-btn">Edit</button>
        </div>
      </div>
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
