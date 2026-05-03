// SPEC-SCHED1.4a: Schedules page (list + detail). Read-only; write flows
// land in 1.4b. Mirrors the targets.js / scans.js layout so nav, loading,
// empty, and error states match the rest of the SPA.
//
// PR6 #39 — list now resolves target/template UUIDs to names by fetching
// /targets and /templates in parallel with /schedules. The lookup cache
// is module-scoped and invalidated on any mutation flow that changes
// names (create/edit/delete of schedule, plus full reload via hash nav).
const SchedulesPage = {
  pageSize: 50,

  // _lookupCache memoizes the parallel /targets and /templates fetches
  // across renders within the same SPA session. Invalidated by any of
  // our own write flows; external mutations (CLI) require a refresh.
  _lookupCache: null,

  async _getLookups() {
    if (this._lookupCache) return this._lookupCache;
    const [targetsResp, templatesResp] = await Promise.all([
      API.targets().catch(() => ({ targets: [] })),
      API.listTemplates({ limit: 500 }).catch(() => ({ items: [] })),
    ]);
    const targets = new Map();
    const targetsList = (targetsResp && (targetsResp.targets || targetsResp.items)) || [];
    targetsList.forEach(t => { if (t && t.id) targets.set(t.id, t); });
    const templates = new Map();
    const templatesList = (templatesResp && templatesResp.items) || [];
    templatesList.forEach(t => { if (t && t.id) templates.set(t.id, t); });
    this._lookupCache = { targets, templates };
    return this._lookupCache;
  },

  _invalidateLookups() { this._lookupCache = null; },

  async render(app, params) {
    if (params && params.id) {
      return this.renderDetail(app, params.id);
    }
    app.innerHTML = '<div class="loading">Loading schedules...</div>';

    const filters = this.parseFilters(params || {});
    try {
      const [data, lookups] = await Promise.all([
        API.listSchedules(this.apiParams(filters)),
        this._getLookups(),
      ]);
      app.innerHTML = this.template(data, filters, lookups);
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

  template(data, f, lookups) {
    const items = data.items || [];
    const total = data.total || 0;
    const L = lookups || { targets: new Map(), templates: new Map() };

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

    const rows = items.map(s => {
      const sid = encodeURIComponent(s.id);
      const goRow = `location.hash='#/schedules/${sid}'`;

      // Target cell: resolved value with UUID short-id underneath; if
      // the target was deleted between fetch and render, surface a
      // tooltip explaining the gap rather than blanking the cell.
      const tgt = L.targets.get(s.target_id);
      const tgtCell = tgt
        ? `<td class="clickable" onclick="event.stopPropagation();location.hash='#/targets/${encodeURIComponent(s.target_id)}'">
             <div>${escapeHtml(tgt.value || s.target_id)}</div>
             <small class="text-muted mono">${Components.truncateID(s.target_id)}</small>
           </td>`
        : `<td class="mono clickable" title="Target not found — may have been deleted." onclick="${goRow}">
             ${Components.truncateID(s.target_id)}
           </td>`;

      // Template cell: name + short UUID, or em-dash when the schedule
      // doesn't reference a template at all.
      let tmplCell;
      if (!s.template_id) {
        tmplCell = `<td class="clickable" onclick="${goRow}"><span class="text-muted">—</span></td>`;
      } else {
        const tmpl = L.templates.get(s.template_id);
        tmplCell = tmpl
          ? `<td class="clickable" onclick="event.stopPropagation();location.hash='#/templates/${encodeURIComponent(s.template_id)}'">
               <div>${escapeHtml(tmpl.name || s.template_id)}</div>
               <small class="text-muted mono">${Components.truncateID(s.template_id)}</small>
             </td>`
          : `<td class="mono clickable" title="Template not found — may have been deleted." onclick="${goRow}">
               ${Components.truncateID(s.template_id)}
             </td>`;
      }

      const isActive = s.status === 'active';
      const pauseLabel = isActive ? 'Pause' : 'Resume';
      const actions = `
        <td class="schedule-actions" onclick="event.stopPropagation()">
          <button type="button" class="pill pill-action row-action-pause" data-row-id="${escapeHtml(s.id)}" data-action="${isActive ? 'pause' : 'resume'}">${pauseLabel}</button>
          <button type="button" class="pill pill-action row-action-edit" data-row-id="${escapeHtml(s.id)}" data-action="edit">Edit</button>
        </td>
      `;

      return `
        <tr data-schedule-id="${sid}">
          <td class="bulk-cell" onclick="event.stopPropagation()">
            <input type="checkbox" class="bulk-row-check" data-id="${escapeHtml(s.id)}">
          </td>
          <td class="mono clickable" onclick="${goRow}">${Components.truncateID(s.id)}</td>
          <td class="clickable" onclick="${goRow}">${escapeHtml(s.name || '-')}</td>
          ${tgtCell}
          ${tmplCell}
          <td class="clickable" onclick="${goRow}">${Components.scheduleStatusBadge(s.status)}</td>
          <td class="clickable" onclick="${goRow}">${Components.rruleCellHtml(s.rrule)}</td>
          <td class="text-muted clickable" onclick="${goRow}">${Components.nextRun(s.next_run_at)}</td>
          ${actions}
        </tr>
      `;
    }).join('');

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
         'ID', 'Name', 'Target', 'Template', 'Status', 'Schedule', 'Next run', ''],
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

    // Inline row actions: Pause/Resume/Edit pills. We don't reuse the
    // bulk-action handlers because these need per-row state (id, the
    // schedule object for Edit) and have to refresh just the list.
    app.querySelectorAll('.pill-action').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        e.preventDefault();
        const id = btn.dataset.rowId;
        const action = btn.dataset.action;
        if (!id || !action) return;
        if (action === 'pause' || action === 'resume') {
          this.inlinePauseResume(app, f, id, action === 'resume');
        } else if (action === 'edit') {
          this.inlineEdit(app, f, id);
        }
      });
    });

    this.rebindBulkBar(app, f);
  },

  async inlinePauseResume(app, f, id, toActive) {
    const errBox = document.getElementById('schedules-bulk-error');
    if (errBox) errBox.innerHTML = '';
    const btn = app.querySelector(`.row-action-pause[data-row-id="${cssEscape(id)}"]`);
    const orig = btn ? btn.textContent : '';
    if (btn) { btn.disabled = true; btn.textContent = '…'; }
    try {
      if (toActive) await API.resumeSchedule(id);
      else await API.pauseSchedule(id);
      // Names didn't change; keep the lookup cache. Re-render the list
      // so the status badge flips and the pill swaps Pause↔Resume.
      SchedulesPage.render(app, f);
    } catch (err) {
      if (errBox) errBox.innerHTML = Components.errorBanner(err);
      if (btn) { btn.disabled = false; btn.textContent = orig; }
    }
  },

  async inlineEdit(app, f, id) {
    const errBox = document.getElementById('schedules-bulk-error');
    if (errBox) errBox.innerHTML = '';
    let s;
    try {
      s = await API.getSchedule(id);
    } catch (err) {
      if (errBox) errBox.innerHTML = Components.errorBanner(err);
      return;
    }
    this.openForm({
      mode: 'edit',
      schedule: s,
      onSaved: () => {
        SchedulesPage._invalidateLookups();
        SchedulesPage.render(app, f);
      },
    });
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
      // deleted rows disappear. Keep filters. Bulk delete may have
      // removed schedules referencing now-orphan target IDs in the
      // lookup, but the lookup is keyed by target_id so it stays
      // valid; only delete invalidates because the page itself moves.
      if (operation === 'delete') SchedulesPage._invalidateLookups();
      SchedulesPage.render(app, f);
    } catch (err) {
      if (errBox) errBox.innerHTML = Components.errorBanner(err);
    }
  },

  // --- SPEC-SCHED1.4b: create / edit form modal ---

  openCreateForm(app, listFilters) {
    this.openForm({
      mode: 'create',
      onSaved: () => {
        SchedulesPage._invalidateLookups();
        SchedulesPage.render(app, listFilters || {});
      },
    });
  },

  // PR8 #41 — public alias for the topbar +New menu. The list page's
  // own "New schedule" button still calls openCreateForm directly with
  // its filter snapshot; this version assumes the operator wants a
  // fresh form and refreshes the unfiltered list on save.
  openCreateModal() {
    return this.openCreateForm(document.getElementById('app'), {});
  },

  openEditForm(app, schedule) {
    this.openForm({
      mode: 'edit',
      schedule,
      onSaved: () => {
        SchedulesPage._invalidateLookups();
        SchedulesPage.renderDetail(app, schedule.id);
      },
    });
  },

  // openForm opens the schedule create/edit modal.
  //
  // PR6 #39 — async because we pre-load the targets/templates lookup so
  // the entity pickers have data; in-flight callers don't need to await
  // (they're fire-and-forget). Form sub-fields:
  //   - Target/Template via Components.entityPicker (datalist typeahead).
  //   - Schedule via Components.rruleBuilder (preset selector + live preview).
  //   - DTSTART pre-filled to "now + 15 min" rounded; timezone defaults
  //     to the browser's IANA zone — both reduce empty-required friction.
  //   - Advanced disclosure hides estimated_duration + edit-mode status.
  async openForm({ mode, schedule, onSaved }) {
    const lookups = await this._getLookups();
    const targetOptions = Array.from(lookups.targets.values()).map(t => ({
      id: t.id, label: t.value || t.id,
    }));
    const templateOptions = Array.from(lookups.templates.values()).map(t => ({
      id: t.id, label: t.name || t.id,
    }));

    const s = schedule || {};
    const isCreate = mode === 'create';
    const dtstartDefault = isCreate ? defaultDtstartLocal() : '';
    const tzDefault = s.timezone || (isCreate ? browserTimezone() : 'UTC');
    const targetHelp = mode === 'edit'
      ? 'Target cannot be changed after creation.'
      : (targetOptions.length ? 'Pick a target by name or paste a UUID.' : 'No targets yet — create one in /targets first.');

    const body = `
      <form id="schedule-form" novalidate>
        <div class="field-error" data-field-error="__general__" style="display:none;margin-bottom:12px;padding:8px 12px;background:var(--sev-critical-bg);border-radius:var(--radius)"></div>

        ${Components.formInput({
          label: 'Name', name: 'name', value: s.name, required: true,
          placeholder: 'auto-suggested from target + cadence',
        })}

        ${Components.entityPicker({
          label: 'Target', name: 'target_id', value: s.target_id || '',
          options: targetOptions, required: true,
          placeholder: targetOptions.length ? 'Select a target…' : 'No targets available',
          help: targetHelp,
        })}

        ${Components.entityPicker({
          label: 'Template', name: 'template_id', value: s.template_id || '',
          options: templateOptions, allowEmpty: true,
          placeholder: 'None — use tool_config directly',
          help: 'Optional. Pick by name or leave blank.',
        })}

        ${Components.rruleBuilder({
          label: 'Schedule', name: 'rrule', value: s.rrule, required: true,
        })}

        ${Components.formDatetime({
          label: 'Starts at', name: 'dtstart',
          value: s.dtstart || dtstartDefault, required: true,
        })}

        ${Components.formInput({
          label: 'Timezone', name: 'timezone', value: tzDefault, required: true,
          placeholder: 'Europe/Madrid',
          help: 'IANA timezone identifier (e.g. UTC, Europe/Madrid, America/New_York).',
        })}

        <details class="form-disclosure">
          <summary>Advanced</summary>
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
        </details>
      </form>
    `;

    const m = Components.modal({
      title: isCreate ? 'New schedule' : 'Edit schedule',
      body,
      size: 'md',
      primaryAction: {
        label: isCreate ? 'Create' : 'Save',
        onClick: async ({ close }) => {
          const form = document.getElementById('schedule-form');
          const data = readScheduleForm(form, mode, lookups);
          if (!data) return;
          try {
            if (isCreate) await API.createSchedule(data);
            else await API.updateSchedule(schedule.id, data);
            if (onSaved) onSaved();
            close('saved');
          } catch (err) {
            showFormError(form, err);
          }
        },
      },
      secondaryAction: { label: 'Cancel' },
      onClose: () => {
        // SPEC-SCHED2.2: clear field values on unmount so browser
        // autofill can't leak state from one open into the next.
        const form = m.root.querySelector('#schedule-form');
        if (!form) return;
        Array.from(form.elements).forEach(el => {
          const tag = el.tagName;
          if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') {
            el.value = '';
          }
        });
      },
    });

    const form = m.root.querySelector('#schedule-form');
    // SPEC-SCHED2.2: explicit state hydration so Chrome/Safari autofill
    // can't override the freshly mounted values with cached prior input.
    hydrateScheduleForm(form, s, mode, lookups);
    // Wire the rrule builder (preset selector + live preview).
    Components.rruleBuilderAttach(form);

    // Auto-suggest the schedule name in create mode while the user
    // hasn't edited it. Stops as soon as the operator types — never
    // clobber a manually entered name.
    if (isCreate) {
      const nameInput = form.querySelector('[name="name"]');
      const targetInput = form.querySelector('[name="target_id"]');
      let nameTouched = !!s.name;
      if (nameInput) {
        nameInput.addEventListener('input', () => { nameTouched = true; });
      }
      const refreshName = () => {
        if (nameTouched || !nameInput || !targetInput) return;
        const tgtTyped = (targetInput.value || '').trim();
        const tgtId = Components.entityPickerResolve(tgtTyped, targetOptions);
        // Prefer the resolved label when the user picked from the list
        // (covers paste-a-UUID flow too); fall back to the raw text.
        const tgtLabel = tgtId
          ? (targetOptions.find(o => o.id === tgtId) || {}).label || tgtTyped
          : tgtTyped;
        const rr = (form.querySelector('[data-rb-output]') || {}).value || '';
        const suggested = suggestScheduleName(tgtLabel, rr);
        if (suggested) nameInput.value = suggested;
      };
      if (targetInput) {
        targetInput.addEventListener('input', refreshName);
        targetInput.addEventListener('change', refreshName);
      }
      form.addEventListener('rrule-change', refreshName);
      // Initial suggestion — the rrule builder fires its first
      // rrule-change on attach, so the suggestion lands without us
      // explicitly poking it here.
    }
  },

  // --- Schedule detail ---

  async renderDetail(app, id) {
    app.innerHTML = '<div class="loading">Loading schedule...</div>';
    const scroller = document.querySelector('main.content');
    if (scroller) scroller.scrollTop = 0;

    try {
      const [s, lookups] = await Promise.all([
        API.getSchedule(id),
        this._getLookups(),
      ]);
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
      app.innerHTML = this.detailTemplate(s, upcoming, lookups);
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
      SchedulesPage._invalidateLookups();
      location.hash = '#/schedules';
      SchedulesPage.render(app, {});
    } catch (err) {
      const box = document.getElementById('schedule-action-error');
      if (box) box.innerHTML = Components.errorBanner(err);
    }
  },

  detailTemplate(s, upcoming, lookups) {
    const L = lookups || { targets: new Map(), templates: new Map() };
    const overrideList = (s.overrides && s.overrides.length)
      ? s.overrides.map(o => `<code class="mono">${escapeHtml(o)}</code>`).join(', ')
      : '<span class="text-muted">none</span>';

    const upcomingRows = upcoming.length === 0
      ? `<span class="text-muted">No upcoming firings in the next 30 days.</span>`
      : `<ul>${upcoming.map(u => `<li class="mono">${escapeHtml(u.fires_at)}</li>`).join('')}</ul>`;

    const mw = s.maintenance_window
      ? `<pre class="json-block">${escapeHtml(JSON.stringify(s.maintenance_window, null, 2))}</pre>`
      : '<span class="text-muted">none</span>';

    // Target: link to detail; show resolved value with the UUID
    // underneath. Falls back to the raw UUID + tooltip if the target
    // was deleted between fetches.
    const tgt = L.targets.get(s.target_id);
    const tgtCell = tgt
      ? `<a href="#/targets/${encodeURIComponent(s.target_id)}">${escapeHtml(tgt.value || s.target_id)}</a>
         <div><small class="text-muted mono">${escapeHtml(s.target_id)}</small></div>`
      : `<a href="#/targets/${encodeURIComponent(s.target_id)}" class="mono" title="Target not found — may have been deleted.">${escapeHtml(s.target_id)}</a>`;

    let tmplCell;
    if (!s.template_id) {
      tmplCell = '<span class="text-muted">—</span>';
    } else {
      const tmpl = L.templates.get(s.template_id);
      tmplCell = tmpl
        ? `<a href="#/templates/${encodeURIComponent(s.template_id)}">${escapeHtml(tmpl.name || s.template_id)}</a>
           <div><small class="text-muted mono">${escapeHtml(s.template_id)}</small></div>`
        : `<a href="#/templates/${encodeURIComponent(s.template_id)}" class="mono" title="Template not found — may have been deleted.">${escapeHtml(s.template_id)}</a>`;
    }

    // RRULE detail row: humanized line first, with the raw rule kept
    // visible underneath so operators can copy/edit it without hovering.
    const human = Components.humanizeRRule(s.rrule);
    const rruleHuman = (typeof human === 'string' && human.charAt(0) === '<')
      ? human  // unknown pattern — already a span with title=
      : escapeHtml(human);
    const rruleRaw = s.rrule
      ? `<div><small class="mono text-muted">${escapeHtml(s.rrule)}</small></div>`
      : '';

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
            <span class="detail-value">${tgtCell}</span>
            <span class="detail-label">Template</span><span class="detail-value">${tmplCell}</span>
            <span class="detail-label">Timezone</span><span class="detail-value mono">${escapeHtml(s.timezone || '-')}</span>
            <span class="detail-label">DTSTART</span><span class="detail-value mono">${escapeHtml(s.dtstart || '-')}</span>
            <span class="detail-label">RRULE</span>
            <span class="detail-value">${rruleHuman}${rruleRaw}</span>
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

// hydrateScheduleForm sets each field's `.value` IDL property explicitly
// from the schedule object after the modal mounts. Setting the property
// (not the attribute) ensures the browser's autofill / form-restore
// behavior cannot override us with cached values from a previous open
// of the same form. Required for SPEC-SCHED2.2 — without this, editing
// schedule A then closing and editing schedule B would re-display A's
// values and silently overwrite B on save.
//
// PR6 #39 — entity pickers display the human-readable label, not the
// raw UUID. We resolve target/template via the lookups Map so re-opens
// don't show "abc-123" instead of "iana.org". The rrule builder
// hydrates from data-rrule-initial in rruleBuilderAttach, so this fn
// doesn't touch the rrule field.
function hydrateScheduleForm(form, schedule, mode, lookups) {
  if (!form) return;
  const s = schedule || {};
  const isCreate = mode === 'create';
  const L = lookups || { targets: new Map(), templates: new Map() };

  const setVal = (name, value) => {
    const el = form.elements[name];
    if (!el) return;
    el.value = value == null ? '' : String(value);
  };

  setVal('name', isCreate ? '' : s.name);

  if (isCreate) {
    // Pre-fill the target picker if the caller seeded a target_id
    // (e.g. targets-page "Create schedule" button). Look up the label.
    if (s.target_id) {
      const t = L.targets.get(s.target_id);
      setVal('target_id', t ? (t.value || s.target_id) : s.target_id);
    } else {
      setVal('target_id', '');
    }
    setVal('template_id', '');
  } else {
    const t = L.targets.get(s.target_id);
    setVal('target_id', t ? (t.value || s.target_id) : (s.target_id || ''));
    if (s.template_id) {
      const tmpl = L.templates.get(s.template_id);
      setVal('template_id', tmpl ? (tmpl.name || s.template_id) : s.template_id);
    } else {
      setVal('template_id', '');
    }
  }

  // datetime-local needs "YYYY-MM-DDTHH:MM"; the API gives back ISO-8601.
  // Create mode: pre-fill with the rounded "now + 15min" so the required
  // field isn't empty.
  setVal('dtstart', isCreate ? defaultDtstartLocal() : toDatetimeLocal(s.dtstart));
  setVal('timezone', s.timezone || (isCreate ? browserTimezone() : 'UTC'));
  setVal('estimated_duration_seconds', '');
  if (form.elements.status) {
    setVal('status', s.status || 'active');
  }
}

// readScheduleForm serializes the schedule form into the API shape.
// DTSTART is converted from "datetime-local" ("YYYY-MM-DDTHH:MM") to a
// UTC ISO string; empty template_id is dropped on create and sent as ""
// on edit so the server applies clear_template via the partial patch.
//
// PR6 #39 — target/template inputs carry the typed label or a pasted
// UUID; we resolve to UUID via Components.entityPickerResolve and
// surface a field error when the typed text matches nothing.
function readScheduleForm(form, mode, lookups) {
  if (!form) return null;
  const fd = new FormData(form);
  const L = lookups || { targets: new Map(), templates: new Map() };

  const targetOptions = Array.from(L.targets.values()).map(t => ({
    id: t.id, label: t.value || t.id,
  }));
  const templateOptions = Array.from(L.templates.values()).map(t => ({
    id: t.id, label: t.name || t.id,
  }));

  const targetTyped = (fd.get('target_id') || '').toString().trim();
  const targetID = Components.entityPickerResolve(targetTyped, targetOptions);
  if (targetID === null) {
    Components.applyFieldErrors(form, [{
      field: 'target_id',
      message: 'unknown target — pick one from the list or paste its UUID',
    }]);
    return null;
  }
  if (mode === 'create' && !targetID) {
    Components.applyFieldErrors(form, [{ field: 'target_id', message: 'required' }]);
    return null;
  }

  const templateTyped = (fd.get('template_id') || '').toString().trim();
  let templateID = '';
  if (templateTyped) {
    const r = Components.entityPickerResolve(templateTyped, templateOptions);
    if (r === null) {
      Components.applyFieldErrors(form, [{
        field: 'template_id',
        message: 'unknown template — pick one from the list, paste its UUID, or leave empty',
      }]);
      return null;
    }
    templateID = r;
  }

  const out = {
    name: (fd.get('name') || '').toString().trim(),
    rrule: (fd.get('rrule') || '').toString().trim(),
    timezone: (fd.get('timezone') || '').toString().trim() || 'UTC',
  };
  if (targetID) out.target_id = targetID;
  // template_id: drop the key on create so the server's omitempty
  // handling treats "no template" as "no template". On edit we send ""
  // when explicitly cleared so the server applies clear_template.
  if (mode === 'create') {
    if (templateID) out.template_id = templateID;
  } else {
    out.template_id = templateID;
  }

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
