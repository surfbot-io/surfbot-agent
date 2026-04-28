// Targets page (list + detail).
//
// PR5 (#38) adds:
//   - type tabs (All / Domain / IPv4 / IPv6 / CIDR / etc.) as pills
//   - live name search (client-side over target.value)
//   - bulk select with two actions: Run scan, Delete (with confirm)
//   - row click still navigates to detail; checkbox click stops propagation
const TargetsPage = {
  // Persisted across re-renders so paginating or refetching doesn't drop
  // the operator's selection.
  _selectedIds: new Set(),

  // Last-fetched data — needed by the bulk handlers + filter handlers
  // without a re-fetch (no pagination on this page yet).
  _allTargets: [],

  // Type tab + search filter state. Persisted on every change.
  _typeFilter: '',
  _searchTerm: '',

  STORAGE_KEY: 'surfbot_targets_filters',
  STORAGE_VERSION: 1,

  async render(app, params) {
    if (params && params.id) {
      return this.renderDetail(app, params.id);
    }

    app.innerHTML = '<div class="loading">Loading targets...</div>';

    try {
      const data = await API.targets();
      this._allTargets = data.targets || [];
      this._restoreFilters();
      app.innerHTML = this.template();
      this.bindEvents(app);
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load targets: ' + err.message);
    }
  },

  template() {
    const targets = this._allTargets || [];

    const addForm = `
      <div class="add-form">
        <form id="add-target-form" class="inline-form">
          <input type="text" id="target-value" class="form-input" placeholder="domain, IP, or CIDR (e.g. example.com)" required>
          <select id="target-scope" class="filter-select">
            <option value="external">external</option>
            <option value="internal">internal</option>
            <option value="both">both</option>
          </select>
          <button type="submit" class="btn btn-primary">Add Target</button>
        </form>
        <div id="add-target-error" class="form-error" style="display:none"></div>
      </div>
    `;

    if (targets.length === 0) {
      return `
        <div class="page-header"><h2>Targets</h2></div>
        ${addForm}
        ${Components.emptyState('No targets', 'Add a target above to start scanning.')}
      `;
    }

    const filtered = this._filteredTargets();
    const typeTabs = this._typeTabsHtml(targets);
    const searchInput = `<input type="text" class="form-input tgt-search" id="tgt-search-input" placeholder="Filter by name…" value="${escapeHtml(this._searchTerm)}" aria-label="Filter targets by name" />`;

    const rows = filtered.map(t => {
      const sel = this._selectedIds.has(t.id);
      const selCls = sel ? ' tgt-row-selected' : '';
      return `
        <tr class="tgt-row clickable${selCls}" data-target-id="${escapeHtml(t.id)}" onclick="TargetsPage.handleRowClick(event, '${t.id}')">
          <td class="fnd-cb-col" onclick="event.stopPropagation()">
            <input type="checkbox" class="tgt-checkbox" value="${escapeHtml(t.id)}" ${sel ? 'checked' : ''}
              aria-label="Select target ${escapeHtml(t.value)}"
              onclick="event.stopPropagation(); TargetsPage.onCheckboxChange(this)" />
          </td>
          <td class="mono">${escapeHtml(t.value)}</td>
          <td><span class="badge badge-info">${escapeHtml(t.type || '')}</span></td>
          <td>${escapeHtml(t.scope || '')}</td>
          <td>${t.finding_count}</td>
          <td>${t.asset_count}</td>
          <td>${t.scan_count}</td>
          <td class="text-muted">${t.last_scan_at ? Components.timeAgo(t.last_scan_at) : 'never'}</td>
          <td>${t.enabled ? '<span style="color:var(--success)">active</span>' : '<span class="text-muted">disabled</span>'}</td>
          <td class="actions-cell" onclick="event.stopPropagation()">
            <button class="btn btn-sm btn-accent" onclick="TargetsPage.scanTarget('${t.id}', '${escapeHtml(t.value)}')" title="Scan">
              <svg width="14" height="14" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M4 2a1 1 0 011 1v2.101a7.002 7.002 0 0111.601 2.566 1 1 0 11-1.885.666A5.002 5.002 0 005.999 7H9a1 1 0 010 2H4a1 1 0 01-1-1V3a1 1 0 011-1zm.008 9.057a1 1 0 011.276.61A5.002 5.002 0 0014.001 13H11a1 1 0 110-2h5a1 1 0 011 1v5a1 1 0 11-2 0v-2.101a7.002 7.002 0 01-11.601-2.566 1 1 0 01.61-1.276z" clip-rule="evenodd"/></svg>
            </button>
            <button class="btn btn-sm btn-ghost" onclick="TargetsPage.viewFindings('${t.id}')" title="Findings">
              <svg width="14" height="14" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M8.257 3.099c.765-1.36 2.722-1.36 3.486 0l5.58 9.92c.75 1.334-.213 2.98-1.742 2.98H4.42c-1.53 0-2.493-1.646-1.743-2.98l5.58-9.92zM11 13a1 1 0 11-2 0 1 1 0 012 0zm-1-8a1 1 0 00-1 1v3a1 1 0 002 0V6a1 1 0 00-1-1z" clip-rule="evenodd"/></svg>
            </button>
            <button class="btn btn-sm btn-danger" onclick="TargetsPage.deleteTarget('${t.id}', '${escapeHtml(t.value)}')" title="Remove">
              <svg width="14" height="14" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M9 2a1 1 0 00-.894.553L7.382 4H4a1 1 0 000 2v10a2 2 0 002 2h8a2 2 0 002-2V6a1 1 0 100-2h-3.382l-.724-1.447A1 1 0 0011 2H9zM7 8a1 1 0 012 0v6a1 1 0 11-2 0V8zm5-1a1 1 0 00-1 1v6a1 1 0 102 0V8a1 1 0 00-1-1z" clip-rule="evenodd"/></svg>
            </button>
          </td>
        </tr>
      `;
    }).join('');

    const tableHtml = filtered.length > 0
      ? `<div class="table-container"><table>
          <thead><tr>
            <th class="fnd-cb-col"><input type="checkbox" id="targets-select-all" aria-label="Select all visible targets" /></th>
            <th scope="col">Target</th>
            <th scope="col">Type</th>
            <th scope="col">Scope</th>
            <th scope="col">Findings</th>
            <th scope="col">Assets</th>
            <th scope="col">Scans</th>
            <th scope="col">Last Scan</th>
            <th scope="col">Status</th>
            <th scope="col" aria-label="actions"></th>
          </tr></thead>
          <tbody>${rows}</tbody>
        </table></div>`
      : `<div class="table-container"><table>
          <thead><tr><th class="fnd-cb-col"></th><th>Target</th></tr></thead>
          <tbody><tr><td colspan="2" class="empty-state">No targets match the current filter.</td></tr></tbody>
        </table></div>`;

    return `
      <div class="page-header">
        <h2>Targets</h2>
        <p>${targets.length} configured target${targets.length !== 1 ? 's' : ''}${this._typeFilter || this._searchTerm ? ` · ${filtered.length} shown` : ''}</p>
      </div>
      ${addForm}
      ${typeTabs}
      <div class="filter-strip" role="group" aria-label="Targets filter">
        ${searchInput}
        <span class="filter-strip-spacer"></span>
      </div>
      <div data-bulk-slot>${Components.bulkBar({ count: this._selectedIds.size, actions: this._bulkActionDescriptors() })}</div>
      ${tableHtml}
    `;
  },

  // _typeTabsHtml builds the pill-strip with one tab per distinct
  // target.type plus an "All" leader. Counts come from the full list.
  _typeTabsHtml(allTargets) {
    const counts = {};
    allTargets.forEach(t => {
      const k = (t.type || '').trim() || 'unknown';
      counts[k] = (counts[k] || 0) + 1;
    });
    const types = Object.keys(counts).sort();
    const tabs = [{ key: '', label: 'All', count: allTargets.length }]
      .concat(types.map(t => ({ key: t, label: this._humanType(t), count: counts[t] })));
    const html = tabs.map(t => {
      const active = t.key === this._typeFilter;
      const cls = 'sev-tab' + (active ? ' sev-tab-active' : '');
      return `<button type="button" class="${cls}" role="tab" aria-selected="${active}" data-tgt-type="${escapeHtml(t.key)}">
        ${escapeHtml(t.label)}<span class="sev-tab-count">${t.count}</span>
      </button>`;
    }).join('');
    return `<div class="sev-tabs" role="tablist" aria-label="Filter targets by type">${html}</div>`;
  },

  _humanType(t) {
    const map = { domain: 'Domain', ipv4: 'IPv4', ipv6: 'IPv6', cidr: 'CIDR', external: 'External', internal: 'Internal' };
    return map[t] || t.charAt(0).toUpperCase() + t.slice(1);
  },

  _filteredTargets() {
    const all = this._allTargets || [];
    const term = (this._searchTerm || '').toLowerCase();
    return all.filter(t => {
      if (this._typeFilter && (t.type || '') !== this._typeFilter) return false;
      if (term && !(t.value || '').toLowerCase().includes(term)) return false;
      return true;
    });
  },

  _restoreFilters() {
    try {
      const raw = localStorage.getItem(this.STORAGE_KEY);
      if (!raw) return;
      const parsed = JSON.parse(raw);
      if (!parsed || parsed.version !== this.STORAGE_VERSION) return;
      this._typeFilter = parsed.type || '';
      this._searchTerm = parsed.search || '';
    } catch (_) {}
  },

  _persistFilters() {
    try {
      localStorage.setItem(this.STORAGE_KEY, JSON.stringify({
        version: this.STORAGE_VERSION,
        type: this._typeFilter,
        search: this._searchTerm,
        savedAt: new Date().toISOString(),
      }));
    } catch (_) {}
  },

  bindEvents(app) {
    const form = document.getElementById('add-target-form');
    if (form) {
      form.addEventListener('submit', async (e) => {
        e.preventDefault();
        const value = document.getElementById('target-value').value.trim();
        const scope = document.getElementById('target-scope').value;
        const errorEl = document.getElementById('add-target-error');
        const btn = form.querySelector('button[type="submit"]');
        if (!value) return;
        btn.disabled = true;
        btn.textContent = 'Adding...';
        errorEl.style.display = 'none';
        try {
          await API.createTarget(value, '', scope);
          TargetsPage.render(app);
        } catch (err) {
          errorEl.textContent = err.message;
          errorEl.style.display = 'block';
          btn.disabled = false;
          btn.textContent = 'Add Target';
        }
      });
    }

    // Type-tab clicks. Filter change resets selection because the
    // visible row set is changing under the operator.
    app.querySelectorAll('[data-tgt-type]').forEach(btn => {
      btn.addEventListener('click', () => {
        this._typeFilter = btn.getAttribute('data-tgt-type') || '';
        this._selectedIds.clear();
        this._persistFilters();
        app.innerHTML = this.template();
        this.bindEvents(app);
      });
    });

    // Live search.
    const search = document.getElementById('tgt-search-input');
    if (search) {
      search.addEventListener('input', () => {
        this._searchTerm = search.value;
        this._selectedIds.clear();
        this._persistFilters();
        // Re-render the table only — keep input focus.
        const cur = document.activeElement === search ? search.selectionStart : null;
        app.innerHTML = this.template();
        this.bindEvents(app);
        const next = document.getElementById('tgt-search-input');
        if (next) {
          next.focus();
          if (cur != null) next.setSelectionRange(cur, cur);
        }
      });
    }

    // Header select-all.
    const selAll = document.getElementById('targets-select-all');
    if (selAll) {
      this._syncSelectAllState(selAll);
      selAll.addEventListener('click', () => this.toggleAll(selAll.checked));
    }

    this._wireBulkBarActions(app);
  },

  // --- Bulk select & actions --------------------------------------

  _bulkActionDescriptors() {
    return [
      { label: 'Run scan', primary: true, onClick: () => this.bulkRunScan() },
      { label: 'Delete',   danger: true,  onClick: () => this.bulkDelete() },
      { label: 'Clear',    onClick: () => this.clearSelection() },
    ];
  },

  _wireBulkBarActions(app) {
    const bar = app.querySelector('[data-bulk-slot] .bulk-bar');
    if (!bar) return;
    const descriptors = this._bulkActionDescriptors();
    bar.querySelectorAll('[data-bulk-action]').forEach(btn => {
      const i = parseInt(btn.getAttribute('data-bulk-action'), 10);
      const a = descriptors[i];
      if (!a || typeof a.onClick !== 'function') return;
      btn.addEventListener('click', () => a.onClick());
    });
  },

  onCheckboxChange(input) {
    const id = input.value;
    if (input.checked) this._selectedIds.add(id);
    else this._selectedIds.delete(id);
    this._refreshBulkBar();
    const row = document.querySelector(`tr.tgt-row[data-target-id="${cssAttrEscape(id)}"]`);
    if (row) row.classList.toggle('tgt-row-selected', input.checked);
    const selAll = document.getElementById('targets-select-all');
    if (selAll) this._syncSelectAllState(selAll);
  },

  toggleAll(checked) {
    document.querySelectorAll('.tgt-checkbox').forEach(cb => {
      cb.checked = checked;
      const id = cb.value;
      if (checked) this._selectedIds.add(id);
      else this._selectedIds.delete(id);
      const row = document.querySelector(`tr.tgt-row[data-target-id="${cssAttrEscape(id)}"]`);
      if (row) row.classList.toggle('tgt-row-selected', checked);
    });
    this._refreshBulkBar();
  },

  clearSelection() {
    this._selectedIds.clear();
    document.querySelectorAll('.tgt-checkbox').forEach(cb => { cb.checked = false; });
    document.querySelectorAll('.tgt-row').forEach(r => r.classList.remove('tgt-row-selected'));
    this._refreshBulkBar();
    const selAll = document.getElementById('targets-select-all');
    if (selAll) this._syncSelectAllState(selAll);
  },

  _syncSelectAllState(el) {
    const visible = Array.from(document.querySelectorAll('.tgt-checkbox'));
    if (visible.length === 0) { el.checked = false; el.indeterminate = false; return; }
    const checkedCount = visible.filter(cb => cb.checked).length;
    if (checkedCount === 0) { el.checked = false; el.indeterminate = false; }
    else if (checkedCount === visible.length) { el.checked = true; el.indeterminate = false; }
    else { el.checked = false; el.indeterminate = true; }
  },

  _refreshBulkBar() {
    const slot = document.querySelector('[data-bulk-slot]');
    if (!slot) return;
    slot.innerHTML = Components.bulkBar({
      count: this._selectedIds.size,
      actions: this._bulkActionDescriptors(),
    });
    this._wireBulkBarActions(document);
  },

  async bulkRunScan() {
    const ids = Array.from(this._selectedIds);
    if (ids.length === 0) return;
    const ok = await Components.confirmDialog({
      title: 'Start ' + ids.length + ' scan' + (ids.length !== 1 ? 's' : '') + '?',
      message: 'A full scan will be queued for each selected target.',
      confirmLabel: 'Start scans',
    });
    if (!ok) return;
    const results = await Promise.allSettled(ids.map(id => API.startScan(id, 'full')));
    const failed = results.filter(r => r.status === 'rejected').length;
    const ok2 = results.length - failed;
    if (failed === 0) {
      Components.toast.success(`${ok2} scan${ok2 !== 1 ? 's' : ''} started`);
    } else {
      Components.toast.error(`${ok2}/${results.length} started · ${failed} failed`);
    }
    this.clearSelection();
  },

  async bulkDelete() {
    const ids = Array.from(this._selectedIds);
    if (ids.length === 0) return;
    const ok = await Components.confirmDialog({
      title: 'Delete ' + ids.length + ' target' + (ids.length !== 1 ? 's' : '') + '?',
      message: 'This removes their associated assets and findings. Cannot be undone.',
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!ok) return;
    const results = await Promise.allSettled(ids.map(id => API.deleteTarget(id)));
    const failed = results.filter(r => r.status === 'rejected').length;
    const ok2 = results.length - failed;
    if (failed === 0) {
      Components.toast.success(`${ok2} target${ok2 !== 1 ? 's' : ''} deleted`);
    } else {
      Components.toast.error(`${ok2}/${results.length} deleted · ${failed} failed`);
    }
    this._selectedIds.clear();
    TargetsPage.render(document.getElementById('app'));
  },

  // handleRowClick navigates to the detail view but ignores clicks that
  // originated on the checkbox column or any per-row action button.
  handleRowClick(evt, id) {
    const t = evt.target;
    if (t && t.closest && t.closest('input, button, a, .actions-cell, .fnd-cb-col')) return;
    location.hash = '#/targets/' + id;
  },

  // --- Target Detail View ---

  async renderDetail(app, id) {
    app.innerHTML = '<div class="loading">Loading target...</div>';

    const scroller = document.querySelector('main.content');
    if (scroller) scroller.scrollTop = 0;

    try {
      const data = await API.target(id);
      this.renderDetailContent(app, data);
    } catch (err) {
      app.innerHTML = Components.emptyState('Not Found', 'Target not found.');
    }
  },

  renderDetailContent(app, data) {
    const t = data.target;
    const latest = data.latest_scan;
    const scans = data.recent_scans || [];
    const findings = data.recent_findings || [];

    const typeBadge = `<span class="badge badge-info">${escapeHtml(t.type || 'domain')}</span>`;
    const scopeBadge = `<span class="badge badge-muted">${escapeHtml(t.scope || 'external')}</span>`;
    const enabledBadge = t.enabled
      ? '<span style="color:var(--success)">active</span>'
      : '<span class="text-muted">disabled</span>';

    const metaParts = [
      enabledBadge,
      `<span class="text-muted">${data.scan_count} scan${data.scan_count === 1 ? '' : 's'}</span>`,
      `<span class="text-muted">${data.asset_count} asset${data.asset_count === 1 ? '' : 's'}</span>`,
      `<span class="text-muted">${data.finding_count} finding${data.finding_count === 1 ? '' : 's'}</span>`,
    ];
    if (t.last_scan_at) {
      metaParts.push(`<span class="text-muted">last scan ${Components.timeAgo(t.last_scan_at)}</span>`);
    } else {
      metaParts.push('<span class="text-muted">never scanned</span>');
    }
    const metaRow = `<div class="detail-meta">${metaParts.join(' <span class="detail-meta-sep">\u00B7</span> ')}</div>`;

    const stateCard = latest ? this.renderTargetStateCard(latest) : '';
    const findingsCard = this.renderRecentFindings(findings);
    const scansCard = this.renderRecentScans(scans);

    app.innerHTML = `
      ${Components.backLink('#/targets', 'Back to targets')}
      <div class="scan-detail-stack">
        <div class="detail-panel">
          <div class="detail-header">
            ${typeBadge}
            <h2 class="mono">${escapeHtml(t.value)}</h2>
            ${scopeBadge}
            <div style="margin-left:auto">
              <button class="btn btn-sm btn-accent" id="target-scan-btn">Scan now</button>
              <button class="btn btn-sm btn-ghost" id="target-findings-btn">Findings</button>
              <button class="btn btn-sm btn-ghost" id="target-assets-btn">Assets</button>
              <button class="btn btn-sm btn-danger" id="target-delete-btn">Delete</button>
            </div>
          </div>
          ${metaRow}
        </div>

        ${stateCard}
        <div id="target-schedules-slot"></div>
        ${findingsCard}
        ${scansCard}
      </div>
    `;

    // Wire action buttons.
    // SPEC-SCHED1.4c R4: the detail-page "Scan now" button opens the
    // 1.4b ad-hoc dispatcher modal with target_id pre-filled (readonly
    // by default; unlockable via the modal's "Edit target" link). The
    // list-page row's legacy scanTarget() path still uses the synchronous
    // /api/v1/scans endpoint — targets list overhaul is separate work.
    const scanBtn = document.getElementById('target-scan-btn');
    if (scanBtn) scanBtn.addEventListener('click', () => {
      if (typeof AdHocPage !== 'undefined' && AdHocPage.open) {
        AdHocPage.open({ prefillTargetID: t.id, lockTargetID: true });
      } else {
        this.scanTarget(t.id, t.value);
      }
    });
    const fBtn = document.getElementById('target-findings-btn');
    if (fBtn) fBtn.addEventListener('click', () => this.viewFindings(t.id));
    const aBtn = document.getElementById('target-assets-btn');
    if (aBtn) aBtn.addEventListener('click', () => { location.hash = '#/assets?target_id=' + t.id; });
    const dBtn = document.getElementById('target-delete-btn');
    if (dBtn) dBtn.addEventListener('click', () => this.deleteTarget(t.id, t.value));

    // SPEC-SCHED1.4c R3: schedules-for-this-target section.
    this.renderTargetSchedulesSection(t);
  },

  async renderTargetSchedulesSection(t) {
    const slot = document.getElementById('target-schedules-slot');
    if (!slot) return;
    slot.innerHTML = '<div class="card"><div class="card-label">Schedules</div><div class="text-muted" style="padding:8px 0">Loading…</div></div>';
    try {
      const resp = await API.listSchedules({ target_id: t.id, limit: 100 });
      const items = (resp && resp.items) || [];
      slot.innerHTML = this.targetSchedulesCard(t, items);
      this.bindTargetSchedulesActions(t);
    } catch (err) {
      slot.innerHTML = `<div class="card"><div class="card-label">Schedules</div>${Components.errorBanner(err)}</div>`;
    }
  },

  targetSchedulesCard(t, items) {
    if (items.length === 0) {
      return `<div class="card">
        <div class="card-label">Schedules</div>
        <div style="margin-top:8px">
          <span class="text-muted">No schedules for this target yet.</span>
          <div style="margin-top:8px;display:flex;gap:8px">
            <button type="button" class="btn btn-sm btn-accent" id="target-schedules-adhoc">Run scan now</button>
            <button type="button" class="btn btn-sm btn-ghost" id="target-schedules-create">Create schedule</button>
          </div>
        </div>
      </div>`;
    }
    const rows = items.map(s => `
      <tr class="clickable" onclick="location.hash='#/schedules/${encodeURIComponent(s.id)}'">
        <td class="mono">${Components.truncateID(s.id)}</td>
        <td class="mono">${s.template_id ? Components.truncateID(s.template_id) : '<span class="text-muted">—</span>'}</td>
        <td>${Components.scheduleStatusBadge(s.status)}</td>
        <td>${Components.rruleCompact(s.rrule)}</td>
        <td class="text-muted">${Components.nextRun(s.next_run_at)}</td>
      </tr>
    `).join('');
    return `<div class="card">
      <div class="card-label">Schedules <span class="text-muted" style="font-weight:400;text-transform:none">(${items.length})</span>
        <button type="button" class="btn btn-sm btn-ghost" id="target-schedules-create" style="margin-left:12px">+ New</button>
      </div>
      <div class="table-container" style="border:none;margin:8px 0 0">
        <table>
          <thead><tr>
            <th scope="col">ID</th>
            <th scope="col">Template</th>
            <th scope="col">Status</th>
            <th scope="col">RRULE</th>
            <th scope="col">Next run</th>
          </tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
    </div>`;
  },

  bindTargetSchedulesActions(t) {
    const adhocBtn = document.getElementById('target-schedules-adhoc');
    if (adhocBtn) adhocBtn.addEventListener('click', () => {
      if (typeof AdHocPage !== 'undefined' && AdHocPage.open) AdHocPage.open({ prefillTargetID: t.id });
    });
    const createBtn = document.getElementById('target-schedules-create');
    if (createBtn) createBtn.addEventListener('click', () => {
      if (typeof SchedulesPage !== 'undefined' && SchedulesPage.openForm) {
        SchedulesPage.openForm({
          mode: 'create',
          schedule: { target_id: t.id },
          onSaved: () => TargetsPage.renderDetail(document.getElementById('app'), t.id),
        });
      }
    });
  },

  renderTargetStateCard(scan) {
    const state = scan.target_state || {};
    const assets = state.assets_by_type || {};
    const ports = state.ports_by_status || {};

    // Mirror scan-detail Target state ordering (KnownAssetTypes order, with
    // ports broken down by status). Keeps target / scan detail consistent.
    const items = [
      { label: 'Subdomains', value: assets.subdomain },
      { label: 'IPs (v4)', value: assets.ipv4 },
      { label: 'IPs (v6)', value: assets.ipv6 },
      { label: 'Ports (open)', value: ports.open },
      { label: 'Ports (filtered)', value: ports.filtered },
      { label: 'Endpoints', value: assets.url },
      { label: 'Technologies', value: assets.technology },
      { label: 'Domains', value: assets.domain },
      { label: 'Services', value: assets.service },
    ].filter(i => i.value > 0);

    const known = new Set(['subdomain', 'ipv4', 'ipv6', 'port_service', 'url', 'technology', 'domain', 'service']);
    Object.entries(assets)
      .filter(([k, v]) => !known.has(k) && v > 0)
      .sort(([a], [b]) => a.localeCompare(b))
      .forEach(([k, v]) => items.push({ label: this.titleCase(k), value: v }));

    if (state.findings_open_total > 0) {
      items.push({ label: 'Findings open', value: state.findings_open_total, divider: true });
    }

    if (items.length === 0) {
      return `<div class="card">
        <div class="card-label">Target state</div>
        <div class="detail-grid detail-grid-message" style="margin-top:8px">
          <span class="text-muted">No state recorded yet — run a scan to populate.</span>
        </div>
      </div>`;
    }

    const sevs = state.findings_open || {};
    const hasSev = Object.values(sevs).some(v => v > 0);

    const scanLink = `<a href="#/scans/${scan.id}" class="text-muted" style="font-weight:400;text-transform:none;font-size:11px;margin-left:8px">from scan ${Components.truncateID(scan.id)} \u00B7 ${Components.timeAgo(scan.finished_at || scan.created_at)}</a>`;

    return `<div class="card">
      <div class="card-label">Target state${scanLink}</div>
      <div class="detail-grid" style="margin-top:8px">
        ${items.map(i => {
          const cls = i.divider ? ' detail-divider' : '';
          return `<span class="detail-label${cls}">${escapeHtml(i.label)}</span><span class="detail-value${cls}">${i.value}</span>`;
        }).join('')}
      </div>
      ${hasSev ? Components.severityBars({
        critical: sevs.critical || 0,
        high: sevs.high || 0,
        medium: sevs.medium || 0,
        low: sevs.low || 0,
        info: sevs.info || 0,
      }) : ''}
    </div>`;
  },

  renderRecentFindings(findings) {
    if (findings.length === 0) {
      return `<div class="card">
        <div class="card-label">Recent findings</div>
        <div class="detail-grid detail-grid-message" style="margin-top:8px">
          <span class="text-muted">No findings recorded for this target.</span>
        </div>
      </div>`;
    }

    const rows = findings.map(f => {
      const assetCell = f.asset_id
        ? `<a href="#/assets/${f.asset_id}" class="mono" onclick="event.stopPropagation()">${escapeHtml(f.asset_value || '—')}</a>`
        : `<span class="mono text-muted">${escapeHtml(f.asset_value || '—')}</span>`;
      return `
        <tr class="clickable" onclick="location.hash='#/findings/${f.id}'">
          <td>${Components.severityBadge ? Components.severityBadge(f.severity) : `<span class="badge badge-${f.severity}">${f.severity}</span>`}</td>
          <td>${escapeHtml(f.title)}</td>
          <td>${assetCell}</td>
          <td>${Components.statusBadge(f.status)}</td>
          <td class="text-muted">${f.source_tool ? escapeHtml(f.source_tool) : '-'}</td>
          <td class="text-muted">${f.last_seen ? Components.timeAgo(f.last_seen) : '-'}</td>
        </tr>
      `;
    }).join('');

    return `<div class="card">
      <div class="card-label">Recent findings <span class="text-muted" style="font-weight:400;text-transform:none">(${findings.length})</span></div>
      <div class="table-container" style="border:none;margin:8px 0 0">
        <table>
          <thead><tr>
            <th scope="col">Severity</th>
            <th scope="col">Title</th>
            <th scope="col">Asset</th>
            <th scope="col">Status</th>
            <th scope="col">Tool</th>
            <th scope="col">Last seen</th>
          </tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
    </div>`;
  },

  renderRecentScans(scans) {
    if (scans.length === 0) {
      return `<div class="card">
        <div class="card-label">Recent scans</div>
        <div class="detail-grid detail-grid-message" style="margin-top:8px">
          <span class="text-muted">No scans recorded for this target yet.</span>
        </div>
      </div>`;
    }

    const rows = scans.map(s => {
      let dur = '-';
      if (s.started_at && s.finished_at) {
        dur = Components.formatDuration(Math.floor((new Date(s.finished_at) - new Date(s.started_at)) / 1000));
      }
      const findingsOpen = (s.target_state && s.target_state.findings_open_total) || 0;
      return `
        <tr class="clickable" onclick="location.hash='#/scans/${s.id}'">
          <td class="mono">${Components.truncateID(s.id)}</td>
          <td>${Components.statusBadge(s.status)}</td>
          <td>${s.type}</td>
          <td class="mono">${dur}</td>
          <td>${findingsOpen} findings</td>
          <td class="text-muted">${Components.timeAgo(s.created_at)}</td>
        </tr>
      `;
    }).join('');

    return `<div class="card">
      <div class="card-label">Recent scans <span class="text-muted" style="font-weight:400;text-transform:none">(${scans.length})</span></div>
      <div class="table-container" style="border:none;margin:8px 0 0">
        <table>
          <thead><tr>
            <th scope="col">ID</th>
            <th scope="col">Status</th>
            <th scope="col">Type</th>
            <th scope="col">Duration</th>
            <th scope="col">Results</th>
            <th scope="col">When</th>
          </tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
    </div>`;
  },

  titleCase(s) {
    if (!s) return s;
    return s.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
  },

  viewFindings(targetId) {
    location.hash = '#/findings?target_id=' + targetId;
  },

  async scanTarget(targetId, targetValue) {
    if (!confirm('Start a full scan on ' + targetValue + '?')) return;

    try {
      await API.startScan(targetId, 'full');
      location.hash = '#/scans';
    } catch (err) {
      alert('Failed to start scan: ' + err.message);
    }
  },

  async deleteTarget(id, value) {
    if (!confirm('Remove target "' + value + '" and all associated data? This cannot be undone.')) return;

    try {
      await API.deleteTarget(id);
      location.hash = '#/targets';
      TargetsPage.render(document.getElementById('app'));
    } catch (err) {
      alert('Failed to delete target: ' + err.message);
    }
  },
};
