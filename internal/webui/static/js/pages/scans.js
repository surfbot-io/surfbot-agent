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
          <td>${(s.target_state && s.target_state.findings_open_total) || 0} findings</td>
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
  ],

  TOOL_ICONS: {
    subfinder: '\u{1F50D}', dnsx: '\u{1F4CB}', naabu: '\u{1F4E1}',
    httpx: '\u{1F310}', subjack: '\u{26A0}\uFE0F', nuclei: '\u{1F6E1}\uFE0F',
  },

  // Build a set of phases that actually ran from tool_runs data.
  ranPhases(toolRuns) {
    const ran = new Set();
    for (const tr of toolRuns) {
      if (tr.phase) ran.add(tr.phase);
    }
    return ran;
  },

  renderPhaseIndicator(scan, toolRuns) {
    const isTerminal = scan.status === 'completed' || scan.status === 'failed' || scan.status === 'cancelled';
    const ran = this.ranPhases(toolRuns || []);
    // For running scans, find the current phase index
    const currentPhaseIdx = this.PHASES.findIndex(p => p.key === scan.phase);

    return `<div class="phase-indicator">${this.PHASES.map((p, i) => {
      const num = i + 1;
      let circleClass = 'phase-circle';
      let content = num;
      let stepClass = 'phase-step';

      if (isTerminal && scan.status === 'completed' && ran.has(p.key)) {
        circleClass += ' phase-done'; content = '\u2713'; stepClass += ' done';
      } else if (isTerminal && scan.status === 'completed' && !ran.has(p.key)) {
        circleClass += ' phase-skipped'; content = '\u2013'; stepClass += ' skipped';
      } else if (isTerminal && (scan.status === 'failed' || scan.status === 'cancelled')) {
        if (ran.has(p.key)) {
          // Check if this phase's tools all completed
          const phaseTools = (toolRuns || []).filter(tr => tr.phase === p.key);
          const allDone = phaseTools.every(tr => tr.status === 'completed');
          if (allDone) {
            circleClass += ' phase-done'; content = '\u2713'; stepClass += ' done';
          } else {
            circleClass += scan.status === 'failed' ? ' phase-failed' : ' phase-cancelled';
            content = scan.status === 'failed' ? '\u2717' : '\u2013';
          }
        } else {
          circleClass += ' phase-skipped'; content = '\u2013'; stepClass += ' skipped';
        }
      } else if (!isTerminal) {
        // Running scan
        if (ran.has(p.key)) {
          const phaseTools = (toolRuns || []).filter(tr => tr.phase === p.key);
          const allDone = phaseTools.every(tr => tr.status === 'completed');
          if (allDone) {
            circleClass += ' phase-done'; content = '\u2713'; stepClass += ' done';
          } else {
            circleClass += ' phase-active'; stepClass += ' active';
          }
        } else if (i === currentPhaseIdx) {
          circleClass += ' phase-active'; stepClass += ' active';
        }
      }

      // Connector line
      const prevDone = circleClass.includes('phase-done');
      const line = i < this.PHASES.length - 1
        ? `<div class="phase-line${prevDone ? ' phase-line-done' : ''}"></div>`
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

  renderPipeline(toolRuns, scanId) {
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
      } else if (tr.status === 'completed') {
        // Sub-millisecond runs show as <1ms rather than silently omitting
        // the column — otherwise subfinder looks like it has no data.
        duration = `<span class="tool-duration mono text-muted">&lt;1ms</span>`;
      }

      const stats = [];
      if (tr.targets_count > 0) stats.push(`<span class="stat-badge">${tr.targets_count} ${tr.targets_count === 1 ? 'target' : 'targets'}</span>`);
      if (tr.findings_count > 0) stats.push(`<span class="stat-badge stat-badge-finding" title="Raw findings emitted by the tool before storage dedup">${tr.findings_count} emitted</span>`);

      // Clicking the whole node toggles the paired log detail row.
      // Providing a hint chevron makes the affordance discoverable.
      const hasLogs = this.toolRunHasLogs(tr);

      return `<div class="pipeline-node ${statusClass}${hasLogs ? ' pipeline-node-clickable' : ''}" data-tr-idx="${i}"${hasLogs ? ' title="Click to view logs"' : ''}>
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
            ${hasLogs ? '<span class="pipeline-chevron text-muted" aria-hidden="true">▸</span>' : ''}
          </div>
          ${stats.length > 0 ? `<div class="pipeline-stats">${stats.join('')}</div>` : ''}
          ${hasLogs ? `<div class="pipeline-detail" data-tr-idx="${i}" hidden>${this.renderToolRunDetail(tr)}</div>` : ''}
        </div>
      </div>`;
    }).join('');

    return `<div class="card">
      <div class="card-label">Pipeline</div>
      <div class="pipeline-view">${nodes}</div>
    </div>`;
  },

  // A tool run "has logs" worth showing if it carries any of the enriched
  // telemetry fields the detection wrappers fill in. Nodes without logs
  // (e.g. skipped-because-no-urls) stay non-clickable.
  toolRunHasLogs(tr) {
    if (tr.output_summary) return true;
    if (tr.error_message) return true;
    const cfg = tr.config || {};
    return !!(cfg.command || cfg.stderr_tail || (cfg.input_preview && cfg.input_preview.length > 0));
  },

  // renderToolRunDetail builds the accordion content for one pipeline
  // node. Surfaces command args, output summary, input preview, tool-
  // specific config keys, error message, and stderr tail — all captured
  // in the wrapper layer (see internal/detection/*.go) and persisted in
  // tool_runs.config.
  renderToolRunDetail(tr) {
    const cfg = tr.config || {};
    const parts = [];

    if (tr.output_summary) {
      parts.push(`<div class="pipeline-detail-section">
        <div class="pipeline-detail-heading">Summary</div>
        <div class="pipeline-detail-text">${escapeHtml(tr.output_summary)}</div>
      </div>`);
    }

    if (cfg.command) {
      parts.push(`<div class="pipeline-detail-section">
        <div class="pipeline-detail-heading">Command</div>
        <pre class="pipeline-detail-pre">${escapeHtml(cfg.command)}</pre>
      </div>`);
    }

    // Per-tool config (the keys that aren't the generic exec context).
    // Presenting them as a definition list keeps the layout predictable.
    const genericKeys = new Set(['command', 'stderr_tail', 'exit_code', 'input_preview', 'inputs_truncated']);
    const toolKeys = Object.keys(cfg).filter(k => !genericKeys.has(k));
    if (toolKeys.length > 0) {
      const rows = toolKeys.map(k => `<span class="detail-label">${escapeHtml(k)}</span><span class="detail-value mono">${escapeHtml(this.formatConfigValue(cfg[k]))}</span>`).join('');
      parts.push(`<div class="pipeline-detail-section">
        <div class="pipeline-detail-heading">Config</div>
        <div class="pipeline-detail-grid">${rows}</div>
      </div>`);
    }

    if (cfg.input_preview && cfg.input_preview.length > 0) {
      const truncated = cfg.inputs_truncated ? ' <span class="text-muted">(truncated)</span>' : '';
      parts.push(`<div class="pipeline-detail-section">
        <div class="pipeline-detail-heading">Inputs${truncated}</div>
        <pre class="pipeline-detail-pre">${cfg.input_preview.map(escapeHtml).join('\n')}</pre>
      </div>`);
    }

    if (cfg.stderr_tail) {
      parts.push(`<div class="pipeline-detail-section">
        <div class="pipeline-detail-heading">Stderr tail</div>
        <pre class="pipeline-detail-pre pipeline-detail-stderr">${escapeHtml(cfg.stderr_tail)}</pre>
      </div>`);
    }

    if (tr.error_message) {
      parts.push(`<div class="pipeline-detail-section">
        <div class="pipeline-detail-heading pipeline-detail-heading-error">Error</div>
        <pre class="pipeline-detail-pre pipeline-detail-stderr">${escapeHtml(tr.error_message)}</pre>
      </div>`);
    }

    // Meta row at the bottom with timestamps and exit code.
    const meta = [];
    if (tr.started_at) meta.push(`started ${Components.timeAgo(tr.started_at)}`);
    if (typeof cfg.exit_code === 'number') meta.push(`exit ${cfg.exit_code}`);
    if (meta.length > 0) {
      parts.push(`<div class="pipeline-detail-meta text-muted">${meta.join(' · ')}</div>`);
    }

    return `<div class="pipeline-detail-body">${parts.join('')}</div>`;
  },

  // bindPipelineAccordion wires click handlers on pipeline nodes.
  // Re-invoked after every render (polling re-renders the whole detail)
  // so listeners are always fresh.
  bindPipelineAccordion() {
    document.querySelectorAll('.pipeline-node-clickable').forEach(node => {
      node.addEventListener('click', (e) => {
        if (e.target.closest('a, button')) return;
        const idx = node.dataset.trIdx;
        const detail = node.querySelector(`.pipeline-detail[data-tr-idx="${idx}"]`);
        if (!detail) return;
        const isOpen = !detail.hasAttribute('hidden');
        if (isOpen) {
          detail.setAttribute('hidden', '');
          node.classList.remove('pipeline-node-open');
        } else {
          detail.removeAttribute('hidden');
          node.classList.add('pipeline-node-open');
        }
      });
    });
  },

  renderFindings(findings, scanId) {
    if (!findings || findings.length === 0) {
      return `<div class="card">
        <div class="card-label">Findings</div>
        <div style="padding:12px 0;color:var(--text-muted);font-size:13px">No findings recorded for this scan.</div>
      </div>`;
    }

    // Two <tr> per finding: a clickable summary row and a collapsible
    // detail row that is expanded inline on click. Keeping the detail
    // inline (rather than navigating to /findings/{id}) preserves the
    // surrounding scan context so the user can compare rows without
    // losing their place.
    //
    // The ASSET column surfaces the distinguishing host/endpoint so
    // findings from the same template against different targets stop
    // looking identical in the list (the common case: "HTTP Missing
    // Security Headers" × 9 with no hint of what's affected).
    const rows = findings.map((f, idx) => {
      const assetTypeCell = this.formatAssetTypeCell(f);
      const assetValueCell = this.formatAssetValueCell(f);
      const detailHTML = this.renderFindingDetail(f);
      return `
        <tr class="finding-row" data-finding-idx="${idx}" title="Click to expand">
          <td>${Components.severityBadge ? Components.severityBadge(f.severity) : `<span class="badge badge-${f.severity}">${f.severity}</span>`}</td>
          <td>${escapeHtml(f.title)}</td>
          <td class="finding-asset-type">${assetTypeCell}</td>
          <td class="finding-asset-value mono" title="${escapeHtml(f.asset_value || '')}">${assetValueCell}</td>
          <td class="mono text-muted" style="font-size:11px">${escapeHtml(f.source_tool || '-')}</td>
          <td>${Components.statusBadge(f.status)}</td>
          <td class="finding-chevron text-muted" aria-hidden="true">▸</td>
        </tr>
        <tr class="finding-detail-row" data-finding-idx="${idx}" hidden>
          <td colspan="7" class="finding-detail-cell">${detailHTML}</td>
        </tr>
      `;
    }).join('');

    return `<div class="card">
      <div class="card-label">Findings from this scan <span class="text-muted" style="font-weight:400;text-transform:none">(${findings.length})</span></div>
      <div class="table-container" style="border:none;margin:8px 0 0">
        <table class="findings-table">
          <thead><tr>
            <th>Severity</th>
            <th>Title</th>
            <th>Type</th>
            <th>Asset</th>
            <th>Tool</th>
            <th>Status</th>
            <th aria-label="expand"></th>
          </tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
    </div>`;
  },

  // Asset is split into two cells so each column is scannable on its own:
  // TYPE uses a uniform-width muted badge (good for eyeballing groupings
  // like "all port_service findings"), while ASSET carries the raw value
  // in monospace. template_id drops out of the main table entirely —
  // it's reference-level detail better shown in the accordion.
  formatAssetTypeCell(f) {
    if (!f.asset_type) return '<span class="text-muted">—</span>';
    return `<span class="badge badge-muted">${escapeHtml(this.titleCase(f.asset_type))}</span>`;
  },

  formatAssetValueCell(f) {
    if (f.asset_value) {
      const label = f.asset_value.length > 60 ? f.asset_value.slice(0, 57) + '…' : f.asset_value;
      if (f.asset_id) {
        return `<a href="#/assets/${f.asset_id}" onclick="event.stopPropagation()">${escapeHtml(label)}</a>`;
      }
      return escapeHtml(label);
    }
    if (f.asset_id) {
      return `<a href="#/assets/${f.asset_id}" class="text-muted" onclick="event.stopPropagation()">${f.asset_id.slice(0, 8)}</a>`;
    }
    return '<span class="text-muted">—</span>';
  },

  // renderFindingDetail renders the expanded-row content for a single
  // finding. Shows the distinguishing bits the list row omits (evidence,
  // description, confidence/CVSS/CVE, timestamps) and offers a "View
  // finding" link for the full-page view.
  renderFindingDetail(f) {
    const meta = [];
    if (f.asset_value) {
      meta.push({ label: 'Asset', value: `<span class="mono">${escapeHtml(f.asset_value)}</span>` });
    }
    if (f.template_id) {
      meta.push({ label: 'Template', value: `<span class="mono">${escapeHtml(f.template_id)}</span>` });
    }
    if (f.cvss) meta.push({ label: 'CVSS', value: String(f.cvss) });
    if (f.cve) meta.push({ label: 'CVE', value: `<span class="mono">${escapeHtml(f.cve)}</span>` });
    if (typeof f.confidence === 'number') meta.push({ label: 'Confidence', value: f.confidence + '%' });
    if (f.first_seen) meta.push({ label: 'First seen', value: Components.timeAgo(f.first_seen) });
    if (f.last_seen) meta.push({ label: 'Last seen', value: Components.timeAgo(f.last_seen) });

    const metaGrid = meta.length > 0
      ? `<div class="finding-detail-grid">${meta.map(m => `<span class="detail-label">${m.label}</span><span class="detail-value">${m.value}</span>`).join('')}</div>`
      : '';

    const desc = f.description
      ? `<div class="finding-detail-section"><div class="finding-detail-heading">Description</div><div class="finding-detail-text">${escapeHtml(f.description)}</div></div>`
      : '';
    const evidence = f.evidence
      ? `<div class="finding-detail-section"><div class="finding-detail-heading">Evidence</div><pre class="finding-detail-evidence">${escapeHtml(f.evidence)}</pre></div>`
      : '';
    const remediation = f.remediation
      ? `<div class="finding-detail-section"><div class="finding-detail-heading">Remediation</div><div class="finding-detail-text">${escapeHtml(f.remediation)}</div></div>`
      : '';

    const viewBtn = `<a href="#/findings/${f.id}" class="btn btn-sm" onclick="event.stopPropagation()">View finding →</a>`;

    return `
      <div class="finding-detail-body">
        ${metaGrid}
        ${desc}
        ${evidence}
        ${remediation}
        <div class="finding-detail-actions">${viewBtn}</div>
      </div>
    `;
  },

  // bindFindingsAccordion wires click handlers on finding summary rows
  // to toggle their paired detail row. Uses data-finding-idx for
  // pairing so repeated re-renders (polling) don't lose or duplicate
  // listeners — we re-bind fresh each time.
  bindFindingsAccordion() {
    document.querySelectorAll('.finding-row').forEach(row => {
      row.addEventListener('click', (e) => {
        // Ignore clicks that originated on interactive descendants so
        // the "View finding" link continues to navigate instead of
        // toggling.
        if (e.target.closest('a, button')) return;
        const idx = row.dataset.findingIdx;
        const detail = document.querySelector(`.finding-detail-row[data-finding-idx="${idx}"]`);
        if (!detail) return;
        const isOpen = !detail.hasAttribute('hidden');
        if (isOpen) {
          detail.setAttribute('hidden', '');
          row.classList.remove('finding-row-open');
        } else {
          detail.removeAttribute('hidden');
          row.classList.add('finding-row-open');
        }
      });
    });
  },

  // agent-spec 2.0: a Scan exposes three semantically distinct aggregates.
  // Each gets its own card so the user can read them as separate questions:
  //
  //   Target state — what currently exists on the target
  //   Changes      — what THIS scan changed vs. prior state
  //   Work         — telemetry of the execution itself
  //
  // Mirrors the three-block layout of `surfbot scan` CLI output for
  // CLI ↔ webui parity.
  renderScanAggregates(scan) {
    return `
      ${this.renderTargetStateBlock(scan)}
      ${this.renderScanDeltaBlock(scan)}
      ${this.renderScanWorkBlock(scan)}
    `;
  },

  renderTargetStateBlock(scan) {
    const state = scan.target_state || {};
    const assets = state.assets_by_type || {};
    const ports = state.ports_by_status || {};

    // Ordering mirrors internal/pipeline/pipeline.go printTargetStateBlock:
    // KnownAssetTypes order for assets, with ports broken down by status
    // slotted in after port_service, and any unknown asset types (open
    // vocabulary) appended alphabetically at the tail. Findings open
    // closes the card after a divider.
    const stateItems = [
      { label: 'Subdomains', value: assets.subdomain },
      { label: 'IPs (v4)', value: assets.ipv4 },
      { label: 'IPs (v6)', value: assets.ipv6 },
      { label: 'Ports (open)', value: ports.open },
      { label: 'Ports (filtered)', value: ports.filtered },
      { label: 'Endpoints', value: assets.url },
      { label: 'Technologies', value: assets.technology },
      { label: 'Domains', value: assets.domain },
      { label: 'Services', value: assets.service },
    ].filter(i => i.value > 0);

    const known = new Set(['subdomain', 'ipv4', 'ipv6', 'port_service', 'url', 'technology', 'domain', 'service']);
    const extras = Object.entries(assets)
      .filter(([k, v]) => !known.has(k) && v > 0)
      .sort(([a], [b]) => a.localeCompare(b));
    extras.forEach(([k, v]) => stateItems.push({ label: this.titleCase(k), value: v }));

    if (state.findings_open_total > 0) {
      stateItems.push({ label: 'Findings open', value: state.findings_open_total, divider: true });
    }

    if (stateItems.length === 0) return '';

    const sevs = state.findings_open || {};
    const hasSev = Object.values(sevs).some(v => v > 0);

    return `<div class="card">
      <div class="card-label">Target state</div>
      <div class="detail-grid" style="margin-top:8px">
        ${stateItems.map(i => {
          const dividerCls = i.divider ? ' detail-divider' : '';
          return `<span class="detail-label${dividerCls}">${escapeHtml(i.label)}</span><span class="detail-value${dividerCls}">${i.value}</span>`;
        }).join('')}
      </div>
      ${hasSev ? Components.severityBars({
        critical: sevs.critical || 0,
        high: sevs.high || 0,
        medium: sevs.medium || 0,
        low: sevs.low || 0,
        info: sevs.info || 0,
      }) : ''}
    </div>`;
  },

  renderScanDeltaBlock(scan) {
    const delta = scan.delta || {};

    // Baseline scans have no meaningful delta — surface that honestly
    // instead of showing zeros that look like "nothing happened".
    if (delta.is_baseline) {
      return `<div class="card">
        <div class="card-label">Changes</div>
        <div class="detail-grid detail-grid-message" style="margin-top:8px">
          <span class="text-muted">Baseline scan — run again to detect changes.</span>
        </div>
      </div>`;
    }

    const sumMap = (m) => Object.values(m || {}).reduce((a, b) => a + b, 0);
    const newAssetsTotal = sumMap(delta.new_assets);
    const disAssetsTotal = sumMap(delta.disappeared_assets);
    const modAssetsTotal = sumMap(delta.modified_assets);
    const newFindingsTotal = sumMap(delta.new_findings);
    const resolvedFindingsTotal = sumMap(delta.resolved_findings);
    const total = newAssetsTotal + disAssetsTotal + modAssetsTotal + newFindingsTotal + resolvedFindingsTotal;

    if (total === 0) {
      return `<div class="card">
        <div class="card-label">Changes</div>
        <div class="detail-grid detail-grid-message" style="margin-top:8px">
          <span class="text-muted">No changes since last scan.</span>
        </div>
      </div>`;
    }

    const formatTypeBreakdown = (m) => {
      const entries = Object.entries(m || {}).filter(([, v]) => v > 0);
      if (entries.length === 0) return '';
      return ' <span class="text-muted">(' + entries.map(([k, v]) => `${v} ${this.titleCase(k)}`).join(', ') + ')</span>';
    };

    const formatSevBreakdown = (m) => {
      const order = ['critical', 'high', 'medium', 'low', 'info'];
      const parts = order.filter(s => (m || {})[s] > 0).map(s => `${m[s]} ${s}`);
      if (parts.length === 0) return '';
      return ' <span class="text-muted">(' + parts.join(', ') + ')</span>';
    };

    const rows = [];
    if (newAssetsTotal > 0) {
      rows.push(`<span class="detail-label" style="color:var(--success)">+ New assets</span><span class="detail-value">${newAssetsTotal}${formatTypeBreakdown(delta.new_assets)}</span>`);
    }
    if (disAssetsTotal > 0) {
      rows.push(`<span class="detail-label" style="color:var(--danger)">− Disappeared assets</span><span class="detail-value">${disAssetsTotal}${formatTypeBreakdown(delta.disappeared_assets)}</span>`);
    }
    if (modAssetsTotal > 0) {
      rows.push(`<span class="detail-label" style="color:var(--sev-medium)">~ Modified assets</span><span class="detail-value">${modAssetsTotal}${formatTypeBreakdown(delta.modified_assets)}</span>`);
    }
    if (newFindingsTotal > 0) {
      rows.push(`<span class="detail-label" style="color:var(--success)">+ New findings</span><span class="detail-value">${newFindingsTotal}${formatSevBreakdown(delta.new_findings)}</span>`);
    }
    if (resolvedFindingsTotal > 0) {
      rows.push(`<span class="detail-label" style="color:var(--success)">✓ Resolved findings</span><span class="detail-value">${resolvedFindingsTotal}</span>`);
    }

    return `<div class="card">
      <div class="card-label">Changes</div>
      <div class="detail-grid" style="margin-top:8px">
        ${rows.join('')}
      </div>
    </div>`;
  },

  renderScanWorkBlock(scan) {
    const work = scan.work || {};
    if (!work.tools_run) return '';

    const items = [
      { label: 'Duration', value: this.formatDurationMs(work.duration_ms || 0) },
      { label: 'Tools run', value: work.tools_run },
    ];
    if (work.tools_failed > 0) items.push({ label: 'Tools failed', value: work.tools_failed });
    if (work.tools_skipped > 0) items.push({ label: 'Tools skipped', value: work.tools_skipped });
    if (work.raw_emissions > 0) {
      // raw_emissions is the pre-storage-dedup tool output. Surfacing it
      // in the UI helps debug noisy detectors (e.g. nuclei emitting many
      // duplicates that storage merges into a few unique findings).
      items.push({ label: 'Raw emissions', value: `${work.raw_emissions} <span class="text-muted">(pre-dedup)</span>` });
    }
    if (work.phases_run && work.phases_run.length > 0) {
      items.push({ label: 'Phases', value: work.phases_run.map(escapeHtml).join(' → ') });
    }

    return `<div class="card">
      <div class="card-label">Work</div>
      <div class="detail-grid" style="margin-top:8px">
        ${items.map(i => `<span class="detail-label">${i.label}</span><span class="detail-value">${i.value}</span>`).join('')}
      </div>
    </div>`;
  },

  titleCase(s) {
    if (!s) return s;
    return s.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
  },

  // formatConfigValue renders a tool-run config value for the config grid.
  // Arrays become comma lists, objects become compact JSON, everything
  // else falls back to String(). Long strings get trimmed so the row
  // doesn't blow out the width.
  formatConfigValue(v) {
    if (v == null) return '—';
    if (Array.isArray(v)) {
      const joined = v.map(x => String(x)).join(', ');
      return joined.length > 120 ? joined.slice(0, 117) + '…' : joined;
    }
    if (typeof v === 'object') {
      try {
        const j = JSON.stringify(v);
        return j.length > 120 ? j.slice(0, 117) + '…' : j;
      } catch {
        return '[object]';
      }
    }
    const s = String(v);
    return s.length > 120 ? s.slice(0, 117) + '…' : s;
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

  renderDetailContent(app, data, findings) {
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

    const typeBadge = `<span class="badge badge-info" style="margin-left:8px">${s.type || 'full'}</span>`;

    const timestamps = [];
    if (s.started_at) timestamps.push(`Started ${Components.formatDate(s.started_at)}`);
    if (s.finished_at) timestamps.push(`Finished ${Components.formatDate(s.finished_at)}`);

    const errorCard = s.error
      ? `<div class="card" style="border-color:rgba(248,81,73,0.3)">
          <div style="color:var(--sev-critical);font-size:13px">${escapeHtml(s.error)}</div>
        </div>`
      : '';

    // Asset Changes table is noisy during a baseline scan — every asset
    // the target has gets an "appeared (Baseline)" row that duplicates the
    // Target State card. The delta card already says "Baseline scan — run
    // again to detect changes", so hide the raw table to avoid repeating
    // the same info in three places (Target State + Changes card + Asset
    // Changes table). Non-baseline scans render the table as before.
    const isBaseline = !!(s.delta && s.delta.is_baseline);
    const changeRows = data.changes.map(c => {
      const valueCell = c.asset_id
        ? `<a href="#/assets/${c.asset_id}">${escapeHtml(c.asset_value)}</a>`
        : escapeHtml(c.asset_value);
      return `
      <tr>
        <td><span class="change-${c.change_type}">${c.change_type}</span></td>
        <td>${escapeHtml(this.titleCase(c.asset_type || ''))}</td>
        <td class="mono">${valueCell}</td>
        <td>${escapeHtml(c.summary || '-')}</td>
      </tr>
    `;
    }).join('');

    const targetLink = s.target_id
      ? `<a href="#/targets/${s.target_id}" class="mono scan-detail-target-link">${escapeHtml(data.target)}</a>`
      : `<span class="text-muted mono">${escapeHtml(data.target)}</span>`;

    const metaParts = [
      targetLink,
      `<span class="text-muted">${dur}</span>`,
    ];
    if (s.finished_at) {
      metaParts.push(`<span class="text-muted">finished ${Components.timeAgo(s.finished_at)}</span>`);
    } else if (isRunning && s.started_at) {
      metaParts.push(`<span class="text-muted">started ${Components.timeAgo(s.started_at)}</span>`);
    }
    const metaRow = `<div class="detail-meta">${metaParts.join(' <span class="detail-meta-sep">\u00B7</span> ')}</div>`;

    app.innerHTML = `
      ${Components.backLink('#/scans', 'Back to scans')}
      <div class="scan-detail-stack">
        <div class="detail-panel">
          <div class="detail-header">
            ${Components.statusBadge(s.status)}
            <h2>Scan ${Components.truncateID(s.id)}</h2>
            ${typeBadge}
            <div style="margin-left:auto">${cancelBtn}</div>
          </div>
          ${metaRow}
          ${timestamps.length > 0 ? `<div class="text-muted" style="font-size:11px;margin-top:4px">${timestamps.join(' \u00B7 ')}</div>` : ''}

          ${this.renderPhaseIndicator(s, data.tool_runs)}
          ${this.renderProgressBar(s)}
          ${errorCard}
        </div>

        ${this.renderPipeline(data.tool_runs, s.id)}

        ${this.renderFindings(findings, s.id)}

        ${this.renderScanAggregates(s)}

        ${(!isBaseline && data.changes.length > 0) ? `
          <div>
            <h3 style="margin-bottom:16px">Asset Changes <span class="text-muted" style="font-size:13px;font-weight:400">(${data.changes.length})</span></h3>
            ${Components.table(['Change', 'Type', 'Value', 'Summary'], [changeRows])}
          </div>
        ` : ''}
      </div>
    `;

    // Accordions (findings + pipeline): rebind after every render (polling
    // may re-render the whole detail, and template literal innerHTML drops
    // previous listeners).
    this.bindFindingsAccordion();
    this.bindPipelineAccordion();

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

    // Reset the main scroll container so navigating between scan details
    // lands at the top instead of wherever the previous page was
    // scrolled to. Window scrollY is already 0 (body doesn't scroll);
    // it's the inner <main class="content"> that holds the scrollbar.
    const scroller = document.querySelector('main.content');
    if (scroller) scroller.scrollTop = 0;

    try {
      const [data, findingsData] = await Promise.all([
        API.scan(id),
        // scan_scope (not scan_id) so findings discovered by this scan
        // but later re-observed by a newer scan still show up here
        // instead of silently disappearing. See SUR-244 audit.
        API.findings({ scan_scope: id, limit: 100 }),
      ]);
      this.renderDetailContent(app, data, findingsData.findings || []);

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
        const [data, findingsData] = await Promise.all([
          API.scan(id),
          // scan_scope (not scan_id) so findings discovered by this scan
        // but later re-observed by a newer scan still show up here
        // instead of silently disappearing. See SUR-244 audit.
        API.findings({ scan_scope: id, limit: 100 }),
        ]);
        if (data.scan.status !== 'running') {
          this.stopPolling();
        }
        this.renderDetailContent(app, data, findingsData.findings || []);
      } catch {
        // Ignore polling errors
      }
    }, 3000);
  },
};
