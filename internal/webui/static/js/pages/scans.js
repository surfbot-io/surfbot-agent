// Scans page
const ScansPage = {
  pollTimer: null,

  async render(app, params) {
    if (params && params.id) {
      return this.renderDetail(app, params.id);
    }

    app.innerHTML = '<div class="loading">Loading scans...</div>';

    try {
      const [scansData, statusData, targetsData] = await Promise.all([
        API.scans({ limit: 50 }),
        API.scanStatus(),
        API.targets(),
      ]);
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

    const rows = data.scans.map(s => {
      let dur = '-';
      if (s.started_at && s.finished_at) {
        dur = Components.formatDuration(Math.floor((new Date(s.finished_at) - new Date(s.started_at)) / 1000));
      }
      return `
        <tr class="clickable" onclick="location.hash='#/scans/${s.id}'">
          <td class="mono">${Components.truncateID(s.id)}</td>
          <td class="mono">${escapeHtml(s.target || s.target_id || '-')}</td>
          <td>${Components.statusBadge(s.status)}</td>
          <td>${s.type}</td>
          <td class="mono">${dur}</td>
          <td>${s.stats.findings_total || 0} findings</td>
          <td class="text-muted">${Components.timeAgo(s.created_at)}</td>
        </tr>
      `;
    }).join('');

    return `
      <div class="page-header">
        <h2>Scans</h2>
        <p>${data.total} total scans</p>
      </div>
      ${scanForm}
      ${activeBanner}
      ${Components.table(['ID', 'Target', 'Status', 'Type', 'Duration', 'Results', 'Date'], [rows])}
    `;
  },

  bindEvents(app, targetsData) {
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
    if (this._elapsedTimer) {
      clearInterval(this._elapsedTimer);
      this._elapsedTimer = null;
    }
  },

  // --- Scan Detail View ---

  PHASES: [
    { key: 'discovery', label: 'Discovering' },
    { key: 'resolution', label: 'DNS' },
    { key: 'port_scan', label: 'Ports' },
    { key: 'http_probe', label: 'HTTP' },
    { key: 'assessment', label: 'Analyzing' },
    { key: 'completed', label: 'Complete' },
  ],

  TOOL_ICONS: {
    subfinder: '\u{1F50D}', dnsx: '\u{1F4CB}', naabu: '\u{1F4E1}',
    httpx: '\u{1F310}', subjack: '\u{26A0}\uFE0F', nuclei: '\u{1F6E1}\uFE0F',
  },

  phaseIndex(phase) {
    if (!phase) return -1;
    const idx = this.PHASES.findIndex(p => p.key === phase);
    if (idx >= 0) return idx;
    // Map alternate names
    if (phase === 'initializing') return -1;
    if (phase === 'diffing') return 5;
    if (phase === 'cancelled') return -1;
    return -1;
  },

  renderPhaseIndicator(scan) {
    const isTerminal = scan.status === 'completed' || scan.status === 'failed' || scan.status === 'cancelled';
    const currentIdx = isTerminal && scan.status === 'completed' ? 6 : this.phaseIndex(scan.phase);

    return `<div class="phase-indicator">${this.PHASES.map((p, i) => {
      const num = i + 1;
      let circleClass = 'phase-circle';
      let content = num;
      let stepClass = 'phase-step';

      if (isTerminal && scan.status === 'completed') {
        circleClass += ' phase-done';
        content = '\u2713';
        stepClass += ' done';
      } else if (i < currentIdx) {
        circleClass += ' phase-done';
        content = '\u2713';
        stepClass += ' done';
      } else if (i === currentIdx) {
        if (scan.status === 'failed') {
          circleClass += ' phase-failed';
          content = '\u2717';
        } else if (scan.status === 'cancelled') {
          circleClass += ' phase-cancelled';
          content = '\u2013';
        } else {
          circleClass += ' phase-active';
        }
        stepClass += ' active';
      }

      const line = i < this.PHASES.length - 1
        ? `<div class="phase-line${i < currentIdx ? ' phase-line-done' : ''}"></div>`
        : '';

      return `<div class="${stepClass}"><div class="${circleClass}">${content}</div><span class="phase-label">${p.label}</span></div>${line}`;
    }).join('')}</div>`;
  },

  renderProgressBar(scan) {
    if (scan.status !== 'running') return '';
    const pct = Math.round(scan.progress || 0);
    const phase = scan.phase || 'initializing';
    const fillClass = pct === 0 ? 'progress-bar-fill progress-shimmer' : 'progress-bar-fill';

    return `<div class="progress-bar-container">
      <div class="progress-bar-header">
        <span>Phase: ${phase}</span>
        <span>${pct}%</span>
      </div>
      <div class="progress-bar-track">
        <div class="${fillClass}" style="width:${pct === 0 ? 100 : pct}%"></div>
      </div>
    </div>`;
  },

  renderPipeline(toolRuns) {
    if (toolRuns.length === 0) return '';

    const sorted = [...toolRuns].sort((a, b) => {
      if (a.started_at && b.started_at) return new Date(a.started_at) - new Date(b.started_at);
      return 0;
    });

    const nodes = sorted.map((tr, i) => {
      const icon = this.TOOL_ICONS[tr.tool_name] || '\u{1F527}';
      const isLast = i === sorted.length - 1;
      const statusClass = 'status-' + (tr.status || 'pending');

      let duration = '';
      if (tr.status === 'running' && tr.started_at) {
        const elapsed = Math.round((Date.now() - new Date(tr.started_at).getTime()) / 1000);
        duration = `<span class="tool-duration mono" data-started="${tr.started_at}">${this.formatElapsed(elapsed)}</span>`;
      } else if (tr.duration_ms > 0) {
        duration = `<span class="tool-duration mono">${this.formatDurationMs(tr.duration_ms)}</span>`;
      }

      const stats = [];
      if (tr.targets_count > 0) stats.push(`<span class="stat-badge">${tr.targets_count} targets</span>`);
      if (tr.findings_count > 0) stats.push(`<span class="stat-badge stat-badge-finding">${tr.findings_count} findings</span>`);

      return `<div class="pipeline-node ${statusClass}">
        <div class="pipeline-connector">
          <div class="pipeline-dot"></div>
          ${isLast ? '' : '<div class="pipeline-vline"></div>'}
        </div>
        <div class="pipeline-content">
          <div class="pipeline-header">
            <span class="tool-icon">${icon}</span>
            <span class="tool-name">${escapeHtml(tr.tool_name)}</span>
            ${Components.statusBadge(tr.status)}
            ${duration}
          </div>
          ${stats.length > 0 ? `<div class="pipeline-stats">${stats.join('')}</div>` : ''}
        </div>
      </div>`;
    }).join('');

    return `<div class="card" style="margin-bottom:16px">
      <div class="card-label">Pipeline</div>
      <div class="pipeline-view">${nodes}</div>
    </div>`;
  },

  formatElapsed(seconds) {
    if (seconds < 60) return seconds + 's';
    return Math.floor(seconds / 60) + 'm ' + (seconds % 60) + 's';
  },

  formatDurationMs(ms) {
    if (ms < 1000) return ms + 'ms';
    const s = ms / 1000;
    if (s < 60) return s.toFixed(1) + 's';
    return Math.floor(s / 60) + 'm ' + Math.round(s % 60) + 's';
  },

  renderDetailContent(app, data) {
    const s = data.scan;
    const isRunning = s.status === 'running';

    let dur = '-';
    if (s.started_at && s.finished_at) {
      dur = Components.formatDuration(Math.floor((new Date(s.finished_at) - new Date(s.started_at)) / 1000));
    } else if (isRunning && s.started_at) {
      const elapsed = Math.round((Date.now() - new Date(s.started_at).getTime()) / 1000);
      dur = this.formatElapsed(elapsed) + ' (running)';
    }

    const cancelBtn = isRunning
      ? `<button class="btn btn-danger btn-sm" id="cancel-scan-btn">Cancel Scan</button>`
      : '';

    const errorCard = s.error
      ? `<div class="card" style="margin-bottom:16px;border-color:rgba(248,81,73,0.3)">
          <div style="color:var(--sev-critical);font-size:13px">${escapeHtml(s.error)}</div>
        </div>`
      : '';

    const changeRows = data.changes.map(c => `
      <tr>
        <td><span class="change-${c.change_type}">${c.change_type}</span></td>
        <td>${c.asset_type}</td>
        <td class="mono">${escapeHtml(c.asset_value)}</td>
        <td>${escapeHtml(c.summary || '-')}</td>
      </tr>
    `).join('');

    app.innerHTML = `
      ${Components.backLink('#/scans', 'Back to scans')}
      <div class="detail-panel">
        <div class="detail-header">
          ${Components.statusBadge(s.status)}
          <h2>Scan ${Components.truncateID(s.id)}</h2>
          <span class="text-muted mono" style="font-size:12px">${escapeHtml(data.target)}</span>
          <span class="text-muted" style="font-size:12px;margin-left:4px">${dur}</span>
          <div style="margin-left:auto">${cancelBtn}</div>
        </div>

        ${this.renderPhaseIndicator(s)}
        ${this.renderProgressBar(s)}
        ${errorCard}
      </div>

      ${this.renderPipeline(data.tool_runs)}

      <div class="card" style="margin-bottom:16px">
        <div class="card-label">Scan Stats</div>
        <div class="detail-grid" style="margin-top:8px">
          <span class="detail-label">Subdomains</span><span class="detail-value">${s.stats.subdomains_found || 0}</span>
          <span class="detail-label">IPs Resolved</span><span class="detail-value">${s.stats.ips_resolved || 0}</span>
          <span class="detail-label">Ports Scanned</span><span class="detail-value">${s.stats.ports_scanned || 0}</span>
          <span class="detail-label">Open Ports</span><span class="detail-value">${s.stats.open_ports || 0}</span>
          <span class="detail-label">HTTP Probed</span><span class="detail-value">${s.stats.http_probed || 0}</span>
          <span class="detail-label">Findings</span><span class="detail-value">${s.stats.findings_total || 0}</span>
        </div>
        ${Components.severityBars({
          critical: s.stats.findings_critical || 0,
          high: s.stats.findings_high || 0,
          medium: s.stats.findings_medium || 0,
          low: s.stats.findings_low || 0,
          info: s.stats.findings_info || 0,
        })}
      </div>

      ${data.changes.length > 0 ? `
        <h3 style="margin-bottom:12px">Asset Changes</h3>
        ${Components.table(['Change', 'Type', 'Value', 'Summary'], [changeRows])}
      ` : ''}
    `;

    // Bind cancel button
    if (isRunning) {
      const cancelEl = document.getElementById('cancel-scan-btn');
      if (cancelEl) {
        cancelEl.addEventListener('click', async () => {
          if (!confirm('Cancel this scan? Tools already running will be terminated.')) return;
          cancelEl.disabled = true;
          cancelEl.textContent = 'Cancelling...';
          try {
            await API.cancelScan(s.id);
            setTimeout(() => this.renderDetail(app, s.id), 1000);
          } catch (err) {
            cancelEl.disabled = false;
            cancelEl.textContent = 'Cancel Scan';
            alert('Failed to cancel: ' + err.message);
          }
        });
      }
    }
  },

  async renderDetail(app, id) {
    this.stopPolling();
    app.innerHTML = '<div class="loading">Loading scan...</div>';

    try {
      const data = await API.scan(id);
      this.renderDetailContent(app, data);

      // Poll if running
      if (data.scan.status === 'running') {
        this.startDetailPolling(app, id);
      }
    } catch (err) {
      app.innerHTML = Components.emptyState('Not Found', 'Scan not found.');
    }
  },

  startDetailPolling(app, id) {
    this.stopPolling();
    this._elapsedTimer = setInterval(() => {
      document.querySelectorAll('.tool-duration[data-started]').forEach(el => {
        const started = new Date(el.dataset.started).getTime();
        const elapsed = Math.round((Date.now() - started) / 1000);
        el.textContent = this.formatElapsed(elapsed);
      });
    }, 1000);

    this.pollTimer = setInterval(async () => {
      try {
        const data = await API.scan(id);
        if (data.scan.status !== 'running') {
          this.stopPolling();
        }
        this.renderDetailContent(app, data);
      } catch {
        // Ignore polling errors
      }
    }, 3000);
  },
};
