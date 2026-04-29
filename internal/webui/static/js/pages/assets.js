// Assets page — UI v2 (PR10 #43).
//
// Layout:
//   1. Page header with target subtitle.
//   2. Type tabs (single-select pills with counts: All / subdomain / port_service / …).
//   3. Filter strip — Status / Tag chips (popup menus) + live Search input.
//   4. Target selector bar (when ≥ 2 targets exist).
//   5. Two-column grid:
//      - col-7: tree of assets (depth-indented, chevron toggle, type pill, sev pill + count).
//      - col-5: detail pane with stats (findings / open ports / tech), tags, CTAs.
//
// State persists to localStorage:
//   - `surfbot_assets_filters`        — type/status/tags/search
//   - `surfbot_assets_active_target`  — last selected target id
//   - `surfbot_assets_expanded`       — set of expanded node ids (per target)
//
// Deep linking: `#/assets?id=Y` opens the detail pane for asset Y, expands the
// path from root, and scrolls it into view. `#/assets?type=subdomain` etc.
// All filtering happens client-side over the single `/assets/tree` payload.
const AssetsPage = {
  STORAGE_KEY: 'surfbot_assets_filters',
  STORAGE_VERSION: 1,
  STORAGE_KEY_ACTIVE_TARGET: 'surfbot_assets_active_target',
  STORAGE_KEY_EXPANDED: 'surfbot_assets_expanded',

  // Tree depth limit for visible rows. Past this we cut off and surface
  // "(N more)" so the DOM stays bounded — virtualization is P2.
  MAX_DEPTH: 5,

  // Tree state. _flatNodes maps id -> { node, parent, depth } so detail pane
  // computations (children-by-type, ancestor walk) don't re-traverse.
  _treeRoots: [],
  _flatNodes: new Map(),
  _expandedNodes: new Set(),
  _selectedAssetId: null,

  // Findings index keyed by asset_id. Single bulk fetch capped at 250
  // (server limit). Severity breakdown for the detail pane reads this map.
  _findingsByAsset: new Map(),
  _worstSevByAsset: new Map(),

  _filters: { type: '', status: '', tags: [], search: '' },
  _targets: [],
  _activeTargetID: '',
  _searchTimer: null,
  _filterMenu: null,

  async render(app, params) {
    const merged = this._mergeFiltersWithStorage(params || {});
    app.innerHTML = '<div class="loading">Loading assets...</div>';

    try {
      const targetsResp = await API.targets().catch(() => ({ targets: [] }));
      this._targets = (targetsResp && targetsResp.targets) || [];

      if (this._targets.length === 0) {
        app.innerHTML = `
          <div class="page-header"><h2>Assets</h2></div>
          ${Components.emptyState(
            'No targets configured',
            'Add a target before discovering assets.'
          )}
          <div style="text-align:center;margin-top:12px">
            <a href="#/targets" class="btn btn-primary">Add target</a>
          </div>
        `;
        return;
      }

      this._activeTargetID = this._resolveActiveTarget(merged);
      if (this._activeTargetID) {
        try { localStorage.setItem(this.STORAGE_KEY_ACTIVE_TARGET, this._activeTargetID); }
        catch (_) {}
      }

      const [treeResp, findingsResp] = await Promise.all([
        API.assetTree(this._activeTargetID),
        API.findings({ target_id: this._activeTargetID, limit: 250 }).catch(() => ({ findings: [] })),
      ]);

      this._treeRoots = (treeResp && treeResp.tree) || [];
      this._buildFindingsIndex((findingsResp && findingsResp.findings) || []);
      this._synthesizeHierarchy();
      this._buildFlatIndex();
      this._loadExpandedFromStorage();

      this._filters = {
        type: merged.type || '',
        status: merged.status || '',
        tags: this._parseTags(merged.tags),
        search: merged.search || '',
      };
      this._selectedAssetId = merged.id || null;

      if (this._selectedAssetId) {
        if (!this._flatNodes.has(this._selectedAssetId)) {
          Components.toast.error('Asset not found');
          this._mutate({ id: null });
          return;
        }
        this._expandPathTo(this._selectedAssetId);
      }

      app.innerHTML = this._template();
      this._wire(app);

      if (this._selectedAssetId) {
        const row = app.querySelector(`[data-asset-id="${cssEscape(this._selectedAssetId)}"]`);
        if (row && row.scrollIntoView) row.scrollIntoView({ block: 'center' });
      }
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load assets: ' + err.message);
    }
  },

  // ----- Storage / merge ----------------------------------------------

  _mergeFiltersWithStorage(params) {
    const p = Object.assign({}, params || {});
    const filterKeys = ['type', 'status', 'tags', 'search', 'target_id'];
    const hasURLFilter = filterKeys.some(k => p[k]);
    if (!hasURLFilter) {
      const stored = this._loadStored();
      if (stored) {
        for (const k of filterKeys) {
          if (stored[k] != null && stored[k] !== '') p[k] = stored[k];
        }
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

  _saveStored() {
    try {
      const f = this._filters;
      const out = {};
      if (f.type) out.type = f.type;
      if (f.status) out.status = f.status;
      if (f.tags && f.tags.length) out.tags = f.tags.join(',');
      if (f.search) out.search = f.search;
      if (this._activeTargetID) out.target_id = this._activeTargetID;
      localStorage.setItem(this.STORAGE_KEY, JSON.stringify({
        version: this.STORAGE_VERSION,
        filters: out,
        savedAt: new Date().toISOString(),
      }));
    } catch (_) {}
  },

  _clearStored() {
    try { localStorage.removeItem(this.STORAGE_KEY); } catch (_) {}
  },

  _loadExpandedFromStorage() {
    try {
      const raw = localStorage.getItem(this.STORAGE_KEY_EXPANDED + ':' + this._activeTargetID);
      if (raw) {
        const ids = JSON.parse(raw);
        if (Array.isArray(ids)) {
          this._expandedNodes = new Set(ids);
          return;
        }
      }
    } catch (_) {}
    // Default: expand top 2 levels.
    this._expandedNodes = new Set();
    const walk = (nodes, depth) => {
      if (depth > 1) return;
      nodes.forEach(n => {
        if (n.children && n.children.length) {
          this._expandedNodes.add(n.id);
          walk(n.children, depth + 1);
        }
      });
    };
    walk(this._treeRoots, 0);
  },

  _saveExpandedToStorage() {
    try {
      localStorage.setItem(
        this.STORAGE_KEY_EXPANDED + ':' + this._activeTargetID,
        JSON.stringify(Array.from(this._expandedNodes)),
      );
    } catch (_) {}
  },

  _parseTags(raw) {
    if (!raw) return [];
    if (Array.isArray(raw)) return raw.filter(Boolean);
    return String(raw).split(',').map(s => s.trim()).filter(Boolean);
  },

  _resolveActiveTarget(merged) {
    if (merged.target_id && this._targets.some(t => t.id === merged.target_id)) {
      return merged.target_id;
    }
    let stored = '';
    try { stored = localStorage.getItem(this.STORAGE_KEY_ACTIVE_TARGET) || ''; } catch (_) {}
    if (stored && this._targets.some(t => t.id === stored)) return stored;
    return this._targets[0] ? this._targets[0].id : '';
  },

  // ----- Index building ----------------------------------------------

  _buildFindingsIndex(findings) {
    const SEV_RANK = { critical: 5, high: 4, medium: 3, low: 2, info: 1 };
    this._findingsByAsset = new Map();
    this._worstSevByAsset = new Map();
    for (const f of findings) {
      const aid = f.asset_id;
      if (!aid) continue;
      if (!this._findingsByAsset.has(aid)) this._findingsByAsset.set(aid, []);
      this._findingsByAsset.get(aid).push(f);
      const cur = this._worstSevByAsset.get(aid) || 'info';
      if ((SEV_RANK[f.severity] || 0) > (SEV_RANK[cur] || 0)) {
        this._worstSevByAsset.set(aid, f.severity);
      }
    }
  },

  _buildFlatIndex() {
    this._flatNodes = new Map();
    const walk = (nodes, parent, depth) => {
      for (const n of nodes) {
        this._flatNodes.set(n.id, { node: n, parent, depth });
        if (n.children && n.children.length) walk(n.children, n, depth + 1);
      }
    };
    walk(this._treeRoots, null, 0);
  },

  // _synthesizeHierarchy infers parent links from asset value patterns when
  // the backend payload has every asset as a flat root (parent_id empty
  // across the board — detection tools don't populate it today). Runs only
  // when no real hierarchy exists, so once tools start setting parent_id
  // the synthesizer becomes a no-op.
  //
  // Rules (best-effort, no schema changes):
  //   port_service "host:port/proto"  → parent matches asset value === host
  //   url          "scheme://host…"   → parent matches asset value === host
  //   subdomain    "a.b.c"             → parent is the longest-suffix existing
  //                                      subdomain/domain shorter than self
  // ipv*/technology stay as roots — value carries no parent signal.
  _synthesizeHierarchy() {
    const roots = this._treeRoots;
    if (!roots.length) return;
    // Skip when ANY backend node already has children or any asset declares
    // parent_id. The presence of either signals the backend already wired
    // the tree and we should not second-guess it.
    for (const n of roots) {
      if ((n.children && n.children.length) || (n.parent_id && n.parent_id.length)) return;
    }

    const byValue = new Map();
    for (const n of roots) byValue.set(n.value, n);

    const findHostParent = (host) => {
      const hit = byValue.get(host);
      return hit && hit.type !== 'port_service' && hit.type !== 'url' ? hit : null;
    };

    const findParent = (n) => {
      if (n.type === 'port_service') {
        const m = String(n.value).match(/^([^:\/]+):/);
        if (m) return findHostParent(m[1]);
      } else if (n.type === 'url') {
        try {
          const u = new URL(n.value);
          return findHostParent(u.hostname);
        } catch (_) { return null; }
      } else if (n.type === 'subdomain' || n.type === 'domain') {
        const parts = String(n.value).split('.');
        for (let i = 1; i < parts.length; i++) {
          const candidate = parts.slice(i).join('.');
          if (candidate && candidate !== n.value && byValue.has(candidate)) {
            const p = byValue.get(candidate);
            if (p.type === 'subdomain' || p.type === 'domain') return p;
          }
        }
      }
      return null;
    };

    // Reset and re-attach.
    for (const n of roots) n.children = [];
    const newRoots = [];
    const TYPE_ORDER = { subdomain: 0, domain: 0, ipv4: 1, ipv6: 1, port_service: 2, url: 3, technology: 4, service: 5 };
    for (const n of roots) {
      const parent = findParent(n);
      if (parent && parent !== n) parent.children.push(n);
      else newRoots.push(n);
    }
    const sortChildren = (node) => {
      if (!node.children || !node.children.length) return;
      node.children.sort((a, b) => {
        const da = TYPE_ORDER[a.type] != null ? TYPE_ORDER[a.type] : 9;
        const db = TYPE_ORDER[b.type] != null ? TYPE_ORDER[b.type] : 9;
        if (da !== db) return da - db;
        return String(a.value).localeCompare(String(b.value));
      });
      node.children.forEach(sortChildren);
    };
    newRoots.sort((a, b) => {
      const da = TYPE_ORDER[a.type] != null ? TYPE_ORDER[a.type] : 9;
      const db = TYPE_ORDER[b.type] != null ? TYPE_ORDER[b.type] : 9;
      if (da !== db) return da - db;
      return String(a.value).localeCompare(String(b.value));
    });
    newRoots.forEach(sortChildren);
    this._treeRoots = newRoots;
  },

  _expandPathTo(assetId) {
    let entry = this._flatNodes.get(assetId);
    while (entry && entry.parent) {
      this._expandedNodes.add(entry.parent.id);
      entry = this._flatNodes.get(entry.parent.id);
    }
  },

  // ----- Counting / filtering ----------------------------------------

  // _typeCounts walks the tree and returns { [type]: count } for every
  // distinct AssetType present in the loaded dataset. Used to render
  // the tab counts; the "All" total is the sum of values.
  _typeCounts() {
    const counts = {};
    const walk = (nodes) => {
      for (const n of nodes) {
        counts[n.type] = (counts[n.type] || 0) + 1;
        if (n.children && n.children.length) walk(n.children);
      }
    };
    walk(this._treeRoots);
    return counts;
  },

  _allTags() {
    const set = new Set();
    for (const entry of this._flatNodes.values()) {
      const tags = entry.node.tags || [];
      tags.forEach(t => { if (t) set.add(t); });
    }
    return Array.from(set).sort();
  },

  // _matchesFilters returns true when the node itself matches every active
  // filter. _isVisible is the public test — it also returns true for any
  // ancestor whose subtree contains a match (so the path is preserved).
  _matchesFilters(node) {
    const f = this._filters;
    if (f.type && node.type !== f.type) return false;
    if (f.status && node.status !== f.status) return false;
    if (f.tags && f.tags.length) {
      const ntags = node.tags || [];
      if (!f.tags.some(t => ntags.includes(t))) return false;
    }
    if (f.search) {
      const q = f.search.toLowerCase();
      if (!String(node.value || '').toLowerCase().includes(q)) return false;
    }
    return true;
  },

  _hasVisibleDescendant(node) {
    if (!node.children || !node.children.length) return false;
    for (const c of node.children) {
      if (this._matchesFilters(c)) return true;
      if (this._hasVisibleDescendant(c)) return true;
    }
    return false;
  },

  _isVisible(node) {
    if (this._matchesFilters(node)) return true;
    return this._hasVisibleDescendant(node);
  },

  // ----- Template ----------------------------------------------------

  _template() {
    const counts = this._typeCounts();
    const total = Object.values(counts).reduce((a, b) => a + b, 0);
    const targetName = this._targetName(this._activeTargetID);

    return `
      <div class="page-header">
        <h2>Assets</h2>
        <p>${total.toLocaleString()} discovered${targetName ? ' · ' + escapeHtml(targetName) : ''}</p>
      </div>
      ${this._typeTabsHtml(counts, total)}
      ${this._filterStripHtml()}
      ${this._targetBarHtml()}
      <div class="assets-grid" data-assets-grid>
        <div class="assets-tree-card" role="region" aria-label="Asset tree">
          ${this._treeHtml()}
        </div>
        <div class="assets-pane-card" data-assets-pane role="region" aria-label="Asset details">
          ${this._paneHtml()}
        </div>
      </div>
    `;
  },

  _typeTabsHtml(counts, total) {
    const order = ['subdomain', 'port_service', 'url', 'ipv4', 'ipv6', 'technology', 'service', 'domain'];
    const present = order.filter(t => (counts[t] || 0) > 0);
    // Surface unknown types at the end so a future detection tool isn't invisible.
    Object.keys(counts).forEach(t => { if (!present.includes(t)) present.push(t); });
    const active = this._filters.type || '';

    const tab = (key, label, count) => {
      const isActive = key === active;
      const cls = 'sev-tab' + (isActive ? ' sev-tab-active' : '');
      return `<button type="button" class="${cls}" role="tab"
        aria-selected="${isActive}" data-asset-type="${escapeHtml(key)}">
        ${escapeHtml(label)}<span class="sev-tab-count">${count.toLocaleString()}</span>
      </button>`;
    };

    return `<div class="sev-tabs" role="tablist" aria-label="Filter by asset type">
      ${tab('', 'All', total)}
      ${present.map(t => tab(t, t, counts[t])).join('')}
    </div>`;
  },

  _filterStripHtml() {
    const f = this._filters;
    const chips = [];
    if (f.status) chips.push({ key: 'status', label: 'Status: ' + f.status });
    if (f.tags && f.tags.length) chips.push({ key: 'tags', label: 'Tags: ' + f.tags.join(', ') });

    const chipMountHtml = chips.map(c =>
      `<span class="filter-chip-mount" data-chip-key="${escapeHtml(c.key)}" data-chip-label="${escapeHtml(c.label)}"></span>`
    ).join('');

    return `<div class="filter-strip" role="group" aria-label="Active filters">
      <div class="filter-strip-chips" data-chip-strip>${chipMountHtml}</div>
      <button type="button" class="filter-add-btn" data-add-filter>
        ${Components.icon('plus', 14)}<span>Filter</span>
      </button>
      <span class="filter-strip-spacer"></span>
      <span class="assets-search-wrap">
        ${Components.icon('search', 14)}
        <input type="text" class="assets-search-input" data-assets-search
          placeholder="Search asset…"
          aria-label="Search assets by value"
          value="${escapeHtml(f.search || '')}" />
      </span>
    </div>`;
  },

  _targetBarHtml() {
    if (this._targets.length < 2) return '';
    const opts = this._targets.map(t =>
      `<option value="${escapeHtml(t.id)}"${t.id === this._activeTargetID ? ' selected' : ''}>${escapeHtml(t.value)}</option>`
    ).join('');
    return `<div class="assets-target-bar">
      <label class="form-label" for="assets-target-select">Target</label>
      <select id="assets-target-select" class="assets-target-select" data-assets-target aria-label="Select active target">
        ${opts}
      </select>
    </div>`;
  },

  _treeHtml() {
    if (this._treeRoots.length === 0) {
      return Components.emptyState(
        'No assets discovered',
        'Run a scan to populate this target.'
      ) + `<div style="text-align:center;margin-top:8px">
        <button type="button" class="btn btn-primary" data-run-first-scan>Run first scan</button>
      </div>`;
    }

    const visibleHtml = this._renderTreeNodes(this._treeRoots, 0);
    if (!visibleHtml) {
      return `<div class="assets-tree-empty">
        ${Components.emptyState('No assets match', 'Adjust filters to see more.')}
        <div style="text-align:center;margin-top:8px">
          <button type="button" class="btn btn-ghost" data-clear-filters>Clear filters</button>
        </div>
      </div>`;
    }

    return `<ul class="assets-tree" role="tree" aria-label="Assets hierarchy">${visibleHtml}</ul>`;
  },

  _renderTreeNodes(nodes, depth) {
    if (depth > this.MAX_DEPTH) {
      const truncated = this._countDescendants(nodes);
      if (truncated === 0) return '';
      return `<li class="tree-row tree-row-truncated" role="treeitem" style="padding-left:${16 + depth * 16}px">
        <span class="text-muted">… ${truncated} more nested asset${truncated !== 1 ? 's' : ''}</span>
      </li>`;
    }
    let html = '';
    for (const n of nodes) {
      if (!this._isVisible(n)) continue;
      html += this._renderTreeRow(n, depth);
    }
    return html;
  },

  _renderTreeRow(node, depth) {
    const hasChildren = node.children && node.children.length > 0;
    const expanded = hasChildren && this._expandedNodes.has(node.id);
    const selected = node.id === this._selectedAssetId;
    const findingCount = node.finding_count || 0;
    const worstSev = this._worstSevByAsset.get(node.id) || 'info';

    const toggleHtml = hasChildren
      ? `<button type="button" class="tree-toggle" data-tree-toggle="${escapeHtml(node.id)}"
            aria-label="${expanded ? 'Collapse' : 'Expand'}" aria-expanded="${expanded}">
            ${Components.icon(expanded ? 'chevron-down' : 'chevron-right', 14)}
          </button>`
      : '<span class="tree-toggle-spacer"></span>';

    const sevPillHtml = findingCount > 0
      ? `${Components.severityPill(worstSev)}
         <span class="tree-finding-count">${findingCount} finding${findingCount !== 1 ? 's' : ''}</span>`
      : '';

    const cls = 'tree-row' + (selected ? ' tree-row-selected' : '');
    const indent = 16 + depth * 16;

    let row = `<li class="${cls}" role="treeitem"
        data-asset-id="${escapeHtml(node.id)}"
        data-tree-row="${escapeHtml(node.id)}"
        aria-level="${depth + 1}"
        ${hasChildren ? `aria-expanded="${expanded}"` : ''}
        ${selected ? 'aria-selected="true"' : ''}
        style="padding-left:${indent}px">
        ${toggleHtml}
        <span class="tree-type-pill">${escapeHtml(node.type)}</span>
        <span class="tree-value">${escapeHtml(node.value)}</span>
        ${sevPillHtml}
      </li>`;

    if (hasChildren && expanded) {
      const childHtml = this._renderTreeNodes(node.children, depth + 1);
      if (childHtml) row += childHtml;
    }
    return row;
  },

  _countDescendants(nodes) {
    let n = 0;
    const walk = (arr) => {
      for (const x of arr) {
        n++;
        if (x.children && x.children.length) walk(x.children);
      }
    };
    walk(nodes);
    return n;
  },

  // ----- Detail pane --------------------------------------------------

  _paneHtml() {
    if (!this._selectedAssetId) {
      return `<div class="assets-pane-empty">
        <p>Select an asset from the tree to see its findings, ports, and tech stack.</p>
      </div>`;
    }
    const entry = this._flatNodes.get(this._selectedAssetId);
    if (!entry) {
      return `<div class="assets-pane-empty">
        <p>Asset not found.</p>
      </div>`;
    }
    const node = entry.node;
    const isHostLike = node.type === 'subdomain' || node.type === 'domain' ||
      node.type === 'ipv4' || node.type === 'ipv6';

    const findings = this._findingsByAsset.get(node.id) || [];
    const sevCounts = { critical: 0, high: 0, medium: 0, low: 0, info: 0 };
    findings.forEach(f => { if (sevCounts[f.severity] != null) sevCounts[f.severity]++; });
    const totalFindings = node.finding_count || findings.length;

    const ports = (node.children || []).filter(c => c.type === 'port_service');
    const techs = (node.children || []).filter(c => c.type === 'technology');

    const stats = [];
    stats.push(this._statCard('Findings',
      String(totalFindings),
      this._sevBreakdownLine(sevCounts)));
    if (isHostLike) {
      stats.push(this._statCard('Open ports', String(ports.length),
        ports.slice(0, 3).map(p => escapeHtml(p.value)).join(', ')));
      stats.push(this._statCard('Tech stack', String(techs.length),
        techs.slice(0, 3).map(t => escapeHtml(t.value)).join(', ') || 'none detected'));
    }

    const tagsHtml = (node.tags && node.tags.length)
      ? node.tags.map(t => `<span class="pill assets-tag-pill">${escapeHtml(t)}</span>`).join('')
      : '<span class="text-muted">No tags</span>';

    return `<div class="assets-detail" data-asset-detail="${escapeHtml(node.id)}">
      <div class="assets-detail-header">
        <span class="tree-type-pill">${escapeHtml(node.type)}</span>
        <span class="assets-detail-value mono">${escapeHtml(node.value)}</span>
        <button type="button" class="btn btn-ghost btn-sm" data-copy-value="${escapeHtml(node.value)}"
          aria-label="Copy asset value">${Components.icon('copy', 14)}</button>
      </div>
      <div class="assets-detail-meta">
        first seen ${escapeHtml(Components.timeAgo(node.first_seen))} ·
        last seen ${escapeHtml(Components.timeAgo(node.last_seen))} ·
        status <span class="mono">${escapeHtml(node.status || '-')}</span>
      </div>
      <div class="assets-detail-stats" style="grid-template-columns:repeat(${stats.length},minmax(0,1fr))">
        ${stats.join('')}
      </div>
      <div class="assets-detail-section">
        <div class="assets-detail-label">TAGS</div>
        <div class="assets-detail-tags">
          ${tagsHtml}
          <button type="button" class="btn btn-ghost btn-sm" disabled
            title="Tag editing coming with PATCH /assets/:id endpoint"
            aria-disabled="true">+ Add</button>
        </div>
      </div>
      <div class="assets-detail-actions">
        <button type="button" class="btn btn-primary" data-asset-action="run-scan">Run scan</button>
        <button type="button" class="btn btn-ghost" data-asset-action="blackout" disabled
          title="Coming with PR8 blackouts integration"
          aria-disabled="true">Add to blackout</button>
        <button type="button" class="btn btn-danger" data-asset-action="remove" disabled
          title="Manual asset deletion coming with vacuum endpoint"
          aria-disabled="true">Remove</button>
      </div>
    </div>`;
  },

  _statCard(label, value, sub) {
    return `<div class="assets-stat-card">
      <div class="assets-stat-label">${escapeHtml(label)}</div>
      <div class="assets-stat-value">${escapeHtml(value)}</div>
      ${sub ? `<div class="assets-stat-sub">${sub}</div>` : ''}
    </div>`;
  },

  _sevBreakdownLine(sevCounts) {
    const order = ['critical', 'high', 'medium', 'low', 'info'];
    const parts = order.filter(s => sevCounts[s] > 0)
      .map(s => `<span class="sev-pill-${s}">${sevCounts[s]} ${s}</span>`);
    return parts.length ? parts.join(' · ') : '<span class="text-muted">no breakdown</span>';
  },

  _targetName(id) {
    const t = this._targets.find(x => x.id === id);
    return t ? t.value : '';
  },

  // ----- Wiring ------------------------------------------------------

  _wire(app) {
    // Type tabs
    app.querySelectorAll('[data-asset-type]').forEach(btn => {
      btn.addEventListener('click', () => {
        const t = btn.getAttribute('data-asset-type') || '';
        this._mutate({ type: t || null });
      });
    });

    // Filter chips
    app.querySelectorAll('.filter-chip-mount').forEach(slot => {
      const key = slot.getAttribute('data-chip-key');
      const label = slot.getAttribute('data-chip-label');
      const chip = Components.filterChip(label, () => {
        if (key === 'tags') this._mutate({ tags: null });
        else this._mutate({ [key]: null });
      });
      slot.replaceWith(chip);
    });

    // + Filter button
    const addBtn = app.querySelector('[data-add-filter]');
    if (addBtn) addBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      this._openFilterMenu(addBtn);
    });

    // Search (debounced 150ms)
    const searchInput = app.querySelector('[data-assets-search]');
    if (searchInput) {
      searchInput.addEventListener('input', () => {
        if (this._searchTimer) clearTimeout(this._searchTimer);
        const v = searchInput.value;
        this._searchTimer = setTimeout(() => {
          this._mutate({ search: v || null });
        }, 150);
      });
    }

    // Target selector
    const targetSel = app.querySelector('[data-assets-target]');
    if (targetSel) targetSel.addEventListener('change', () => {
      const id = targetSel.value;
      try { localStorage.setItem(this.STORAGE_KEY_ACTIVE_TARGET, id); } catch (_) {}
      this._mutate({ target_id: id, id: null });
    });

    // Tree row click → select
    app.querySelectorAll('[data-tree-row]').forEach(row => {
      row.addEventListener('click', (e) => {
        if (e.target.closest('[data-tree-toggle]')) return;
        const id = row.getAttribute('data-tree-row');
        this._selectAsset(id);
      });
    });

    // Tree toggles
    app.querySelectorAll('[data-tree-toggle]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const id = btn.getAttribute('data-tree-toggle');
        this._toggleNode(id);
      });
    });

    // Detail pane copy button
    const copyBtn = app.querySelector('[data-copy-value]');
    if (copyBtn) copyBtn.addEventListener('click', () => {
      const v = copyBtn.getAttribute('data-copy-value') || '';
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(v).then(
          () => Components.toast.success('Copied'),
          () => Components.toast.error('Copy failed'),
        );
      }
    });

    // Run-scan CTA
    app.querySelectorAll('[data-asset-action="run-scan"]').forEach(btn => {
      btn.addEventListener('click', () => {
        if (typeof AdHocPage === 'undefined' || !AdHocPage.open) return;
        AdHocPage.open({ prefillTargetID: this._activeTargetID, lockTargetID: true });
      });
    });

    // Run-first-scan empty-state CTA
    const firstScanBtn = app.querySelector('[data-run-first-scan]');
    if (firstScanBtn) firstScanBtn.addEventListener('click', () => {
      if (typeof AdHocPage === 'undefined' || !AdHocPage.open) return;
      AdHocPage.open({ prefillTargetID: this._activeTargetID, lockTargetID: true });
    });

    // Clear-filters CTA
    const clearBtn = app.querySelector('[data-clear-filters]');
    if (clearBtn) clearBtn.addEventListener('click', () => this._resetFilters());

    // Tree keyboard nav: ↑/↓ move selection, ←/→ collapse/expand on row
    const treeEl = app.querySelector('.assets-tree');
    if (treeEl) treeEl.addEventListener('keydown', (e) => this._onTreeKey(e, treeEl));
    if (treeEl) treeEl.setAttribute('tabindex', '0');
  },

  // ----- Filter menu (Status / Tags) ---------------------------------

  _openFilterMenu(anchor) {
    this._closeFilterMenu();
    const f = this._filters;
    const menu = document.createElement('div');
    menu.className = 'filter-menu';
    menu.setAttribute('role', 'menu');
    const items = [];
    if (!f.status) items.push({ key: 'status', label: 'Status' });
    if (!f.tags || !f.tags.length) items.push({ key: 'tags', label: 'Tag' });
    items.push({ key: '__reset__', label: 'Reset filters', divider: true });

    menu.innerHTML = items.map(it => {
      if (it.divider) return `<div class="filter-menu-divider"></div>
        <button type="button" class="filter-menu-item filter-menu-reset" data-mi-key="${it.key}" role="menuitem">${escapeHtml(it.label)}</button>`;
      return `<button type="button" class="filter-menu-item" data-mi-key="${escapeHtml(it.key)}" role="menuitem">${escapeHtml(it.label)}</button>`;
    }).join('');
    document.body.appendChild(menu);
    this._positionMenu(menu, anchor);
    this._filterMenu = menu;

    menu.querySelectorAll('[data-mi-key]').forEach(btn => {
      btn.addEventListener('click', () => {
        const k = btn.getAttribute('data-mi-key');
        if (k === '__reset__') {
          this._closeFilterMenu();
          this._resetFilters();
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
      const opts = ['active', 'new', 'disappeared', 'returned', 'inactive', 'ignored'];
      menu.innerHTML = opts.map(v =>
        `<button type="button" class="filter-menu-item" data-mi-pick="${escapeHtml(v)}" role="menuitem">${escapeHtml(v)}</button>`
      ).join('');
    } else if (key === 'tags') {
      const tags = this._allTags();
      if (!tags.length) {
        menu.innerHTML = `<div class="filter-menu-empty">No tags in current view.</div>`;
      } else {
        menu.innerHTML = tags.map(t =>
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
        const v = btn.getAttribute('data-mi-pick');
        if (key === 'tags') this._mutate({ tags: v });
        else this._mutate({ [key]: v });
      });
    });

    setTimeout(() => {
      document.addEventListener('click', this._onMenuOutside);
      document.addEventListener('keydown', this._onMenuKey);
    }, 0);
  },

  _onMenuOutside: (e) => {
    if (!AssetsPage._filterMenu) return;
    if (!AssetsPage._filterMenu.contains(e.target)) AssetsPage._closeFilterMenu();
  },
  _onMenuKey: (e) => { if (e.key === 'Escape') AssetsPage._closeFilterMenu(); },

  _closeFilterMenu() {
    if (this._filterMenu && this._filterMenu.parentNode) this._filterMenu.remove();
    this._filterMenu = null;
    document.removeEventListener('click', this._onMenuOutside);
    document.removeEventListener('keydown', this._onMenuKey);
  },

  _positionMenu(menu, anchor) {
    const rect = anchor.getBoundingClientRect();
    menu.style.position = 'fixed';
    menu.style.top = (rect.bottom + 4) + 'px';
    menu.style.left = rect.left + 'px';
    menu.style.zIndex = '40';
  },

  // ----- Mutation / nav ----------------------------------------------

  _selectAsset(id) {
    this._mutate({ id: id });
  },

  _toggleNode(id) {
    if (this._expandedNodes.has(id)) this._expandedNodes.delete(id);
    else this._expandedNodes.add(id);
    this._saveExpandedToStorage();
    // In-place re-render of the tree card avoids losing the focused row.
    this._rerenderTreeOnly();
  },

  _rerenderTreeOnly() {
    const card = document.querySelector('.assets-tree-card');
    if (!card) return;
    card.innerHTML = this._treeHtml();
    // Re-wire row + toggle handlers within the card.
    card.querySelectorAll('[data-tree-row]').forEach(row => {
      row.addEventListener('click', (e) => {
        if (e.target.closest('[data-tree-toggle]')) return;
        this._selectAsset(row.getAttribute('data-tree-row'));
      });
    });
    card.querySelectorAll('[data-tree-toggle]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        this._toggleNode(btn.getAttribute('data-tree-toggle'));
      });
    });
    const treeEl = card.querySelector('.assets-tree');
    if (treeEl) {
      treeEl.setAttribute('tabindex', '0');
      treeEl.addEventListener('keydown', (e) => this._onTreeKey(e, treeEl));
    }
    const clearBtn = card.querySelector('[data-clear-filters]');
    if (clearBtn) clearBtn.addEventListener('click', () => this._resetFilters());
    const firstScanBtn = card.querySelector('[data-run-first-scan]');
    if (firstScanBtn) firstScanBtn.addEventListener('click', () => {
      if (typeof AdHocPage === 'undefined' || !AdHocPage.open) return;
      AdHocPage.open({ prefillTargetID: this._activeTargetID, lockTargetID: true });
    });
  },

  _resetFilters() {
    this._filters = { type: '', status: '', tags: [], search: '' };
    this._clearStored();
    this._mutate({ type: null, status: null, tags: null, search: null });
  },

  // _mutate is the single entry point for every state change that needs
  // the URL hash + storage updated together. Patch keys with value `null`
  // or `''` are dropped from the hash.
  _mutate(patch) {
    const cur = parseQueryParams();
    const next = Object.assign({}, cur, patch);
    Object.keys(next).forEach(k => {
      if (next[k] == null || next[k] === '' || (Array.isArray(next[k]) && !next[k].length)) {
        delete next[k];
      }
    });
    if (next.tags && Array.isArray(next.tags)) next.tags = next.tags.join(',');
    if (this._activeTargetID && !next.target_id) next.target_id = this._activeTargetID;

    // Persist filter snapshot (target_id stays in its own key).
    this._filters = {
      type: next.type || '',
      status: next.status || '',
      tags: this._parseTags(next.tags),
      search: next.search || '',
    };
    this._saveStored();

    const q = Object.entries(next).map(([k, v]) => k + '=' + encodeURIComponent(v)).join('&');
    location.hash = '#/assets' + (q ? '?' + q : '');
  },

  // ----- Keyboard nav ------------------------------------------------

  _onTreeKey(e, treeEl) {
    const rows = Array.from(treeEl.querySelectorAll('[data-tree-row]'));
    if (!rows.length) return;
    let idx = rows.findIndex(r => r.getAttribute('data-tree-row') === this._selectedAssetId);
    if (idx === -1) idx = 0;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      const next = rows[Math.min(idx + 1, rows.length - 1)];
      if (next) this._selectAsset(next.getAttribute('data-tree-row'));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      const prev = rows[Math.max(idx - 1, 0)];
      if (prev) this._selectAsset(prev.getAttribute('data-tree-row'));
    } else if (e.key === 'ArrowRight') {
      const id = this._selectedAssetId;
      if (id && !this._expandedNodes.has(id)) {
        const entry = this._flatNodes.get(id);
        if (entry && entry.node.children && entry.node.children.length) {
          e.preventDefault();
          this._toggleNode(id);
        }
      }
    } else if (e.key === 'ArrowLeft') {
      const id = this._selectedAssetId;
      if (id && this._expandedNodes.has(id)) {
        e.preventDefault();
        this._toggleNode(id);
      } else if (id) {
        const entry = this._flatNodes.get(id);
        if (entry && entry.parent) {
          e.preventDefault();
          this._selectAsset(entry.parent.id);
        }
      }
    }
  },
};
