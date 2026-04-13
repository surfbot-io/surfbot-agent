// Settings > Schedule page — configure scan schedule, intervals, tools, window.
const SettingsSchedulePage = {
  async render(app) {
    app.innerHTML = '<div class="loading">Loading schedule config...</div>';
    try {
      const [schedule, toolsData] = await Promise.all([
        API.schedule(),
        API.availableTools(),
      ]);
      app.innerHTML = this.template(schedule, toolsData.tools || []);
      this.bindEvents(app, toolsData.tools || []);
    } catch (err) {
      app.innerHTML = Components.emptyState('Error', 'Failed to load schedule config: ' + err.message);
    }
  },

  template(cfg, availableTools) {
    const quickTools = cfg.quick_check_tools || [];
    const mw = cfg.maintenance_window || {};

    const toolCheckboxes = availableTools.map(t => {
      const checked = quickTools.includes(t.name) ? 'checked' : '';
      return `<label class="checkbox-label">
        <input type="checkbox" name="quick_tool" value="${escapeHtml(t.name)}" ${checked}>
        ${escapeHtml(t.name)} <span class="text-muted">(${escapeHtml(t.phase)})</span>
      </label>`;
    }).join('');

    return `
      <div class="page-header">
        <h2>Schedule Settings</h2>
        <p>Configure automated scan intervals, tools, and maintenance windows.</p>
      </div>

      <form id="schedule-form" class="settings-form">
        <div class="form-section">
          <h3>Scheduler</h3>
          <div class="form-row">
            <label class="checkbox-label">
              <input type="checkbox" id="sched-enabled" ${cfg.enabled ? 'checked' : ''}>
              Enable scheduled scans
            </label>
          </div>
        </div>

        <div class="form-section">
          <h3>Intervals</h3>
          <div class="form-row">
            <label for="full-interval">Full scan interval</label>
            <input type="text" id="full-interval" class="form-input form-input-sm"
                   value="${escapeHtml(cfg.full_scan_interval || '24h')}"
                   placeholder="24h">
            <span class="form-hint">Duration: e.g. 24h, 12h, 30m</span>
          </div>
          <div class="form-row">
            <label for="quick-interval">Quick check interval</label>
            <input type="text" id="quick-interval" class="form-input form-input-sm"
                   value="${escapeHtml(cfg.quick_check_interval || '1h')}"
                   placeholder="1h">
            <span class="form-hint">Must be less than full scan interval</span>
          </div>
          <div class="form-row">
            <label for="jitter">Jitter</label>
            <input type="text" id="jitter" class="form-input form-input-sm"
                   value="${escapeHtml(cfg.jitter || '5m')}"
                   placeholder="5m">
            <span class="form-hint">Random delay added to prevent thundering herd</span>
          </div>
          <div class="form-row">
            <label class="checkbox-label">
              <input type="checkbox" id="run-on-start" ${cfg.run_on_start ? 'checked' : ''}>
              Run scan on daemon start
            </label>
          </div>
        </div>

        <div class="form-section">
          <h3>Quick Check Tools</h3>
          <p class="text-muted">Select which tools run during quick checks.</p>
          <div class="checkbox-group" id="quick-tools-group">
            ${toolCheckboxes || '<span class="text-muted">No tools registered</span>'}
          </div>
        </div>

        <div class="form-section">
          <h3>Maintenance Window</h3>
          <div class="form-row">
            <label class="checkbox-label">
              <input type="checkbox" id="window-enabled" ${mw.enabled ? 'checked' : ''}>
              Enable maintenance window (block scans during this period)
            </label>
          </div>
          <div id="window-fields" style="${mw.enabled ? '' : 'display:none'}">
            <div class="form-row">
              <label for="window-start">Start time</label>
              <input type="time" id="window-start" class="form-input form-input-sm"
                     value="${escapeHtml(mw.start || '02:00')}">
            </div>
            <div class="form-row">
              <label for="window-end">End time</label>
              <input type="time" id="window-end" class="form-input form-input-sm"
                     value="${escapeHtml(mw.end || '04:00')}">
            </div>
            <div class="form-row">
              <label for="window-tz">Timezone</label>
              <input type="text" id="window-tz" class="form-input form-input-sm"
                     value="${escapeHtml(mw.timezone || 'UTC')}"
                     placeholder="Europe/Madrid">
              <span class="form-hint">IANA timezone (e.g. UTC, Europe/Madrid, America/New_York)</span>
            </div>
          </div>
        </div>

        <div class="form-actions">
          <button type="submit" class="btn btn-primary" id="save-btn">Save Schedule</button>
          <span id="save-status" class="form-status" style="display:none"></span>
        </div>
        <div id="save-errors" class="form-error" style="display:none"></div>
      </form>
    `;
  },

  bindEvents(app) {
    const form = document.getElementById('schedule-form');
    const windowEnabled = document.getElementById('window-enabled');
    const windowFields = document.getElementById('window-fields');

    // Toggle maintenance window fields visibility.
    windowEnabled.addEventListener('change', () => {
      windowFields.style.display = windowEnabled.checked ? '' : 'none';
    });

    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const btn = document.getElementById('save-btn');
      const statusEl = document.getElementById('save-status');
      const errEl = document.getElementById('save-errors');

      btn.disabled = true;
      btn.textContent = 'Saving...';
      statusEl.style.display = 'none';
      errEl.style.display = 'none';

      const quickTools = Array.from(document.querySelectorAll('input[name="quick_tool"]:checked'))
        .map(cb => cb.value);

      // Convert time input "HH:MM" to the expected format
      const startVal = document.getElementById('window-start').value || '02:00';
      const endVal = document.getElementById('window-end').value || '04:00';

      const payload = {
        enabled: document.getElementById('sched-enabled').checked,
        full_scan_interval: document.getElementById('full-interval').value.trim(),
        quick_check_interval: document.getElementById('quick-interval').value.trim(),
        jitter: document.getElementById('jitter').value.trim(),
        run_on_start: document.getElementById('run-on-start').checked,
        quick_check_tools: quickTools,
        maintenance_window: {
          enabled: windowEnabled.checked,
          start: startVal,
          end: endVal,
          timezone: document.getElementById('window-tz').value.trim() || 'UTC',
        },
      };

      try {
        await API.updateSchedule(payload);
        statusEl.textContent = 'Schedule updated successfully.';
        statusEl.className = 'form-status form-status-ok';
        statusEl.style.display = 'inline';
      } catch (err) {
        errEl.textContent = err.message;
        errEl.style.display = 'block';
      } finally {
        btn.disabled = false;
        btn.textContent = 'Save Schedule';
      }
    });
  },
};
