// Explicit update checks only. Canonical source; Scanner's copy is checked by sync.mjs.
const API = 'https://api.github.com/repos/zerotrustgarage/tools/releases';
const REPO = 'https://github.com/zerotrustgarage/tools/releases';
const TOOLS = { scanner: 'Scanner', readout: 'Readout', copy: 'Copy', tftp: 'TFTP' };
const LIMIT = 4 * 1024 * 1024;
const VERSION = /^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;

function parse(value) {
  const match = typeof value === 'string' && VERSION.exec(value);
  if (!match || (match[4] || '').split('.').some(p => /^0\d+$/.test(p))) return null;
  return { parts: match.slice(1, 4).map(Number), pre: match[4] || '' };
}
function compare(a, b) {
  for (let i = 0; i < 3; i++) if (a.parts[i] !== b.parts[i]) return Math.sign(a.parts[i] - b.parts[i]);
  return a.pre && !b.pre ? -1 : 0;
}
function installer(tool, version) {
  return `ZTG-${TOOLS[tool]}-${version}-${tool === 'readout' ? 'Setup.exe' : 'x64.msi'}`;
}

export function releasesURL(tool) {
  if (!Object.hasOwn(TOOLS, tool)) throw new Error('Unknown ZTG tool.');
  return `${REPO}?q=${tool}-v&expanded=true`;
}

// Kept public to share version/asset selection fixtures with the Go client.
export function selectRelease(tool, current, releases) {
  const result = { tool, current, latest: '', status: 'unreleased', release_url: releasesURL(tool) };
  if (typeof current !== 'string' || current.length > 128 || /[\r\n\x00\x1b]/.test(current)) throw new Error('Invalid build version.');
  let best = null;
  for (const rel of releases) {
    if (!rel || typeof rel !== 'object' || rel.draft || rel.prerelease || !rel.published_at || typeof rel.tag_name !== 'string' || !rel.tag_name.startsWith(`${tool}-v`)) continue;
    const version = rel.tag_name.slice(tool.length + 2), parsed = parse(version);
    if (!parsed || parsed.pre || version.includes('+') || !Array.isArray(rel.assets)) continue;
    const names = new Set(rel.assets.filter(a => a && a.state === 'uploaded' && a.size > 0).map(a => a.name));
    if (!names.has('SHA256SUMS.txt') || !names.has(installer(tool, version))) continue;
    if (!best || compare(parsed, best) > 0) { best = parsed; result.latest = version; }
  }
  if (!best) return result;
  result.release_url = `${REPO}/tag/${tool}-v${result.latest}`;
  const installed = parse(current);
  result.status = !installed ? 'development' : ['available', 'current', 'newer'][compare(installed, best) + 1];
  return result;
}

export async function checkForUpdates(tool, current, { fetcher = globalThis.fetch, signal } = {}) {
  selectRelease(tool, current, []); // Validate local inputs before any network request.
  const abort = new AbortController();
  const cancel = () => abort.abort();
  if (signal?.aborted) cancel();
  signal?.addEventListener('abort', cancel, { once: true });
  const timer = setTimeout(cancel, 15000);
  const releases = [];
  try {
    for (let page = 1; page <= 10; page++) {
      const response = await fetcher(`${API}?per_page=100&page=${page}`, {
        signal: abort.signal, credentials: 'omit', redirect: 'error', cache: 'no-store', referrerPolicy: 'no-referrer',
        headers: { Accept: 'application/vnd.github+json' },
      });
      if (!response.ok) throw new Error([403, 429].includes(response.status)
        ? 'GitHub rate limit or access restriction. Try again later.'
        : `Update service returned HTTP ${response.status}. Try again later.`);
      const reader = response.body?.getReader();
      if (!reader) throw new Error('Invalid update response.');
      let bytes = 0;
      const chunks = [];
      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          bytes += value.byteLength;
          if (bytes > LIMIT) throw new Error('Update response is too large.');
          chunks.push(value);
        }
      } finally { await reader.cancel().catch(() => {}); }
      const data = new Uint8Array(bytes);
      let at = 0;
      for (const chunk of chunks) { data.set(chunk, at); at += chunk.byteLength; }
      let rows;
      try { rows = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(data)); }
      catch { throw new Error('Invalid update response.'); }
      if (!Array.isArray(rows) || rows.length > 100) throw new Error('Invalid update response.');
      releases.push(...rows);
      if (rows.length < 100) return selectRelease(tool, current, releases);
    }
    throw new Error('Release history limit reached. View GitHub releases directly.');
  } catch (error) {
    if (error instanceof TypeError || abort.signal.aborted) throw new Error('Could not check updates. Check your connection and try again.');
    throw error;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener('abort', cancel);
  }
}

export function showUpdateDialog({ tool, current, openExternal }) {
  releasesURL(tool);
  const existing = document.getElementById('ztg-update-dialog');
  if (existing) { existing.focus(); return; }
  const element = (tag, text) => { const node = document.createElement(tag); if (text) node.textContent = text; return node; };
  const dialog = element('dialog'); dialog.id = 'ztg-update-dialog'; dialog.className = 'ztg-updates';
  dialog.setAttribute('aria-labelledby', 'ztg-update-title'); dialog.tabIndex = -1;
  const heading = element('h2', 'Check for updates'); heading.id = 'ztg-update-title';
  const version = element('p', `ZTG ${TOOLS[tool]} · Installed: ${current}`);
  const status = element('p', 'Checking GitHub releases…'); status.setAttribute('role', 'status'); status.setAttribute('aria-live', 'polite');
  const help = element('p', 'Checks contact GitHub only when requested. No documents, scan results or credentials are sent. Downloads open in your browser; install the signed update when you are ready.');
  help.className = 'ztg-update-help';
  const actions = element('div'); actions.className = 'ztg-update-actions';
  const retry = element('button', 'Check again'); retry.type = 'button'; retry.disabled = true;
  const download = element('button', 'View releases'); download.type = 'button';
  const close = element('button', 'Close'); close.type = 'button'; close.autofocus = true;
  let releaseURL = releasesURL(tool), controller;
  download.addEventListener('click', async () => {
    try { await openExternal(releaseURL); }
    catch { status.textContent = 'Could not open your browser. Try again.'; }
  });
  close.addEventListener('click', () => dialog.close());
  dialog.addEventListener('close', () => { controller?.abort(); dialog.remove(); });
  async function run() {
    controller = new AbortController(); retry.disabled = true; status.textContent = 'Checking GitHub releases…';
    releaseURL = releasesURL(tool); download.textContent = 'View releases';
    try {
      const result = await checkForUpdates(tool, current, { signal: controller.signal });
      if (!dialog.isConnected) return;
      releaseURL = result.release_url;
      const messages = {
        available: `Update available: ${result.latest}.`,
        current: `You are up to date (${result.latest}).`,
        newer: `This build is newer than the latest published release (${result.latest}).`,
        development: `Development build. Latest published release: ${result.latest}.`,
        unreleased: 'No supported Windows release with an installer and checksums is published yet.',
      };
      status.textContent = messages[result.status];
      if (result.latest) download.textContent = 'View release and downloads';
    } catch (error) {
      if (dialog.isConnected) status.textContent = error instanceof Error ? error.message : 'Could not check updates. Try again.';
    } finally { retry.disabled = false; }
  }
  retry.addEventListener('click', () => void run());
  actions.append(download, retry, close); dialog.append(heading, version, status, help, actions);
  document.body.append(dialog); dialog.showModal(); void run();
}
