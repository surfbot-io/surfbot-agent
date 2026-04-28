// Findings page — grouped-by-host default, toggle to raw per-asset view.
// PR4 (#37): rows open a slide-over instead of a detail page. The deep
// link #/findings/{id} renders the list and opens the panel on top.
const FindingsPage = {
  // _slide is the live slide-over handle while open, null otherwise.
  // _lastTrigger is the row element that opened it — focus returns there
  // on close so keyboard operators don't lose their place.
  _slide: null,
  _lastTrigger: null,
  _abortCtrl: null,
  _statusToPill: { open: 'open', acknowledged: 'ack', resolved: 'resolved', false_positive: 'fp', ignored: 'ignored' },

  async render(app, params) {
    // PR4: deep link #/findings/{id} renders the list, then opens the
    // panel. Legacy ?view=raw&host=…&template_id=… still routes to the
    // raw list (no slide-over auto-open).
    const view = params?.view || 'grouped';
    let listPromise;
    if (view === 'grouped') {
      listPromise = this.renderGrouped(app, params);
    } else {
      listPromise = this.renderRaw(app, params);
    }
    await listPromise;
    if (params && params.id) {
      // After the list paints, open the slide-over on top.
      this.openSlideoverFor(params.id);
    }
  },

  // --- Grouped view (default) ---

  async renderGrouped(app, params) {
    app.innerHTML = '<div class="loading">Loading findings...</div>';
    try {
      const data = await API.findingsGrouped({
        severity: params?.severity,
        tool: params?.tool,
        host: params?.host,
        sort: params?.sort || 'last_seen',
        page: params?.page || 1,
        limit: 50,
      });
      app.innerHTML = this.groupedTemplate(data, params);
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load findings: ' + err.message);
    }
  },

  groupedTemplate(data, params) {
    if (data.groups.length === 0 && !params?.severity && !params?.host) {
      return `
        <div class="page-header"><h2>Findings</h2></div>
        ${this.viewToggle('grouped')}
        ${Components.emptyState('No findings', 'No vulnerabilities detected. Run a scan to discover findings.')}
      `;
    }

    // Single-finding groups skip the raw-filtered drill: the row opens
    // the slide-over directly using finding_ids[0]. Multi-asset groups
    // still drill — picking which port/asset is meaningful when N>1.
    const rows = data.groups.map(g => {
      const badge = g.affected_assets_count > 1
        ? `<span class="port-badge">${g.affected_assets_count} ports</span>`
        : '';
      const single = g.affected_assets_count === 1
        && Array.isArray(g.finding_ids) && g.finding_ids.length === 1;
      let onclick;
      if (single) {
        onclick = `FindingsPage.handleGroupedRowClick(event, '${escapeHtml(g.finding_ids[0])}')`;
      } else {
        const drillHref = '#/findings?view=raw&host=' + encodeURIComponent(g.host)
          + '&template_id=' + encodeURIComponent(g.template_id);
        onclick = `location.hash='${drillHref}'`;
      }
      const dataAttr = single ? ` data-finding-id="${escapeHtml(g.finding_ids[0])}"` : '';
      return `
        <tr class="clickable"${dataAttr} onclick="${onclick}">
          <td>${Components.severityBadge(g.severity)}</td>
          <td class="text-truncate">${escapeHtml(g.title)} ${badge}</td>
          <td class="mono">${escapeHtml(g.host)}</td>
          <td class="mono">${escapeHtml(g.source_tool)}</td>
          <td class="mono text-muted">${Components.timeAgo(g.last_seen)}</td>
        </tr>
      `;
    }).join('');

    const totalPages = Math.ceil(data.total / data.limit);
    const page = data.page;

    return `
      <div class="page-header">
        <h2>Findings</h2>
        <p>${data.total} unique finding${data.total !== 1 ? 's' : ''}${params?.severity ? ' (' + params.severity + ')' : ''}</p>
      </div>
      ${this.viewToggle('grouped')}
      ${this.groupedFiltersHtml(params)}
      ${Components.table(['Severity', 'Title', 'Host', 'Tool', 'Last Seen'], [rows])}
      ${totalPages > 1 ? this.paginationHtml(page, totalPages, params) : ''}
    `;
  },

  groupedFiltersHtml(params) {
    const sev = params?.severity || '';
    const host = params?.host || '';
    return `<div class="filters">
      <select class="filter-select" id="filter-severity" onchange="FindingsPage.applyFilters()">
        <option value="">All severities</option>
        <option value="critical" ${sev==='critical'?'selected':''}>Critical</option>
        <option value="high" ${sev==='high'?'selected':''}>High</option>
        <option value="medium" ${sev==='medium'?'selected':''}>Medium</option>
        <option value="low" ${sev==='low'?'selected':''}>Low</option>
        <option value="info" ${sev==='info'?'selected':''}>Info</option>
      </select>
      <input class="filter-input" id="filter-host" placeholder="Filter by host..." value="${escapeHtml(host)}"
        onkeydown="if(event.key==='Enter')FindingsPage.applyFilters()" />
    </div>`;
  },

  // --- Raw view (per-asset findings) ---

  async renderRaw(app, params) {
    app.innerHTML = '<div class="loading">Loading findings...</div>';
    try {
      const data = await API.findings({
        severity: params?.severity,
        tool: params?.tool,
        status: params?.status,
        target_id: params?.target_id,
        scan_id: params?.scan_id,
        host: params?.host,
        template_id: params?.template_id,
        page: params?.page || 1,
        limit: 50,
      });
      app.innerHTML = this.rawTemplate(data, params);
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load findings: ' + err.message);
    }
  },

  rawTemplate(data, params) {
    if (data.findings.length === 0 && !params?.severity && !params?.status) {
      return `
        <div class="page-header"><h2>Findings</h2></div>
        ${this.viewToggle('raw')}
        ${Components.emptyState('No findings', 'No vulnerabilities detected. Run a scan to discover findings.')}
      `;
    }

    // PR4: row click opens slide-over. Inline event.target check stops
    // the click from firing when the operator is clicking the inline
    // status <select>. data-finding-id lets the row stash its id without
    // re-encoding the string into the onclick attribute.
    const rows = data.findings.map(f => `
      <tr class="clickable" data-finding-id="${escapeHtml(f.id)}" onclick="FindingsPage.handleRowClick(event, '${escapeHtml(f.id)}')">
        <td>${Components.severityBadge(f.severity)}</td>
        <td class="text-truncate">${escapeHtml(f.title)}</td>
        <td class="mono text-truncate">${escapeHtml(f.evidence || '')}</td>
        <td class="mono">${escapeHtml(f.source_tool)}</td>
        <td>
          <select class="status-select status-${f.status}" onchange="FindingsPage.changeStatus('${f.id}', this.value, this)" data-original="${f.status}">
            <option value="open" ${f.status==='open'?'selected':''}>open</option>
            <option value="acknowledged" ${f.status==='acknowledged'?'selected':''}>acknowledged</option>
            <option value="resolved" ${f.status==='resolved'?'selected':''}>resolved</option>
            <option value="false_positive" ${f.status==='false_positive'?'selected':''}>false positive</option>
            <option value="ignored" ${f.status==='ignored'?'selected':''}>ignored</option>
          </select>
        </td>
        <td class="mono text-muted">${Components.timeAgo(f.last_seen)}</td>
      </tr>
    `).join('');

    const totalPages = Math.ceil(data.total / data.limit);
    const page = data.page;

    return `
      <div class="page-header">
        <h2>Findings</h2>
        <p>${data.total} finding${data.total !== 1 ? 's' : ''}${params?.severity ? ' (' + params.severity + ')' : ''}</p>
      </div>
      ${this.viewToggle('raw')}
      ${this.rawFiltersHtml(params)}
      ${Components.table(['Severity', 'Title', 'Asset', 'Tool', 'Status', 'Last Seen'], [rows])}
      ${totalPages > 1 ? this.paginationHtml(page, totalPages, params) : ''}
    `;
  },

  rawFiltersHtml(params) {
    const sev = params?.severity || '';
    const status = params?.status || '';
    return `<div class="filters">
      <select class="filter-select" id="filter-severity" onchange="FindingsPage.applyFilters()">
        <option value="">All severities</option>
        <option value="critical" ${sev==='critical'?'selected':''}>Critical</option>
        <option value="high" ${sev==='high'?'selected':''}>High</option>
        <option value="medium" ${sev==='medium'?'selected':''}>Medium</option>
        <option value="low" ${sev==='low'?'selected':''}>Low</option>
        <option value="info" ${sev==='info'?'selected':''}>Info</option>
      </select>
      <select class="filter-select" id="filter-status" onchange="FindingsPage.applyFilters()">
        <option value="">All statuses</option>
        <option value="open" ${status==='open'?'selected':''}>Open</option>
        <option value="acknowledged" ${status==='acknowledged'?'selected':''}>Acknowledged</option>
        <option value="resolved" ${status==='resolved'?'selected':''}>Resolved</option>
        <option value="false_positive" ${status==='false_positive'?'selected':''}>False Positive</option>
        <option value="ignored" ${status==='ignored'?'selected':''}>Ignored</option>
      </select>
    </div>`;
  },

  // --- View toggle ---

  viewToggle(active) {
    const groupedCls = active === 'grouped' ? 'btn-accent' : 'btn-ghost';
    const rawCls = active === 'raw' ? 'btn-accent' : 'btn-ghost';
    return `<div class="view-toggle" style="margin-bottom:12px;display:flex;gap:8px">
      <button class="btn btn-sm ${groupedCls}" onclick="FindingsPage.switchView('grouped')">Group by host</button>
      <button class="btn btn-sm ${rawCls}" onclick="FindingsPage.switchView('raw')">Show all</button>
    </div>`;
  },

  switchView(view) {
    const params = parseQueryParams();
    const q = ['view=' + view];
    if (params.severity) q.push('severity=' + params.severity);
    const newHash = '#/findings?' + q.join('&');
    if (location.hash === newHash) {
      Router.navigate();
    } else {
      location.hash = newHash;
    }
  },

  // --- Shared helpers ---

  applyFilters() {
    const severity = document.getElementById('filter-severity')?.value || '';
    const status = document.getElementById('filter-status')?.value || '';
    const host = document.getElementById('filter-host')?.value || '';
    const params = parseQueryParams();
    const view = params.view || 'grouped';
    const q = ['view=' + view];
    if (severity) q.push('severity=' + severity);
    if (status) q.push('status=' + status);
    if (host) q.push('host=' + host);
    location.hash = '#/findings?' + q.join('&');
  },

  paginationHtml(page, totalPages, params) {
    let html = '<div style="display:flex;gap:8px;justify-content:center;margin-top:16px">';
    if (page > 1) {
      html += `<button class="refresh-btn" onclick="FindingsPage.goToPage(${page-1})">Prev</button>`;
    }
    html += `<span class="text-muted" style="padding:4px 8px">Page ${page} of ${totalPages}</span>`;
    if (page < totalPages) {
      html += `<button class="refresh-btn" onclick="FindingsPage.goToPage(${page+1})">Next</button>`;
    }
    html += '</div>';
    return html;
  },

  goToPage(page) {
    const hash = location.hash;
    const base = hash.split('?')[0];
    const params = parseQueryParams();
    params.page = page;
    const q = Object.entries(params).filter(([,v]) => v).map(([k,v]) => k+'='+v).join('&');
    location.hash = base + (q ? '?' + q : '');
  },

  async changeStatus(id, newStatus, selectEl) {
    const original = selectEl.dataset.original;
    try {
      await API.updateFindingStatus(id, newStatus);
      selectEl.dataset.original = newStatus;
      selectEl.className = 'status-select status-' + newStatus;
      // Refresh the sidebar findings count after a status flip — open
      // ↔ non-open transitions are exactly what the badge tracks.
      if (typeof loadSidebarBadge === 'function') loadSidebarBadge();
    } catch (err) {
      selectEl.value = original;
      Components.toast.error('Failed to update status: ' + err.message);
    }
  },

  // --- Slide-over (PR4 #37) ----------------------------------------

  // handleRowClick is the click handler for raw-view <tr>s. It guards
  // against firing when the operator clicked the inline status <select>
  // or any other interactive element nested in the row.
  handleRowClick(evt, id) {
    const t = evt.target;
    const tag = t && t.tagName;
    if (tag === 'SELECT' || tag === 'OPTION' || tag === 'INPUT' ||
        tag === 'BUTTON' || tag === 'A') return;
    this._lastTrigger = evt.currentTarget;
    // Setting hash drives the open via Router's findings transition
    // detector — keeps Back-button semantics aligned with the panel.
    location.hash = '#/findings/' + encodeURIComponent(id);
  },

  // handleGroupedRowClick is the equivalent for the grouped view when
  // the group has a single underlying finding. Multi-asset groups keep
  // the drill-through to the raw-filtered list because picking the
  // port/asset is the meaningful next step there.
  handleGroupedRowClick(evt, id) {
    this._lastTrigger = evt.currentTarget;
    location.hash = '#/findings/' + encodeURIComponent(id);
  },

  // openSlideoverFor is the entry point used by both the row click and
  // the deep-link route. It mounts the panel with a skeleton, fetches
  // the finding, and replaces the body. AbortController guards against
  // a stale render when the operator clicks a different row mid-fetch.
  async openSlideoverFor(id) {
    if (this._slide) {
      // Already open on a different finding: tear down the old panel
      // before mounting a new one so the focus-return pointer is fresh.
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
        // Focus return to the row that opened the panel — keyboard
        // operators rely on this to keep their place in the list.
        if (trigger && document.body.contains(trigger)) {
          try { trigger.focus(); } catch (_) {}
        }
        // Strip the /{id} segment from the hash so reopening from
        // history doesn't immediately re-mount the panel. Skip the
        // cleanup when 'replace' (we're swapping for another finding).
        if (reason !== 'replace' && /^#\/findings\/[^?]+/.test(location.hash)) {
          const params = parseQueryParams();
          const q = Object.entries(params).filter(([,v]) => v != null && v !== '').map(([k,v]) => k+'='+v).join('&');
          history.replaceState(null, '', '#/findings' + (q ? '?' + q : ''));
        }
      },
    });

    let data;
    try {
      data = await API.finding(id);
    } catch (err) {
      if (ctrl.signal.aborted) return;
      // 404: the finding does not exist (or was deleted). Close the
      // panel, surface a non-blocking toast, and clean the URL so a
      // refresh does not loop on the same error.
      if (err.status === 404) {
        if (this._slide) this._slide.close('not-found');
        Components.toast.error('Finding ' + id + ' not found');
        history.replaceState(null, '', '#/findings');
      } else {
        // 5xx or network: leave the panel open and offer a retry.
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

  // _renderSlideoverBody builds the full panel interior: header pills +
  // title + asset, action bar, tab strip, and the four tab panels. The
  // panel content all comes from the single GET /findings/{id} call —
  // tab switches do not re-fetch.
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
    // Update the panel title for screen readers — the helper builds
    // .slide-over-title from the title arg of slideOver({title:…}); we
    // patch it post-mount with the real finding title.
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
    // Visibility matrix per status — see issue #37 P0.4. Map button
    // class hints to so-action-* modifiers so the colors match status
    // tokens without restating them in the call site.
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
    // Comments tab is intentionally absent (#37 Non-goals #4). aria-
    // selected is updated by _wireTabs on activation.
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
          // 1. Mutate the local model and refresh in-place: header pill
          //    + action bar + the row in the list behind. No re-fetch.
          f.status = newStatus;
          this._refreshHeaderStatusPill(root, newStatus);
          bar.outerHTML = this._actionBarHtml(f);
          this._wireActions(root, f);
          this._refreshListRow(f.id, newStatus);
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
    // Status pill is the second .pill in the header row.
    const pills = row.querySelectorAll('.pill');
    if (pills.length >= 2) {
      pills[1].outerHTML = Components.statusPill(pillKey);
    }
  },

  _refreshListRow(id, newStatus) {
    // The list lives outside the panel. Find the <tr> by its
    // data-finding-id and patch the inline <select> + the className
    // tokens that drive the pill color.
    const row = document.querySelector(`tr[data-finding-id="${cssAttrEscape(id)}"]`);
    if (!row) return;
    const sel = row.querySelector('.status-select');
    if (sel) {
      sel.value = newStatus;
      sel.dataset.original = newStatus;
      sel.className = 'status-select status-' + newStatus;
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

  // --- Tab content ------------------------------------------------

  _overviewHtml(f) {
    // CVSS card. Range → token. Empty / 0 → em-dash.
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

    // Confidence card.
    const conf = (f.confidence == null || f.confidence === 0)
      ? '<span class="so-card-value-empty">—</span>'
      : `<span class="so-card-value">${Math.round(f.confidence)}%</span>`;
    const confCard = this._cardHtml('Confidence', conf, '');

    // EPSS column omitted (model.Finding has no EPSS field — see #37
    // Non-goals #6). Two cards collapse to a 2-col grid.
    const cards = `<div class="so-cards" style="--so-card-cols:2">${cvssCard}${confCard}</div>`;

    const description = f.description
      ? `<div class="so-section">
          <h3 class="so-section-label">Description</h3>
          <p class="so-prose">${escapeHtml(f.description)}</p>
        </div>`
      : '';

    const refs = this._referencesHtml(f);

    // Metadata footer (template + first/last seen) — the wireframe
    // collapses the legacy detail-grid into a smaller card. Useful for
    // operators who want the canonical IDs at a glance.
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
          // Mixed string with a CVE id inside — render the CVE link plus
          // a stripped-text remainder as plain.
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

    // 1. JSON shape: try to parse; if it has request/response keys use
    //    the dual-block format, otherwise pretty-print the object.
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

    // 2. Plain text — preformatted with a header label.
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

    // Numbered-list parser: if every non-empty line starts with "1." /
    // "2)" / etc., render as <ol>. Otherwise prose.
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

  // _inlineCode escapes HTML and then promotes single-backtick spans to
  // <code> — a deliberate ASCII-only mini parser, NOT markdown. Run on
  // already-escaped text so the regex never sees raw HTML.
  _inlineCode(text) {
    return escapeHtml(text).replace(/`([^`]+)`/g, (_m, g) => `<code>${g}</code>`);
  },

  _timelineHtml(f) {
    // Events derive from the model — there is no finding_status_history
    // table yet (see #37 P2.1). resolved_at is the only status-related
    // event we can reliably show.
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

  // closeSlideover is invoked by app.js when the hash transitions away
  // from #/findings/{id} (back button, manual edit, etc.). Idempotent.
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
