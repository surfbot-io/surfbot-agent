// Assets page
const AssetsPage = {
  async render(app, params) {
    app.innerHTML = '<div class="loading">Loading assets...</div>';

    try {
      const [listData, treeData] = await Promise.all([
        API.assets({ type: params?.type, target_id: params?.target_id, limit: 200 }),
        API.assetTree(params?.target_id || ''),
      ]);

      app.innerHTML = this.template(listData, treeData, params);
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load assets: ' + err.message);
    }
  },

  template(listData, treeData, params) {
    const view = params?.view || 'tree';

    if (listData.assets.length === 0) {
      return `
        <div class="page-header"><h2>Assets</h2></div>
        ${Components.emptyState('No assets', 'No assets discovered yet. Run a scan to discover assets.')}
      `;
    }

    const typ = params?.type || '';
    const viewToggle = `<div class="filters">
      <select class="filter-select" id="filter-view" onchange="AssetsPage.applyFilters()">
        <option value="tree" ${view === 'tree' ? 'selected' : ''}>Tree View</option>
        <option value="list" ${view === 'list' ? 'selected' : ''}>List View</option>
      </select>
      <select class="filter-select" id="filter-type" onchange="AssetsPage.applyFilters()">
        <option value="">All types</option>
        <option value="subdomain" ${typ === 'subdomain' ? 'selected' : ''}>Subdomain</option>
        <option value="ipv4" ${typ === 'ipv4' ? 'selected' : ''}>IPv4</option>
        <option value="ipv6" ${typ === 'ipv6' ? 'selected' : ''}>IPv6</option>
        <option value="port_service" ${typ === 'port_service' ? 'selected' : ''}>Port/Service</option>
        <option value="url" ${typ === 'url' ? 'selected' : ''}>URL</option>
        <option value="technology" ${typ === 'technology' ? 'selected' : ''}>Technology</option>
      </select>
    </div>`;

    let contentHtml;
    if (view === 'tree' && treeData.tree && treeData.tree.length > 0) {
      contentHtml = `<div class="card" style="padding:16px">
        ${treeData.tree.map(node => Components.treeNode(node)).join('')}
      </div>`;
    } else {
      const rows = listData.assets.map(a => `
        <tr>
          <td class="mono">${escapeHtml(a.value)}</td>
          <td><span class="badge badge-info">${a.type}</span></td>
          <td>${Components.statusBadge(a.status)}</td>
          <td class="text-muted">${Components.timeAgo(a.first_seen)}</td>
          <td class="text-muted">${Components.timeAgo(a.last_seen)}</td>
        </tr>
      `).join('');

      contentHtml = Components.table(
        ['Value', 'Type', 'Status', 'First Seen', 'Last Seen'],
        [rows]
      );
    }

    return `
      <div class="page-header">
        <h2>Assets</h2>
        <p>${listData.total} total assets</p>
      </div>
      ${viewToggle}
      ${contentHtml}
    `;
  },

  applyFilters() {
    const view = document.getElementById('filter-view')?.value || 'tree';
    const type = document.getElementById('filter-type')?.value || '';
    const q = ['view=' + view];
    if (type) q.push('type=' + type);
    location.hash = '#/assets?' + q.join('&');
  },
};
