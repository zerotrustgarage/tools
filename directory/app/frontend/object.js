// The object page: one clean page per object instead of a tabbed property
// dialog. Fields edit inline and stage into the pending changes tray; account
// actions build a plan straight away. Everything is shown with textContent.
import {
  $, h, icon, kindIcon, clear, call, errText, state, nav, pick, ask, toast, badge, relTime, shortDate, rdnName, parentDN,
  spinner, store, copyText, KIND_LABEL, plural,
} from './core.js';
import { reviewPlan } from './plan.js';

const panel = () => $('#object');
let cur = null; // { dn, view }
let history = [];
let effective = null; // { dn, groups } once loaded
let loadSeq = 0;

export const currentObject = () => cur;

/** openObject shows dn in the object panel. push keeps a back step. */
export async function openObject(dn, { push = false, keepScroll = false, closeIfMissing = false } = {}) {
  if (push && cur && cur.dn !== dn) history.push(cur.dn);
  if (!push && (!cur || cur.dn !== dn)) history = [];
  const el = panel();
  el.hidden = false;
  document.body.classList.add('has-object');
  const same = cur && cur.dn.toLowerCase() === dn.toLowerCase();
  const scroll = keepScroll && same ? el.scrollTop : 0;
  if (!same) {
    effective = null;
    clear(el).append(h('div', { class: 'obj-loading' }, spinner(), ' Loading'));
  } else {
    el.classList.add('refreshing');
  }
  const mine = ++loadSeq;
  try {
    const view = await call('Object', dn);
    if (mine !== loadSeq) return;
    cur = { dn: view.dn, view };
    remember(view);
    render();
    el.scrollTop = scroll;
  } catch (e) {
    if (mine !== loadSeq) return;
    if (closeIfMissing) { closeObject(); toast('The object is no longer at ' + rdnName(dn) + '; it was moved, renamed or deleted.'); return; }
    clear(el).append(header(null), h('div', { class: 'obj-body' }, h('p', { class: 'error', role: 'alert', text: errText(e) }),
      h('button', { type: 'button', class: 'ghost', onclick: () => openObject(dn) }, icon('refresh'), 'Try again')));
  } finally {
    el.classList.remove('refreshing');
  }
  nav.markOpen && nav.markOpen(dn);
}

export function closeObject() {
  cur = null;
  history = [];
  panel().hidden = true;
  panel().classList.remove('wide');
  document.body.classList.remove('has-object', 'object-wide');
  nav.markOpen && nav.markOpen('');
}

export function rerender() { if (cur) render(); }

function remember(v) {
  const recent = store.get('recent', []).filter((r) => r.dn !== v.dn);
  recent.unshift({ dn: v.dn, name: v.name, kind: v.kind, sub: v.subtitle || '' });
  store.set('recent', recent.slice(0, 8));
}

const readOnly = (v) => !!v.privileged;

function render() {
  const v = cur.view;
  const el = clear(panel());
  el.append(header(v));
  const body = h('div', { class: 'obj-body' });
  if (v.privileged) {
    body.append(h('div', { class: 'note special', id: 'tier0-note' }, icon('shield'), h('div', null,
      h('strong', { text: 'Tier 0 object. ' }), h('span', { text: v.privileged + ' ' }),
      h('span', { text: 'This page is read-only. A change to a Tier 0 object goes through a plan with a second confirmation and should be made from a privileged access workstation or a domain controller.' }))));
  }
  if (v.rightsWarning) body.append(h('p', { class: 'note warn' }, icon('alert'), h('span', { text: v.rightsWarning })));

  const colA = h('div', { class: 'obj-col' });
  const colB = h('div', { class: 'obj-col' });
  for (const sec of v.sections || []) colA.append(sectionCard(v, sec));
  if (v.kind === 'group') colB.append(membersCard(v));
  if (['user', 'group', 'computer', 'contact'].includes(v.kind)) colB.append(groupsCard(v));
  if (v.kind === 'user' && (v.manager || (v.reports || []).length)) colB.append(orgCard(v));
  if (v.kind === 'user') colB.append(cloudCard(v));
  colB.append(timelineCard(v));
  colB.append(advancedCard(v));
  body.append(h('div', { class: 'obj-cols' }, colA, colB));
  el.append(body);
}

// ---- Header and actions ---------------------------------------------------------

function header(v) {
  const top = h('div', { class: 'obj-top' });
  const tools = h('div', { class: 'obj-tools' });
  if (history.length) tools.append(h('button', { type: 'button', class: 'icon-btn', title: 'Back', id: 'obj-back', onclick: () => openObject(history.pop()) }, icon('back')));
  tools.append(h('span', { class: 'grow' }));
  if (v) {
    tools.append(h('button', { type: 'button', class: 'icon-btn', title: 'Refresh', onclick: () => openObject(v.dn, { keepScroll: true }) }, icon('refresh')));
    tools.append(h('button', { type: 'button', class: 'icon-btn', title: 'Copy the distinguished name', onclick: async () => toast(await copyText(v.dn) ? 'Distinguished name copied' : 'Copy is not available here') }, icon('copy')));
    const wide = document.body.classList.contains('object-wide');
    tools.append(h('button', { type: 'button', class: 'icon-btn', id: 'obj-expand', title: wide ? 'Show results beside the page' : 'Use the full width', onclick: toggleWide }, icon(wide ? 'collapse' : 'expand')));
  }
  tools.append(h('button', { type: 'button', class: 'icon-btn', id: 'obj-close', title: 'Close (Esc)', onclick: closeObject }, icon('x')));
  top.append(tools);
  if (!v) return top;

  const parent = parentDN(v.dn);
  top.append(h('div', { class: 'obj-id' },
    h('div', { class: 'obj-avatar ' + v.kind }, kindIcon(v.kind)),
    h('div', { class: 'obj-titles' },
      h('h2', { class: 'obj-name', id: 'obj-name', text: v.name }),
      h('div', { class: 'obj-sub' }, h('span', { text: KIND_LABEL[v.kind] || 'Object' }), v.subtitle ? h('span', { class: 'sep', text: v.subtitle, id: 'obj-subtitle' }) : null),
      h('button', { type: 'button', class: 'crumb-btn', title: 'Browse this location', onclick: () => nav.browse(parent) }, icon('ou'), h('span', { text: v.location }))),
  ));
  if ((v.badges || []).length) top.append(h('div', { class: 'obj-badges', id: 'obj-badges' }, v.badges.map((b) => badge(b.label, b.tone))));
  if (!readOnly(v)) top.append(actionBar(v));
  return top;
}

function toggleWide() {
  document.body.classList.toggle('object-wide');
  render();
}

function act(id, iconName, label, fn, title) {
  return h('button', { type: 'button', class: 'act', id: 'act-' + id, title: title || label, onclick: fn }, icon(iconName), h('span', { text: label }));
}

function actionBar(v) {
  const bar = h('div', { class: 'obj-actions', role: 'toolbar', 'aria-label': 'Actions' });
  const one = (title, edits) => planNow(title, edits);
  if (v.kind === 'user') {
    bar.append(act('reset', 'key', 'Reset password', async () => {
      const fields = [{ id: 'must', type: 'checkbox', label: 'User must change the password at next logon', checked: true }];
      if (v.locked && v.canUnlock) fields.push({ id: 'unlock', type: 'checkbox', label: 'Also unlock the account', checked: true });
      const r = await ask({ title: 'Reset the password for ' + v.name, text: 'A random temporary password is generated and shown once after the plan runs. It is never recorded.', fields, ok: 'Review plan' });
      if (!r) return;
      const edits = [];
      if (r.unlock) edits.push({ kind: 'unlock', dn: v.dn });
      edits.push({ kind: 'reset-password', dn: v.dn, mustChange: r.must });
      one((r.unlock ? 'Unlock and reset the password for ' : 'Reset the password for ') + v.name, edits);
    }));
  }
  if (v.canUnlock) bar.append(act('unlock', 'unlock', 'Unlock', () => one('Unlock ' + v.name, [{ kind: 'unlock', dn: v.dn }])));
  if (v.canEnable && v.enabled !== undefined) {
    bar.append(v.enabled
      ? act('disable', 'power', 'Disable', () => one('Disable ' + v.name, [{ kind: 'disable', dn: v.dn }]))
      : act('enable', 'power', 'Enable', () => one('Enable ' + v.name, [{ kind: 'enable', dn: v.dn }])));
  }
  if (v.canExpire) bar.append(act('expires', 'calendar', 'End date', () => setEndDate(v, true)));
  if (['user', 'group', 'computer', 'contact'].includes(v.kind)) {
    bar.append(act('addgroup', 'group', 'Add to group', async () => {
      const g = await pick({ title: 'Add ' + v.name + ' to a group', cls: 'group', placeholder: 'Group name', exclude: (v.memberOf || []).map((r) => r.dn) });
      if (g) one(`Add ${v.name} to ${g.name}`, [{ kind: 'add-member', dn: g.dn, target: v.dn }]);
    }));
  }
  bar.append(act('move', 'move', 'Move', async () => {
    const ou = await pick({ title: 'Move ' + v.name + ' to', cls: 'container', placeholder: 'OU or container name', exclude: [parentDN(v.dn)] });
    const rdn = v.dn.slice(0, v.dn.length - parentDN(v.dn).length - 1);
    if (ou) planNow(`Move ${v.name} to ${ou.name}`, [{ kind: 'move', dn: v.dn, target: ou.dn }], (res) => { if (res.ok) openObject(rdn + ',' + ou.dn); });
  }));
  if (v.canRename) {
    bar.append(act('rename', 'pencil', 'Rename', async () => {
      const r = await ask({ title: 'Rename ' + v.name, fields: [{ id: 'name', label: 'New name', value: rdnName(v.dn) }], ok: 'Review plan' });
      if (!r || !r.name.trim()) return;
      const type = v.dn.slice(0, v.dn.indexOf('='));
      const next = `${type}=${escapeRDN(r.name.trim())},${parentDN(v.dn)}`;
      planNow(`Rename ${v.name}`, [{ kind: 'rename', dn: v.dn, target: r.name.trim() }], (res) => { if (res.ok) openObject(next); });
    }));
  }
  bar.append(act('delete', 'trash', 'Delete', () => one('Delete ' + v.name, [{ kind: 'delete', dn: v.dn }]),
    'Delete (restorable from the Recycle Bin while it is enabled)'));
  return bar;
}

/** escapeRDN escapes an attribute value for a DN (RFC 4514). */
function escapeRDN(v) {
  return v.replace(/[\\,+"<>;=]/g, (c) => '\\' + c).replace(/^([ #])/, '\\$1').replace(/ $/, '\\ ');
}

/** planNow builds a plan for edits and opens the review. */
export function planNow(title, edits, onApplied) {
  return reviewPlan((ticket) => call('PlanEdits', { title, ticket, edits }), { onApplied });
}

async function setEndDate(v, now) {
  const cur = (v.attributes || []).find((a) => a.attribute === 'accountexpires');
  const r = await ask({
    title: 'Account end date for ' + v.name,
    text: 'The account stops working at the end of this day (the domain sets accountExpires to the start of the next day). Leave it empty to remove the end date.',
    fields: [{ id: 'date', type: 'date', label: 'Last working day' }], ok: now ? 'Review plan' : 'Stage',
  });
  if (!r) return;
  if (now) planNow(r.date ? `Set the end date for ${v.name} to ${r.date}` : `Remove the end date for ${v.name}`, [{ kind: 'expires', dn: v.dn, date: r.date }]);
  else stage(`${v.dn}|expires`, { kind: 'expires', dn: v.dn, date: r.date }, { objName: v.name, label: 'Account expires', before: cur ? cur.display : [], after: [r.date || 'Never'] });
}

// ---- Staging ---------------------------------------------------------------------

function stage(key, edit, info) {
  state.pending.set(key, { edit, ...info });
  nav.pendingChanged();
  toast(`Staged: ${info.label} on ${info.objName}`, 'ok');
}
function unstage(key) {
  state.pending.delete(key);
  nav.pendingChanged();
}

// ---- Sections and inline editing -----------------------------------------------------

function sectionCard(v, sec) {
  const card = h('section', { class: 'card' }, h('h3', { text: sec.title }));
  const filled = [], empty = [];
  for (const f of sec.fields) {
    const key = fieldKey(v, f);
    ((f.values || []).length || state.pending.has(key) ? filled : empty).push(f);
  }
  const list = h('div', { class: 'fields' });
  for (const f of filled) list.append(fieldRow(v, f));
  card.append(list);
  // Empty fields wait behind one link, so the page shows what the object has.
  if (empty.length) {
    const more = h('div', { class: 'fields', hidden: true });
    for (const f of empty) more.append(fieldRow(v, f));
    const label = () => `Show ${plural(empty.length, 'empty field')}`;
    const btn = h('button', { type: 'button', class: 'link-btn more-fields', onclick: () => { more.hidden = !more.hidden; btn.textContent = more.hidden ? label() : 'Hide empty fields'; } }, label());
    if (!filled.length) list.append(h('p', { class: 'muted small', text: 'No values set' }));
    card.append(more, btn);
  }
  return card;
}

const fieldKey = (v, f) => (f.kind === 'filetime' && f.attribute.toLowerCase() === 'accountexpires' ? `${v.dn}|expires` : `${v.dn}|set|${f.attribute.toLowerCase()}`);

function fieldRow(v, f) {
  const key = fieldKey(v, f);
  const pend = state.pending.get(key);
  const canEdit = f.editable && !readOnly(v);
  const label = h('div', { class: 'f-label' }, h('span', { text: f.label }));
  if (f.synced) label.append(h('span', { class: 'synced', title: 'Synchronizes to Microsoft 365 for synced users' }, icon('cloud', 'small')));
  if (f.help) label.append(h('span', { class: 'help', title: f.help }, icon('info', 'small')));
  const value = h('div', { class: 'f-value' + (canEdit ? ' editable' : '') });
  if (pend) {
    value.classList.add('pending');
    value.append(h('div', { class: 'pend-old', text: (pend.before || []).join(', ') || 'not set' }),
      h('div', { class: 'pend-new' }, h('span', { text: (pend.after || []).join(', ') || 'cleared' }), badge('Pending', 'warn'),
        h('button', { type: 'button', class: 'link-btn', title: 'Remove this staged change', onclick: (e) => { e.stopPropagation(); unstage(key); } }, 'Undo')));
  } else if ((f.display || []).length) {
    if (f.kind === 'multi') value.append(h('ul', { class: 'multi' }, f.display.map((d) => h('li', { text: d }))));
    else if (f.kind === 'dn' && f.values[0]) {
      value.append(h('button', { type: 'button', class: 'link-btn', text: f.display[0], title: 'Open', onclick: (e) => { e.stopPropagation(); openObject(f.values[0], { push: true }); } }));
    } else value.append(h('span', { text: f.display.join(', ') }));
  } else {
    value.append(h('span', { class: 'unset', text: 'Not set' }));
  }
  const row = h('div', { class: 'field', dataset: { attr: f.attribute } }, label, value);
  if (canEdit) {
    value.tabIndex = 0;
    value.setAttribute('role', 'button');
    value.title = 'Edit ' + f.label;
    value.append(icon('pencil', 'edit-hint'));
    const start = (e) => { if (e.target.closest('.link-btn')) return; startEdit(v, f, row); };
    value.addEventListener('click', start);
    value.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === 'F2') { e.preventDefault(); start(e); } });
  }
  return row;
}

function startEdit(v, f, row) {
  const key = fieldKey(v, f);
  const pend = state.pending.get(key);
  const now = pend ? pend.edit.values || [] : f.values || [];
  if (f.kind === 'dn') return editDN(v, f);
  if (f.kind === 'filetime') return setEndDate(v, false);
  if (f.kind === 'multi') return editMulti(v, f, row, now);
  const input = h('input', { class: 'inline-input', value: now[0] || '', spellcheck: 'false', 'aria-label': f.label });
  const value = row.querySelector('.f-value');
  const editor = h('div', { class: 'f-value editing' }, input, h('span', { class: 'kbd-hint', text: 'Enter to stage, Esc to cancel' }));
  value.replaceWith(editor);
  input.focus();
  input.select();
  let done = false;
  const finish = (commit) => {
    if (done) return;
    done = true;
    const next = input.value.trim();
    if (commit && next !== (f.values[0] || '').trim()) {
      stage(key, { kind: 'set', dn: v.dn, attribute: f.attribute, values: next ? [next] : [] }, { objName: v.name, label: f.label, before: f.display || [], after: next ? [next] : [] });
    } else if (commit && pend) {
      unstage(key); // edited back to the current value
    } else {
      editor.replaceWith(fieldRow(v, f));
    }
  };
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); finish(true); }
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); finish(false); }
  });
  input.addEventListener('blur', () => finish(input.value.trim() !== (now[0] || '').trim()));
}

function editMulti(v, f, row, now) {
  const key = fieldKey(v, f);
  let values = [...now];
  const list = h('ul', { class: 'multi-edit' });
  const add = h('input', { class: 'inline-input', placeholder: 'Add a value, then Enter', spellcheck: 'false', 'aria-label': 'Add to ' + f.label });
  const draw = () => {
    clear(list);
    values.forEach((val, i) => list.append(h('li', null, h('span', { text: val }),
      h('button', { type: 'button', class: 'icon-btn tiny', title: 'Remove ' + val, onclick: () => { values.splice(i, 1); draw(); } }, icon('x')))));
  };
  draw();
  add.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      const val = add.value.trim();
      if (val && !values.includes(val)) values.push(val);
      add.value = '';
      draw();
    }
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); editor.replaceWith(fieldRow(v, f)); }
  });
  const editor = h('div', { class: 'f-value editing' }, list, add, h('div', { class: 'inline-actions' },
    h('button', { type: 'button', class: 'ghost small-btn', onclick: () => editor.replaceWith(fieldRow(v, f)) }, 'Cancel'),
    h('button', { type: 'button', class: 'primary small-btn', onclick: () => {
      const pendingAdd = add.value.trim();
      if (pendingAdd && !values.includes(pendingAdd)) values.push(pendingAdd);
      const same = values.length === f.values.length && values.every((x) => f.values.includes(x));
      if (same) { state.pending.has(key) ? unstage(key) : editor.replaceWith(fieldRow(v, f)); return; }
      stage(key, { kind: 'set', dn: v.dn, attribute: f.attribute, values }, { objName: v.name, label: f.label, before: f.display || [], after: values });
    } }, 'Stage')));
  row.querySelector('.f-value').replaceWith(editor);
  add.focus();
}

async function editDN(v, f) {
  const r = await pick({ title: f.label + ' for ' + v.name, cls: 'user', placeholder: 'Find a person', allowClear: (f.values || []).length > 0, exclude: [v.dn] });
  if (!r) return;
  const key = fieldKey(v, f);
  const values = r.clear ? [] : [r.dn];
  stage(key, { kind: 'set', dn: v.dn, attribute: f.attribute, values }, { objName: v.name, label: f.label, before: f.display || [], after: r.clear ? [] : [r.name] });
}

// ---- Relationship cards ------------------------------------------------------------

function refRow(ref, { remove, pendingTag, via } = {}) {
  const row = h('li', { class: 'ref' + (pendingTag === 'removed' ? ' removed' : '') },
    h('button', { type: 'button', class: 'ref-link', onclick: () => openObject(ref.dn, { push: true }) }, ref.kind ? kindIcon(ref.kind, 'small') : h('span', { class: 'ref-dot', 'aria-hidden': 'true' }), h('span', { text: ref.name })));
  if (via) row.append(badge(via === 'direct' ? 'Direct' : via === 'primary' ? 'Primary group' : 'Nested', via === 'nested' ? 'info' : 'dim'));
  if (pendingTag) row.append(badge(pendingTag === 'added' ? 'Pending add' : 'Pending removal', 'warn'));
  if (remove) row.append(remove);
  return row;
}

function memberEdits(groupDN, memberDN) {
  return {
    add: state.pending.get(`${groupDN}|add-member|${memberDN}`.toLowerCase()),
    remove: state.pending.get(`${groupDN}|remove-member|${memberDN}`.toLowerCase()),
  };
}
function stageMember(kind, group, member, objName) {
  const key = `${group.dn}|${kind}|${member.dn}`.toLowerCase();
  const opposite = `${group.dn}|${kind === 'add-member' ? 'remove-member' : 'add-member'}|${member.dn}`.toLowerCase();
  if (state.pending.has(opposite)) return unstage(opposite);
  stage(key, { kind, dn: group.dn, target: member.dn }, {
    objName, label: kind === 'add-member' ? `Add ${member.name} to ${group.name}` : `Remove ${member.name} from ${group.name}`,
    before: [], after: [], member: true, group: group.dn, target: member.dn, targetName: member.name, groupName: group.name,
  });
}

function pendingMembers(match) {
  return [...state.pending.values()].filter((p) => p.member && match(p));
}

function groupsCard(v) {
  const ro = readOnly(v);
  const card = h('section', { class: 'card', id: 'card-groups' }, h('div', { class: 'card-head' }, h('h3', { text: 'Member of' }), h('span', { class: 'count', text: String((v.memberOf || []).length) })));
  const self = { dn: v.dn, name: v.name };
  const list = h('ul', { class: 'refs' });
  for (const g of v.memberOf || []) {
    const p = memberEdits(g.dn, v.dn);
    const rm = ro ? null : h('button', { type: 'button', class: 'icon-btn tiny', title: p.remove ? 'Keep membership' : 'Remove from ' + g.name, onclick: () => stageMember('remove-member', g, self, v.name) }, icon(p.remove ? 'refresh' : 'x'));
    list.append(refRow(g, { remove: rm, pendingTag: p.remove ? 'removed' : null }));
  }
  for (const p of pendingMembers((p) => p.edit.kind === 'add-member' && p.target.toLowerCase() === v.dn.toLowerCase())) {
    list.append(refRow({ dn: p.group, name: p.groupName, kind: 'group' }, { pendingTag: 'added',
      remove: h('button', { type: 'button', class: 'icon-btn tiny', title: 'Undo', onclick: () => unstage(`${p.group}|add-member|${p.target}`.toLowerCase()) }, icon('x')) }));
  }
  if (!list.childNodes.length) list.append(h('li', { class: 'muted small', text: 'No direct group memberships' + (v.kind === 'user' ? ' (the primary group is not listed here)' : '') }));
  card.append(list);
  const tools = h('div', { class: 'card-tools' });
  if (!ro) {
    tools.append(h('button', { type: 'button', class: 'ghost small-btn', id: 'groups-add', onclick: async () => {
      const g = await pick({ title: 'Add ' + v.name + ' to a group', cls: 'group', placeholder: 'Group name', exclude: (v.memberOf || []).map((r) => r.dn) });
      if (g) stageMember('add-member', g, self, v.name);
    } }, icon('plus'), 'Add to group'));
  }
  const effBox = h('div', { class: 'effective' });
  const effBtn = h('button', { type: 'button', class: 'ghost small-btn', id: 'groups-effective', onclick: async () => {
    if (effective && effective.dn === v.dn) { effective = null; render(); return; }
    effBtn.disabled = true;
    clear(effBox).append(h('div', { class: 'muted small' }, spinner(), ' Expanding nested groups'));
    try {
      effective = { dn: v.dn, groups: (await call('EffectiveGroups', v.dn)) || [] };
      render();
    } catch (e) { clear(effBox).append(h('p', { class: 'error small', text: errText(e) })); effBtn.disabled = false; }
  } }, icon('tree'), effective && effective.dn === v.dn ? 'Hide effective groups' : 'Show effective groups');
  tools.append(effBtn);
  card.append(tools, effBox);
  if (effective && effective.dn === v.dn) {
    effBox.append(h('div', { class: 'sub-head', text: `Effective groups (${effective.groups.length}), nesting included` }),
      h('ul', { class: 'refs', id: 'effective-list' }, effective.groups.map((g) => refRow(g, { via: g.via }))));
  }
  return card;
}

function membersCard(v) {
  const can = v.canMembers && !readOnly(v);
  const group = { dn: v.dn, name: v.name };
  const card = h('section', { class: 'card', id: 'card-members' }, h('div', { class: 'card-head' }, h('h3', { text: 'Members' }), h('span', { class: 'count', text: String((v.members || []).length) })));
  const filter = h('input', { class: 'filter-input', type: 'search', placeholder: 'Filter members', 'aria-label': 'Filter members' });
  const list = h('ul', { class: 'refs' });
  const draw = () => {
    clear(list);
    const f = filter.value.trim().toLowerCase();
    for (const m of v.members || []) {
      if (f && !m.name.toLowerCase().includes(f)) continue;
      const p = memberEdits(v.dn, m.dn);
      const rm = can ? h('button', { type: 'button', class: 'icon-btn tiny', title: p.remove ? 'Keep member' : 'Remove ' + m.name, onclick: () => stageMember('remove-member', group, m, v.name) }, icon(p.remove ? 'refresh' : 'x')) : null;
      list.append(refRow(m, { remove: rm, pendingTag: p.remove ? 'removed' : null }));
    }
    for (const p of pendingMembers((p) => p.edit.kind === 'add-member' && p.group.toLowerCase() === v.dn.toLowerCase())) {
      list.append(refRow({ dn: p.target, name: p.targetName }, { pendingTag: 'added',
        remove: h('button', { type: 'button', class: 'icon-btn tiny', title: 'Undo', onclick: () => unstage(`${p.group}|add-member|${p.target}`.toLowerCase()) }, icon('x')) }));
    }
    if (!list.childNodes.length) list.append(h('li', { class: 'muted small', text: f ? 'No members match' : 'This group has no members' }));
  };
  filter.addEventListener('input', draw);
  draw();
  if ((v.members || []).length > 8) card.append(filter);
  card.append(list);
  if (can) {
    card.append(h('div', { class: 'card-tools' }, h('button', { type: 'button', class: 'ghost small-btn', id: 'members-add', onclick: async () => {
      const m = await pick({ title: 'Add a member to ' + v.name, cls: '', placeholder: 'Person, group, computer or contact', exclude: [v.dn, ...(v.members || []).map((r) => r.dn)] });
      if (m) stageMember('add-member', group, m, v.name);
    } }, icon('plus'), 'Add member')));
  } else if (!readOnly(v)) {
    card.append(h('p', { class: 'muted small', text: 'Your account cannot change the members of this group.' }));
  }
  return card;
}

function orgCard(v) {
  const card = h('section', { class: 'card', id: 'card-org' }, h('h3', { text: 'Reporting line' }));
  if (v.manager) card.append(h('div', { class: 'sub-head', text: 'Manager' }), h('ul', { class: 'refs' }, refRow(v.manager)));
  if ((v.reports || []).length) card.append(h('div', { class: 'sub-head', text: `Direct reports (${v.reports.length})` }), h('ul', { class: 'refs' }, v.reports.map((r) => refRow(r))));
  return card;
}

function cloudCard(v) {
  const card = h('section', { class: 'card', id: 'card-cloud' }, h('div', { class: 'card-head' }, h('h3', null, icon('cloud'), ' Microsoft 365')));
  const list = h('ul', { class: 'checks' });
  for (const c of v.readiness || []) {
    list.append(h('li', { class: 'check-row ' + c.status }, icon(c.status === 'ok' ? 'check' : c.status === 'warn' ? 'alert' : 'info'),
      h('div', null, h('div', { class: 'check-label', text: c.label }), h('div', { class: 'muted small', text: c.detail }))));
  }
  card.append(list, h('p', { class: 'muted small cloud-note', text: v.cloudNote }));
  return card;
}

function timelineCard(v) {
  const card = h('section', { class: 'card', id: 'card-timeline' }, h('h3', { text: 'Timeline' }));
  const items = v.timeline || [];
  if (!items.length) { card.append(h('p', { class: 'muted small', text: 'No dates recorded' })); return card; }
  card.append(h('ol', { class: 'timeline' }, items.map((e) => h('li', { class: e.kind },
    h('span', { class: 'tl-dot', 'aria-hidden': 'true' }),
    h('div', null, h('div', { text: e.label }), h('div', { class: 'muted small', text: `${shortDate(e.time)} · ${relTime(e.time)}${e.kind === 'record' ? ' · change record' : ''}` }))))));
  return card;
}

function advancedCard(v) {
  const attrs = v.attributes || [];
  const det = h('details', { class: 'card advanced', id: 'card-advanced' });
  const filter = h('input', { class: 'filter-input', type: 'search', placeholder: 'Filter attributes', 'aria-label': 'Filter attributes' });
  const body = h('div', { class: 'attr-table' });
  const draw = () => {
    clear(body);
    const f = filter.value.trim().toLowerCase();
    for (const a of attrs) {
      const text = (a.display || []).join('\n');
      if (f && !a.attribute.toLowerCase().includes(f) && !text.toLowerCase().includes(f)) continue;
      body.append(h('div', { class: 'attr-row' }, h('code', { class: 'attr-name', text: a.attribute }), h('div', { class: 'attr-val', text: text })));
    }
    if (!body.childNodes.length) body.append(h('p', { class: 'muted small', text: 'No attributes match' }));
  };
  filter.addEventListener('input', draw);
  det.append(h('summary', null, h('h3', { text: 'Advanced' }), h('span', { class: 'count', text: `${attrs.length} attributes` })), filter, body);
  det.addEventListener('toggle', () => { if (det.open && !body.childNodes.length) draw(); });
  return det;
}
