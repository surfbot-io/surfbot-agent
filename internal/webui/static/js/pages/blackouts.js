// SPEC-SCHED1.4a: Blackouts page (list + detail). Read-only.
//
// "Next 5 activations" preview is omitted per OQ5 — client-side RRULE
// expansion would duplicate intervalsched logic (untouchable). The
// /schedules/upcoming endpoint already returns blackouts-in-horizon, so
// 1.4b can either filter those by blackout_id or add a dedicated
// /blackouts/{id}/upcoming endpoint.
const BlackoutsPage = {
  pageSize: 50,

  async render(app, params) {
    if (params && params.id) {
      return this.renderDetail(app, params.id);
    }
    app.innerHTML = '<div class="loading">Loading blackouts...</div>';

    const activeOnly = (params && params.active_only === '1');
    const limit = parseInt((params || {}).limit, 10) || this.pageSize;
    const offset = parseInt((params || {}).offset, 10) || 0;

    try {
      const apiParams = { limit, offset };
      if (activeOnly) apiParams.active_at = new Date().toISOString();
      const data = await API.listBlackouts(apiParams);
      app.innerHTML = this.template(data, { activeOnly, limit, offset });
      this.bindEvents();
    } catch (err) {
      app.innerHTML = `
        <div class="page-header"><h2>Blackout windows</h2></div>
        ${Components.errorBanner(err)}
      `;
    }
  },

  template(data, f) {
    const items = data.items || [];
    const total = data.total || 0;

    const filterBar = `
      <form id="blackouts-filters" class="inline-form" style="margin-bottom:16px">
        <label style="display:inline-flex;gap:6px;align-items:center;color:var(--text-secondary)">
          <input type="checkbox" id="filter-active-only"${f.activeOnly ? ' checked' : ''}>
          Active now
        </label>
        <button type="submit" class="btn btn-ghost">Apply</button>
      </form>
    `;

    const headerActions = '<div style="margin-left:auto"><button type="button" class="btn btn-primary" id="blackouts-new">New blackout</button></div>';

    if (items.length === 0) {
      // PR8 #41 — premium empty state for the unfiltered case (zero
      // blackouts in the DB). The activeOnly branch keeps the legacy
      // copy: it's a filter-result, not a first-run state.
      if (f.activeOnly) {
        return `
          <div class="page-header">
            <h2>Blackout windows</h2>
            ${headerActions}
          </div>
          ${filterBar}
          ${Components.emptyState('No blackouts', 'No blackouts are active right now.')}
        `;
      }
      return `
        <div class="page-header">
          <h2>Blackout windows</h2>
          ${headerActions}
        </div>
        ${filterBar}
        <div class="blackouts-empty">
          <div class="blackouts-empty-icon">${Components.icon('eye-off', 16)}</div>
          <h3 class="blackouts-empty-title">No hay blackouts definidos</h3>
          <p class="blackouts-empty-body">
            Crea ventanas para suprimir scans durante deploys, mantenimiento, o eventos.
            Los schedules se reanudan automáticamente al cerrar la ventana.
          </p>
          <div class="blackouts-empty-actions">
            <button type="button" class="btn btn-primary" id="blackouts-empty-new">+ New blackout</button>
            <button type="button" class="btn btn-ghost btn-sm" disabled
              title="Calendar import coming with calendar integration"
              aria-disabled="true">Importar desde calendario ↗</button>
          </div>
          <p class="blackouts-empty-cli">
            O desde CLI: <code class="mono">surfbot blackout create</code>
          </p>
        </div>
      `;
    }

    const nowIso = new Date().toISOString();
    const rows = items.map(b => {
      // Active-now is authoritative when the API already filtered with
      // active_at. Otherwise we render a '—' so we don't repeat the
      // rrule expansion client-side.
      const activeCell = f.activeOnly
        ? '<span style="color:var(--success)">●</span>'
        : '<span class="text-muted">—</span>';
      return `
        <tr class="clickable" onclick="location.hash='#/blackouts/${encodeURIComponent(b.id)}'">
          <td class="mono">${Components.truncateID(b.id)}</td>
          <td>${escapeHtml(b.name || '-')}</td>
          <td><span class="badge badge-muted">${escapeHtml(b.scope || '-')}</span></td>
          <td>${Components.rruleCompact(b.rrule)}</td>
          <td class="mono">${Components.formatDuration(b.duration_seconds)}</td>
          <td>${b.enabled ? '<span style="color:var(--success)">enabled</span>' : '<span class="text-muted">disabled</span>'}</td>
          <td>${activeCell}</td>
        </tr>
      `;
    }).join('');

    const pager = this.pagerHtml(total, f);

    return `
      <div class="page-header">
        <h2>Blackout windows</h2>
        <p>${total} window${total === 1 ? '' : 's'}${f.activeOnly ? ' active at ' + escapeHtml(nowIso) : ''}</p>
        ${headerActions}
      </div>
      ${filterBar}
      ${Components.table(
        ['ID', 'Name', 'Scope', 'RRULE', 'Duration', 'Enabled', 'Active now'],
        [rows]
      )}
      ${pager}
    `;
  },

  pagerHtml(total, f) {
    if (total <= f.limit) return '';
    const page = Math.floor(f.offset / f.limit) + 1;
    const pages = Math.max(1, Math.ceil(total / f.limit));
    const base = f.activeOnly ? 'active_only=1&' : '';
    const hashFor = (offset) => `#/blackouts?${base}limit=${f.limit}&offset=${offset}`;
    const prev = f.offset > 0
      ? `<a href="${hashFor(f.offset - f.limit)}" class="btn btn-sm btn-ghost">&larr; Prev</a>`
      : `<span class="btn btn-sm btn-ghost" style="opacity:0.4">&larr; Prev</span>`;
    const next = f.offset + f.limit < total
      ? `<a href="${hashFor(f.offset + f.limit)}" class="btn btn-sm btn-ghost">Next &rarr;</a>`
      : `<span class="btn btn-sm btn-ghost" style="opacity:0.4">Next &rarr;</span>`;
    return `<div class="pager" style="display:flex;gap:12px;align-items:center;margin-top:12px">
      ${prev}
      <span class="text-muted">Page ${page} of ${pages}</span>
      ${next}
    </div>`;
  },

  bindEvents() {
    const form = document.getElementById('blackouts-filters');
    if (form) {
      form.addEventListener('submit', (e) => {
        e.preventDefault();
        const activeOnly = document.getElementById('filter-active-only').checked;
        location.hash = activeOnly ? '#/blackouts?active_only=1' : '#/blackouts';
      });
    }
    const newBtn = document.getElementById('blackouts-new');
    if (newBtn) newBtn.addEventListener('click', () => this.openCreateForm());
    // PR8 #41 — empty-state CTA. Wired via id, only present in the
    // first-run branch of template().
    const emptyBtn = document.getElementById('blackouts-empty-new');
    if (emptyBtn) emptyBtn.addEventListener('click', () => this.openCreateForm());
  },

  // --- Blackout detail ---

  async renderDetail(app, id) {
    app.innerHTML = '<div class="loading">Loading blackout...</div>';
    const scroller = document.querySelector('main.content');
    if (scroller) scroller.scrollTop = 0;

    try {
      const b = await API.getBlackout(id);
      app.innerHTML = this.detailTemplate(b);
      this.bindDetailActions(app, b);
    } catch (err) {
      app.innerHTML = `
        ${Components.backLink('#/blackouts', 'Back to blackouts')}
        ${Components.errorBanner(err)}
      `;
    }
  },

  bindDetailActions(app, b) {
    const editBtn = document.getElementById('blackout-edit-btn');
    if (editBtn) editBtn.addEventListener('click', () => this.openEditForm(app, b));

    const delBtn = document.getElementById('blackout-delete-btn');
    if (delBtn) delBtn.addEventListener('click', () => this.confirmDelete(app, b));
  },

  detailTemplate(b) {
    const targetCell = b.target_id
      ? `<a href="#/targets/${encodeURIComponent(b.target_id)}" class="mono">${escapeHtml(b.target_id)}</a>`
      : '<span class="text-muted">— (global scope)</span>';

    return `
      ${Components.backLink('#/blackouts', 'Back to blackouts')}
      <div class="scan-detail-stack">
        <div class="detail-panel">
          <div class="detail-header">
            <h2>${escapeHtml(b.name || b.id)}</h2>
            <span class="badge badge-muted">${escapeHtml(b.scope || '-')}</span>
            ${b.enabled ? '<span style="color:var(--success);margin-left:8px">enabled</span>' : '<span class="text-muted" style="margin-left:8px">disabled</span>'}
            <div style="margin-left:auto;display:flex;gap:8px">
              <button type="button" class="btn btn-ghost" id="blackout-edit-btn">Edit</button>
              <button type="button" class="btn btn-danger" id="blackout-delete-btn">Delete</button>
            </div>
          </div>
          <div id="blackout-action-error" style="margin-top:8px"></div>
          <div class="detail-grid" style="margin-top:12px">
            <span class="detail-label">ID</span><span class="detail-value mono">${escapeHtml(b.id)}</span>
            <span class="detail-label">Scope</span><span class="detail-value mono">${escapeHtml(b.scope || '-')}</span>
            <span class="detail-label">Target</span><span class="detail-value">${targetCell}</span>
            <span class="detail-label">Timezone</span><span class="detail-value mono">${escapeHtml(b.timezone || '-')}</span>
            <span class="detail-label">RRULE</span><span class="detail-value mono" style="word-break:break-all">${escapeHtml(b.rrule || '-')}</span>
            <span class="detail-label">Duration</span><span class="detail-value mono">${Components.formatDuration(b.duration_seconds)} (${b.duration_seconds}s)</span>
            <span class="detail-label">Created</span><span class="detail-value">${Components.formatDate(b.created_at)}</span>
            <span class="detail-label">Updated</span><span class="detail-value">${Components.formatDate(b.updated_at)}</span>
          </div>
        </div>

        <div class="card" data-section="blackout-activations-placeholder">
          <div class="card-label">Upcoming activations</div>
          <div style="margin-top:8px;font-size:13px">
            <p class="text-muted" style="margin:0">
              Activation preview will be available in a future release.
              Expanding an RRULE client-side would duplicate the
              scheduler's intervalsched logic; a dedicated endpoint is
              tracked as post-launch work.
            </p>
            <p style="margin-top:8px">
              This blackout's RRULE:
              <br><code class="mono">${escapeHtml(b.rrule || '(none)')}</code>
              for <code class="mono">${escapeHtml(b.duration_seconds + 's')}</code>
              in <code class="mono">${escapeHtml(b.timezone || 'UTC')}</code>.
            </p>
            <p class="text-muted" style="margin-top:8px">
              Use <code class="mono">surfbot blackout show ${escapeHtml(b.id)}</code>
              or query SQLite directly to inspect occurrences until the
              endpoint lands.
            </p>
          </div>
        </div>
      </div>
    `;
  },

  // --- SPEC-SCHED1.4b: blackout create / edit / delete ---
  //
  // The 1.3a API (OQ3) carries a `scope` ("global"|"target") plus a
  // single optional `target_id` — NOT a `target_ids` array as the 1.4b
  // spec draft implied. To target multiple IDs the operator creates
  // multiple blackouts. Duration is rendered as hours + minutes +
  // seconds inputs and serialized to a single `duration_seconds`.

  openCreateForm(opts) {
    const o = opts || {};
    this.openForm({
      mode: 'create',
      prefill: o.prefill,
      onSaved: () => {
        if (typeof o.onSaved === 'function') o.onSaved();
        // Re-render the list when the user is on /blackouts; skip the
        // refresh otherwise (the modal was launched from the topbar or
        // another page, the operator stays where they were).
        if ((location.hash || '').startsWith('#/blackouts')) {
          BlackoutsPage.render(document.getElementById('app'), {});
        }
      },
    });
  },

  // PR8 #41 — public alias for the topbar +New menu and the asset
  // detail-pane "Add to blackout" CTA. Same shape as
  // SchedulesPage.openCreateForm but without listFilters since blackouts
  // doesn't paginate the list filter state into the create flow.
  openCreateModal(opts) {
    return this.openCreateForm(opts);
  },

  openEditForm(app, b) {
    this.openForm({
      mode: 'edit',
      blackout: b,
      onSaved: () => BlackoutsPage.renderDetail(app, b.id),
    });
  },

  openForm({ mode, blackout, prefill, onSaved }) {
    // PR8 #41: when a caller (asset detail pane, topbar +New) opens the
    // create modal with a `prefill` payload, merge it onto the empty
    // blackout draft so the scope/target_id fields render pre-filled.
    // Edit mode ignores prefill — the existing record always wins.
    const base = blackout || {};
    const b = (mode === 'create' && prefill)
      ? Object.assign({}, base, prefill)
      : base;
    const secs = Math.max(0, b.duration_seconds || 0);
    const hours = Math.floor(secs / 3600);
    const mins = Math.floor((secs % 3600) / 60);
    const remSecs = secs % 60;

    const body = `
      <form id="blackout-form" novalidate>
        <div class="field-error" data-field-error="__general__" style="display:none;margin-bottom:12px;padding:8px 12px;background:var(--sev-critical-bg);border-radius:var(--radius)"></div>
        ${Components.formInput({ label: 'Name', name: 'name', value: b.name, required: true })}
        ${Components.formSelect({
          label: 'Scope', name: 'scope',
          options: [{ value: 'global', label: 'Global (all targets)' }, { value: 'target', label: 'Single target' }],
          value: b.scope || 'global',
          required: true,
        })}
        ${Components.formInput({
          label: 'Target ID', name: 'target_id', value: b.target_id || '',
          help: 'Required when scope is "Single target"; ignored when scope is "Global".',
        })}
        ${Components.rruleField({ label: 'RRULE', name: 'rrule', value: b.rrule })}
        ${Components.formInput({ label: 'Timezone', name: 'timezone', value: b.timezone || 'UTC' })}
        <div class="form-field" data-field="duration_seconds">
          <label class="form-label">Duration <span class="form-required">*</span></label>
          <div style="display:flex;gap:8px">
            <input class="form-input" name="dur_h" type="number" min="0" value="${hours}" placeholder="h" style="flex:1">
            <input class="form-input" name="dur_m" type="number" min="0" max="59" value="${mins}" placeholder="m" style="flex:1">
            <input class="form-input" name="dur_s" type="number" min="0" max="59" value="${remSecs}" placeholder="s" style="flex:1">
          </div>
          <div class="form-help">Hours / minutes / seconds — serialized as a single <code>duration_seconds</code> value. Max 7 days.</div>
          <div class="field-error" data-field-error="duration_seconds" style="display:none"></div>
        </div>
        ${mode === 'edit' ? Components.formSelect({
          label: 'Enabled', name: 'enabled',
          options: [{ value: 'true', label: 'Enabled' }, { value: 'false', label: 'Disabled' }],
          value: b.enabled === false ? 'false' : 'true',
        }) : ''}
      </form>
    `;

    const m = Components.modal({
      title: mode === 'create' ? 'New blackout' : 'Edit blackout',
      body,
      size: 'md',
      primaryAction: {
        label: mode === 'create' ? 'Create' : 'Save',
        onClick: async ({ close }) => {
          const form = document.getElementById('blackout-form');
          const data = readBlackoutForm(form, mode);
          if (!data) return;
          try {
            if (mode === 'create') await API.createBlackout(data);
            else await API.updateBlackout(b.id, data);
            if (onSaved) onSaved();
            close('saved');
          } catch (err) {
            showBlackoutFormError(form, err);
          }
        },
      },
      secondaryAction: { label: 'Cancel' },
    });
    Components.rruleAttachBlurCheck(m.root.querySelector('#blackout-form'), 'rrule');
  },

  async confirmDelete(app, b) {
    const ok = await Components.confirmDialog({
      title: 'Delete blackout?',
      message: `Delete blackout "${b.name || b.id}"? This cannot be undone.`,
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!ok) return;
    try {
      await API.deleteBlackout(b.id);
      location.hash = '#/blackouts';
      BlackoutsPage.render(app, {});
    } catch (err) {
      const box = document.getElementById('blackout-action-error');
      if (box) box.innerHTML = Components.errorBanner(err);
    }
  },
};

function readBlackoutForm(form, mode) {
  if (!form) return null;
  const fd = new FormData(form);
  const scope = (fd.get('scope') || 'global').toString();
  const targetId = (fd.get('target_id') || '').toString().trim();
  const out = {
    scope,
    name: (fd.get('name') || '').toString().trim(),
    rrule: (fd.get('rrule') || '').toString().trim(),
    timezone: (fd.get('timezone') || '').toString().trim() || 'UTC',
  };
  if (scope === 'target') {
    if (!targetId) {
      Components.applyFieldErrors(form, [{ field: 'target_id', message: 'required when scope is "target"' }]);
      return null;
    }
    out.target_id = targetId;
  } else if (mode === 'edit' && !targetId) {
    // Switching back from target → global: ask the API to clear the fk.
    out.clear_target = true;
  }
  const h = parseInt((fd.get('dur_h') || '0').toString(), 10) || 0;
  const m = parseInt((fd.get('dur_m') || '0').toString(), 10) || 0;
  const s = parseInt((fd.get('dur_s') || '0').toString(), 10) || 0;
  const seconds = h * 3600 + m * 60 + s;
  if (seconds <= 0) {
    Components.applyFieldErrors(form, [{ field: 'duration_seconds', message: 'must be greater than zero' }]);
    return null;
  }
  out.duration_seconds = seconds;
  if (mode === 'edit') {
    const en = (fd.get('enabled') || 'true').toString();
    out.enabled = en === 'true';
  }
  return out;
}

function showBlackoutFormError(form, err) {
  if (!form) return;
  if (err && err.fieldErrors && err.fieldErrors.length) {
    Components.applyFieldErrors(form, err.fieldErrors);
    return;
  }
  const general = form.querySelector('[data-field-error="__general__"]');
  if (general) {
    const title = (err && err.problem && err.problem.title) || (err && err.message) || 'Request failed';
    const detail = err && err.problem && err.problem.detail ? ' — ' + err.problem.detail : '';
    general.textContent = title + detail;
    general.style.display = 'block';
  }
}
