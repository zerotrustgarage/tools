const clone = value => JSON.parse(JSON.stringify(value));
const splitLines = value => value.split(/\r?\n/).map(s => s.trim()).filter(Boolean);

export async function initBits({api, make, modal, field, toast, attempt}) {
  const $ = id => document.getElementById(id);
  const [catalog, initial] = await Promise.all([api.BitsCatalog(), api.DefaultBitsJob()]);
  const lookup = new Map(catalog.map(option => [option.name.toLowerCase(), option]));
  let draft = clone(initial), review = null, sequence = 0, timer;
  const read = () => {
    const sources = splitLines($('bits-source').value), destinations = splitLines($('bits-destination').value);
    draft.transfers = Array.from({length:Math.max(sources.length,destinations.length)}, (_,i) => ({source:sources[i] || '',destination:destinations[i] || ''}));
    return clone(draft);
  };
  const lockActions = disabled => {
    for (const id of ['bits-copy','bits-export','bits-save']) $(id).disabled = disabled;
  };
  const refresh = () => {
    clearTimeout(timer);
    const id = ++sequence, j = read();
    review = null; lockActions(true);
    $('bits-command').textContent = 'Checking this design…';
    renderChips();
    timer = setTimeout(async () => {
      try {
        const result = await api.DescribeBits(j);
        if (id !== sequence) return;
        review = result;
        const errors = result.issues.filter(issue => issue.severity === 'error');
        $('bits-badge').textContent = result.valid ? 'Ready' : j.transfers.length ? 'Review' : 'Set files';
        $('bits-badge').className = 'badge' + (result.valid ? ' ok' : errors.length ? ' warn' : '');
        $('bits-validation').replaceChildren();
        if (result.valid) $('bits-validation').append(make('p','Ready to copy or export this command.','ready'));
        for (const issue of result.issues.slice(0,16)) $('bits-validation').append(make('p',(issue.argument ? issue.argument + ': ' : '') + issue.message,issue.severity === 'error' ? 'issue' : 'field-note'));
        $('bits-command').textContent = result.command || 'Complete the file pairs and resolve the issues above.';
        $('bits-count').textContent = j.transfers.length + (j.transfers.length === 1 ? ' file pair' : ' file pairs');
        lockActions(!result.valid);
      } catch (error) { if (id === sequence) toast(error?.message || String(error), true); }
    },140);
  };
  function install(j) {
    draft = clone(j);
    draft.options ||= []; draft.transfers ||= [];
    $('bits-source').value = draft.transfers.map(file=>file.source).join('\n');
    $('bits-destination').value = draft.transfers.map(file=>file.destination).join('\n');
    refresh();
  }
  function renderChips() {
    $('bits-selected').replaceChildren();
    draft.options.forEach((option,index) => {
      const entry = lookup.get(option.name.toLowerCase());
      const button = make('button','-' + option.name + (option.values?.length ? ': ' + option.values.join(', ') : '') + ' ×','option-chip' + (!entry?.available ? ' warning' : ''));
      button.type = 'button'; button.title = entry?.help || 'Unknown BITS parameter, retained for review';
      button.onclick = () => edit(entry, index);
      $('bits-selected').append(button);
    });
  }
  function renderCatalog() {
    const query = $('bits-search').value.trim().toLowerCase();
    $('bits-catalog').replaceChildren();
    for (const option of catalog.filter(entry => (entry.available || $('bits-reference').checked) && (entry.name+' '+entry.help+' '+entry.group+' '+entry.values.join(' ')).toLowerCase().includes(query))) {
      const row = make('div',undefined,'catalog-row'), detail = make('div');
      detail.append(make('p',option.help));
      if (option.default_value) detail.append(make('span','BITS default: '+option.default_value,'field-note'));
      if (!option.available) detail.append(make('span','REFERENCE ONLY','reference'));
      const add = make('button',option.available ? 'Add' : 'Explain');
      add.type = 'button'; add.onclick = () => edit(option);
      row.append(make('span','-'+option.name,'catalog-token'),detail,add);
      $('bits-catalog').append(row);
    }
    if (!$('bits-catalog').children.length) $('bits-catalog').append(make('p','No matching BITS parameters.','field-note'));
  }
  function edit(entry, index) {
    const selected = Number.isInteger(index) ? draft.options[index] : null;
    let input;
    const name = entry?.name || selected.name;
    const build = body => {
      body.append(make('p',entry?.help || 'This parameter is retained exactly as imported. Remove it before copying the command.','dialog-help'));
      if (entry?.default_value) body.append(make('p','BITS default: '+entry.default_value,'field-note'));
      if (entry?.kind === 'prompt') body.append(make('p','The command inserts (Get-Credential). Credentials are requested by PowerShell when you run it.','dialog-help'));
      if (entry?.available && !['switch','prompt'].includes(entry.kind)) {
        const value = selected?.values?.[0] || entry.default_value || '';
        if (entry.kind === 'enum') {
          const wrap = make('div',undefined,'dialog-field'), label = make('label','Value');
          label.htmlFor = 'bits-option-value'; input = make('select'); input.id = 'bits-option-value';
          for (const choice of entry.values) { const item = make('option',choice); item.value = choice; input.append(item); }
          input.value = entry.values.find(choice=>choice.toLowerCase()===value.toLowerCase()) || entry.values[0];
          wrap.append(label,input); body.append(wrap);
        } else {
          input = field(body,entry.kind === 'list' ? 'One value per line' : 'Value','bits-option-value',entry.kind === 'list' ? (selected?.values || []).join('\n') : value,entry.kind === 'list');
          input.required = true;
          if (entry.kind === 'integer') { input.inputMode = 'numeric'; input.pattern = '[0-9]+'; }
        }
      }
      if (selected) {
        if (!entry?.available) body.append(make('pre',JSON.stringify(selected,null,2),'dialog-code'));
        const remove = make('button','Remove this option','danger'); remove.type = 'button';
        remove.onclick = () => {draft.options.splice(index,1); document.getElementById('modal').close(); refresh();};
        body.append(remove);
      }
    };
    modal('-'+name,entry?.available ? (selected ? 'Save option' : 'Add option') : '',build,entry?.available ? async () => {
      if (!selected && draft.options.some(option=>option.name.toLowerCase()===name.toLowerCase())) throw new Error('Already selected. Click its chip to edit it.');
      const option = {name:entry.name};
      if (input) option.values = entry.kind === 'list' ? splitLines(input.value) : [input.value];
      if (selected) draft.options.splice(index,1,option); else draft.options.push(option);
      refresh();
    } : null);
  }
  $('bits-source').oninput = refresh; $('bits-destination').oninput = refresh;
  $('bits-search').oninput = renderCatalog; $('bits-reference').onchange = renderCatalog;
  for (const button of document.querySelectorAll('[data-bits-preset]')) button.onclick = () => {
    const preset = button.dataset.bitsPreset;
    draft.options = [{name:'TransferType',values:[preset === 'upload' ? 'Upload' : 'Download']},{name:'Priority',values:[preset === 'background' ? 'Normal' : 'Foreground']}];
    if (preset === 'background') draft.options.push({name:'Asynchronous'});
    for (const item of document.querySelectorAll('[data-bits-preset]')) item.classList.toggle('selected',item===button);
    refresh();
  };
  for (const button of document.querySelectorAll('[data-bits-browse]')) button.onclick = attempt(async () => {
    const path = await api.PickBitsFile(button.dataset.bitsBrowse);
    if (!path) return;
    const input = $('bits-'+button.dataset.bitsBrowse);
    input.value = splitLines(input.value).length > 1 ? input.value.trim()+'\n'+path : path;
    refresh();
  });
  $('bits-import').onclick = attempt(async () => {
    const result = await api.ImportBitsJob();
    if (result) { install(result.job); toast('BITS job imported. Review the files and parameters.'); }
  });
  $('bits-copy').onclick = attempt(async () => {
    if (!review?.valid) return;
    if (window.runtime?.ClipboardSetText) await window.runtime.ClipboardSetText(review.command);
    else await navigator.clipboard.writeText(review.command);
    toast('BITS PowerShell command copied.');
  });
  $('bits-save').onclick = attempt(async () => {
    if (!review?.valid) return;
    const name = await api.ExportBitsJob(read()); if (name) toast('Saved BITS job to '+name);
  });
  $('bits-export').onclick = attempt(async () => {
    if (!review?.valid) return;
    const name = await api.ExportBitsScript(read()); if (name) toast('Exported PowerShell script to '+name);
  });
  install(initial); renderCatalog();
}
