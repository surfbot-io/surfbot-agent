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
    const max = Math.max(1, ...severities.map(s => counts[s] || 0));
    return `<div class="sev-bars">${severities.map(sev => {
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
    const ths = headers.map(h => `<th>${h}</th>`).join('');
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
