// SPEC-X3.1 — Agent status (sidebar compact card).
// Renders a status dot + version + uptime in the sidebar footer,
// polling /api/daemon/status every 8 s. Vanilla JS to match the rest
// of the embedded UI; no build step.
//
// PR2 #35 moved the agent status out of the dashboard so it stays
// visible from every route. PR12 #45 removed the dashboard widget
// surface (mount/unmount/refresh/skeleton/template/...) — only the
// compact sidebar card remains.

const AgentCard = {
  POLL_MS: 8000,
  _compactTimer: null,

  // mountCompact(el) renders the sidebar footer status. Polls on its
  // own schedule and is not unmounted by route changes.
  mountCompact(el) {
    if (this._compactTimer) clearInterval(this._compactTimer);
    this.refreshCompact(el);
    this._compactTimer = setInterval(() => this.refreshCompact(el), this.POLL_MS);
  },

  async refreshCompact(el) {
    if (!el) return;
    try {
      const data = await API.daemonStatus();
      el.innerHTML = this.compactTemplate(data);
    } catch (err) {
      el.innerHTML = this.compactErrorTemplate();
    }
  },

  compactTemplate(d) {
    let dotCls = 'agent-dot-err';
    let label = 'stopped';
    if (d.installed && d.running) {
      dotCls = 'agent-dot-ok';
      label = 'running';
    } else if (!d.installed) {
      dotCls = 'agent-dot-warn';
      label = 'not installed';
    }
    const version = d.version ? 'v' + d.version : '—';
    const uptime = (d.installed && d.running)
      ? this.formatUptime(d.uptime_seconds || 0)
      : '';
    const uptimeHtml = uptime
      ? `<span class="agent-status-uptime" title="Uptime">${escapeHtml(uptime)}</span>`
      : '';
    return `<span class="agent-dot ${dotCls}" title="${escapeHtml(label)}" aria-label="Agent ${escapeHtml(label)}"></span>
      <span class="agent-status-version" id="agent-version">${escapeHtml(version)}</span>
      ${uptimeHtml}`;
  },

  compactErrorTemplate() {
    return `<span class="agent-dot agent-dot-err" title="UI can't read agent state" aria-label="Agent state unknown"></span>
      <span class="agent-status-version" id="agent-version">—</span>`;
  },

  formatUptime(sec) {
    if (sec <= 0) return '-';
    const h = Math.floor(sec / 3600);
    const m = Math.floor((sec % 3600) / 60);
    if (h > 0) return h + 'h ' + m + 'm';
    if (m > 0) return m + 'm';
    return sec + 's';
  },
};
