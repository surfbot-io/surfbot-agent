// Shared UI components

const Components = {
  severityBadge(severity) {
    return `<span class="badge badge-${severity}">${severity}</span>`;
  },

  statusBadge(status) {
    return `<span class="badge-status badge-${status}">${status}</span>`;
  },

  timeAgo(dateStr) {
    if (!dateStr) return 'never';
    const d = new Date(dateStr);
    const now = new Date();
    const secs = Math.floor((now - d) / 1000);
    if (secs < 60) return secs + 's ago';
    const mins = Math.floor(secs / 60);
    if (mins < 60) return mins + 'm ago';
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) return hrs + 'h ago';
    const days = Math.floor(hrs / 24);
    return days + 'd ago';
  },

  formatDate(dateStr) {
    if (!dateStr) return '-';
    return new Date(dateStr).toLocaleString();
  },

  formatDuration(seconds) {
    if (!seconds || seconds <= 0) return '-';
    if (seconds < 60) return seconds + 's';
    const m = Math.floor(seconds / 60);
    const s = seconds % 60;
    return m + 'm ' + s + 's';
  },

  formatBytes(bytes) {
    if (!bytes) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB'];
    let i = 0;
    let size = bytes;
    while (size >= 1024 && i < units.length - 1) { size /= 1024; i++; }
    return size.toFixed(i === 0 ? 0 : 1) + ' ' + units[i];
  },

  truncate(str, len) {
    if (!str) return '';
    return str.length > len ? str.slice(0, len) + '...' : str;
  },

  truncateID(id) {
    if (!id) return '';
    return id.slice(0, 8);
  },

  sevColor(severity) {
    const colors = {
      critical: 'var(--sev-critical)',
      high: 'var(--sev-high)',
      medium: 'var(--sev-medium)',
      low: 'var(--sev-low)',
      info: 'var(--sev-info)',
    };
    return colors[severity] || 'var(--text-secondary)';
  },

  scoreClass(score) {
    if (score >= 80) return 'score-good';
    if (score >= 60) return 'score-warn';
    if (score >= 40) return 'score-bad';
    return 'score-critical';
  },

  severityBars(counts) {
    const severities = ['critical', 'high', 'medium', 'low', 'info'];
    const visible = severities.filter(s => (counts[s] || 0) > 0);
    if (visible.length === 0) return '';
    const max = Math.max(1, ...visible.map(s => counts[s] || 0));
    return `<div class="sev-bars">${visible.map(sev => {
      const count = counts[sev] || 0;
      const pct = (count / max) * 100;
      return `<div class="sev-bar-row">
        <span class="sev-bar-label">${sev}</span>
        <div class="sev-bar-track">
          <div class="sev-bar-fill" style="width:${pct}%;background:${Components.sevColor(sev)}"></div>
        </div>
        <span class="sev-bar-count">${count}</span>
      </div>`;
    }).join('')}</div>`;
  },

  table(headers, rows, opts = {}) {
    // scope="col" lets screen readers announce the column header for
    // every cell underneath, which is the WCAG-recommended marker.
    const ths = headers.map(h => `<th scope="col">${h}</th>`).join('');
    const trs = rows.length === 0
      ? `<tr><td colspan="${headers.length}" class="empty-state">No data</td></tr>`
      : rows.join('');
    return `<div class="table-container"><table><thead><tr>${ths}</tr></thead><tbody>${trs}</tbody></table></div>`;
  },

  jsonViewer(obj) {
    const json = typeof obj === 'string' ? obj : JSON.stringify(obj, null, 2);
    return `<div class="json-viewer">${escapeHtml(json)}</div>`;
  },

  treeNode(node) {
    const hasChildren = node.children && node.children.length > 0;
    const toggle = hasChildren ? '&#9654;' : '&nbsp;';
    const findingBadge = node.finding_count > 0
      ? `<span class="badge badge-high" style="font-size:10px">${node.finding_count}</span>`
      : '';

    let html = `<div class="tree-node">
      <div class="tree-node-header" onclick="toggleTreeNode(this)">
        <span class="tree-toggle">${toggle}</span>
        <span class="tree-type">${node.type}</span>
        <span class="tree-value">${escapeHtml(node.value)}</span>
        <span class="tree-badge">${findingBadge}</span>
      </div>`;

    if (hasChildren) {
      html += `<div class="tree-children">`;
      for (const child of node.children) {
        html += Components.treeNode(child);
      }
      html += `</div>`;
    }

    html += `</div>`;
    return html;
  },

  emptyState(title, message) {
    return `<div class="empty-state"><h3>${title}</h3><p>${message}</p></div>`;
  },

  backLink(hash, label) {
    return `<a href="${hash}" class="back-link">&larr; ${label}</a>`;
  },

  // SPEC-SCHED1.4a helpers. The 1.3a API returns RFC 7807 problems; the
  // api.js fetch wrapper attaches the parsed problem to thrown errors as
  // err.problem. errorBanner renders what it has: status + title + detail
  // when the error came from the API, err.message otherwise.
  errorBanner(err) {
    if (!err) return '';
    const problem = err.problem || {};
    const parts = [];
    const title = problem.title || err.message || 'Request failed';
    if (err.status) parts.push(`<span class="mono">${err.status}</span>`);
    parts.push(escapeHtml(title));
    let body = `<strong>${parts.join(' ')}</strong>`;
    if (problem.detail) body += `<div class="text-muted">${escapeHtml(problem.detail)}</div>`;
    if (problem.field_errors && problem.field_errors.length) {
      body += '<ul>' + problem.field_errors.map(f =>
        `<li><span class="mono">${escapeHtml(f.field)}</span>: ${escapeHtml(f.message)}</li>`
      ).join('') + '</ul>';
    }
    return `<div class="error-banner">${body}</div>`;
  },

  // rruleCompact keeps the first semicolon-separated component (FREQ=...)
  // or the first 40 chars, whichever is shorter. Full RRULE goes into the
  // title attribute for hover.
  rruleCompact(rrule) {
    if (!rrule) return '-';
    const safe = escapeHtml(rrule);
    const first = rrule.split(';')[0];
    let short = first.length < 40 ? first : rrule.slice(0, 40);
    if (short.length < rrule.length) short += '…';
    return `<span class="mono" title="${safe}">${escapeHtml(short)}</span>`;
  },

  // nextRun formats a *future* time as "in 3h 42m" and a past time as
  // "42m ago" — timeAgo handles the past branch; this helper adds the
  // "in X" idiom for future timestamps.
  nextRun(dateStr) {
    if (!dateStr) return '<span class="text-muted">—</span>';
    const d = new Date(dateStr);
    if (isNaN(d.getTime())) return '<span class="text-muted">—</span>';
    const now = new Date();
    const diff = Math.floor((d - now) / 1000);
    if (diff <= 0) return Components.timeAgo(dateStr);
    if (diff < 60) return 'in ' + diff + 's';
    const mins = Math.floor(diff / 60);
    if (mins < 60) return 'in ' + mins + 'm';
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) return 'in ' + hrs + 'h ' + (mins % 60) + 'm';
    const days = Math.floor(hrs / 24);
    return 'in ' + days + 'd ' + (hrs % 24) + 'h';
  },

  scheduleStatusBadge(status) {
    const cls = status === 'active' ? 'badge-info' : 'badge-muted';
    return `<span class="badge ${cls}">${escapeHtml(status || '-')}</span>`;
  },

  // --- SPEC-SCHED1.4b: form, modal, confirm, bulk helpers ---
  //
  // Field renderers return HTML strings to match the existing Components
  // convention. modal() and confirmDialog() own DOM because they mount
  // outside the #app container and need lifecycle control.
  //
  // Form fields carry a dedicated .field-error sibling selected by name=
  // so applyFieldErrors can populate per-field messages from a 1.3a
  // ProblemResponse without the caller threading them through render.

  formInput({ label, name, type, value, error, required, placeholder, help }) {
    const reqMark = required ? ' <span class="form-required">*</span>' : '';
    const v = value == null ? '' : String(value);
    const helpHtml = help ? `<div class="form-help">${escapeHtml(help)}</div>` : '';
    return `<div class="form-field" data-field="${escapeHtml(name)}">
      <label class="form-label" for="f-${escapeHtml(name)}">${escapeHtml(label)}${reqMark}</label>
      <input class="form-input" id="f-${escapeHtml(name)}" name="${escapeHtml(name)}"
        type="${escapeHtml(type || 'text')}"
        ${required ? 'required' : ''}
        ${placeholder ? `placeholder="${escapeHtml(placeholder)}"` : ''}
        value="${escapeHtml(v)}">
      ${helpHtml}
      <div class="field-error" data-field-error="${escapeHtml(name)}" style="display:${error ? 'block' : 'none'}">${escapeHtml(error || '')}</div>
    </div>`;
  },

  formSelect({ label, name, options, value, error, required }) {
    const reqMark = required ? ' <span class="form-required">*</span>' : '';
    const opts = (options || []).map(o => {
      const val = typeof o === 'string' ? o : o.value;
      const lab = typeof o === 'string' ? o : (o.label || o.value);
      const sel = String(val) === String(value || '') ? ' selected' : '';
      return `<option value="${escapeHtml(val)}"${sel}>${escapeHtml(lab)}</option>`;
    }).join('');
    return `<div class="form-field" data-field="${escapeHtml(name)}">
      <label class="form-label" for="f-${escapeHtml(name)}">${escapeHtml(label)}${reqMark}</label>
      <select class="filter-select" id="f-${escapeHtml(name)}" name="${escapeHtml(name)}" ${required ? 'required' : ''}>
        ${opts}
      </select>
      <div class="field-error" data-field-error="${escapeHtml(name)}" style="display:${error ? 'block' : 'none'}">${escapeHtml(error || '')}</div>
    </div>`;
  },

  formTextarea({ label, name, value, rows, error, monospace, help, placeholder }) {
    const cls = 'form-input' + (monospace ? ' form-input-mono' : '');
    const helpHtml = help ? `<div class="form-help">${escapeHtml(help)}</div>` : '';
    return `<div class="form-field" data-field="${escapeHtml(name)}">
      <label class="form-label" for="f-${escapeHtml(name)}">${escapeHtml(label)}</label>
      <textarea class="${cls}" id="f-${escapeHtml(name)}" name="${escapeHtml(name)}"
        rows="${rows || 4}"
        ${placeholder ? `placeholder="${escapeHtml(placeholder)}"` : ''}
      >${escapeHtml(value == null ? '' : String(value))}</textarea>
      ${helpHtml}
      <div class="field-error" data-field-error="${escapeHtml(name)}" style="display:${error ? 'block' : 'none'}">${escapeHtml(error || '')}</div>
    </div>`;
  },

  // formDatetime accepts a value in either ISO-8601 (API shape) or the
  // native datetime-local format ("YYYY-MM-DDTHH:MM"). On submit callers
  // convert the field's value to ISO via `new Date(val).toISOString()`.
  formDatetime({ label, name, value, error, required }) {
    const local = toDatetimeLocal(value);
    return Components.formInput({
      label, name,
      type: 'datetime-local',
      value: local,
      error, required,
    });
  },

  // rruleField wraps formInput with a non-blocking client-side syntax
  // check. The server's 422 is the source of truth; the warning just
  // catches obvious typos before the round-trip.
  rruleField({ label, name, value, error }) {
    const html = Components.formInput({
      label, name, type: 'text', value, error,
      required: true,
      placeholder: 'FREQ=DAILY;BYHOUR=2',
    });
    // The inline warning span is revealed by rruleAttachBlurCheck when
    // the form is mounted. Named by convention (rrule-warning-<name>).
    return html + `<div class="rrule-warning" data-rrule-warning-for="${escapeHtml(name)}" style="display:none;font-size:12px;color:var(--sev-medium);margin-top:4px">RRULE syntax looks off — server will validate.</div>`;
  },

  // rruleAttachBlurCheck wires the client-side check after the form is
  // in the DOM. Call once per form in the modal onOpen callback.
  rruleAttachBlurCheck(formEl, fieldName) {
    if (!formEl) return;
    const input = formEl.querySelector(`input[name="${fieldName}"]`);
    const warn = formEl.querySelector(`[data-rrule-warning-for="${fieldName}"]`);
    if (!input || !warn) return;
    input.addEventListener('blur', () => {
      const v = input.value.trim();
      if (!v) { warn.style.display = 'none'; return; }
      const ok = /FREQ=(SECONDLY|MINUTELY|HOURLY|DAILY|WEEKLY|MONTHLY|YEARLY)\b/.test(v);
      warn.style.display = ok ? 'none' : 'block';
    });
  },

  // modal mounts a dialog as a direct child of <body> with a backdrop.
  // Returns { close } so the caller can dismiss programmatically on
  // successful submit. primaryAction / secondaryAction accept
  // { label, onClick, danger }; onClick may return a Promise for a
  // loading spinner. Esc key and backdrop click dismiss the modal and
  // invoke onClose with reason='dismiss'.
  modal({ title, body, primaryAction, secondaryAction, onClose, size }) {
    const root = document.createElement('div');
    root.className = 'modal-backdrop';
    root.tabIndex = -1;
    root.innerHTML = `
      <div class="modal modal-${size || 'md'}" role="dialog" aria-modal="true" aria-label="${escapeHtml(title || '')}">
        <div class="modal-header">
          <h3>${escapeHtml(title || '')}</h3>
          <button type="button" class="modal-close" aria-label="Close">&times;</button>
        </div>
        <div class="modal-body">${body || ''}</div>
        <div class="modal-footer">
          ${secondaryAction ? `<button type="button" class="btn btn-ghost" data-modal-secondary>${escapeHtml(secondaryAction.label)}</button>` : ''}
          ${primaryAction ? `<button type="button" class="btn ${primaryAction.danger ? 'btn-danger' : 'btn-primary'}" data-modal-primary>${escapeHtml(primaryAction.label)}</button>` : ''}
        </div>
      </div>
    `;
    document.body.appendChild(root);
    document.body.classList.add('modal-open');

    let closed = false;
    function close(reason) {
      if (closed) return;
      closed = true;
      document.body.classList.remove('modal-open');
      document.removeEventListener('keydown', onKey);
      root.remove();
      if (typeof onClose === 'function') onClose(reason || 'close');
    }
    function onKey(e) {
      if (e.key === 'Escape') close('dismiss');
    }
    document.addEventListener('keydown', onKey);

    root.addEventListener('click', (e) => {
      if (e.target === root) close('dismiss');
    });
    root.querySelector('.modal-close').addEventListener('click', () => close('dismiss'));

    const secBtn = root.querySelector('[data-modal-secondary]');
    if (secBtn && secondaryAction) {
      secBtn.addEventListener('click', async () => {
        try {
          const r = secondaryAction.onClick ? secondaryAction.onClick({ close, root }) : undefined;
          if (r && typeof r.then === 'function') await r;
        } finally {
          // Secondary defaults to "just close" unless onClick kept it
          // open explicitly by not calling close.
          if (!secondaryAction.keepOpen) close('secondary');
        }
      });
    }

    const primBtn = root.querySelector('[data-modal-primary]');
    if (primBtn && primaryAction) {
      primBtn.addEventListener('click', async () => {
        primBtn.disabled = true;
        const origLabel = primBtn.textContent;
        primBtn.textContent = '…';
        try {
          const r = primaryAction.onClick ? primaryAction.onClick({ close, root }) : undefined;
          if (r && typeof r.then === 'function') await r;
        } catch (_) {
          // onClick is responsible for showing errors in the body.
        } finally {
          primBtn.disabled = false;
          primBtn.textContent = origLabel;
        }
      });
    }

    // Autofocus the first input in the body, if any.
    setTimeout(() => {
      const first = root.querySelector('input,select,textarea,button[data-modal-primary]');
      if (first) first.focus();
    }, 0);

    return { close, root };
  },

  // confirmDialog wraps modal for destructive yes/no prompts. Resolves
  // true when primary is clicked, false on dismiss / secondary.
  confirmDialog({ title, message, confirmLabel, danger }) {
    return new Promise((resolve) => {
      let decided = false;
      const m = Components.modal({
        title: title || 'Confirm',
        body: `<p>${escapeHtml(message || '')}</p>`,
        primaryAction: {
          label: confirmLabel || 'Confirm',
          danger: !!danger,
          onClick: ({ close }) => {
            decided = true;
            resolve(true);
            close('confirm');
          },
        },
        secondaryAction: { label: 'Cancel' },
        onClose: (reason) => {
          if (!decided) resolve(false);
        },
      });
      void m;
    });
  },

  // applyFieldErrors clears every .field-error inside the form, then
  // writes each problem.field_errors entry into the matching name=
  // field's error span. Unknown field names surface in a generic
  // banner at the top of the form (data-field-error="__general__").
  applyFieldErrors(formEl, fieldErrors) {
    if (!formEl) return;
    formEl.querySelectorAll('.field-error').forEach(el => {
      el.textContent = '';
      el.style.display = 'none';
    });
    if (!fieldErrors || !fieldErrors.length) return;
    const general = [];
    fieldErrors.forEach(fe => {
      const span = formEl.querySelector(`[data-field-error="${cssEscape(fe.field)}"]`);
      if (span) {
        span.textContent = fe.message;
        span.style.display = 'block';
      } else {
        general.push(`${fe.field}: ${fe.message}`);
      }
    });
    if (general.length) {
      const gen = formEl.querySelector('[data-field-error="__general__"]');
      if (gen) {
        gen.textContent = general.join('; ');
        gen.style.display = 'block';
      }
    }
  },

  // bulkActionsBar renders a sticky bottom bar with a selection count
  // and action buttons. The caller mounts it once and toggles visibility
  // via .hidden based on selection state.
  // --- SPEC-SCHED1.4c: timeline helpers ---
  //
  // The timeline is a day-grouped vertical list, not a calendar grid —
  // no date library, only native Date arithmetic. Grouping uses the
  // browser's local timezone so "Today" on the header matches what the
  // operator reads on their wall clock.

  groupByDay(firings) {
    const map = new Map();
    (firings || []).forEach(f => {
      const d = new Date(f.fires_at);
      if (isNaN(d.getTime())) return;
      const key = d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate());
      if (!map.has(key)) map.set(key, []);
      map.get(key).push(f);
    });
    const keys = Array.from(map.keys()).sort();
    return keys.map(key => ({
      date: key,
      dayLabel: relativeDayLabel(key),
      items: map.get(key).sort((a, b) => new Date(a.fires_at) - new Date(b.fires_at)),
    }));
  },

  timelineRow({ firing, onClick }) {
    const time = new Date(firing.fires_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    const tmpl = firing.template_id
      ? `<span class="mono" title="${escapeHtml(firing.template_id)}">${Components.truncateID(firing.template_id)}</span>`
      : '<span class="text-muted">—</span>';
    const row = document.createElement('div');
    row.className = 'timeline-row';
    row.innerHTML = `
      <span class="timeline-time mono">${escapeHtml(time)}</span>
      <span class="timeline-target mono" title="${escapeHtml(firing.target_id)}">${Components.truncateID(firing.target_id)}</span>
      <span class="timeline-template">${tmpl}</span>
      <span class="timeline-schedule mono" title="${escapeHtml(firing.schedule_id)}">${Components.truncateID(firing.schedule_id)}</span>
    `;
    if (typeof onClick === 'function') {
      row.classList.add('clickable');
      row.addEventListener('click', () => onClick(firing));
    }
    return row;
  },

  // timelineEmptySlot renders a clickable empty-state rectangle for a
  // day with no firings. Defaults to local 09:00 per OQ2 — the most
  // common "start of business day" anchor operators reach for.
  timelineEmptySlot({ date, onClickCreate }) {
    const slot = document.createElement('div');
    slot.className = 'timeline-empty-slot';
    slot.textContent = 'Click to create a schedule for this day (09:00 local)';
    if (typeof onClickCreate === 'function') {
      slot.classList.add('clickable');
      slot.addEventListener('click', () => {
        const [y, m, d] = date.split('-').map(n => parseInt(n, 10));
        const local = new Date(y, m - 1, d, 9, 0, 0);
        onClickCreate(local.toISOString(), date);
      });
    }
    return slot;
  },

  // horizonSelector wraps a <select>. Default is 24h. onChange fires
  // with the Go-duration string ("1h", "24h", "7d", etc.) the server's
  // upcoming handler accepts.
  horizonSelector({ current, onChange }) {
    const opts = [
      { v: '1h', l: '1 hour' },
      { v: '6h', l: '6 hours' },
      { v: '24h', l: '24 hours' },
      { v: '72h', l: '3 days' },
      { v: '168h', l: '7 days' },
      { v: '720h', l: '30 days' },
    ];
    const sel = document.createElement('select');
    sel.className = 'filter-select';
    sel.innerHTML = opts.map(o =>
      `<option value="${o.v}"${o.v === (current || '24h') ? ' selected' : ''}>${o.l}</option>`
    ).join('');
    if (typeof onChange === 'function') {
      sel.addEventListener('change', () => onChange(sel.value));
    }
    return sel;
  },

  // targetFilterSelector renders a datalist-backed text input so the
  // operator can type any target ID without coupling to a /targets
  // fetch. Existing target IDs (optional) populate the autocomplete.
  targetFilterSelector({ targets, current, onChange }) {
    const wrap = document.createElement('span');
    const listID = 'target-filter-dl';
    const datalistHTML = (targets || []).map(t =>
      `<option value="${escapeHtml(t.id || t)}">${escapeHtml(t.value || t.id || t)}</option>`
    ).join('');
    wrap.innerHTML = `
      <input class="form-input" list="${listID}" placeholder="All targets"
             value="${escapeHtml(current || '')}" style="width:240px">
      <datalist id="${listID}">${datalistHTML}</datalist>
    `;
    const input = wrap.querySelector('input');
    if (typeof onChange === 'function') {
      input.addEventListener('change', () => onChange(input.value.trim()));
    }
    return wrap;
  },

  bulkActionsBar({ selectedCount, actions }) {
    const hidden = selectedCount > 0 ? '' : ' hidden';
    const btns = (actions || []).map((a, i) => {
      const cls = a.danger ? 'btn btn-danger' : 'btn btn-ghost';
      return `<button type="button" class="${cls}" data-bulk-action="${i}">${escapeHtml(a.label)}</button>`;
    }).join(' ');
    return `<div class="bulk-actions-bar${hidden}">
      <span class="bulk-actions-count"><strong>${selectedCount}</strong> selected</span>
      <span class="bulk-actions-buttons">${btns}</span>
    </div>`;
  },

  // --- UI v2 foundation (PR1 #34) ---------------------------------
  //
  // The redesign introduces "pill" shapes for severity and finding
  // status, a removable filter chip, kbd hints, a registry for inline
  // SVG icons, an imperative slide-over panel, and a floating bulk-bar
  // (distinct from the legacy bulkActionsBar). These helpers are
  // additive; the older severityBadge/statusBadge/bulkActionsBar stay
  // for the pages that haven't been migrated yet.

  // severityPill renders a CRITICAL/HIGH/... pill with a leading dot
  // colored from --sev-{level}. Unknown levels fall back to info.
  severityPill(level) {
    const lvl = SEVERITY_LEVELS[level] ? level : 'info';
    const label = (level || 'info').toString().toUpperCase();
    return `<span class="pill sev-pill-${lvl}"><span class="pill-dot"></span>${escapeHtml(label)}</span>`;
  },

  // statusPill renders a finding triage status (open/ack/resolved/fp/
  // ignored) with the human label. Unknown statuses render as raw.
  statusPill(status) {
    const labels = {
      open: 'Open',
      ack: 'Acknowledged',
      resolved: 'Resolved',
      fp: 'False positive',
      ignored: 'Ignored',
    };
    const cls = labels[status] ? `status-pill-${status}` : 'status-pill-fp';
    const label = labels[status] || (status || '').toString();
    return `<span class="pill ${cls}">${escapeHtml(label)}</span>`;
  },

  // filterChip returns an HTMLElement (not a string) so the caller can
  // wire onRemove without setting up event delegation. Passing
  // onRemove=null/undefined renders without the ✕ button — useful for
  // read-only displays.
  filterChip(label, onRemove) {
    const el = document.createElement('span');
    el.className = 'filter-chip';
    const removeHtml = onRemove
      ? `<button type="button" class="filter-chip-remove" aria-label="Remove ${escapeHtml(label)}">&times;</button>`
      : '';
    el.innerHTML = `<span class="filter-chip-label">${escapeHtml(label)}</span>${removeHtml}`;
    if (onRemove) {
      el.querySelector('.filter-chip-remove').addEventListener('click', (e) => {
        e.stopPropagation();
        onRemove(e);
      });
    }
    return el;
  },

  // kbd wraps a shortcut hint in a styled <kbd>. Used by Cmd+K,
  // help overlays, and inline shortcuts.
  kbd(label) {
    return `<kbd class="kbd">${escapeHtml(label)}</kbd>`;
  },

  // icon looks up a name in ICONS and returns an inline <svg> string
  // sized to 12/14/16px. Unknown names return ''. Keep ICONS as the
  // single registry — adding a glyph here makes it available app-wide.
  icon(name, size) {
    const sz = size === 12 || size === 14 || size === 16 ? size : 16;
    const path = ICONS[name];
    if (!path) return '';
    return `<svg class="icon icon-${sz}" width="${sz}" height="${sz}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${path}</svg>`;
  },

  // slideOver mounts a right-anchored panel as a sibling of <body>.
  // body may be an HTML string or an HTMLElement; the latter is taken
  // over (appendChild) so callers can wire up event listeners before
  // mounting. actions is an array of { label, onClick, primary, danger }
  // rendered as buttons in the footer.
  //
  // Returns { close, root, body } so the caller can refresh the body
  // (e.g. after a status change) without closing the panel.
  //
  // Esc and backdrop click both invoke onClose with reason='dismiss'.
  slideOver({ title, body, actions, onClose }) {
    const backdrop = document.createElement('div');
    backdrop.className = 'slide-over-backdrop';
    const panel = document.createElement('aside');
    panel.className = 'slide-over';
    panel.setAttribute('role', 'dialog');
    panel.setAttribute('aria-modal', 'true');
    panel.setAttribute('aria-label', title || 'Detail');

    const actionsHtml = (actions || []).map((a, i) => {
      const cls = a.danger ? 'btn btn-danger' : (a.primary ? 'btn btn-primary' : 'btn btn-ghost');
      return `<button type="button" class="${cls}" data-slide-action="${i}">${escapeHtml(a.label)}</button>`;
    }).join('');

    panel.innerHTML = `
      <div class="slide-over-header">
        <h3 class="slide-over-title">${escapeHtml(title || '')}</h3>
        <button type="button" class="slide-over-close" aria-label="Close">&times;</button>
      </div>
      <div class="slide-over-body" data-slide-body></div>
      ${actions && actions.length ? `<div class="slide-over-footer">${actionsHtml}</div>` : ''}
    `;
    const bodyEl = panel.querySelector('[data-slide-body]');
    if (body instanceof HTMLElement) {
      bodyEl.appendChild(body);
    } else if (typeof body === 'string') {
      bodyEl.innerHTML = body;
    }

    document.body.appendChild(backdrop);
    document.body.appendChild(panel);
    // Force a reflow so the .open class triggers the transition
    // instead of being collapsed into the initial paint.
    void panel.offsetWidth;
    backdrop.classList.add('open');
    panel.classList.add('open');
    document.body.classList.add('modal-open');

    let closed = false;
    function close(reason) {
      if (closed) return;
      closed = true;
      backdrop.classList.remove('open');
      panel.classList.remove('open');
      document.removeEventListener('keydown', onKey);
      document.body.classList.remove('modal-open');
      // Allow the slide-out transition to play before we yank the DOM.
      setTimeout(() => {
        backdrop.remove();
        panel.remove();
      }, 200);
      if (typeof onClose === 'function') onClose(reason || 'close');
    }
    function onKey(e) {
      if (e.key === 'Escape') close('dismiss');
    }
    document.addEventListener('keydown', onKey);
    backdrop.addEventListener('click', () => close('dismiss'));
    panel.querySelector('.slide-over-close').addEventListener('click', () => close('dismiss'));

    panel.querySelectorAll('[data-slide-action]').forEach((btn) => {
      const i = parseInt(btn.getAttribute('data-slide-action'), 10);
      const a = (actions || [])[i];
      if (!a || typeof a.onClick !== 'function') return;
      btn.addEventListener('click', async () => {
        const r = a.onClick({ close, root: panel, body: bodyEl });
        if (r && typeof r.then === 'function') {
          btn.disabled = true;
          try { await r; } finally { btn.disabled = false; }
        }
      });
    });

    return { close, root: panel, body: bodyEl };
  },

  // bulkBar renders the floating, pill-shaped selection bar used by
  // the redesigned findings list. Returns an HTML string with a hidden
  // class when count is 0; pages toggle visibility by re-rendering or
  // by flipping .hidden directly. action.danger styles as btn-danger,
  // action.primary as btn-primary, otherwise ghost.
  bulkBar({ count, actions }) {
    const hidden = count > 0 ? '' : ' hidden';
    const btns = (actions || []).map((a, i) => {
      const cls = a.danger ? 'btn btn-danger' : (a.primary ? 'btn btn-primary' : 'btn btn-ghost');
      return `<button type="button" class="${cls}" data-bulk-action="${i}">${escapeHtml(a.label)}</button>`;
    }).join('');
    return `<div class="bulk-bar${hidden}">
      <span class="bulk-bar-count"><strong>${count || 0}</strong>selected</span>
      <span class="bulk-bar-actions">${btns}</span>
    </div>`;
  },
};

// Severity level whitelist for severityPill. Kept as a frozen object
// so an unknown level can short-circuit to 'info' without spelling out
// the five branches in the helper.
const SEVERITY_LEVELS = Object.freeze({
  critical: true, high: true, medium: true, low: true, info: true,
});

// Inline SVG path data for the foundation icon set. Each entry is the
// inner markup of a 24×24 viewBox <svg> with stroke=currentColor and
// stroke-width=2 — Components.icon() wraps these into a sized <svg>.
// Add new glyphs here, not at call sites.
const ICONS = Object.freeze({
  x:              '<line x1="6" y1="6" x2="18" y2="18"/><line x1="6" y1="18" x2="18" y2="6"/>',
  search:         '<circle cx="11" cy="11" r="7"/><line x1="20" y1="20" x2="16.65" y2="16.65"/>',
  plus:           '<line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>',
  'chevron-down': '<polyline points="6 9 12 15 18 9"/>',
  'chevron-right':'<polyline points="9 6 15 12 9 18"/>',
  check:          '<polyline points="5 12 10 17 19 7"/>',
  clock:          '<circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>',
  alert:          '<path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/>',
  copy:           '<rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/>',
});

function pad2(n) { return String(n).padStart(2, '0'); }

// relativeDayLabel renders a day-key ("YYYY-MM-DD") as "Today", "Tomorrow",
// "Yesterday", or "Weekday, Mon D" relative to the browser's local date.
function relativeDayLabel(key) {
  const [y, m, d] = key.split('-').map(n => parseInt(n, 10));
  const date = new Date(y, m - 1, d);
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const deltaDays = Math.round((date - today) / 86400000);
  if (deltaDays === 0) return 'Today';
  if (deltaDays === 1) return 'Tomorrow';
  if (deltaDays === -1) return 'Yesterday';
  return date.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' });
}

// toDatetimeLocal converts an ISO-8601 / Date value to the native
// datetime-local format ("YYYY-MM-DDTHH:MM") the input expects. Returns
// an empty string if `v` is nullish or unparseable.
function toDatetimeLocal(v) {
  if (!v) return '';
  const d = new Date(v);
  if (isNaN(d.getTime())) return typeof v === 'string' ? v : '';
  const pad = (n) => String(n).padStart(2, '0');
  return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate())
    + 'T' + pad(d.getHours()) + ':' + pad(d.getMinutes());
}

// cssEscape quotes a string for use in a CSS attribute selector. Light
// polyfill for CSS.escape; sufficient for dotted tool_config.* paths.
function cssEscape(s) {
  if (typeof CSS !== 'undefined' && typeof CSS.escape === 'function') return CSS.escape(s);
  return String(s).replace(/(["\\])/g, '\\$1');
}

// Demo — invocation shapes for the 1.4b helpers. Not executed; kept as
// a human-readable quick reference so the next maintainer doesn't have
// to chase call sites.
//
//   Components.formInput({label:'Name', name:'name', required:true})
//   Components.formSelect({label:'Status', name:'status',
//     options:[{value:'active',label:'Active'},{value:'paused',label:'Paused'}],
//     value:'active'})
//   Components.formTextarea({label:'Tool config', name:'tool_config',
//     monospace:true, rows:8})
//   Components.formDatetime({label:'Starts at', name:'dtstart', required:true})
//   Components.rruleField({label:'RRULE', name:'rrule', value:'FREQ=DAILY'})
//   const m = Components.modal({
//     title:'New schedule',
//     body:'<form id="f">...</form>',
//     primaryAction:{label:'Create', onClick: async ({close}) => {
//       try { await API.createSchedule(body); close(); }
//       catch (err) { Components.applyFieldErrors(document.getElementById('f'), err.fieldErrors); }
//     }},
//     secondaryAction:{label:'Cancel'},
//   })
//   const ok = await Components.confirmDialog({
//     title:'Delete schedule?', message:'Cannot be undone.', danger:true})
//   Components.bulkActionsBar({selectedCount:2, actions:[
//     {label:'Pause', onClick: ...},
//     {label:'Delete', danger:true, onClick: ...}]})

function toggleTreeNode(el) {
  const children = el.nextElementSibling;
  if (!children) return;
  children.classList.toggle('open');
  const toggle = el.querySelector('.tree-toggle');
  if (toggle) {
    toggle.innerHTML = children.classList.contains('open') ? '&#9660;' : '&#9654;';
  }
}

function escapeHtml(str) {
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

function safeLink(url) {
  const safe = /^https?:\/\//i.test(url);
  if (safe) {
    return `<li><a href="${escapeHtml(url)}" target="_blank" rel="noopener">${escapeHtml(url)}</a></li>`;
  }
  return `<li>${escapeHtml(url)}</li>`;
}

function copyToClipboard(text, el) {
  navigator.clipboard.writeText(text).then(() => {
    const orig = el.textContent;
    el.textContent = 'Copied!';
    setTimeout(() => { el.textContent = orig; }, 1500);
  });
}
