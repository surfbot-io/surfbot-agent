// SPEC-SCHED1.4b R9: Ad-hoc dispatcher modal.
//
// Triggered by the "Run scan now" button in the sidebar (and Cmd+R from
// any route, see PR9 #42). The modal hits POST /api/v1/scans/ad-hoc
// with {target_id, name?, reason?, tool_config_override?, template_id?}
// and renders either the 202 success view (with the returned scan_id)
// or a typed error message derived from the Problem `type`
// (TARGET_BUSY, IN_BLACKOUT, DISPATCHER_UNREACHABLE).
//
// PR9 #42: target_id and template_id render as datalist-backed
// comboboxes so the operator can type a target value (e.g. "iana.org")
// and the field resolves to its UUID at submit. UUIDs pasted directly
// still work as a power-user escape hatch. The advanced fields
// (`reason`, `tool_config_override`) collapse under a `<details>` that
// is closed by default but auto-opens when prefilled.

const AdHocPage = {
  // SPEC-SCHED1.4c R4: open() accepts an optional prefill object so
  // target-page launchers can pre-fill target_id without duplicating
  // the modal wiring. Backwards compatible — existing callers pass no
  // argument. When `lockTargetID` is truthy (typical when launched
  // from a target page) the field renders readonly with an "Edit
  // target" link that relaxes the lock — guards against accidental
  // retargeting per OQ3.
  async open(prefill) {
    const p = prefill || {};
    const preset = {
      target_id: p.prefillTargetID || p.target_id || '',
      template_id: p.prefillTemplateID || p.template_id || '',
      name: p.prefillName || p.name || '',
      reason: p.prefillReason || p.reason || '',
      tool_config_override: p.prefillToolConfigOverride || p.tool_config_override || '',
      lockTargetID: p.lockTargetID !== undefined
        ? !!p.lockTargetID
        : !!p.prefillTargetID,
    };

    // Lookups are best-effort: the modal still renders if /targets or
    // /templates fail. The combobox just becomes free-text in that
    // case, matching the pre-PR9 behavior. Mount happens regardless,
    // so the operator never sees a blank screen waiting on lookups.
    let targets = new Map();
    let templates = new Map();
    try { targets   = await LookupCache.targets(); }   catch (_) {}
    try { templates = await LookupCache.templates(); } catch (_) {}

    const body = this.formBody(preset, targets, templates);
    const m = Components.modal({
      title: 'Run scan now',
      body,
      size: 'md',
      primaryAction: {
        label: 'Dispatch',
        onClick: async ({ close, root }) => {
          const form = root.querySelector('#adhoc-form');
          const data = readAdHocForm(form, targets, templates);
          if (!data) return;
          try {
            const resp = await API.createAdHocScan(data);
            swapToSuccessView(root, resp, targets, templates);
          } catch (err) {
            showAdHocFormError(root, form, err);
          }
        },
      },
      secondaryAction: { label: 'Close' },
    });

    // Unlock link wiring: clicking "Edit target" drops the readonly
    // attribute and focuses the field. Idempotent.
    const unlock = m.root.querySelector('#adhoc-target-unlock');
    if (unlock) unlock.addEventListener('click', (e) => {
      e.preventDefault();
      const input = m.root.querySelector('input[name="target_id"]');
      if (input) {
        input.removeAttribute('readonly');
        input.focus();
        unlock.style.display = 'none';
      }
    });

    return m;
  },

  // formBody is exported so the success view's "Run another" button can
  // restore the original form markup after a successful dispatch.
  formBody(preset, targets, templates) {
    const p = preset || {};
    const lock = !!p.lockTargetID;
    const T = targets || new Map();
    const TM = templates || new Map();

    // Display the target value when locked / prefilled by id, else the
    // raw typed value. Pre-fill resolves the id to the human label so
    // the field reads "iana.org" not "5acbf288-...".
    const targetById = T.get(p.target_id);
    const targetDisplay = p.target_id
      ? (targetById ? targetById.value : p.target_id)
      : '';
    const targetOpts = Array.from(T.values()).map(t =>
      `<option value="${escapeHtml(t.value)}">${escapeHtml(t.id)}</option>`
    ).join('');

    const tmplById = TM.get(p.template_id);
    const tmplDisplay = p.template_id
      ? (tmplById ? (tmplById.name || tmplById.value || tmplById.id) : p.template_id)
      : '';
    const tmplOpts = Array.from(TM.values()).map(t =>
      `<option value="${escapeHtml(t.name || t.value || t.id)}">${escapeHtml(t.id)}</option>`
    ).join('');

    const advancedOpen = (p.reason || p.tool_config_override) ? ' open' : '';

    const targetField = `
      <div class="form-field" data-field="target_id">
        <label class="form-label" for="f-target_id">Target <span class="form-required">*</span></label>
        <input class="form-input" id="f-target_id" name="target_id" type="text"
               list="adhoc-target-options" autocomplete="off" required
               ${lock ? 'readonly' : ''}
               placeholder="iana.org or UUID"
               value="${escapeHtml(targetDisplay)}">
        <datalist id="adhoc-target-options">${targetOpts}</datalist>
        <div class="form-help">
          ${lock
            ? 'Pre-filled from the current target page. <a href="#" id="adhoc-target-unlock">Edit target</a>.'
            : 'Type a value or paste a UUID.'}
        </div>
        <div class="field-error" data-field-error="target_id" style="display:none"></div>
      </div>
    `;

    const templateField = `
      <div class="form-field" data-field="template_id">
        <label class="form-label" for="f-template_id">Template</label>
        <input class="form-input" id="f-template_id" name="template_id" type="text"
               list="adhoc-template-options" autocomplete="off"
               placeholder="audit-daily-critical or UUID"
               value="${escapeHtml(tmplDisplay)}">
        <datalist id="adhoc-template-options">${tmplOpts}</datalist>
        <div class="form-help">Optional. Type a template name or paste a UUID.</div>
        <div class="field-error" data-field-error="template_id" style="display:none"></div>
      </div>
    `;

    const nameField = Components.formInput({
      label: 'Name', name: 'name', value: p.name || '',
      placeholder: 'e.g. Manual re-run · post-deploy verification',
      help: 'Optional. Shown on the scan in lists.',
    });

    return `
      <form id="adhoc-form" novalidate>
        <div class="field-error" data-field-error="__general__" style="display:none;margin-bottom:12px;padding:8px 12px;background:var(--sev-critical-bg);border-radius:var(--radius)"></div>
        ${targetField}
        ${templateField}
        ${nameField}
        <details class="adhoc-advanced"${advancedOpen}>
          <summary>Advanced options</summary>
          <div class="adhoc-advanced-body">
            ${Components.formInput({ label: 'Reason', name: 'reason', value: p.reason || '',
              placeholder: 'e.g. manual re-run after infra change' })}
            ${Components.formTextarea({
              label: 'Tool config override (JSON)', name: 'tool_config_override',
              value: p.tool_config_override || '', rows: 6, monospace: true,
              help: 'Optional. Leave blank to let the server auto-populate from the target/template.',
            })}
          </div>
        </details>
      </form>
    `;
  },
};

function readAdHocForm(form, targets, templates) {
  if (!form) return null;
  const fd = new FormData(form);
  const typedTarget = (fd.get('target_id') || '').toString().trim();
  if (!typedTarget) {
    Components.applyFieldErrors(form, [{ field: 'target_id', message: 'required' }]);
    return null;
  }
  const targetID = LookupCache.resolveTargetID(typedTarget, targets || new Map());
  if (targetID === null) {
    Components.applyFieldErrors(form, [{
      field: 'target_id',
      message: 'Target not found in /targets — paste the UUID instead',
    }]);
    return null;
  }

  const out = { target_id: targetID || typedTarget };

  const typedTmpl = (fd.get('template_id') || '').toString().trim();
  if (typedTmpl) {
    const tmplID = LookupCache.resolveTemplateID(typedTmpl, templates || new Map());
    if (tmplID === null) {
      Components.applyFieldErrors(form, [{
        field: 'template_id',
        message: 'Template not found — paste the UUID instead',
      }]);
      return null;
    }
    out.template_id = tmplID || typedTmpl;
  }

  const name = (fd.get('name') || '').toString().trim();
  if (name) out.name = name;

  const reason = (fd.get('reason') || '').toString().trim();
  if (reason) out.reason = reason;

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

function swapToSuccessView(root, resp, targets, templates) {
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
    body.innerHTML = AdHocPage.formBody({}, targets, templates);
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
          const data = readAdHocForm(form, targets, templates);
          if (!data) { btn.disabled = false; btn.textContent = 'Dispatch'; return; }
          try {
            const resp2 = await API.createAdHocScan(data);
            swapToSuccessView(root, resp2, targets, templates);
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
