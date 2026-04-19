// SPEC-SCHED1.4b R9: Ad-hoc dispatcher modal.
//
// Triggered by the "Run scan now" button in the sidebar. The modal hits
// POST /api/v1/scans/ad-hoc with {target_id, reason?, tool_config_override?}
// and renders either the 202 success view (with the returned scan_id) or
// a typed error message derived from the Problem `type` (TARGET_BUSY,
// IN_BLACKOUT, DISPATCHER_UNREACHABLE). Target picker is free-text —
// targets-page integration lands in 1.4c (spec non-goal N3).

const AdHocPage = {
  open() {
    const body = this.formBody({});
    const m = Components.modal({
      title: 'Run scan now',
      body,
      size: 'md',
      primaryAction: {
        label: 'Dispatch',
        onClick: async ({ close, root }) => {
          const form = root.querySelector('#adhoc-form');
          const data = readAdHocForm(form);
          if (!data) return;
          try {
            const resp = await API.createAdHocScan(data);
            swapToSuccessView(root, resp);
          } catch (err) {
            showAdHocFormError(root, form, err);
          }
        },
      },
      secondaryAction: { label: 'Close' },
    });
    return m;
  },

  // formBody is exported so the success view's "Run another" button can
  // restore the original form markup after a successful dispatch.
  formBody(preset) {
    const p = preset || {};
    return `
      <form id="adhoc-form" novalidate>
        <div class="field-error" data-field-error="__general__" style="display:none;margin-bottom:12px;padding:8px 12px;background:var(--sev-critical-bg);border-radius:var(--radius)"></div>
        ${Components.formInput({
          label: 'Target ID', name: 'target_id', value: p.target_id || '', required: true,
          placeholder: 'UUID from /targets',
          help: 'Free-text target ID. Picker ships in 1.4c.',
        })}
        ${Components.formInput({ label: 'Template ID', name: 'template_id', value: p.template_id || '',
          help: 'Optional. If set, the template\'s tool_config is used (overlaid with the override below).' })}
        ${Components.formInput({ label: 'Reason', name: 'reason', value: p.reason || '',
          placeholder: 'e.g. manual re-run after infra change' })}
        ${Components.formTextarea({
          label: 'Tool config override (JSON)', name: 'tool_config_override',
          value: p.tool_config_override || '', rows: 6, monospace: true,
          help: 'Optional. Leave blank to let the server auto-populate from the target/template.',
        })}
      </form>
    `;
  },
};

function readAdHocForm(form) {
  if (!form) return null;
  const fd = new FormData(form);
  const out = {
    target_id: (fd.get('target_id') || '').toString().trim(),
    reason: (fd.get('reason') || '').toString().trim(),
  };
  if (!out.target_id) {
    Components.applyFieldErrors(form, [{ field: 'target_id', message: 'required' }]);
    return null;
  }
  if (!out.reason) delete out.reason;
  const tmpl = (fd.get('template_id') || '').toString().trim();
  if (tmpl) out.template_id = tmpl;

  const rawOverride = (fd.get('tool_config_override') || '').toString().trim();
  if (rawOverride) {
    let parsed;
    try { parsed = JSON.parse(rawOverride); }
    catch (err) {
      Components.applyFieldErrors(form, [{ field: 'tool_config_override', message: 'Invalid JSON: ' + err.message }]);
      return null;
    }
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      Components.applyFieldErrors(form, [{ field: 'tool_config_override', message: 'Must be a JSON object' }]);
      return null;
    }
    out.tool_config_override = parsed;
  }
  return out;
}

// showAdHocFormError maps code → tailored message; non-typed errors fall
// back to the generic field-error / banner path.
function showAdHocFormError(root, form, err) {
  // Typed problem types get their own body swap so the operator sees a
  // clear message instead of a generic banner.
  if (err && err.code === 'DISPATCHER_UNREACHABLE') {
    showAdHocBanner(root, form, 'Daemon dispatcher is not reachable. Is the daemon running?');
    return;
  }
  if (err && err.code === 'TARGET_BUSY') {
    showAdHocBanner(root, form, 'Target is already being scanned. Try again when the current scan finishes.');
    return;
  }
  if (err && err.code === 'IN_BLACKOUT') {
    showAdHocBanner(root, form, 'Target is inside an active blackout window. Try again later.');
    return;
  }
  if (err && err.fieldErrors && err.fieldErrors.length) {
    Components.applyFieldErrors(form, err.fieldErrors);
    return;
  }
  const general = form.querySelector('[data-field-error="__general__"]');
  if (general) {
    const title = (err && err.problem && err.problem.title) || (err && err.message) || 'Request failed';
    const detail = err && err.problem && err.problem.detail ? ' — ' + err.problem.detail : '';
    general.textContent = title + detail;
    general.style.display = 'block';
  }
}

function showAdHocBanner(root, form, message) {
  const general = form.querySelector('[data-field-error="__general__"]');
  if (general) {
    general.textContent = message;
    general.style.display = 'block';
  }
}

function swapToSuccessView(root, resp) {
  const body = root.querySelector('.modal-body');
  if (!body) return;
  const scanHtml = resp.scan_id
    ? `<div><span class="detail-label">Scan ID</span><br><span class="mono">${escapeHtml(resp.scan_id)}</span></div>`
    : '<div class="text-muted">Scan ID pending — poll <code class="mono">ad_hoc_scan_runs</code> by the run ID below.</div>';
  body.innerHTML = `
    <div class="card" style="border-color:var(--success)">
      <div class="card-label" style="color:var(--success)">Dispatched ✓</div>
      <div style="margin-top:8px">
        <span class="detail-label">Ad-hoc run ID</span><br>
        <span class="mono">${escapeHtml(resp.ad_hoc_run_id)}</span>
      </div>
      <div style="margin-top:12px">${scanHtml}</div>
    </div>
    <button type="button" class="btn btn-ghost" id="adhoc-run-another" style="margin-top:12px">Run another</button>
  `;
  // Replace primary button with Close so the "Dispatch" spinner state
  // doesn't linger on the success view.
  const footer = root.querySelector('.modal-footer');
  if (footer) {
    const primary = footer.querySelector('[data-modal-primary]');
    if (primary) primary.remove();
  }
  const another = body.querySelector('#adhoc-run-another');
  if (another) another.addEventListener('click', () => {
    body.innerHTML = AdHocPage.formBody({});
    // Re-add the Dispatch button.
    if (footer && !footer.querySelector('[data-modal-primary]')) {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'btn btn-primary';
      btn.dataset.modalPrimary = '';
      btn.textContent = 'Dispatch';
      footer.appendChild(btn);
      btn.addEventListener('click', async () => {
        btn.disabled = true; btn.textContent = '…';
        try {
          const form = document.getElementById('adhoc-form');
          const data = readAdHocForm(form);
          if (!data) { btn.disabled = false; btn.textContent = 'Dispatch'; return; }
          try {
            const resp2 = await API.createAdHocScan(data);
            swapToSuccessView(root, resp2);
          } catch (err) {
            showAdHocFormError(root, form, err);
            btn.disabled = false; btn.textContent = 'Dispatch';
          }
        } catch (_) {
          btn.disabled = false; btn.textContent = 'Dispatch';
        }
      });
    }
  });
}
