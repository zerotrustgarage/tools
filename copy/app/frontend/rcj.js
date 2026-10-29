// RCJ job files (design section 9.4). File names, paths and switch text come from a file
// and are untrusted: every value reaches the page through textContent only.
const MAX_PLAIN_SWITCHES = 300;

export function initRCJ({api, make, modal, toast, attempt, readDraft, loadJob, isBusy}) {
  const $ = id => document.getElementById(id);

  function listBlock(title, items) {
    const wrap = make('div', undefined, 'rcj-section');
    wrap.append(make('h3', title));
    const list = make('ul', undefined, 'rcj-list');
    for (const item of items) list.append(item);
    wrap.append(list);
    return wrap;
  }
  function flagged(text, kind) {return make('span', text, 'rcj-flag ' + kind);}

  function renderSwitch(s) {
    const li = make('li', undefined, 'rcj-item');
    const head = make('div', undefined, 'rcj-head');
    head.append(make('code', s.argument));
    if (!s.known) head.append(flagged(s.argument.startsWith('/') ? 'UNKNOWN' : 'STRAY ARGUMENT', 'warn'));
    if (s.hidden) head.append(flagged('HIDDEN', 'bad'));
    if (s.destructive) head.append(flagged('DESTRUCTIVE', 'bad'));
    if (s.known && !s.available) head.append(flagged('NOT ENABLED IN THE RUNNER', 'warn'));
    li.append(head);
    if (s.items?.length) li.append(make('code', s.items.join('\n'), 'rcj-items'));
    if (!s.known) li.append(make('p', 'Not in the option catalog. Kept exactly as imported; the job cannot run until it is removed or corrected.', 'rcj-help'));
    if (s.help) li.append(make('p', s.help, 'rcj-help'));
    if (s.handling) li.append(make('p', s.handling, 'rcj-help'));
    if (s.tags?.length) li.append(make('p', 'Tags: ' + s.tags.join(', '), 'tag-list'));
    return li;
  }

  function review(item) {
    const view = item.view, job = view.job;
    let ack;
    modal('Review imported job file', 'Load into designer', body => {
      body.append(make('p', 'Nothing has been saved or run. Review the explanation, then load the job into the designer. It still needs a preview and, for risky switches, a confirmation before anything is copied.', 'dialog-help'));
      body.append(make('p', item.file + ' · ' + item.encoding + ' · ' + item.bytes + ' bytes', 'rcj-file'));
      const filters = job.files?.length ? job.files.join(', ') : 'all files';
      body.append(make('pre', 'Source: ' + (job.source || '(not set)') + '\nDestination: ' + (job.destination || '(not set)') + '\nFile filters: ' + filters + (job.name ? '\nName: ' + job.name : ''), 'dialog-code'));
      const problems = [
        ...(view.issues || []).map(i => ({severity: i.severity, text: (i.argument ? i.argument + ': ' : '') + i.message})),
        ...(item.notes || []).map(n => ({severity: n.severity, text: 'Import: ' + (n.argument ? n.argument + ': ' : '') + n.message}))
      ];
      if (problems.length) {
        body.append(listBlock('Errors and warnings (' + problems.length + ')', problems.slice(0, 100).map(p => {
          const li = make('li', undefined, 'rcj-item');
          li.append(flagged(p.severity.toUpperCase(), p.severity === 'error' ? 'bad' : 'warn'), document.createTextNode(' '), make('span', p.text));
          return li;
        })));
        if (problems.length > 100) body.append(make('p', (problems.length - 100) + ' more are not shown here. They remain in the designer review.', 'dialog-help'));
      }
      let plain = 0, omitted = 0;
      const rows = [];
      for (const s of item.switches || []) {
        const isFlagged = !s.known || s.hidden || s.destructive;
        if (!isFlagged && ++plain > MAX_PLAIN_SWITCHES) {omitted++; continue;}
        rows.push(renderSwitch(s));
      }
      body.append(listBlock('Switches (' + (item.switches || []).length + ')', rows.length ? rows : [make('li', 'No switches besides the folders and file filters.', 'rcj-help')]));
      if (omitted) body.append(make('p', omitted + ' ordinary switches are not listed. All are kept in the job.', 'dialog-help'));
      if (view.restriction) body.append(make('p', 'Cannot run yet: ' + view.restriction, 'error-text'));
      if (item.needs_review) {
        const label = make('label', undefined, 'check-label dialog-field');
        ack = make('input'); ack.type = 'checkbox'; ack.id = 'rcj-ack'; ack.required = true;
        label.append(ack, document.createTextNode('I have reviewed the flagged items. They stay flagged in the designer.'));
        body.append(label);
      }
    }, async () => {
      if (isBusy()) throw new Error('Stop the current operation before changing jobs.');
      if (item.needs_review && !ack?.checked) throw new Error('Confirm that you reviewed the flagged items.');
      await loadJob(job);
      toast('Job file loaded. Review its folders and options, then preview.');
    });
  }

  $('import-rcj').onclick = attempt(async () => {
    if (isBusy()) throw new Error('Stop the current operation first.');
    const item = await api.ImportRCJ();
    if (item) review(item);
  });

  function exportDialog(job, first) {
    let mode = 'auto', select, text, facts;
    const show = async () => {
      const preview = await api.PreviewRCJExport(job, mode);
      text.textContent = preview.text;
      facts.textContent = 'Encoding: ' + preview.encoding + ' · ' + preview.bytes + ' bytes';
    };
    modal('Export a Robocopy job file', 'Choose file and export', body => {
      body.append(make('p', 'Writes the job as an .RCJ file. Only catalog options and the safe defaults are written, never a credential. The file is created for your account only and an existing file is never replaced. Nothing is run.', 'dialog-help'));
      const wrap = make('div', undefined, 'dialog-field');
      const label = make('label', 'Encoding'); label.htmlFor = 'rcj-encoding';
      select = make('select'); select.id = 'rcj-encoding';
      for (const [value, name] of [['auto', 'Automatic (plain ASCII, UTF-16 only when needed)'], ['utf8', 'UTF-8'], ['utf16', 'UTF-16 with byte order mark']]) {
        const option = make('option', name); option.value = value; select.append(option);
      }
      select.onchange = attempt(async () => {mode = select.value; await show();});
      wrap.append(label, select); body.append(wrap);
      facts = make('p', '', 'rcj-file'); facts.id = 'rcj-export-facts';
      text = make('pre', '', 'dialog-code'); text.id = 'rcj-export-text';
      body.append(facts, text);
      body.append(make('p', 'To have the installed Robocopy check the file afterward, run robocopy /JOB:FILE /QUIT yourself.', 'dialog-help'));
      show().catch(error => {$('modal-error').textContent = error?.message || String(error); $('modal-error').hidden = false;});
    }, async () => {
      const result = await api.ExportRCJ(job, mode);
      if (!result) return;
      setTimeout(() => modal('Job file written', 'Done', body => {
        body.append(make('pre', result.path, 'dialog-code'));
        body.append(make('p', result.encoding + ' · ' + result.bytes + ' bytes', 'rcj-file'));
        body.append(make('p', result.check, 'dialog-help'));
      }, async () => {}), 0);
    });
  }

  $('export-rcj').onclick = attempt(async () => {
    const job = readDraft();
    try {
      await api.PreviewRCJExport(job, 'auto');
    } catch (error) {
      modal('Export refused', '', body => {
        body.append(make('p', 'This job cannot be written as a job file until its errors are fixed.', 'dialog-help'));
        body.append(make('p', error?.message || String(error), 'error-text'));
      }, null);
      return;
    }
    exportDialog(job);
  });
}
