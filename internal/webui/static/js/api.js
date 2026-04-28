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
    if (!resp.ok) throw await makeAPIError(resp);
    return resp.json();
  },

  async post(path, body) {
    const resp = await fetch('/api/v1' + path, {
      method: 'POST',
      headers: this._headers({ 'Content-Type': 'application/json' }),
      body: body == null ? undefined : JSON.stringify(body),
    });
    if (!resp.ok) throw await makeAPIError(resp);
    if (resp.status === 204) return null;
    return resp.json().catch(() => null);
  },

  async patch(path, body) {
    const resp = await fetch('/api/v1' + path, {
      method: 'PATCH',
      headers: this._headers({ 'Content-Type': 'application/json' }),
      body: JSON.stringify(body),
    });
    if (!resp.ok) throw await makeAPIError(resp);
    if (resp.status === 204) return null;
    return resp.json().catch(() => null);
  },

  async put(path, body) {
    const resp = await fetch('/api/v1' + path, {
      method: 'PUT',
      headers: this._headers({ 'Content-Type': 'application/json' }),
      body: JSON.stringify(body),
    });
    if (!resp.ok) throw await makeAPIError(resp);
    if (resp.status === 204) return null;
    return resp.json().catch(() => null);
  },

  async del(path) {
    const resp = await fetch('/api/v1' + path, { method: 'DELETE', headers: this._headers() });
    if (!resp.ok) throw await makeAPIError(resp);
    if (resp.status === 204) return null;
    return resp.json().catch(() => null);
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
  // SPEC-SCHED1.3a read-only endpoints. 1.4b adds write methods on top.
  listSchedules(params) { return this.get('/schedules' + toQuery(params)); },
  getSchedule(id)       { return this.get('/schedules/' + encodeURIComponent(id)); },
  upcomingSchedules(params) { return this.get('/schedules/upcoming' + toQuery(params)); },
  listTemplates(params) { return this.get('/templates' + toQuery(params)); },
  getTemplate(id)       { return this.get('/templates/' + encodeURIComponent(id)); },
  listBlackouts(params) { return this.get('/blackouts' + toQuery(params)); },
  getBlackout(id)       { return this.get('/blackouts/' + encodeURIComponent(id)); },
  getScheduleDefaults() { return this.get('/schedule-defaults'); },

  // --- SPEC-SCHED1.4b write methods ---
  // Every method throws an APIError on 4xx/5xx (see makeAPIError below).
  // Callers read .status / .code / .fieldErrors / .problem and render
  // inline via Components.applyFieldErrors, or as a banner via
  // Components.errorBanner. The .code field is set for Problem types the
  // ad-hoc dispatcher modal wants to react to specifically.

  createSchedule(body)      { return this.post('/schedules', body); },
  updateSchedule(id, body)  { return this.put('/schedules/' + encodeURIComponent(id), body); },
  deleteSchedule(id)        { return this.del('/schedules/' + encodeURIComponent(id)); },
  pauseSchedule(id)         { return this.post('/schedules/' + encodeURIComponent(id) + '/pause'); },
  resumeSchedule(id)        { return this.post('/schedules/' + encodeURIComponent(id) + '/resume'); },
  bulkSchedules(body)       { return this.post('/schedules/bulk', body); },

  createTemplate(body)      { return this.post('/templates', body); },
  updateTemplate(id, body)  { return this.put('/templates/' + encodeURIComponent(id), body); },
  deleteTemplate(id, opts)  {
    const q = opts && opts.force ? '?force=true' : '';
    return this.del('/templates/' + encodeURIComponent(id) + q);
  },

  createBlackout(body)      { return this.post('/blackouts', body); },
  updateBlackout(id, body)  { return this.put('/blackouts/' + encodeURIComponent(id), body); },
  deleteBlackout(id)        { return this.del('/blackouts/' + encodeURIComponent(id)); },

  updateDefaults(body)      { return this.put('/schedule-defaults', body); },

  createAdHocScan(body)     { return this.post('/scans/ad-hoc', body); },

  // Legacy write endpoints (non-schedule resources).
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

// makeAPIError parses a failed Response into a rich Error object. The
// 1.3a API returns RFC 7807 application/problem+json with title/detail/
// type/field_errors; 1.4a legacy handlers and non-v1 routes may still
// return {error:"..."}. The thrown object has:
//   .message       — best human-readable line
//   .status        — HTTP status
//   .problem       — the parsed body (either shape)
//   .fieldErrors   — alias for problem.field_errors (array of {field,message})
//   .code          — symbolic code derived from problem.type for the
//                    handful of dispatcher/bulk scenarios the UI cares
//                    about (DISPATCHER_UNREACHABLE / TARGET_BUSY /
//                    IN_BLACKOUT / VALIDATION / NOT_FOUND / CONFLICT)
async function makeAPIError(resp) {
  const body = await resp.json().catch(() => ({ error: resp.statusText }));
  const e = new Error(body.title || body.error || body.detail || ('HTTP ' + resp.status));
  e.status = resp.status;
  e.problem = body;
  e.fieldErrors = body.field_errors || [];
  e.code = codeFromProblem(body, resp.status);
  return e;
}

function codeFromProblem(body, status) {
  const type = (body && body.type) || '';
  if (type === '/problems/dispatcher-unreachable' || status === 503) return 'DISPATCHER_UNREACHABLE';
  if (type === '/problems/target-busy') return 'TARGET_BUSY';
  if (type === '/problems/in-blackout') return 'IN_BLACKOUT';
  if (type === '/problems/template-in-use') return 'TEMPLATE_IN_USE';
  if (type === '/problems/overlap') return 'OVERLAP';
  if (type === '/problems/validation' || status === 422) return 'VALIDATION';
  if (type === '/problems/not-found' || status === 404) return 'NOT_FOUND';
  if (type === '/problems/already-exists' || status === 409) return 'CONFLICT';
  return '';
}
