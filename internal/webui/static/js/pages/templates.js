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
      app.innerHTML = this.template(data, { limit, offset });
    } catch (err) {
      app.innerHTML = `
        <div class="page-header"><h2>Templates</h2></div>
        ${Components.errorBanner(err)}
      `;
    }
  },

  template(data, f) {
    const items = data.items || [];
    const total = data.total || 0;

    if (items.length === 0) {
      return `
        <div class="page-header"><h2>Templates</h2></div>
        ${Components.emptyState('No templates',
          'No templates yet. Create one via CLI: <code class="mono">surfbot template create --name \'...\' --rrule \'...\'</code>')}
      `;
    }

    const rows = items.map(t => {
      const toolCount = Object.keys(t.tool_config || {}).length;
      const sysBadge = t.is_system ? '<span class="badge badge-muted">system</span>' : '';
      return `
        <tr class="clickable" onclick="location.hash='#/templates/${encodeURIComponent(t.id)}'">
          <td class="mono">${Components.truncateID(t.id)}</td>
          <td>${escapeHtml(t.name || '-')} ${sysBadge}</td>
          <td class="text-muted">${escapeHtml(Components.truncate(t.description || '', 80))}</td>
          <td>${toolCount}</td>
          <td>${Components.rruleCompact(t.rrule)}</td>
          <td class="text-muted">${Components.timeAgo(t.updated_at)}</td>
        </tr>
      `;
    }).join('');

    const pager = this.pagerHtml(total, f);

    return `
      <div class="page-header">
        <h2>Templates</h2>
        <p>${total} template${total === 1 ? '' : 's'}</p>
      </div>
      ${Components.table(
        ['ID', 'Name', 'Description', 'Tools', 'RRULE', 'Updated'],
        [rows]
      )}
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
    } catch (err) {
      app.innerHTML = `
        ${Components.backLink('#/templates', 'Back to templates')}
        ${Components.errorBanner(err)}
      `;
    }
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
            ${t.is_system ? '<span class="badge badge-muted">system</span>' : ''}
          </div>
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
};
