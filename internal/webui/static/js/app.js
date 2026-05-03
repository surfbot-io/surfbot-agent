// Hash-based SPA router
const Router = {
  routes: [
    { pattern: /^#?\/?$/, page: 'dashboard', render: () => DashboardPage.render(app) },
    // PR4 #37: deep link with id MUST come before the list route. The
    // capture stops at `?` so #/findings/abc?status=open routes to the
    // list+slide-over for abc with the query preserved on the listing.
    { pattern: /^#\/findings\/([^?]+)(\?.*)?$/, page: 'findings', render: (m) => FindingsPage.render(app, Object.assign(parseQueryParams(), { id: decodeURIComponent(m[1]) })) },
    { pattern: /^#\/findings/, page: 'findings', render: () => FindingsPage.render(app, parseQueryParams()) },
    { pattern: /^#\/assets/, page: 'assets', render: () => AssetsPage.render(app, parseQueryParams()) },
    // PR7 #40: scan-detail lives in its own module (ScanDetailPage).
    // The list route below stays on ScansPage. Detail captures stop at
    // `?` so a future query string can ride along without the id
    // swallowing it.
    { pattern: /^#\/scans\/([^?]+)(\?.*)?$/, page: 'scans', render: (m) => ScanDetailPage.render(app, decodeURIComponent(m[1])) },
    { pattern: /^#\/scans/, page: 'scans', render: () => ScansPage.render(app, parseQueryParams()) },
    { pattern: /^#\/targets\/(.+)$/, page: 'targets', render: (m) => TargetsPage.render(app, { id: m[1] }) },
    { pattern: /^#\/targets/, page: 'targets', render: () => TargetsPage.render(app) },
    { pattern: /^#\/tools/, page: 'tools', render: () => ToolsPage.render(app) },
    // SPEC-SCHED1.4a: first-class schedule resources. Detail routes are
    // the `(.+)$` variants and must come BEFORE the list route so the
    // longest-prefix match wins. No Go-side route changes — the SPA
    // fallback in server.go already serves index.html for any unknown
    // path, so these hash routes resolve entirely client-side.
    { pattern: /^#\/timeline/, page: 'timeline', render: () => TimelinePage.render(app, parseQueryParams()) },
    { pattern: /^#\/schedules\/(.+)$/, page: 'schedules', render: (m) => SchedulesPage.render(app, { id: decodeURIComponent(m[1]) }) },
    { pattern: /^#\/schedules/, page: 'schedules', render: () => SchedulesPage.render(app, parseQueryParams()) },
    { pattern: /^#\/templates\/(.+)$/, page: 'templates', render: (m) => TemplatesPage.render(app, { id: decodeURIComponent(m[1]) }) },
    { pattern: /^#\/templates/, page: 'templates', render: () => TemplatesPage.render(app, parseQueryParams()) },
    { pattern: /^#\/blackouts\/(.+)$/, page: 'blackouts', render: (m) => BlackoutsPage.render(app, { id: decodeURIComponent(m[1]) }) },
    { pattern: /^#\/blackouts/, page: 'blackouts', render: () => BlackoutsPage.render(app, parseQueryParams()) },
    { pattern: /^#\/changes/, page: 'changes', render: () => ChangesPage.render(app, parseQueryParams()) },
    // PR8 #41: legacy URL — redirect transparent. Replace the hash so
    // the redirect doesn't create a back-button trap and the canonical
    // /settings/scan-defaults section opens.
    { pattern: /^#\/settings\/defaults\b/, page: 'settings', render: () => { location.replace('#/settings/scan-defaults'); } },
    { pattern: /^#\/settings\/([^/?]+)(\?.*)?$/, page: 'settings', render: (m) => SettingsPage.render(app, { section: decodeURIComponent(m[1]) }) },
    { pattern: /^#\/settings(\?.*)?$/, page: 'settings', render: () => SettingsPage.render(app, {}) },
  ],

  // _lastHash captures the previous hash so the navigate() short-circuit
  // can detect findings list-vs-slide-over toggles without re-rendering
  // the list. Set on every navigate() call.
  _lastHash: '',

  navigate() {
    const hash = location.hash || '#/';
    const app = document.getElementById('app');

    // PR4 #37: when transitioning between #/findings and
    // #/findings/{id} with identical query params, the only thing that
    // changes is whether the slide-over is open. Re-rendering the list
    // would flash and lose scroll/selection state, so short-circuit.
    if (this._isFindingsSlideoverToggle(this._lastHash, hash)) {
      this._lastHash = hash;
      this._syncFindingsSlideover(hash);
      return;
    }
    this._lastHash = hash;

    // Update active nav link
    document.querySelectorAll('.nav-link').forEach(link => {
      link.classList.remove('active');
      const page = link.getAttribute('data-page');
      if (hash === '#/' && page === 'dashboard') link.classList.add('active');
      else if (page !== 'dashboard' && hash.startsWith('#/' + page)) link.classList.add('active');
    });

    // PR2 #35: refresh breadcrumbs in the topbar based on the new hash.
    // Mapping is hash-prefix → [groupLabel, pageLabel]. Detail routes
    // inherit their list parent (e.g. #/findings/abc → "Findings").
    Breadcrumbs.update(hash);

    // Stop the AgentCard *dashboard* poll loop on every navigation. The
    // sidebar compact AgentCard runs on its own loop (mountCompact) and
    // is not affected.
    if (typeof AgentCard !== 'undefined' && AgentCard.unmount) {
      AgentCard.unmount();
    }

    // Slide-overs from one page should not survive cross-page nav.
    if (typeof FindingsPage !== 'undefined' && FindingsPage.closeSlideover) {
      FindingsPage.closeSlideover();
    }

    for (const route of this.routes) {
      const match = hash.match(route.pattern);
      if (match) {
        route.render(match);
        return;
      }
    }

    app.innerHTML = Components.emptyState('Page not found', 'The requested page does not exist.');
  },

  // _isFindingsSlideoverToggle returns true when prev and cur both live
  // under #/findings, share the same query string, and differ only by
  // the trailing /{id} segment (open or close). The path comparison
  // uses split('?') so query order is preserved verbatim.
  _isFindingsSlideoverToggle(prev, cur) {
    if (!prev || !cur) return false;
    const a = this._parseFindings(prev);
    const b = this._parseFindings(cur);
    if (!a || !b) return false;
    if (a.query !== b.query) return false;
    return a.id !== b.id;
  },

  _parseFindings(hash) {
    const m = hash.match(/^#\/findings(\/[^?]+)?(\?.*)?$/);
    if (!m) return null;
    return {
      id: m[1] ? decodeURIComponent(m[1].slice(1)) : '',
      query: m[2] || '',
    };
  },

  _syncFindingsSlideover(hash) {
    const parsed = this._parseFindings(hash);
    if (!parsed) return;
    if (typeof FindingsPage === 'undefined') return;
    if (parsed.id) {
      FindingsPage.openSlideoverFor(parsed.id);
    } else {
      FindingsPage.closeSlideover();
    }
  },
};

// PR2 #35: breadcrumbs in the topbar. Each list-route hash maps to a
// `[group, page]` pair; the rendered crumb is `Surfbot / <group> /
// <page>`. Detail routes (#/findings/abc-123) reuse the list crumb so
// the user always sees the same path while drilling in.
const Breadcrumbs = {
  MAP: {
    '/':           ['Insights',  'Dashboard'],
    '/findings':   ['Triage',    'Findings'],
    '/assets':     ['Discover',  'Assets'],
    '/targets':    ['Discover',  'Targets'],
    '/changes':    ['Discover',  'Changes'],
    '/scans':      ['Detect',    'Scans'],
    '/schedules':  ['Detect',    'Schedules'],
    '/templates':  ['Detect',    'Templates'],
    '/timeline':   ['Detect',    'Timeline'],
    '/tools':      ['Configure', 'Tools'],
    '/blackouts':  ['Configure', 'Blackouts'],
    '/settings':   ['Configure', 'Settings'],
  },

  resolve(hash) {
    const path = (hash || '#/').replace(/^#/, '').split('?')[0];
    if (path === '/' || path === '') return this.MAP['/'];
    // Match the longest prefix so /findings/abc inherits /findings.
    const keys = Object.keys(this.MAP).filter(k => k !== '/');
    keys.sort((a, b) => b.length - a.length);
    for (const k of keys) {
      if (path === k || path.startsWith(k + '/')) return this.MAP[k];
    }
    return null;
  },

  update(hash) {
    const root = document.getElementById('breadcrumbs');
    if (!root) return;
    const crumbs = ['Surfbot'];
    const resolved = this.resolve(hash);
    if (resolved) crumbs.push(resolved[0], resolved[1]);
    const parts = [];
    crumbs.forEach((c, i) => {
      const last = i === crumbs.length - 1;
      const cls = last ? 'breadcrumb-item breadcrumb-current' : 'breadcrumb-item';
      parts.push(`<span class="${cls}">${escapeHtml(c)}</span>`);
      if (!last) parts.push('<span class="breadcrumb-sep" aria-hidden="true">/</span>');
    });
    root.innerHTML = parts.join('');
  },
};

// PR2 #35: topbar scan indicator. Polls /scans/status every POLL_MS
// regardless of route so the user sees scan state from any page. Hides
// when no scan is running. Errors are swallowed — the indicator is a
// nice-to-have, not a blocker, and the scans page already surfaces
// fetch errors.
const ScanIndicator = {
  POLL_MS: 5000,
  _timer: null,

  start() {
    this.refresh();
    if (this._timer) clearInterval(this._timer);
    this._timer = setInterval(() => this.refresh(), this.POLL_MS);
  },

  async refresh() {
    try {
      const s = await API.scanStatus();
      this.update(s);
    } catch (_) {
      this.update(null);
    }
  },

  update(status) {
    const el = document.getElementById('scan-indicator');
    const targetEl = document.getElementById('scan-indicator-target');
    const toolEl = document.getElementById('scan-indicator-tool');
    const pctEl = document.getElementById('scan-indicator-pct');
    if (!el) return;
    const scanning = !!(status && status.scanning);
    if (!scanning) {
      el.classList.add('hidden');
      if (targetEl) targetEl.textContent = '';
      if (toolEl) toolEl.textContent = '';
      if (pctEl) pctEl.textContent = '';
      return;
    }
    el.classList.remove('hidden');
    const scan = status.scan || {};
    if (targetEl) targetEl.textContent = scan.target || '';
    if (toolEl) toolEl.textContent = scan.phase || '';
    if (pctEl) pctEl.textContent = (scan.progress != null) ? Math.round(scan.progress) + '%' : '';
    // PR7 #40: deep-link the indicator. Single running scan → its
    // detail page; multiple running → filtered list. /scans/status
    // returns the most recent running scan, so we can route directly
    // when a scan id is present.
    if (scan.id) {
      el.setAttribute('href', '#/scans/' + encodeURIComponent(scan.id));
    } else {
      el.setAttribute('href', '#/scans?status=running');
    }
  },
};

function parseQueryParams() {
  const hash = location.hash;
  const qIdx = hash.indexOf('?');
  if (qIdx === -1) return {};
  const params = {};
  new URLSearchParams(hash.slice(qIdx + 1)).forEach((v, k) => { params[k] = v; });
  return params;
}

const app = document.getElementById('app');

// Load sidebar badge on init and keep it updated. The compact agent
// status mounts independently via AgentCard.mountCompact so the
// version + uptime stay live across navigation.
function loadSidebarBadge() {
  API.overview().then(data => {
    updateSidebarBadge(data.findings_by_severity);
  }).catch(() => {});
}

window.addEventListener('hashchange', () => Router.navigate());
document.addEventListener('DOMContentLoaded', () => {
  Router.navigate();
  loadSidebarBadge();

  // Compact agent status pinned in the sidebar footer. Polls
  // independently of routes; PR2 (#35) replaces the big dashboard
  // agent card.
  const agentSlot = document.getElementById('agent-status-slot');
  if (agentSlot && typeof AgentCard !== 'undefined' && AgentCard.mountCompact) {
    AgentCard.mountCompact(agentSlot);
  }

  // Topbar scan indicator (PR2 #35).
  ScanIndicator.start();

  // SPEC-SCHED1.4b R9: "Run scan now" opens the ad-hoc dispatcher
  // modal. The button is in the sidebar footer; wired once at load so
  // it works across all hash routes without re-binding on navigate.
  const adhocBtn = document.getElementById('nav-adhoc-btn');
  if (adhocBtn && typeof AdHocPage !== 'undefined') {
    adhocBtn.addEventListener('click', () => AdHocPage.open());
  }

  // PR9 #42: Cmd+K command palette + Cmd+R ad-hoc dispatcher. The
  // topbar search button is the click affordance for Cmd+K. Notif and
  // help icons stay disabled with a "Coming in v0.6" tooltip set in
  // markup; no handler needed.
  const cmdkBtn = document.getElementById('topbar-cmdk-btn');
  if (cmdkBtn) {
    cmdkBtn.addEventListener('click', () => {
      if (typeof CommandPalette !== 'undefined') CommandPalette.open();
    });
  }
  bindGlobalShortcuts();
  const newBtn = document.getElementById('topbar-new-btn');
  if (newBtn) {
    newBtn.setAttribute('aria-haspopup', 'menu');
    newBtn.setAttribute('aria-expanded', 'false');
    newBtn.removeAttribute('title');
    newBtn.setAttribute('aria-label', 'New');
    newBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      TopbarNewMenu.toggle(newBtn);
    });
  }
});

// PR8 #41 — Topbar "+ New" dropdown. Replaces the PR2 placeholder that
// dispatched straight to the ad-hoc modal. Renders four entries:
//   Add target  → TargetsPage.openCreateModal() | fallback to /targets
//   Run scan    → AdHocPage.open()
//   New schedule→ SchedulesPage.openCreateModal() | fallback to /schedules?new=1
//   New blackout→ BlackoutsPage.openCreateModal() | fallback to /blackouts?new=1
//
// Keyboard shortcuts (T/S/⌘⇧S/B) are visual only in P0; PR9 #42 wires
// the global handlers as part of Cmd+K.
const TopbarNewMenu = {
  _menu: null,
  _anchor: null,

  toggle(anchor) {
    if (this._menu) {
      this.close();
      return;
    }
    this.open(anchor);
  },

  open(anchor) {
    this.close();
    const menu = document.createElement('div');
    menu.className = 'topbar-new-menu';
    menu.setAttribute('role', 'menu');
    menu.setAttribute('aria-label', 'Create new resource');
    menu.innerHTML = `
      <button type="button" class="topbar-new-item" role="menuitem" data-new-action="target">
        ${Components.icon('plus', 14)}<span class="topbar-new-label">Add target…</span>
        <kbd class="kbd">T</kbd>
      </button>
      <button type="button" class="topbar-new-item" role="menuitem" data-new-action="scan">
        ${Components.icon('play', 14)}<span class="topbar-new-label">Run scan…</span>
        <kbd class="kbd">S</kbd>
      </button>
      <button type="button" class="topbar-new-item" role="menuitem" data-new-action="schedule">
        ${Components.icon('clock', 14)}<span class="topbar-new-label">New schedule…</span>
        <kbd class="kbd">⌘⇧S</kbd>
      </button>
      <button type="button" class="topbar-new-item" role="menuitem" data-new-action="blackout">
        ${Components.icon('eye-off', 14)}<span class="topbar-new-label">New blackout…</span>
        <kbd class="kbd">B</kbd>
      </button>
    `;
    document.body.appendChild(menu);
    this._position(menu, anchor);
    this._menu = menu;
    this._anchor = anchor;
    if (anchor) anchor.setAttribute('aria-expanded', 'true');

    menu.querySelectorAll('[data-new-action]').forEach(btn => {
      btn.addEventListener('click', () => {
        const kind = btn.getAttribute('data-new-action');
        this.close();
        TopbarNewMenu.dispatch(kind);
      });
    });

    // Defer the global listeners so the click that opened the menu
    // doesn't immediately close it.
    setTimeout(() => {
      document.addEventListener('click', this._onOutside);
      document.addEventListener('keydown', this._onKey);
    }, 0);

    // Focus the first item so keyboard users can act immediately.
    const first = menu.querySelector('[data-new-action]');
    if (first) first.focus();
  },

  close() {
    if (!this._menu) return;
    if (this._menu.parentNode) this._menu.remove();
    this._menu = null;
    if (this._anchor) {
      this._anchor.setAttribute('aria-expanded', 'false');
      this._anchor = null;
    }
    document.removeEventListener('click', this._onOutside);
    document.removeEventListener('keydown', this._onKey);
  },

  _position(menu, anchor) {
    const rect = anchor.getBoundingClientRect();
    menu.style.position = 'fixed';
    menu.style.top = (rect.bottom + 4) + 'px';
    menu.style.right = (window.innerWidth - rect.right) + 'px';
    menu.style.zIndex = '40';
  },

  _onOutside(e) {
    if (!TopbarNewMenu._menu) return;
    if (TopbarNewMenu._menu.contains(e.target)) return;
    if (TopbarNewMenu._anchor && TopbarNewMenu._anchor.contains(e.target)) return;
    TopbarNewMenu.close();
  },

  _onKey(e) {
    if (!TopbarNewMenu._menu) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      TopbarNewMenu.close();
      if (TopbarNewMenu._anchor) TopbarNewMenu._anchor.focus();
      return;
    }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const items = Array.from(TopbarNewMenu._menu.querySelectorAll('[data-new-action]'));
      if (!items.length) return;
      const cur = document.activeElement;
      let idx = items.indexOf(cur);
      if (idx === -1) idx = 0;
      const next = e.key === 'ArrowDown'
        ? (idx + 1) % items.length
        : (idx - 1 + items.length) % items.length;
      items[next].focus();
    }
  },

  dispatch(kind) {
    switch (kind) {
      case 'target':
        if (typeof TargetsPage !== 'undefined' && TargetsPage.openCreateModal) {
          TargetsPage.openCreateModal();
        } else {
          location.hash = '#/targets';
        }
        return;
      case 'scan':
        if (typeof AdHocPage !== 'undefined' && AdHocPage.open) {
          AdHocPage.open();
        }
        return;
      case 'schedule':
        if (typeof SchedulesPage !== 'undefined' && SchedulesPage.openCreateModal) {
          SchedulesPage.openCreateModal();
        } else {
          location.hash = '#/schedules?new=1';
        }
        return;
      case 'blackout':
        if (typeof BlackoutsPage !== 'undefined' && BlackoutsPage.openCreateModal) {
          BlackoutsPage.openCreateModal();
        } else {
          location.hash = '#/blackouts?new=1';
        }
        return;
    }
  },
};

// PR9 #42 — global keyboard shortcuts. Cmd+K opens the command palette
// from any route; Cmd+R opens the ad-hoc dispatcher. Both are no-ops
// when their respective overlay is already open. Cmd+R intercepts the
// browser refresh (OQ4 default); F5 stays available as a fallback and
// the cmdk footer documents this.
//
// The handler skips when the user is typing into an input/textarea
// /select/contenteditable to avoid hijacking text input — except inside
// the palette overlay, where Cmd+K must still no-op the palette and Esc
// is owned by the palette controller itself.
function bindGlobalShortcuts() {
  document.addEventListener('keydown', (e) => {
    const meta = e.metaKey || e.ctrlKey;
    if (!meta) return;
    const key = (e.key || '').toLowerCase();
    if (key !== 'k' && key !== 'r') return;

    const t = e.target;
    const inEditable = t && t.matches && t.matches('input, textarea, select, [contenteditable=""], [contenteditable="true"]');
    const inPalette = t && t.closest && t.closest('.command-palette-overlay');
    if (inEditable && !inPalette) {
      // Cmd+K is a universal "open search" pattern — intercept even
      // inside text fields so the user doesn't have to click out
      // first. Cmd+R inside a text field stays as the browser default
      // (refresh) so an operator typing in a textarea isn't
      // interrupted by a modal opening on top of their work.
      if (key === 'r') return;
    }

    if (key === 'k') {
      e.preventDefault();
      if (typeof CommandPalette === 'undefined') return;
      if (CommandPalette.isOpen()) return;
      CommandPalette.open();
      return;
    }
    if (key === 'r') {
      e.preventDefault();
      if (typeof AdHocPage === 'undefined') return;
      // No-op if a modal or the command palette is already open — both
      // would otherwise stack a second overlay on top.
      if (document.body.classList.contains('modal-open')) return;
      if (document.body.classList.contains('cmdk-open')) return;
      AdHocPage.open();
      return;
    }
  });
}
