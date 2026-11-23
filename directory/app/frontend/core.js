// Shared helpers for the ZTG Directory console. Directory data is untrusted:
// every value reaches the page through textContent (h() below), never as HTML.

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

/** h builds an element. attrs: class, text, dataset, on<event>, boolean props, other attributes. */
export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v === undefined || v === null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k === 'text') el.textContent = v;
      else if (k === 'dataset') Object.assign(el.dataset, v);
      else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
      else if (['disabled', 'checked', 'hidden', 'value', 'selected', 'open'].includes(k)) el[k] = v;
      else el.setAttribute(k, v === true ? '' : String(v));
    }
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const c of children.flat(Infinity)) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
}

const SVG = 'http://www.w3.org/2000/svg';
/** icon returns an inline SVG that uses a symbol from the sprite in index.html. */
export function icon(name, cls = '') {
  const svg = document.createElementNS(SVG, 'svg');
  svg.setAttribute('class', ('ico ' + cls).trim());
  svg.setAttribute('aria-hidden', 'true');
  const use = document.createElementNS(SVG, 'use');
  use.setAttribute('href', '#i-' + name);
  svg.append(use);
  return svg;
}

export const KIND_ICON = { user: 'user', group: 'group', computer: 'computer', contact: 'contact', ou: 'ou', container: 'container', other: 'other' };
export const KIND_LABEL = { user: 'User', group: 'Group', computer: 'Computer', contact: 'Contact', ou: 'Organizational unit', container: 'Container', other: 'Object' };
export const kindIcon = (kind, cls) => icon(KIND_ICON[kind] || 'other', cls);

export function clear(el) {
  while (el.firstChild) el.firstChild.remove();
  return el;
}

export function debounce(fn, ms) {
  let t;
  const d = (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
  d.cancel = () => clearTimeout(t);
  return d;
}

export const errText = (e) => {
  const t = String((e && e.message) || e || 'Something went wrong').replace(/^Error: /, '');
  return t.charAt(0).toUpperCase() + t.slice(1);
};

/** relTime renders an RFC 3339 time relative to now: "today", "3 days ago". */
export function relTime(iso) {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const days = Math.floor((Date.now() - t) / 86400000);
  if (days < 0) return 'in ' + plural(-days, 'day');
  if (days === 0) return 'Today';
  if (days === 1) return 'Yesterday';
  if (days < 60) return days + ' days ago';
  if (days < 730) return Math.round(days / 30.4) + ' months ago';
  return Math.round(days / 365) + ' years ago';
}
export const plural = (n, word, many) => n + ' ' + (n === 1 ? word : many || word + 's');

export function shortDate(iso) {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso || '';
  return new Date(t).toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

/** rdnName returns the readable value of a DN's first component. */
export function rdnName(dn) {
  const m = /^[^=]+=((?:\\.|[^,])*)/.exec(dn || '');
  // A deleted object's name ends in \0ADEL:<GUID>; only the name before it is shown.
  return m ? m[1].replace(/\\0A[\s\S]*$/i, '').replace(/\\(.)/g, '$1') : dn || '';
}
/** parentDN removes the first component of a DN (escaped commas respected). */
export function parentDN(dn) {
  const m = /^(?:\\.|[^,])*,(.*)$/.exec(dn || '');
  return m ? m[1] : '';
}

// ---- Binding ----------------------------------------------------------------

let afterError = () => {};
export function onCallError(fn) { afterError = fn; }

/** call invokes a bound console.API method (Wails: window.go.console.API). */
export async function call(name, ...args) {
  const api = window.go && window.go.console && window.go.console.API;
  if (!api || typeof api[name] !== 'function') throw new Error('The console is not connected (binding ' + name + ' is missing).');
  try {
    return await api[name](...args);
  } catch (e) {
    const err = new Error(errText(e));
    afterError(err);
    throw err;
  }
}

// ---- Shared state -------------------------------------------------------------

export const state = {
  status: null,
  // pending edits staged from object pages, keyed by dn|attribute or dn|kind|target
  pending: new Map(),
};
export const nav = {}; // filled by app.js: openObject, browse, refreshAfterApply, setPage, pendingChanged

// ---- Toasts -----------------------------------------------------------------

export function toast(text, tone = 'info') {
  const el = h('div', { class: 'toast ' + tone, role: tone === 'crit' ? 'alert' : 'status' }, icon(tone === 'crit' ? 'alert' : tone === 'ok' ? 'check' : 'info'), h('span', { text }));
  $('#toasts').append(el);
  setTimeout(() => el.classList.add('out'), 3600);
  setTimeout(() => el.remove(), 4000);
}

// ---- Dialogs ------------------------------------------------------------------

for (const dlg of $$('dialog')) {
  dlg.addEventListener('click', (e) => { if (e.target.closest('[data-close]')) dlg.close('cancel'); });
}

export function openDialog(dlg) {
  if (!dlg.open) dlg.showModal();
}

/**
 * ask shows a small form and resolves with the field values, or null.
 * fields: [{id, label, type: text|date|checkbox|select|textarea, value, checked, options, help, placeholder}]
 */
export function ask({ title, text, warn, fields = [], ok = 'Continue', okClass = 'primary' }) {
  const dlg = $('#ask-dlg');
  $('#ask-title').textContent = title;
  const body = clear($('#ask-body'));
  if (text) body.append(h('p', { class: 'muted', text }));
  if (warn) body.append(h('p', { class: 'note warn', text: warn }));
  const inputs = {};
  for (const f of fields) {
    let input;
    if (f.type === 'checkbox') {
      input = h('input', { type: 'checkbox', id: 'ask-' + f.id, checked: !!f.checked });
      body.append(h('label', { class: 'check' }, input, ' ', f.label));
    } else if (f.type === 'select') {
      input = h('select', { id: 'ask-' + f.id }, (f.options || []).map((o) => h('option', { value: o.value, text: o.label, selected: o.value === f.value })));
      body.append(h('label', null, f.label, input));
    } else if (f.type === 'textarea') {
      input = h('textarea', { id: 'ask-' + f.id, rows: 3, value: f.value || '' });
      body.append(h('label', null, f.label, input));
    } else {
      input = h('input', { id: 'ask-' + f.id, type: f.type || 'text', value: f.value || '', placeholder: f.placeholder || '', spellcheck: 'false', list: f.list || null });
      body.append(h('label', null, f.label, input));
    }
    if (f.help) body.append(h('p', { class: 'muted small', text: f.help }));
    inputs[f.id] = input;
  }
  const okBtn = $('#ask-ok');
  okBtn.textContent = ok;
  okBtn.className = okClass;
  openDialog(dlg);
  const first = body.querySelector('input:not([type=checkbox]), select, textarea');
  if (first) first.focus(); else okBtn.focus();
  return new Promise((resolve) => {
    const done = () => {
      dlg.removeEventListener('close', done);
      if (dlg.returnValue === 'cancel' || dlg.returnValue === '') return resolve(null);
      const out = {};
      for (const [id, el] of Object.entries(inputs)) out[id] = el.type === 'checkbox' ? el.checked : el.value;
      resolve(out);
    };
    dlg.returnValue = '';
    $('#ask-form').onsubmit = (e) => { e.preventDefault(); dlg.close('ok'); };
    dlg.addEventListener('close', done);
  });
}

/**
 * pick lets the technician find one directory object (Lookup). Resolves with
 * the chosen row, {clear: true} when "Clear" is offered and chosen, or null.
 */
export function pick({ title, cls = '', placeholder = 'Type a name', allowClear = false, exclude = [] }) {
  const dlg = $('#pick-dlg');
  $('#pick-title').textContent = title;
  const q = $('#pick-q');
  q.value = '';
  q.placeholder = placeholder;
  const list = clear($('#pick-list'));
  const foot = clear($('#pick-foot'));
  if (allowClear) foot.append(h('button', { type: 'button', class: 'ghost', id: 'pick-clear', onclick: () => finish({ clear: true }) }, 'Clear the value'));
  foot.append(h('span', { class: 'grow' }), h('button', { type: 'button', class: 'ghost', 'data-close': true }, 'Cancel'));
  let resolver, rows = [], cursor = 0, seq = 0;
  const skip = new Set(exclude.map((d) => d.toLowerCase()));
  const finish = (v) => { resolver(v); resolver = () => {}; dlg.close('done'); };
  const render = () => {
    clear(list);
    if (!rows.length) { list.append(h('li', { class: 'pick-empty', text: q.value ? 'No matches' : 'Type to search' })); return; }
    rows.forEach((r, i) => {
      list.append(h('li', { role: 'option', class: 'pick-row' + (i === cursor ? ' cursor' : ''), 'aria-selected': String(i === cursor), dataset: { dn: r.dn },
        onclick: () => finish(r) }, kindIcon(r.kind), h('span', { class: 'pick-name', text: r.name }), h('span', { class: 'pick-sub', text: r.sub || r.location })));
    });
  };
  const search = debounce(async () => {
    const mine = ++seq;
    list.classList.add('loading');
    try {
      const res = (await call('Lookup', q.value.trim(), cls)) || [];
      if (mine !== seq) return;
      rows = res.filter((r) => !skip.has(r.dn.toLowerCase()));
      cursor = 0;
      render();
    } catch (e) {
      if (mine === seq) { clear(list).append(h('li', { class: 'pick-empty error', text: errText(e) })); }
    } finally { if (mine === seq) list.classList.remove('loading'); }
  }, 180);
  q.oninput = search;
  q.onkeydown = (e) => {
    if (e.key === 'ArrowDown') { cursor = Math.min(rows.length - 1, cursor + 1); render(); e.preventDefault(); }
    if (e.key === 'ArrowUp') { cursor = Math.max(0, cursor - 1); render(); e.preventDefault(); }
    if (e.key === 'Enter' && rows[cursor]) { e.preventDefault(); finish(rows[cursor]); }
  };
  render();
  search();
  openDialog(dlg);
  q.focus();
  return new Promise((resolve) => {
    resolver = resolve;
    dlg.addEventListener('close', () => resolver(null), { once: true });
  });
}

/** copyText copies to the clipboard through the browser or the Wails runtime. */
export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    try {
      if (window.runtime && window.runtime.ClipboardSetText) return !!(await window.runtime.ClipboardSetText(text));
    } catch { /* fall through */ }
  }
  return false;
}

// ---- Local, per-device conveniences (never directory data beyond names) --------

export const store = {
  get(key, fallback) {
    try { const v = localStorage.getItem('ztg-directory:' + key); return v === null ? fallback : JSON.parse(v); } catch { return fallback; }
  },
  set(key, value) {
    try { localStorage.setItem('ztg-directory:' + key, JSON.stringify(value)); } catch { /* storage unavailable */ }
  },
};

export function badge(label, tone) { return h('span', { class: 'badge ' + (tone || 'dim'), text: label }); }

export function spinner() { return h('span', { class: 'spinner', 'aria-hidden': 'true' }); }

export function emptyState(iconName, title, text) {
  return h('div', { class: 'empty' }, icon(iconName, 'big'), h('div', { class: 'empty-title', text: title }), text ? h('p', { class: 'muted', text }) : null);
}
