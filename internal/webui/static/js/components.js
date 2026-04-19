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
};

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
