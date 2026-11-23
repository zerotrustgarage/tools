// ZTG Directory console. The window binds console.API (Wails:
// window.go.console.API); every call returns a Promise. Directory values are
// inserted with textContent only.
import {
  $, $$, h, icon, kindIcon, clear, call, errText, debounce, relTime, plural, state, nav, toast, badge, store, onCallError,
  pick, ask, rdnName, parentDN, spinner, emptyState,
} from './core.js';
import { openObject, closeObject, currentObject, rerender, planNow } from './object.js';
import { reviewPlan } from './plan.js';
import { showNew, showBin, showRecords } from './pages.js';

// ---- Theme ------------------------------------------------------------------------

const THEMES = ['system', 'light', 'dark'];
function applyTheme(t) {
  if (t === 'system') delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = t;
  const btn = $('#theme-btn');
  btn.title = 'Theme: ' + t;
  clear(btn).append(icon(t === 'light' ? 'sun' : t === 'dark' ? 'moon' : 'monitor'));
  try {
    const rt = window.runtime;
    if (rt) (t === 'light' ? rt.WindowSetLightTheme : t === 'dark' ? rt.WindowSetDarkTheme : rt.WindowSetSystemDefaultTheme)();
  } catch { /* outside the desktop window */ }
}
applyTheme(store.get('theme', 'system'));
$('#theme-btn').addEventListener('click', () => {
  const next = THEMES[(THEMES.indexOf(store.get('theme', 'system')) + 1) % THEMES.length];
  store.set('theme', next);
  applyTheme(next);
});

// ---- Session and screens ------------------------------------------------------------

function show(screen) {
  $('#boot').hidden = true;
  $('#auth').hidden = screen !== 'auth';
  $('#shell').hidden = screen !== 'shell';
}

async function refreshStatus() {
  try {
    const st = await call('Status');
    applyStatus(st);
  } catch { /* keep the current screen; errors are shown where they happen */ }
}

let shellReady = false;
function applyStatus(st) {
  const was = state.status;
  state.status = st;
  if (st.mode === 'setup') return showSetup(st);
  if (!st.signedIn) {
    shellReady = false;
    closeObject();
    $('#lock').hidden = true;
    return showSignIn(st);
  }
  if (!shellReady) enterShell(st);
  else fillTop(st);
  if (st.locked) showLock(st);
  else $('#lock').hidden = true;
  if (was && was.locked && !st.locked) nav.reload();
}

onCallError(() => statusSoon());
const statusSoon = debounce(refreshStatus, 300);
setInterval(refreshStatus, 30000);

// ---- Setup ------------------------------------------------------------------------------

function authFrame(st) {
  show('auth');
  $('#auth-domain').textContent = st.mode === 'setup' ? 'First-run setup' : st.domain || '';
  $('#auth-demo').hidden = st.mode !== 'demo';
  $('#auth-foot').textContent = [st.version && 'Version ' + st.version, st.device && 'Device ' + st.device].filter(Boolean).join(' · ');
}

function showSetup(st) {
  authFrame(st);
  $('#setup').hidden = false;
  $('#signin').hidden = true;
  $('#setup-hint').textContent = st.hint || '';
  syncSetup();
}

function syncSetup() {
  const mfa = $('input[name=mfa]:checked').value;
  $('#s-radius').hidden = mfa === '';
  $('#s-duo-help').hidden = mfa !== 'duo';
  $('#s-none').hidden = mfa !== '';
  $('#s-ntlm-warn').hidden = !$('#s-ntlm').checked;
  $('#setup-btn').disabled = mfa === '' && !$('#s-pwonly').checked;
}
for (const el of $$('#setup-form input')) el.addEventListener('change', () => { $('#setup-err').hidden = true; syncSetup(); });

$('#setup-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const err = $('#setup-err');
  err.hidden = true;
  const mfa = $('input[name=mfa]:checked').value;
  const btn = $('#setup-btn');
  btn.disabled = true;
  try {
    const st = await call('Setup', {
      domain: $('#s-domain').value.trim(), server: $('#s-server').value.trim(), baseDN: $('#s-basedn').value.trim(),
      ldaps: $('#s-ldaps').checked, allowNTLM: $('#s-ntlm').checked, secondFactor: mfa, allowPasswordOnly: mfa === '' && $('#s-pwonly').checked,
      radiusServer: mfa ? $('#s-radius-server').value.trim() : '', radiusSecret: mfa ? $('#s-radius-secret').value : '',
      usernameFormat: $('#s-userfmt').value, operatorsGroup: $('#s-ops').value.trim(), approversGroup: $('#s-appr').value.trim(),
      idleLockMinutes: Number($('#s-idle').value) || 15, notice: $('#s-notice').value,
    });
    $('#s-radius-secret').value = '';
    applyStatus(st);
  } catch (e2) {
    err.textContent = errText(e2);
    err.hidden = false;
  } finally { syncSetup(); }
});

// ---- Sign-in ----------------------------------------------------------------------------

function showSignIn(st) {
  authFrame(st);
  $('#setup').hidden = true;
  $('#signin').hidden = false;
  const demo = st.mode === 'demo';
  $('#signin-demo').hidden = !demo;
  $('#signin-demo').textContent = demo ? 'Demo mode: a fictional directory held in memory. Changes are recorded but never reach a domain controller.' : '';
  const notice = (st.notice || '').trim();
  $('#notice-box').hidden = !notice;
  $('#notice-text').textContent = notice;
  $('#notice-ack').checked = false;
  $('#factor-box').hidden = !st.secondFactor;
  $('#signin-hint').textContent = st.hint || (st.passwordOnly ? 'This domain signs in with a password only; each sign-in is recorded as an open IA.L2-3.5.3 item.' : '');
  $('#signin-err').hidden = true;
  $('#password').value = '';
  syncSignIn();
  (notice ? $('#notice-ack') : $('#user')).focus();
}

function syncSignIn() {
  const ack = $('#notice-box').hidden || $('#notice-ack').checked;
  for (const el of $$('#signin-form input, #signin-form button')) el.disabled = !ack;
  $('#signin-form').classList.toggle('disabled', !ack);
  const factor = ($('input[name=factor]:checked') || {}).value;
  $('#passcode').hidden = factor !== 'passcode';
}
$('#notice-ack').addEventListener('change', () => { syncSignIn(); if ($('#notice-ack').checked) $('#user').focus(); });
for (const r of $$('input[name=factor]')) r.addEventListener('change', syncSignIn);

$('#signin-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const st = state.status || {};
  const err = $('#signin-err');
  err.hidden = true;
  let factor = '';
  if (st.secondFactor) {
    factor = $('input[name=factor]:checked').value;
    if (factor === 'passcode') factor = $('#passcode').value.trim();
    if (!factor) { err.textContent = 'Enter the passcode.'; err.hidden = false; return; }
  }
  const wait = $('#signin-wait');
  $('#signin-wait-text').textContent = !st.secondFactor ? 'Checking your account with a domain controller'
    : factor === 'push' ? 'Approve the request on your device' : factor === 'phone' ? 'Answer the call and follow the prompts' : 'Checking the passcode';
  wait.hidden = false;
  $('#signin-btn').disabled = true;
  try {
    const next = await call('SignIn', { user: $('#user').value.trim(), domain: '', password: $('#password').value, factor });
    $('#password').value = '';
    $('#passcode').value = '';
    applyStatus(next);
  } catch (e2) {
    err.textContent = errText(e2);
    err.hidden = false;
    $('#password').select();
  } finally {
    wait.hidden = true;
    $('#signin-btn').disabled = false;
  }
});

// ---- Shell ---------------------------------------------------------------------------------

function initials(name) {
  return (name || '?').split(/[\s.\\]+/).filter(Boolean).slice(-2).map((p) => p[0].toUpperCase()).join('');
}

function fillTop(st) {
  $('#demo-badge').hidden = st.mode !== 'demo';
  $('#mfa-chip').hidden = !st.passwordOnly;
  $('#dc-name').textContent = st.dc || 'no domain controller';
  $('#account-name').textContent = st.displayName || st.account;
  $('#account-roles').textContent = (st.roles || []).map((r) => r[0].toUpperCase() + r.slice(1)).join(', ');
  $('#avatar').textContent = initials(st.displayName || st.account);
  $('#tree-domain').textContent = st.domain || '';
  const facts = clear($('#account-facts'));
  for (const [k, v] of [['Account', st.account], ['Roles', (st.roles || []).join(', ')], ['Sign-in method', st.method + (st.passwordOnly ? ' (no MFA)' : '')],
    ['Domain controller', st.dc], ['Idle lock', plural(st.idleMinutes, 'minute')], ['Device', st.device], ['Version', st.version]]) {
    facts.append(h('dt', { text: k }), h('dd', { text: v || '' }));
  }
}

function enterShell(st) {
  shellReady = true;
  show('shell');
  fillTop(st);
  state.pending.clear();
  pendingChanged();
  loadTree();
  setPage('results');
  query = { text: '', cls: '', view: null, under: null, oneLevel: false };
  $('#q').value = '';
  drawChips();
  runQuery();
}

// Account menu
$('#account-btn').addEventListener('click', (e) => {
  e.stopPropagation();
  const m = $('#account-menu');
  m.hidden = !m.hidden;
  $('#account-btn').setAttribute('aria-expanded', String(!m.hidden));
});
document.addEventListener('click', (e) => {
  if (!e.target.closest('.account')) { $('#account-menu').hidden = true; $('#account-btn').setAttribute('aria-expanded', 'false'); }
});
$('#updates-btn').addEventListener('click', async () => {
  const out = $('#updates-out');
  out.hidden = false;
  clear(out).append(spinner(), ' Checking');
  try { out.textContent = await call('CheckUpdates'); } catch (e) { out.textContent = errText(e); }
});
$('#signout-btn').addEventListener('click', signOut);
async function signOut() {
  if (state.pending.size && !(await ask({ title: 'Sign out?', text: `${plural(state.pending.size, 'staged change')} will be discarded. Nothing has been written.`, ok: 'Sign out', okClass: 'danger' }))) return;
  $('#account-menu').hidden = true;
  state.pending.clear();
  pendingChanged();
  applyStatus(await call('SignOut'));
}
$('#lock-btn').addEventListener('click', async () => applyStatus(await call('Lock')));

// ---- Lock -------------------------------------------------------------------------------------

function showLock(st) {
  const lock = $('#lock');
  if (!lock.hidden) return;
  for (const d of $$('dialog[open]')) d.close('cancel');
  $('#lock-account').textContent = st.account;
  $('#lock-err').hidden = true;
  $('#lock-pw').value = '';
  lock.hidden = false;
  $('#lock-pw').focus();
}
$('#lock-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const err = $('#lock-err');
  err.hidden = true;
  try {
    const st = await call('Unlock', $('#lock-pw').value);
    $('#lock-pw').value = '';
    applyStatus(st);
  } catch (e2) {
    err.textContent = errText(e2);
    err.hidden = false;
  }
});
$('#lock-signout').addEventListener('click', async () => {
  state.pending.clear();
  pendingChanged();
  applyStatus(await call('SignOut'));
});

// ---- Pages ----------------------------------------------------------------------------------

let page = 'results';
function setPage(p) {
  page = p;
  for (const id of ['results', 'new', 'bin', 'records']) $('#page-' + id).hidden = id !== p;
  markNav();
  if (p === 'new') showNew();
  if (p === 'bin') showBin();
  if (p === 'records') showRecords();
}
// The other pages use the full width, so the object panel closes.
for (const b of $$('#nav-main [data-page]')) b.addEventListener('click', () => { closeObject(); setPage(b.dataset.page); });
$('#new-btn').addEventListener('click', () => { closeObject(); setPage('new'); });

// ---- Queries ----------------------------------------------------------------------------------

const VIEWS = [
  { key: 'locked', label: 'Locked out', icon: 'lock', q: { cls: 'user', filters: ['locked'] } },
  { key: 'disabled', label: 'Disabled', icon: 'power', q: { cls: '', filters: ['disabled'] } },
  { key: 'never', label: 'Never logged on', icon: 'sleep', q: { cls: 'user', filters: ['never-logged-on'] } },
  { key: 'inactive', label: 'Inactive 90 days', icon: 'clock', q: { cls: 'user', inactiveDays: 90 } },
  { key: 'created', label: 'Created last 7 days', icon: 'new', q: { cls: '', createdDays: 7 } },
  { key: 'mustchange', label: 'Must change password', icon: 'key', q: { cls: 'user', filters: ['must-change-password'] } },
  { key: 'neverexpires', label: 'Password never expires', icon: 'infinity', q: { cls: 'user', filters: ['password-never-expires'] } },
  { key: 'nomanager', label: 'No manager', icon: 'user', q: { cls: 'user', filters: ['no-manager'] } },
  { key: 'emptygroups', label: 'Empty groups', icon: 'empty', q: { cls: 'group', filters: ['empty-group'] } },
];
const CLASSES = [['', 'All'], ['user', 'Users'], ['group', 'Groups'], ['computer', 'Computers'], ['contact', 'Contacts'], ['container', 'OUs']];

let query = { text: '', cls: '', view: null, under: null, oneLevel: false };
let rows = [];
let sort = { key: null, dir: 1 };
let cursor = -1;
const selected = new Set();
let seq = 0;

const navViews = $('#nav-views');
for (const v of VIEWS) {
  navViews.append(h('li', null, h('button', { type: 'button', dataset: { view: v.key }, onclick: () => setView(v.key) }, icon(v.icon), h('span', { text: v.label }),
    h('span', { class: 'nav-count', dataset: { count: v.key } }))));
}
$('#nav-main [data-view=home]').addEventListener('click', () => goHome());

function goHome() {
  $('#q').value = '';
  query = { text: '', cls: '', view: null, under: null, oneLevel: false };
  setPage('results');
  drawChips();
  runQuery();
}

function setView(key) {
  const v = VIEWS.find((x) => x.key === key);
  $('#q').value = '';
  query = { text: '', cls: v.q.cls, view: key, under: null, oneLevel: false };
  setPage('results');
  drawChips();
  runQuery();
}

function browse(dn) {
  if (!dn) return;
  $('#q').value = '';
  query = { text: '', cls: '', view: null, under: dn, oneLevel: true };
  setPage('results');
  drawChips();
  runQuery();
  expandTreeTo(dn);
}

function isHome() { return !query.text && !query.view && !query.under; }

function drawChips() {
  const chips = clear($('#class-chips'));
  for (const [cls, label] of CLASSES) {
    chips.append(h('button', { type: 'button', class: 'chip-btn' + (query.cls === cls ? ' on' : ''), 'aria-pressed': String(query.cls === cls), dataset: { cls },
      onclick: () => { query.cls = cls; drawChips(); runQuery(); } }, label));
  }
  const scope = clear($('#scope-chips'));
  if (query.view) {
    const v = VIEWS.find((x) => x.key === query.view);
    scope.append(h('span', { class: 'scope' }, icon(v.icon, 'small'), v.label, h('button', { type: 'button', class: 'icon-btn tiny', title: 'Remove this filter', onclick: () => { query.view = null; drawChips(); runQuery(); } }, icon('x'))));
  }
  if (query.under && query.text) {
    scope.append(h('span', { class: 'scope' }, icon('ou', 'small'), 'In ' + rdnName(query.under), h('button', { type: 'button', class: 'icon-btn tiny', title: 'Search everywhere', onclick: () => { query.under = null; drawChips(); runQuery(); } }, icon('x'))));
  }
  $('#class-chips').hidden = isHome();
}

function request() {
  const v = query.view ? VIEWS.find((x) => x.key === query.view).q : {};
  return { text: query.text, class: query.cls, filters: v.filters || [], under: query.under || '', oneLevel: !!(query.oneLevel && !query.text),
    createdDays: v.createdDays || 0, inactiveDays: v.inactiveDays || 0, limit: 500, includeHidden: false };
}

function heading() {
  const crumbs = clear($('#results-crumbs'));
  if (query.view) return VIEWS.find((x) => x.key === query.view).label;
  if (query.under && !query.text) {
    // Breadcrumb from the domain root down to the browsed OU.
    const chain = [];
    const base = ((state.status && state.status.baseDn) || '').toLowerCase();
    for (let dn = query.under; dn && dn.toLowerCase() !== base && /^(ou|cn)=/i.test(dn); dn = parentDN(dn)) chain.unshift(dn);
    crumbs.append(h('button', { type: 'button', class: 'crumb', onclick: () => goHome() }, state.status.domain || 'Domain'));
    chain.slice(0, -1).forEach((dn) => crumbs.append(icon('chevron', 'small'), h('button', { type: 'button', class: 'crumb', onclick: () => browse(dn) }, rdnName(dn))));
    return rdnName(query.under);
  }
  if (query.text) return 'Search';
  return 'Overview';
}

async function runQuery() {
  const mine = ++seq;
  $('#results-title').textContent = heading();
  markNav();
  selected.clear();
  drawBulk();
  if (isHome()) {
    $('#grid-wrap').hidden = true;
    $('#home').hidden = false;
    $('#results-meta').textContent = '';
    drawHome();
    return;
  }
  $('#home').hidden = true;
  $('#grid-wrap').hidden = false;
  $('#grid-wrap').classList.add('loading');
  $('#results-meta').textContent = 'Searching';
  try {
    const res = await call('Search', request());
    if (mine !== seq) return;
    rows = res.rows || [];
    sort = { key: null, dir: 1 };
    cursor = rows.length ? 0 : -1;
    $('#results-meta').textContent = `${plural(rows.length, 'result')} in ${res.millis} ms`;
    const tr = $('#truncated');
    tr.hidden = !res.truncated;
    tr.textContent = res.truncated ? 'More objects match than are shown. Narrow the search, or pick a class or an OU.' : '';
    drawRows();
  } catch (e) {
    if (mine !== seq) return;
    rows = [];
    drawRows(errText(e));
    $('#results-meta').textContent = '';
  } finally {
    if (mine === seq) $('#grid-wrap').classList.remove('loading');
  }
}

const search = debounce(() => runQuery(), 200);
$('#q').addEventListener('input', () => {
  // The top search is global; only an OU being browsed stays as a removable scope.
  if (page !== 'results' || query.view) query = { text: '', cls: '', view: null, under: page === 'results' ? query.under : null, oneLevel: false };
  query.text = $('#q').value.trim();
  query.oneLevel = !!query.under && !query.text;
  if (page !== 'results') setPage('results');
  drawChips();
  search();
});
$('#q').addEventListener('keydown', (e) => {
  if (e.key === 'ArrowDown' && rows.length) { e.preventDefault(); cursor = 0; drawCursor(); $('#rows').focus(); }
  if (e.key === 'Enter') { e.preventDefault(); search.cancel(); runQuery().then(() => { if (rows.length === 1) openObject(rows[0].dn); }); }
  if (e.key === 'Escape' && $('#q').value) { e.stopPropagation(); $('#q').value = ''; $('#q').dispatchEvent(new Event('input')); }
});

// ---- Grid ------------------------------------------------------------------------------------

function sortedRows() {
  if (!sort.key) return rows;
  const k = sort.key;
  return [...rows].sort((a, b) => String(a[k] || '').localeCompare(String(b[k] || ''), undefined, { numeric: true, sensitivity: 'base' }) * sort.dir);
}

for (const th of $$('#grid th[data-sort]')) {
  th.tabIndex = 0;
  const go = () => {
    sort = sort.key === th.dataset.sort ? { key: th.dataset.sort, dir: -sort.dir } : { key: th.dataset.sort, dir: 1 };
    drawRows();
  };
  th.addEventListener('click', go);
  th.addEventListener('keydown', (e) => { if (e.key === 'Enter') go(); });
}

function statusBadges(r) {
  const out = [];
  if (r.enabled === false) out.push(badge('Disabled', 'dim'));
  if (r.locked) out.push(badge('Locked', 'crit'));
  if (r.privileged) out.push(badge('Tier 0', 'special'));
  return out;
}

function drawRows(error) {
  const body = clear($('#rows'));
  for (const th of $$('#grid th[data-sort]')) {
    th.classList.toggle('sorted', sort.key === th.dataset.sort);
    th.setAttribute('aria-sort', sort.key === th.dataset.sort ? (sort.dir > 0 ? 'ascending' : 'descending') : 'none');
  }
  const empty = $('#grid-empty');
  const list = sortedRows();
  if (error || !list.length) {
    empty.hidden = false;
    clear(empty).append(error ? emptyState('alert', 'The search failed', error)
      : emptyState('search', query.under && !query.text ? 'This container is empty' : 'No objects match', query.text ? 'Search matches names, sign-in names, email addresses, phone numbers and employee IDs.' : ''));
  } else empty.hidden = true;
  const open = currentObject();
  list.forEach((r, i) => {
    const sel = h('input', { type: 'checkbox', 'aria-label': 'Select ' + r.name, checked: selected.has(r.dn), onclick: (e) => e.stopPropagation(),
      onchange: (e) => { e.target.checked ? selected.add(r.dn) : selected.delete(r.dn); drawBulk(); } });
    const tr = h('tr', { dataset: { dn: r.dn, i: String(i) }, class: (open && open.dn === r.dn ? 'open ' : '') + (i === cursor ? 'cursor' : '') },
      h('td', { class: 'c-sel' }, sel),
      h('td', { class: 'c-name' }, h('span', { class: 'cell-name' }, kindIcon(r.kind, 'kind ' + r.kind), h('span', { class: 'name', text: r.name }))),
      h('td', { class: 'c-sub', text: r.sub || '' }),
      h('td', { class: 'c-title', text: r.title || '' }),
      h('td', { class: 'c-dept', text: r.department || '' }),
      h('td', { class: 'c-loc', text: r.location || '' }),
      h('td', { class: 'c-logon', text: r.kind === 'user' || r.kind === 'computer' ? (r.lastLogon ? relTime(r.lastLogon) : 'Never') : '', title: r.lastLogon || '' }),
      h('td', { class: 'c-status' }, statusBadges(r)));
    tr.addEventListener('click', () => { cursor = i; drawCursor(); activate(r); });
    body.append(tr);
  });
  $('#sel-all').checked = list.length > 0 && list.every((r) => selected.has(r.dn));
}

// Every row opens its page; an OU or container also browses in on double-click or Shift+Enter.
function activate(r) { openObject(r.dn); }

function drawCursor() {
  for (const tr of $$('#rows tr')) tr.classList.toggle('cursor', Number(tr.dataset.i) === cursor);
  const tr = $(`#rows tr[data-i="${cursor}"]`);
  if (tr) tr.scrollIntoView({ block: 'nearest' });
}

$('#rows').addEventListener('keydown', (e) => {
  const list = sortedRows();
  if (!list.length) return;
  if (e.key === 'ArrowDown') { cursor = Math.min(list.length - 1, cursor + 1); drawCursor(); e.preventDefault(); }
  else if (e.key === 'ArrowUp') { if (cursor <= 0) { $('#q').focus(); } else { cursor -= 1; drawCursor(); } e.preventDefault(); }
  else if (e.key === 'Enter' && list[cursor]) {
    e.preventDefault();
    const r = list[cursor];
    if (e.shiftKey && (r.kind === 'ou' || r.kind === 'container')) browse(r.dn); else activate(r);
  } else if (e.key === ' ' && list[cursor]) {
    e.preventDefault();
    const dn = list[cursor].dn;
    selected.has(dn) ? selected.delete(dn) : selected.add(dn);
    drawRows();
    drawBulk();
  }
});
$('#rows').addEventListener('dblclick', (e) => {
  const tr = e.target.closest('tr');
  const r = tr && sortedRows()[Number(tr.dataset.i)];
  if (r && (r.kind === 'ou' || r.kind === 'container')) browse(r.dn);
});
$('#sel-all').addEventListener('change', (e) => {
  for (const r of sortedRows()) e.target.checked ? selected.add(r.dn) : selected.delete(r.dn);
  drawRows();
  drawBulk();
});

function markOpen(dn) {
  for (const tr of $$('#rows tr')) tr.classList.toggle('open', tr.dataset.dn === dn);
}

// ---- Bulk ------------------------------------------------------------------------------------

const BULK = [
  ['enable', 'Enable', 'power'], ['disable', 'Disable', 'power'], ['unlock', 'Unlock', 'unlock'], ['move', 'Move to OU', 'move'],
  ['addgroup', 'Add to group', 'plus'], ['rmgroup', 'Remove from group', 'minus'], ['set', 'Set attribute', 'tag'], ['delete', 'Delete', 'trash'],
];
for (const [key, label, ic] of BULK) {
  $('#bulk-actions').append(h('button', { type: 'button', class: 'ghost small-btn', id: 'bulk-' + key, onclick: () => bulk(key) }, icon(ic), label));
}
$('#bulk-clear').addEventListener('click', () => { selected.clear(); drawRows(); drawBulk(); });

function drawBulk() {
  $('#bulk').hidden = selected.size === 0;
  $('#bulk-count').textContent = plural(selected.size, 'selected', 'selected');
}

const SET_ATTRS = ['title', 'department', 'company', 'physicalDeliveryOfficeName', 'description', 'telephoneNumber', 'l', 'st', 'co', 'msExchUsageLocation', 'employeeType'];

async function bulk(key) {
  const dns = [...selected];
  const n = plural(dns.length, 'object');
  let edits = [], title = '';
  if (['enable', 'disable', 'unlock', 'delete'].includes(key)) {
    edits = dns.map((dn) => ({ kind: key, dn }));
    title = `${key[0].toUpperCase() + key.slice(1)} ${n}`;
  } else if (key === 'move') {
    const ou = await pick({ title: `Move ${n} to`, cls: 'container', placeholder: 'OU or container name' });
    if (!ou) return;
    edits = dns.map((dn) => ({ kind: 'move', dn, target: ou.dn }));
    title = `Move ${n} to ${ou.name}`;
  } else if (key === 'addgroup' || key === 'rmgroup') {
    const g = await pick({ title: `${key === 'addgroup' ? 'Add' : 'Remove'} ${n} ${key === 'addgroup' ? 'to' : 'from'} a group`, cls: 'group', placeholder: 'Group name' });
    if (!g) return;
    edits = dns.map((dn) => ({ kind: key === 'addgroup' ? 'add-member' : 'remove-member', dn: g.dn, target: dn }));
    title = `${key === 'addgroup' ? 'Add' : 'Remove'} ${n} ${key === 'addgroup' ? 'to' : 'from'} ${g.name}`;
  } else if (key === 'set') {
    const r = await ask({ title: `Set an attribute on ${n}`, text: 'One plan sets the same value on every selected object. Leave the value empty to clear the attribute.',
      fields: [{ id: 'attr', label: 'Attribute', value: 'title', list: 'attr-list', help: 'The LDAP name, for example title, department or physicalDeliveryOfficeName.' }, { id: 'value', label: 'Value' }], ok: 'Review plan' });
    if (!r || !r.attr.trim()) return;
    edits = dns.map((dn) => ({ kind: 'set', dn, attribute: r.attr.trim(), values: r.value.trim() ? [r.value.trim()] : [] }));
    title = `Set ${r.attr.trim()} on ${n}`;
  }
  planNow(title, edits);
}
document.body.append(h('datalist', { id: 'attr-list' }, SET_ATTRS.map((a) => h('option', { value: a }))));

// ---- Home -----------------------------------------------------------------------------------

async function drawHome() {
  const home = clear($('#home'));
  const st = state.status || {};
  home.append(h('div', { class: 'home-hero' },
    h('div', null, h('h3', { text: 'Find anyone in ' + (st.domain || 'the directory') }),
      h('p', { class: 'muted', text: 'Type a name, sign-in name, email address, phone number or employee ID. Press / or Ctrl+K to search from anywhere, arrow keys to move through results and Enter to open one.' }))));
  const tiles = h('div', { class: 'tiles', id: 'home-tiles' });
  const tileViews = ['locked', 'disabled', 'never', 'inactive', 'created', 'mustchange'];
  for (const key of tileViews) {
    const v = VIEWS.find((x) => x.key === key);
    const count = h('span', { class: 'tile-count' }, spinner());
    tiles.append(h('button', { type: 'button', class: 'tile ' + key, dataset: { view: key }, onclick: () => setView(key) }, icon(v.icon), count, h('span', { class: 'tile-label', text: v.label })));
    const q = { text: '', class: v.q.cls, filters: v.q.filters || [], under: '', oneLevel: false, createdDays: v.q.createdDays || 0, inactiveDays: v.q.inactiveDays || 0, limit: 1000, includeHidden: false };
    call('Search', q).then((res) => {
      const n = (res.rows || []).length;
      count.textContent = res.truncated ? n + '+' : String(n);
      const badgeEl = $(`.nav-count[data-count="${key}"]`);
      if (badgeEl) badgeEl.textContent = n ? String(n) : '';
    }).catch(() => { count.textContent = 'n/a'; });
  }
  home.append(h('h4', { class: 'home-h', text: 'At a glance' }), tiles);
  const recent = store.get('recent', []);
  if (recent.length) {
    home.append(h('h4', { class: 'home-h', text: 'Recently opened on this device' }),
      h('ul', { class: 'recent' }, recent.map((r) => h('li', null, h('button', { type: 'button', onclick: () => openObject(r.dn) }, kindIcon(r.kind), h('span', { class: 'name', text: r.name }), h('span', { class: 'muted small', text: r.sub }))))));
  }
}

// ---- OU tree ----------------------------------------------------------------------------------

async function loadTree() {
  const root = clear($('#tree'));
  root.append(h('li', { class: 'muted small tree-loading' }, spinner(), ' Loading'));
  try {
    const nodes = (await call('Tree', '')) || [];
    clear(root);
    for (const n of nodes) root.append(treeNode(n, 0));
    if (!nodes.length) root.append(h('li', { class: 'muted small', text: 'No containers found' }));
  } catch (e) {
    clear(root).append(h('li', { class: 'error small', text: errText(e) }));
  }
}

function treeNode(n, depth) {
  const kids = h('ul', { role: 'group', hidden: true });
  const twist = h('button', { type: 'button', class: 'twist', 'aria-label': 'Expand ' + n.name, tabIndex: -1 }, icon('chevron', 'small'));
  const label = h('button', { type: 'button', class: 'tree-label', dataset: { dn: n.dn } }, icon(n.kind === 'ou' ? 'ou' : 'container', 'small'), h('span', { text: n.name }));
  const li = h('li', { role: 'treeitem', 'aria-expanded': 'false', dataset: { dn: n.dn }, class: 'depth' }, h('div', { class: 'tree-row' }, twist, label), kids);
  li.style.setProperty('--depth', String(depth));
  let loaded = false;
  const toggle = async (open) => {
    const want = open === undefined ? kids.hidden : open;
    if (want && !loaded) {
      loaded = true;
      kids.append(h('li', { class: 'muted small tree-loading' }, spinner()));
      try {
        const nodes = (await call('Tree', n.dn)) || [];
        clear(kids);
        for (const c of nodes) kids.append(treeNode(c, depth + 1));
        if (!nodes.length) li.classList.add('leaf');
      } catch (e) { clear(kids).append(h('li', { class: 'error small', text: errText(e) })); loaded = false; }
    }
    kids.hidden = !want;
    li.setAttribute('aria-expanded', String(want));
  };
  li._toggle = toggle;
  twist.addEventListener('click', () => toggle());
  label.addEventListener('click', () => { browse(n.dn); toggle(true); });
  return li;
}

async function expandTreeTo(dn) {
  // Expand every loaded ancestor of dn and mark it current.
  const chain = [];
  for (let d = dn; d; d = parentDN(d)) chain.unshift(d.toLowerCase());
  for (const a of chain) {
    const li = $$('#tree li[data-dn]').find((x) => x.dataset.dn.toLowerCase() === a);
    if (li && li._toggle && a !== dn.toLowerCase()) await li._toggle(true);
  }
  markNav();
}

function markNav() {
  for (const b of $$('.rail button[data-view], .rail button[data-page]')) {
    const on = page === 'results' ? (b.dataset.view ? (b.dataset.view === 'home' ? isHome() : b.dataset.view === query.view) : false) : b.dataset.page === page;
    b.classList.toggle('on', on);
  }
  for (const b of $$('#tree .tree-label')) b.classList.toggle('on', page === 'results' && !query.text && !!query.under && b.dataset.dn.toLowerCase() === query.under.toLowerCase());
}

// ---- Pending changes tray --------------------------------------------------------------------------

function pendingChanged() {
  const n = state.pending.size;
  $('#tray').hidden = n === 0;
  $('#tray-toggle').textContent = plural(n, 'pending change');
  const list = clear($('#tray-list'));
  for (const [key, p] of state.pending) {
    list.append(h('div', { class: 'tray-item' }, h('div', null, h('div', { class: 'tray-what', text: p.member ? p.label : `${p.label} on ${p.objName}` }),
      p.member ? null : h('div', { class: 'muted small', text: `${(p.before || []).join(', ') || 'not set'} → ${(p.after || []).join(', ') || 'cleared'}` })),
      h('button', { type: 'button', class: 'icon-btn tiny', title: 'Remove', onclick: () => { state.pending.delete(key); pendingChanged(); } }, icon('x'))));
  }
  if (!n) { list.hidden = true; $('#tray-toggle').setAttribute('aria-expanded', 'false'); }
  rerender();
}
$('#tray-toggle').addEventListener('click', () => {
  const l = $('#tray-list');
  l.hidden = !l.hidden;
  $('#tray-toggle').setAttribute('aria-expanded', String(!l.hidden));
});
$('#tray-discard').addEventListener('click', () => { state.pending.clear(); pendingChanged(); toast('Staged changes discarded'); });
$('#tray-review').addEventListener('click', () => {
  const keys = [...state.pending.keys()];
  const entries = [...state.pending.values()];
  const names = [...new Set(entries.map((p) => p.objName))];
  const title = names.length === 1 ? `Edit ${names[0]}` : `Edit ${plural(names.length, 'object')}`;
  reviewPlan((ticket) => call('PlanEdits', { title, ticket, edits: entries.map((p) => p.edit) }), {
    onApplied: () => { for (const k of keys) state.pending.delete(k); pendingChanged(); },
  });
});

// ---- After apply -------------------------------------------------------------------------------------

function refreshAfterApply() {
  const open = currentObject();
  if (open) openObject(open.dn, { keepScroll: true, closeIfMissing: true });
  nav.reload();
}

function reload() {
  if (page === 'results') runQuery();
  else setPage(page);
}

// ---- Keyboard ----------------------------------------------------------------------------------------

document.addEventListener('keydown', (e) => {
  if ($('#shell').hidden || !$('#lock').hidden) return;
  const typing = e.target.closest('input, textarea, select, [contenteditable]');
  if ((e.key === '/' && !typing) || (e.key.toLowerCase() === 'k' && (e.ctrlKey || e.metaKey))) {
    e.preventDefault();
    $('#q').focus();
    $('#q').select();
  } else if (e.key === 'Escape' && !typing && !document.querySelector('dialog[open]') && currentObject()) {
    closeObject();
    $('#rows').focus();
  }
});

Object.assign(nav, { openObject, browse, refreshAfterApply, setPage, pendingChanged, reload, markOpen, goHome });

// ---- Start ------------------------------------------------------------------------------------------

(async () => {
  try {
    applyStatus(await call('Status'));
  } catch (e) {
    $('#boot').textContent = 'The console could not start: ' + errText(e);
  }
})();
