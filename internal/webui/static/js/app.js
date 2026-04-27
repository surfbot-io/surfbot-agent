// Hash-based SPA router
const Router = {
  routes: [
    { pattern: /^#?\/?$/, page: 'dashboard', render: () => DashboardPage.render(app) },
    { pattern: /^#\/findings\/(.+)$/, page: 'findings', render: (m) => FindingsPage.render(app, { id: m[1] }) },
    { pattern: /^#\/findings/, page: 'findings', render: () => FindingsPage.render(app, parseQueryParams()) },
    { pattern: /^#\/assets/, page: 'assets', render: () => AssetsPage.render(app, parseQueryParams()) },
    { pattern: /^#\/scans\/(.+)$/, page: 'scans', render: (m) => ScansPage.render(app, { id: m[1] }) },
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
    { pattern: /^#\/settings\/defaults/, page: 'settings-defaults', render: () => SettingsDefaultsPage.render(app) },
  ],

  navigate() {
    const hash = location.hash || '#/';
    const app = document.getElementById('app');

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

    for (const route of this.routes) {
      const match = hash.match(route.pattern);
      if (match) {
        route.render(match);
        return;
      }
    }

    app.innerHTML = Components.emptyState('Page not found', 'The requested page does not exist.');
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

  // PR2 #35 stubs. Cmd+K and "+ New" are placeholders until PR9
  // (search) and PR3 (dashboard reframe) wire real behavior. Notif and
  // help icons stay disabled with a "Coming in v0.6" tooltip set in
  // markup; no handler needed.
  const cmdkBtn = document.getElementById('topbar-cmdk-btn');
  if (cmdkBtn) cmdkBtn.addEventListener('click', () => {
    // Placeholder — PR9 implements global search.
  });
  const newBtn = document.getElementById('topbar-new-btn');
  if (newBtn) newBtn.addEventListener('click', () => {
    // Placeholder — PR3 wires the "+ New" entry point. Falls back to
    // the ad-hoc dispatcher so the button isn't dead in the meantime.
    if (typeof AdHocPage !== 'undefined' && AdHocPage.open) AdHocPage.open();
  });
});
