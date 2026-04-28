// PR7 (#40): scan-detail page extracted from scans.js. Owns the
// /#/scans/:id route. 4-tab view (Overview / Phases / Logs / Config),
// 5-phase tracker, live findings panel, polling lifecycle scoped to
// running scans. Cancel scan + topbar deep link integrate here. The
// Logs tab degrades to a placeholder until the backend endpoint exists
// — the HEAD probe primes a flip-on path for when it ships.
const ScanDetailPage = {
  POLL_MS: 2000,
  ELAPSED_MS: 1000,
  PHASES: [
    { key: 'discovery',  label: 'Discovery',  defaultTool: 'subfinder' },
    { key: 'resolution', label: 'Resolution', defaultTool: 'dnsx' },
    { key: 'port_scan',  label: 'Port scan',  defaultTool: 'naabu' },
    { key: 'http_probe', label: 'HTTP probe', defaultTool: 'httpx' },
    { key: 'assess',     label: 'Assessment', defaultTool: 'nuclei' },
  ],
  // Backend uses `assessment` historically; tracker normalizes both.
  PHASE_ALIASES: { assess: 'assessment', assessment: 'assess' },
  TOOL_ICONS: {
    subfinder: '\u{1F50D}', dnsx: '\u{1F4CB}', naabu: '\u{1F4E1}',
    httpx: '\u{1F310}', subjack: '\u{26A0}\uFE0F', nuclei: '\u{1F6E1}\uFE0F',
  },

  _pollTimer: null, _elapsedTimer: null, _abort: null,
  _activeTab: 'overview',
  _openToolRunIds: new Set(),  // preserves PR5 fix c3802e47
  _scanID: null, _data: null, _findings: [],
  _logsEnabled: null, _failStreak: 0, _onHashChange: null,
  // Issue #52: live log state. _logs is the in-memory line buffer
  // (full corpus, used for download); rendering caps at 500 most-recent
  // when virtualization kicks in. _logSince is the cursor for the next
  // ?since=N call; _logsPaused freezes appending. _logsWrap toggles
  // whitespace handling. _logFilters are client-side multi-select
  // chips: levels (info/warn/error) and sources (tool names).
  _logs: [], _logSince: 0, _logsPaused: false, _logsWrap: false,
  _logFilters: { levels: new Set(), sources: new Set() },
  _logsTimer: null, _logsFailStreak: 0,

  async render(app, id) {
    if (this._scanID !== id) {
      this._openToolRunIds.clear();
      this._activeTab = 'overview';
      this._failStreak = 0;
      this._logsEnabled = null;
      this._logs = [];
      this._logSince = 0;
      this._logsPaused = false;
      this._logFilters = { levels: new Set(), sources: new Set() };
    }
    this._scanID = id;
    this._destroy();
    app.innerHTML = '<div class="loading">Loading scan...</div>';
    const scroller = document.querySelector('main.content');
    if (scroller) scroller.scrollTop = 0;

    try {
      const { data, findings } = await this._fetch(id);
      this._data = data; this._findings = findings;
      this._mount(app, id);
      if (this._logsEnabled === null) {
        this._probeLogsEndpoint(id).then(enabled => {
          if (this._scanID !== id) return;
          this._logsEnabled = enabled;
          if (enabled) {
            // Prime the log buffer + start polling. Both Overview
            // (preview) and Logs (full) tabs consume _logs, so we
            // fetch once regardless of which tab is active.
            this._fetchLogs(id, /*reset*/ true).then(() => {
              if (this._activeTab === 'logs' || this._activeTab === 'overview') {
                this._renderActivePanel(app);
              }
              if (data.scan.status === 'running') this._startLogsPolling(id, app);
            });
          } else if (this._activeTab === 'logs' || this._activeTab === 'overview') {
            this._renderActivePanel(app);
          }
        });
      }
      if (data.scan.status === 'running') this.startPolling(app, id);
    } catch (err) {
      app.innerHTML = Components.emptyState('Not found', 'Scan not found.');
    }

    // Tear down on route change so polling/AbortController don't leak.
    this._onHashChange = () => {
      if ((location.hash || '').indexOf('#/scans/' + id) !== 0) {
        this._destroy();
        window.removeEventListener('hashchange', this._onHashChange);
        this._onHashChange = null;
      }
    };
    window.addEventListener('hashchange', this._onHashChange);
  },

  async _fetch(id) {
    if (this._abort) this._abort.abort();
    this._abort = new AbortController();
    const signal = this._abort.signal;
    const headers = API._headers();
    const get = async (path) => {
      const r = await fetch('/api/v1' + path, { headers, signal });
      if (!r.ok) { const e = new Error('HTTP ' + r.status); e.status = r.status; throw e; }
      return r.json();
    };
    // scan_scope (not scan_id) so findings discovered by this scan but
    // later re-observed by a newer scan still show up — see SUR-244.
    const [data, findingsResp] = await Promise.all([
      get('/scans/' + encodeURIComponent(id)),
      get('/findings?scan_scope=' + encodeURIComponent(id) + '&limit=100'),
    ]);
    return { data, findings: findingsResp.findings || [] };
  },

  async _probeLogsEndpoint(id) {
    try {
      const r = await fetch('/api/v1/scans/' + encodeURIComponent(id) + '/logs', {
        method: 'HEAD', headers: API._headers(),
      });
      return r.status === 200 || r.status === 204;
    } catch { return false; }
  },

  _mount(app, id) {
    app.innerHTML = this.template(this._data, this._findings);
    this.bindEvents(app, id);
  },

  template(data, findings) {
    const s = data.scan;
    const target = data.target || s.target_id || '-';
    return `${Components.backLink('#/scans', 'Back to scans')}
      ${this._renderHeader(s, target)}
      ${this._renderTabs()}
      <div class="scan-detail-panel" data-scan-detail-panel>${this._renderTabContent(this._activeTab, data, findings)}</div>`;
  },

  _renderHeader(s, target) {
    const shortId = (s.id || '').slice(0, 8);
    const isRunning = s.status === 'running';
    const cancelBtn = isRunning
      ? `<button type="button" class="btn btn-danger btn-sm" data-cancel-scan
           aria-label="Cancel scan ${escapeHtml(shortId)} on ${escapeHtml(target)}">Cancel scan</button>`
      : '';
    const rerunBtn = `<button type="button" class="btn btn-ghost btn-sm" disabled
         title="Coming with template re-run flow">Re-run</button>`;
    const findingsBtn = `<a href="#/findings?scan_id=${encodeURIComponent(s.id)}"
         class="btn btn-primary btn-sm">View findings</a>`;
    return `<div class="page-header scan-detail-header">
      <div class="scan-detail-titleblock">
        <h2 class="scan-detail-title">
          <span class="mono text-muted">${escapeHtml(shortId)}</span>
          <span class="scan-detail-sep" aria-hidden="true">·</span>
          ${escapeHtml(target)}
        </h2>
        <p class="scan-detail-subtitle">${this._headerSubtitle(s)}</p>
      </div>
      <div class="scan-detail-actions">${cancelBtn}${rerunBtn}${findingsBtn}</div>
    </div>`;
  },

  _headerSubtitle(s) {
    const tmpl = s.template_id ? escapeHtml(s.template_id) : (s.type || 'full') + ' scan';
    const sep = '<span class="scan-detail-sep" aria-hidden="true">·</span>';
    let when = '', extra = '';
    if (s.status === 'running' && s.started_at) {
      when = 'started ' + Components.timeAgo(s.started_at);
      const elapsed = Math.round((Date.now() - new Date(s.started_at).getTime()) / 1000);
      extra = ' ' + sep + ' <span data-elapsed="' + escapeHtml(s.started_at) + '">' + this._fmtElapsed(elapsed) + '</span>';
    } else if (s.status === 'completed' && s.finished_at) {
      const dur = (s.started_at && s.finished_at)
        ? Components.formatDuration(Math.floor((new Date(s.finished_at) - new Date(s.started_at)) / 1000))
        : '';
      when = 'finished ' + Components.timeAgo(s.finished_at);
      if (dur) extra = ' ' + sep + ' ' + escapeHtml(dur);
    } else if (s.status === 'cancelled' && s.finished_at) {
      when = 'cancelled ' + Components.timeAgo(s.finished_at);
    } else if (s.status === 'failed' && s.finished_at) {
      const errShort = s.error ? ' — ' + escapeHtml(this._truncate(s.error, 80)) : '';
      when = 'failed ' + Components.timeAgo(s.finished_at) + errShort;
    } else if (s.created_at) {
      when = 'queued ' + Components.timeAgo(s.created_at);
    }
    return tmpl + ' ' + sep + ' ' + when + extra;
  },

  _renderTabs() {
    const tabs = [
      { key: 'overview', label: 'Overview' },
      { key: 'phases',   label: 'Phases' },
      { key: 'logs',     label: 'Logs' },
      { key: 'config',   label: 'Config' },
    ];
    return `<div class="sev-tabs scan-detail-tabs" role="tablist" aria-label="Scan detail sections">
      ${tabs.map(t => {
        const isActive = t.key === this._activeTab;
        return `<button type="button" class="sev-tab${isActive ? ' sev-tab-active' : ''}" role="tab"
          aria-selected="${isActive}" data-scan-tab="${t.key}">${escapeHtml(t.label)}</button>`;
      }).join('')}
    </div>`;
  },

  _renderTabContent(tab, data, findings) {
    if (tab === 'phases') return this._renderPhases(data);
    if (tab === 'logs')   return this._renderLogs(data, /*full*/true);
    if (tab === 'config') return this._renderConfig(data);
    return this._renderOverview(data, findings);
  },

  // ---- Overview tab ------------------------------------------------

  _renderOverview(data, findings) {
    const s = data.scan;
    const errorCard = s.error
      ? `<div class="card scan-error-card"><div class="text-danger">${escapeHtml(s.error)}</div></div>`
      : '';
    return `<div class="dash-grid scan-detail-overview">
      <div class="dash-col-8">
        ${this._renderProgressCard(s)}
        ${this._renderPhaseTracker(s, data.tool_runs || [])}
        ${errorCard}
      </div>
      <div class="dash-col-4">${this._renderFindingsPanel(s, findings)}</div>
      <div class="dash-col-12 scan-log-preview-slot">${this._renderLogs(data, /*full*/false)}</div>
    </div>`;
  },

  _renderProgressCard(s) {
    const isRunning = s.status === 'running';
    const pct = Math.round(s.progress || 0);
    const elapsedTxt = (s.started_at)
      ? this._fmtElapsed(Math.round(((s.finished_at ? new Date(s.finished_at) : Date.now()) - new Date(s.started_at).getTime()) / 1000))
      : '—';
    const fillCls = isRunning ? 'progress-bar-fill marquee' : 'progress-bar-fill';
    const fillStyle = isRunning && pct === 0 ? 'width:100%' : 'width:' + pct + '%';
    return `<div class="card scan-progress-card">
      <div class="scan-progress-row">
        <div class="scan-progress-pct">${pct}%</div>
        <div class="scan-progress-meta">
          <span>elapsed ${escapeHtml(elapsedTxt)}</span>
          ${isRunning ? '<span class="text-muted"> · phase ' + escapeHtml(s.phase || 'initializing') + '</span>' : ''}
        </div>
      </div>
      <div class="progress-bar-track" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${pct}">
        <div class="${fillCls}" style="${fillStyle}"></div>
      </div>
    </div>`;
  },

  _renderPhaseTracker(s, toolRuns) {
    const rows = this.PHASES.map((p, i) => {
      const status = this._derivePhaseStatus(p.key, toolRuns, s);
      const pct = this._derivePhaseProgress(p.key, toolRuns, s, status);
      const phaseRuns = this._toolRunsForPhase(p.key, toolRuns);
      const tools = phaseRuns.length > 0 ? phaseRuns.map(t => t.tool_name).join(', ') : p.defaultTool;
      const dur = this._phaseDuration(phaseRuns);
      const summary = this._phaseSummary(phaseRuns);
      const isLast = i === this.PHASES.length - 1;
      return `<li class="phase-row phase-row-${status}">
        <div class="phase-marker">
          <span class="phase-dot phase-dot-${status}" aria-hidden="true"></span>
          ${isLast ? '' : '<span class="phase-stem"></span>'}
        </div>
        <div class="phase-body">
          <div class="phase-meta">
            <div class="phase-name">${escapeHtml(p.label)}</div>
            <div class="phase-tool text-muted">${escapeHtml(tools)}</div>
          </div>
          <div class="phase-progress">${this._renderPhaseBar(status, pct)}</div>
          <div class="phase-duration text-muted">${escapeHtml(dur || '—')}</div>
          <div class="phase-summary text-muted">${summary}</div>
        </div>
      </li>`;
    }).join('');
    return `<div class="card scan-phase-card">
      <div class="card-label">Phases</div>
      <ol class="phase-tracker" aria-label="Scan phases">${rows}</ol>
    </div>`;
  },

  _renderPhaseBar(status, pct) {
    if (status === 'pending') return '<div class="phase-bar phase-bar-pending"></div>';
    if (status === 'failed')  return '<div class="phase-bar phase-bar-failed" style="width:100%"></div>';
    if (status === 'done')    return '<div class="phase-bar phase-bar-done" style="width:100%"></div>';
    return `<div class="phase-bar-track"><div class="phase-bar-fill marquee" style="width:${pct}%"></div></div>`;
  },

  _derivePhaseStatus(phaseKey, toolRuns, scan) {
    const runs = this._toolRunsForPhase(phaseKey, toolRuns);
    if (runs.length === 0) {
      // Active phase with no tool runs yet still reads "running" so the
      // tracker shows motion before the first ToolRun row materializes.
      if (scan && scan.status === 'running' && this._matchesPhase(phaseKey, scan.phase)) return 'running';
      return 'pending';
    }
    if (runs.some(r => r.status === 'failed' || r.status === 'timeout')) return 'failed';
    if (runs.some(r => r.status === 'running' || r.status === 'pending')) return 'running';
    if (runs.every(r => r.status === 'completed' || r.status === 'skipped')) return 'done';
    return 'running';
  },

  _derivePhaseProgress(phaseKey, toolRuns, scan, status) {
    if (status !== 'running') return status === 'done' ? 100 : 0;
    const runs = this._toolRunsForPhase(phaseKey, toolRuns);
    if (runs.length === 0) return Math.max(5, Math.round(scan.progress || 5));
    const done = runs.filter(r => r.status === 'completed' || r.status === 'skipped' || r.status === 'failed').length;
    return Math.max(Math.round((done / runs.length) * 100), 5);
  },

  _toolRunsForPhase(phaseKey, toolRuns) {
    const alias = this.PHASE_ALIASES[phaseKey];
    return (toolRuns || []).filter(tr => tr.phase === phaseKey || (alias && tr.phase === alias));
  },

  _matchesPhase(phaseKey, scanPhase) {
    if (!scanPhase) return false;
    return scanPhase === phaseKey || scanPhase === this.PHASE_ALIASES[phaseKey];
  },

  _phaseDuration(runs) {
    if (!runs || runs.length === 0) return '';
    const totalMs = runs.reduce((acc, r) => acc + (r.duration_ms || 0), 0);
    if (totalMs > 0) return this._fmtDurationMs(totalMs);
    const running = runs.find(r => r.status === 'running' && r.started_at);
    if (running) {
      const elapsed = Math.round((Date.now() - new Date(running.started_at).getTime()) / 1000);
      return this._fmtElapsed(elapsed);
    }
    return '';
  },

  _phaseSummary(runs) {
    if (!runs || runs.length === 0) return '';
    const summaries = runs.map(r => r.output_summary || '').filter(Boolean);
    if (summaries.length > 0) return escapeHtml(this._truncate(summaries[0], 28));
    const targets = runs.reduce((acc, r) => acc + (r.targets_count || 0), 0);
    const findings = runs.reduce((acc, r) => acc + (r.findings_count || 0), 0);
    if (findings > 0) return findings + ' emitted';
    if (targets > 0) return targets + ' targets';
    return '';
  },

  // ---- Findings panel ---------------------------------------------

  _renderFindingsPanel(s, findings) {
    const top = (findings || []).slice(0, 8);
    const body = top.length === 0
      ? '<div class="findings-panel-empty text-muted">No findings yet</div>'
      : top.map((f, idx) => this._renderFindingsRow(f, idx)).join('');
    const counter = (findings && findings.length > 0)
      ? `<span class="findings-panel-count">${findings.length}</span>`
      : '';
    return `<div class="card findings-panel">
      <div class="card-label findings-panel-header"><span>Findings discovered</span>${counter}</div>
      <div class="findings-panel-list">${body}</div>
      <a class="findings-panel-cta" href="#/findings?scan_id=${encodeURIComponent(s.id)}">View all in Findings →</a>
    </div>`;
  },

  _renderFindingsRow(f, idx) {
    const title = this._truncate(f.title || '(untitled)', 60);
    const asset = this._truncate(f.asset_value || '', 36);
    return `<a class="findings-panel-row" href="#/findings/${encodeURIComponent(f.id)}" data-finding-row="${idx}">
      ${Components.severityPill(f.severity)}
      <div class="findings-panel-text">
        <div class="findings-panel-title">${escapeHtml(title)}</div>
        ${asset ? '<div class="findings-panel-asset mono text-muted">' + escapeHtml(asset) + '</div>' : ''}
      </div>
    </a>`;
  },

  // ---- Phases tab --------------------------------------------------

  _renderPhases(data) {
    const tracker = this._renderPhaseTracker(data.scan, data.tool_runs || []);
    const groups = this.PHASES.map(p => {
      const runs = this._toolRunsForPhase(p.key, data.tool_runs || []);
      if (runs.length === 0) return '';
      const sorted = [...runs].sort((a, b) => {
        if (a.started_at && b.started_at) return new Date(a.started_at) - new Date(b.started_at);
        return 0;
      });
      const nodes = sorted.map((tr, i) => this._renderPipelineNode(tr, i, sorted.length)).join('');
      return `<div class="phase-group">
        <div class="phase-group-label">${escapeHtml(p.label)}</div>
        <div class="pipeline-view">${nodes}</div>
      </div>`;
    }).join('');
    return tracker + (groups || '<div class="empty-state"><p>No tool runs recorded yet.</p></div>');
  },

  _renderPipelineNode(tr, i, total) {
    const icon = this.TOOL_ICONS[tr.tool_name] || '\u{1F527}';
    const isLast = i === total - 1;
    const statusClass = 'status-' + (tr.status || 'pending');
    let duration = '';
    if (tr.status === 'running' && tr.started_at) {
      const elapsed = Math.round((Date.now() - new Date(tr.started_at).getTime()) / 1000);
      duration = `<span class="tool-duration mono" data-started="${escapeHtml(tr.started_at)}">${this._fmtElapsed(elapsed)}</span>`;
    } else if (tr.duration_ms > 0) {
      duration = `<span class="tool-duration mono">${this._fmtDurationMs(tr.duration_ms)}</span>`;
    } else if (tr.status === 'completed') {
      duration = `<span class="tool-duration mono text-muted">&lt;1ms</span>`;
    }
    const stats = [];
    if (tr.targets_count > 0) stats.push(`<span class="stat-badge">${tr.targets_count} ${tr.targets_count === 1 ? 'target' : 'targets'}</span>`);
    if (tr.findings_count > 0) stats.push(`<span class="stat-badge stat-badge-finding" title="Raw emissions before storage dedup">${tr.findings_count} emitted</span>`);
    const hasLogs = this._toolRunHasLogs(tr);
    const trID = tr.id || '';
    const isOpen = hasLogs && trID && this._openToolRunIds.has(trID);
    const nodeCls = `pipeline-node ${statusClass}${hasLogs ? ' pipeline-node-clickable' : ''}${isOpen ? ' pipeline-node-open' : ''}`;
    return `<div class="${nodeCls}" data-tr-idx="${i}" data-tr-id="${escapeHtml(trID)}"${hasLogs ? ' title="Click to view logs"' : ''}>
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
        ${hasLogs ? `<div class="pipeline-detail" data-tr-idx="${i}" data-tr-id="${escapeHtml(trID)}"${isOpen ? '' : ' hidden'}>${this._renderToolRunDetail(tr)}</div>` : ''}
      </div>
    </div>`;
  },

  _toolRunHasLogs(tr) {
    if (tr.output_summary) return true;
    if (tr.error_message) return true;
    const cfg = tr.config || {};
    return !!(cfg.command || cfg.stderr_tail || (cfg.input_preview && cfg.input_preview.length > 0));
  },

  _renderToolRunDetail(tr) {
    const cfg = tr.config || {};
    const parts = [];
    const section = (heading, body, extraCls) => parts.push(
      `<div class="pipeline-detail-section">
        <div class="pipeline-detail-heading${extraCls || ''}">${heading}</div>${body}
      </div>`);
    if (tr.output_summary) section('Summary', `<div class="pipeline-detail-text">${escapeHtml(tr.output_summary)}</div>`);
    if (cfg.command) section('Command', `<pre class="pipeline-detail-pre">${escapeHtml(cfg.command)}</pre>`);
    const generic = new Set(['command', 'stderr_tail', 'exit_code', 'input_preview', 'inputs_truncated']);
    const toolKeys = Object.keys(cfg).filter(k => !generic.has(k));
    if (toolKeys.length > 0) {
      const rows = toolKeys.map(k =>
        `<span class="detail-label">${escapeHtml(k)}</span><span class="detail-value mono">${escapeHtml(this._fmtConfigValue(cfg[k]))}</span>`).join('');
      section('Config', `<div class="pipeline-detail-grid">${rows}</div>`);
    }
    if (cfg.input_preview && cfg.input_preview.length > 0) {
      const truncated = cfg.inputs_truncated ? ' <span class="text-muted">(truncated)</span>' : '';
      section('Inputs' + truncated, `<pre class="pipeline-detail-pre">${cfg.input_preview.map(escapeHtml).join('\n')}</pre>`);
    }
    if (cfg.stderr_tail) section('Stderr tail', `<pre class="pipeline-detail-pre pipeline-detail-stderr">${escapeHtml(cfg.stderr_tail)}</pre>`);
    if (tr.error_message) section('Error', `<pre class="pipeline-detail-pre pipeline-detail-stderr">${escapeHtml(tr.error_message)}</pre>`, ' pipeline-detail-heading-error');
    const meta = [];
    if (tr.started_at) meta.push(`started ${Components.timeAgo(tr.started_at)}`);
    if (typeof cfg.exit_code === 'number') meta.push(`exit ${cfg.exit_code}`);
    if (meta.length > 0) parts.push(`<div class="pipeline-detail-meta text-muted">${meta.join(' · ')}</div>`);
    return `<div class="pipeline-detail-body">${parts.join('')}</div>`;
  },

  // ---- Logs tab + preview (issue #52) -----------------------------

  _renderLogs(data, full) {
    if (this._logsEnabled !== true) {
      const detail = full
        ? '<p class="text-muted">Live log streaming requires backend support. The page is wired to consume <span class="mono">/api/v1/scans/' + escapeHtml(data.scan.id) + '/logs?since=N</span> as soon as it ships.</p>'
        : '<p class="text-muted">Live log preview will appear here once the backend logs endpoint ships.</p>';
      return `<div class="card scan-logs-card scan-logs-placeholder" role="log" aria-live="polite">
        <div class="card-label">Live log</div>${detail}
      </div>`;
    }
    const filtered = this._filteredLogs();
    // Overview tab: last 8 lines + CTA. Logs tab: full corpus with
    // toolbar (filters, pause, wrap, download) and virtualized tail.
    if (!full) {
      const tail = filtered.slice(-8);
      const lines = tail.length === 0
        ? '<div class="scan-log-empty text-muted">No log lines yet — scan is starting.</div>'
        : tail.map(l => this._renderLogLine(l)).join('');
      const cta = `<a class="scan-log-cta" href="javascript:void(0)" data-log-open-tab>Open Logs →</a>`;
      return `<div class="card scan-logs-card" role="log" aria-live="polite">
        <div class="card-label scan-log-header">
          <span>Live log</span>
          <span class="scan-log-count text-muted">${filtered.length}/${this._logs.length}</span>
        </div>
        <pre class="scan-log-pre scan-log-pre-preview${this._logsWrap ? ' scan-log-wrap' : ''}">${lines}</pre>
        ${cta}
      </div>`;
    }
    const allLines = filtered.length === 0
      ? '<div class="scan-log-empty text-muted">No log lines match the current filters.</div>'
      : filtered.map(l => this._renderLogLine(l)).join('');
    return `<div class="card scan-logs-card" role="log" aria-live="polite">
      <div class="card-label scan-log-header">
        <span>Live log</span>
        <span class="scan-log-count text-muted">${filtered.length}/${this._logs.length}</span>
      </div>
      ${this._renderLogToolbar()}
      <pre class="scan-log-pre scan-log-pre-full${this._logsWrap ? ' scan-log-wrap' : ''}" data-log-pre>${allLines}</pre>
    </div>`;
  },

  _renderLogLine(l) {
    const ts = l.ts ? this._fmtLogTime(l.ts) : '';
    const lvl = l.level || 'info';
    const src = l.source || 'scanner';
    return `<span class="scan-log-line scan-log-line-${escapeHtml(lvl)}">`
      + `<span class="scan-log-ts text-muted">[${escapeHtml(ts)}]</span> `
      + `<span class="scan-log-src">${escapeHtml(src)}</span> `
      + `<span class="scan-log-text">${escapeHtml(l.text || '')}</span>`
      + `\n</span>`;
  },

  _renderLogToolbar() {
    const sources = Array.from(new Set(this._logs.map(l => l.source))).sort();
    const levels = ['info', 'warn', 'error'];
    const levelChips = levels.map(lv => {
      const active = this._logFilters.levels.has(lv);
      return `<button type="button" class="scan-log-chip scan-log-chip-${lv}${active ? ' active' : ''}" data-log-level="${lv}">${lv}</button>`;
    }).join('');
    const sourceChips = sources.map(src => {
      const active = this._logFilters.sources.has(src);
      return `<button type="button" class="scan-log-chip${active ? ' active' : ''}" data-log-source="${escapeHtml(src)}">${escapeHtml(src)}</button>`;
    }).join('');
    return `<div class="scan-log-toolbar">
      <div class="scan-log-chips">
        <span class="scan-log-chips-label text-muted">level:</span>${levelChips}
        ${sources.length > 0 ? '<span class="scan-log-chips-sep" aria-hidden="true">·</span>' : ''}
        ${sources.length > 0 ? '<span class="scan-log-chips-label text-muted">tool:</span>' + sourceChips : ''}
      </div>
      <div class="scan-log-actions">
        <button type="button" class="btn btn-ghost btn-sm" data-log-pause aria-pressed="${this._logsPaused}">${this._logsPaused ? 'Resume' : 'Pause'}</button>
        <button type="button" class="btn btn-ghost btn-sm" data-log-wrap aria-pressed="${this._logsWrap}">${this._logsWrap ? 'No wrap' : 'Wrap'}</button>
        <button type="button" class="btn btn-ghost btn-sm" data-log-download>Download</button>
      </div>
    </div>`;
  },

  _filteredLogs() {
    const lv = this._logFilters.levels;
    const src = this._logFilters.sources;
    if (lv.size === 0 && src.size === 0) return this._logs;
    return this._logs.filter(l => {
      if (lv.size > 0 && !lv.has(l.level)) return false;
      if (src.size > 0 && !src.has(l.source)) return false;
      return true;
    });
  },

  async _fetchLogs(id, reset) {
    if (reset) {
      this._logs = [];
      this._logSince = 0;
    }
    try {
      // Loop the cursor a few times in case the first batch is short
      // — guards against the case where the polling cadence is slower
      // than the burst rate at scan start.
      for (let i = 0; i < 5; i++) {
        const resp = await API.scanLogs(id, { since: this._logSince, limit: 200 });
        const lines = (resp && resp.lines) || [];
        if (lines.length === 0) break;
        this._appendLogs(lines);
        if (resp && resp.next != null) this._logSince = resp.next;
        if (!resp.has_more) break;
      }
      this._logsFailStreak = 0;
      return true;
    } catch (err) {
      this._logsFailStreak++;
      return false;
    }
  },

  _appendLogs(lines) {
    if (!lines || lines.length === 0) return;
    if (this._logsPaused) {
      // Still ingest into the cursor-tracked buffer so Resume catches up.
      // Storing into _logs (vs. a side queue) is simplest — the renderer
      // is the single throttle.
    }
    for (const l of lines) this._logs.push(l);
  },

  _startLogsPolling(id, app) {
    if (this._logsTimer) clearInterval(this._logsTimer);
    this._logsTimer = setInterval(async () => {
      if (document.visibilityState === 'hidden') return;
      const ok = await this._fetchLogs(id, /*reset*/ false);
      if (!ok) return;
      // Re-render the active panel only if it consumes _logs. The
      // Phases / Config tabs don't, so the polling tick is invisible
      // there — saves DOM churn.
      if (this._activeTab === 'overview' || this._activeTab === 'logs') {
        this._renderActivePanel(app);
        if (!this._logsPaused) this._scrollLogsToBottom(app);
      }
      // Stop polling when scan reaches a terminal status. The main
      // _data poll updates _data; we read it lazily here.
      const s = this._data && this._data.scan;
      if (s && s.status !== 'running') {
        clearInterval(this._logsTimer);
        this._logsTimer = null;
      }
    }, this.POLL_MS);
  },

  _scrollLogsToBottom(app) {
    const pre = app.querySelector('[data-log-pre]');
    if (pre) pre.scrollTop = pre.scrollHeight;
  },

  _bindLogControls(app, id) {
    // Open Logs tab from the Overview preview CTA.
    const opener = app.querySelector('[data-log-open-tab]');
    if (opener) opener.addEventListener('click', () => {
      this._activeTab = 'logs';
      const tablist = app.querySelector('.scan-detail-tabs');
      if (tablist) {
        tablist.outerHTML = this._renderTabs();
        this._wireTabClicks(app, id);
      }
      this._renderActivePanel(app);
    });

    app.querySelectorAll('[data-log-level]').forEach(btn => {
      btn.addEventListener('click', () => {
        const lv = btn.getAttribute('data-log-level');
        if (this._logFilters.levels.has(lv)) this._logFilters.levels.delete(lv);
        else this._logFilters.levels.add(lv);
        this._renderActivePanel(app);
      });
    });
    app.querySelectorAll('[data-log-source]').forEach(btn => {
      btn.addEventListener('click', () => {
        const src = btn.getAttribute('data-log-source');
        if (this._logFilters.sources.has(src)) this._logFilters.sources.delete(src);
        else this._logFilters.sources.add(src);
        this._renderActivePanel(app);
      });
    });
    const pauseBtn = app.querySelector('[data-log-pause]');
    if (pauseBtn) pauseBtn.addEventListener('click', () => {
      this._logsPaused = !this._logsPaused;
      this._renderActivePanel(app);
      if (!this._logsPaused) this._scrollLogsToBottom(app);
    });
    const wrapBtn = app.querySelector('[data-log-wrap]');
    if (wrapBtn) wrapBtn.addEventListener('click', () => {
      this._logsWrap = !this._logsWrap;
      this._renderActivePanel(app);
    });
    const dlBtn = app.querySelector('[data-log-download]');
    if (dlBtn) dlBtn.addEventListener('click', () => this._downloadLogs(id));
  },

  _downloadLogs(id) {
    const txt = this._logs.map(l => {
      const ts = l.ts ? this._fmtLogTime(l.ts) : '';
      return `[${ts}] [${l.level || 'info'}] [${l.source || 'scanner'}] ${l.text || ''}`;
    }).join('\n');
    const blob = new Blob([txt], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'scan-' + id.slice(0, 8) + '-logs.txt';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    setTimeout(() => URL.revokeObjectURL(url), 0);
  },

  _fmtLogTime(iso) {
    if (!iso) return '';
    const d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    const pad = n => String(n).padStart(2, '0');
    return pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
  },

  // ---- Config tab --------------------------------------------------

  _renderConfig(data) {
    const groups = this.PHASES.map(p => {
      const runs = this._toolRunsForPhase(p.key, data.tool_runs || []);
      if (runs.length === 0) return '';
      const items = runs.map(tr => this._renderConfigRun(tr)).join('');
      return `<div class="config-phase">
        <div class="config-phase-label">${escapeHtml(p.label.toUpperCase())}</div>
        ${items}
      </div>`;
    }).join('');
    if (!groups) return '<div class="card"><div class="text-muted">No tool runs recorded yet.</div></div>';
    return `<div class="card scan-config-card">${groups}</div>`;
  },

  _renderConfigRun(tr) {
    const cfg = tr.config || {};
    const keys = Object.keys(cfg);
    const body = keys.length === 0
      ? '<div class="text-muted">No config recorded for this run.</div>'
      : '<dl class="config-list">' + keys.map(k =>
          `<dt class="mono">${escapeHtml(k)}</dt><dd class="mono">${escapeHtml(this._fmtConfigValue(cfg[k]))}</dd>`
        ).join('') + '</dl>';
    return `<div class="config-run">
      <div class="config-run-name">─ ${escapeHtml(tr.tool_name)}</div>
      ${body}
    </div>`;
  },

  // ---- Events ------------------------------------------------------

  bindEvents(app, id) {
    this._wireTabClicks(app, id);
    const cancelBtn = app.querySelector('[data-cancel-scan]');
    if (cancelBtn) cancelBtn.addEventListener('click', () => this.cancelScan(app, id));
    this._bindAccordion();
    this._bindLogControls(app, id);
  },

  _wireTabClicks(app, id) {
    app.querySelectorAll('[data-scan-tab]').forEach(btn => {
      btn.addEventListener('click', () => {
        const next = btn.getAttribute('data-scan-tab');
        if (next === this._activeTab) return;
        this._activeTab = next;
        const tablist = app.querySelector('.scan-detail-tabs');
        if (tablist) {
          tablist.outerHTML = this._renderTabs();
          this._wireTabClicks(app, id);
        }
        this._renderActivePanel(app);
      });
    });
  },

  _renderActivePanel(app) {
    const slot = app.querySelector('[data-scan-detail-panel]');
    if (!slot) return;
    slot.innerHTML = this._renderTabContent(this._activeTab, this._data, this._findings);
    this._bindAccordion();
    this._bindLogControls(app, this._scanID);
  },

  // Pipeline accordion. _openToolRunIds persists open state across
  // re-renders (PR5 fix c3802e47); this handler only mutates the Set.
  _bindAccordion() {
    document.querySelectorAll('.pipeline-node-clickable').forEach(node => {
      if (node.dataset.bound === '1') return;
      node.dataset.bound = '1';
      node.addEventListener('click', (e) => {
        if (e.target.closest('a, button')) return;
        const idx = node.dataset.trIdx;
        const trID = node.dataset.trId || '';
        const detail = node.querySelector(`.pipeline-detail[data-tr-idx="${idx}"]`);
        if (!detail) return;
        const isOpen = !detail.hasAttribute('hidden');
        if (isOpen) {
          detail.setAttribute('hidden', '');
          node.classList.remove('pipeline-node-open');
          if (trID) this._openToolRunIds.delete(trID);
        } else {
          detail.removeAttribute('hidden');
          node.classList.add('pipeline-node-open');
          if (trID) this._openToolRunIds.add(trID);
        }
      });
    });
  },

  async cancelScan(app, id) {
    const ok = await Components.confirmDialog({
      title: 'Cancel scan?',
      message: 'Any in-flight tool runs will be terminated.',
      confirmLabel: 'Cancel scan',
      danger: true,
    });
    if (!ok) return;
    try {
      await API.cancelScan(id);
      Components.toast.success('Scan cancelled');
      try {
        const { data, findings } = await this._fetch(id);
        this._data = data; this._findings = findings;
        this._mount(app, id);
        if (data.scan.status !== 'running') this.stopPolling();
      } catch (_) {}
    } catch (err) {
      // 404 = scan vanished mid-cancel; 409/400 = it terminated first.
      if (err && err.status === 404) Components.toast.info('Scan already completed.');
      else if (err && err.status === 409) Components.toast.info('Scan finished before cancel could apply.');
      else if (err && err.status === 400) Components.toast.info('Scan is no longer running.');
      else Components.toast.error('Failed to cancel: ' + (err && err.message || 'unknown error'));
    }
  },

  // ---- Polling -----------------------------------------------------

  startPolling(app, id) {
    this.stopPolling();
    this._elapsedTimer = setInterval(() => this._tickElapsed(), this.ELAPSED_MS);
    const tick = async () => {
      if (document.visibilityState === 'hidden') return;
      try {
        const { data, findings } = await this._fetch(id);
        this._data = data; this._findings = findings;
        this._failStreak = 0;
        this._renderActivePanel(app);
        const headerRoot = app.querySelector('.scan-detail-header');
        if (headerRoot) {
          headerRoot.outerHTML = this._renderHeader(data.scan, data.target);
          const newCancel = app.querySelector('[data-cancel-scan]');
          if (newCancel) newCancel.addEventListener('click', () => this.cancelScan(app, id));
        }
        if (data.scan.status !== 'running') this.stopPolling();
      } catch (err) {
        if (err && err.name === 'AbortError') return;
        this._failStreak++;
        if (this._failStreak >= 3 && this._pollTimer) {
          clearInterval(this._pollTimer);
          this._pollTimer = setInterval(tick, this.POLL_MS * 5);
        }
      }
    };
    this._pollTimer = setInterval(tick, this.POLL_MS);
  },

  stopPolling() {
    if (this._pollTimer)    { clearInterval(this._pollTimer);    this._pollTimer = null; }
    if (this._elapsedTimer) { clearInterval(this._elapsedTimer); this._elapsedTimer = null; }
    if (this._logsTimer)    { clearInterval(this._logsTimer);    this._logsTimer = null; }
    if (this._abort)        { try { this._abort.abort(); } catch (_) {} this._abort = null; }
  },

  _destroy() { this.stopPolling(); },

  _tickElapsed() {
    document.querySelectorAll('[data-elapsed]').forEach(el => {
      const started = new Date(el.dataset.elapsed).getTime();
      if (isNaN(started)) return;
      el.textContent = this._fmtElapsed(Math.round((Date.now() - started) / 1000));
    });
    document.querySelectorAll('.tool-duration[data-started]').forEach(el => {
      const started = new Date(el.dataset.started).getTime();
      if (isNaN(started)) return;
      el.textContent = this._fmtElapsed(Math.round((Date.now() - started) / 1000));
    });
  },

  // ---- Formatters --------------------------------------------------

  _fmtElapsed(seconds) {
    if (seconds < 0) return '0s';
    if (seconds < 60) return seconds + 's';
    const m = Math.floor(seconds / 60);
    if (m < 60) return m + 'm ' + (seconds % 60) + 's';
    const h = Math.floor(m / 60);
    return h + 'h ' + (m % 60) + 'm';
  },

  _fmtDurationMs(ms) {
    if (ms < 1000) return ms + 'ms';
    const s = ms / 1000;
    if (s < 60) return s.toFixed(1) + 's';
    return Math.floor(s / 60) + 'm ' + Math.round(s % 60) + 's';
  },

  _fmtConfigValue(v) {
    if (v == null) return '—';
    if (Array.isArray(v)) {
      const j = v.map(x => String(x)).join(', ');
      return j.length > 120 ? j.slice(0, 117) + '…' : j;
    }
    if (typeof v === 'object') {
      try {
        const j = JSON.stringify(v);
        return j.length > 120 ? j.slice(0, 117) + '…' : j;
      } catch { return '[object]'; }
    }
    const s = String(v);
    return s.length > 120 ? s.slice(0, 117) + '…' : s;
  },

  _truncate(s, n) {
    if (!s) return '';
    return s.length > n ? s.slice(0, n - 1) + '…' : s;
  },
};

// Pause polling when the tab is hidden, resume when it returns.
document.addEventListener('visibilitychange', () => {
  if (typeof ScanDetailPage === 'undefined') return;
  const sd = ScanDetailPage;
  if (document.visibilityState === 'visible' && sd._scanID && !sd._pollTimer
      && sd._data && sd._data.scan && sd._data.scan.status === 'running') {
    const appEl = document.getElementById('app');
    if (appEl) sd.startPolling(appEl, sd._scanID);
  }
});
