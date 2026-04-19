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
        : 'No schedules yet. Create one via CLI: <code class="mono">surfbot schedule create --target &lt;id&gt; --template &lt;id&gt; --rrule \'...\' --dtstart \'...\'</code>';
      return `
        <div class="page-header"><h2>Schedules</h2></div>
        ${filterBar}
        ${Components.emptyState('No schedules', hint)}
      `;
    }

    const rows = items.map(s => `
      <tr class="clickable" onclick="location.hash='#/schedules/${encodeURIComponent(s.id)}'">
        <td class="mono">${Components.truncateID(s.id)}</td>
        <td>${escapeHtml(s.name || '-')}</td>
        <td class="mono">${Components.truncateID(s.target_id)}</td>
        <td class="mono">${s.template_id ? Components.truncateID(s.template_id) : '<span class="text-muted">—</span>'}</td>
        <td>${Components.scheduleStatusBadge(s.status)}</td>
        <td>${Components.rruleCompact(s.rrule)}</td>
        <td class="text-muted">${Components.nextRun(s.next_run_at)}</td>
      </tr>
    `).join('');

    const pager = this.pagerHtml(total, f);

    return `
      <div class="page-header">
        <h2>Schedules</h2>
        <p>${total} schedule${total === 1 ? '' : 's'}</p>
      </div>
      ${filterBar}
      ${Components.table(
        ['ID', 'Name', 'Target', 'Template', 'Status', 'RRULE', 'Next run'],
        [rows]
      )}
      ${pager}
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
    if (!form) return;
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
    } catch (err) {
      app.innerHTML = `
        ${Components.backLink('#/schedules', 'Back to schedules')}
        ${Components.errorBanner(err)}
      `;
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
