// SPEC-X3.1 — Agent status card.
// Renders the daemon + scheduler state on the dashboard, polling
// /api/daemon/status every 8 s. Vanilla JS to match the rest of the
// embedded UI; no build step.
//
// Four states (spec §2):
//   1. running + window closed (green dot)
//   2. running + window open   (amber on the window line)
//   3. running + scheduler disabled (collapsed scheduler)
//   4. stopped / not installed (red dot, copy-to-clipboard hint)

const AgentCard = {
  POLL_MS: 8000,
  _timer: null,
  // "Scan now" was removed with SPEC-SCHED1.4a; target-anchored ad-hoc
  // dispatch via /api/v1/scans/ad-hoc lands on target pages in 1.4b.

  // mount(containerEl) renders the placeholder and starts the poll loop.
  mount(el) {
    el.innerHTML = this.skeleton();
    this.refresh(el);
    if (this._timer) clearInterval(this._timer);
    this._timer = setInterval(() => this.refresh(el), this.POLL_MS);
  },

  // unmount() stops the poll loop. Called by the dashboard route on
  // navigation away. Idempotent.
  unmount() {
    if (this._timer) {
      clearInterval(this._timer);
      this._timer = null;
    }
  },

  skeleton() {
    return `<section class="card agent-card" aria-label="Agent status" aria-live="polite">
      <div class="card-label">Agent</div>
      <div class="text-muted" style="padding:8px 0">Loading…</div>
    </section>`;
  },

  async refresh(el) {
    try {
      const data = await API.daemonStatus();
      el.innerHTML = this.template(data);
      this.bind(el, data);
    } catch (err) {
      el.innerHTML = this.errorTemplate(err.message);
    }
  },

  // template() renders the four spec §2 states from a status payload.
  template(d) {
    if (!d.installed) {
      return this.stoppedTemplate('Agent is not installed.', false, d.install_hint);
    }
    if (!d.running) {
      return this.stoppedTemplate(d.reason || 'Agent is not running.', true, d.install_hint);
    }
    return this.runningTemplate(d);
  },

  runningTemplate(d) {
    const dot = `<span class="agent-dot agent-dot-ok"
        aria-label="Status: running"></span>`;
    const srOnly = `<span class="sr-only">Status: running.</span>`;
    const sched = d.scheduler;
    const schedLink = `<div style="padding-top:4px"><a href="#/schedules" class="text-muted" style="font-size:12px">View schedules →</a></div>`;
    const schedHtml = !sched
      ? schedLink
      : !sched.enabled
        ? `<div class="text-muted" style="padding-top:8px">Scheduler disabled ${schedLink}</div>`
        : this.schedulerHtml(sched) + schedLink;

    const uptime = this.formatUptime(d.uptime_seconds || 0);
    return `<section class="card agent-card" aria-label="Agent status" aria-live="polite">
      <div class="agent-header">
        <div class="card-label">Agent</div>
        <div class="agent-status">${srOnly}${dot}<span>running</span></div>
      </div>
      <div class="detail-grid" style="margin-top:8px">
        <span class="detail-label">Version</span>
        <span class="detail-value mono">${escapeHtml(d.version || '?')}</span>
        <span class="detail-label">Uptime</span>
        <span class="detail-value">${uptime}</span>
        <span class="detail-label">PID</span>
        <span class="detail-value mono">${d.pid || '-'}</span>
      </div>
      ${schedHtml}
    </section>`;
  },

  schedulerHtml(s) {
    const lastFull  = this.scanRow('Last full',  s.last_full);
    const lastQuick = this.scanRow('Last quick', s.last_quick);
    const nextFull  = s.next_full
      ? this.timeRow('Next full',  s.next_full,  'in ')
      : '';
    const nextQuick = s.next_quick
      ? this.timeRow('Next quick', s.next_quick, 'in ')
      : '';
    const win = s.window ? this.windowRow(s.window) : '';
    return `<div class="detail-grid" style="margin-top:12px;border-top:1px solid var(--border);padding-top:12px">
      ${lastFull}${lastQuick}${nextFull}${nextQuick}${win}
    </div>`;
  },

  scanRow(label, scan) {
    if (!scan || !scan.at) return '';
    const ok = scan.status === 'ok';
    const mark = ok
      ? `<span style="color:var(--success)" aria-label="ok">✓</span>`
      : `<span style="color:var(--danger)" aria-label="failed" title="${escapeHtml(scan.error || '')}">✗</span>`;
    const ts = `<time datetime="${scan.at}">${Components.formatDate(scan.at)}</time>`;
    return `<span class="detail-label">${label}</span>
      <span class="detail-value">${ts} ${mark}</span>`;
  },

  timeRow(label, iso, prefix) {
    const ms = new Date(iso).getTime() - Date.now();
    const rel = ms > 0
      ? prefix + this.humanDuration(ms / 1000)
      : Components.timeAgo(iso);
    const ts = `<time datetime="${iso}">${Components.formatDate(iso)}</time>`;
    return `<span class="detail-label">${label}</span>
      <span class="detail-value">${ts} <span class="text-muted">(${rel})</span></span>`;
  },

  windowRow(w) {
    const desc = `${escapeHtml(w.start || '')}–${escapeHtml(w.end || '')} ${escapeHtml(w.timezone || '')}`;
    if (!w.enabled) {
      return `<span class="detail-label">Window</span>
        <span class="detail-value text-muted">disabled ${desc ? '(' + desc + ')' : ''}</span>`;
    }
    const stateLabel = w.open_now
      ? `<span style="color:var(--sev-medium)">open</span>`
      : `closed`;
    return `<span class="detail-label">Window</span>
      <span class="detail-value">${desc} <span class="text-muted">${stateLabel}</span></span>`;
  },

  stoppedTemplate(reason, installed, hint) {
    const dotClass = installed ? 'agent-dot-err' : 'agent-dot-warn';
    const headerLabel = installed ? 'stopped' : 'not installed';
    // Server-supplied commands. The UI never invents flags or sudo
    // prefixes — different OSes need different things.
    const h = hint || {};
    const cmd = installed
      ? (h.start_command || 'surfbot daemon start')
      : (h.install_command || 'surfbot daemon install');
    const adminNote = h.requires_admin
      ? `<div class="text-muted" style="margin-top:4px;font-size:12px">${
          installed
            ? 'Run from a terminal with administrator privileges.'
            : 'Run from a terminal with administrator privileges, then start the service.'
        }</div>`
      : '';
    const docsLink = h.docs_url
      ? ` <a href="${escapeHtml(h.docs_url)}" target="_blank" rel="noopener noreferrer">docs</a>`
      : '';
    return `<section class="card agent-card" aria-label="Agent status" aria-live="polite">
      <div class="agent-header">
        <div class="card-label">Agent</div>
        <div class="agent-status">
          <span class="sr-only">Status: ${headerLabel}.</span>
          <span class="agent-dot ${dotClass}" aria-label="Status: ${headerLabel}"></span>
          <span>${headerLabel}</span>
        </div>
      </div>
      <p class="text-muted" style="margin-top:8px">${escapeHtml(reason)}</p>
      <p style="margin-top:8px">
        Run <code id="agent-start-cmd">${escapeHtml(cmd)}</code>
        <button id="agent-copy-cmd" class="refresh-btn" data-cmd="${escapeHtml(cmd)}"
          aria-label="Copy command to clipboard">Copy</button>
        <span id="agent-copy-feedback" class="text-muted" style="margin-left:6px"></span>${docsLink}
      </p>
      ${adminNote}
    </section>`;
  },

  errorTemplate(msg) {
    return `<section class="card agent-card" aria-label="Agent status" aria-live="polite">
      <div class="card-label">Agent</div>
      <p class="text-muted">UI can't read agent state: ${escapeHtml(msg)}</p>
    </section>`;
  },

  // bind() wires up the copy button event handler after every render.
  bind(root, _data) {
    const copyBtn = root.querySelector('#agent-copy-cmd');
    if (copyBtn) {
      copyBtn.addEventListener('click', () => {
        const cmd = copyBtn.getAttribute('data-cmd') || '';
        this.copyToClipboard(cmd).then(ok => {
          const fb = root.querySelector('#agent-copy-feedback');
          if (fb) {
            fb.textContent = ok ? 'copied' : 'copy failed';
            setTimeout(() => { fb.textContent = ''; }, 2000);
          }
        });
      });
    }
  },

  copyToClipboard(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text).then(() => true).catch(() => false);
    }
    // Fallback for older browsers / non-secure contexts.
    try {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      const ok = document.execCommand('copy');
      document.body.removeChild(ta);
      return Promise.resolve(ok);
    } catch (_) {
      return Promise.resolve(false);
    }
  },

  formatUptime(sec) {
    if (sec <= 0) return '-';
    const h = Math.floor(sec / 3600);
    const m = Math.floor((sec % 3600) / 60);
    if (h > 0) return h + 'h ' + m + 'm';
    if (m > 0) return m + 'm';
    return sec + 's';
  },

  humanDuration(sec) {
    sec = Math.max(0, Math.round(sec));
    if (sec < 60) return sec + 's';
    const m = Math.floor(sec / 60);
    if (m < 60) return m + 'm';
    const h = Math.floor(m / 60);
    const rm = m % 60;
    if (h < 24) return h + 'h' + (rm ? ' ' + rm + 'm' : '');
    const d = Math.floor(h / 24);
    return d + 'd ' + (h % 24) + 'h';
  },
};
