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
      else if (hash.startsWith('#/' + page)) link.classList.add('active');
    });

    // Stop the AgentCard poll loop on every navigation. Dashboard
    // re-mounts it; every other route leaves it stopped so the
    // background fetch does not leak across pages.
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

function parseQueryParams() {
  const hash = location.hash;
  const qIdx = hash.indexOf('?');
  if (qIdx === -1) return {};
  const params = {};
  new URLSearchParams(hash.slice(qIdx + 1)).forEach((v, k) => { params[k] = v; });
  return params;
}

const app = document.getElementById('app');

// Load sidebar badge on init and keep it updated
function loadSidebarBadge() {
  API.overview().then(data => {
    const el = document.getElementById('agent-version');
    if (el) el.textContent = 'v' + data.agent.version;
    updateSidebarBadge(data.findings_by_severity);
  }).catch(() => {});
}

window.addEventListener('hashchange', () => Router.navigate());
document.addEventListener('DOMContentLoaded', () => {
  Router.navigate();
  loadSidebarBadge();
});
