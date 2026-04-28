// Dashboard page — UI v2 (PR3 #36). KPI quad of clickable security-
// posture cards above an activity feed composed client-side from
// /scans + /findings + last_scan.delta, plus Top Targets and Upcoming
// scans panels. Agent diagnostics live in the sidebar footer (PR2 #35).

const SEV_ORDER = ['critical', 'high', 'medium', 'low', 'info'];
const SEV_LABEL = { critical: 'Crit', high: 'High', medium: 'Med', low: 'Low', info: 'Info' };
const SCAN_STATUS_TO_PILL = { completed: 'resolved', running: 'ack', failed: 'open', cancelled: 'fp' };
const TONE_STYLE = {
  brand:    'background:rgba(0,229,153,0.15);color:var(--brand)',
  resolved: 'background:var(--status-resolved-bg);color:var(--status-resolved)',
  critical: 'background:var(--sev-critical-bg);color:var(--sev-critical)',
  high:     'background:var(--sev-high-bg);color:var(--sev-high)',
  medium:   'background:var(--sev-medium-bg);color:var(--sev-medium)',
  low:      'background:var(--sev-low-bg);color:var(--sev-low)',
  info:     'background:var(--sev-info-bg);color:var(--sev-info)',
  fp:       'background:var(--status-fp-bg);color:var(--status-fp)' };

const DashboardPage = {
  _state: null,
  _filterTab: 'all',
  _subtitleTimer: null,

  async render(app) {
    if (this._subtitleTimer) { clearInterval(this._subtitleTimer); this._subtitleTimer = null; }
    app.innerHTML = this._skeleton();
    try {
      this._state = await this._fetchAll();
      app.innerHTML = this.template(this._state);
      updateSidebarBadge(this._state.overview.findings_by_severity);
      this._bind(app);
      this._startSubtitleTimer();
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load dashboard: ' + err.message);
    }
  },

  // /upcoming silently skips paused schedules and returns IDs only;
  // /schedules recovers names + the paused list. Non-overview reads
  // degrade to empty so an older daemon doesn't blank the dashboard.
  async _fetchAll() {
    const [overview, scans, findings, targets, upcoming, schedules] = await Promise.all([
      API.overview(),
      API.scans({ limit: 20 }).catch(() => ({ scans: [] })),
      API.findings({ status: 'open', limit: 20 }).catch(() => ({ findings: [] })),
      API.targets().catch(() => ({ targets: [] })),
      API.upcomingSchedules({ limit: 5 }).catch(() => ({ items: [] })),
      API.listSchedules({ limit: 250 }).catch(() => ({ items: [] })),
    ]);
    return {
      overview, scans: scans.scans || [], findings: findings.findings || [],
      targets: targets.targets || [], upcoming: upcoming.items || [],
      schedules: schedules.items || [], fetchedAt: new Date(),
    };
  },

  _skeleton() {
    const cell = `<div class="dash-col-3 kpi-card"><div class="dash-skeleton" style="width:60%"></div><div class="dash-skeleton" style="width:40%;height:32px;margin-top:12px"></div></div>`;
    return `<div class="dash-grid">${cell.repeat(4)}</div>`;
  },

  template(d) {
    return `${this._header()}<div class="dash-grid">${this._kpiRiskScore(d.overview)}${this._kpiOpenFindings(d.overview)}${this._kpiAssets(d.overview)}${this._kpiLastScan(d.overview)}</div>
      <div class="dash-grid">
        <div class="dash-col-8">${this._activityPanel(d)}</div>
        <div class="dash-col-4" style="display:flex;flex-direction:column;gap:16px">${this._topTargetsPanel(d)}${this._upcomingPanel(d)}</div>
      </div>`;
  },

  _header() {
    return `<div class="page-header" style="display:flex;align-items:flex-end;gap:16px"><div style="flex:1"><h2>Dashboard</h2><p>Security posture — <span id="dash-subtitle"></span></p></div>
      <div style="display:flex;gap:8px"><button type="button" class="btn btn-ghost" id="dash-refresh-btn" aria-label="Refresh dashboard">${Components.icon('refresh', 14)} Refresh</button>
      <button type="button" class="btn btn-accent" id="dash-runscan-btn">Run scan</button></div></div>`;
  },

  _kpiRiskScore(ov) {
    const score = ov.security_score, cls = Components.scoreClass(score);
    const hist = Array.isArray(ov.score_history) ? ov.score_history : null;
    let extras = '', trend = '';
    if (hist && hist.length > 0) {
      const max = Math.max(1, ...hist.map(h => h.score || 0));
      const bars = hist.slice(-16).map(h => `<span class="sparkline-bar" style="height:${Math.max(8, Math.round(((h.score || 0) / max) * 100))}%"></span>`).join('');
      extras = `<div class="sparkline" aria-hidden="true">${bars}</div><div class="kpi-foot">last ${hist.length} days</div>`;
    }
    if (hist && hist.length >= 2) {
      const delta = (hist[hist.length - 1].score || 0) - (hist[hist.length - 2].score || 0);
      const tcls = delta > 0 ? 'kpi-trend-up' : delta < 0 ? 'kpi-trend-down' : 'kpi-trend-flat';
      trend = `<span class="kpi-trend ${tcls}">${delta > 0 ? '+' : ''}${delta}</span>`;
    }
    return this._kpiButton('kpi-risk',
      `Risk score ${score} of 100. Click to view findings sorted by CVSS.`,
      "location.hash='#/findings?sort=cvss-desc'",
      `<div class="kpi-header"><span class="kpi-label">Risk Score</span>${trend}</div>
       <div><span class="kpi-number ${cls}">${score}</span><span class="kpi-suffix">/100</span></div>${extras}`);
  },

  _kpiOpenFindings(ov) {
    const sev = ov.findings_by_severity || {};
    const total = SEV_ORDER.reduce((s, k) => s + (sev[k] || 0), 0);
    const stacked = total > 0 ? SEV_ORDER.map(k => {
      const v = sev[k] || 0;
      return v === 0 ? '' : `<span class="kpi-stacked-seg" style="width:${(v/total)*100}%;background:var(--sev-${k})"></span>`;
    }).join('') : '';
    const counts = total > 0
      ? `<div class="kpi-counts">${SEV_ORDER.map(k => `<span style="color:var(--sev-${k})"><strong>${sev[k] || 0}</strong> ${SEV_LABEL[k]}</span>`).join('')}</div>`
      : `<div class="kpi-foot" style="text-align:center">no open findings</div>`;
    return this._kpiButton('kpi-findings',
      `${total} open findings. Click to view findings list.`,
      "location.hash='#/findings?status=open'",
      `<div class="kpi-header"><span class="kpi-label">Open Findings</span></div>
       <div><span class="kpi-number">${total}</span><span class="kpi-suffix">unique</span></div>
       <div class="kpi-stacked-bar" aria-hidden="true">${stacked}</div>${counts}`);
  },

  _kpiAssets(ov) {
    const total = ov.total_assets || 0;
    const sorted = Object.entries(ov.assets_by_type || {}).sort((a, b) => b[1] - a[1]);
    const top4 = sorted.slice(0, 4), rest = sorted.length - top4.length;
    const pills = top4.map(([k, v]) => `<span class="kpi-mini-pill">${this._fmtNum(v)} ${escapeHtml(k)}</span>`).join('')
      + (rest > 0 ? `<span class="kpi-mini-pill">+${rest}</span>` : '');
    const dis = (ov.changes_since_last && ov.changes_since_last.disappeared_assets) || 0;
    const disPill = dis > 0 ? `<span class="pill sev-pill-critical"><span class="pill-dot"></span>−${dis} disappeared</span>` : '';
    return this._kpiButton('kpi-assets',
      `${total} discovered assets. Click to view asset inventory.`,
      "location.hash='#/assets'",
      `<div class="kpi-header"><span class="kpi-label">Discovered Assets</span></div>
       <div style="display:flex;align-items:baseline;gap:8px;flex-wrap:wrap"><span class="kpi-number">${this._fmtNum(total)}</span>${disPill}</div>
       <div class="kpi-pills">${pills}</div>`);
  },

  _kpiLastScan(ov) {
    const s = ov.last_scan;
    if (!s) {
      return `<div class="dash-col-3 kpi-card">
        <div class="kpi-header"><span class="kpi-label">Last Scan</span></div>
        <div class="dash-empty"><strong style="color:var(--text-secondary)">No scans yet</strong><p>Run <code>surfbot scan &lt;target&gt;</code> to get started.</p></div>
      </div>`;
    }
    // changes_since_last carries the flat new_findings count; last_scan.delta
    // is the per-severity map (see internal/model/scan.go ScanDelta).
    const newF = (ov.changes_since_last && ov.changes_since_last.new_findings) || 0;
    const subs = (s.target_state && s.target_state.assets_by_type && s.target_state.assets_by_type.subdomain) || 0;
    const tools = (s.work && s.work.tools_run) || 0;
    const pill = Components.statusPill(SCAN_STATUS_TO_PILL[s.status] || 'fp');
    return this._kpiButton('kpi-last-scan',
      `Last scan of ${s.target} ${s.status}. Click to view detail.`,
      `location.hash='#/scans/${encodeURIComponent(s.id)}'`,
      `<div class="kpi-header"><span class="kpi-label">Last Scan</span>${pill}</div>
       <div class="mono" style="font-size:13px;color:var(--text-primary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${escapeHtml(s.target)}</div>
       <div class="kpi-foot">${escapeHtml(this._scanTypeLabel(s))} · ${Components.formatDuration(s.duration_seconds)} · ${Components.timeAgo(s.finished_at)}</div>
       <div class="kpi-foot">${newF} new finding${newF === 1 ? '' : 's'} · ${subs} subdomains · ${tools} tools</div>
       <div style="font-size:11px;color:var(--brand)">View detail →</div>`);
  },

  _scanTypeLabel(s) {
    return s.type ? s.type.charAt(0).toUpperCase() + s.type.slice(1) + ' scan' : 'Scan';
  },

  _kpiButton(id, label, onClick, body) { return `<button type="button" class="dash-col-3 kpi-card" id="${id}" aria-label="${escapeHtml(label)}" onclick="${onClick}">${body}</button>`; },

  _activityPanel(d) {
    const tabsHtml = ['all', 'findings', 'scans', 'changes'].map(t => {
      const cls = t === this._filterTab ? 'dash-tab active' : 'dash-tab';
      const label = t === 'all' ? 'All' : t.charAt(0).toUpperCase() + t.slice(1);
      return `<button type="button" class="${cls}" data-activity-tab="${t}">${label}</button>`;
    }).join('');
    return `<div class="dash-panel">
      <div class="dash-panel-header">
        <div><h3>Activity</h3><div class="dash-panel-sub">events from the last 7 days</div></div>
        <div class="dash-tabs" role="tablist">${tabsHtml}</div>
      </div>
      <div class="dash-panel-body" id="dash-activity-body">${this._activityList(d)}</div>
    </div>`;
  },

  _activityList(d) {
    const events = this._composeEvents(d);
    const filtered = this._filterTab === 'all' ? events : events.filter(e => e.tab === this._filterTab);
    if (filtered.length === 0) return `<div class="dash-empty">No activity in the last 7 days.</div>`;
    return `<ul class="activity-list">${filtered.map(ev => this._activityRow(ev)).join('')}</ul>`;
  },

  _composeEvents(d) {
    const cutoff = Date.now() - 7 * 24 * 3600 * 1000;
    const out = [];
    const scanHref = id => `#/scans/${encodeURIComponent(id)}`;
    (d.scans || []).forEach(s => {
      if (s.status === 'running' && s.started_at) {
        const at = new Date(s.started_at).getTime();
        if (at >= cutoff) out.push({ tab: 'scans', at, icon: 'clock', tone: 'brand',
          title: 'Scan started', detail: `${s.target} · ${this._scanTypeLabel(s)}`,
          cta: 'View live →', href: scanHref(s.id) });
        return;
      }
      if (!s.finished_at) return;
      const at = new Date(s.finished_at).getTime();
      if (at < cutoff) return;
      if (s.status === 'completed') {
        const newF = sumSeverityMap(s.delta && s.delta.new_findings);
        out.push({ tab: 'scans', at, icon: 'check', tone: 'resolved', title: 'Scan completed',
          detail: `${s.target} · ${Components.formatDuration(s.duration_seconds)} · ${newF} new finding${newF === 1 ? '' : 's'}`,
          cta: 'Open report →', href: scanHref(s.id) });
      } else if (s.status === 'failed') {
        out.push({ tab: 'scans', at, icon: 'alert', tone: 'critical', title: 'Scan failed',
          detail: `${s.target}${s.error ? ' · ' + s.error : ''}`,
          cta: 'View error →', href: scanHref(s.id) });
      } else if (s.status === 'cancelled') {
        out.push({ tab: 'scans', at, icon: 'x', tone: 'fp', title: 'Scan cancelled',
          detail: s.target, cta: 'View →', href: scanHref(s.id) });
      }
    });
    // Skip low/info findings so a chatty scanner doesn't bury scan/change events.
    (d.findings || []).forEach(f => {
      const at = new Date(f.first_seen).getTime();
      if (!at || at < cutoff) return;
      if (f.severity !== 'critical' && f.severity !== 'high' && f.severity !== 'medium') return;
      const cta = (f.severity === 'critical' || f.severity === 'high') ? 'Triage →' : 'View →';
      out.push({ tab: 'findings', at, icon: 'alert', tone: f.severity,
        title: `New ${f.severity} finding`,
        detail: `${f.title || f.template_name || 'Untitled'}${f.asset_value ? ' · ' + f.asset_value : ''}`,
        cta, href: `#/findings/${encodeURIComponent(f.id)}` });
    });
    const last = d.overview.last_scan, ch = d.overview.changes_since_last;
    if (last && last.finished_at && ch) {
      const at = new Date(last.finished_at).getTime();
      if (at >= cutoff) {
        const dis = ch.disappeared_assets || 0, add = ch.new_assets || 0;
        if (dis > 0) out.push({ tab: 'changes', at, icon: 'alert', tone: 'critical',
          title: `${dis} asset${dis === 1 ? '' : 's'} disappeared`,
          detail: `${last.target} · review for offboarding`, cta: 'View diff →', href: '#/assets' });
        if (add > 0) out.push({ tab: 'changes', at: at - 1, icon: 'plus', tone: 'medium',
          title: `${add} new asset${add === 1 ? '' : 's'}`,
          detail: `${last.target} · auto-added to scope`, cta: 'View →', href: '#/assets' });
      }
    }
    out.sort((a, b) => b.at - a.at);
    return out.slice(0, 20);
  },

  _activityRow(ev) {
    const tone = TONE_STYLE[ev.tone] || TONE_STYLE.info;
    return `<li class="activity-item" data-href="${escapeHtml(ev.href)}">
      <span class="activity-icon" style="${tone}">${Components.icon(ev.icon, 14)}</span>
      <div class="activity-body"><span class="activity-title">${escapeHtml(ev.title)}</span><span class="activity-detail">${escapeHtml(ev.detail)}</span></div>
      <div class="activity-meta"><span class="activity-when">${Components.timeAgo(new Date(ev.at).toISOString())}</span><a class="activity-cta" href="${escapeHtml(ev.href)}">${escapeHtml(ev.cta)}</a></div>
    </li>`;
  },

  _topTargetsPanel(d) {
    const targets = (d.targets || []).slice().sort((a, b) => (b.finding_count || 0) - (a.finding_count || 0)).slice(0, 4);
    const body = targets.length === 0
      ? `<div class="dash-empty">No targets yet.<a class="btn btn-accent btn-sm" href="#/targets">Add target</a></div>`
      : `<ul class="dash-list">${targets.map(t => `<li class="dash-list-item" onclick="location.hash='#/targets/${encodeURIComponent(t.id)}'"><span class="dash-list-name">${escapeHtml(t.value)}</span><span class="dash-list-meta">${this._fmtNum(t.asset_count || 0)} assets</span><span class="kpi-mini-pill">${t.finding_count || 0} findings</span></li>`).join('')}</ul>`;
    return `<div class="dash-panel"><div class="dash-panel-header"><h3>Targets</h3><a href="#/targets" class="dash-panel-sub" style="color:var(--brand)">Manage →</a></div><div class="dash-panel-body">${body}</div></div>`;
  },

  _upcomingPanel(d) {
    const byID = {}, tgtByID = {};
    (d.schedules || []).forEach(s => { byID[s.id] = s; });
    (d.targets || []).forEach(t => { tgtByID[t.id] = t; });
    const tgtName = id => (tgtByID[id] && tgtByID[id].value) || id;
    const items = (d.upcoming || []).slice(0, 3).map(u => ({
      id: u.schedule_id, targetValue: tgtName(u.target_id),
      scheduleName: (byID[u.schedule_id] && byID[u.schedule_id].name) || 'Schedule',
      firesAt: u.fires_at, paused: false,
    })).concat((d.schedules || []).filter(s => !s.enabled).slice(0, 2).map(s => ({
      id: s.id, targetValue: tgtName(s.target_id),
      scheduleName: s.name, firesAt: null, paused: true,
    }))).slice(0, 3);
    const body = items.length === 0
      ? `<div class="dash-empty">No scheduled scans.<a class="btn btn-accent btn-sm" href="#/schedules">Add schedule</a></div>`
      : `<ul class="dash-list">${items.map(it => {
          const dotCls = it.paused ? 'dash-status-dot-paused' : 'dash-status-dot-active';
          const when = it.paused ? 'Paused' : Components.nextRun(it.firesAt);
          const action = it.paused ? `<button type="button" class="btn btn-ghost btn-sm" data-resume-id="${escapeHtml(it.id)}">Resume</button>` : '';
          return `<li class="dash-list-item"><span class="dash-status-dot ${dotCls}"></span>
            <div style="flex:1;min-width:0">
              <div class="mono" style="font-size:12px">${escapeHtml(it.targetValue)} · ${escapeHtml(it.scheduleName)}</div>
              <div class="dash-list-meta">${when}</div>
            </div>${action}</li>`;
        }).join('')}</ul>`;
    return `<div class="dash-panel">
      <div class="dash-panel-header"><h3>Upcoming scans</h3><a href="#/schedules" class="dash-panel-sub" style="color:var(--brand)">Schedules →</a></div>
      <div class="dash-panel-body">${body}</div></div>`;
  },

  _bind(app) {
    const refresh = app.querySelector('#dash-refresh-btn');
    if (refresh) refresh.addEventListener('click', () => this.render(app));
    const runBtn = app.querySelector('#dash-runscan-btn');
    if (runBtn) runBtn.addEventListener('click', () => {
      if (typeof AdHocPage !== 'undefined' && AdHocPage.open) AdHocPage.open();
    });
    app.querySelectorAll('[data-activity-tab]').forEach(btn => {
      btn.addEventListener('click', () => {
        this._filterTab = btn.getAttribute('data-activity-tab');
        const body = app.querySelector('#dash-activity-body');
        if (body) body.innerHTML = this._activityList(this._state);
        app.querySelectorAll('[data-activity-tab]').forEach(b =>
          b.classList.toggle('active', b.getAttribute('data-activity-tab') === this._filterTab));
      });
    });
    // Whole row navigates. The CTA <a> bubbles to the same handler so
    // either click hits the same hash; no stopPropagation needed except
    // for the Resume button which must NOT navigate.
    app.querySelectorAll('.activity-item[data-href]').forEach(li => {
      li.addEventListener('click', () => { location.hash = li.getAttribute('data-href'); });
    });
    app.querySelectorAll('[data-resume-id]').forEach(btn => {
      btn.addEventListener('click', async (e) => {
        e.stopPropagation();
        btn.disabled = true;
        try { await API.resumeSchedule(btn.getAttribute('data-resume-id')); this.render(app); }
        catch (err) { btn.disabled = false; alert('Resume failed: ' + err.message); }
      });
    });
  },

  _startSubtitleTimer() {
    const update = () => {
      const el = document.getElementById('dash-subtitle');
      if (!el) { clearInterval(this._subtitleTimer); this._subtitleTimer = null; return; }
      el.textContent = 'updated ' + Components.timeAgo(this._state.fetchedAt.toISOString());
    };
    update();
    if (this._subtitleTimer) clearInterval(this._subtitleTimer);
    this._subtitleTimer = setInterval(update, 30000);
  },

  _fmtNum(n) { return typeof Intl !== 'undefined' ? new Intl.NumberFormat().format(n) : String(n); },
};

// sumSeverityMap collapses a {critical, high, medium, low, info} count
// map (model.ScanDelta.NewFindings shape) into a single int.
function sumSeverityMap(m) {
  if (!m) return 0;
  return SEV_ORDER.reduce((s, k) => s + (m[k] || 0), 0);
}

// Sidebar findings badge — kept on the dashboard owner because it's the
// page that always loads /overview. Hidden when crit+high = 0.
function updateSidebarBadge(sevCounts) {
  if (!sevCounts) return;
  const total = (sevCounts.critical || 0) + (sevCounts.high || 0);
  const el = document.getElementById('findings-badge');
  if (!el) return;
  if (total > 0) { el.textContent = total; el.style.display = ''; }
  else { el.style.display = 'none'; }
}
