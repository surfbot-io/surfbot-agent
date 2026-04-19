// SPEC-SCHED1.4a: Schedules page (list + detail). Read-only; write flows
// land in 1.4b. Mirrors the targets.js / scans.js layout so nav, loading,
// empty, and error states match the rest of the SPA.
const SchedulesPage = {
  pageSize: 50,

  async render(app, params) {
    if (params && params.id) {
      return this.renderDetail(app, params.id);
    }
    app.innerHTML = '<div class="loading">Loading schedules...</div>';

    const filters = this.parseFilters(params || {});
    try {
      const data = await API.listSchedules(this.apiParams(filters));
      app.innerHTML = this.template(data, filters);
      this.bindEvents(app, filters);
    } catch (err) {
      app.innerHTML = `
        <div class="page-header"><h2>Schedules</h2></div>
        ${Components.errorBanner(err)}
      `;
    }
  },

  parseFilters(params) {
    return {
      status: params.status || '',
      target_id: params.target_id || '',
      template_id: params.template_id || '',
      limit: parseInt(params.limit, 10) || this.pageSize,
      offset: parseInt(params.offset, 10) || 0,
    };
  },

  apiParams(f) {
    const out = { limit: f.limit, offset: f.offset };
    if (f.status) out.status = f.status;
    if (f.target_id) out.target_id = f.target_id;
    if (f.template_id) out.template_id = f.template_id;
    return out;
  },

  template(data, f) {
    const items = data.items || [];
    const total = data.total || 0;

    const filterBar = `
      <form id="schedules-filters" class="inline-form" style="margin-bottom:16px">
        <select id="filter-status" class="filter-select">
          <option value="">All statuses</option>
          <option value="active"${f.status === 'active' ? ' selected' : ''}>Active</option>
          <option value="paused"${f.status === 'paused' ? ' selected' : ''}>Paused</option>
        </select>
        <input type="text" id="filter-target" class="form-input" placeholder="Target ID"
               value="${escapeHtml(f.target_id)}">
        <input type="text" id="filter-template" class="form-input" placeholder="Template ID"
               value="${escapeHtml(f.template_id)}">
        <button type="submit" class="btn btn-ghost">Filter</button>
      </form>
    `;

    if (items.length === 0) {
      const hint = (f.status || f.target_id || f.template_id)
        ? 'No schedules match the current filters.'
        : 'No schedules yet. Click <strong>New schedule</strong> above or use <code class="mono">surfbot schedule create</code>.';
      return `
        <div class="page-header">
          <h2>Schedules</h2>
          <div style="margin-left:auto">
            <button type="button" class="btn btn-primary" id="schedules-new">New schedule</button>
          </div>
        </div>
        ${filterBar}
        ${Components.emptyState('No schedules', hint)}
      `;
    }

    const rows = items.map(s => `
      <tr data-schedule-id="${encodeURIComponent(s.id)}">
        <td class="bulk-cell" onclick="event.stopPropagation()">
          <input type="checkbox" class="bulk-row-check" data-id="${escapeHtml(s.id)}">
        </td>
        <td class="mono clickable" onclick="location.hash='#/schedules/${encodeURIComponent(s.id)}'">${Components.truncateID(s.id)}</td>
        <td class="clickable" onclick="location.hash='#/schedules/${encodeURIComponent(s.id)}'">${escapeHtml(s.name || '-')}</td>
        <td class="mono clickable" onclick="location.hash='#/schedules/${encodeURIComponent(s.id)}'">${Components.truncateID(s.target_id)}</td>
        <td class="mono clickable" onclick="location.hash='#/schedules/${encodeURIComponent(s.id)}'">${s.template_id ? Components.truncateID(s.template_id) : '<span class="text-muted">—</span>'}</td>
        <td class="clickable" onclick="location.hash='#/schedules/${encodeURIComponent(s.id)}'">${Components.scheduleStatusBadge(s.status)}</td>
        <td class="clickable" onclick="location.hash='#/schedules/${encodeURIComponent(s.id)}'">${Components.rruleCompact(s.rrule)}</td>
        <td class="text-muted clickable" onclick="location.hash='#/schedules/${encodeURIComponent(s.id)}'">${Components.nextRun(s.next_run_at)}</td>
      </tr>
    `).join('');

    const pager = this.pagerHtml(total, f);

    return `
      <div class="page-header">
        <h2>Schedules</h2>
        <p>${total} schedule${total === 1 ? '' : 's'}</p>
        <div style="margin-left:auto">
          <button type="button" class="btn btn-primary" id="schedules-new">New schedule</button>
        </div>
      </div>
      ${filterBar}
      <div id="schedules-bulk-error" style="margin-bottom:8px"></div>
      ${Components.table(
        ['<input type="checkbox" id="bulk-select-all" title="Select all on this page">',
         'ID', 'Name', 'Target', 'Template', 'Status', 'RRULE', 'Next run'],
        [rows]
      )}
      ${pager}
      <div id="schedules-bulk-bar-host">${Components.bulkActionsBar({
        selectedCount: 0,
        actions: [
          { label: 'Pause selected' },
          { label: 'Resume selected' },
          { label: 'Delete selected', danger: true },
        ],
      })}</div>
    `;
  },

  pagerHtml(total, f) {
    if (total <= f.limit) return '';
    const page = Math.floor(f.offset / f.limit) + 1;
    const pages = Math.max(1, Math.ceil(total / f.limit));
    const prev = f.offset > 0
      ? `<a href="${this.hashFor(f, f.offset - f.limit)}" class="btn btn-sm btn-ghost">&larr; Prev</a>`
      : `<span class="btn btn-sm btn-ghost" style="opacity:0.4">&larr; Prev</span>`;
    const next = f.offset + f.limit < total
      ? `<a href="${this.hashFor(f, f.offset + f.limit)}" class="btn btn-sm btn-ghost">Next &rarr;</a>`
      : `<span class="btn btn-sm btn-ghost" style="opacity:0.4">Next &rarr;</span>`;
    return `<div class="pager" style="display:flex;gap:12px;align-items:center;margin-top:12px">
      ${prev}
      <span class="text-muted">Page ${page} of ${pages}</span>
      ${next}
    </div>`;
  },

  hashFor(f, offset) {
    const params = { ...f, offset };
    const q = Object.entries(params)
      .filter(([, v]) => v !== '' && v != null)
      .map(([k, v]) => encodeURIComponent(k) + '=' + encodeURIComponent(v))
      .join('&');
    return '#/schedules' + (q ? '?' + q : '');
  },

  bindEvents(app, f) {
    const form = document.getElementById('schedules-filters');
    if (form) {
      form.addEventListener('submit', (e) => {
        e.preventDefault();
        const next = {
          status: document.getElementById('filter-status').value,
          target_id: document.getElementById('filter-target').value.trim(),
          template_id: document.getElementById('filter-template').value.trim(),
          limit: f.limit,
          offset: 0,
        };
        location.hash = this.hashFor(next, 0);
      });
    }
    const newBtn = document.getElementById('schedules-new');
    if (newBtn) newBtn.addEventListener('click', () => this.openCreateForm(app, f));

    // Bulk selection wiring. Selection state lives on the page module
    // as a Set<string> so toggling checkboxes doesn't force a list
    // re-render; after a successful bulk op we refresh the whole page
    // and selection resets naturally.
    this._selected = new Set();
    const checks = app.querySelectorAll('.bulk-row-check');
    checks.forEach(cb => cb.addEventListener('change', () => this.onRowCheck(app, f)));

    const all = document.getElementById('bulk-select-all');
    if (all) all.addEventListener('change', () => {
      checks.forEach(cb => { cb.checked = all.checked; });
      this.onRowCheck(app, f);
    });

    this.rebindBulkBar(app, f);
  },

  onRowCheck(app, f) {
    const checks = app.querySelectorAll('.bulk-row-check');
    this._selected = new Set();
    checks.forEach(cb => {
      const row = cb.closest('tr');
      if (cb.checked) {
        this._selected.add(cb.dataset.id);
        if (row) row.classList.add('row-selected');
      } else if (row) {
        row.classList.remove('row-selected');
      }
    });
    const host = document.getElementById('schedules-bulk-bar-host');
    if (host) {
      host.innerHTML = Components.bulkActionsBar({
        selectedCount: this._selected.size,
        actions: [
          { label: 'Pause selected' },
          { label: 'Resume selected' },
          { label: 'Delete selected', danger: true },
        ],
      });
      this.rebindBulkBar(app, f);
    }
  },

  rebindBulkBar(app, f) {
    const host = document.getElementById('schedules-bulk-bar-host');
    if (!host) return;
    const btns = host.querySelectorAll('[data-bulk-action]');
    btns.forEach(btn => {
      const idx = parseInt(btn.dataset.bulkAction, 10);
      btn.addEventListener('click', () => {
        if (idx === 0) this.runBulk(app, f, 'pause');
        else if (idx === 1) this.runBulk(app, f, 'resume');
        else if (idx === 2) this.confirmBulkDelete(app, f);
      });
    });
  },

  async confirmBulkDelete(app, f) {
    const ids = Array.from(this._selected);
    if (!ids.length) return;
    const ok = await Components.confirmDialog({
      title: 'Delete schedules?',
      message: `Delete ${ids.length} schedule(s)? This cannot be undone.`,
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!ok) return;
    await this.runBulk(app, f, 'delete');
  },

  async runBulk(app, f, operation) {
    const ids = Array.from(this._selected);
    if (!ids.length) return;
    const errBox = document.getElementById('schedules-bulk-error');
    if (errBox) errBox.innerHTML = '';
    try {
      const resp = await API.bulkSchedules({ operation, schedule_ids: ids });
      const succeeded = resp && resp.succeeded ? resp.succeeded.length : 0;
      const failed = resp && resp.failed ? resp.failed : [];
      if (errBox) {
        const parts = [`<div class="text-muted">Bulk ${escapeHtml(operation)}: <strong>${succeeded}</strong> succeeded, <strong>${failed.length}</strong> failed.</div>`];
        if (failed.length) {
          parts.push('<ul>' + failed.map(fe =>
            `<li class="mono">${escapeHtml(fe.schedule_id)}: ${escapeHtml(fe.error)}</li>`
          ).join('') + '</ul>');
        }
        errBox.innerHTML = parts.join('');
      }
      // Refresh the list so paused→active (etc.) visibly flips and
      // deleted rows disappear. Keep filters.
      SchedulesPage.render(app, f);
    } catch (err) {
      if (errBox) errBox.innerHTML = Components.errorBanner(err);
    }
  },

  // --- SPEC-SCHED1.4b: create / edit form modal ---

  openCreateForm(app, listFilters) {
    this.openForm({
      mode: 'create',
      onSaved: () => SchedulesPage.render(app, listFilters || {}),
    });
  },

  openEditForm(app, schedule) {
    this.openForm({
      mode: 'edit',
      schedule,
      onSaved: () => SchedulesPage.renderDetail(app, schedule.id),
    });
  },

  openForm({ mode, schedule, onSaved }) {
    const s = schedule || {};
    const body = `
      <form id="schedule-form" novalidate>
        <div class="field-error" data-field-error="__general__" style="display:none;margin-bottom:12px;padding:8px 12px;background:var(--sev-critical-bg);border-radius:var(--radius)"></div>
        ${Components.formInput({ label: 'Name', name: 'name', value: s.name, required: true })}
        ${Components.formInput({
          label: 'Target ID', name: 'target_id', value: s.target_id, required: true,
          placeholder: 'UUID from /targets',
          help: mode === 'edit' ? 'Target cannot be changed after creation.' : '',
        })}
        ${Components.formInput({ label: 'Template ID', name: 'template_id', value: s.template_id || '',
          help: 'Optional. Omit to run with tool_config directly.' })}
        ${Components.rruleField({ label: 'RRULE', name: 'rrule', value: s.rrule })}
        ${Components.formDatetime({ label: 'DTSTART', name: 'dtstart', value: s.dtstart, required: true })}
        ${Components.formInput({ label: 'Timezone', name: 'timezone', value: s.timezone || 'UTC', required: true })}
        ${Components.formInput({
          label: 'Estimated duration (seconds)', name: 'estimated_duration_seconds',
          type: 'number', value: '',
          help: 'Used for overlap checks at create/update time. Leave blank to use the server default (3600s).',
        })}
        ${mode === 'edit' ? Components.formSelect({
          label: 'Status', name: 'status',
          options: [{ value: 'active', label: 'Active' }, { value: 'paused', label: 'Paused' }],
          value: s.status || 'active',
        }) : ''}
      </form>
    `;

    const m = Components.modal({
      title: mode === 'create' ? 'New schedule' : 'Edit schedule',
      body,
      size: 'md',
      primaryAction: {
        label: mode === 'create' ? 'Create' : 'Save',
        onClick: async ({ close }) => {
          const form = document.getElementById('schedule-form');
          const data = readScheduleForm(form, mode);
          if (!data) return;
          try {
            if (mode === 'create') await API.createSchedule(data);
            else await API.updateSchedule(schedule.id, data);
            if (onSaved) onSaved();
            close('saved');
          } catch (err) {
            showFormError(form, err);
          }
        },
      },
      secondaryAction: { label: 'Cancel' },
    });
    // Non-blocking RRULE syntax warning wires after the DOM is in place.
    Components.rruleAttachBlurCheck(m.root.querySelector('#schedule-form'), 'rrule');
  },

  // --- Schedule detail ---

  async renderDetail(app, id) {
    app.innerHTML = '<div class="loading">Loading schedule...</div>';
    const scroller = document.querySelector('main.content');
    if (scroller) scroller.scrollTop = 0;

    try {
      const s = await API.getSchedule(id);
      let upcoming = [];
      try {
        // The 1.3a /schedules/upcoming endpoint doesn't filter by
        // schedule_id, so we client-filter. Small N, acceptable at this
        // scale. 1.4b may add server-side filter.
        const resp = await API.upcomingSchedules({ target_id: s.target_id, horizon: '720h', limit: 200 });
        upcoming = (resp.items || []).filter(it => it.schedule_id === s.id).slice(0, 5);
      } catch (_) {
        upcoming = [];
      }
      app.innerHTML = this.detailTemplate(s, upcoming);
      this.bindDetailActions(app, s);
    } catch (err) {
      app.innerHTML = `
        ${Components.backLink('#/schedules', 'Back to schedules')}
        ${Components.errorBanner(err)}
      `;
    }
  },

  bindDetailActions(app, s) {
    const editBtn = document.getElementById('schedule-edit-btn');
    if (editBtn) editBtn.addEventListener('click', () => this.openEditForm(app, s));

    const pauseBtn = document.getElementById('schedule-pause-btn');
    if (pauseBtn) pauseBtn.addEventListener('click', () => this.setPauseState(app, s, false));

    const resumeBtn = document.getElementById('schedule-resume-btn');
    if (resumeBtn) resumeBtn.addEventListener('click', () => this.setPauseState(app, s, true));

    const delBtn = document.getElementById('schedule-delete-btn');
    if (delBtn) delBtn.addEventListener('click', () => this.confirmDelete(app, s));
  },

  async setPauseState(app, s, toActive) {
    const btnId = toActive ? 'schedule-resume-btn' : 'schedule-pause-btn';
    const btn = document.getElementById(btnId);
    const origLabel = btn ? btn.textContent : '';
    if (btn) { btn.disabled = true; btn.textContent = '…'; }
    try {
      if (toActive) await API.resumeSchedule(s.id);
      else await API.pauseSchedule(s.id);
      SchedulesPage.renderDetail(app, s.id);
    } catch (err) {
      const box = document.getElementById('schedule-action-error');
      if (box) box.innerHTML = Components.errorBanner(err);
      if (btn) { btn.disabled = false; btn.textContent = origLabel; }
    }
  },

  async confirmDelete(app, s) {
    const ok = await Components.confirmDialog({
      title: 'Delete schedule?',
      message: `Delete schedule "${s.name || s.id}"? This cannot be undone.`,
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!ok) return;
    try {
      await API.deleteSchedule(s.id);
      location.hash = '#/schedules';
      SchedulesPage.render(app, {});
    } catch (err) {
      const box = document.getElementById('schedule-action-error');
      if (box) box.innerHTML = Components.errorBanner(err);
    }
  },

  detailTemplate(s, upcoming) {
    const overrideList = (s.overrides && s.overrides.length)
      ? s.overrides.map(o => `<code class="mono">${escapeHtml(o)}</code>`).join(', ')
      : '<span class="text-muted">none</span>';

    const upcomingRows = upcoming.length === 0
      ? `<span class="text-muted">No upcoming firings in the next 30 days.</span>`
      : `<ul>${upcoming.map(u => `<li class="mono">${escapeHtml(u.fires_at)}</li>`).join('')}</ul>`;

    const mw = s.maintenance_window
      ? `<pre class="json-block">${escapeHtml(JSON.stringify(s.maintenance_window, null, 2))}</pre>`
      : '<span class="text-muted">none</span>';

    const tmplCell = s.template_id
      ? `<a href="#/templates/${encodeURIComponent(s.template_id)}" class="mono">${escapeHtml(s.template_id)}</a>`
      : '<span class="text-muted">—</span>';

    return `
      ${Components.backLink('#/schedules', 'Back to schedules')}
      <div class="scan-detail-stack">
        <div class="detail-panel">
          <div class="detail-header">
            <h2>${escapeHtml(s.name || s.id)}</h2>
            ${Components.scheduleStatusBadge(s.status)}
            <div style="margin-left:auto;display:flex;gap:8px" id="schedule-detail-actions">
              <button type="button" class="btn btn-ghost" id="schedule-edit-btn">Edit</button>
              ${s.status === 'active'
                ? '<button type="button" class="btn btn-ghost" id="schedule-pause-btn">Pause</button>'
                : '<button type="button" class="btn btn-ghost" id="schedule-resume-btn">Resume</button>'}
              <button type="button" class="btn btn-danger" id="schedule-delete-btn">Delete</button>
            </div>
            <div id="schedule-action-error" style="margin-top:8px"></div>
          </div>
          <div class="detail-grid" style="margin-top:12px">
            <span class="detail-label">ID</span><span class="detail-value mono">${escapeHtml(s.id)}</span>
            <span class="detail-label">Target</span>
            <span class="detail-value mono">
              <a href="#/targets/${encodeURIComponent(s.target_id)}">${escapeHtml(s.target_id)}</a>
            </span>
            <span class="detail-label">Template</span><span class="detail-value">${tmplCell}</span>
            <span class="detail-label">Timezone</span><span class="detail-value mono">${escapeHtml(s.timezone || '-')}</span>
            <span class="detail-label">DTSTART</span><span class="detail-value mono">${escapeHtml(s.dtstart || '-')}</span>
            <span class="detail-label">RRULE</span><span class="detail-value mono" style="word-break:break-all">${escapeHtml(s.rrule || '-')}</span>
            <span class="detail-label">Overrides</span><span class="detail-value">${overrideList}</span>
            <span class="detail-label">Next run</span><span class="detail-value">${Components.nextRun(s.next_run_at)}</span>
            <span class="detail-label">Last run</span><span class="detail-value">${s.last_run_at ? Components.formatDate(s.last_run_at) : '<span class="text-muted">never</span>'}</span>
            <span class="detail-label">Last status</span><span class="detail-value">${s.last_run_status ? escapeHtml(s.last_run_status) : '<span class="text-muted">—</span>'}</span>
            <span class="detail-label">Last scan</span><span class="detail-value">${s.last_scan_id ? `<a href="#/scans/${encodeURIComponent(s.last_scan_id)}" class="mono">${escapeHtml(s.last_scan_id)}</a>` : '<span class="text-muted">—</span>'}</span>
            <span class="detail-label">Created</span><span class="detail-value">${Components.formatDate(s.created_at)}</span>
            <span class="detail-label">Updated</span><span class="detail-value">${Components.formatDate(s.updated_at)}</span>
          </div>
        </div>

        <div class="card">
          <div class="card-label">Next 5 firings (30-day horizon)</div>
          <div style="margin-top:8px">${upcomingRows}</div>
        </div>

        <div class="card">
          <div class="card-label">Maintenance window</div>
          <div style="margin-top:8px">${mw}</div>
        </div>

        <div class="card">
          <div class="card-label">Tool config</div>
          <pre class="json-block">${escapeHtml(JSON.stringify(s.tool_config || {}, null, 2))}</pre>
        </div>
      </div>
    `;
  },
};

// readScheduleForm serializes the schedule form into the API shape.
// DTSTART is converted from "datetime-local" ("YYYY-MM-DDTHH:MM") to a
// UTC ISO string; empty template_id is submitted as "" so the server
// treats it as "no template" (edit flow uses clear_template when the
// caller wants to explicitly drop the link, but the UI doesn't surface
// that nuance yet — 1.4c may expose a dedicated "clear template" ticker).
// Returns null after surfacing field errors when the form is syntactically
// bad at the client level.
function readScheduleForm(form, mode) {
  if (!form) return null;
  const fd = new FormData(form);
  const out = {
    name: (fd.get('name') || '').toString().trim(),
    target_id: (fd.get('target_id') || '').toString().trim(),
    template_id: (fd.get('template_id') || '').toString().trim(),
    rrule: (fd.get('rrule') || '').toString().trim(),
    timezone: (fd.get('timezone') || '').toString().trim() || 'UTC',
  };
  const dtLocal = (fd.get('dtstart') || '').toString().trim();
  if (dtLocal) {
    const d = new Date(dtLocal);
    if (isNaN(d.getTime())) {
      Components.applyFieldErrors(form, [{ field: 'dtstart', message: 'invalid date/time' }]);
      return null;
    }
    out.dtstart = d.toISOString();
  }
  const dur = (fd.get('estimated_duration_seconds') || '').toString().trim();
  if (dur) {
    const n = parseInt(dur, 10);
    if (isNaN(n) || n < 0) {
      Components.applyFieldErrors(form, [{ field: 'estimated_duration_seconds', message: 'must be a non-negative integer' }]);
      return null;
    }
    out.estimated_duration_seconds = n;
  }
  // template_id: empty string means "not set" — drop the key on create
  // so the omitempty JSON handling on the server treats it naturally.
  // On edit we send "" if the user cleared it so the server applies
  // clear_template semantics via the partial patch.
  if (!out.template_id) {
    delete out.template_id;
  }
  if (mode === 'edit') {
    const status = (fd.get('status') || '').toString();
    if (status === 'active' || status === 'paused') {
      out.enabled = status === 'active';
    }
  }
  return out;
}

// showFormError routes an APIError into the right surface: 422 with
// field_errors → inline via applyFieldErrors; everything else → the
// general banner at the top of the form.
function showFormError(form, err) {
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
