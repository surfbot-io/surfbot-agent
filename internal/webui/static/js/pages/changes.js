// PR8 #41 — Changes page. Three-column attack-surface diff:
//
//   Nuevos (status=new)         — brand tone,    + prefix
//   Desaparecidos (=disappeared) — sev-critical, − prefix, line-through
//   Volvieron (=returned)        — status-ack,   no prefix
//
// P0 fetches three /assets queries in parallel filtered by status. The
// time-windowed `Range: 7d` and `Export diff` actions are visual-only
// placeholders awaiting the P1 /asset-changes endpoint.
//
// On render the page also pushes the disappeared count to the sidebar
// "Changes" nav badge — the badge tracks attack-surface loss, which the
// wireframe surfaces as the most actionable signal.
const ChangesPage = {
  STORAGE_KEY_ACTIVE_TARGET: 'surfbot_changes_active_target',
  TOP_N: 10,

  _targets: [],
  _activeTargetID: '',
  _data: { newAssets: [], disappeared: [], returned: [] },

  async render(app, params) {
    app.innerHTML = '<div class="loading">Loading changes...</div>';

    try {
      const targetsResp = await API.targets().catch(() => ({ targets: [] }));
      this._targets = (targetsResp && targetsResp.targets) || [];

      if (this._targets.length === 0) {
        app.innerHTML = `
          ${this._headerHtml()}
          ${Components.emptyState(
            'No targets configured',
            'Add a target before tracking changes.',
          )}
          <div style="text-align:center;margin-top:12px">
            <a href="#/targets" class="btn btn-primary">Add target</a>
          </div>
        `;
        return;
      }

      this._activeTargetID = this._resolveActiveTarget(params || {});
      try { localStorage.setItem(this.STORAGE_KEY_ACTIVE_TARGET, this._activeTargetID); } catch (_) {}

      const [news, gone, returned] = await Promise.all([
        API.assets({ status: 'new',         target_id: this._activeTargetID, limit: 100 }).catch(() => ({ assets: [] })),
        API.assets({ status: 'disappeared', target_id: this._activeTargetID, limit: 100 }).catch(() => ({ assets: [] })),
        API.assets({ status: 'returned',    target_id: this._activeTargetID, limit: 100 }).catch(() => ({ assets: [] })),
      ]);

      this._data = {
        newAssets:    (news     && news.assets)     || [],
        disappeared:  (gone     && gone.assets)     || [],
        returned:     (returned && returned.assets) || [],
      };

      app.innerHTML = this._template();
      this._wire(app);
      this._syncSidebarBadge(this._data.disappeared.length);
    } catch (err) {
      app.innerHTML = `
        ${this._headerHtml()}
        ${Components.errorBanner(err)}
      `;
    }
  },

  _resolveActiveTarget(params) {
    if (params.target_id && this._targets.some(t => t.id === params.target_id)) return params.target_id;
    let stored = '';
    try { stored = localStorage.getItem(this.STORAGE_KEY_ACTIVE_TARGET) || ''; } catch (_) {}
    if (stored && this._targets.some(t => t.id === stored)) return stored;
    return this._targets[0] ? this._targets[0].id : '';
  },

  _headerHtml() {
    const headerActions = `
      <div style="margin-left:auto;display:flex;gap:8px">
        <button type="button" class="btn btn-ghost" disabled
          title="Time-range filter coming with /asset-changes endpoint"
          aria-disabled="true">${Components.icon('clock', 14)} Range: 7d</button>
        <button type="button" class="btn btn-ghost" disabled
          title="Export coming with diff endpoint"
          aria-disabled="true">${Components.icon('external', 14)} Export diff</button>
      </div>
    `;
    return `
      <div class="page-header">
        <h2>Changes</h2>
        <p>Diff de attack surface · nuevos / desaparecidos / volvieron</p>
        ${headerActions}
      </div>
    `;
  },

  _targetBarHtml() {
    if (this._targets.length < 2) return '';
    const opts = this._targets.map(t =>
      `<option value="${escapeHtml(t.id)}"${t.id === this._activeTargetID ? ' selected' : ''}>${escapeHtml(t.value)}</option>`
    ).join('');
    return `<div class="assets-target-bar">
      <label class="form-label" for="changes-target-select">Target</label>
      <select id="changes-target-select" class="assets-target-select" data-changes-target aria-label="Select active target">
        ${opts}
      </select>
    </div>`;
  },

  _template() {
    const d = this._data;
    const total = d.newAssets.length + d.disappeared.length + d.returned.length;

    if (total === 0) {
      return `
        ${this._headerHtml()}
        ${this._targetBarHtml()}
        ${Components.emptyState(
          'No changes detected',
          'Run a scan to see what is new, what disappeared, and what came back.',
        )}
      `;
    }

    return `
      ${this._headerHtml()}
      ${this._targetBarHtml()}
      <div class="changes-grid">
        ${this._cardHtml({
          label: 'Nuevos',
          tone:  'new',
          prefix:'+',
          assets: d.newAssets,
          strike: false,
        })}
        ${this._cardHtml({
          label: 'Desaparecidos',
          tone:  'gone',
          prefix:'−',
          assets: d.disappeared,
          strike: true,
        })}
        ${this._cardHtml({
          label: 'Volvieron',
          tone:  'back',
          prefix:'',
          assets: d.returned,
          strike: false,
        })}
      </div>
    `;
  },

  _cardHtml({ label, tone, prefix, assets, strike }) {
    const count = assets.length;
    const top = assets.slice(0, this.TOP_N);
    const remaining = count - top.length;

    const items = top.map(a => {
      const id = escapeHtml(a.id);
      const value = escapeHtml(a.value);
      const cls = 'changes-row' + (strike ? ' changes-row-strike' : '');
      return `<li class="${cls}" data-changes-row="${id}" tabindex="0" role="button"
        aria-label="Open asset ${value}">${value}</li>`;
    }).join('');

    const tail = remaining > 0
      ? `<li class="changes-row-more">+ ${remaining} más…</li>`
      : '';

    const list = count > 0
      ? `<ul class="changes-list">${items}${tail}</ul>`
      : `<div class="changes-empty">No changes.</div>`;

    return `<div class="changes-card changes-card-${tone}">
      <div class="changes-card-header">
        <div class="changes-card-label">${escapeHtml(label)}</div>
        <span class="changes-card-count changes-count-${tone}">${prefix}${count}</span>
      </div>
      ${list}
    </div>`;
  },

  _wire(app) {
    const targetSel = app.querySelector('[data-changes-target]');
    if (targetSel) targetSel.addEventListener('change', () => {
      const id = targetSel.value;
      try { localStorage.setItem(this.STORAGE_KEY_ACTIVE_TARGET, id); } catch (_) {}
      this.render(app, { target_id: id });
    });

    app.querySelectorAll('[data-changes-row]').forEach(row => {
      const id = row.getAttribute('data-changes-row');
      const go = () => { location.hash = '#/assets?id=' + encodeURIComponent(id); };
      row.addEventListener('click', go);
      row.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          go();
        }
      });
    });
  },

  _syncSidebarBadge(count) {
    const el = document.getElementById('changes-badge');
    if (!el) return;
    if (count > 0) {
      el.textContent = String(count);
      el.style.display = '';
    } else {
      el.style.display = 'none';
      el.textContent = '';
    }
  },
};
