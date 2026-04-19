// SPEC-SCHED1.4c R2: Timeline page.
//
// Day-grouped vertical list of upcoming firings from
// /api/v1/schedules/upcoming. No 2D calendar grid — vanilla-JS tractable,
// no date library. Horizon + target filter adjust the fetch. Days in
// the horizon with zero firings get a clickable empty slot that opens
// the 1.4b schedule create modal with dtstart pre-filled (OQ2: local
// 09:00 default).

const TimelinePage = {
  _horizon: '24h',
  _targetID: '',

  async render(app, params) {
    // Query params override in-memory state on route hit so deep links
    // survive a refresh.
    const f = this.parseParams(params || {});
    this._horizon = f.horizon;
    this._targetID = f.target_id;

    app.innerHTML = '<div class="loading">Loading timeline...</div>';
    try {
      const apiParams = { horizon: f.horizon, limit: 500 };
      if (f.target_id) apiParams.target_id = f.target_id;
      const data = await API.upcomingSchedules(apiParams);
      app.innerHTML = this.template(data, f);
      this.bindHeader(app);
      this.bindBody(app, data, f);
    } catch (err) {
      app.innerHTML = `
        <div class="page-header"><h2>Timeline</h2></div>
        ${Components.errorBanner(err)}
      `;
    }
  },

  parseParams(params) {
    return {
      horizon: params.horizon || this._horizon || '24h',
      target_id: params.target_id || this._targetID || '',
    };
  },

  template(data, f) {
    const items = (data && data.items) || [];
    const blackouts = (data && data.blackouts_in_horizon) || [];
    const totalFirings = items.length;

    const body = items.length === 0
      ? Components.emptyState(
          'No upcoming firings',
          `No schedules are going to fire in the next ${escapeHtml(f.horizon)}. Check <a href="#/schedules">/schedules</a> to make sure at least one schedule is active.`,
        )
      : '<div id="timeline-days"></div>';

    const bkHtml = blackouts.length === 0
      ? ''
      : `<div class="card" style="margin-top:16px">
           <div class="card-label">Blackouts in horizon <span class="text-muted" style="font-weight:400;text-transform:none">(${blackouts.length})</span></div>
           <ul style="margin-top:8px;font-size:13px">
             ${blackouts.map(b => `<li><span class="mono">${Components.truncateID(b.blackout_id)}</span> — ${escapeHtml(new Date(b.starts_at).toLocaleString())} → ${escapeHtml(new Date(b.ends_at).toLocaleString())}</li>`).join('')}
           </ul>
         </div>`;

    return `
      <div class="page-header">
        <h2>Timeline</h2>
        <p>${totalFirings} upcoming firing${totalFirings === 1 ? '' : 's'} in the next ${escapeHtml(f.horizon)}</p>
      </div>
      <div class="inline-form" style="margin-bottom:16px;gap:12px;align-items:center">
        <label class="form-label" style="margin:0">Horizon</label>
        <span id="timeline-horizon-host"></span>
        <label class="form-label" style="margin:0">Target</label>
        <span id="timeline-target-host"></span>
      </div>
      ${body}
      ${bkHtml}
    `;
  },

  bindHeader(app) {
    const hHost = document.getElementById('timeline-horizon-host');
    if (hHost) {
      const sel = Components.horizonSelector({
        current: this._horizon,
        onChange: (v) => {
          this._horizon = v;
          location.hash = this.hashForFilters();
        },
      });
      hHost.appendChild(sel);
    }
    const tHost = document.getElementById('timeline-target-host');
    if (tHost) {
      const sel = Components.targetFilterSelector({
        targets: [],
        current: this._targetID,
        onChange: (v) => {
          this._targetID = v;
          location.hash = this.hashForFilters();
        },
      });
      tHost.appendChild(sel);
    }
  },

  hashForFilters() {
    const parts = [];
    if (this._horizon && this._horizon !== '24h') parts.push('horizon=' + encodeURIComponent(this._horizon));
    if (this._targetID) parts.push('target_id=' + encodeURIComponent(this._targetID));
    return '#/timeline' + (parts.length ? '?' + parts.join('&') : '');
  },

  bindBody(app, data, f) {
    const host = document.getElementById('timeline-days');
    if (!host) return;

    const grouped = Components.groupByDay((data && data.items) || []);
    const horizonDays = expandHorizonDays(f.horizon, grouped);

    horizonDays.forEach(({ key, label }) => {
      const section = document.createElement('div');
      section.className = 'timeline-day';
      const header = document.createElement('div');
      header.className = 'timeline-day-header';
      header.innerHTML = `<span>${escapeHtml(label)}</span><span class="text-muted" style="font-size:11px">${escapeHtml(key)}</span>`;
      section.appendChild(header);

      const body = document.createElement('div');
      body.className = 'timeline-day-items';
      const group = grouped.find(g => g.date === key);
      if (group && group.items.length) {
        group.items.forEach(fi => {
          body.appendChild(Components.timelineRow({
            firing: fi,
            onClick: (ff) => { location.hash = '#/schedules/' + encodeURIComponent(ff.schedule_id); },
          }));
        });
      } else {
        body.appendChild(Components.timelineEmptySlot({
          date: key,
          onClickCreate: (dtstartISO) => {
            if (typeof SchedulesPage !== 'undefined' && SchedulesPage.openForm) {
              SchedulesPage.openForm({
                mode: 'create',
                schedule: { dtstart: dtstartISO, target_id: f.target_id || '' },
                onSaved: () => TimelinePage.render(app, {}),
              });
            }
          },
        }));
      }
      section.appendChild(body);
      host.appendChild(section);
    });
  },
};

// expandHorizonDays returns the keyed day list to render. We start at
// "today" and add days out to the horizon so the viewer sees empty days
// (slot-creation targets) even when no firings fall on them. Horizon
// strings we recognise: Nh (hours) / Nd (days). Unknown format → just
// render the days present in `grouped`.
function expandHorizonDays(horizon, grouped) {
  const m = /^(\d+)([hd])$/.exec(horizon || '24h');
  if (!m) return grouped.map(g => ({ key: g.date, label: g.dayLabel }));
  const n = parseInt(m[1], 10);
  const hours = m[2] === 'd' ? n * 24 : n;
  const days = Math.max(1, Math.ceil(hours / 24));
  const out = [];
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  for (let i = 0; i < days; i++) {
    const d = new Date(today);
    d.setDate(d.getDate() + i);
    const key = d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0');
    out.push({
      key,
      label: i === 0 ? 'Today' : (i === 1 ? 'Tomorrow' : d.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' })),
    });
  }
  return out;
}
