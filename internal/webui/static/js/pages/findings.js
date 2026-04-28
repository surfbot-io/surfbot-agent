// Findings page — UI v2 (PR5 #38).
//
// Layout above the list (raw view):
//   1. Severity tabs (pills, single-select including "All").
//   2. Filter chip strip — every active non-severity filter is a chip with
//      a ✕ button. "+ Filter" opens a popup menu to add one. "Save view"
//      is a status pill (decorative + Reset).
//   3. Bulk-bar (Components.bulkBar) — visible when selectedIds.size > 0.
//   4. Table with a leading checkbox column.
//
// Filters auto-persist to localStorage (key `surfbot_findings_filters`).
// URL hash params win over storage so deep links from dashboard chips
// stay deterministic.
//
// Slide-over (PR4 #37) is preserved verbatim — row click still opens the
// detail panel; checkbox stops propagation so it doesn't trigger.
const FindingsPage = {
  STORAGE_KEY: 'surfbot_findings_filters',
  STORAGE_VERSION: 1,

  // Selection state. Persists across pagination but resets on filter change.
  // Module-level Set so #_renderList() can read/write across re-renders.
  _selectedIds: new Set(),

  // Slide-over state (PR4).
  _slide: null,
  _lastTrigger: null,
  _abortCtrl: null,
  _statusToPill: { open: 'open', acknowledged: 'ack', resolved: 'resolved', false_positive: 'fp', ignored: 'ignored' },

  // Active "+ Filter" menu element (so we close on outside click before
  // mounting a new one).
  _filterMenu: null,

  // Cache of /overview severity counts for the tab labels. Refreshed
  // whenever the list re-renders; falls back to 0 on fetch failure.
  _sevCounts: null,

  // Targets & list data — kept on the page so submenu/bulk handlers
  // can reach the most recent fetch without threading them through every
  // helper.
  _lastFindings: [],
  _lastTargets: [],

  async render(app, params) {
    // PR5: list always uses the "raw" data shape — chips and bulk
    // operations need per-finding rows. The legacy grouped view is
    // removed in favor of the new layout. Deep link #/findings/{id}
    // still renders the list and opens the panel on top.
    const merged = this._mergeFiltersWithStorage(params);
    await this._renderList(app, merged);
    if (params && params.id) {
      this.openSlideoverFor(params.id);
    }
  },

  // Merge URL params with localStorage. URL wins so deep links
  // (`#/findings?status=open` from dashboard chips) override prior state.
  // When the URL has no filter params at all, restore from storage and
  // rewrite the hash so refreshes are stable.
  _mergeFiltersWithStorage(params) {
    const p = Object.assign({}, params || {});
    const filterKeys = ['severity', 'status', 'host', 'tool', 'target_id', 'template_id'];
    const hasURLFilter = filterKeys.some(k => p[k]);
    if (hasURLFilter) return p;

    const stored = this._loadStored();
    if (!stored) return p;
    let restored = false;
    for (const k of filterKeys) {
      if (stored[k]) { p[k] = stored[k]; restored = true; }
    }
    if (restored) {
      const q = filterKeys.filter(k => p[k]).map(k => k + '=' + encodeURIComponent(p[k]));
      if (p.page) q.push('page=' + p.page);
      const newHash = '#/findings' + (q.length ? '?' + q.join('&') : '');
      if (location.hash !== newHash) {
        history.replaceState(null, '', newHash);
      }
    }
    return p;
  },

  _loadStored() {
    try {
      const raw = localStorage.getItem(this.STORAGE_KEY);
      if (!raw) return null;
      const parsed = JSON.parse(raw);
      if (!parsed || parsed.version !== this.STORAGE_VERSION) return null;
      return parsed.filters || {};
    } catch (_) { return null; }
  },

  _saveStored(filters) {
    try {
      localStorage.setItem(this.STORAGE_KEY, JSON.stringify({
        version: this.STORAGE_VERSION,
        filters: filters,
        savedAt: new Date().toISOString(),
      }));
    } catch (_) { /* quota / private mode — silently ignore */ }
  },

  _clearStored() {
    try { localStorage.removeItem(this.STORAGE_KEY); } catch (_) {}
  },

  // _persistFromParams writes the canonical filter set to storage. Called
  // after every filter mutation so a refresh reapplies the last state.
  _persistFromParams(params) {
    const filterKeys = ['severity', 'status', 'host', 'tool', 'target_id', 'template_id'];
    const filters = {};
    for (const k of filterKeys) if (params[k]) filters[k] = params[k];
    this._saveStored(filters);
  },

  // --- List render ------------------------------------------------

  async _renderList(app, params) {
    app.innerHTML = '<div class="loading">Loading findings...</div>';
    try {
      const [data, overview, targetsResp] = await Promise.all([
        API.findings({
          severity: params.severity,
          tool: params.tool,
          status: params.status,
          target_id: params.target_id,
          host: params.host,
          template_id: params.template_id,
          page: params.page || 1,
          limit: 50,
        }),
        API.overview().catch(() => null),
        API.targets().catch(() => ({ targets: [] })),
      ]);
      this._sevCounts = (overview && overview.findings_by_severity) || {};
      this._lastFindings = data.findings || [];
      this._lastTargets = (targetsResp && targetsResp.targets) || [];
      app.innerHTML = this._listTemplate(data, params);
      this._wireListInteractions(app, params);
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load findings: ' + err.message);
    }
  },

  _listTemplate(data, params) {
    if (data.findings.length === 0 && !this._anyFilter(params)) {
      return `
        <div class="page-header"><h2>Findings</h2></div>
        ${Components.emptyState('No findings', 'No vulnerabilities detected. Run a scan to discover findings.')}
      `;
    }

    const totalPages = Math.ceil(data.total / data.limit);
    const page = data.page;

    return `
      <div class="page-header">
        <h2>Findings</h2>
        <p>${data.total} finding${data.total !== 1 ? 's' : ''}${params.severity ? ' (' + params.severity + ')' : ''}</p>
      </div>
      ${this._severityTabsHtml(params)}
      ${this._chipStripHtml(params)}
      <div data-bulk-slot>${Components.bulkBar({ count: this._selectedIds.size, actions: this._bulkActionDescriptors() })}</div>
      ${this._tableHtml(data.findings)}
      ${totalPages > 1 ? this._paginationHtml(page, totalPages, params) : ''}
    `;
  },

  _anyFilter(params) {
    return !!(params.severity || params.status || params.host || params.tool || params.target_id || params.template_id);
  },

  _severityTabsHtml(params) {
    const counts = this._sevCounts || {};
    const total = Object.values(counts).reduce((a, b) => a + (b || 0), 0);
    const levels = [
      { key: '',         label: 'All',  count: total },
      { key: 'critical', label: 'Crit', count: counts.critical || 0 },
      { key: 'high',     label: 'High', count: counts.high || 0 },
      { key: 'medium',   label: 'Med',  count: counts.medium || 0 },
      { key: 'low',      label: 'Low',  count: counts.low || 0 },
      { key: 'info',     label: 'Info', count: counts.info || 0 },
    ];
    const active = params.severity || '';
    const tabs = levels.map(l => {
      const isActive = (l.key === active);
      const cls = 'sev-tab' + (l.key ? ' sev-tab-' + l.key : ' sev-tab-all') + (isActive ? ' sev-tab-active' : '');
      return `<button type="button" class="${cls}" role="tab" aria-selected="${isActive}" data-sev="${escapeHtml(l.key)}">
        ${l.key ? '<span class="sev-tab-dot"></span>' : ''}${escapeHtml(l.label)}<span class="sev-tab-count">${l.count}</span>
      </button>`;
    }).join('');
    return `<div class="sev-tabs" role="tablist" aria-label="Filter by severity">${tabs}</div>`;
  },

  _chipStripHtml(params) {
    const chips = [];
    const labels = {
      status: { Open: 'open', Acknowledged: 'acknowledged', Resolved: 'resolved', 'False positive': 'false_positive', Ignored: 'ignored' },
    };
    if (params.status) chips.push({ key: 'status', label: 'Status: ' + this._humanStatus(params.status) });
    if (params.target_id) {
      const t = this._lastTargets.find(t => t.id === params.target_id);
      chips.push({ key: 'target_id', label: 'Target: ' + (t ? t.value : params.target_id) });
    }
    if (params.tool) chips.push({ key: 'tool', label: 'Tool: ' + params.tool });
    if (params.host) chips.push({ key: 'host', label: 'Host: ' + params.host });
    if (params.template_id) chips.push({ key: 'template_id', label: 'Template: ' + params.template_id });

    const chipMounts = chips.map((c, i) =>
      `<span class="filter-chip-mount" data-chip-idx="${i}" data-chip-key="${c.key}" data-chip-label="${escapeHtml(c.label)}"></span>`
    ).join('');

    const saveLabel = chips.length || params.severity ? 'View saved' : 'Save view';
    const saveCls = 'save-view-pill' + (chips.length || params.severity ? ' saved' : ' empty');
    const checkIcon = chips.length || params.severity ? Components.icon('check', 14) : '';
    void labels;

    return `<div class="filter-strip" role="group" aria-label="Active filters">
      <div class="filter-strip-chips" data-chip-strip>${chipMounts}</div>
      <button type="button" class="filter-add-btn" data-add-filter>
        ${Components.icon('plus', 14)}<span>Filter</span>
      </button>
      <span class="filter-strip-spacer"></span>
      <button type="button" class="${saveCls}" data-save-view>${checkIcon}<span>${saveLabel}</span></button>
    </div>`;
  },

  _tableHtml(findings) {
    if (!findings || findings.length === 0) {
      return `<div class="table-container"><table>
        <thead><tr>
          <th class="fnd-cb-col"><input type="checkbox" disabled aria-label="Select all (no findings)" /></th>
          <th>Severity</th><th>Title</th><th>Asset</th><th>Tool</th><th>Status</th><th>Last Seen</th>
        </tr></thead>
        <tbody><tr><td colspan="7" class="empty-state">No findings match the current filters.</td></tr></tbody>
      </table></div>`;
    }

    const rows = findings.map(f => {
      const sel = this._selectedIds.has(f.id);
      const selCls = sel ? ' fnd-row-selected' : '';
      return `
        <tr class="fnd-row clickable${selCls}" data-finding-id="${escapeHtml(f.id)}" onclick="FindingsPage.handleRowClick(event, '${escapeHtml(f.id)}')">
          <td class="fnd-cb-col" onclick="event.stopPropagation()">
            <input type="checkbox" class="fnd-checkbox" value="${escapeHtml(f.id)}" ${sel ? 'checked' : ''}
              aria-label="Select finding ${escapeHtml(f.title || f.id)}"
              onclick="event.stopPropagation(); FindingsPage.onCheckboxChange(this)" />
          </td>
          <td>${Components.severityPill(f.severity)}</td>
          <td class="text-truncate">${escapeHtml(f.title)}</td>
          <td class="mono text-truncate">${escapeHtml(f.asset_value || f.evidence || '')}</td>
          <td class="mono">${escapeHtml(f.source_tool)}</td>
          <td data-row-status>${Components.statusPill(this._statusToPill[f.status] || 'open')}</td>
          <td class="mono text-muted">${Components.timeAgo(f.last_seen)}</td>
        </tr>
      `;
    }).join('');

    return `<div class="table-container"><table>
      <thead><tr>
        <th class="fnd-cb-col">
          <input type="checkbox" id="findings-select-all" aria-label="Select all visible findings" />
        </th>
        <th scope="col">Severity</th>
        <th scope="col">Title</th>
        <th scope="col">Asset</th>
        <th scope="col">Tool</th>
        <th scope="col">Status</th>
        <th scope="col">Last Seen</th>
      </tr></thead>
      <tbody>${rows}</tbody>
    </table></div>`;
  },

  _paginationHtml(page, totalPages, params) {
    let html = '<div class="pagination">';
    if (page > 1) {
      html += `<button class="btn btn-ghost btn-sm" onclick="FindingsPage.goToPage(${page-1})">Prev</button>`;
    }
    html += `<span class="text-muted">Page ${page} of ${totalPages}</span>`;
    if (page < totalPages) {
      html += `<button class="btn btn-ghost btn-sm" onclick="FindingsPage.goToPage(${page+1})">Next</button>`;
    }
    html += '</div>';
    return html;
  },

  // --- Wiring ----------------------------------------------------

  _wireListInteractions(app, params) {
    // Severity tabs.
    app.querySelectorAll('[data-sev]').forEach(btn => {
      btn.addEventListener('click', () => {
        const sev = btn.getAttribute('data-sev') || '';
        this._mutateFilter({ severity: sev || null });
      });
    });

    // Render filter chips into their mount slots so onRemove handlers
    // are wired without delegation.
    app.querySelectorAll('.filter-chip-mount').forEach(slot => {
      const key = slot.getAttribute('data-chip-key');
      const label = slot.getAttribute('data-chip-label');
      const chip = Components.filterChip(label, () => this._mutateFilter({ [key]: null }));
      slot.replaceWith(chip);
    });

    // "+ Filter" menu.
    const addBtn = app.querySelector('[data-add-filter]');
    if (addBtn) addBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      this._openFilterMenu(addBtn, params);
    });

    // Save-view pill: opens menu with Reset filters when filters are
    // active, decorative-only when the list is unfiltered.
    const saveBtn = app.querySelector('[data-save-view]');
    if (saveBtn && !saveBtn.classList.contains('empty')) {
      saveBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        this._openSaveMenu(saveBtn);
      });
    }

    // Header select-all checkbox.
    const selAll = app.querySelector('#findings-select-all');
    if (selAll) {
      this._syncSelectAllState(selAll);
      selAll.addEventListener('click', () => this.toggleAll(selAll.checked));
    }

    // Bulk-bar action buttons (read descriptors from this._bulkActionDescriptors).
    this._wireBulkBarActions(app);
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

  _bulkActionDescriptors() {
    return [
      { label: 'Acknowledge',    onClick: () => this.bulkApplyStatus('acknowledged') },
      { label: 'Resolve',        onClick: () => this.bulkApplyStatus('resolved') },
      { label: 'False positive', onClick: () => this.bulkApplyStatus('false_positive') },
      { label: 'Ignore',         onClick: () => this.bulkApplyStatus('ignored') },
      { label: 'Clear',          onClick: () => this.clearSelection() },
    ];
  },

  // --- Filter mutation ---

  // _mutateFilter is the single entry point for any chip add/remove or
  // tab change. Patches the current params, persists, resets selection
  // (filter change invalidates which IDs were "visible"), and rewrites
  // the hash so the back button rewinds to the previous state.
  _mutateFilter(patch) {
    this.clearSelection({ rerender: false });
    const params = parseQueryParams();
    delete params.id; // never carry slide-over id forward
    delete params.view; // legacy grouped view dropped
    Object.assign(params, patch);
    // Drop nulled-out keys so empty values don't pollute the hash.
    Object.keys(params).forEach(k => { if (params[k] == null || params[k] === '') delete params[k]; });
    delete params.page; // filter change always resets pagination
    this._persistFromParams(params);
    const q = Object.entries(params).map(([k, v]) => k + '=' + encodeURIComponent(v)).join('&');
    location.hash = '#/findings' + (q ? '?' + q : '');
  },

  // --- "+ Filter" menu --------------------------------------------

  _openFilterMenu(anchor, params) {
    this._closeFilterMenu();
    const menu = document.createElement('div');
    menu.className = 'filter-menu';
    menu.setAttribute('role', 'menu');

    const items = [];
    if (!params.status)      items.push({ key: 'status',      label: 'Status' });
    if (!params.target_id)   items.push({ key: 'target_id',   label: 'Target' });
    if (!params.tool)        items.push({ key: 'tool',        label: 'Tool' });
    if (!params.host)        items.push({ key: 'host',        label: 'Host' });
    if (!params.template_id) items.push({ key: 'template_id', label: 'Template' });
    items.push({ key: '__reset__', label: 'Reset filters', divider: true });

    menu.innerHTML = items.map(it => {
      if (it.divider) return `<div class="filter-menu-divider"></div>
        <button type="button" class="filter-menu-item filter-menu-reset" data-mi-key="${it.key}" role="menuitem">${escapeHtml(it.label)}</button>`;
      return `<button type="button" class="filter-menu-item" data-mi-key="${it.key}" role="menuitem">${escapeHtml(it.label)}</button>`;
    }).join('');

    document.body.appendChild(menu);
    this._positionMenu(menu, anchor);
    this._filterMenu = menu;

    menu.querySelectorAll('[data-mi-key]').forEach(btn => {
      btn.addEventListener('click', () => {
        const k = btn.getAttribute('data-mi-key');
        if (k === '__reset__') {
          this._closeFilterMenu();
          this.resetAllFilters();
          return;
        }
        this._closeFilterMenu();
        this._openFilterSubmenu(anchor, k);
      });
    });

    setTimeout(() => {
      document.addEventListener('click', this._onMenuOutside);
      document.addEventListener('keydown', this._onMenuKey);
    }, 0);
  },

  _openFilterSubmenu(anchor, key) {
    this._closeFilterMenu();
    const menu = document.createElement('div');
    menu.className = 'filter-menu filter-submenu';
    menu.setAttribute('role', 'menu');

    if (key === 'status') {
      const opts = [
        ['open', 'Open'], ['acknowledged', 'Acknowledged'],
        ['resolved', 'Resolved'], ['false_positive', 'False positive'], ['ignored', 'Ignored'],
      ];
      menu.innerHTML = opts.map(([v, l]) =>
        `<button type="button" class="filter-menu-item" data-mi-pick="${v}" role="menuitem">${escapeHtml(l)}</button>`
      ).join('');
    } else if (key === 'target_id') {
      const targets = this._lastTargets || [];
      menu.innerHTML = `<input type="text" class="filter-menu-search" placeholder="Search targets…" aria-label="Search targets" />
        <div class="filter-menu-list" data-target-list>
          ${targets.map(t => `<button type="button" class="filter-menu-item" data-mi-pick="${escapeHtml(t.id)}" data-mi-text="${escapeHtml(t.value)}" role="menuitem">${escapeHtml(t.value)}</button>`).join('')}
        </div>`;
    } else if (key === 'tool') {
      const tools = Array.from(new Set((this._lastFindings || []).map(f => f.source_tool).filter(Boolean))).sort();
      if (!tools.length) {
        menu.innerHTML = `<div class="filter-menu-empty">No tools in current view.</div>`;
      } else {
        menu.innerHTML = tools.map(t =>
          `<button type="button" class="filter-menu-item" data-mi-pick="${escapeHtml(t)}" role="menuitem">${escapeHtml(t)}</button>`
        ).join('');
      }
    } else if (key === 'host') {
      menu.innerHTML = `<input type="text" class="filter-menu-search" placeholder="Host (e.g. api.example.com)" aria-label="Filter by host" />
        <button type="button" class="filter-menu-item filter-menu-apply" role="menuitem">Apply</button>`;
    } else if (key === 'template_id') {
      const tmpls = Array.from(new Set((this._lastFindings || []).map(f => f.template_id).filter(Boolean))).sort();
      if (!tmpls.length) {
        menu.innerHTML = `<div class="filter-menu-empty">No template IDs in current view.</div>`;
      } else {
        menu.innerHTML = tmpls.map(t =>
          `<button type="button" class="filter-menu-item" data-mi-pick="${escapeHtml(t)}" role="menuitem">${escapeHtml(t)}</button>`
        ).join('');
      }
    }

    document.body.appendChild(menu);
    this._positionMenu(menu, anchor);
    this._filterMenu = menu;

    menu.querySelectorAll('[data-mi-pick]').forEach(btn => {
      btn.addEventListener('click', () => {
        this._closeFilterMenu();
        this._mutateFilter({ [key]: btn.getAttribute('data-mi-pick') });
      });
    });

    // Host text input: enter / Apply applies the value.
    const search = menu.querySelector('.filter-menu-search');
    if (search) {
      search.focus();
      if (key === 'host') {
        const apply = () => {
          const v = search.value.trim();
          if (!v) return;
          this._closeFilterMenu();
          this._mutateFilter({ host: v });
        };
        search.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); apply(); } });
        const applyBtn = menu.querySelector('.filter-menu-apply');
        if (applyBtn) applyBtn.addEventListener('click', apply);
      } else if (key === 'target_id') {
        // Live filter the target list as the user types.
        search.addEventListener('input', () => {
          const q = search.value.toLowerCase();
          menu.querySelectorAll('[data-mi-pick]').forEach(btn => {
            const txt = (btn.getAttribute('data-mi-text') || '').toLowerCase();
            btn.style.display = (!q || txt.includes(q)) ? '' : 'none';
          });
        });
      }
    }

    setTimeout(() => {
      document.addEventListener('click', this._onMenuOutside);
      document.addEventListener('keydown', this._onMenuKey);
    }, 0);
  },

  _onMenuOutside: (e) => {
    const menu = FindingsPage._filterMenu;
    if (!menu) return;
    if (menu.contains(e.target)) return;
    FindingsPage._closeFilterMenu();
  },

  _onMenuKey: (e) => {
    if (e.key === 'Escape') FindingsPage._closeFilterMenu();
  },

  _closeFilterMenu() {
    if (this._filterMenu) {
      this._filterMenu.remove();
      this._filterMenu = null;
    }
    document.removeEventListener('click', this._onMenuOutside);
    document.removeEventListener('keydown', this._onMenuKey);
  },

  _positionMenu(menu, anchor) {
    const r = anchor.getBoundingClientRect();
    menu.style.position = 'fixed';
    menu.style.top = (r.bottom + 4) + 'px';
    menu.style.left = r.left + 'px';
    menu.style.zIndex = 100;
  },

  // --- Save-view menu ---------------------------------------------

  _openSaveMenu(anchor) {
    this._closeFilterMenu();
    const menu = document.createElement('div');
    menu.className = 'filter-menu';
    menu.setAttribute('role', 'menu');
    menu.innerHTML = `<button type="button" class="filter-menu-item filter-menu-reset" role="menuitem" data-mi-reset>Reset filters</button>`;
    document.body.appendChild(menu);
    this._positionMenu(menu, anchor);
    this._filterMenu = menu;
    menu.querySelector('[data-mi-reset]').addEventListener('click', () => {
      this._closeFilterMenu();
      this.resetAllFilters();
    });
    setTimeout(() => {
      document.addEventListener('click', this._onMenuOutside);
      document.addEventListener('keydown', this._onMenuKey);
    }, 0);
  },

  resetAllFilters() {
    this._clearStored();
    this.clearSelection({ rerender: false });
    location.hash = '#/findings';
  },

  // --- Bulk select ------------------------------------------------

  // onCheckboxChange — called from the per-row checkbox onclick. Reads
  // .checked off the input directly so we don't have to hunt up the
  // value via dataset on every render.
  onCheckboxChange(input) {
    const id = input.value;
    if (input.checked) this._selectedIds.add(id);
    else this._selectedIds.delete(id);
    this._refreshBulkBar();
    this._refreshRowHighlight(id, input.checked);
    const selAll = document.getElementById('findings-select-all');
    if (selAll) this._syncSelectAllState(selAll);
  },

  toggleAll(checked) {
    document.querySelectorAll('.fnd-checkbox').forEach(cb => {
      cb.checked = checked;
      const id = cb.value;
      if (checked) this._selectedIds.add(id);
      else this._selectedIds.delete(id);
      this._refreshRowHighlight(id, checked);
    });
    this._refreshBulkBar();
  },

  clearSelection(opts) {
    this._selectedIds.clear();
    if (!opts || opts.rerender !== false) {
      document.querySelectorAll('.fnd-checkbox').forEach(cb => { cb.checked = false; });
      document.querySelectorAll('.fnd-row').forEach(r => r.classList.remove('fnd-row-selected'));
      this._refreshBulkBar();
      const selAll = document.getElementById('findings-select-all');
      if (selAll) this._syncSelectAllState(selAll);
    }
  },

  _refreshRowHighlight(id, selected) {
    const row = document.querySelector(`tr.fnd-row[data-finding-id="${cssAttrEscape(id)}"]`);
    if (row) row.classList.toggle('fnd-row-selected', selected);
  },

  _syncSelectAllState(el) {
    const visible = Array.from(document.querySelectorAll('.fnd-checkbox'));
    if (visible.length === 0) {
      el.checked = false;
      el.indeterminate = false;
      return;
    }
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

  // --- Bulk apply -------------------------------------------------

  async bulkApplyStatus(newStatus) {
    const ids = Array.from(this._selectedIds);
    const total = ids.length;
    if (total === 0) return;

    // Resolve / FP confirm modals — both are "completed" terminal states
    // and harder to undo than ack/ignore. Compute severity breakdown from
    // the rows the user can see (good enough — selectedIds always come
    // from the current page).
    if (newStatus === 'resolved' || newStatus === 'false_positive') {
      const breakdown = this._severityBreakdown(ids);
      const ok = await Components.confirmDialog({
        title: newStatus === 'resolved' ? 'Mark as resolved?' : 'Mark as false positive?',
        message: this._buildBulkConfirmMessage(total, newStatus, breakdown),
        confirmLabel: 'Confirm',
      });
      if (!ok) return;
    }

    const results = await Promise.allSettled(
      ids.map(id => API.updateFindingStatus(id, newStatus))
    );
    const failed = [];
    results.forEach((r, i) => { if (r.status === 'rejected') failed.push(ids[i]); });

    if (failed.length === 0) {
      Components.toast.success(`${total} finding${total !== 1 ? 's' : ''} updated to ${this._humanStatus(newStatus)}`);
      ids.forEach(id => this._updateRowStatus(id, newStatus));
      this._selectedIds.clear();
      this._refreshBulkBar();
      const selAll = document.getElementById('findings-select-all');
      if (selAll) this._syncSelectAllState(selAll);
      if (typeof loadSidebarBadge === 'function') loadSidebarBadge();
    } else {
      Components.toast.error(`${failed.length}/${total} failed to update. Selection retained for retry.`);
      // Successful ones still patch the row UI; retain failures only.
      ids.forEach(id => {
        if (!failed.includes(id)) this._updateRowStatus(id, newStatus);
      });
      this._selectedIds = new Set(failed);
      // Rebuild the visible checked state to match the new selection set.
      document.querySelectorAll('.fnd-checkbox').forEach(cb => {
        const sel = this._selectedIds.has(cb.value);
        cb.checked = sel;
        this._refreshRowHighlight(cb.value, sel);
      });
      this._refreshBulkBar();
      if (typeof loadSidebarBadge === 'function') loadSidebarBadge();
    }
  },

  _severityBreakdown(ids) {
    const counts = {};
    ids.forEach(id => {
      const f = (this._lastFindings || []).find(x => x.id === id);
      if (!f) return;
      counts[f.severity] = (counts[f.severity] || 0) + 1;
    });
    return counts;
  },

  _buildBulkConfirmMessage(total, status, breakdown) {
    const order = ['critical', 'high', 'medium', 'low', 'info'];
    const parts = order.filter(s => breakdown[s] > 0).map(s => `${breakdown[s]} ${s}`);
    const suffix = parts.length ? ` This affects: ${parts.join(', ')}.` : '';
    const verb = status === 'resolved' ? 'resolved' : 'false positive';
    return `Mark ${total} finding${total !== 1 ? 's' : ''} as ${verb}?${suffix}`;
  },

  _updateRowStatus(id, newStatus) {
    const row = document.querySelector(`tr.fnd-row[data-finding-id="${cssAttrEscape(id)}"]`);
    if (!row) return;
    const cell = row.querySelector('[data-row-status]');
    if (cell) cell.innerHTML = Components.statusPill(this._statusToPill[newStatus] || 'open');
    // Mirror to the in-memory cache so a follow-up bulk action sees the
    // current status without a re-fetch.
    const f = (this._lastFindings || []).find(x => x.id === id);
    if (f) f.status = newStatus;
  },

  _humanStatus(s) {
    return ({
      open: 'Open', acknowledged: 'Acknowledged', resolved: 'Resolved',
      false_positive: 'False positive', ignored: 'Ignored',
    })[s] || s;
  },

  // --- Pagination -------------------------------------------------

  goToPage(page) {
    const params = parseQueryParams();
    params.page = page;
    delete params.id;
    const q = Object.entries(params).filter(([, v]) => v != null && v !== '').map(([k, v]) => k + '=' + encodeURIComponent(v)).join('&');
    location.hash = '#/findings' + (q ? '?' + q : '');
  },

  // --- Slide-over (PR4 #37) — preserved verbatim ------------------

  // handleRowClick is the click handler for raw-view <tr>s. It guards
  // against firing when the operator clicked the inline checkbox or any
  // other interactive element nested in the row.
  handleRowClick(evt, id) {
    const t = evt.target;
    if (t && t.closest && t.closest('input, button, a, .fnd-cb-col')) return;
    this._lastTrigger = evt.currentTarget;
    location.hash = '#/findings/' + encodeURIComponent(id);
  },

  async openSlideoverFor(id) {
    if (this._slide) {
      this._slide.close('replace');
      this._slide = null;
    }
    if (this._abortCtrl) this._abortCtrl.abort();
    const ctrl = new AbortController();
    this._abortCtrl = ctrl;

    const bodyEl = document.createElement('div');
    bodyEl.className = 'so-content';
    bodyEl.innerHTML = this._skeletonHtml();

    const trigger = this._lastTrigger;
    this._slide = Components.slideOver({
      title: 'Finding',
      body: bodyEl,
      onClose: (reason) => {
        this._slide = null;
        if (this._abortCtrl) { this._abortCtrl.abort(); this._abortCtrl = null; }
        if (trigger && document.body.contains(trigger)) {
          try { trigger.focus(); } catch (_) {}
        }
        if (reason !== 'replace' && /^#\/findings\/[^?]+/.test(location.hash)) {
          const params = parseQueryParams();
          const q = Object.entries(params).filter(([,v]) => v != null && v !== '').map(([k,v]) => k+'='+encodeURIComponent(v)).join('&');
          history.replaceState(null, '', '#/findings' + (q ? '?' + q : ''));
        }
      },
    });

    let data;
    try {
      data = await API.finding(id);
    } catch (err) {
      if (ctrl.signal.aborted) return;
      if (err.status === 404) {
        if (this._slide) this._slide.close('not-found');
        Components.toast.error('Finding ' + id + ' not found');
        history.replaceState(null, '', '#/findings');
      } else {
        bodyEl.innerHTML = `<div class="so-empty">Failed to load finding.<br>
          <button class="btn btn-ghost btn-sm" style="margin-top:12px"
            onclick="FindingsPage.openSlideoverFor('${escapeHtml(id)}')">Retry</button></div>`;
      }
      return;
    }
    if (ctrl.signal.aborted || !this._slide) return;
    this._renderSlideoverBody(bodyEl, data.finding, data.asset);
  },

  _skeletonHtml() {
    return `<div class="so-skeleton">
      <div class="so-skel-bar so-skel-bar-1"></div>
      <div class="so-skel-bar so-skel-bar-2"></div>
      <div class="so-skel-bar so-skel-bar-3"></div>
      <div class="so-skel-bar so-skel-bar-4"></div>
    </div>`;
  },

  _renderSlideoverBody(root, f, asset) {
    root.innerHTML = `
      ${this._headerHtml(f, asset)}
      ${this._actionBarHtml(f)}
      ${this._tabsHtml()}
      <div class="so-tabpanel" data-tab-panel="overview" role="tabpanel" aria-labelledby="so-tab-overview">${this._overviewHtml(f)}</div>
      <div class="so-tabpanel" data-tab-panel="evidence" role="tabpanel" aria-labelledby="so-tab-evidence" hidden>${this._evidenceHtml(f)}</div>
      <div class="so-tabpanel" data-tab-panel="remediation" role="tabpanel" aria-labelledby="so-tab-remediation" hidden>${this._remediationHtml(f)}</div>
      <div class="so-tabpanel" data-tab-panel="timeline" role="tabpanel" aria-labelledby="so-tab-timeline" hidden>${this._timelineHtml(f)}</div>
    `;
    this._wireTabs(root);
    this._wireActions(root, f);
    this._wireCopyButtons(root, f, asset);
    this._wireRemediationCopy(root, f);
    if (this._slide && this._slide.root) {
      const titleEl = this._slide.root.querySelector('.slide-over-title');
      if (titleEl) titleEl.textContent = f.title || 'Finding';
    }
  },

  _headerHtml(f, asset) {
    const sevPill = Components.severityPill(f.severity);
    const statusPill = Components.statusPill(this._statusToPill[f.status] || 'open');
    const toolPill = `<span class="pill" style="background:var(--ink-700);border:1px solid var(--ink-600);color:var(--text-secondary)">${escapeHtml(f.source_tool || 'unknown')}</span>`;
    const idShort = (f.id || '').slice(0, 12);
    const assetLine = asset
      ? `on <code>${escapeHtml(asset.value)}</code>
         <button type="button" class="so-icon-btn" data-copy-asset aria-label="Copy asset">${Components.icon('copy', 14)}</button>`
      : `<span class="text-muted">(asset deleted)</span>`;
    return `
      <div class="so-pillrow">
        ${sevPill}
        ${statusPill}
        ${toolPill}
        <span style="flex:1"></span>
        <button type="button" class="so-id" data-copy-id aria-label="Copy finding ID">${escapeHtml(idShort)}</button>
      </div>
      <h2 class="so-title">${escapeHtml(f.title || '(untitled finding)')}</h2>
      <div class="so-asset">
        ${assetLine}
        <span class="so-asset-sep">·</span>
        <span class="text-muted">last seen ${escapeHtml(Components.timeAgo(f.last_seen))}</span>
      </div>
    `;
  },

  _actionBarHtml(f) {
    const matrix = {
      open:           [['acknowledged','Acknowledge','ack'], ['resolved','Mark resolved','resolved'], ['false_positive','False positive',''], ['ignored','Ignore','']],
      acknowledged:   [['resolved','Mark resolved','resolved'], ['open','Reopen','reopen'], ['false_positive','False positive','']],
      resolved:       [['open','Reopen','reopen']],
      false_positive: [['open','Reopen','reopen']],
      ignored:        [['open','Reopen','reopen']],
    };
    const actions = matrix[f.status] || [];
    if (!actions.length) return '<div class="so-actionbar" data-actionbar></div>';
    const btns = actions.map(([s, label, accent]) => {
      const cls = 'so-action' + (accent ? ' so-action-' + accent : '');
      return `<button type="button" class="${cls}" data-set-status="${s}">${escapeHtml(label)}</button>`;
    }).join('');
    return `<div class="so-actionbar" data-actionbar>${btns}</div>`;
  },

  _tabsHtml() {
    return `<div class="so-tabs" role="tablist" aria-label="Finding sections">
      <button type="button" class="so-tab tab-active" id="so-tab-overview" role="tab" aria-selected="true" aria-controls="so-panel-overview" data-tab="overview">Overview</button>
      <button type="button" class="so-tab" id="so-tab-evidence" role="tab" aria-selected="false" aria-controls="so-panel-evidence" data-tab="evidence">Evidence</button>
      <button type="button" class="so-tab" id="so-tab-remediation" role="tab" aria-selected="false" aria-controls="so-panel-remediation" data-tab="remediation">Remediation</button>
      <button type="button" class="so-tab" id="so-tab-timeline" role="tab" aria-selected="false" aria-controls="so-panel-timeline" data-tab="timeline">Timeline</button>
    </div>`;
  },

  _wireTabs(root) {
    const tabs = root.querySelectorAll('.so-tab');
    tabs.forEach(tab => {
      tab.addEventListener('click', () => {
        const name = tab.getAttribute('data-tab');
        tabs.forEach(t => {
          const active = t === tab;
          t.classList.toggle('tab-active', active);
          t.setAttribute('aria-selected', active ? 'true' : 'false');
        });
        root.querySelectorAll('[data-tab-panel]').forEach(p => {
          p.hidden = p.getAttribute('data-tab-panel') !== name;
        });
      });
    });
  },

  _wireActions(root, f) {
    const bar = root.querySelector('[data-actionbar]');
    if (!bar) return;
    bar.querySelectorAll('[data-set-status]').forEach(btn => {
      btn.addEventListener('click', async () => {
        const newStatus = btn.getAttribute('data-set-status');
        bar.querySelectorAll('[data-set-status]').forEach(b => { b.disabled = true; });
        try {
          await API.updateFindingStatus(f.id, newStatus);
          f.status = newStatus;
          this._refreshHeaderStatusPill(root, newStatus);
          bar.outerHTML = this._actionBarHtml(f);
          this._wireActions(root, f);
          this._updateRowStatus(f.id, newStatus);
          if (typeof loadSidebarBadge === 'function') loadSidebarBadge();
        } catch (err) {
          Components.toast.error('Failed to update status: ' + err.message);
          bar.querySelectorAll('[data-set-status]').forEach(b => { b.disabled = false; });
        }
      });
    });
  },

  _refreshHeaderStatusPill(root, status) {
    const row = root.querySelector('.so-pillrow');
    if (!row) return;
    const pillKey = this._statusToPill[status] || 'open';
    const pills = row.querySelectorAll('.pill');
    if (pills.length >= 2) {
      pills[1].outerHTML = Components.statusPill(pillKey);
    }
  },

  _wireCopyButtons(root, f, asset) {
    const idBtn = root.querySelector('[data-copy-id]');
    if (idBtn) {
      idBtn.addEventListener('click', () => {
        navigator.clipboard.writeText(f.id || '').then(
          () => Components.toast.success('Finding ID copied'),
          () => Components.toast.error('Copy failed'),
        );
      });
    }
    const assetBtn = root.querySelector('[data-copy-asset]');
    if (assetBtn && asset) {
      assetBtn.addEventListener('click', () => {
        navigator.clipboard.writeText(asset.value || '').then(
          () => Components.toast.success('Asset copied'),
          () => Components.toast.error('Copy failed'),
        );
      });
    }
  },

  _wireRemediationCopy(root, f) {
    const btn = root.querySelector('[data-copy-runbook]');
    if (!btn) return;
    btn.addEventListener('click', () => {
      const blob = '## Remediation\n\n' + (f.remediation || '');
      navigator.clipboard.writeText(blob).then(
        () => Components.toast.success('Runbook copied'),
        () => Components.toast.error('Copy failed'),
      );
    });
  },

  // --- Tab content (PR4) ------------------------------------------

  _overviewHtml(f) {
    let cvssCard;
    if (!f.cvss || f.cvss <= 0) {
      cvssCard = this._cardHtml('CVSS 3.1', '<span class="so-card-value-empty">—</span>', '');
    } else {
      const score = f.cvss.toFixed(1);
      let sev = 'low', label = 'Low';
      if (f.cvss >= 9.0) { sev = 'critical'; label = 'Critical'; }
      else if (f.cvss >= 7.0) { sev = 'high'; label = 'High'; }
      else if (f.cvss >= 4.0) { sev = 'medium'; label = 'Medium'; }
      cvssCard = this._cardHtml('CVSS 3.1', `<span class="so-card-value sev-${sev}">${score}</span>`, label);
    }
    const conf = (f.confidence == null || f.confidence === 0)
      ? '<span class="so-card-value-empty">—</span>'
      : `<span class="so-card-value">${Math.round(f.confidence)}%</span>`;
    const confCard = this._cardHtml('Confidence', conf, '');
    const cards = `<div class="so-cards" style="--so-card-cols:2">${cvssCard}${confCard}</div>`;

    const description = f.description
      ? `<div class="so-section">
          <h3 class="so-section-label">Description</h3>
          <p class="so-prose">${escapeHtml(f.description)}</p>
        </div>`
      : '';

    const refs = this._referencesHtml(f);

    const meta = `<div class="so-section">
      <h3 class="so-section-label">Details</h3>
      <ul class="so-list" style="list-style:none;padding-left:0">
        <li><span class="text-muted">Template</span> <code>${escapeHtml(f.template_id || '—')}</code></li>
        <li><span class="text-muted">First seen</span> ${escapeHtml(Components.formatDate(f.first_seen))}</li>
        ${f.resolved_at ? `<li><span class="text-muted">Resolved</span> ${escapeHtml(Components.formatDate(f.resolved_at))}</li>` : ''}
      </ul>
    </div>`;

    return `<div class="so-section">${cards}</div>${description}${refs}${meta}`;
  },

  _cardHtml(label, valueHtml, captionText) {
    return `<div class="so-card">
      <div class="so-card-label">${escapeHtml(label)}</div>
      ${valueHtml}
      ${captionText ? `<div class="so-card-caption">${escapeHtml(captionText)}</div>` : ''}
    </div>`;
  },

  _referencesHtml(f) {
    const items = [];
    if (f.cve) items.push({ kind: 'cve', value: f.cve });
    if (Array.isArray(f.references)) {
      for (const r of f.references) {
        if (!r) continue;
        const cveMatch = String(r).match(/CVE-\d{4}-\d{4,7}/i);
        if (cveMatch && /^CVE-\d{4}-\d{4,7}$/i.test(String(r).trim())) {
          items.push({ kind: 'cve', value: cveMatch[0] });
        } else if (/^https?:\/\//i.test(String(r))) {
          items.push({ kind: 'url', value: String(r) });
        } else if (cveMatch) {
          items.push({ kind: 'cve', value: cveMatch[0] });
        } else {
          items.push({ kind: 'text', value: String(r) });
        }
      }
    }
    if (!items.length) return '';
    const lis = items.map(it => {
      if (it.kind === 'cve') return `<li>${Components.cveLink(it.value)}</li>`;
      if (it.kind === 'url') return `<li><a class="so-extlink" href="${escapeHtml(it.value)}" target="_blank" rel="noopener noreferrer">${escapeHtml(it.value)}${Components.icon('external', 12)}</a></li>`;
      return `<li><span class="text-muted">${escapeHtml(it.value)}</span></li>`;
    }).join('');
    return `<div class="so-section">
      <h3 class="so-section-label">References</h3>
      <ul class="so-refs">${lis}</ul>
    </div>`;
  },

  _evidenceHtml(f) {
    const raw = f.evidence;
    if (!raw) return '<div class="so-empty">No evidence captured.</div>';
    const trimmed = String(raw).trim();
    if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
      try {
        const obj = JSON.parse(trimmed);
        if (obj && (obj.request || obj.response)) {
          return this._httpInteractionHtml(obj);
        }
        return `<div class="so-section">
          <h3 class="so-section-label">Evidence</h3>
          <pre class="so-evidence">${escapeHtml(JSON.stringify(obj, null, 2))}</pre>
        </div>`;
      } catch (_) { /* fallthrough */ }
    }
    return `<div class="so-section">
      <h3 class="so-section-label">Evidence</h3>
      <pre class="so-evidence">${escapeHtml(String(raw))}</pre>
    </div>`;
  },

  _httpInteractionHtml(obj) {
    const req = obj.request ? `<div class="so-evidence-block">
      <div class="so-evidence-block-label">Request</div>
      <pre class="so-evidence">${escapeHtml(typeof obj.request === 'string' ? obj.request : JSON.stringify(obj.request, null, 2))}</pre>
    </div>` : '';
    const resp = obj.response ? `<div class="so-evidence-block">
      <div class="so-evidence-block-label">Response</div>
      <pre class="so-evidence">${escapeHtml(typeof obj.response === 'string' ? obj.response : JSON.stringify(obj.response, null, 2))}</pre>
    </div>` : '';
    return `<div class="so-section">
      <h3 class="so-section-label">Evidence</h3>
      ${req}${resp}
    </div>`;
  },

  _remediationHtml(f) {
    const body = f.remediation;
    if (!body) return '<div class="so-empty">No remediation steps documented.</div>';
    const lines = String(body).split(/\r?\n/).map(s => s.trim()).filter(Boolean);
    const allNumbered = lines.length > 1 && lines.every(l => /^\d+[.)]\s+/.test(l));
    let bodyHtml;
    if (allNumbered) {
      const items = lines.map(l => l.replace(/^\d+[.)]\s+/, '')).map(l =>
        `<li>${this._inlineCode(l)}</li>`).join('');
      bodyHtml = `<ol class="so-list">${items}</ol>`;
    } else {
      bodyHtml = `<p class="so-prose">${this._inlineCode(String(body))}</p>`;
    }
    return `<div class="so-section">
      <h3 class="so-section-label">Remediation</h3>
      ${bodyHtml}
      <div style="margin-top:12px">
        <button type="button" class="btn btn-ghost btn-sm" data-copy-runbook>Copy as runbook</button>
      </div>
    </div>`;
  },

  _inlineCode(text) {
    return escapeHtml(text).replace(/`([^`]+)`/g, (_m, g) => `<code>${g}</code>`);
  },

  _timelineHtml(f) {
    const events = [];
    if (f.created_at) {
      const scanShort = (f.first_seen_scan_id || f.scan_id || '').slice(0, 8);
      const scanText = scanShort ? `Detected · scan <code>${escapeHtml(scanShort)}</code>` : 'Detected';
      events.push({ ts: f.created_at, text: scanText });
    }
    if (f.first_seen && f.first_seen !== f.created_at) {
      events.push({ ts: f.first_seen, text: 'First seen on this asset' });
    }
    if (f.last_seen && f.last_seen !== f.first_seen) {
      events.push({ ts: f.last_seen, text: 'Last seen' });
    }
    if (f.resolved_at) {
      events.push({ ts: f.resolved_at, text: 'Resolved' });
    }
    if (!events.length) return '<div class="so-empty">No timeline events.</div>';
    events.sort((a, b) => new Date(b.ts) - new Date(a.ts));
    const lis = events.map(e => `<li>
      <span class="so-timeline-when">${escapeHtml(Components.timeAgo(e.ts))}</span>
      <span class="so-timeline-what">${e.text}</span>
    </li>`).join('');
    return `<div class="so-section">
      <h3 class="so-section-label">Timeline</h3>
      <ol class="so-timeline">${lis}</ol>
    </div>`;
  },

  closeSlideover() {
    if (this._slide) {
      this._slide.close('hashchange');
      this._slide = null;
    }
  },
};

// cssAttrEscape quotes a value for use inside an attribute selector
// (`[data-finding-id="…"]`). CSS.escape covers everything; the manual
// fallback covers the rare browser without it.
function cssAttrEscape(s) {
  if (typeof CSS !== 'undefined' && typeof CSS.escape === 'function') return CSS.escape(s);
  return String(s).replace(/(["\\])/g, '\\$1');
}
