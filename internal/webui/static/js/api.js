// API client for /api/v1/*
const API = {
  async get(path) {
    const resp = await fetch('/api/v1' + path);
    if (!resp.ok) {
      const err = await resp.json().catch(() => ({ error: resp.statusText }));
      throw new Error(err.error || 'Request failed');
    }
    return resp.json();
  },

  async post(path, body) {
    const resp = await fetch('/api/v1' + path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    const data = await resp.json().catch(() => ({ error: resp.statusText }));
    if (!resp.ok) throw new Error(data.error || 'Request failed');
    return data;
  },

  async patch(path, body) {
    const resp = await fetch('/api/v1' + path, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    const data = await resp.json().catch(() => ({ error: resp.statusText }));
    if (!resp.ok) throw new Error(data.error || 'Request failed');
    return data;
  },

  async del(path) {
    const resp = await fetch('/api/v1' + path, { method: 'DELETE' });
    const data = await resp.json().catch(() => ({ error: resp.statusText }));
    if (!resp.ok) throw new Error(data.error || 'Request failed');
    return data;
  },

  // Read endpoints
  overview()            { return this.get('/overview'); },
  findings(params)      { return this.get('/findings' + toQuery(params)); },
  finding(id)           { return this.get('/findings/' + id); },
  assets(params)        { return this.get('/assets' + toQuery(params)); },
  assetTree(targetId)   { return this.get('/assets/tree' + toQuery({ target_id: targetId })); },
  scans(params)         { return this.get('/scans' + toQuery(params)); },
  scan(id)              { return this.get('/scans/' + id); },
  targets()             { return this.get('/targets'); },
  tools()               { return this.get('/tools'); },
  availableTools()      { return this.get('/tools/available'); },
  scanStatus()          { return this.get('/scans/status'); },

  // Write endpoints
  createTarget(value, type, scope) {
    return this.post('/targets', { value, type: type || '', scope: scope || 'external' });
  },
  deleteTarget(id)      { return this.del('/targets/' + id); },
  updateFindingStatus(id, status) {
    return this.patch('/findings/' + id + '/status', { status });
  },
  startScan(targetId, type) {
    return this.post('/scans', { target_id: targetId, type: type || 'full' });
  },

  // SPEC-X3.1 — daemon endpoints live outside /api/v1/.
  daemonStatus() {
    return fetch('/api/daemon/status').then(r => {
      if (!r.ok) throw new Error('daemon status failed: ' + r.status);
      return r.json();
    });
  },
  daemonTrigger(profile) {
    return fetch('/api/daemon/trigger', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ profile: profile || 'full' }),
    }).then(async r => {
      const data = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(data.error || ('trigger failed: ' + r.status));
      return data;
    });
  },
};

function toQuery(params) {
  if (!params) return '';
  const entries = Object.entries(params).filter(([, v]) => v != null && v !== '');
  if (entries.length === 0) return '';
  return '?' + entries.map(([k, v]) => encodeURIComponent(k) + '=' + encodeURIComponent(v)).join('&');
}
