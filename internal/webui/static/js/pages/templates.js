// SPEC-SCHED1.4a: Templates page (list + detail). Read-only.
//
// The USED BY column is omitted per OQ4 — computing it client-side needs
// one ListSchedules fetch per template, which is N+1 and degrades on any
// non-trivial list. 1.4b adds the count server-side (either as a field on
// TemplateResponse or via an aggregate endpoint). The detail page does
// list dependent schedules because that's a single call, not N.
const TemplatesPage = {
  pageSize: 50,

  async render(app, params) {
    if (params && params.id) {
      return this.renderDetail(app, params.id);
    }
    app.innerHTML = '<div class="loading">Loading templates...</div>';

    const limit = parseInt((params || {}).limit, 10) || this.pageSize;
    const offset = parseInt((params || {}).offset, 10) || 0;
    try {
      const data = await API.listTemplates({ limit, offset });
      // _lastItems backs the click handlers in bindListActions so cards
      // can pass the full template object to the duplicate / edit /
      // run flows without re-fetching.
      this._lastItems = (data && data.items) || [];
      app.innerHTML = this.template(data, { limit, offset });
      this.bindListActions(app);
      // SPEC-SCHED1.4c R6: hydrate USED BY counts for the visible
      // page. Runs after paint so the first render isn't blocked by
      // the N per-template fetches.
      this.hydrateUsedByCounts(data && data.items);
    } catch (err) {
      app.innerHTML = `
        <div class="page-header"><h2>Templates</h2></div>
        ${Components.errorBanner(err)}
      `;
    }
  },

  // USED_BY_SOFT_THRESHOLD: above this count we skip the per-template
  // fetch (OQ5) — the latency budget would balloon with the list size,
  // and anyone browsing that many templates probably already knows
  // which ones are hot. Placeholder '—' with a tooltip explains why.
  USED_BY_SOFT_THRESHOLD: 50,

  async hydrateUsedByCounts(items) {
    if (!items || !items.length) return;
    const cells = document.querySelectorAll('[data-used-by]');
    if (!cells.length) return;

    const setCount = (cell, label, title) => {
      cell.textContent = label;
      cell.title = title;
    };

    if (items.length > this.USED_BY_SOFT_THRESHOLD) {
      cells.forEach(cell => setCount(cell, '— schedules',
        'Count unavailable for lists over ' + this.USED_BY_SOFT_THRESHOLD + ' templates.'));
      return;
    }

    // Fetch in parallel: bounded by slowest single request, not by sum.
    const byId = new Map();
    cells.forEach(cell => byId.set(cell.dataset.usedBy, cell));

    await Promise.all(items.map(async t => {
      const cell = byId.get(t.id);
      if (!cell) return;
      try {
        const resp = await API.listSchedules({ template_id: t.id, limit: 1 });
        const total = (resp && typeof resp.total === 'number') ? resp.total : null;
        if (total == null) {
          setCount(cell, '? schedules', 'Count not available (API did not return a total).');
        } else {
          const noun = total === 1 ? 'schedule' : 'schedules';
          setCount(cell, `${total} ${noun}`,
            total === 0 ? 'No schedules reference this template.' : total + ' schedule(s) reference this template.');
        }
      } catch (err) {
        setCount(cell, '? schedules', 'Error loading count: ' + (err.message || 'unknown'));
      }
    }));
  },

  bindListActions(app) {
    const newBtn = document.getElementById('templates-new');
    if (newBtn) newBtn.addEventListener('click', () => this.openCreateForm(app));

    // Card click → detail. Action buttons inside the card stop
    // propagation so they don't double-navigate. We keep the
    // dispatching here (instead of inline onclick) so the per-card
    // template object can be passed by reference rather than re-fetched.
    const cards = app.querySelectorAll('.template-card');
    cards.forEach(card => {
      const id = card.dataset.id;
      if (!id) return;
      const t = (this._lastItems || []).find(x => x.id === id);
      card.addEventListener('click', () => { location.hash = '#/templates/' + encodeURIComponent(id); });
      card.querySelectorAll('[data-action]').forEach(btn => {
        btn.addEventListener('click', (e) => {
          e.stopPropagation();
          e.preventDefault();
          const action = btn.dataset.action;
          if (!t) return;
          if (action === 'run') AdHocPage.open({ prefillTemplateID: t.id });
          else if (action === 'edit') this.openEditForm(app, t);
          else if (action === 'duplicate') this.duplicate(app, t);
        });
      });
    });
  },

  template(data, f) {
    const items = data.items || [];
    const total = data.total || 0;

    const headerActions = '<div style="margin-left:auto"><button type="button" class="btn btn-primary" id="templates-new">New template</button></div>';

    if (items.length === 0) {
      return `
        <div class="page-header">
          <h2>Templates</h2>
          ${headerActions}
        </div>
        ${Components.emptyState('No templates',
          'No templates yet. Click <strong>New template</strong> above or use <code class="mono">surfbot template create</code>.')}
      `;
    }

    const cards = items.map(t => {
      const toolCount = Object.keys(t.tool_config || {}).length;
      const toolNoun = toolCount === 1 ? 'tool' : 'tools';
      const kindBadge = t.is_system
        ? '<span class="badge badge-muted" title="Built-in template — seeded on first boot.">built-in</span>'
        : '<span class="badge badge-muted">custom</span>';
      const desc = (t.description || '').trim();
      const descHtml = desc
        ? `<p class="card-desc">${escapeHtml(desc)}</p>`
        : '<p class="card-desc text-muted">No description.</p>';

      return `
        <div class="template-card clickable" data-id="${escapeHtml(t.id)}">
          <div class="template-card-header">
            <span class="template-card-title">${escapeHtml(t.name || '-')}</span>
            ${kindBadge}
          </div>
          ${descHtml}
          <div class="template-card-meta">
            <span class="pill pill-muted">${toolCount} ${toolNoun}</span>
            <span class="pill pill-muted" data-used-by="${escapeHtml(t.id)}" title="Loading…">… schedules</span>
            <span class="template-card-uuid mono text-muted" title="${escapeHtml(t.id)}">${Components.truncateID(t.id)}</span>
          </div>
          <div class="template-card-actions">
            <button type="button" class="pill pill-action pill-brand" data-action="run">Run with…</button>
            <button type="button" class="pill pill-action" data-action="edit">Edit</button>
            <button type="button" class="pill pill-action" data-action="duplicate">Duplicate</button>
          </div>
        </div>
      `;
    }).join('');

    const pager = this.pagerHtml(total, f);

    return `
      <div class="page-header">
        <h2>Templates</h2>
        <p>${total} template${total === 1 ? '' : 's'}</p>
        ${headerActions}
      </div>
      <div class="template-grid">${cards}</div>
      ${pager}
    `;
  },

  pagerHtml(total, f) {
    if (total <= f.limit) return '';
    const page = Math.floor(f.offset / f.limit) + 1;
    const pages = Math.max(1, Math.ceil(total / f.limit));
    const hashFor = (offset) => `#/templates?limit=${f.limit}&offset=${offset}`;
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

  // --- Template detail ---

  async renderDetail(app, id) {
    app.innerHTML = '<div class="loading">Loading template...</div>';
    const scroller = document.querySelector('main.content');
    if (scroller) scroller.scrollTop = 0;

    try {
      const t = await API.getTemplate(id);
      let usedBy = [];
      try {
        const resp = await API.listSchedules({ template_id: id, limit: 500 });
        usedBy = resp.items || [];
      } catch (_) {
        usedBy = [];
      }
      app.innerHTML = this.detailTemplate(t, usedBy);
      this.bindDetailActions(app, t, usedBy);
    } catch (err) {
      app.innerHTML = `
        ${Components.backLink('#/templates', 'Back to templates')}
        ${Components.errorBanner(err)}
      `;
    }
  },

  bindDetailActions(app, t, usedBy) {
    const editBtn = document.getElementById('template-edit-btn');
    if (editBtn) editBtn.addEventListener('click', () => this.openEditForm(app, t));

    const delBtn = document.getElementById('template-delete-btn');
    if (delBtn) delBtn.addEventListener('click', () => this.confirmDelete(app, t, usedBy));
  },

  detailTemplate(t, usedBy) {
    const mw = t.maintenance_window
      ? `<pre class="json-block">${escapeHtml(JSON.stringify(t.maintenance_window, null, 2))}</pre>`
      : '<span class="text-muted">none</span>';

    const usedByCard = usedBy.length === 0
      ? `<span class="text-muted">No schedules are using this template.</span>`
      : `<ul>${usedBy.map(s => `
          <li><a href="#/schedules/${encodeURIComponent(s.id)}" class="mono">${escapeHtml(s.name || s.id)}</a>
              <span class="text-muted"> — target <span class="mono">${Components.truncateID(s.target_id)}</span>, ${Components.scheduleStatusBadge(s.status)}</span></li>
        `).join('')}</ul>`;

    return `
      ${Components.backLink('#/templates', 'Back to templates')}
      <div class="scan-detail-stack">
        <div class="detail-panel">
          <div class="detail-header">
            <h2>${escapeHtml(t.name)}</h2>
            ${t.is_system ? '<span class="badge badge-muted" title="Built-in template — seeded on first boot. Edits are allowed but the template cannot be deleted.">built-in</span>' : ''}
            <div style="margin-left:auto;display:flex;gap:8px">
              <button type="button" class="btn btn-ghost" id="template-edit-btn">Edit</button>
              ${t.is_system
                ? '<button type="button" class="btn btn-danger" disabled title="Built-in templates cannot be deleted.">Delete</button>'
                : '<button type="button" class="btn btn-danger" id="template-delete-btn">Delete</button>'}
            </div>
          </div>
          <div id="template-action-error" style="margin-top:8px"></div>
          <div class="detail-grid" style="margin-top:12px">
            <span class="detail-label">ID</span><span class="detail-value mono">${escapeHtml(t.id)}</span>
            <span class="detail-label">Description</span><span class="detail-value">${escapeHtml(t.description || '-')}</span>
            <span class="detail-label">Timezone</span><span class="detail-value mono">${escapeHtml(t.timezone || '-')}</span>
            <span class="detail-label">RRULE</span><span class="detail-value mono" style="word-break:break-all">${escapeHtml(t.rrule || '-')}</span>
            <span class="detail-label">Created</span><span class="detail-value">${Components.formatDate(t.created_at)}</span>
            <span class="detail-label">Updated</span><span class="detail-value">${Components.formatDate(t.updated_at)}</span>
          </div>
        </div>

        <div class="card">
          <div class="card-label">Used by <span class="text-muted" style="font-weight:400;text-transform:none">(${usedBy.length})</span></div>
          <div style="margin-top:8px">${usedByCard}</div>
        </div>

        <div class="card">
          <div class="card-label">Maintenance window</div>
          <div style="margin-top:8px">${mw}</div>
        </div>

        <div class="card">
          <div class="card-label">Tool config</div>
          <pre class="json-block">${escapeHtml(JSON.stringify(t.tool_config || {}, null, 2))}</pre>
        </div>
      </div>
    `;
  },

  // --- SPEC-SCHED1.4b: template create / edit / delete ---
  //
  // Tool config is a `<textarea>` holding pretty-printed JSON. The UI
  // validates only the outer shape (top-level keys in the known tool
  // set + parseable JSON); per-tool parameter validation happens
  // server-side and comes back as field errors scoped to
  // `tool_config.<name>`. Delete surfaces the 409 /problems/template-
  // in-use payload as a follow-up cascade-delete confirm.

  openCreateForm(app) {
    this.openForm({
      mode: 'create',
      onSaved: () => TemplatesPage.render(app, {}),
    });
  },

  // duplicate seeds the create form with a copy of the source template.
  // is_system is intentionally dropped — built-in is a server-managed
  // marker, not part of the user-editable shape.
  duplicate(app, t) {
    const copy = {
      name: `${t.name || 'template'} (copy)`,
      description: t.description || '',
      rrule: t.rrule || '',
      timezone: t.timezone || 'UTC',
      tool_config: t.tool_config || {},
      maintenance_window: t.maintenance_window || null,
    };
    this.openForm({
      mode: 'create',
      template: copy,
      onSaved: () => TemplatesPage.render(app, {}),
    });
  },

  openEditForm(app, t) {
    this.openForm({
      mode: 'edit',
      template: t,
      onSaved: () => TemplatesPage.renderDetail(app, t.id),
    });
  },

  openForm({ mode, template, onSaved }) {
    const t = template || {};
    const toolConfigJson = JSON.stringify(t.tool_config || {}, null, 2);
    // Fields the form doesn't surface (e.g. maintenance_window) but we
    // still want to forward when the form was seeded by Duplicate.
    const carryover = {};
    if (mode === 'create' && t.maintenance_window) {
      carryover.maintenance_window = t.maintenance_window;
    }
    const tzDefault = t.timezone || (mode === 'create' ? browserTimezone() : 'UTC');
    const body = `
      <form id="template-form" novalidate>
        <div class="field-error" data-field-error="__general__" style="display:none;margin-bottom:12px;padding:8px 12px;background:var(--sev-critical-bg);border-radius:var(--radius)"></div>
        ${Components.formInput({ label: 'Name', name: 'name', value: t.name, required: true })}
        ${Components.formInput({ label: 'Description', name: 'description', value: t.description || '' })}
        ${Components.rruleBuilder({ label: 'Schedule', name: 'rrule', value: t.rrule, required: true })}
        ${Components.formInput({ label: 'Timezone', name: 'timezone', value: tzDefault,
          placeholder: 'Europe/Madrid',
          help: 'IANA timezone identifier (e.g. UTC, Europe/Madrid).' })}
        ${Components.formTextarea({
          label: 'Tool config (JSON)', name: 'tool_config',
          value: t.tool_config ? toolConfigJson : '{}',
          rows: 10, monospace: true,
          help: 'Top-level keys must be known tool names (nuclei, naabu, httpx, subfinder, dnsx). Per-tool params are validated on the server.',
        })}
      </form>
    `;

    const m = Components.modal({
      title: mode === 'create' ? 'New template' : 'Edit template',
      body,
      size: 'lg',
      primaryAction: {
        label: mode === 'create' ? 'Create' : 'Save',
        onClick: async ({ close }) => {
          const form = document.getElementById('template-form');
          const data = readTemplateForm(form);
          if (!data) return;
          Object.assign(data, carryover);
          try {
            if (mode === 'create') await API.createTemplate(data);
            else await API.updateTemplate(t.id, data);
            if (onSaved) onSaved();
            close('saved');
          } catch (err) {
            showTemplateFormError(form, err);
          }
        },
      },
      secondaryAction: { label: 'Cancel' },
    });
    Components.rruleBuilderAttach(m.root.querySelector('#template-form'));
  },

  async confirmDelete(app, t, usedBy) {
    const box = document.getElementById('template-action-error');
    const dependents = usedBy || [];

    const initial = await Components.confirmDialog({
      title: 'Delete template?',
      message: dependents.length
        ? `Template "${t.name}" is referenced by ${dependents.length} schedule(s). Delete the template first? Schedules that still reference it will be listed next.`
        : `Delete template "${t.name}"? This cannot be undone.`,
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!initial) return;

    try {
      await API.deleteTemplate(t.id);
      location.hash = '#/templates';
      TemplatesPage.render(app, {});
      return;
    } catch (err) {
      if (err && err.code === 'TEMPLATE_IN_USE') {
        const ids = (err.fieldErrors || []).map(fe => fe.message);
        const cascade = await Components.confirmDialog({
          title: 'Cascade delete?',
          message: `Template is still referenced by ${ids.length} schedule(s). Delete template AND its schedules?`,
          confirmLabel: 'Delete template + schedules',
          danger: true,
        });
        if (!cascade) return;
        try {
          await API.deleteTemplate(t.id, { force: true });
          location.hash = '#/templates';
          TemplatesPage.render(app, {});
          return;
        } catch (err2) {
          if (box) box.innerHTML = Components.errorBanner(err2);
          return;
        }
      }
      if (box) box.innerHTML = Components.errorBanner(err);
    }
  },
};

const KNOWN_TOOLS = new Set(['nuclei', 'naabu', 'httpx', 'subfinder', 'dnsx']);

// readTemplateForm serializes the template form into the API shape.
// Returns null after surfacing client-side JSON/tool errors.
function readTemplateForm(form) {
  if (!form) return null;
  const fd = new FormData(form);
  const out = {
    name: (fd.get('name') || '').toString().trim(),
    description: (fd.get('description') || '').toString(),
    rrule: (fd.get('rrule') || '').toString().trim(),
    timezone: (fd.get('timezone') || '').toString().trim() || 'UTC',
  };
  const tcRaw = (fd.get('tool_config') || '').toString().trim();
  if (tcRaw) {
    let tc;
    try {
      tc = JSON.parse(tcRaw);
    } catch (err) {
      Components.applyFieldErrors(form, [{ field: 'tool_config', message: 'Invalid JSON: ' + err.message }]);
      return null;
    }
    if (tc === null || typeof tc !== 'object' || Array.isArray(tc)) {
      Components.applyFieldErrors(form, [{ field: 'tool_config', message: 'Must be a JSON object' }]);
      return null;
    }
    const unknown = Object.keys(tc).filter(k => !KNOWN_TOOLS.has(k));
    if (unknown.length) {
      Components.applyFieldErrors(form, unknown.map(k => ({
        field: 'tool_config.' + k,
        message: 'Unknown tool (allowed: ' + Array.from(KNOWN_TOOLS).join(', ') + ')',
      })));
      return null;
    }
    out.tool_config = tc;
  } else {
    out.tool_config = {};
  }
  return out;
}

function showTemplateFormError(form, err) {
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
