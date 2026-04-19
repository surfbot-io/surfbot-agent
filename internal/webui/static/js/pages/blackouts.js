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

    if (items.length === 0) {
      const hint = f.activeOnly
        ? 'No blackouts are active right now.'
        : 'No blackout windows defined. Create one via CLI: <code class="mono">surfbot blackout create --name \'...\' --rrule \'...\' --duration 3600</code>';
      return `
        <div class="page-header"><h2>Blackout windows</h2></div>
        ${filterBar}
        ${Components.emptyState('No blackouts', hint)}
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
    if (!form) return;
    form.addEventListener('submit', (e) => {
      e.preventDefault();
      const activeOnly = document.getElementById('filter-active-only').checked;
      location.hash = activeOnly ? '#/blackouts?active_only=1' : '#/blackouts';
    });
  },

  // --- Blackout detail ---

  async renderDetail(app, id) {
    app.innerHTML = '<div class="loading">Loading blackout...</div>';
    const scroller = document.querySelector('main.content');
    if (scroller) scroller.scrollTop = 0;

    try {
      const b = await API.getBlackout(id);
      app.innerHTML = this.detailTemplate(b);
    } catch (err) {
      app.innerHTML = `
        ${Components.backLink('#/blackouts', 'Back to blackouts')}
        ${Components.errorBanner(err)}
      `;
    }
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
          </div>
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

        <div class="card">
          <div class="card-label">Upcoming activations</div>
          <div style="margin-top:8px">
            <span class="text-muted">Preview unavailable in 1.4a. The
              <a href="#/schedules">/schedules/upcoming</a> endpoint returns
              blackouts within the horizon of the calendar view (1.4b).</span>
          </div>
        </div>
      </div>
    `;
  },
};
