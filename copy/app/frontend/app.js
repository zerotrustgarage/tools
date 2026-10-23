import {showUpdateDialog} from './updates.js';

const $ = id => document.getElementById(id);
const make = (tag, text, cls) => {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (cls) node.className = cls;
  return node;
};
const clone = value => JSON.parse(JSON.stringify(value));
const key = arg => String(arg).split(':')[0].toUpperCase();
const errorText = error => error?.message || String(error);
const lines = text => text.split(/\r?\n/).map(s => s.trim()).filter(Boolean);
const number = value => new Intl.NumberFormat().format(value || 0);
const bytes = value => {
  if (!value) return '0 B';
  const unit = Math.min(4, Math.floor(Math.log(value) / Math.log(1024)));
  return (value / 1024 ** unit).toLocaleString(undefined, {maximumFractionDigits: unit ? 1 : 0}) + ' ' + ['B','KiB','MiB','GiB','TiB'][unit];
};
let api, boot, draft = {schema:1,source:'',destination:'',files:[],options:[]}, review, catalogue = new Map(), state = null;
let reviewSequence = 0, reviewTimer, toastTimer, pollTimer, pollBusy = false;
let activeJob, previewJob, previewSummary, completedID = 0, latestRecordID = '';
const rt = window.runtime;

function toast(message, error = false) {
  $('toast').textContent = message;
  $('toast').classList.toggle('error', error);
  $('toast').hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {$('toast').hidden = true;}, error ? 9000 : 4500);
}
const attempt = action => async () => {try {await action();} catch (error) {toast(errorText(error), true);}};

function applyTheme(choice) {
  if (!['auto','light','dark'].includes(choice)) choice = 'auto';
  if (choice === 'auto') document.documentElement.removeAttribute('data-theme');
  else document.documentElement.dataset.theme = choice;
  $('theme').value = choice;
  try {localStorage.setItem('ztg-copy:theme', choice);} catch {}
  const color = getComputedStyle(document.documentElement).getPropertyValue('--bg').trim();
  const rgb = color.match(/[a-f0-9]{2}/gi)?.map(s => parseInt(s, 16));
  if (rgb?.length === 3) rt?.WindowSetBackgroundColour?.(...rgb, 255);
  if (choice === 'auto') rt?.WindowSetSystemDefaultTheme?.();
  else if (choice === 'dark') rt?.WindowSetDarkTheme?.();
  else rt?.WindowSetLightTheme?.();
}
try {applyTheme(localStorage.getItem('ztg-copy:theme') || 'auto');} catch {applyTheme('auto');}
$('theme').addEventListener('change', () => applyTheme($('theme').value));
matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {if ($('theme').value === 'auto') applyTheme('auto');});
if (rt?.WindowMinimise) {
  $('window-controls').hidden = false;
  $('win-min').onclick = () => rt.WindowMinimise();
  $('win-max').onclick = attempt(async () => {
    await rt.WindowToggleMaximise();
    $('win-max').setAttribute('aria-label', (await rt.WindowIsMaximised()) ? 'Restore' : 'Maximize');
  });
  $('win-close').onclick = () => rt.Quit();
  $('titlebar').ondblclick = event => {
    if (getComputedStyle(event.target).getPropertyValue('--wails-draggable').trim() === 'drag') rt.WindowToggleMaximise();
  };
}

async function navigate(name) {
  for (const node of document.querySelectorAll('.view')) node.hidden = node.id !== 'view-' + name;
  for (const node of document.querySelectorAll('[data-view]')) {
    node.classList.toggle('active', node.dataset.view === name);
    if (node.dataset.view === name) node.setAttribute('aria-current', 'page');
    else node.removeAttribute('aria-current');
  }
  if (name === 'templates') await loadTemplates();
  if (name === 'history') await loadHistory();
}
for (const node of document.querySelectorAll('[data-view]')) node.onclick = attempt(() => navigate(node.dataset.view));
for (const id of ['back-design','return-design','templates-design']) $(id).onclick = attempt(() => navigate('design'));

function readDraft() {
  draft.name = $('job-name').value;
  draft.source = $('source').value;
  draft.destination = $('destination').value;
  draft.files = lines($('files').value);
  return clone(draft);
}
function installJob(j) {
  if (!j || j.schema !== 1) return;
  draft = clone(j);
  draft.options ||= [];
  draft.files ||= [];
  $('job-name').value = draft.name || '';
  $('source').value = draft.source || '';
  $('destination').value = draft.destination || '';
  $('files').value = draft.files.join('\n');
  invalidatePreview();
  renderChips();
  requestReview();
}
function invalidatePreview() {
  previewJob = null;
  $('preview-actions').hidden = true;
}
function requestReview() {
  invalidatePreview();
  clearTimeout(reviewTimer);
  const sequence = ++reviewSequence;
  const j = readDraft();
  $('preview').disabled = true;
  reviewTimer = setTimeout(async () => {
    try {
      const next = await api.Describe(j);
      if (sequence !== reviewSequence) return;
      review = next;
      renderReview();
    } catch (error) {if (sequence === reviewSequence) toast(errorText(error), true);}
  }, 140);
  $('filter-summary').textContent = draft.files.length ? draft.files.join(', ') : 'All files';
}
for (const id of ['job-name','source','destination','files']) $(id).addEventListener('input', requestReview);
function renderReview() {
  const area = $('validation');
  area.replaceChildren();
  const empty = !draft.source || !draft.destination;
  if (empty) area.append(make('p', 'Choose a source and destination to begin.'));
  else if (review.can_run) area.append(make('p', 'Ready for a folder check and preview.', 'ready'));
  else area.append(make('p', review.restriction || 'Review the job before continuing.', 'issue'));
  const issues = review.issues || [];
  if (!empty && issues.length) {
    const list = make('ul');
    for (const issue of issues.slice(0, 12)) list.append(make('li', (issue.argument ? issue.argument + ': ' : '') + issue.message, 'issue'));
    area.append(list);
  }
  $('review-badge').textContent = empty ? 'Set folders' : review.can_run ? 'Ready' : 'Review';
  $('review-badge').className = 'badge' + (empty ? '' : review.can_run ? ' ok' : ' warn');
  $('command').textContent = review.command || 'Choose folders to see the command.';
  $('preview').disabled = !review.can_run || Boolean(state?.running);
  $('copy-command').disabled = !review.can_run;
  renderChips();
}
function groups() {
  const result = [];
  const options = draft.options || [];
  for (let i = 0; i < options.length; i++) {
    const start = i, option = catalogue.get(key(options[i]));
    if (option?.argument === 'list') while (i + 1 < options.length && !options[i + 1].startsWith('/')) i++;
    result.push({start, end: i + 1, args: options.slice(start, i + 1), option});
  }
  return result;
}
function renderChips() {
  $('selected-options').replaceChildren();
  for (const group of groups()) {
    const button = make('button', undefined, 'option-chip' + (!group.option || !group.option.available ? ' warning' : ''));
    button.type = 'button';
    button.title = (group.option?.help || 'Unknown option, retained for review') + '. Click to edit or remove.';
    button.append(make('span', group.args.join(' ')), make('span', '×', 'remove'));
    button.disabled = Boolean(state?.running);
    button.onclick = () => editOption(group.option, group);
    $('selected-options').append(button);
  }
}
function renderCatalog() {
  if (!boot) return;
  const query = $('option-search').value.toLowerCase().trim();
  const full = $('show-reference').checked;
  const entries = boot.catalog.filter(o => (o.available || full) && (o.token + ' ' + o.help + ' ' + o.handling + ' ' + o.group).toLowerCase().includes(query));
  $('catalog').replaceChildren();
  for (const option of entries) {
    const row = make('div', undefined, 'catalog-row');
    row.append(make('span', option.token, 'catalog-token'));
    const detail = make('div');
    detail.append(make('p', option.help));
    if (!option.available) detail.append(make('span', 'REFERENCE ONLY · copying not enabled', 'reference'));
    const button = make('button', option.available ? 'Add' : 'Explain');
    button.type = 'button';
    button.disabled = Boolean(state?.running);
    button.onclick = () => editOption(option);
    row.append(detail, button);
    $('catalog').append(row);
  }
  if (!entries.length) $('catalog').append(make('p', 'No matching options.', 'field-note'));
}
$('option-search').oninput = renderCatalog;
$('show-reference').onchange = renderCatalog;

let modalSubmit;
function closeModal() {$('modal').close(); modalSubmit = null;}
$('modal-close').onclick = closeModal;
$('modal-cancel').onclick = closeModal;
$('modal').addEventListener('cancel', () => {modalSubmit = null;});
$('modal-form').onsubmit = async event => {
  event.preventDefault();
  if (!modalSubmit) return;
  $('modal-submit').disabled = true;
  $('modal-error').hidden = true;
  try {await modalSubmit(); closeModal();}
  catch (error) {$('modal-error').textContent = errorText(error); $('modal-error').hidden = false;}
  finally {$('modal-submit').disabled = false;}
};
function modal(title, label, build, submit) {
  $('modal-title').textContent = title;
  $('modal-submit').textContent = label;
  $('modal-submit').hidden = !submit;
  $('modal-submit').disabled = false;
  $('modal-error').hidden = true;
  $('modal-body').replaceChildren();
  build($('modal-body'));
  modalSubmit = submit;
  $('modal').showModal();
}
function field(parent, label, id, value = '', textarea = false) {
  const wrap = make('div', undefined, 'dialog-field');
  const text = make('label', label); text.htmlFor = id;
  const input = make(textarea ? 'textarea' : 'input'); input.id = id; input.value = value;
  if (textarea) input.rows = 5;
  input.spellcheck = false;
  wrap.append(text, input); parent.append(wrap);
  return input;
}
function editOption(option, group) {
  if (!option) {
    modal('Unrecognized option', 'Remove option', body => {
      body.append(make('p', 'This argument is retained exactly as imported. Remove it or edit the original command before running.', 'dialog-help'), make('pre', group.args.join('\n'), 'dialog-code'));
    }, async () => {draft.options.splice(group.start, group.end - group.start); requestReview();});
    return;
  }
  let input;
  modal(option.syntax, group ? 'Save option' : option.available ? 'Add option' : '', body => {
    body.append(make('p', option.help, 'dialog-help'));
    if (option.handling) body.append(make('p', option.handling, 'dialog-help'));
    if ((option.tags || []).length) body.append(make('p', 'Tags: ' + option.tags.join(', '), 'tag-list'));
    if (!option.available) body.append(make('p', 'This option can be explained and retained in imported jobs. The current copy runner does not enable it.', 'dialog-help'));
    if (option.argument !== 'none' && option.available) {
      const existing = group ? option.argument === 'list' ? group.args.slice(1).join('\n') : group.args[0].split(':').slice(1).join(':') : ({'/COPY':'DAT','/DCOPY':'DAT','/MT':'8','/R':'2','/W':'5','/IORATE':'1048576'})[option.token] || '';
      input = field(body, option.argument === 'list' ? 'One value per line' : 'Value' + (option.argument === 'optional' ? ' (optional)' : ''), 'option-value', existing, option.argument === 'list');
      input.required = option.argument === 'value' || option.argument === 'list';
    }
    if (group) {
      const remove = make('button', 'Remove this option', 'danger'); remove.type = 'button';
      remove.onclick = () => {draft.options.splice(group.start, group.end - group.start); closeModal(); requestReview();};
      body.append(remove);
    }
  }, option.available ? async () => {
    let args = [option.token];
    if (option.argument === 'list') args.push(...lines(input.value));
    else if (input && input.value) args = [option.token + ':' + input.value.trim()];
    const next = clone(draft);
    if (group) next.options.splice(group.start, group.end - group.start, ...args);
    else {
      if (next.options.some(a => key(a) === option.token)) throw new Error('This option is already selected. Click its chip to edit it.');
      next.options.push(...args);
    }
    const result = await api.Describe(next);
    const issue = (result.issues || []).find(i => i.severity === 'error' && (key(i.argument) === option.token || i.message.includes('conflicts')));
    if (issue) throw new Error(issue.argument + ': ' + issue.message);
    draft = next; requestReview();
  } : null);
}
for (const node of document.querySelectorAll('[data-preset]')) node.onclick = () => {
  draft.options = ['/E','/MT:8','/DCOPY:DAT','/R:2','/W:5','/XJ'];
  if (node.dataset.preset === 'newer') draft.options.push('/XO');
  if (node.dataset.preset === 'serial') draft.options = draft.options.filter(a => key(a) !== '/MT');
  for (const p of document.querySelectorAll('[data-preset]')) p.classList.toggle('selected', p === node);
  requestReview();
};
for (const button of document.querySelectorAll('[data-browse]')) button.onclick = attempt(async () => {
  const path = await api.PickDirectory(button.dataset.browse);
  if (path) {$(button.dataset.browse).value = path; requestReview();}
});
$('swap').onclick = () => {const source = $('source').value; $('source').value = $('destination').value; $('destination').value = source; requestReview();};
$('records-location').onclick = attempt(async () => {const root = await api.ChooseRecordsDirectory(); if (root) {$('records-path').textContent = root; invalidatePreview(); toast('Records location changed. Preview the job again before copying.');}});
$('copy-command').onclick = attempt(async () => {
  if (!review?.command) return;
  if (rt?.ClipboardSetText) await rt.ClipboardSetText(review.command);
  else await navigator.clipboard.writeText(review.command);
  toast('PowerShell command copied.');
});
$('import-job').onclick = attempt(async () => {const result = await api.ImportJob(); if (result?.job?.schema) {installJob(result.job); toast('Job imported. Review its folders and options.');}});
$('export-job').onclick = attempt(async () => {const name = await api.ExportJob(readDraft()); if (name) toast('Exported to ' + name);});
$('import-command').onclick = () => {
  let command;
  modal('Explain a Robocopy command', 'Import and explain', body => {
    body.append(make('p', 'Paste a command beginning with robocopy. Unknown arguments are retained for review. Shell scripts and environment expansion are not imported.', 'dialog-help'));
    command = field(body, 'Robocopy command', 'import-command-text', '', true); command.required = true;
  }, async () => {const result = await api.Explain(command.value); installJob(result.job);});
};
$('save-template').onclick = () => {
  let id, description, ask;
  modal('Save as template', 'Save template', body => {
    id = field(body, 'Name (lowercase letters, numbers and dashes)', 'template-name'); id.required = true; id.pattern = '[a-z0-9][a-z0-9-]{0,63}';
    description = field(body, 'Description', 'template-description');
    const label = make('label', undefined, 'check-label dialog-field'); ask = make('input'); ask.type = 'checkbox'; ask.checked = true; ask.id = 'template-ask';
    label.append(ask, document.createTextNode('Ask for source and destination each time')); body.append(label);
    body.append(make('p', 'Existing template files are kept. Use a new name to save a variation.', 'dialog-help'));
  }, async () => {await api.SaveTemplate(id.value, description.value, readDraft(), ask.checked); await loadTemplates(); toast('Template saved.');});
};

async function startOperation(copyData, j) {
  activeJob = clone(j);
  const id = await api.Start(activeJob, copyData);
  if (!id) return;
  invalidatePreview();
  await navigate('activity');
  clearTimeout(pollTimer);
  await poll();
}
$('preview').onclick = attempt(() => startOperation(false, readDraft()));
$('start-copy').onclick = () => {
  if (!previewJob) return;
  const approved = clone(previewJob);
  modal('Start this copy?', 'Start copy', body => {
    body.append(make('p', 'Matching files in the destination may be replaced. Your source files will stay in place.', 'dialog-help'));
    body.append(make('pre', approved.source + '\n→ ' + approved.destination, 'dialog-code'));
    if (previewSummary) body.append(make('p', number(previewSummary.files.copied) + ' files · ' + bytes(previewSummary.bytes.copied) + ' planned. A fresh preview and convergence check will run.', 'dialog-help'));
  }, () => startOperation(true, approved));
};
$('stop').onclick = attempt(async () => {await api.Stop(); $('phase').textContent = 'Stopping'; await poll();});
function lockForm(running) {
  for (const node of document.querySelectorAll('#job-name,#source,#destination,#files,[data-browse],[data-preset],#swap,#save-template,#import-job,#import-command,#records-location,#option-search,#show-reference')) node.disabled = running;
  $('preview').disabled = running || !review?.can_run;
  $('running-dot').hidden = !running;
  renderChips(); renderCatalog();
}
function elapsed(start, finished) {
  const seconds = Math.max(0, Math.floor(((finished ? new Date(finished).getTime() : Date.now()) - new Date(start).getTime()) / 1000));
  if (!Number.isFinite(seconds)) return '00:00';
  return [Math.floor(seconds / 3600), Math.floor(seconds / 60) % 60, seconds % 60].map(n => String(n).padStart(2,'0')).slice(seconds >= 3600 ? 0 : 1).join(':');
}
function renderState(next) {
  state = next;
  lockForm(next.running);
  $('stop').disabled = !next.running;
  $('activity-title').textContent = next.mode === 'run' ? 'Copy operation' : next.id ? 'Job preview' : 'No operation yet';
  const currentJob = next.record?.job || activeJob;
  if (currentJob) $('activity-subtitle').textContent = currentJob.source + ' → ' + currentJob.destination;
  const labels = {planned:'Preview ready',copied:'Copy complete',review:'Review required',failed:'Copy failed',error:'Operation failed',canceled:'Operation stopped',invalid:'Options rejected','preview-failed':'Preview needs review'};
  $('phase').textContent = labels[next.phase] || next.phase || 'Ready';
  $('elapsed').textContent = next.id ? elapsed(next.started, next.running ? null : next.record?.finished) : '00:00';
  $('activity-progress').hidden = !next.running;
  $('activity-indicator').className = 'status-orb' + (next.running ? ' running' : ['planned','copied'].includes(next.phase) ? ' complete' : '');
  $('activity-log').textContent = next.messages?.length ? next.messages.join('\n') : next.running ? 'Checking folders and preparing the run record…' : 'No activity yet.';
  const phases = next.record?.phases || next.phases || [];
  const copied = phases.find(p => p.name === 'copy' && p.summary);
  const preview = phases.find(p => p.name === 'preview' && p.summary);
  const summary = copied?.summary || preview?.summary || (next.mode === 'run' ? previewSummary : null);
  $('files-stat-label').textContent = copied ? 'FILES COPIED' : 'FILES TO COPY';
  $('files-total').textContent = summary ? number(summary.files.copied) : '—';
  $('bytes-total').textContent = summary ? bytes(summary.bytes.copied) : '—';
  $('extras-total').textContent = summary ? number(summary.files.extras) : '—';
  $('outcome').textContent = next.record ? (next.record.normalized_exit === 0 ? 'Passed' : next.record.normalized_exit === 1 ? 'Review' : next.phase === 'canceled' ? 'Stopped' : 'Failed') : '—';
  $('progress-note').textContent = next.running ? 'Phase status is live. File and byte counts update after each phase; a file announcement does not establish completion.' : next.record?.mode === 'plan' ? 'These are preview totals. Extra destination files are listed and will be kept by an ordinary copy.' : 'The final record includes native logs and completed phase results.';
  const verification = phases.find(p => p.name === 'convergence' && p.summary);
  $('verification').hidden = next.mode !== 'run' || !next.id;
  if (verification) {
    const counts = verification.summary;
    const remaining = counts.files.copied + counts.directories.copied;
    const mismatched = counts.files.mismatch + counts.directories.mismatch;
    const failed = counts.files.failed + counts.directories.failed;
    const extras = counts.files.extras + counts.directories.extras;
    const passed = remaining === 0 && mismatched === 0 && failed === 0 && extras === 0;
    $('verification-title').textContent = 'Convergence check';
    $('verification-badge').textContent = passed ? 'Passed' : 'Review';
    $('verification-badge').className = 'badge ' + (passed ? 'ok' : 'warn');
    $('verification-detail').textContent = passed ? 'No remaining copies, mismatches, failures or extra items were found.' : number(remaining) + ' items remain to copy · ' + number(mismatched) + ' mismatched · ' + number(failed) + ' failed · ' + number(extras) + ' extra destination items kept.';
  } else {
    $('verification-title').textContent = next.running ? 'Convergence check follows the copy' : 'Verification not completed';
    $('verification-badge').textContent = next.running ? 'Pending' : 'Incomplete';
    $('verification-badge').className = 'badge' + (next.running ? '' : ' warn');
    $('verification-detail').textContent = next.running ? 'The destination will be checked after Robocopy finishes.' : 'Review the run record before relying on this copy.';
  }
  const engineFailure = ['failed','invalid','preview-failed'].includes(next.phase);
  $('operation-error').hidden = !next.error && next.phase !== 'canceled' && !engineFailure;
  $('operation-error').textContent = next.phase === 'canceled' ? 'The operation was stopped. Some files may have copied; review the native log before trying again.' : next.error || (engineFailure ? 'Robocopy did not approve this operation. Open the run records to review its native log.' : '');
  const completed = name => phases.some(p => p.name === name && !p.error && !p.process?.canceled && (p.process?.native_exit ?? 0) < 8 && (name === 'validate' || p.summary));
  const finishedSteps = [completed('validate') || completed('preview'), completed('preview'), completed('copy'), completed('convergence')];
  const livePhase = {'Validating options':0,'Previewing':1,'Copying':2,'Checking convergence':3}[next.phase];
  const currentPhase = next.running ? livePhase ?? (finishedSteps[2] ? 3 : finishedSteps[1] ? 2 : 0) : next.phase === 'planned' ? 1 : -1;
  for (const [index, node] of [...$('steps').children].entries()) {
    node.classList.toggle('active', index === currentPhase);
    node.classList.toggle('done', finishedSteps[index]);
  }
  $('finished-actions').hidden = next.running || !next.record?.directory;
  $('open-latest').disabled = !next.record?.directory;
  if (next.record?.directory) {
    const pieces = next.record.directory.replaceAll('\\','/').split('/');
    latestRecordID = pieces.slice(-2).join('/');
  }
  if (!next.running && next.id > 0 && completedID !== next.id) {
    completedID = next.id;
    if (next.mode === 'plan' && next.phase === 'planned' && !next.error && next.record?.normalized_exit === 0) {
      previewJob = clone(activeJob || next.record.job);
      previewSummary = preview?.summary;
    }
  }
  $('preview-actions').hidden = next.running || !previewJob || next.phase !== 'planned';
}
async function poll() {
  if (pollBusy) return;
  pollBusy = true;
  try {
    renderState(await api.Status());
    if (state.running) pollTimer = setTimeout(poll, 500);
  } catch (error) {toast(errorText(error), true); pollTimer = setTimeout(poll, 2000);}
  finally {pollBusy = false;}
}
setInterval(() => {if (state?.running) $('elapsed').textContent = elapsed(state.started);}, 1000);
$('open-latest').onclick = attempt(async () => {if (latestRecordID) await api.RevealRecord(latestRecordID);});

async function loadTemplates() {
  const templates = await api.Templates();
  $('template-count').textContent = templates.length;
  $('templates-empty').hidden = templates.length > 0;
  $('templates-list').replaceChildren();
  for (const item of templates) {
    const t = item.template;
    const card = make('article', undefined, 'card template-card');
    card.append(make('h2', t.id), make('p', t.description || 'Saved copy job', 'description'), make('pre', t.job.source + '\n→ ' + t.job.destination));
    const tags = [];
    if (t.fields?.length) tags.push(t.fields.length + ' fields to fill');
    if (!item.view.can_run) tags.push('Review options before running');
    if (tags.length) card.append(make('p', tags.join(' · '), 'field-note'));
    const actions = make('div', undefined, 'toolbar');
    const use = make('button', 'Use template', 'primary');
    use.onclick = attempt(async () => {
      if (state?.running) throw new Error('Stop the current operation before changing jobs.');
      if (!t.fields?.length) {const result = await api.ResolveTemplate(t.id, {}); installJob(result.job); await navigate('design'); return;}
      const inputs = new Map();
      modal('Use ' + t.id, 'Open in designer', body => {
        body.append(make('p', 'Fill in the requested values. The job will still need a preview.', 'dialog-help'));
        for (const option of t.fields) {
          const input = field(body, option.description || option.name, 'template-field-' + option.name, option.default || '');
          input.required = true;
          if (option.kind === 'number') input.inputMode = 'numeric';
          inputs.set(option.name, input);
        }
      }, async () => {
        const values = Object.fromEntries([...inputs].map(([name,input]) => [name,input.value]));
        const result = await api.ResolveTemplate(t.id, values); installJob(result.job); await navigate('design');
      });
    });
    const exportButton = make('button', 'Export'); exportButton.onclick = attempt(async () => {const name = await api.ExportTemplate(t.id); if (name) toast('Exported to ' + name);});
    actions.append(use, exportButton); card.append(actions); $('templates-list').append(card);
  }
}
$('import-template').onclick = attempt(async () => {
  const item = await api.InspectTemplate();
  if (!item) return;
  modal('Review imported template', 'Accept and save', body => {
    body.append(make('p', 'Review the complete template before adding it to your library. Unknown options remain flagged; imported templates never skip preview or guards.', 'dialog-help'));
    body.append(make('pre', JSON.stringify(item.template, null, 2), 'dialog-code'));
    if (item.view.restriction) body.append(make('p', item.view.restriction, 'error-text'));
  }, async () => {await api.AcceptTemplate(item.template, true); await loadTemplates(); toast('Template added to your library.');});
});
async function loadHistory() {
  const rows = await api.History();
  $('history-empty').hidden = rows.length > 0; $('history-wrap').hidden = rows.length === 0;
  $('history-list').replaceChildren();
  for (const item of rows) {
    const row = make('tr');
    row.append(make('td', new Date(item.started).toLocaleString()));
    const path = make('td'); path.append(make('span', item.source, 'history-path'), make('span', '→ ' + item.destination, 'history-path')); row.append(path);
    row.append(make('td', item.mode === 'plan' ? 'Preview' : 'Copy'));
    const outcome = make('td'); outcome.append(make('span', item.state, 'state-tag ' + (['planned','copied','review','failed','error','canceled'].includes(item.state) ? item.state : ''))); row.append(outcome);
    const actions = make('td');
    const again = make('button', 'Load job'); again.onclick = attempt(async () => {if (state?.running) throw new Error('Stop the current operation first.'); const result = await api.HistoryJob(item.id); installJob(result.job); await navigate('design');});
    const open = make('button', 'Records'); open.onclick = attempt(() => api.RevealRecord(item.id));
    actions.append(again, open); row.append(actions); $('history-list').append(row);
  }
}
$('refresh-history').onclick = attempt(loadHistory);
$('check-updates').onclick = () => showUpdateDialog({tool:'copy',current:boot?.version || 'dev',openExternal:url => rt?.BrowserOpenURL?.(url)});

async function init() {
  for (let count = 0; count < 100 && !window.go?.main?.App; count++) await new Promise(resolve => setTimeout(resolve, 50));
  api = window.go?.main?.App;
  if (!api) throw new Error('The desktop bridge is unavailable. Open the Windows application to use Copy.');
  boot = await api.Bootstrap();
  catalogue = new Map(boot.catalog.map(option => [option.token, option]));
  document.title = 'ZTG Copy ' + boot.version;
  $('version').textContent = boot.version;
  rt?.WindowSetTitle?.(document.title);
  $('records-path').textContent = boot.records || 'Choose a local records directory';
  installJob(boot.default_job);
  renderCatalog();
  await loadTemplates();
  await poll();
}
init().catch(error => toast(errorText(error), true));
