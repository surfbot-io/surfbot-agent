// PR9 #42 — Cmd+K command palette controller + shared LookupCache.
//
// LookupCache memoizes /targets and /templates fetches across pages.
// SchedulesPage already had a per-page cache; this cross-page version
// is what the AdHoc combobox upgrade and the palette query for value
// resolution. Invalidation is best-effort: callers that mutate
// targets/templates (`createTarget`, `deleteTarget`, etc.) call
// LookupCache.invalidate() themselves; otherwise the cache lives for
// the session.
const LookupCache = {
  _targets: null,    // Map<id, target>
  _templates: null,  // Map<id, template>
  _targetsP: null,
  _templatesP: null,

  async targets() {
    if (this._targets) return this._targets;
    if (!this._targetsP) {
      this._targetsP = API.targets()
        .then(resp => {
          const m = new Map();
          // /api/v1/targets returns {targets:[…]}; some test harnesses
          // return a bare array — accept both shapes.
          const list = Array.isArray(resp)
            ? resp
            : (resp && (resp.targets || resp.items)) || [];
          list.forEach(t => { if (t && t.id) m.set(t.id, t); });
          this._targets = m;
          return m;
        })
        .catch(err => { this._targetsP = null; throw err; });
    }
    return this._targetsP;
  },

  async templates() {
    if (this._templates) return this._templates;
    if (!this._templatesP) {
      this._templatesP = API.listTemplates({ limit: 500 })
        .then(resp => {
          const m = new Map();
          // /api/v1/templates returns the shared PaginatedResponse
          // ({items:[…]}). Same defensive fallback as targets().
          const list = Array.isArray(resp)
            ? resp
            : (resp && (resp.items || resp.templates)) || [];
          list.forEach(t => { if (t && t.id) m.set(t.id, t); });
          this._templates = m;
          return m;
        })
        .catch(err => { this._templatesP = null; throw err; });
    }
    return this._templatesP;
  },

  invalidate(kind) {
    if (!kind || kind === 'targets')   { this._targets = null;   this._targetsP = null; }
    if (!kind || kind === 'templates') { this._templates = null; this._templatesP = null; }
  },

  // resolveTargetID maps a user-typed string (target value or UUID) to a
  // canonical target_id. Returns '' when typed is empty, the resolved id
  // on a successful lookup, or null when nothing matches. The caller
  // surfaces a field error on null.
  resolveTargetID(typed, targets) {
    const t = (typed || '').trim();
    if (!t) return '';
    if (!targets) return null;
    if (targets.has(t)) return t; // pasted UUID
    for (const tg of targets.values()) {
      if (tg.value === t) return tg.id;
    }
    return null;
  },

  resolveTemplateID(typed, templates) {
    const t = (typed || '').trim();
    if (!t) return '';
    if (!templates) return null;
    if (templates.has(t)) return t;
    for (const tp of templates.values()) {
      if ((tp.name || tp.value) === t) return tp.id;
    }
    return null;
  },
};

// CommandPalette is the controller behind Cmd+K. It owns:
//   - the open()/close() lifecycle (single instance)
//   - the parallel fetch of /findings, /assets, /scans on open
//   - the in-memory recents (last 3 navigations, session-only — P1
//     upgrade to localStorage is a follow-up)
//   - the fuzzy filtering + scoring
//   - the keyboard nav (↑/↓/↵/Esc) on top of Components.commandPalette
const CommandPalette = {
  _instance: null,
  _recents: [],    // [{ type, label, sublabel, hash, icon }]
  _state: null,    // { findings, assets, scans, query, flat, activeIdx }

  isOpen() { return !!this._instance; },

  open() {
    if (this._instance) return;

    const state = {
      findings: { loading: true, items: [], error: null },
      assets:   { loading: true, items: [], error: null },
      scans:    { loading: true, items: [], error: null },
      query: '',
      flat: [],     // flat list of currently-rendered selectable items
      activeIdx: 0, // index into flat
    };
    this._state = state;

    const palette = Components.commandPalette({
      onInput: (q) => {
        state.query = q;
        this._render();
      },
      onAction: (idx) => this._activate(idx),
      onClose: () => { this._instance = null; this._state = null; },
      footerHint: 'F5 to refresh',
    });

    // Global keys for ↑/↓/↵/Esc. Bound on document while the palette is
    // open so the input keeps its native typing behavior unless we
    // intercept (arrows + enter + escape).
    this._onKey = (e) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        palette.close('dismiss');
        return;
      }
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        const flat = state.flat;
        if (!flat.length) return;
        let i = state.activeIdx;
        if (e.key === 'ArrowDown') i = (i + 1) % flat.length;
        else                       i = (i - 1 + flat.length) % flat.length;
        state.activeIdx = i;
        palette.setActive(flat[i].idx);
        return;
      }
      if (e.key === 'Enter') {
        const flat = state.flat;
        if (!flat.length) return;
        e.preventDefault();
        const cur = flat[state.activeIdx];
        if (cur) this._activate(cur.idx);
        return;
      }
    };
    document.addEventListener('keydown', this._onKey, true);

    // Replace onClose to also clean up the keydown listener.
    const innerClose = palette.close;
    palette.close = (reason) => {
      document.removeEventListener('keydown', this._onKey, true);
      innerClose(reason);
    };

    this._instance = palette;
    this._render();

    // Fire the three fetches in parallel. Each updates its slice in
    // state and re-renders. Failures show "Failed to load." in that
    // block but don't block the others. The webui handlers return
    // {findings:[]}, {assets:[]}, {scans:[]} — there is no shared
    // envelope, so each fetch reads its own key (with a defensive
    // fallback to a bare array in case the shape changes).
    function asArray(resp, key) {
      if (Array.isArray(resp)) return resp;
      if (resp && Array.isArray(resp[key])) return resp[key];
      return [];
    }
    API.findings({ limit: 50 })
      .then(resp => { state.findings = { loading: false, items: asArray(resp, 'findings') }; })
      .catch(() => { state.findings = { loading: false, items: [], error: 'Failed to load.' }; })
      .finally(() => this._render());
    API.assets({ limit: 50 })
      .then(resp => { state.assets = { loading: false, items: asArray(resp, 'assets') }; })
      .catch(() => { state.assets = { loading: false, items: [], error: 'Failed to load.' }; })
      .finally(() => this._render());
    API.scans({ limit: 20 })
      .then(resp => { state.scans = { loading: false, items: asArray(resp, 'scans') }; })
      .catch(() => { state.scans = { loading: false, items: [], error: 'Failed to load.' }; })
      .finally(() => this._render());

    // Warm the lookup cache so resolved target/asset values are
    // available when we render finding sublabels.
    LookupCache.targets().catch(() => {});
  },

  close(reason) {
    if (!this._instance) return;
    this._instance.close(reason || 'close');
  },

  // _render rebuilds the source list from current state + query.
  _render() {
    if (!this._instance || !this._state) return;
    const s = this._state;
    const q = (s.query || '').trim().toLowerCase();

    // Build raw items per category.
    const findingsRaw = (s.findings.items || []).map(f => {
      const subParts = [];
      if (f.severity) subParts.push(String(f.severity));
      if (f.cve) subParts.push(f.cve);
      const tail = f.template_id ? f.template_id : '';
      return {
        type: 'finding',
        label: f.title || f.cve || f.template_id || f.id,
        sublabel: subParts.join(' · ') + (tail ? ' · ' + tail : ''),
        hash: '#/findings/' + encodeURIComponent(f.id),
        icon: 'shield',
        sortKey: f.last_seen || f.updated_at || f.created_at || '',
      };
    });

    const assetsRaw = (s.assets.items || []).map(a => ({
      type: 'asset',
      label: a.value || a.id,
      sublabel: (a.type || '') + (a.status ? ' · ' + a.status : ''),
      hash: '#/assets?id=' + encodeURIComponent(a.id),
      icon: 'cube',
      sortKey: a.last_seen || a.updated_at || a.created_at || '',
    }));

    const scansRaw = (s.scans.items || []).map(sc => ({
      type: 'scan',
      label: (sc.target_value || sc.target || sc.target_id || sc.id) + ' · ' + (sc.status || ''),
      sublabel: (sc.type || '') + (sc.started_at ? ' · ' + new Date(sc.started_at).toLocaleString() : ''),
      hash: '#/scans/' + encodeURIComponent(sc.id),
      icon: 'play',
      sortKey: sc.started_at || sc.created_at || '',
    }));

    const actionsRaw = [
      { type: 'action', actionId: 'run-scan', label: 'Run scan on…',
        sublabel: 'Open the ad-hoc dispatcher (Cmd+R)', icon: 'play', hash: '' },
      { type: 'action', actionId: 'view-running', label: 'View running scans',
        sublabel: 'Filter to status=running', icon: 'list', hash: '#/scans?status=running' },
      { type: 'action', actionId: 'pause-all', label: 'Pause all active schedules',
        sublabel: 'Coming in PR10 follow-up', icon: 'pause', hash: '',
        disabled: true, tooltip: 'Coming in PR10 follow-up' },
    ];

    // Filter + score per category. Empty query → keep everything,
    // sorted by sortKey desc; non-empty → fuzzy substring + position +
    // length-penalty scoring (in-house, ~30 LOC).
    function scoreItem(item, qLower) {
      if (!qLower) return 0;
      const lbl = (item.label || '').toLowerCase();
      const sub = (item.sublabel || '').toLowerCase();
      let pos = lbl.indexOf(qLower);
      let base = 0;
      if (pos === 0) base = 100;
      else if (pos > 0) base = 50;
      else {
        pos = sub.indexOf(qLower);
        if (pos === 0) base = 60;
        else if (pos > 0) base = 30;
        else return -1;
      }
      const penalty = Math.min(50, Math.floor((item.label || '').length / 4));
      return base - penalty;
    }
    function pickTopN(arr, n) {
      if (!q) {
        const sorted = arr.slice().sort((a, b) => String(b.sortKey).localeCompare(String(a.sortKey)));
        return sorted.slice(0, n);
      }
      const scored = [];
      for (const it of arr) {
        const s = scoreItem(it, q);
        if (s >= 0) scored.push({ it, s });
      }
      scored.sort((a, b) => b.s - a.s);
      return scored.slice(0, n).map(x => x.it);
    }

    const findingsTop = pickTopN(findingsRaw, 5);
    const assetsTop   = pickTopN(assetsRaw, 5);
    const scansTop    = pickTopN(scansRaw, 5);
    // Quick actions: filter by query but keep all 3 visible when q
    // empty; an empty query also surfaces recents.
    const actionsTop = q
      ? actionsRaw.filter(a => scoreItem(a, q) >= 0)
      : actionsRaw;

    const recentsTop = !q
      ? this._recents.slice(0, 3)
      : this._recents.filter(r => scoreItem(r, q) >= 0).slice(0, 3);

    // Resolve sources order. Recents only show on empty query (or matching).
    const sources = [];
    if (recentsTop.length) {
      sources.push({ type: 'recent', label: 'Recents', items: recentsTop });
    }
    sources.push({
      type: 'finding', label: 'Findings',
      loading: s.findings.loading, error: s.findings.error,
      items: findingsTop,
      emptyMessage: s.findings.loading || s.findings.error ? '' : 'No matches',
    });
    sources.push({
      type: 'asset', label: 'Assets',
      loading: s.assets.loading, error: s.assets.error,
      items: assetsTop,
      emptyMessage: s.assets.loading || s.assets.error ? '' : 'No matches',
    });
    sources.push({
      type: 'scan', label: 'Scans',
      loading: s.scans.loading, error: s.scans.error,
      items: scansTop,
      emptyMessage: s.scans.loading || s.scans.error ? '' : 'No matches',
    });
    sources.push({ type: 'action', label: 'Quick actions', items: actionsTop });

    // Build flat selectable list (skip loading/error blocks) so ↑/↓ can
    // walk a single linear list. idx encodes "<sourceIdx>.<itemIdx>" to
    // match the data attribute the helper renders.
    const flat = [];
    sources.forEach((src, sIdx) => {
      if (src.loading || src.error || !src.items) return;
      src.items.forEach((it, iIdx) => {
        flat.push({ idx: `${sIdx}.${iIdx}`, item: it });
      });
    });
    s.flat = flat;
    if (s.activeIdx >= flat.length) s.activeIdx = 0;

    this._instance.setSources(sources);
    if (flat.length) this._instance.setActive(flat[s.activeIdx].idx);
  },

  _activate(idxStr) {
    const flat = this._state && this._state.flat;
    if (!flat || !flat.length) return;
    const entry = flat.find(f => f.idx === idxStr);
    if (!entry) return;
    const item = entry.item;
    if (item.disabled) return;

    // Quick actions with a synthetic actionId have custom dispatch.
    if (item.type === 'action') {
      this._dispatchAction(item.actionId);
    } else if (item.hash) {
      this._addRecent(item);
      location.hash = item.hash;
    }
    this.close('action');
  },

  _dispatchAction(id) {
    if (id === 'run-scan' && typeof AdHocPage !== 'undefined') {
      AdHocPage.open();
      return;
    }
    if (id === 'view-running') {
      location.hash = '#/scans?status=running';
      return;
    }
    // pause-all is disabled in P0; no-op.
  },

  _addRecent(item) {
    if (!item || !item.hash) return;
    // Dedupe by hash, keep most-recent at front, cap at 3.
    const filtered = this._recents.filter(r => r.hash !== item.hash);
    filtered.unshift({
      type: item.type, label: item.label, sublabel: item.sublabel,
      hash: item.hash, icon: item.icon,
    });
    this._recents = filtered.slice(0, 3);
  },
};
