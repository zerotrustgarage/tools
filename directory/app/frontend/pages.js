// Pages beside the results grid: new objects from templates (with a live
// preview), the AD Recycle Bin, and the change records.
import { $, h, icon, kindIcon, clear, call, errText, debounce, state, nav, pick, shortDate, plural, spinner, emptyState, badge, rdnName } from './core.js';
import { reviewPlan } from './plan.js';
import { planNow } from './object.js';

// ---- New object --------------------------------------------------------------------------

let chosen = null; // template name
let form = {};
let previewSeq = 0;

export async function showNew() {
  const page = clear($('#page-new'));
  page.append(pageHead('New object', 'Templates set the OU, naming, groups and attributes. Fill in the form and review the plan; nothing is created until you apply.'));
  const body = h('div', { class: 'new-layout' }, h('div', { class: 'loading-line' }, spinner(), ' Loading templates'));
  page.append(body);
  let list;
  try {
    list = await call('Templates');
  } catch (e) {
    clear(body).append(h('p', { class: 'error', text: errText(e) }));
    return;
  }
  const templates = list.templates || [];
  const side = h('div', { class: 'tpl-list', id: 'tpl-list' });
  const groups = { user: 'People', group: 'Groups', contact: 'Contacts', organizationalunit: 'Organizational units' };
  for (const [cls, label] of Object.entries(groups)) {
    const ts = templates.filter((t) => (t.class || 'user').toLowerCase() === cls);
    if (!ts.length) continue;
    side.append(h('div', { class: 'sub-head', text: label }));
    for (const t of ts) {
      side.append(h('button', { type: 'button', class: 'tpl' + (chosen === t.name ? ' on' : ''), dataset: { name: t.name }, onclick: () => { chosen = t.name; form = {}; showNew(); } },
        kindIcon(cls === 'organizationalunit' ? 'ou' : cls), h('span', { class: 'tpl-text' }, h('span', { class: 'tpl-name', text: t.name }), h('span', { class: 'muted small', text: t.description || '' })),
        h('span', { class: 'tpl-ver', text: 'v' + t.version })));
    }
  }
  if (!templates.length) side.append(h('p', { class: 'muted', text: 'No templates yet.' }));
  const folder = h('div', { class: 'tpl-folder' }, h('div', { class: 'sub-head', text: 'Template folder' }), h('code', { class: 'path', text: list.folder }),
    h('p', { class: 'muted small', text: state.status && state.status.mode === 'demo'
      ? 'Demo templates for the fictional lab.example directory.'
      : 'Templates are JSON files in this folder. The first time the folder is empty, the demo templates are copied here as examples; adapt or replace them before use.' }));
  for (const p of list.problems || []) folder.append(h('p', { class: 'note warn small', text: p }));
  side.append(folder);

  const main = h('div', { class: 'tpl-main' });
  const t = templates.find((x) => x.name === chosen);
  if (!t) main.append(emptyState('template', 'Choose a template', 'Pick a template on the left to start a new user, group or contact.'));
  else main.append(templateForm(t));
  clear(body).append(side, main);
}

function pageHead(title, text, ...tools) {
  return h('div', { class: 'page-head' }, h('div', null, h('h2', { text: title }), text ? h('p', { class: 'muted', text }) : null), h('div', { class: 'page-tools' }, tools));
}

function templateForm(t) {
  const cls = (t.class || 'user').toLowerCase();
  const wrap = h('div', { class: 'tpl-form-wrap' });
  const f = h('form', { class: 'form tpl-form', id: 'tpl-form', autocomplete: 'off' });
  const preview = h('div', { class: 'preview card', id: 'tpl-preview' });
  const create = h('button', { type: 'submit', class: 'primary', id: 'tpl-create', disabled: true }, 'Review plan');
  const field = (attr, label, opts = {}) => {
    let input;
    if (opts.type === 'user') {
      const shown = h('span', { class: 'picked', text: form[attr + ':name'] || 'None chosen' });
      input = h('div', { class: 'pick-field' }, shown, h('button', { type: 'button', class: 'ghost small-btn', onclick: async () => {
        const r = await pick({ title: label, cls: 'user', placeholder: 'Find a person', allowClear: !!form[attr] });
        if (!r) return;
        form[attr] = r.clear ? '' : r.dn;
        form[attr + ':name'] = r.clear ? '' : r.name;
        shown.textContent = form[attr + ':name'] || 'None chosen';
        refresh();
      } }, 'Choose'));
      return h('div', { class: 'label-like' }, h('span', null, label, opts.required ? h('span', { class: 'req', text: ' *' }) : null), input, opts.help ? h('span', { class: 'muted small', text: opts.help }) : null);
    }
    if (opts.options && opts.options.length) {
      input = h('select', { dataset: { attr } }, h('option', { value: '', text: 'Choose' }), opts.options.map((o) => h('option', { value: o, text: o, selected: form[attr] === o })));
    } else {
      input = h('input', { dataset: { attr }, type: opts.type === 'date' ? 'date' : 'text', value: form[attr] || '', spellcheck: 'false', id: 'tf-' + attr });
    }
    input.addEventListener('input', () => { form[attr] = input.value; refresh(); });
    return h('label', null, h('span', null, label, opts.required ? h('span', { class: 'req', text: ' *' }) : null), input, opts.help ? h('span', { class: 'muted small', text: opts.help }) : null);
  };
  if (cls === 'user' || cls === 'contact') {
    f.append(h('div', { class: 'row3' }, field('givenName', 'First name', { required: true }), field('initials', 'Initials'), field('sn', 'Last name', { required: true })));
  } else {
    f.append(field('name', 'Name', { required: true }));
  }
  for (const tf of t.fields || []) f.append(field(tf.attribute, tf.label || tf.attribute, { required: tf.required, type: tf.type, options: tf.options, help: tf.help }));
  const info = h('div', { class: 'tpl-facts' },
    h('div', null, h('span', { class: 'fact-label', text: 'Created in' }), h('span', { text: ouPath(t.ou) })),
    (t.groups || []).length ? h('div', null, h('span', { class: 'fact-label', text: 'Groups' }), h('span', { class: 'chips-inline' }, t.groups.map((g) => badge(rdnName(g), 'info')))) : null,
    cls === 'user' ? h('div', null, h('span', { class: 'fact-label', text: 'Account' }), h('span', { text: (t.enable ? 'Enabled' : 'Disabled') + ' with a temporary password the user must change' })) : null);
  f.append(info, h('div', { class: 'actions-row' }, create));
  f.addEventListener('submit', (e) => {
    e.preventDefault();
    const values = formValues();
    reviewPlan((ticket) => call('PlanTemplate', t.name, values, ticket), { onApplied: (res) => {
      if (res.ok) { form = {}; const dn = (res.steps || [])[0] && res.steps[0].dn; showNew(); if (dn) setTimeout(() => nav.openObject(dn), 0); }
    } });
  });
  const run = debounce(async () => {
    const mine = ++previewSeq;
    preview.classList.add('busy');
    try {
      const p = await call('PreviewTemplate', t.name, formValues());
      if (mine !== previewSeq) return;
      drawPreview(preview, p);
      create.disabled = (p.problems || []).length > 0;
    } catch (e) {
      if (mine !== previewSeq) return;
      clear(preview).append(h('p', { class: 'error', text: errText(e) }));
      create.disabled = true;
    } finally { if (mine === previewSeq) preview.classList.remove('busy'); }
  }, 250);
  const refresh = () => { create.disabled = true; run(); };
  refresh();
  wrap.append(h('div', { class: 'card tpl-card' }, h('div', { class: 'card-head' }, h('h3', { text: t.name }), h('span', { class: 'count', text: 'version ' + t.version })),
    t.description ? h('p', { class: 'muted small', text: t.description }) : null, f), preview);
  return wrap;
}

function formValues() {
  const out = {};
  for (const [k, v] of Object.entries(form)) if (!k.includes(':') && String(v).trim()) out[k] = String(v).trim();
  return out;
}

function ouPath(dn) {
  const parts = [];
  for (const m of (dn || '').matchAll(/(?:^|,)(?:OU|CN)=((?:\\.|[^,])*)/gi)) parts.unshift(m[1].replace(/\\(.)/g, '$1'));
  return parts.join(' / ') || dn;
}

function drawPreview(box, p) {
  clear(box).append(h('div', { class: 'card-head' }, h('h3', { text: 'Preview' }), h('span', { class: 'count', text: 'updates as you type' })));
  const rows = [['Name (CN)', p.cn], ['Display name', p.displayName], ['Account name', p.sAMAccountName], ['Sign-in name (UPN)', p.userPrincipalName], ['Email', p.mail], ['Location', p.dn ? ouPath(p.dn.replace(/^(?:\\.|[^,])*,/, '')) : '']];
  const list = h('dl', { class: 'preview-list', id: 'preview-list' });
  for (const [k, v] of rows) if (v) list.append(h('dt', { text: k }), h('dd', { text: v, dataset: { k } }));
  if (list.childNodes.length) box.append(list);
  for (const n of p.notes || []) box.append(h('p', { class: 'note info small' }, icon('info'), h('span', { text: n })));
  if ((p.problems || []).length) {
    box.append(h('ul', { class: 'problems', id: 'preview-problems' }, p.problems.map((x) => h('li', null, icon('alert', 'small'), h('span', { text: x })))));
  } else if ((p.attributes || []).length) {
    box.append(h('details', { class: 'preview-attrs' }, h('summary', { text: `All attributes (${p.attributes.length})` }),
      h('dl', { class: 'preview-list' }, p.attributes.map((a) => [h('dt', { text: a.name }), h('dd', { text: (a.values || []).join('; ') })]))));
  }
}

// ---- Recycle Bin -----------------------------------------------------------------------------

export function showBin() {
  const page = clear($('#page-bin'));
  const q = h('input', { type: 'search', id: 'bin-q', placeholder: 'Find a deleted object', spellcheck: 'false', 'aria-label': 'Find a deleted object' });
  page.append(pageHead('Recycle Bin', 'Deleted objects the AD Recycle Bin still holds. A restore brings an object back with its attributes and group memberships to its last known OU, through a plan.'),
    h('div', { class: 'toolbar' }, h('div', { class: 'search small-search' }, icon('search'), q)));
  const out = h('div', { class: 'bin-out' });
  page.append(out);
  const run = debounce(async () => {
    clear(out).append(h('div', { class: 'loading-line' }, spinner(), ' Searching the Recycle Bin'));
    try {
      const rows = (await call('Deleted', q.value.trim())) || [];
      clear(out);
      if (!rows.length) { out.append(emptyState('recycle', 'Nothing found', q.value ? 'No deleted object matches.' : 'The Recycle Bin is empty, or it is not enabled in this forest.')); return; }
      out.append(h('table', { class: 'grid simple', id: 'bin-table' }, h('thead', null, h('tr', null, ['Name', 'Kind', 'Last known location', 'Restores to', ''].map((t) => h('th', { text: t })))),
        h('tbody', null, rows.map((r) => h('tr', null,
          h('td', null, h('span', { class: 'cell-name' }, kindIcon(r.kind, 'kind ' + r.kind), h('span', { class: 'name', text: r.name }))),
          h('td', { text: r.kind }), h('td', { text: r.location }), h('td', { class: 'mono small', text: r.restoreTo || 'unknown' }),
          h('td', { class: 'right' }, h('button', { type: 'button', class: 'ghost small-btn', disabled: !r.restoreTo, onclick: () =>
            planNow('Restore ' + r.name, [{ kind: 'undelete', dn: r.dn, target: r.restoreTo }]) }, icon('recycle'), 'Restore')))))));
    } catch (e) { clear(out).append(h('p', { class: 'error', text: errText(e) })); }
  }, 200);
  q.addEventListener('input', run);
  run();
  q.focus();
}

// ---- Change records ---------------------------------------------------------------------------

const KINDS = [['', 'All kinds'], ['change', 'Changes'], ['sign-in', 'Sign-ins'], ['sign-in-failed', 'Failed sign-ins'], ['lock', 'Locks'], ['unlock', 'Unlocks'], ['sign-out', 'Sign-outs'], ['template', 'Templates']];

export async function showRecords() {
  const page = clear($('#page-records'));
  const text = h('input', { type: 'search', id: 'rec-q', placeholder: 'Account, object, ticket or note', spellcheck: 'false', 'aria-label': 'Filter records' });
  const kind = h('select', { id: 'rec-kind', 'aria-label': 'Kind' }, KINDS.map(([v, l]) => h('option', { value: v, text: l })));
  const days = h('select', { id: 'rec-days', 'aria-label': 'Period' }, [[0, 'Any time'], [1, 'Last 24 hours'], [7, 'Last 7 days'], [30, 'Last 30 days'], [90, 'Last 90 days']].map(([v, l]) => h('option', { value: String(v), text: l })));
  const verifyOut = h('div', { id: 'verify-out' });
  const verify = h('button', { type: 'button', class: 'ghost', id: 'verify-btn', onclick: async () => {
    clear(verifyOut).append(h('p', { class: 'muted small' }, spinner(), ' Checking every hash chain'));
    try {
      const probs = (await call('VerifyRecords')) || [];
      clear(verifyOut).append(probs.length
        ? h('div', { class: 'note crit' }, icon('alert'), h('div', null, h('strong', { text: plural(probs.length, 'problem') + ' in the change records' }),
          h('ul', null, probs.map((p) => h('li', { text: `${p.writer} ${p.file} line ${p.line}: ${p.reason}` })))))
        : h('p', { class: 'note ok', id: 'verify-ok' }, icon('verify'), h('span', { text: 'Chain intact: every record links to the one before it and no line was changed.' })));
    } catch (e) { clear(verifyOut).append(h('p', { class: 'error', text: errText(e) })); }
  } }, icon('verify'), 'Verify chain');
  let folder = '';
  try { folder = await call('RecordsFolder'); } catch { /* shown empty */ }
  page.append(pageHead('Change records', 'Append-only and hash-chained. Every sign-in, lock and change is recorded with the account, the domain controller and the device; passwords never are.', verify),
    verifyOut,
    h('div', { class: 'toolbar' }, h('div', { class: 'search small-search' }, icon('search'), text), kind, days, h('span', { class: 'grow' }), h('span', { class: 'muted small' }, 'Folder ', h('code', { class: 'path', text: folder }))));
  const out = h('div', { class: 'rec-out' });
  page.append(out);
  const run = debounce(async () => {
    out.classList.add('busy');
    try {
      const es = (await call('Records', { text: text.value.trim(), kind: kind.value, days: Number(days.value), limit: 500 })) || [];
      clear(out);
      if (!es.length) { out.append(emptyState('records', 'No records match', '')); return; }
      out.append(h('table', { class: 'grid simple records', id: 'rec-table' },
        h('thead', null, h('tr', null, ['Time', 'Kind', 'What', 'Who', 'Result'].map((t) => h('th', { text: t })))),
        h('tbody', null, es.map((e) => recordRows(e)))));
    } catch (e) { clear(out).append(h('p', { class: 'error', text: errText(e) })); } finally { out.classList.remove('busy'); }
  }, 200);
  text.addEventListener('input', run);
  kind.addEventListener('change', run);
  days.addEventListener('change', run);
  run();
}

function recordRows(e) {
  const what = e.note || e.change || (e.job ? `Plan ${e.result || 'ended'} (job ${e.job})` : e.error || '');
  const who = e.technician || e.requester || e.actor || '';
  const tone = e.kind === 'sign-in-failed' || e.result === 'failed' || e.result === 'stopped' ? 'crit' : e.kind === 'change' ? 'info' : 'dim';
  const detail = h('tr', { class: 'rec-detail', hidden: true }, h('td', { colspan: '5' }, h('dl', { class: 'facts grid-facts' },
    Object.entries(e).filter(([, v]) => v !== '' && v !== 0 && v !== null && !(Array.isArray(v) && !v.length)).map(([k, v]) => [h('dt', { text: k }), h('dd', { text: Array.isArray(v) ? v.join('; ') : String(v) })]))));
  const row = h('tr', { class: 'rec-row', tabindex: '0' },
    h('td', { class: 'nowrap', title: e.time, text: shortDate(e.time) }),
    h('td', null, badge(e.kind, tone)),
    h('td', null, h('div', { text: what }), e.dn ? h('div', { class: 'muted small', text: rdnName(e.dn) + (e.attribute ? ' · ' + e.attribute : '') }) : null),
    h('td', { text: who }),
    h('td', { text: e.result || (e.error ? 'error' : '') }));
  const toggle = () => { detail.hidden = !detail.hidden; row.classList.toggle('expanded', !detail.hidden); };
  row.addEventListener('click', toggle);
  row.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') toggle(); });
  return [row, detail];
}

