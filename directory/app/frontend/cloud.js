// The Microsoft 365 side: the connection chip, the mirror pane on the object
// page (loaded after the object, so the page stays fast), Sync now with its
// watch, and the Sync page. Tenant values are untrusted text: textContent only.
import { $, h, icon, clear, call, errText, state, nav, toast, badge, spinner, plural, shortDate } from './core.js';

// ---- Time --------------------------------------------------------------------------

/** ago renders an RFC 3339 time relative to now, to the minute. */
export function ago(iso) {
  const t = Date.parse(iso || '');
  if (Number.isNaN(t)) return '';
  const s = Math.round((Date.now() - t) / 1000);
  const abs = Math.abs(s), future = s < 0;
  const unit = abs < 60 ? null : abs < 3600 ? [Math.round(abs / 60), 'minute'] : abs < 86400 ? [Math.round(abs / 3600), 'hour'] : [Math.round(abs / 86400), 'day'];
  if (!unit) return future ? 'in under a minute' : 'just now';
  const text = plural(unit[0], unit[1]);
  return future ? 'in ' + text : text + ' ago';
}
const when = (iso) => (iso ? `${ago(iso)} (${shortDate(iso)})` : '');

// ---- Connection ------------------------------------------------------------------------

export async function refreshCloud() {
  try {
    state.cloud = await call('CloudStatus');
  } catch {
    state.cloud = null;
  }
  drawChip();
  return state.cloud;
}

let signingIn = false;

/** connect runs the delegated Microsoft 365 sign-in (the system browser in live mode). */
export async function connect() {
  if (signingIn) return;
  signingIn = true;
  drawChip();
  try {
    state.cloud = await call('CloudSignIn');
    toast('Signed in to Microsoft 365 as ' + state.cloud.account, 'ok');
  } catch (e) {
    toast('Microsoft 365 sign-in failed: ' + errText(e), 'crit');
    await refreshCloud();
  } finally {
    signingIn = false;
    drawChip();
    nav.cloudChanged && nav.cloudChanged();
  }
}

export async function disconnect() {
  state.cloud = await call('CloudSignOut');
  mirrors.clear();
  drawChip();
  nav.cloudChanged && nav.cloudChanged();
}

const CLOUD_NAMES = { commercial: 'Commercial', gcchigh: 'GCC High', dod: 'DoD' };
const SYNC_NAMES = { connect: 'Entra Connect Sync', cloud: 'Entra Cloud Sync', '': 'None' };

function drawChip() {
  const chip = $('#cloud-chip');
  const cs = state.cloud;
  const out = $('#cloud-signout');
  out.hidden = !(cs && cs.signedIn);
  if (!cs || !cs.configured) { chip.hidden = true; return; }
  chip.hidden = false;
  clear(chip);
  chip.disabled = signingIn;
  chip.classList.toggle('connected', !!cs.signedIn);
  if (signingIn) {
    chip.append(spinner(), h('span', { text: 'Finish the sign-in in your browser' }));
    chip.title = 'Microsoft 365 sign-in in progress';
  } else if (cs.signedIn) {
    chip.append(icon('cloud'), h('span', { class: 'cloud-acct', text: cs.account }));
    chip.title = `Microsoft 365 (${CLOUD_NAMES[cs.cloud] || cs.cloud}), tenant ${cs.tenantId}. Sign out from the account menu.`;
  } else {
    chip.append(icon('cloud'), h('span', { text: 'Connect Microsoft 365' }));
    chip.title = 'Sign in to Microsoft 365 as yourself to see the cloud side of each object';
  }
}

export function cloudFacts() {
  const cs = state.cloud;
  if (!cs || !cs.configured) return [['Microsoft 365', 'Not set up']];
  return [['Microsoft 365', cs.signedIn ? cs.account : 'Not signed in'], ['Tenant', `${cs.tenantId} (${CLOUD_NAMES[cs.cloud] || cs.cloud})`],
    ['Directory sync', SYNC_NAMES[cs.syncKind || ''] + (cs.syncServer ? ' on ' + cs.syncServer : '')]];
}

/** resetCloud forgets the cloud side when the AD session ends (the cloud sign-in ends with it). */
export function resetCloud() {
  for (const m of mirrors.values()) if (m.sync) stopWatch(m.sync);
  mirrors.clear();
  state.cloud = null;
  drawChip();
}

// ---- Mirror pane -----------------------------------------------------------------------------

// Per object: { view, error, loading, seq, showAll, sync: { phase, message, ... } }
const mirrors = new Map();
const key = (dn) => dn.toLowerCase();
let rerenderCard = () => {};

/** loadMirror fetches the pane for dn; redraw(dn) is called when it changes. */
export async function loadMirror(dn, redraw) {
  rerenderCard = redraw;
  const cs = state.cloud;
  if (!cs || !cs.configured || !cs.signedIn) return;
  const m = mirrors.get(key(dn)) || { showAll: false };
  if (m.sync && (m.sync.phase === 'starting' || m.sync.phase === 'waiting')) return; // the watch refreshes it
  m.loading = true;
  m.seq = (m.seq || 0) + 1;
  const mine = m.seq;
  mirrors.set(key(dn), m);
  redraw(dn);
  try {
    const v = await call('Mirror', dn);
    if (mine !== m.seq) return;
    m.view = v;
    m.error = '';
  } catch (e) {
    if (mine !== m.seq) return;
    m.error = errText(e);
  } finally {
    if (mine === m.seq) m.loading = false;
  }
  redraw(dn);
}

const MATCH_TONE = { 'linked by source anchor': 'ok', 'linked by SID': 'ok', 'possible match': 'warn', 'not in Microsoft 365': 'dim', 'not compared': 'dim' };
const STATUS_TONE = { match: 'ok', 'waiting for sync': 'info', 'out of step': 'crit', 'not compared': 'dim' };
const METHODS = { microsoftAuthenticator: 'Microsoft Authenticator', phone: 'Phone', fido2: 'FIDO2 security key', windowsHelloForBusiness: 'Windows Hello for Business',
  softwareOath: 'Authenticator app (OATH)', temporaryAccessPass: 'Temporary Access Pass', email: 'Email', x509Certificate: 'Certificate', platformCredential: 'Platform credential' };
const TRUST = { AzureAd: 'Entra joined', ServerAd: 'Hybrid joined', Workplace: 'Registered' };

/** mirrorCard renders the Microsoft 365 card for the object view v. */
export function mirrorCard(v) {
  const card = h('section', { class: 'card cloud-card', id: 'card-cloud' });
  const head = h('div', { class: 'card-head' }, h('h3', null, icon('cloud'), ' Microsoft 365'));
  card.append(head);
  const cs = state.cloud;
  if (!cs || !cs.configured || !cs.signedIn) {
    card.append(h('p', { class: 'muted small', text: v.cloudNote }));
    if (cs && cs.configured && !cs.signedIn) {
      card.append(h('div', { class: 'card-tools' }, h('button', { type: 'button', class: 'ghost small-btn', id: 'mirror-connect', onclick: connect }, icon('cloud'), 'Connect Microsoft 365')));
    }
    card.append(readiness(v));
    return card;
  }
  const m = mirrors.get(key(v.dn)) || {};
  const mv = m.view;
  if (mv && mv.match) head.append(badge(mv.match, MATCH_TONE[mv.match] || 'dim'));
  head.append(h('span', { class: 'grow' }), syncButton(v, m));

  if (!mv && (m.loading || !m.error)) {
    card.append(h('div', { class: 'skeleton', id: 'mirror-loading', 'aria-label': 'Loading the Microsoft 365 side' },
      h('span', { class: 'sk w60' }), h('span', { class: 'sk w40' }), h('span', { class: 'sk w80' }), h('span', { class: 'sk w50' })));
    return card;
  }
  if (m.error && !mv) {
    card.append(h('p', { class: 'error small', role: 'alert', text: m.error }),
      h('div', { class: 'card-tools' }, h('button', { type: 'button', class: 'ghost small-btn', onclick: () => loadMirror(v.dn, rerenderCard) }, icon('refresh'), 'Try again')));
    return card;
  }
  card.append(syncStatus(m));
  const u = mv.user;
  for (const e of (u && u.provisioningErrors) || []) {
    card.append(h('div', { class: 'note crit sync-error', role: 'alert' }, icon('alert'), h('div', null,
      h('strong', { text: `Sync error: ${e.category} on ${e.propertyCausingError}` }),
      h('div', { text: e.value }),
      h('div', { class: 'small', text: 'Reported ' + when(e.occurredDateTime) + '. Microsoft 365 keeps the old value until the conflict is resolved in AD.' }))));
  }
  card.append(h('p', { class: 'muted small match-reason', id: 'mirror-reason', text: mv.reason }));

  if (mv.match === 'possible match') {
    for (const c of mv.candidates || []) {
      card.append(h('div', { class: 'candidate', id: 'mirror-candidate' },
        h('div', { class: 'cand-head' }, h('span', { class: 'cand-name', text: c.user.displayName }), badge('Not linked', 'warn')),
        h('div', { class: 'muted small', text: c.user.userPrincipalName }),
        h('ul', { class: 'cand-reasons' }, (c.reasons || []).map((r) => h('li', { text: r })))));
    }
  }
  if (mv.hint) card.append(h('p', { class: 'note info small' }, icon('info'), h('span', { text: mv.hint })));

  if (u) {
    card.append(h('dl', { class: 'cloud-facts', id: 'mirror-facts' },
      fact('Cloud user', u.userPrincipalName),
      fact('Sign-in', null, badge(u.signInEnabled === false ? 'Blocked' : 'Enabled', u.signInEnabled === false ? 'dim' : 'ok')),
      fact('Last cloud sign-in', u.lastSignIn ? when(u.lastSignIn) : 'Not reported'),
      fact('Last sync', u.lastSync ? when(u.lastSync) : (u.syncEnabled ? 'Not reported' : 'Not synced (cloud-only user)'), null, 'mirror-lastsync'),
      fact('Source', u.source),
      fact('Created in the cloud', u.created ? shortDate(u.created) : '')));
    card.append(comparison(v, m));
    card.append(sub('Licenses'), (mv.licenses || []).length
      ? h('div', { class: 'chips-inline' }, mv.licenses.map((l) => h('span', { class: 'lic' }, h('span', { text: l.skuPartNumber || l.skuId }), l.viaGroup ? h('span', { class: 'via', text: 'via group' }) : null)))
      : h('p', { class: 'muted small', text: 'No licenses assigned' }));
    const methods = mv.methods || [];
    card.append(sub('Sign-in methods'));
    if (mv.methodsNote) card.append(h('p', { class: 'muted small', text: mv.methodsNote }));
    else if (!methods.length) card.append(h('p', { class: 'note warn small', id: 'mirror-nomfa' }, icon('alert'), h('span', { text: 'No multifactor method is registered for this user (IA.L2-3.5.3).' })));
    else card.append(h('div', { class: 'chips-inline' }, methods.map((x) => badge(METHODS[x] || x, 'info'))));
    card.append(sub('Devices'));
    if (mv.devicesNote) card.append(h('p', { class: 'muted small', text: mv.devicesNote }));
    else if (!(mv.devices || []).length) card.append(h('p', { class: 'muted small', text: 'No registered devices' }));
    else {
      card.append(h('ul', { class: 'devices' }, mv.devices.map((d) => h('li', null, icon('computer', 'small'),
        h('div', { class: 'dev-text' }, h('span', { class: 'dev-name', text: d.displayName }),
          h('span', { class: 'muted small', text: [d.operatingSystem, TRUST[d.trustType] || d.trustType, d.approximateLastSignInDateTime ? 'seen ' + ago(d.approximateLastSignInDateTime) : ''].filter(Boolean).join(' · ') })),
        d.isCompliant === true ? badge('Compliant', 'ok') : d.isCompliant === false ? badge('Not compliant', 'crit') : badge(d.isManaged ? 'Managed' : 'Unmanaged', 'dim')))));
    }
    card.append(sub('Cloud groups'));
    if (mv.groupsNote) card.append(h('p', { class: 'muted small', text: mv.groupsNote }));
    for (const g of mv.groups || []) {
      card.append(h('div', { class: 'gset' }, h('div', { class: 'gset-label' }, h('span', { text: g.label }), h('span', { class: 'count', text: String((g.groups || []).length) })),
        h('div', { class: 'chips-inline' }, (g.groups || []).map((x) => h('span', { class: 'gchip ' + g.kind, text: x.displayName, title: x.mail || '' })))));
    }
    if (!(mv.groups || []).length && !mv.groupsNote) card.append(h('p', { class: 'muted small', text: 'No cloud group memberships' }));
  }
  card.append(readiness(v));
  card.append(h('div', { class: 'cloud-foot' }, h('span', { class: 'muted small', text: `Checked ${ago(mv.checkedAt)} as ${mv.account}` }),
    h('button', { type: 'button', class: 'link-btn', id: 'mirror-refresh', disabled: m.loading, onclick: () => loadMirror(v.dn, rerenderCard) }, m.loading ? 'Refreshing' : 'Refresh')));
  return card;
}

function fact(label, value, node, id) {
  return [h('dt', { text: label }), h('dd', { id: id || null }, node || h('span', { text: value || '' }))];
}
const sub = (t) => h('div', { class: 'sub-head', text: t });

function readiness(v) {
  const checks = v.readiness || [];
  if (!checks.length) return h('span', { hidden: true });
  return h('div', { class: 'ad-checks' }, sub('Checks on the AD side'), h('ul', { class: 'checks' }, checks.map((c) => h('li', { class: 'check-row ' + c.status },
    icon(c.status === 'ok' ? 'check' : c.status === 'warn' ? 'alert' : 'info'),
    h('div', null, h('div', { class: 'check-label', text: c.label }), h('div', { class: 'muted small', text: c.detail }))))));
}

function comparison(v, m) {
  const mv = m.view;
  const rows = mv.rows || [];
  const diffs = rows.filter((r) => r.status === 'waiting for sync' || r.status === 'out of step');
  const box = h('div', { class: 'compare', id: 'mirror-compare' });
  const outOfStep = diffs.length - (mv.waiting || 0);
  const summary = !rows.length ? 'No attributes compared'
    : !diffs.length ? `All ${rows.filter((r) => r.status === 'match').length} compared attributes match`
      : [mv.waiting ? `${mv.waiting} waiting for sync` : '', outOfStep ? `${outOfStep} out of step` : ''].filter(Boolean).join(', ');
  box.append(h('div', { class: 'compare-head' }, h('span', { class: 'sub-head inline', text: 'Attributes' }),
    h('span', { class: 'compare-sum ' + (outOfStep ? 'crit' : mv.waiting ? 'info' : 'ok'), id: 'mirror-summary', text: summary }),
    h('span', { class: 'grow' }),
    rows.length ? h('button', { type: 'button', class: 'link-btn', id: 'mirror-toggle', onclick: () => { m.showAll = !m.showAll; rerenderCard(v.dn); } },
      m.showAll ? 'Show differences only' : `Show all ${rows.length} rows`) : null));
  if (mv.adChanged) box.append(h('p', { class: 'muted small', text: `AD last changed ${when(mv.adChanged)}` }));
  const shown = m.showAll ? rows : diffs;
  if (!shown.length) return box;
  const table = h('table', { class: 'cmp-table', id: 'mirror-rows' }, h('thead', null, h('tr', null, h('th', { text: 'Attribute' }), h('th', { text: 'Active Directory' }), h('th', { text: 'Microsoft 365' }), h('th', { text: 'Status' }))));
  const body = h('tbody');
  for (const r of shown) {
    const tone = STATUS_TONE[r.status] || 'dim';
    body.append(h('tr', { class: 'cmp ' + tone, dataset: { status: r.status, attr: r.attribute } },
      h('td', { class: 'cmp-label' }, h('div', { text: r.label }), h('code', { class: 'small', text: `${r.attribute} → ${r.property}` })),
      h('td', { class: 'cmp-val', text: (r.ad || []).join('\n') || '(empty)' }),
      h('td', { class: 'cmp-val', text: (r.cloud || []).join('\n') || '(empty)' }),
      h('td', null, badge(r.status, tone))));
    if (r.note && r.status !== 'match') body.append(h('tr', { class: 'cmp-note' }, h('td', { colspan: '4', text: r.note })));
  }
  table.append(body);
  box.append(h('div', { class: 'cmp-wrap' }, table));
  return box;
}

// ---- Sync now ------------------------------------------------------------------------------

function syncBlocked(v, m) {
  const cs = state.cloud || {};
  const u = m.view && m.view.user;
  if (!cs.syncReady) return cs.syncKind ? 'Sync now cannot run from this computer: ' + (cs.note || 'the sync method is not available.') : 'No sync method is configured. Add Entra Connect Sync or Cloud Sync in the setup.';
  if (v.privileged) return 'Tier 0 objects are outside Sync now from this console.';
  if (v.kind !== 'user') return 'Sync now covers users in this version.';
  if (!m.view) return 'Loading the cloud side';
  if (!u) return 'This user has no linked cloud user, so there is nothing to watch: check the sync scope first.';
  if (u.syncEnabled === false || u.source !== 'Active Directory') return 'This is a cloud-only user; it does not sync from AD.';
  return '';
}

function syncButton(v, m) {
  const reason = syncBlocked(v, m);
  const busy = m.sync && (m.sync.phase === 'starting' || m.sync.phase === 'waiting');
  return h('button', { type: 'button', class: 'small-btn sync-btn ' + (reason ? 'ghost' : 'primary'), id: 'sync-now', disabled: !!reason || busy,
    title: reason || 'Start a sync for this user and watch the cloud update', onclick: () => syncNow(v.dn) }, icon('refresh', busy ? 'spin' : ''), busy ? 'Syncing' : 'Sync now');
}

function syncStatus(m) {
  const s = m.sync;
  if (!s) return h('span', { hidden: true });
  if (s.phase === 'starting') return h('div', { class: 'sync-state', id: 'sync-state' }, spinner(), h('span', { text: 'Starting the sync' }));
  if (s.phase === 'waiting') {
    const secs = Math.max(0, Math.round((Date.now() - s.t0) / 1000));
    return h('div', { class: 'sync-state waiting', id: 'sync-state' }, h('div', { class: 'progress', 'aria-hidden': 'true' }, h('span')),
      h('div', null, h('strong', { text: 'Waiting for the cloud to update' }), h('div', { class: 'muted small', text: `${sentence(s.message)} Checking every ${plural(s.poll, 'second')}; ${secs} s so far.` })),
      h('button', { type: 'button', class: 'link-btn', onclick: () => { stopWatch(s); s.phase = 'stopped'; s.message = 'Stopped watching. The sync may still complete; refresh later.'; rerenderCard(s.dn); } }, 'Stop watching'));
  }
  const tone = s.phase === 'done' ? 'ok' : s.phase === 'error' ? 'crit' : 'info';
  return h('div', { class: 'note ' + tone + ' sync-result', id: 'sync-state' }, icon(s.phase === 'done' ? 'check' : s.phase === 'error' ? 'alert' : 'info'), h('span', { text: s.message }),
    h('button', { type: 'button', class: 'icon-btn tiny', title: 'Dismiss', onclick: () => { m.sync = null; rerenderCard(s.dn); } }, icon('x')));
}

const sentence = (t) => (t && !/[.!?]$/.test(t) ? t + '.' : t || '');

function stopWatch(s) { clearTimeout(s.timer); clearInterval(s.tick); }

async function syncNow(dn) {
  const m = mirrors.get(key(dn));
  if (!m) return;
  const s = { dn, phase: 'starting', message: '' };
  m.sync = s;
  rerenderCard(dn);
  let res;
  try {
    res = await call('SyncNow', dn);
  } catch (e) {
    s.phase = 'error';
    s.message = 'Sync now failed: ' + errText(e);
    rerenderCard(dn);
    return;
  }
  if (!res.watch) {
    s.phase = 'info';
    s.message = sentence(res.message) + ' The cloud user cannot be watched from here; refresh in a few minutes.';
    rerenderCard(dn);
    return;
  }
  Object.assign(s, { phase: 'waiting', message: res.message, before: Date.parse(res.lastSyncBefore || '') || 0, poll: res.pollSeconds || 15,
    deadline: Date.now() + (res.watchMinutes || 10) * 60000, t0: Date.now() });
  s.tick = setInterval(() => rerenderCard(dn, true), 1000);
  const check = async () => {
    if (s.phase !== 'waiting') return;
    try {
      const v = await call('Mirror', dn);
      const last = Date.parse((v.user && v.user.lastSync) || '') || 0;
      if (last > s.before) {
        stopWatch(s);
        m.view = v;
        s.phase = 'done';
        s.message = `Synced at ${shortDate(v.user.lastSync)}. ` + (v.differences ? `${plural(v.differences, 'difference')} remain${v.differences === 1 ? 's' : ''}; see the rows below.` : 'The cloud now matches AD.');
        rerenderCard(dn);
        return;
      }
    } catch (e) {
      stopWatch(s);
      s.phase = 'error';
      s.message = 'Watching stopped: ' + errText(e);
      rerenderCard(dn);
      return;
    }
    if (Date.now() > s.deadline) {
      stopWatch(s);
      s.phase = 'info';
      s.message = `The cloud user has not updated after ${plural(Math.round((s.deadline - s.t0) / 60000), 'minute')}. The cycle may still be running or the object may be out of scope; see the Sync page.`;
      rerenderCard(dn);
      return;
    }
    s.timer = setTimeout(check, s.poll * 1000);
  };
  // The first look comes early: on-demand provisioning and a quiet delta cycle are often quick.
  s.timer = setTimeout(check, Math.min(2, s.poll) * 1000);
  rerenderCard(dn);
}

/** refreshTick updates only the watch line of the visible card. */
export function syncLine(dn) {
  const m = mirrors.get(key(dn));
  return m ? syncStatus(m) : null;
}

// ---- Sync page ---------------------------------------------------------------------------------

export async function showSync() {
  const page = clear($('#page-sync'));
  const refresh = h('button', { type: 'button', class: 'ghost', id: 'sync-refresh', onclick: () => showSync() }, icon('refresh'), 'Refresh');
  page.append(h('div', { class: 'page-head' }, h('div', null, h('h2', { text: 'Directory sync' }),
    h('p', { class: 'muted', text: 'How AD reaches Microsoft 365: the sync method, the health of the sync server and the Microsoft 365 sign-in of this console.' })), h('div', { class: 'page-tools' }, refresh)));
  const grid = h('div', { class: 'sync-grid' });
  page.append(grid);
  const cs = await refreshCloud();
  grid.append(connectionCard(cs));
  const health = h('section', { class: 'card health', id: 'sync-health' }, h('h3', { text: 'Sync health' }), h('div', { class: 'loading-line' }, spinner(), ' Reading the sync server'));
  grid.prepend(health);
  try {
    const hl = await call('SyncHealth');
    health.replaceWith(healthCard(hl));
  } catch (e) {
    clear(health).append(h('h3', { text: 'Sync health' }), h('p', { class: 'error', role: 'alert', text: errText(e) }));
  }
}

function connectionCard(cs) {
  const card = h('section', { class: 'card', id: 'sync-connection' }, h('h3', null, icon('cloud'), ' Microsoft 365 connection'));
  if (!cs) { card.append(h('p', { class: 'error', text: 'The Microsoft 365 status could not be read.' })); return card; }
  const dl = h('dl', { class: 'cloud-facts' });
  for (const [k, v] of cloudFacts()) dl.append(h('dt', { text: k }), h('dd', { text: v }));
  dl.append(h('dt', { text: 'Sync now' }), h('dd', null, badge(cs.syncReady ? 'Available' : 'Not available', cs.syncReady ? 'ok' : 'dim')));
  card.append(dl, h('p', { class: 'muted small', text: cs.note }));
  if (cs.configured) {
    card.append(h('div', { class: 'card-tools' }, cs.signedIn
      ? h('button', { type: 'button', class: 'ghost small-btn', onclick: disconnect }, 'Sign out of Microsoft 365')
      : h('button', { type: 'button', class: 'primary small-btn', onclick: connect }, icon('cloud'), 'Connect Microsoft 365')));
  }
  return card;
}

const VERSION_TONE = { current: 'ok', retiring: 'warn', unsupported: 'crit', unknown: 'dim' };

function healthCard(hl) {
  const card = h('section', { class: 'card health', id: 'sync-health' }, h('div', { class: 'card-head' }, h('h3', { text: 'Sync health' }),
    hl.lastResult ? badge('Last cycle: ' + hl.lastResult, /success/i.test(hl.lastResult) ? 'ok' : 'crit') : null));
  const dl = h('dl', { class: 'cloud-facts' });
  const row = (k, v, node) => dl.append(h('dt', { text: k }), h('dd', null, node || h('span', { text: v || 'Not reported' })));
  row('Method', SYNC_NAMES[hl.kind] || hl.kind);
  if (hl.server) row('Server', hl.server);
  if (hl.version || hl.versionStatus) {
    row('Version', null, h('span', { class: 'inline-facts' }, h('span', { text: hl.version || 'unknown' }), hl.versionStatus ? badge(hl.versionStatus, VERSION_TONE[hl.versionStatus] || 'dim') : null));
  }
  if (hl.kind === 'connect') {
    row('Staging mode', null, hl.stagingMode ? badge('On: this server does not export', 'warn') : badge('Off', 'ok'));
    row('Scheduler', null, hl.schedulerEnabled ? badge('Enabled', 'ok') : badge('Disabled', 'warn'));
  }
  if (hl.intervalMinutes) row('Interval', plural(hl.intervalMinutes, 'minute'));
  row('Last cycle', hl.lastCycle ? when(hl.lastCycle) : '');
  if (hl.nextCycle && hl.schedulerEnabled && Date.parse(hl.nextCycle) < Date.now() - 60000) {
    // A next cycle in the past means the scheduler is overdue or stuck.
    row('Next cycle', null, h('span', null, badge('Overdue', 'warn'), h('span', { text: ' was due ' + when(hl.nextCycle) })));
  } else {
    row('Next cycle', hl.nextCycle ? when(hl.nextCycle) : '');
  }
  if (hl.appAuth !== undefined && hl.appAuth !== null) {
    row('Application-based auth', null, hl.appAuth ? badge('Configured', 'ok') : badge('Not configured', 'warn'));
  }
  card.append(dl);
  if (hl.versionNote) card.append(h('p', { class: 'note ' + (hl.versionStatus === 'unsupported' ? 'crit' : hl.versionStatus === 'retiring' ? 'warn' : 'info') + ' small' }, icon('info'), h('span', { text: hl.versionNote })));
  for (const w of hl.warnings || []) card.append(h('p', { class: 'note warn small' }, icon('alert'), h('span', { text: w })));
  card.append(h('p', { class: 'muted small', text: 'Checked ' + shortDate(hl.checkedAt) }));
  return card;
}

