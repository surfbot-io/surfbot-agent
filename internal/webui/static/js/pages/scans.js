// Scans page (list view). PR7 #40 extracted the detail view into the
// dedicated ScanDetailPage module — this file now only owns the list,
// polling of overall scan status, the ad-hoc "Start scan" form, and
// the PR5 #38 filter chips/tabs.
const ScansPage = {
  pollTimer: null,

  // PR5 (#38): scans list adds status tabs + filter chips (target /
  // template / range). Bulk actions are explicitly out of scope; the
  // backend doesn't yet accept template_id or range params either, so
  // those filters are applied client-side over the fetched page.
  STORAGE_KEY: 'surfbot_scans_filters',
  STORAGE_VERSION: 1,
  _filterMenu: null,
  _filters: { status: '', target_id: '', template_id: '', range: '' },
  _allScans: [],
  _allTargets: [],

  async render(app, params) {
    app.innerHTML = '<div class="loading">Loading scans...</div>';

    try {
      this._restoreFilters();
      // Apply URL params on top of stored — deep links win.
      if (params) {
        ['status', 'target_id', 'template_id', 'range'].forEach(k => {
          if (params[k]) this._filters[k] = params[k];
        });
      }
      const [scansData, statusData, targetsData] = await Promise.all([
        API.scans(this._backendParamsForScans()),
        API.scanStatus(),
        API.targets(),
      ]);
      this._allScans = scansData.scans || [];
      this._allTargets = (targetsData && targetsData.targets) || [];
      app.innerHTML = this.template(scansData, statusData, targetsData);
      this.bindEvents(app, targetsData);

      // Start polling if a scan is running
      if (statusData.scanning) {
        this.startPolling(app);
      }
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load scans: ' + err.message);
    }
  },

  // _backendParamsForScans returns only the params the /scans endpoint
  // actually accepts. template_id and range are filtered client-side
  // (P0.7 backend gap noted in #38).
  _backendParamsForScans() {
    const p = { limit: 50 };
    if (this._filters.target_id) p.target_id = this._filters.target_id;
    return p;
  },

  _restoreFilters() {
    try {
      const raw = localStorage.getItem(this.STORAGE_KEY);
      if (!raw) return;
      const parsed = JSON.parse(raw);
      if (!parsed || parsed.version !== this.STORAGE_VERSION) return;
      Object.assign(this._filters, parsed.filters || {});
    } catch (_) {}
  },

  _persistFilters() {
    try {
      localStorage.setItem(this.STORAGE_KEY, JSON.stringify({
        version: this.STORAGE_VERSION,
        filters: this._filters,
        savedAt: new Date().toISOString(),
      }));
    } catch (_) {}
  },

  // _applyClientFilters narrows the backend response by the chips that
  // /scans doesn't natively support: status (tab), template_id, range.
  _applyClientFilters(scans) {
    let out = scans || [];
    if (this._filters.status) out = out.filter(s => s.status === this._filters.status);
    if (this._filters.template_id) out = out.filter(s => s.template_id === this._filters.template_id);
    if (this._filters.range) {
      const since = this._rangeToSinceMs(this._filters.range);
      if (since != null) {
        out = out.filter(s => {
          const t = new Date(s.started_at || s.created_at).getTime();
          return !isNaN(t) && t >= since;
        });
      }
    }
    return out;
  },

  _rangeToSinceMs(range) {
    const now = Date.now();
    switch (range) {
      case '24h': return now - 24 * 3600 * 1000;
      case '7d':  return now - 7 * 24 * 3600 * 1000;
      case '30d': return now - 30 * 24 * 3600 * 1000;
      default:    return null;
    }
  },

  template(data, statusData, targetsData) {
    const targets = targetsData?.targets || [];
    const scanRunning = statusData?.scanning;

    const scanForm = targets.length > 0 ? `
      <div class="add-form">
        <form id="new-scan-form" class="inline-form">
          <select id="scan-target" class="filter-select" required>
            ${targets.map(t => `<option value="${t.id}">${escapeHtml(t.value)}</option>`).join('')}
          </select>
          <select id="scan-type" class="filter-select">
            <option value="full">Full scan</option>
            <option value="quick">Quick scan</option>
            <option value="discovery">Discovery only</option>
          </select>
          <button type="submit" class="btn btn-primary" ${scanRunning ? 'disabled' : ''}>
            ${scanRunning ? 'Scan running...' : 'Start Scan'}
          </button>
          <button type="button" id="toggle-scan-opts" class="btn btn-secondary" style="margin-left:4px"
            title="Advanced options">⚙</button>
        </form>
        <div id="scan-advanced" class="scan-advanced" style="display:none">
          <div class="form-section" style="margin-top:8px">
            <h4 style="margin:0 0 8px">Advanced Options</h4>
            <div class="form-row">
              <label for="scan-rate-limit">Rate limit (req/s)</label>
              <input type="number" id="scan-rate-limit" class="form-input form-input-sm"
                     min="0" value="0" placeholder="0 = per-tool default">
            </div>
            <div class="form-row">
              <label for="scan-timeout">Per-phase timeout (seconds)</label>
              <input type="number" id="scan-timeout" class="form-input form-input-sm"
                     min="0" value="0" placeholder="0 = default (300s)">
            </div>
            <div class="form-row">
              <label>Tools</label>
              <div id="scan-tools-group" class="checkbox-group">
                <span class="text-muted">Loading tools...</span>
              </div>
            </div>
          </div>
        </div>
        <div id="scan-error" class="form-error" style="display:none"></div>
      </div>
    ` : '';

    // Active scan banner
    const activeBanner = scanRunning && statusData.scan ? `
      <div class="scan-banner">
        <div class="scan-banner-content">
          <div class="loading" style="height:auto;padding:0">Scanning ${escapeHtml(statusData.scan.target)}...</div>
          <span class="text-muted">Phase: ${statusData.scan.phase} &middot; ${Math.round(statusData.scan.progress)}%</span>
        </div>
        <div class="scan-progress-bar">
          <div class="scan-progress-fill" style="width:${statusData.scan.progress}%"></div>
        </div>
      </div>
    ` : '';

    if (data.scans.length === 0 && !scanRunning) {
      return `
        <div class="page-header"><h2>Scans</h2></div>
        ${scanForm}
        ${Components.emptyState('No scans', 'No scans have been run yet. Start one above or from the Targets page.')}
      `;
    }

    const filtered = this._applyClientFilters(data.scans);
    const tabsHtml = this._statusTabsHtml(data.scans);
    const stripHtml = this._chipStripHtml();

    const rows = filtered.map(s => {
      let dur = '-';
      if (s.started_at && s.finished_at) {
        dur = Components.formatDuration(Math.floor((new Date(s.finished_at) - new Date(s.started_at)) / 1000));
      }
      return `
        <tr class="clickable" onclick="location.hash='#/scans/${s.id}'">
          <td class="mono">${Components.truncateID(s.id)}</td>
          <td class="mono">${escapeHtml(s.target || s.target_id || '-')}</td>
          <td>${this._statusCell(s)}</td>
          <td>${s.type}</td>
          <td>${this._progressCell(s)}</td>
          <td class="mono">${dur}</td>
          <td>${(s.target_state && s.target_state.findings_open_total) || 0} findings</td>
          <td class="text-muted">${Components.timeAgo(s.created_at)}</td>
        </tr>
      `;
    }).join('');

    const filteredCount = filtered.length;
    const totalCount = data.scans.length;
    const counterText = (filteredCount === totalCount)
      ? `${data.total} total scans`
      : `${filteredCount} shown of ${totalCount} loaded · ${data.total} total`;

    return `
      <div class="page-header">
        <h2>Scans</h2>
        <p>${counterText}</p>
      </div>
      ${scanForm}
      ${activeBanner}
      ${tabsHtml}
      ${stripHtml}
      ${filteredCount === 0
        ? `<div class="table-container"><table>
            <thead><tr><th>ID</th><th>Target</th><th>Status</th><th>Type</th><th>Progress</th><th>Duration</th><th>Results</th><th>Date</th></tr></thead>
            <tbody><tr><td colspan="8" class="empty-state">No scans match the current filters.</td></tr></tbody>
          </table></div>`
        : Components.table(['ID', 'Target', 'Status', 'Type', 'Progress', 'Duration', 'Results', 'Date'], [rows])}
    `;
  },

  // PR7 #40: status pill carries phase name + animated dot for running
  // scans, plain status badge for terminal states. Mirrors the topbar
  // indicator visual so the list and topbar stay coherent.
  _statusCell(s) {
    if (s.status === 'running') {
      const phase = s.phase ? escapeHtml(s.phase) : 'running';
      return `<span class="badge-status badge-running scan-running-pill">
        <span class="scan-running-dot" aria-hidden="true"></span>${phase}
      </span>`;
    }
    return Components.statusBadge(s.status);
  },

  // PR7 #40: inline progress column. Running scans get a 96px bar +
  // percent; terminal states render a single em-dash so the column
  // never goes empty.
  _progressCell(s) {
    if (s.status !== 'running') return '<span class="text-muted">—</span>';
    const pct = Math.round(s.progress || 0);
    const fill = pct === 0 ? 100 : pct;
    return `<div class="scan-row-progress" title="${pct}%">
      <div class="scan-row-progress-track">
        <div class="scan-row-progress-fill marquee" style="width:${fill}%"></div>
      </div>
      <span class="scan-row-progress-pct mono">${pct}%</span>
    </div>`;
  },

  _statusTabsHtml(allScans) {
    const counts = { running: 0, completed: 0, cancelled: 0, failed: 0 };
    (allScans || []).forEach(s => { if (counts[s.status] != null) counts[s.status]++; });
    const total = (allScans || []).length;
    const tabs = [
      { key: '',          label: 'All',       count: total },
      { key: 'running',   label: 'Running',   count: counts.running },
      { key: 'completed', label: 'Completed', count: counts.completed },
      { key: 'cancelled', label: 'Cancelled', count: counts.cancelled },
      { key: 'failed',    label: 'Failed',    count: counts.failed },
    ];
    const active = this._filters.status || '';
    return `<div class="sev-tabs" role="tablist" aria-label="Filter by status">
      ${tabs.map(t => {
        const isActive = t.key === active;
        const cls = 'sev-tab' + (isActive ? ' sev-tab-active' : '');
        return `<button type="button" class="${cls}" role="tab" aria-selected="${isActive}" data-scan-status="${escapeHtml(t.key)}">
          ${escapeHtml(t.label)}<span class="sev-tab-count">${t.count}</span>
        </button>`;
      }).join('')}
    </div>`;
  },

  _chipStripHtml() {
    const f = this._filters;
    const chips = [];
    if (f.target_id) {
      const t = this._allTargets.find(x => x.id === f.target_id);
      chips.push({ key: 'target_id', label: 'Target: ' + (t ? t.value : f.target_id) });
    }
    if (f.template_id) chips.push({ key: 'template_id', label: 'Template: ' + f.template_id });
    if (f.range) chips.push({ key: 'range', label: 'Range: ' + f.range });

    const chipMounts = chips.map((c, i) =>
      `<span class="filter-chip-mount" data-chip-idx="${i}" data-chip-key="${c.key}" data-chip-label="${escapeHtml(c.label)}"></span>`
    ).join('');

    return `<div class="filter-strip" role="group" aria-label="Active scan filters">
      <div class="filter-strip-chips">${chipMounts}</div>
      <button type="button" class="filter-add-btn" data-add-filter>
        ${Components.icon('plus', 14)}<span>Filter</span>
      </button>
    </div>`;
  },

  bindEvents(app, targetsData) {
    // Status tabs + filter chips first — these must wire even when the
    // page renders without #new-scan-form (no targets configured).
    this._wireFilterUI(app);

    const form = document.getElementById('new-scan-form');
    if (!form) return;

    // Advanced options toggle.
    const toggleBtn = document.getElementById('toggle-scan-opts');
    const advPanel = document.getElementById('scan-advanced');
    if (toggleBtn && advPanel) {
      toggleBtn.addEventListener('click', () => {
        const hidden = advPanel.style.display === 'none';
        advPanel.style.display = hidden ? '' : 'none';
        if (hidden) this.loadToolCheckboxes();
      });
    }

    // Restore saved options from localStorage.
    this.restoreScanOpts();

    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const targetId = document.getElementById('scan-target').value;
      const scanType = document.getElementById('scan-type').value;
      const errorEl = document.getElementById('scan-error');
      const btn = form.querySelector('button[type="submit"]');

      btn.disabled = true;
      btn.textContent = 'Starting...';
      errorEl.style.display = 'none';

      const opts = this.collectScanOpts();
      this.saveScanOpts(opts);

      try {
        await API.startScan(targetId, scanType, opts);
        ScansPage.render(app);
      } catch (err) {
        errorEl.textContent = err.message;
        errorEl.style.display = 'block';
        btn.disabled = false;
        btn.textContent = 'Start Scan';
      }
    });
  },

  // _wireFilterUI binds status tab clicks, chip removal, and the
  // "+ Filter" menu. Mounted via bindEvents so it runs after every
  // template re-render.
  _wireFilterUI(app) {
    app.querySelectorAll('[data-scan-status]').forEach(btn => {
      btn.addEventListener('click', () => {
        this._filters.status = btn.getAttribute('data-scan-status') || '';
        this._persistFilters();
        ScansPage.render(app);
      });
    });

    app.querySelectorAll('.filter-chip-mount').forEach(slot => {
      const key = slot.getAttribute('data-chip-key');
      const label = slot.getAttribute('data-chip-label');
      const chip = Components.filterChip(label, () => {
        this._filters[key] = '';
        this._persistFilters();
        ScansPage.render(app);
      });
      slot.replaceWith(chip);
    });

    const addBtn = app.querySelector('[data-add-filter]');
    if (addBtn) {
      addBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        this._openFilterMenu(addBtn, app);
      });
    }
  },

  _openFilterMenu(anchor, app) {
    this._closeFilterMenu();
    const menu = document.createElement('div');
    menu.className = 'filter-menu';
    menu.setAttribute('role', 'menu');
    const items = [];
    if (!this._filters.target_id)   items.push({ key: 'target_id',   label: 'Target' });
    if (!this._filters.template_id) items.push({ key: 'template_id', label: 'Template' });
    if (!this._filters.range)       items.push({ key: 'range',       label: 'Range' });
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
        this._closeFilterMenu();
        if (k === '__reset__') {
          this._filters = { status: '', target_id: '', template_id: '', range: '' };
          try { localStorage.removeItem(this.STORAGE_KEY); } catch (_) {}
          ScansPage.render(app);
          return;
        }
        this._openFilterSubmenu(anchor, k, app);
      });
    });
    setTimeout(() => {
      document.addEventListener('click', this._onMenuOutside);
      document.addEventListener('keydown', this._onMenuKey);
    }, 0);
  },

  _openFilterSubmenu(anchor, key, app) {
    this._closeFilterMenu();
    const menu = document.createElement('div');
    menu.className = 'filter-menu filter-submenu';
    menu.setAttribute('role', 'menu');

    if (key === 'target_id') {
      const targets = this._allTargets || [];
      menu.innerHTML = `<input type="text" class="filter-menu-search" placeholder="Search targets…" aria-label="Search targets" />
        <div class="filter-menu-list">
          ${targets.map(t => `<button type="button" class="filter-menu-item" data-mi-pick="${escapeHtml(t.id)}" data-mi-text="${escapeHtml(t.value)}" role="menuitem">${escapeHtml(t.value)}</button>`).join('')}
        </div>`;
    } else if (key === 'template_id') {
      const tmpls = Array.from(new Set((this._allScans || []).map(s => s.template_id).filter(Boolean))).sort();
      menu.innerHTML = tmpls.length
        ? tmpls.map(t => `<button type="button" class="filter-menu-item" data-mi-pick="${escapeHtml(t)}" role="menuitem">${escapeHtml(t)}</button>`).join('')
        : `<div class="filter-menu-empty">No template IDs in current view.</div>`;
    } else if (key === 'range') {
      const opts = [['24h', 'Last 24 hours'], ['7d', 'Last 7 days'], ['30d', 'Last 30 days']];
      menu.innerHTML = opts.map(([v, l]) =>
        `<button type="button" class="filter-menu-item" data-mi-pick="${v}" role="menuitem">${escapeHtml(l)}</button>`
      ).join('');
    }

    document.body.appendChild(menu);
    this._positionMenu(menu, anchor);
    this._filterMenu = menu;

    menu.querySelectorAll('[data-mi-pick]').forEach(btn => {
      btn.addEventListener('click', () => {
        this._closeFilterMenu();
        this._filters[key] = btn.getAttribute('data-mi-pick');
        this._persistFilters();
        ScansPage.render(app);
      });
    });

    const search = menu.querySelector('.filter-menu-search');
    if (search) {
      search.focus();
      if (key === 'target_id') {
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
    const menu = ScansPage._filterMenu;
    if (!menu) return;
    if (menu.contains(e.target)) return;
    ScansPage._closeFilterMenu();
  },

  _onMenuKey: (e) => {
    if (e.key === 'Escape') ScansPage._closeFilterMenu();
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

  async loadToolCheckboxes() {
    const group = document.getElementById('scan-tools-group');
    if (!group || group.dataset.loaded) return;
    try {
      const data = await API.availableTools();
      const tools = data.tools || [];
      const saved = this.getSavedTools();
      group.innerHTML = tools.map(t => {
        const checked = saved.includes(t.name) ? 'checked' : '';
        return `<label class="checkbox-label">
          <input type="checkbox" name="scan_tool" value="${escapeHtml(t.name)}" ${checked}>
          ${escapeHtml(t.name)} <span class="text-muted">(${escapeHtml(t.phase)})</span>
        </label>`;
      }).join('');
      group.dataset.loaded = '1';
    } catch {
      group.innerHTML = '<span class="text-muted">Failed to load tools</span>';
    }
  },

  collectScanOpts() {
    const rl = document.getElementById('scan-rate-limit');
    const to = document.getElementById('scan-timeout');
    const tools = Array.from(document.querySelectorAll('input[name="scan_tool"]:checked'))
      .map(cb => cb.value);
    return {
      tools: tools,
      rate_limit: rl ? parseInt(rl.value, 10) || 0 : 0,
      timeout: to ? parseInt(to.value, 10) || 0 : 0,
    };
  },

  saveScanOpts(opts) {
    try { localStorage.setItem('surfbot_scan_opts', JSON.stringify(opts)); } catch {}
  },

  restoreScanOpts() {
    try {
      const raw = localStorage.getItem('surfbot_scan_opts');
      if (!raw) return;
      const opts = JSON.parse(raw);
      const rl = document.getElementById('scan-rate-limit');
      const to = document.getElementById('scan-timeout');
      if (rl && opts.rate_limit) rl.value = opts.rate_limit;
      if (to && opts.timeout) to.value = opts.timeout;
    } catch {}
  },

  getSavedTools() {
    try {
      const raw = localStorage.getItem('surfbot_scan_opts');
      if (!raw) return [];
      return JSON.parse(raw).tools || [];
    } catch { return []; }
  },

  startPolling(app) {
    this.stopPolling();
    this.pollTimer = setInterval(async () => {
      try {
        const status = await API.scanStatus();
        if (!status.scanning) {
          this.stopPolling();
          ScansPage.render(app);
        } else {
          // Update progress banner
          const fill = document.querySelector('.scan-progress-fill');
          const phase = document.querySelector('.scan-banner-content .text-muted');
          if (fill && status.scan) {
            fill.style.width = status.scan.progress + '%';
          }
          if (phase && status.scan) {
            phase.textContent = 'Phase: ' + status.scan.phase + ' \u00B7 ' + Math.round(status.scan.progress) + '%';
          }
        }
      } catch {
        // Ignore polling errors
      }
    }, 3000);
  },

  stopPolling() {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  },

};
