// API client for /api/v1/*
const API = {
  // Token is injected by the Go server into <head> as
  //   <meta name="surfbot-token" content="...">
  // and read once at load. requireToken on the server gates every /api/*
  // request, so the SPA cannot talk to the backend without it.
  _token: (document.querySelector('meta[name="surfbot-token"]') || {}).content || '',

  _headers(extra) {
    const h = Object.assign({}, extra || {});
    if (this._token) h['Authorization'] = 'Bearer ' + this._token;
    return h;
  },

  async get(path) {
    const resp = await fetch('/api/v1' + path, { headers: this._headers() });
    if (!resp.ok) {
      const err = await resp.json().catch(() => ({ error: resp.statusText }));
      // SPEC-SCHED1.3a uses RFC 7807 problem+json (title/detail/type/
      // field_errors); legacy handlers still return {error:"..."}. The
      // thrown Error carries both shapes so callers can render either.
      const e = new Error(err.error || err.title || err.detail || 'Request failed');
      e.status = resp.status;
      e.problem = err;
      throw e;
    }
    return resp.json();
  },

  async post(path, body) {
    const resp = await fetch('/api/v1' + path, {
      method: 'POST',
      headers: this._headers({ 'Content-Type': 'application/json' }),
      body: JSON.stringify(body),
    });
    const data = await resp.json().catch(() => ({ error: resp.statusText }));
    if (!resp.ok) throw new Error(data.error || 'Request failed');
    return data;
  },

  async patch(path, body) {
    const resp = await fetch('/api/v1' + path, {
      method: 'PATCH',
      headers: this._headers({ 'Content-Type': 'application/json' }),
      body: JSON.stringify(body),
    });
    const data = await resp.json().catch(() => ({ error: resp.statusText }));
    if (!resp.ok) throw new Error(data.error || 'Request failed');
    return data;
  },

  async put(path, body) {
    const resp = await fetch('/api/v1' + path, {
      method: 'PUT',
      headers: this._headers({ 'Content-Type': 'application/json' }),
      body: JSON.stringify(body),
    });
    const data = await resp.json().catch(() => ({ error: resp.statusText }));
    if (!resp.ok) throw new Error(data.error || data.errors ? JSON.stringify(data.errors) : 'Request failed');
    return data;
  },

  async del(path) {
    const resp = await fetch('/api/v1' + path, { method: 'DELETE', headers: this._headers() });
    const data = await resp.json().catch(() => ({ error: resp.statusText }));
    if (!resp.ok) throw new Error(data.error || 'Request failed');
    return data;
  },

  // Read endpoints
  overview()            { return this.get('/overview'); },
  findings(params)      { return this.get('/findings' + toQuery(params)); },
  findingsGrouped(params) { return this.get('/findings/grouped' + toQuery(params)); },
  finding(id)           { return this.get('/findings/' + id); },
  assets(params)        { return this.get('/assets' + toQuery(params)); },
  assetTree(targetId)   { return this.get('/assets/tree' + toQuery({ target_id: targetId })); },
  scans(params)         { return this.get('/scans' + toQuery(params)); },
  scan(id)              { return this.get('/scans/' + id); },
  targets()             { return this.get('/targets'); },
  target(id)            { return this.get('/targets/' + id); },
  tools()               { return this.get('/tools'); },
  availableTools()      { return this.get('/tools/available'); },
  scanStatus()          { return this.get('/scans/status'); },
  schedule()            { return this.get('/schedule'); },
  updateSchedule(cfg)   { return this.put('/schedule', cfg); },

  // SPEC-SCHED1.3a read-only endpoints. 1.4b adds write methods on top.
  listSchedules(params) { return this.get('/schedules' + toQuery(params)); },
  getSchedule(id)       { return this.get('/schedules/' + encodeURIComponent(id)); },
  upcomingSchedules(params) { return this.get('/schedules/upcoming' + toQuery(params)); },
  listTemplates(params) { return this.get('/templates' + toQuery(params)); },
  getTemplate(id)       { return this.get('/templates/' + encodeURIComponent(id)); },
  listBlackouts(params) { return this.get('/blackouts' + toQuery(params)); },
  getBlackout(id)       { return this.get('/blackouts/' + encodeURIComponent(id)); },
  getScheduleDefaults() { return this.get('/schedule-defaults'); },

  // Write endpoints
  createTarget(value, type, scope) {
    return this.post('/targets', { value, type: type || '', scope: scope || 'external' });
  },
  deleteTarget(id)      { return this.del('/targets/' + id); },
  cancelScan(id)        { return this.del('/scans/' + id); },
  updateFindingStatus(id, status) {
    return this.patch('/findings/' + id + '/status', { status });
  },
  startScan(targetId, type, opts) {
    const body = { target_id: targetId, type: type || 'full' };
    if (opts) {
      if (opts.tools && opts.tools.length > 0) body.tools = opts.tools;
      if (opts.rate_limit > 0) body.rate_limit = opts.rate_limit;
      if (opts.timeout > 0) body.timeout = opts.timeout;
    }
    return this.post('/scans', body);
  },

  // SPEC-X3.1 — daemon endpoints live outside /api/v1/. The former
  // daemonTrigger() is gone with SPEC-SCHED1.4a; target-anchored ad-hoc
  // dispatch via /api/v1/scans/ad-hoc lands in the write-flow UI (1.4b).
  daemonStatus() {
    return fetch('/api/daemon/status', { headers: this._headers() }).then(r => {
      if (!r.ok) throw new Error('daemon status failed: ' + r.status);
      return r.json();
    });
  },
};

function toQuery(params) {
  if (!params) return '';
  const entries = Object.entries(params).filter(([, v]) => v != null && v !== '');
  if (entries.length === 0) return '';
  return '?' + entries.map(([k, v]) => encodeURIComponent(k) + '=' + encodeURIComponent(v)).join('&');
}
