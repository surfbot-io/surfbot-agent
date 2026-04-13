// Tools page — shows registered detection tools and their availability.
const ToolsPage = {
  async render(app) {
    app.innerHTML = '<div class="loading">Loading tools...</div>';
    try {
      const data = await API.availableTools();
      app.innerHTML = this.template(data);
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load tools: ' + err.message);
    }
  },

  template(data) {
    const tools = data.tools || [];

    if (tools.length === 0) {
      return `
        <div class="page-header"><h2>Tools</h2></div>
        ${Components.emptyState('No tools registered', 'No detection tools are registered in the pipeline.')}
      `;
    }

    const rows = tools.map(t => {
      const badge = t.available
        ? '<span class="badge badge-success">available</span>'
        : '<span class="badge badge-muted">unavailable</span>';
      return `
        <tr>
          <td class="mono">${escapeHtml(t.name)}</td>
          <td>${escapeHtml(t.phase)}</td>
          <td>${badge}</td>
        </tr>
      `;
    }).join('');

    return `
      <div class="page-header">
        <h2>Tools</h2>
        <p>${data.available}/${data.total} available</p>
      </div>
      ${Components.table(['Name', 'Phase', 'Status'], [rows])}
    `;
  },
};
