// Plan review and apply (design principle 1, runbook section 1): every change is
// computed, shown with its before and after values and warnings, and written
// only after the technician confirms. Temporary passwords are shown once.
import { $, h, icon, clear, call, errText, openDialog, debounce, plural, copyText, spinner, toast, nav, rdnName } from './core.js';

const KIND_TEXT = {
  create: 'Create', set: 'Set', 'add-member': 'Add member', 'remove-member': 'Remove member', move: 'Move', rename: 'Rename',
  enable: 'Enable', disable: 'Disable', unlock: 'Unlock', 'reset-password': 'Reset password', delete: 'Delete', undelete: 'Restore',
};

let current = null; // { build, plan, ticket, onApplied }

/**
 * reviewPlan opens the review dialog. build(ticket) returns a PlanView; it is
 * called again when the ticket changes, because the plan hash covers it.
 * onApplied(response) runs after a successful or partial apply.
 */
export async function reviewPlan(build, { onApplied } = {}) {
  const dlg = $('#plan-dlg');
  current = { build, plan: null, ticket: '', onApplied, seq: 0 };
  $('#plan-title').textContent = 'Building the plan';
  clear($('#plan-body')).append(h('div', { class: 'loading-line' }, spinner(), ' Reading the current values and your rights on the domain controller'));
  clear($('#plan-foot'));
  openDialog(dlg);
  await rebuild();
}

async function rebuild() {
  const mine = ++current.seq;
  const body = $('#plan-body');
  body.classList.add('busy');
  try {
    const plan = await current.build(current.ticket);
    if (mine !== current.seq) return;
    current.plan = plan;
    renderPlan(plan);
  } catch (e) {
    if (mine !== current.seq) return;
    current.plan = null;
    $('#plan-title').textContent = 'The plan could not be built';
    clear(body).append(h('p', { class: 'error', role: 'alert', text: errText(e) }));
    clear($('#plan-foot')).append(h('button', { type: 'button', class: 'ghost', 'data-close': true }, 'Close'));
  } finally {
    if (mine === current.seq) body.classList.remove('busy');
  }
}

const onTicket = debounce(() => rebuild(), 450);

function renderPlan(p) {
  $('#plan-title').textContent = p.title || 'Review plan';
  const body = $('#plan-body');
  const changes = p.changes || [];
  const warnings = p.warnings || [];
  // The ticket field survives a re-plan so typing is never interrupted.
  let ticketBox = $('#plan-ticket-box');
  if (!ticketBox) {
    clear(body);
    const ticket = h('input', { id: 'plan-ticket', value: current.ticket, placeholder: 'For example T-1042', spellcheck: 'false', autocomplete: 'off' });
    ticket.addEventListener('input', () => {
      current.ticket = ticket.value.trim();
      const apply = $('#plan-apply');
      if (apply) apply.disabled = true;
      $('#plan-ticket-state').textContent = 'Updating the plan';
      onTicket();
    });
    ticketBox = h('label', { class: 'ticket', id: 'plan-ticket-box' }, 'Ticket reference', ticket, h('span', { id: 'plan-ticket-state', class: 'muted small' }));
    body.append(h('div', { id: 'plan-meta-box' }), ticketBox, h('div', { id: 'plan-dyn', class: 'plan-dyn' }));
  }
  $('#plan-ticket-state').textContent = p.ticket ? 'Recorded with every step' : 'Optional; recorded with every step';
  clear($('#plan-meta-box')).append(h('div', { class: 'plan-meta' },
    fact('Domain controller', p.dc), fact('Technician', p.technician),
    fact('Objects', String(p.objects)), fact('Changes', String(changes.length))));
  const dyn = clear($('#plan-dyn'));
  const old = $('#plan-apply-err');
  if (old) old.remove();

  if (warnings.length) {
    dyn.append(h('ul', { class: 'warnings', id: 'plan-warnings' }, warnings.map((w) =>
      h('li', { class: w.blocking ? 'crit' : 'warn' }, icon(w.blocking ? 'ban' : 'alert'), h('span', { text: w.message })))));
  }

  if (!changes.length) {
    dyn.append(h('p', { class: 'note info', text: warnings.length ? 'Nothing can be changed as planned; see the warnings above.' : 'Nothing to change: the directory already has these values.' }));
  } else {
    dyn.append(h('ol', { class: 'changes', id: 'plan-changes' }, changes.map((c) => {
      const li = h('li', { class: 'change' }, h('div', { class: 'change-head' }, h('span', { class: 'kind-tag ' + c.kind, text: KIND_TEXT[c.kind] || c.kind }), h('span', { class: 'change-sum', text: c.summary })));
      if (c.kind === 'create') {
        li.append(h('ul', { class: 'create-attrs' }, (c.after || []).map((a) => h('li', { text: a }))));
      } else if (c.attribute || (c.before && c.before.length) || (c.after && c.after.length)) {
        const before = (c.before || []).join('; ');
        const after = (c.after || []).join('; ');
        li.append(h('div', { class: 'diff' },
          c.attribute ? h('code', { class: 'attr', text: c.attribute }) : null,
          h('span', { class: 'before' + (before ? '' : ' none'), text: before || 'not set' }),
          icon('chevron', 'arrow'),
          h('span', { class: 'after' + (after ? '' : ' none'), text: c.kind === 'reset-password' ? 'a new temporary password' : after || 'cleared' })));
      }
      return li;
    })));
  }

  const confirm = h('div', { class: 'confirm' });
  let tier0, typed;
  if (p.tier0) {
    tier0 = h('input', { type: 'checkbox', id: 'plan-tier0' });
    confirm.append(h('label', { class: 'check tier0' }, tier0, ' This plan changes Tier 0 objects. I am working from a privileged access workstation or a domain controller and confirm the change.'));
  }
  if (p.typedCount) {
    typed = h('input', { id: 'plan-typed', inputmode: 'numeric', autocomplete: 'off', class: 'narrow-input' });
    confirm.append(h('label', null, `This plan changes ${p.objects} objects. Type ${p.objects} to confirm.`, typed));
  }
  if (confirm.childNodes.length) dyn.append(confirm);

  const foot = clear($('#plan-foot'));
  const apply = h('button', { type: 'button', class: p.blocked || !changes.length ? 'ghost' : 'primary', id: 'plan-apply' }, p.blocked ? 'Blocked' : 'Apply ' + plural(changes.length, 'change'));
  const update = () => {
    apply.disabled = p.blocked || !changes.length || (tier0 && !tier0.checked) || (typed && Number(typed.value) !== p.objects);
  };
  tier0 && tier0.addEventListener('change', update);
  typed && typed.addEventListener('input', update);
  update();
  apply.addEventListener('click', () => doApply(p, tier0, typed));
  foot.append(h('span', { class: 'muted small grow', text: p.blocked ? 'A blocking warning stops this plan. Nothing will be written.' : 'Nothing is written until you apply.' }),
    h('button', { type: 'button', class: 'ghost', 'data-close': true }, 'Cancel'), apply);
}

function fact(label, value) {
  return h('div', { class: 'fact' }, h('span', { class: 'fact-label', text: label }), h('span', { class: 'fact-value', text: value || 'unknown' }));
}

async function doApply(p, tier0, typed) {
  const apply = $('#plan-apply');
  apply.disabled = true;
  clear(apply).append(spinner(), ' Applying');
  try {
    const res = await call('Apply', { planId: p.id, hash: p.hash, typedCount: typed ? Number(typed.value) : 0, confirmTier0: !!(tier0 && tier0.checked) });
    renderResult(p, res);
    if (current.onApplied) current.onApplied(res);
  } catch (e) {
    clear(apply).append('Apply');
    apply.disabled = false;
    const body = $('#plan-body');
    const old = $('#plan-apply-err');
    if (old) old.remove();
    body.prepend(h('p', { class: 'error', id: 'plan-apply-err', role: 'alert', text: errText(e) }));
    body.scrollTop = 0;
  }
}

function renderResult(p, res) {
  const steps = res.steps || [];
  $('#plan-title').textContent = res.ok ? 'Applied' : 'Stopped';
  const body = clear($('#plan-body'));
  body.append(h('p', { class: 'note ' + (res.ok ? 'ok' : 'crit'), id: 'plan-result' },
    icon(res.ok ? 'check' : 'alert'),
    h('span', { text: res.ok ? (steps.length === 1 ? 'The step finished and was recorded.' : `All ${steps.length} steps finished and were recorded.`) : (res.error ? 'The run stopped: ' + res.error : 'A step failed, so the remaining steps did not run. Every step that ran is recorded.') })));
  const passwords = steps.filter((s) => s.password);
  body.append(h('ol', { class: 'steps', id: 'plan-steps' }, steps.map((s) => h('li', { class: 'step ' + s.status },
    icon(s.status === 'done' ? 'check' : s.status === 'failed' ? 'x' : 'minus'),
    h('div', null, h('div', { text: s.summary }), s.error ? h('div', { class: 'error small', text: s.error }) : null,
      h('div', { class: 'muted small', text: s.status === 'done' ? 'Done' : s.status === 'failed' ? 'Failed' : 'Not run' }))))));
  if (passwords.length) {
    const box = h('div', { class: 'secrets', id: 'plan-secrets' }, h('div', { class: 'secrets-title' }, icon('key'), 'Temporary passwords'),
      h('p', { class: 'muted small', text: 'Shown once and not recorded anywhere. Give each password to its owner through a channel you trust; they must change it at the next sign-in.' }));
    for (const s of passwords) {
      const value = h('code', { class: 'secret masked', text: '•'.repeat(12), dataset: { secret: '' } });
      let shown = false;
      const reveal = h('button', { type: 'button', class: 'ghost small-btn', onclick: () => {
        shown = !shown;
        value.textContent = shown ? s.password : '•'.repeat(12);
        value.classList.toggle('masked', !shown);
        reveal.textContent = shown ? 'Hide' : 'Reveal';
      } }, 'Reveal');
      const copy = h('button', { type: 'button', class: 'ghost small-btn', onclick: async () => toast(await copyText(s.password) ? 'Copied to the clipboard' : 'Copy is not available; reveal and type it instead', 'info') }, 'Copy');
      box.append(h('div', { class: 'secret-row' }, h('span', { class: 'secret-for', text: rdnName(s.dn), title: s.summary }), value, reveal, copy));
    }
    body.append(box);
  }
  const foot = clear($('#plan-foot'));
  foot.append(h('span', { class: 'muted small grow', text: 'Job ' + (res.job || p.id) }),
    h('button', { type: 'button', class: 'primary', id: 'plan-done', 'data-close': true }, 'Done'));
  $('#plan-dlg').addEventListener('close', () => {
    // Passwords leave the page with the dialog.
    for (const el of document.querySelectorAll('#plan-secrets .secret')) el.textContent = '';
    nav.refreshAfterApply && nav.refreshAfterApply(res);
  }, { once: true });
}
