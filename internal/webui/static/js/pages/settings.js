// PR8 #41 — Consolidated Settings page. Replaces the singleton
// /settings/defaults route with a sub-nav + section shell that scales as
// new config surfaces land. P0 ships:
//
//   • Scan defaults — delegates to SettingsDefaultsPage (functional form)
//   • General / Notifications / Integrations / API & tokens / Storage /
//     About — placeholder cards ("Coming in v0.6") with section copy
//
// Routing:
//   #/settings                    → renders shell + scan-defaults
//   #/settings/<section>          → shell + that section
//   #/settings/defaults           → router redirects to /settings/scan-defaults
//
// Section state is held in URL hash, not in module state, so a refresh
// or a deep link from a docs page lands on the right pane.
const SettingsPage = {
  DEFAULT_SECTION: 'scan-defaults',

  SECTIONS: [
    { key: 'general',         label: 'General',        description: 'Agent identity, locale' },
    { key: 'scan-defaults',   label: 'Scan defaults',  description: 'Concurrency, rate-limit, depth' },
    { key: 'notifications',   label: 'Notifications',  description: 'Webhooks, Slack, email' },
    { key: 'integrations',    label: 'Integrations',   description: 'Jira, GitHub, MCP' },
    { key: 'api-tokens',      label: 'API & tokens',   description: 'UI token, API access' },
    { key: 'storage',         label: 'Storage',        description: 'SQLite path, retention' },
    { key: 'about',           label: 'About',          description: 'Version, license, logs' },
  ],

  async render(app, params) {
    const requested = (params && params.section) || this.DEFAULT_SECTION;
    const section = this.SECTIONS.some(s => s.key === requested) ? requested : this.DEFAULT_SECTION;

    app.innerHTML = this._shellHtml(section);
    this._wireShell(app);

    const host = app.querySelector('[data-settings-section-host]');
    if (!host) return;
    await this._renderSection(host, section);
  },

  _shellHtml(activeKey) {
    const subnav = this.SECTIONS.map(s => {
      const active = s.key === activeKey;
      const cls = 'settings-subnav-item' + (active ? ' settings-subnav-item-active' : '');
      return `<li>
        <a href="#/settings/${escapeHtml(s.key)}" class="${cls}"
           data-settings-link="${escapeHtml(s.key)}"
           aria-current="${active ? 'page' : 'false'}">
          <div class="settings-subnav-label">${escapeHtml(s.label)}</div>
          <div class="settings-subnav-desc">${escapeHtml(s.description)}</div>
        </a>
      </li>`;
    }).join('');

    return `
      <div class="page-header">
        <h2>Settings</h2>
        <p>Configuración del agente, defaults de scan e integraciones</p>
      </div>
      <div class="settings-grid">
        <nav class="settings-subnav" aria-label="Settings sections">
          <ul>${subnav}</ul>
        </nav>
        <section class="settings-section" data-settings-section-host
          aria-live="polite">
          <div class="loading">Loading...</div>
        </section>
      </div>
    `;
  },

  _wireShell(app) {
    // The links are real anchors; the browser's hash navigation will
    // re-trigger the router and refresh the section. No JS click
    // handler needed — this preserves the back/forward stack.
  },

  async _renderSection(host, key) {
    if (key === 'scan-defaults') {
      // Reuse the existing SettingsDefaultsPage rendering logic. Its
      // render() targets the #app container directly, so we hand it our
      // section host instead — it owns the loading spinner, error
      // banner, and view↔edit transitions inside this slot.
      if (typeof SettingsDefaultsPage === 'undefined') {
        host.innerHTML = Components.errorBanner({
          message: 'SettingsDefaultsPage module not loaded',
        });
        return;
      }
      await SettingsDefaultsPage.render(host);
      return;
    }
    host.innerHTML = this._placeholderHtml(key);
  },

  _placeholderHtml(key) {
    const sec = this.SECTIONS.find(s => s.key === key);
    if (!sec) return Components.emptyState('Section not found', 'Pick a section from the left.');
    return `
      <div class="settings-placeholder">
        <div class="settings-placeholder-eyebrow">${escapeHtml(sec.label)}</div>
        <h3 class="settings-placeholder-title">Coming in v0.6</h3>
        <p class="settings-placeholder-body">${escapeHtml(sec.description)}.</p>
      </div>
    `;
  },
};
