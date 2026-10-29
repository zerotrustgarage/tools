// Browser UI test for ZTG Readout, run in headless Chromium against the built browser bundle
// (readout/dist, the web platform). Usage: node uitest/run.mjs [screenshot-dir]
// Build first: npm run build --workspace @ztg-tools/readout
// Requires the playwright package (npm i -g playwright) and a Chromium.
//
// Every scenario runs in a fresh browser context (empty storage, empty file system), at a fixed
// viewport, in both colour schemes. A scenario fails on any console error, page error, failed or
// non-local request, or Content Security Policy violation, besides its own assertions. Nothing
// touches the network: every request that is not to the local server is refused and reported.
//
// What a headless browser cannot do is replaced by uitest/mock.js: the file pickers (they hand out
// real origin-private file handles), and the clipboard. The desktop-only title bar is covered by
// a small harness page that renders the real component (uitest/harness/).
import { createRequire } from 'node:module';
import { createServer } from 'node:http';
import { readFile, mkdir } from 'node:fs/promises';
import { extname, join, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import { contrastFailures, focusIndicator } from './measure.mjs';

// createRequire honors NODE_PATH, so a global playwright install works.
const { chromium } = createRequire(import.meta.url)('playwright');
const here = fileURLToPath(new URL('.', import.meta.url));
const readout = join(here, '..');
const dist = join(readout, 'dist');
const shots = process.argv[2];
const only = process.env.UITEST_ONLY ?? ''; // run the scenarios whose name contains this text
const VIEWPORT = { width: 1320, height: 820 };
const types = {
  '.html': 'text/html',
  '.css': 'text/css',
  '.js': 'text/javascript',
  '.png': 'image/png',
  '.woff2': 'font/woff2',
  '.woff': 'font/woff',
  '.ttf': 'font/ttf',
  '.svg': 'image/svg+xml',
  '.json': 'application/json',
};

// ---- servers ------------------------------------------------------------------------------

const staticServer = createServer(async (req, res) => {
  const asked = normalize(new URL(req.url, 'http://x').pathname).replace(/^(\.\.[/\\])+/, '');
  const path = asked === '/' ? '/index.html' : asked;
  try {
    const body = await readFile(join(dist, path));
    res.writeHead(200, { 'content-type': types[extname(path)] || 'application/octet-stream' });
    res.end(body);
  } catch {
    res.writeHead(404).end();
  }
}).listen(0, '127.0.0.1');
await new Promise((r) => staticServer.once('listening', r));
const appUrl = `http://127.0.0.1:${staticServer.address().port}/`;

// A Vite dev server serves only the title bar harness (the component needs no dependency, so
// nothing is pre-bundled and no reload can happen mid-test).
const { createServer: createVite } = await import('vite');
const vite = await createVite({
  configFile: false,
  root: readout,
  logLevel: 'error',
  clearScreen: false,
  appType: 'mpa',
  optimizeDeps: { noDiscovery: true, include: [] },
  server: { host: '127.0.0.1', port: 0, hmr: false, fs: { allow: [join(readout, '..')] } },
});
await vite.listen();
const harnessUrl = `http://127.0.0.1:${vite.httpServer.address().port}/uitest/harness/titlebar.html`;

const browser = await chromium.launch(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {});

// ---- fixtures -----------------------------------------------------------------------------

const corpus = (name) => readFile(join(readout, 'test', 'corpus', name), 'utf8');

const CALLOUTS = [
  '# Callouts',
  '',
  '> [!NOTE]',
  '> A note about **bold** text.',
  '',
  '> [!TIP] Custom title',
  '> A tip.',
  '',
  '> [!WARNING]-',
  '> A folded warning.',
  '',
  '!!! danger "Admonition"',
  '    MkDocs style.',
  '',
  'A plain paragraph with `code`.',
  '',
].join('\n');

const heading = (n) => `## Section ${n}`;
const filler = (n) => Array.from({ length: 45 }, (_, i) => `Line ${n}.${i + 1} of filler text.`).join('\n\n');
const OUTLINE_DOC = ['# Outline test', '', heading(1), '', filler(1), '', heading(2), '', filler(2), '', '### Last section', '', filler(3), '', 'The end.', ''].join('\n');
const lineOf = (text, line) => text.split('\n').indexOf(line) + 1;

// A document with every kind of rendered content, for the accessibility scenarios.
const A11Y_DOC = [
  '# Heading one',
  '',
  'Body text with a [link](https://example.com), `inline code`, **bold**, *italic* and ~~struck~~ words.',
  '',
  '###### Heading six',
  '',
  '> A quotation.',
  '',
  '> [!NOTE]',
  '> A note.',
  '',
  '> [!TIP]',
  '> A tip.',
  '',
  '> [!IMPORTANT]',
  '> Important.',
  '',
  '> [!WARNING]',
  '> A warning.',
  '',
  '> [!CAUTION]',
  '> Be careful.',
  '',
  '| Control | Status |',
  '|:--------|:------:|',
  '| AC.L2-3.1.1 | Met |',
  '',
  '- [x] Done',
  '- [ ] Open',
  '',
  '```js',
  'const answer = 42; // a comment',
  "function f(x) { return 'text' + x; }",
  '```',
  '',
  '```json',
  '{ "tenant": "example", "score": 87.5 }',
  '```',
  '',
  'Footnote reference[^1] and a wiki link [[Other]].',
  '',
  '[^1]: The footnote.',
  '',
].join('\n');

const BOM = [0xef, 0xbb, 0xbf];
const bytesOf = (text) => [...new TextEncoder().encode(text)];

const FOLDER = {
  'proj/README.md': '# Project\n\nSee [the guide](docs/guide/setup.md).\n',
  'proj/docs/intro.md': '# Intro\n\nThe needle is here too.\n',
  'proj/docs/guide/setup.md': '# Setup\n\nOne\n\nTwo\n\nThe needle is on line 7.\n\nEnd\n',
  'proj/notes/todo.markdown': '# Todo\n\n- [ ] nothing\n',
  'proj/node_modules/pkg/readme.md': '# Skipped\n\nneedle in a skipped folder\n',
  'proj/.hidden/secret.md': '# Hidden\n\nneedle in a hidden folder\n',
  'proj/docs/image.png': [1, 2, 3],
};

// ---- harness ------------------------------------------------------------------------------

const results = [];
let failed = false;

/** Runs one scenario in a fresh context; collects everything that must never happen. */
async function scenario(name, scheme, body, { url = appUrl, mock = true } = {}) {
  const context = await browser.newContext({ viewport: VIEWPORT, colorScheme: scheme, acceptDownloads: false });
  context.setDefaultTimeout(10_000);
  const problems = [];
  const origin = new URL(url).origin;
  await context.route('**/*', (route) => {
    const target = route.request().url();
    if (target.startsWith(origin) || target.startsWith('data:') || target.startsWith('blob:')) return route.continue();
    problems.push(`request to ${target}`);
    return route.abort();
  });
  const page = await context.newPage();
  page.on('console', (m) => m.type() === 'error' && problems.push(`console error: ${m.text()}`));
  page.on('pageerror', (e) => problems.push(`page error: ${e}`));
  page.on('requestfailed', (r) => {
    const target = r.request ? r.request().url() : r.url();
    if (target.startsWith(origin)) problems.push(`request failed: ${target}`);
  });
  page.on('response', (r) => r.status() >= 400 && problems.push(`HTTP ${r.status()}: ${r.url()}`));
  if (mock) await page.addInitScript({ path: join(here, 'mock.js') });
  const label = `${name} [${scheme}]`;
  try {
    await page.goto(url);
    await body(page, problems);
    const csp = await page.evaluate(() => window.__csp ?? []).catch(() => []);
    assert.deepEqual(csp, [], 'Content Security Policy violations');
    assert.deepEqual(problems, [], 'console, page, request or network problems');
    results.push(`ok   ${label}`);
  } catch (error) {
    failed = true;
    results.push(`FAIL ${label}\n     ${String(error.stack ?? error).split('\n').slice(0, 12).join('\n     ')}`);
    if (shots) {
      await mkdir(shots, { recursive: true });
      await page.screenshot({ path: join(shots, `fail-${name.replace(/\W+/g, '-')}-${scheme}.png`) }).catch(() => undefined);
    }
  } finally {
    await context.close();
  }
  console.log(results.at(-1));
}

const button = (page, name) => page.getByRole('button', { name, exact: true });
const editorText = (page) => page.evaluate(() => document.querySelector('.cm-content').innerText.replace(/​/g, ''));
const editorLines = async (page) => (await editorText(page)).split('\n').filter((l) => l !== '');
const cursor = (page) => page.locator('.status-bar span').first().textContent();
const banner = (page) => page.locator('.banner');
const docTitle = (page) => page.locator('.doc-title');

async function boot(page) {
  await page.waitForSelector('.cm-content');
  await page.waitForSelector('.status-bar');
}
async function openFile(page, path, text) {
  if (text !== undefined) await page.evaluate(([p, t]) => window.__fs.seed({ [p]: t }), [path, text]);
  await page.evaluate((p) => window.__fs.queueOpen(p), path);
  await button(page, 'Open file (Ctrl+O)').click();
}
async function openFolder(page, files = FOLDER, root = 'proj') {
  await page.evaluate((f) => window.__fs.seed(f), files);
  await page.evaluate((r) => window.__fs.queueDirectory(r), root);
  await button(page, 'Open folder or repository (Ctrl+Shift+O)').click();
  await page.waitForSelector('.tree-file', { state: 'attached' });
}
/**
 * Presses Tab from the top of the page until focus comes back around, and reports the focus
 * indicator of every stop. The editor swallows Tab, so Escape (tab-focus mode) leaves it first.
 */
async function tabWalk(page, max = 140) {
  await page.evaluate(() => {
    window.__walked = new WeakSet();
    document.activeElement?.blur();
  });
  const stops = [];
  for (let i = 0; i < max; i++) {
    if (await page.evaluate(() => Boolean(document.activeElement?.closest?.('.cm-content')))) await page.keyboard.press('Escape');
    await page.keyboard.press('Tab');
    const fresh = await page.evaluate(() => {
      const e = document.activeElement;
      if (!e || e === document.body || window.__walked.has(e)) return false;
      window.__walked.add(e);
      return true;
    });
    if (!fresh) break;
    // CodeMirror marks the editor as focused a moment after the focus event.
    if (await page.evaluate(() => Boolean(document.activeElement?.matches('.cm-content')))) await page.waitForSelector('.cm-editor.cm-focused');
    stops.push(await focusIndicator(page));
  }
  return stops;
}
/** The browser's accessibility tree (Chromium): role, name and properties of every node a screen reader can reach. */
async function axNodes(page) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Accessibility.enable');
  const { nodes } = await cdp.send('Accessibility.getFullAXTree');
  await cdp.detach();
  return nodes
    .filter((n) => !n.ignored)
    .map((n) => ({ role: n.role?.value ?? '', name: (n.name?.value ?? '').trim(), props: Object.fromEntries((n.properties ?? []).map((p) => [p.name, p.value?.value])) }));
}
// Roles that must carry a name wherever they appear (a task list's display-only checkbox is read
// together with its item's text and is left out).
const NAMED_ROLES = ['button', 'link', 'textbox', 'searchbox', 'treeitem', 'tree', 'separator', 'dialog', 'region', 'complementary', 'toolbar', 'navigation'];
const unnamed = (nodes) => nodes.filter((n) => NAMED_ROLES.includes(n.role) && n.name === '').map((n) => n.role);
const active = (page, selector) => page.evaluate((s) => document.activeElement?.matches(s) ?? false, selector);
const paneWidth = (page, selector) => page.evaluate((s) => document.querySelector(s).getBoundingClientRect().width, selector);
const luminance = (rgb) => {
  const [r, g, b] = rgb.match(/\d+(\.\d+)?/g).slice(0, 3).map(Number);
  return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
};

// ---- scenarios ----------------------------------------------------------------------------

const scenarios = [
  ['open and render a document', async (page) => {
    await boot(page);
    const report = await corpus('ai-report.md');
    await openFile(page, 'report/ai-report.md', report);
    await page.waitForSelector('.markdown-body h1');
    assert.equal(await page.locator('.markdown-body h1').first().textContent(), 'Quarterly Security Posture Report');
    assert.equal(await docTitle(page).textContent(), 'ai-report.md');
    assert.ok((await editorText(page)).includes('# Quarterly Security Posture Report'), 'source shown in the editor');
    assert.ok(await page.locator('.markdown-body table').count() >= 1, 'table renders');
    assert.ok(await page.locator('.markdown-body input[type="checkbox"]').count() >= 2, 'task list renders');
    assert.ok(await page.locator('.markdown-body .katex').count() >= 1, 'math renders');
    // The sanitizer keeps the document's colour span and drops nothing the page depends on.
    assert.equal(await page.locator('.markdown-body script').count(), 0);
    assert.ok(/words/.test(await page.locator('.status-bar').textContent()));
    assert.equal(await page.locator('.status-bar').getByText('UTF-8', { exact: false }).first().isVisible(), true);
    if (shots) {
      await mkdir(shots, { recursive: true });
      await page.screenshot({ path: join(shots, `render-${await page.evaluate(() => document.documentElement.dataset.theme)}.png`) });
    }
  }],

  ['source-faithful edit and save round trip', async (page) => {
    await boot(page);
    const original = '# Doc\r\n\r\nText with no final newline';
    await page.evaluate(([p, b]) => window.__fs.seed({ [p]: b }), ['work/doc.md', [...BOM, ...bytesOf(original)]]);
    await page.evaluate(() => window.__fs.queueOpen('work/doc.md'));
    await button(page, 'Open file (Ctrl+O)').click();
    await page.waitForSelector('.markdown-body h1');
    assert.equal(await page.locator('.dirty-dot').isHidden(), true);
    const status = await page.locator('.status-bar').textContent();
    assert.ok(status.includes('UTF-8 with BOM') && status.includes('CRLF'), status);

    // An edit that is undone by hand leaves the text as it was: the document is clean and a
    // save writes the original bytes (BOM, CRLF and the missing final newline included).
    await page.locator('.cm-content').click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('!');
    await page.locator('.dirty-dot').waitFor({ state: 'visible' });
    await page.keyboard.press('Backspace');
    await page.locator('.dirty-dot').waitFor({ state: 'hidden' });
    await button(page, 'Save (Ctrl+S)').click();
    await page.waitForTimeout(150);
    assert.deepEqual(await page.evaluate(() => window.__fs.bytes('work/doc.md')), [...BOM, ...bytesOf(original)], 'unedited save reproduces the bytes');

    // A real edit changes only the characters typed, and the preview follows the editor.
    await page.locator('.cm-content').click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type(' and an edit');
    await page.locator('.markdown-body', { hasText: 'and an edit' }).waitFor();
    assert.equal(await page.locator('.dirty-dot').isVisible(), true);
    assert.equal(await page.evaluate(() => document.body.dataset.dirty), 'true');
    await page.keyboard.press('Control+s');
    await page.locator('.dirty-dot').waitFor({ state: 'hidden' });
    assert.deepEqual(await page.evaluate(() => window.__fs.bytes('work/doc.md')), [...BOM, ...bytesOf(original + ' and an edit')]);
    assert.equal(await page.title(), 'doc.md - ZTG Readout');
  }],

  ['new document and Save as through a file handle', async (page) => {
    await boot(page);
    await page.locator('.cm-content').click();
    await page.keyboard.type('# Fresh\n\nwritten here\n');
    await page.evaluate(() => window.__fs.queueSave('out/fresh.md'));
    await button(page, 'Save (Ctrl+S)').click();
    await page.locator('.dirty-dot').waitFor({ state: 'hidden' });
    assert.equal(await page.evaluate(() => window.__fs.text('out/fresh.md')), '# Fresh\n\nwritten here\n');
    assert.equal(await docTitle(page).textContent(), 'fresh.md');
  }],

  ['drag and drop a file onto the window', async (page) => {
    await boot(page);
    const drag = (type, dt) => page.dispatchEvent('body', type, { dataTransfer: dt });
    const file = (name, text) =>
      page.evaluateHandle(([n, t]) => {
        const dt = new DataTransfer();
        dt.items.add(new File([t], n, { type: 'text/plain' }));
        return dt;
      }, [name, text]);
    const overlay = page.locator('.drop-overlay');

    const dropped = await file('dropped.md', '# Dropped file\n\nbody\n');
    await drag('dragenter', dropped);
    await overlay.waitFor({ state: 'visible' });
    assert.ok((await overlay.textContent()).includes('Drop a Markdown file'));
    await drag('dragleave', dropped);
    await overlay.waitFor({ state: 'hidden' });
    await drag('dragenter', dropped);
    await drag('dragover', dropped);
    await drag('drop', dropped);
    await overlay.waitFor({ state: 'hidden' });
    await page.waitForSelector('.markdown-body h1');
    assert.equal(await docTitle(page).textContent(), 'dropped.md');
    assert.equal(await page.locator('.markdown-body h1').textContent(), 'Dropped file');
    // The drop opens the file; it is not pasted into the open document.
    assert.deepEqual(await editorLines(page), ['# Dropped file', 'body']);

    // Anything that is not Markdown is refused with a message, and nothing opens.
    const image = await file('photo.png', 'not markdown');
    await drag('dragenter', image);
    await drag('drop', image);
    await banner(page).getByText('Only Markdown files', { exact: false }).waitFor();
    assert.equal(await docTitle(page).textContent(), 'dropped.md');

    // Dragging that carries no files (selected text) is left to the editor.
    const text = await page.evaluateHandle(() => {
      const dt = new DataTransfer();
      dt.setData('text/plain', 'plain text');
      return dt;
    });
    await drag('dragenter', text);
    assert.equal(await overlay.isHidden(), true);
  }],

  ['dividers: drag, keyboard, reset', async (page) => {
    await boot(page);
    await button(page, 'Split view').click();
    const handle = page.locator('.splitter-panes');
    assert.equal(await handle.getAttribute('role'), 'separator');
    assert.equal(await handle.getAttribute('aria-label'), 'Resize the editor and preview');
    const total = (await paneWidth(page, '.panes')) - (await paneWidth(page, '.splitter-panes'));
    const start = await paneWidth(page, '.editor-pane');
    assert.ok(Math.abs(start - total / 2) < 8, `starts near half (${start} of ${total})`);

    // Mouse: dragging right grows the editor, and the share is stored.
    const box = await handle.boundingBox();
    const y = box.y + box.height / 2;
    await page.mouse.move(box.x + box.width / 2, y);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 + 120, y, { steps: 6 });
    await page.mouse.up();
    const dragged = await paneWidth(page, '.editor-pane');
    assert.ok(Math.abs(dragged - (start + 120)) < 6, `drag moved the divider by 120 (${start} to ${dragged})`);
    assert.ok(Number(await page.evaluate(() => localStorage.getItem('ztg-readout-editor-fraction'))) > 0.5);
    assert.equal(await page.evaluate(() => document.body.classList.contains('resizing')), false);

    // Dragging past the end stops at the minimum width of the other pane.
    await page.mouse.move(box.x + box.width / 2 + 120, y);
    await page.mouse.down();
    await page.mouse.move(box.x + 2000, y);
    await page.mouse.up();
    const far = await paneWidth(page, '.preview-pane');
    assert.ok(far >= 215, `preview keeps its minimum width (${far})`);

    // Keyboard: arrows move it by 16, Shift by 64; Home and double-click reset.
    await handle.focus();
    await page.keyboard.press('Home');
    const home = await paneWidth(page, '.editor-pane');
    assert.ok(Math.abs(home - total / 2) < 8, `Home resets (${home})`);
    await page.keyboard.press('ArrowRight');
    assert.ok(Math.abs((await paneWidth(page, '.editor-pane')) - (home + 16)) < 2);
    await page.keyboard.press('Shift+ArrowLeft');
    assert.ok(Math.abs((await paneWidth(page, '.editor-pane')) - (home + 16 - 64)) < 2);
    assert.ok(Number(await handle.getAttribute('aria-valuenow')) > 0, 'aria-valuenow follows');
    await handle.dblclick();
    assert.ok(Math.abs((await paneWidth(page, '.editor-pane')) - total / 2) < 8, 'double-click resets');
  }],

  ['folder mode: tree, open from tree, filter, sidebar divider', async (page) => {
    await boot(page);
    assert.equal(await page.locator('.sidebar').isHidden(), true);
    await openFolder(page);
    assert.equal(await page.locator('.sidebar .sidebar-title').textContent(), 'proj');
    assert.equal(await page.locator('.sidebar-count').textContent(), '4 Markdown files');
    // The folder's README opens by itself; hidden and dependency folders are not listed.
    await page.waitForSelector('.markdown-body h1');
    assert.equal(await docTitle(page).textContent(), 'README.md');
    const listed = await page.locator('.tree-file').evaluateAll((nodes) => nodes.map((n) => n.dataset.path).sort());
    assert.deepEqual(listed, ['README.md', 'docs/guide/setup.md', 'docs/intro.md', 'notes/todo.markdown']);

    for (const folder of ['docs', 'docs/guide']) {
      const item = page.locator(`.tree-folder[data-path="${folder}"]`);
      if ((await item.getAttribute('aria-expanded')) !== 'true') await item.locator('> .tree-row').click();
    }
    await page.locator('.tree-file[data-path="docs/guide/setup.md"]').click();
    await page.locator('.markdown-body h1', { hasText: 'Setup' }).waitFor();
    assert.equal(await docTitle(page).textContent(), 'docs/guide/setup.md');
    assert.equal(await page.locator('.tree-file[data-path="docs/guide/setup.md"]').getAttribute('aria-current'), 'page');

    // A link between files of the folder opens in the app (README links to the guide).
    await page.locator('.tree-file[data-path="README.md"]').click();
    await page.locator('.markdown-body a', { hasText: 'the guide' }).click();
    await page.locator('.markdown-body h1', { hasText: 'Setup' }).waitFor();
    await button(page, 'Back').click();
    await page.locator('.markdown-body h1', { hasText: 'Project' }).waitFor();

    await page.locator('input[aria-label="Filter files"]').fill('intro');
    assert.equal(await page.locator('.tree-file').count(), 1);
    await page.locator('input[aria-label="Filter files"]').fill('');

    // Sidebar divider: drag and keyboard.
    const before = await paneWidth(page, '.sidebar');
    const handle = page.locator('.splitter-sidebar');
    const box = await handle.boundingBox();
    const y = box.y + box.height / 2;
    await page.mouse.move(box.x + box.width / 2, y);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 + 90, y, { steps: 5 });
    await page.mouse.up();
    const after = await paneWidth(page, '.sidebar');
    assert.ok(Math.abs(after - (before + 90)) < 6, `sidebar grew by 90 (${before} to ${after})`);
    assert.ok(Number(await page.evaluate(() => localStorage.getItem('ztg-readout-sidebar-width'))) > before);
    await handle.focus();
    await page.keyboard.press('ArrowLeft');
    assert.ok(Math.abs((await paneWidth(page, '.sidebar')) - (after - 16)) < 2);
    await page.keyboard.press('Home');
    assert.ok(Math.abs((await paneWidth(page, '.sidebar')) - 270) < 2, 'Home restores 270');

    await button(page, 'Close folder').click();
    await page.locator('.sidebar').waitFor({ state: 'hidden' });
  }],

  ['folder search (RD9): results, skipped folders, jump to line', async (page) => {
    await boot(page);
    assert.equal(await button(page, 'Search').isDisabled(), true, 'search needs a folder');
    await openFolder(page);
    await button(page, 'Search').click();
    const input = page.locator('input[aria-label="Search text in the open folder"]');
    await input.fill('needle');
    await input.press('Enter');
    await page.locator('.search-status', { hasText: 'matches in' }).waitFor();
    const hits = await page.locator('.search-hit').evaluateAll((nodes) => nodes.map((n) => n.title).sort());
    assert.deepEqual(hits, ['docs/guide/setup.md, line 7', 'docs/intro.md, line 3'], 'skipped folders are not searched');
    assert.ok((await page.locator('.search-panel .search-status').textContent()).startsWith('2 matches in 4 files searched'));

    await page.locator('.search-hit[title="docs/guide/setup.md, line 7"]').click();
    await page.locator('.markdown-body h1', { hasText: 'Setup' }).waitFor();
    assert.equal(await docTitle(page).textContent(), 'docs/guide/setup.md');
    assert.ok((await cursor(page)).startsWith('Ln 7,'), await cursor(page));
    assert.equal(await page.locator('.cm-selectionBackground').count() >= 1, true, 'the match is selected');

    await input.fill('no such text anywhere');
    await input.press('Enter');
    await page.locator('.tool-empty', { hasText: 'No matches.' }).waitFor();
    await button(page, 'Close search').click();
    assert.equal(await page.locator('.search-panel').isHidden(), true);
  }],

  ['title bar and toolbar in the browser build', async (page) => {
    await boot(page);
    // The browser draws no title bar of its own (the page has the browser's); that is desktop-only
    // and is checked by the harness scenario below.
    assert.equal(await page.locator('.titlebar').count(), 0);
    assert.equal(await page.evaluate(() => document.querySelector('.app').classList.contains('has-titlebar')), false);
    assert.equal(await page.title(), 'Untitled.md - ZTG Readout');
    // Every control in the app bar, formatting bar and status bar has an accessible name.
    const unnamed = await page.evaluate(() =>
      [...document.querySelectorAll('.top-bar button, .format-bar button, .status-bar button')]
        .filter((b) => !(b.getAttribute('aria-label') || b.textContent || '').trim())
        .map((b) => b.outerHTML.slice(0, 80)),
    );
    assert.deepEqual(unnamed, []);
    for (const name of [
      'New document (Ctrl+N)', 'Open file (Ctrl+O)', 'Open folder or repository (Ctrl+Shift+O)', 'Recent files',
      'Save (Ctrl+S)', 'Save as (Ctrl+Shift+S)', 'Edit view', 'Split view', 'Preview view', 'Outline', 'Search',
      'Copy as rich text', 'Export as HTML', 'Print or save as PDF (Ctrl+P)', 'Back', 'Check for updates',
    ]) {
      assert.equal(await button(page, name).count(), 1, `${name} is present once`);
    }
    assert.equal(await page.getByRole('group', { name: 'View' }).count(), 1);
    assert.equal(await page.getByRole('toolbar', { name: 'Formatting' }).count(), 1);
    assert.equal(await button(page, 'Back').isDisabled(), true);
    // View modes switch panes and say which is pressed.
    await button(page, 'Preview view').click();
    assert.equal(await button(page, 'Preview view').getAttribute('aria-pressed'), 'true');
    assert.equal(await page.locator('.editor-pane').isHidden(), true);
    await button(page, 'Edit view').click();
    assert.equal(await page.locator('.preview-pane').isHidden(), true);
    assert.equal(await page.locator('.format-bar').isVisible(), true);
  }],

  ['themes: auto follows the system, the button cycles', async (page, scheme) => {
    await boot(page);
    const theme = () => page.evaluate(() => document.documentElement.dataset.theme);
    const bg = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    assert.equal(await theme(), scheme);
    const own = luminance(await bg());
    assert.ok(scheme === 'dark' ? own < 0.3 : own > 0.7, `background luminance ${own} fits ${scheme}`);
    // Auto, light, dark, auto.
    const cycle = page.getByRole('button', { name: /^Theme: / });
    assert.ok((await cycle.getAttribute('aria-label')).startsWith('Theme: auto'));
    await cycle.click();
    assert.equal(await theme(), 'light');
    assert.ok(luminance(await bg()) > 0.7);
    await cycle.click();
    assert.equal(await theme(), 'dark');
    assert.ok(luminance(await bg()) < 0.3);
    assert.equal(await page.evaluate(() => localStorage.getItem('ztg-readout-theme')), 'dark');
    await cycle.click();
    assert.equal(await theme(), scheme);
    // The preview follows the theme: a code block keeps a readable foreground in both.
    await page.locator('.cm-content').click();
    await page.keyboard.type('```js\nconst a = 1;\n```\n');
    await page.locator('.markdown-body pre code').waitFor();
    const colors = await page.evaluate(() => {
      const pre = document.querySelector('.markdown-body pre');
      return [getComputedStyle(pre).backgroundColor, getComputedStyle(pre.querySelector('code')).color];
    });
    assert.ok(Math.abs(luminance(colors[0]) - luminance(colors[1])) > 0.3, `code block contrast ${colors}`);
  }],

  ['callouts render (RD6)', async (page) => {
    await boot(page);
    await openFile(page, 'c/callouts.md', CALLOUTS);
    await page.waitForSelector('.markdown-alert');
    const kinds = await page.locator('[data-callout]').evaluateAll((nodes) => nodes.map((n) => `${n.tagName.toLowerCase()}:${n.dataset.callout}:${n.className.replace(/.*markdown-alert-(\w+).*/, '$1')}`));
    assert.deepEqual(kinds, ['div:note:note', 'div:tip:tip', 'details:warning:warning', 'div:danger:caution']);
    assert.ok((await page.locator('[data-callout="tip"]').textContent()).includes('Custom title'));
    assert.equal(await page.locator('[data-callout="note"] strong').textContent(), 'bold');
    // A folded callout is a closed details element that opens on a click of its title.
    const folded = page.locator('details[data-callout="warning"]');
    assert.equal(await folded.evaluate((d) => d.open), false);
    await folded.locator('summary').click();
    assert.equal(await folded.evaluate((d) => d.open), true);
    // The literal marker is not left in the text.
    assert.equal((await page.locator('.markdown-body').textContent()).includes('[!NOTE]'), false);
  }],

  ['outline click jumps (RD8)', async (page) => {
    await boot(page);
    await openFile(page, 'o/outline.md', OUTLINE_DOC);
    await page.waitForSelector('.markdown-body h1');
    await button(page, 'Outline').click();
    await page.waitForSelector('.outline-item');
    const items = await page.locator('.outline-item').evaluateAll((nodes) => nodes.map((n) => [n.textContent, n.getAttribute('aria-level')]));
    assert.deepEqual(items, [['Outline test', '1'], ['Section 1', '2'], ['Section 2', '2'], ['Last section', '3']]);

    // Split view: the editor cursor moves to the heading's line.
    await page.locator('.outline-item', { hasText: 'Section 2' }).click();
    const line = lineOf(OUTLINE_DOC, heading(2));
    assert.ok((await cursor(page)).startsWith(`Ln ${line},`), `${await cursor(page)} should be line ${line}`);
    assert.equal(await page.locator('.outline-item.active').textContent(), 'Section 2');
    assert.equal(await page.locator('.outline-item.active').getAttribute('aria-current'), 'location');

    // Preview only: the heading's block scrolls to the top of the preview.
    await button(page, 'Preview view').click();
    await page.locator('.outline-item', { hasText: 'Last section' }).click();
    await page.waitForFunction(() => {
      const h = [...document.querySelectorAll('.markdown-body h3')].find((n) => n.textContent === 'Last section');
      const pane = document.querySelector('.preview-pane').getBoundingClientRect();
      return h && Math.abs(h.getBoundingClientRect().top - pane.top) < 80;
    });
    await button(page, 'Close outline').click();
    assert.equal(await page.locator('.outline-panel').isHidden(), true);
  }],

  ['mixed line endings: banner and Convert (RD15)', async (page) => {
    await boot(page);
    const mixed = [...bytesOf('# Mixed\r\ntwo\r\nthree\nfour\r\n')];
    await page.evaluate((b) => window.__fs.seed({ 'e/mixed.md': b }), mixed);
    await page.evaluate(() => window.__fs.queueOpen('e/mixed.md'));
    await button(page, 'Open file (Ctrl+O)').click();
    await page.waitForSelector('.markdown-body h1');
    assert.equal(await docTitle(page).textContent(), 'mixed.md (read-only)');
    assert.ok((await banner(page).textContent()).includes('Convert line endings'));
    assert.ok((await page.locator('.status-bar').textContent()).includes('Mixed line endings'));
    // Read-only: typing changes nothing.
    await page.locator('.cm-content').click();
    await page.keyboard.type('zzz');
    assert.equal((await editorText(page)).includes('zzz'), false);

    // Cancel keeps it as it was.
    await banner(page).getByRole('button', { name: 'Convert line endings' }).click();
    const dialog = page.locator('dialog.modal');
    await dialog.waitFor({ state: 'visible' });
    assert.ok((await dialog.textContent()).includes('Nothing is written until you save.'));
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await dialog.waitFor({ state: 'hidden' });
    assert.equal(await docTitle(page).textContent(), 'mixed.md (read-only)');

    // Convert to the majority ending: editable and unsaved, the file on disk untouched.
    await banner(page).getByRole('button', { name: 'Convert line endings' }).click();
    await dialog.getByRole('button', { name: 'Convert to CRLF (most lines)' }).click();
    await banner(page).getByText('Line endings converted to CRLF', { exact: false }).waitFor();
    assert.equal(await docTitle(page).textContent(), 'mixed.md');
    assert.ok((await page.locator('.status-bar').textContent()).includes('CRLF (converted, not saved)'));
    assert.equal(await page.locator('.dirty-dot').isVisible(), true);
    assert.deepEqual(await page.evaluate(() => window.__fs.bytes('e/mixed.md')), mixed, 'nothing written before Save');
    await button(page, 'Save (Ctrl+S)').click();
    await page.locator('.dirty-dot').waitFor({ state: 'hidden' });
    assert.deepEqual(await page.evaluate(() => window.__fs.bytes('e/mixed.md')), bytesOf('# Mixed\r\ntwo\r\nthree\r\nfour\r\n'));

    // A file that is not UTF-8 is never offered a conversion.
    await openFile(page, 'e/binary.md', [0x61, 0x80, 0x62, 0x0d, 0x0a, 0x63, 0x0a]);
    await docTitle(page).getByText('binary.md (read-only)').waitFor();
    assert.equal(await banner(page).getByRole('button', { name: 'Convert line endings' }).count(), 0);
  }],

  ['Copy as rich text writes text/html (RD11)', async (page) => {
    await boot(page);
    await openFile(page, 'r/callouts.md', CALLOUTS);
    await page.waitForSelector('.markdown-alert');
    assert.equal(await page.evaluate(() => window.__clipboard), null);
    await button(page, 'Copy as rich text').click();
    await banner(page).getByText('Copied the whole document as rich text', { exact: false }).waitFor();
    const clip = await page.evaluate(() => window.__clipboard);
    assert.deepEqual([...clip.types].sort(), ['text/html', 'text/plain']);
    assert.ok(clip.html.includes('<h1') && clip.html.includes('Custom title'), 'html carries the formatted document');
    assert.ok(clip.html.includes('<table'), 'callouts are rebuilt as tables for Outlook and Word');
    assert.equal(/<script|onerror=|javascript:/i.test(clip.html), false);
    assert.ok(clip.text.includes('Custom title') && !clip.text.includes('<'), 'plain text alternative');

    // A selection in the preview copies only that selection.
    await page.evaluate(() => {
      const p = [...document.querySelectorAll('.markdown-body p')].find((n) => n.textContent.startsWith('A plain paragraph'));
      const range = document.createRange();
      range.selectNodeContents(p);
      const selection = getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
    });
    await button(page, 'Copy as rich text').click();
    await banner(page).getByText('Copied the selection as rich text', { exact: false }).waitFor();
    const part = await page.evaluate(() => window.__clipboard);
    assert.ok(part.html.includes('A plain paragraph') && !part.html.includes('Callouts'), 'selection only');
  }],

  ['recent files panel (RD10) with file handles', async (page) => {
    await boot(page);
    const recentButton = button(page, 'Recent files');
    await recentButton.click();
    await page.locator('.tool-empty', { hasText: 'No recent files yet' }).waitFor();
    await recentButton.click();

    await openFile(page, 'rec/first.md', '# First\n');
    await page.locator('.markdown-body h1', { hasText: 'First' }).waitFor();
    await openFile(page, 'rec/second.md', '# Second\n');
    await page.locator('.markdown-body h1', { hasText: 'Second' }).waitFor();
    await openFile(page, 'rec/third.md', '# Third\n');
    await page.locator('.markdown-body h1', { hasText: 'Third' }).waitFor();
    await page.waitForTimeout(100);

    await page.keyboard.press('Control+Shift+E');
    await page.locator('.recent-panel').waitFor({ state: 'visible' });
    await page.waitForFunction(() => document.querySelectorAll('.recent-item').length === 3);
    assert.deepEqual(await page.locator('.recent-name').allTextContents(), ['third.md', 'second.md', 'first.md'], 'newest first');
    assert.equal(await recentButton.getAttribute('aria-pressed'), 'true');

    // Opening an entry reopens that file through its stored handle.
    await page.locator('.recent-item', { hasText: 'first.md' }).click();
    await page.locator('.markdown-body h1', { hasText: 'First' }).waitFor();
    assert.equal(await docTitle(page).textContent(), 'first.md');
    assert.equal(await page.locator('.recent-panel').isHidden(), true, 'the panel closes after opening');

    await recentButton.click();
    await page.waitForFunction(() => document.querySelector('.recent-name')?.textContent === 'first.md');
    await button(page, 'Remove second.md from the list').click();
    await page.waitForFunction(() => document.querySelectorAll('.recent-item').length === 2);
    await page.getByRole('button', { name: 'Clear list' }).click();
    await page.locator('.tool-empty', { hasText: 'No recent files yet' }).waitFor();
    await button(page, 'Close recent files').click();
    assert.equal(await page.locator('.recent-panel').isHidden(), true);
  }],

  ['accessibility: contrast of every surface (WCAG 2.1 AA)', async (page, scheme) => {
    await boot(page);
    const failures = {};
    const measure = async (state) => {
      await page.waitForTimeout(150);
      failures[state] = await contrastFailures(page);
    };
    await openFolder(page, { ...FOLDER, 'proj/docs/a11y.md': A11Y_DOC });
    await page.locator('.tree-file[data-path="docs/a11y.md"]').click();
    await page.locator('.markdown-body h1', { hasText: 'Heading one' }).waitFor();
    await measure('split view with folder tree');
    await page.locator('.cm-content').click();
    await page.keyboard.press('Control+f');
    await page.locator('.cm-search').waitFor();
    await measure('editor find panel');
    await page.keyboard.press('Escape');
    await button(page, 'Outline').click();
    await page.waitForSelector('.outline-item');
    await measure('outline panel');
    await button(page, 'Outline').click();
    await button(page, 'Search').click();
    await page.locator('input[aria-label="Search text in the open folder"]').fill('needle');
    await page.locator('.search-hit').first().waitFor();
    await measure('search results');
    await button(page, 'Search').click();
    await button(page, 'Recent files').click();
    await page.locator('.recent-item').first().waitFor();
    await measure('recent files');
    await button(page, 'Recent files').click();
    await button(page, 'Preview view').click();
    await measure('preview only');
    await button(page, 'Edit view').click();
    await measure('edit only');
    // A banner and a dialog.
    await openFile(page, 'e/mixed.md', [...bytesOf('# Mixed\r\ntwo\r\nthree\nfour\r\n')]);
    await banner(page).getByRole('button', { name: 'Convert line endings' }).waitFor();
    await measure('banner');
    await banner(page).getByRole('button', { name: 'Convert line endings' }).click();
    await page.locator('dialog.modal').waitFor({ state: 'visible' });
    await measure('dialog');
    const all = Object.entries(failures).flatMap(([state, list]) => list.map((line) => `[${state}] ${line}`));
    if (process.env.A11Y_DUMP) console.log(`--- ${scheme}\n${all.join('\n')}`);
    assert.deepEqual(all, [], 'contrast failures');
  }],

  ['accessibility: every focusable element shows a focus indicator', async (page) => {
    await boot(page);
    await openFolder(page, { ...FOLDER, 'proj/docs/a11y.md': A11Y_DOC });
    await page.locator('.tree-file[data-path="docs/a11y.md"]').click();
    await page.locator('.markdown-body h1', { hasText: 'Heading one' }).waitFor();
    const problems = [];
    const walk = async (state) => {
      const stops = await tabWalk(page);
      assert.ok(stops.length > 8, `${state}: the walk found ${stops.length} stops`);
      for (const s of stops) {
        if (!s.visible) problems.push(`[${state}] no focus indicator: ${s.name}`);
        else if (s.ratio < 3 || s.width < 2) problems.push(`[${state}] weak focus indicator (${s.ratio.toFixed(2)}:1, ${s.width}px): ${s.name}`);
      }
    };
    await walk('workspace');
    await button(page, 'Outline').click();
    await page.waitForSelector('.outline-item');
    await walk('outline');
    await button(page, 'Outline').click();
    await button(page, 'Search').click();
    await page.locator('input[aria-label="Search text in the open folder"]').fill('needle');
    await page.locator('.search-hit').first().waitFor();
    await walk('search');
    await button(page, 'Search').click();
    await button(page, 'Recent files').click();
    await page.locator('.recent-item').first().waitFor();
    await walk('recent files');
    if (process.env.A11Y_DUMP) console.log(problems.join('\n'));
    assert.deepEqual(problems, []);
  }],

  ['accessibility: landmarks, roles and names (screen reader tree)', async (page) => {
    await boot(page);
    await openFolder(page, { ...FOLDER, 'proj/docs/a11y.md': A11Y_DOC });
    await page.locator('.tree-file[data-path="docs/a11y.md"]').click();
    await page.locator('.markdown-body h1', { hasText: 'Heading one' }).waitFor();
    const check = async (state) => {
      const nodes = await axNodes(page);
      assert.deepEqual(unnamed(nodes), [], `${state}: every control, landmark and region has a name`);
      return nodes;
    };
    let nodes = await check('workspace');
    const count = (role, name) => nodes.filter((n) => n.role === role && (name === undefined || n.name === name)).length;
    assert.equal(count('banner'), 1, 'the top bar is the banner');
    assert.equal(count('main'), 1, 'one main');
    assert.equal(count('contentinfo'), 1, 'the status bar is the footer');
    assert.equal(count('complementary', 'Folder'), 1, 'the folder sidebar is a named complementary');
    assert.equal(count('toolbar', 'Formatting'), 1);
    assert.equal(count('tree', 'Markdown files'), 1);
    assert.equal(count('region', 'Preview of the document'), 1);
    assert.equal(count('textbox', 'Markdown source'), 1, 'the editor');
    assert.ok(count('separator') >= 2, 'both dividers are separators');
    assert.equal(count('navigation'), 0, 'no unnamed navigation landmark');
    for (const name of ['Open file (Ctrl+O)', 'Save (Ctrl+S)', 'Edit view', 'Split view', 'Preview view', 'Export as HTML', 'Close folder', 'Bold (Ctrl+B)', 'Check for updates'])
      assert.ok(count('button', name) >= 1, `button "${name}"`);
    assert.ok(nodes.some((n) => n.role === 'button' && n.name.startsWith('Theme:')), 'the theme button names its state');

    // The split view toggle says which view is on.
    assert.equal(await button(page, 'Split view').getAttribute('aria-pressed'), 'true');

    // The dividers say where they stand.
    const sep = page.locator('.splitter-sidebar');
    await sep.focus();
    const [min, max, now] = await Promise.all(['aria-valuemin', 'aria-valuemax', 'aria-valuenow'].map((a) => sep.getAttribute(a)));
    assert.ok(Number(min) < Number(now) && Number(now) <= Number(max), `divider range ${min} to ${max}, now ${now}`);

    // Panels are named complementary regions too.
    await button(page, 'Outline').click();
    await page.waitForSelector('.outline-item');
    nodes = await check('outline');
    assert.equal(nodes.filter((n) => n.role === 'complementary' && n.name === 'Outline').length, 1);
    await button(page, 'Outline').click();
    await button(page, 'Search').click();
    await page.locator('input[aria-label="Search text in the open folder"]').fill('needle');
    await page.locator('.search-hit').first().waitFor();
    nodes = await check('search');
    assert.equal(nodes.filter((n) => n.role === 'complementary' && n.name === 'Search folder').length, 1);
    await button(page, 'Search').click();
    await button(page, 'Recent files').click();
    await page.locator('.recent-item').first().waitFor();
    nodes = await check('recent files');
    assert.equal(nodes.filter((n) => n.role === 'complementary' && n.name === 'Recent files').length, 1);
    await button(page, 'Recent files').click();

    // The unsaved mark can be read, and the cursor position is not a live region.
    await page.locator('.cm-content').click();
    await page.keyboard.type('x');
    await page.locator('.dirty-dot').waitFor({ state: 'visible' });
    assert.equal(await page.locator('.dirty-dot').textContent(), 'Unsaved changes');
    assert.equal(await page.locator('.status-bar').evaluate((n) => n.closest('[aria-live], [role=status], [role=alert]') === null && n.querySelector('[aria-live], [role=status], [role=alert]') === null), true, 'typing changes no live region');
  }],

  ['accessibility: banners and changes are announced politely, typing is not', async (page) => {
    await boot(page);
    const live = page.locator('[role=status][aria-live=polite]').filter({ hasNot: page.locator('.nothing') }).first();
    const said = () => page.evaluate(() => [...document.querySelectorAll('[role=status]')].map((n) => n.textContent).join('|'));
    // A banner is said when it appears, though the banner element is not itself a live region.
    assert.equal(await page.locator('.banner').getAttribute('role'), null);
    await openFile(page, 'e/mixed.md', [...bytesOf('# Mixed\r\ntwo\r\nthree\nfour\r\n')]);
    await banner(page).getByRole('button', { name: 'Convert line endings' }).waitFor();
    await page.waitForFunction(() => document.querySelector('.app > [role=status]')?.textContent.includes('Convert line endings'));
    assert.ok((await live.count()) >= 1);
    // Saving and changing a setting are said; each keystroke is not.
    await openFile(page, 'e/plain.md', '# Plain\n');
    await page.waitForFunction(() => document.querySelector('.app > [role=status]')?.textContent === 'Opened plain.md');
    await page.locator('.cm-content').click();
    const before = await page.locator('.app > [role=status]').textContent();
    await page.keyboard.type('hello world');
    await page.waitForTimeout(300);
    assert.equal(await page.locator('.app > [role=status]').textContent(), before, 'typing is silent');
    await page.evaluate(() => window.__fs.queueSave('e/plain.md'));
    await button(page, 'Save (Ctrl+S)').click();
    await page.waitForFunction(() => document.querySelector('.app > [role=status]')?.textContent === 'Saved plain.md');
    await button(page, /^Theme:/).click().catch(async () => page.getByRole('button', { name: /^Theme:/ }).click());
    await page.waitForFunction(() => /^Theme: /.test(document.querySelector('.app > [role=status]')?.textContent ?? ''));
    assert.ok((await said()).length > 0);
  }],

  ['accessibility: the folder tree follows the WAI-ARIA tree pattern', async (page) => {
    await boot(page);
    await openFolder(page);
    await page.waitForSelector('.markdown-body h1');
    const items = page.locator('.tree [role=treeitem]');
    const focused = () => page.evaluate(() => document.activeElement?.getAttribute('data-path'));
    const press = (key) => page.keyboard.press(key);
    const expanded = (path) => page.locator(`.tree-folder[data-path="${path}"]`).getAttribute('aria-expanded');

    // Roles and levels: docs is open by default, docs/guide is not.
    assert.equal(await page.locator('.tree[class] [role=tree]').count(), 1);
    const attrs = async (path) => page.locator(`.tree [data-path="${path}"]`).evaluate((n) => [n.getAttribute('aria-level'), n.getAttribute('aria-setsize'), n.getAttribute('aria-posinset'), n.getAttribute('aria-expanded'), n.getAttribute('aria-selected')]);
    assert.deepEqual(await attrs('docs'), ['1', '3', '1', 'true', null]);
    assert.deepEqual(await attrs('notes'), ['1', '3', '2', 'false', null]);
    assert.deepEqual(await attrs('README.md'), ['1', '3', '3', null, 'true']);
    assert.deepEqual(await attrs('docs/guide'), ['2', '2', '1', 'false', null]);
    assert.deepEqual(await attrs('docs/intro.md'), ['2', '2', '2', null, 'false']);
    assert.equal(await page.locator('.tree [role=group]').first().getAttribute('role'), 'group');
    assert.equal(await page.locator('.tree-file[data-path="docs/guide/setup.md"]').isHidden(), true, 'a closed folder hides its items');
    assert.equal(await page.locator('.tree [role=treeitem][tabindex="0"]').count(), 1, 'one tab stop');
    assert.equal(await page.locator('.tree-file[data-path="README.md"]').getAttribute('tabindex'), '0', 'the open file holds it');

    // Tab from the filter reaches the tree on the open file; Shift+Tab goes back.
    await page.locator('input[aria-label="Filter files"]').focus();
    await press('Tab');
    assert.equal(await focused(), 'README.md');
    await press('Home');
    assert.equal(await focused(), 'docs');
    await press('ArrowDown');
    assert.equal(await focused(), 'docs/guide');
    assert.equal(await expanded('docs/guide'), 'false');
    await press('ArrowRight');
    assert.equal(await expanded('docs/guide'), 'true', 'Right opens a closed folder');
    assert.equal(await focused(), 'docs/guide', 'and stays on it');
    await press('ArrowRight');
    assert.equal(await focused(), 'docs/guide/setup.md', 'Right on an open folder enters it');
    await press('ArrowRight');
    assert.equal(await focused(), 'docs/guide/setup.md', 'Right on a file does nothing');
    await press('ArrowLeft');
    assert.equal(await focused(), 'docs/guide', 'Left on a file goes to its folder');
    await press('ArrowLeft');
    assert.equal(await expanded('docs/guide'), 'false', 'Left on an open folder closes it');
    await press('ArrowLeft');
    assert.equal(await focused(), 'docs', 'Left on a closed folder goes to its parent');
    await press('ArrowLeft');
    assert.equal(await expanded('docs'), 'false');
    assert.deepEqual(await items.evaluateAll((n) => n.filter((i) => !i.closest('[role=group][hidden]')).map((i) => i.dataset.path)), ['docs', 'notes', 'README.md']);
    await press('End');
    assert.equal(await focused(), 'README.md');
    await press('ArrowDown');
    assert.equal(await focused(), 'README.md', 'Down stops at the end');
    await press('ArrowUp');
    assert.equal(await focused(), 'notes');
    await press('Space');
    assert.equal(await expanded('notes'), 'true', 'Space opens a folder');
    await press('Enter');
    assert.equal(await expanded('notes'), 'false', 'Enter closes it');

    // Type-ahead jumps to the next visible name that starts with the letters; one letter cycles.
    await press('Home');
    await press('ArrowRight');
    await press('i');
    assert.equal(await focused(), 'docs/intro.md');
    await press('n');
    assert.equal(await focused(), 'docs/intro.md', 'letters typed together make one prefix ("in")');
    await page.waitForTimeout(800);
    await press('n');
    assert.equal(await focused(), 'notes', 'a letter after a pause starts again');
    await page.waitForTimeout(800);
    await press('r');
    assert.equal(await focused(), 'README.md');

    // Enter opens a file, which becomes the selected item; focus stays on the tree.
    await press('ArrowUp');
    await press('ArrowUp');
    await press('ArrowDown');
    await page.waitForTimeout(800);
    await press('i');
    assert.equal(await focused(), 'docs/intro.md');
    await press('Enter');
    await page.locator('.markdown-body h1', { hasText: 'Intro' }).waitFor();
    assert.equal(await docTitle(page).textContent(), 'docs/intro.md');
    assert.deepEqual(await attrs('docs/intro.md'), ['2', '2', '2', null, 'true']);
    assert.deepEqual(await attrs('README.md'), ['1', '3', '3', null, 'false']);
    assert.equal(await page.locator('.tree-file[data-path="docs/intro.md"]').getAttribute('aria-current'), 'page');
    assert.equal(await focused(), 'docs/intro.md');
    assert.equal(await page.locator('.tree [role=treeitem][tabindex="0"]').count(), 1);

    // Tab leaves the tree in one stroke; Shift+Tab comes back to the same item.
    await press('Tab');
    assert.equal(await page.evaluate(() => document.activeElement.closest('.tree') === null), true);
    await press('Shift+Tab');
    assert.equal(await focused(), 'docs/intro.md');

    // Names are the file or folder name; the path is the tooltip.
    assert.equal(await page.getByRole('treeitem', { name: 'intro.md' }).count(), 1);
    assert.equal(await page.getByRole('treeitem', { name: 'docs', exact: true }).getAttribute('aria-expanded'), 'true');

    // The filter lists matches as a flat tree, and Down from the field enters it.
    const filter = page.locator('input[aria-label="Filter files"]');
    await filter.fill('setup');
    assert.deepEqual(await items.evaluateAll((n) => n.map((i) => [i.dataset.path, i.getAttribute('aria-level'), i.getAttribute('aria-label')])), [['docs/guide/setup.md', '1', 'docs/guide/setup.md']]);
    await press('ArrowDown');
    assert.equal(await focused(), 'docs/guide/setup.md');
    await press('Enter');
    await page.locator('.markdown-body h1', { hasText: 'Setup' }).waitFor();
    await filter.fill('');
    assert.equal(await expanded('docs/guide'), 'true', 'the folders above the open file are open again');

    // A click opens a file and a click on a folder toggles it, as before.
    await page.locator('.tree-folder[data-path="notes"] > .tree-row').click();
    assert.equal(await expanded('notes'), 'true');
    assert.equal(await focused(), 'notes');
  }],

  ['accessibility: dialogs are labelled, trap focus and return it', async (page) => {
    await boot(page);
    await openFile(page, 'e/mixed.md', [...bytesOf('# Mixed\r\ntwo\r\nthree\nfour\r\n')]);
    const opener = banner(page).getByRole('button', { name: 'Convert line endings' });
    await opener.waitFor();
    await opener.focus();
    await page.keyboard.press('Enter');
    const dialog = page.locator('dialog.modal');
    await dialog.waitFor({ state: 'visible' });
    const nodes = await axNodes(page);
    assert.equal(nodes.filter((n) => n.role === 'dialog' && n.name === 'Convert line endings?').length, 1, 'the dialog is named by its heading');
    assert.equal(await dialog.getAttribute('aria-describedby'), 'confirm-body');
    assert.equal(await page.evaluate(() => document.activeElement.closest('dialog') !== null), true, 'focus moves into the dialog');
    // Tab and Shift+Tab stay inside, however long they are held.
    for (const key of ['Tab', 'Tab', 'Tab', 'Tab', 'Tab', 'Shift+Tab', 'Shift+Tab', 'Shift+Tab', 'Shift+Tab']) {
      await page.keyboard.press(key);
      assert.equal(await page.evaluate(() => document.activeElement.closest('dialog') !== null), true, `${key} stays in the dialog`);
    }
    // The page behind it cannot take focus.
    await page.evaluate(() => document.querySelector('.top-bar button:not(:disabled)').focus());
    assert.equal(await page.evaluate(() => document.activeElement.closest('dialog') !== null), true, 'the page behind is inert');
    await page.keyboard.press('Escape');
    await dialog.waitFor({ state: 'detached' });
    assert.equal(await active(page, 'button.banner-action'), true, 'Escape returns focus to the button that opened it');
    assert.equal(await docTitle(page).textContent(), 'mixed.md (read-only)', 'Escape declined');

    // Cancel by button returns focus the same way.
    await page.keyboard.press('Enter');
    await dialog.waitFor({ state: 'visible' });
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await dialog.waitFor({ state: 'detached' });
    assert.equal(await active(page, 'button.banner-action'), true, 'Cancel returns focus too');
  }],

  ['accessibility: the editor can be left by keyboard (Escape, then Tab)', async (page) => {
    await boot(page);
    await page.locator('.cm-content').click();
    await page.keyboard.press('Tab');
    assert.ok((await editorText(page)).includes('\t') || (await editorText(page)).startsWith('  '), 'Tab indents while typing');
    assert.equal(await active(page, '.cm-content'), true);
    await page.keyboard.press('Escape');
    await page.keyboard.press('Tab');
    assert.equal(await active(page, '.cm-content'), false, 'Escape then Tab leaves the editor');
    // The preview can be scrolled from the keyboard.
    await button(page, 'Preview view').click();
    await page.locator('.preview-pane').focus();
    assert.equal(await active(page, '.preview-pane'), true);
  }],

  ['accessibility: the format bar is one tab stop with arrow keys', async (page) => {
    await boot(page);
    const bar = page.locator('.format-bar');
    const stops = bar.locator('button[tabindex="0"]');
    assert.equal(await stops.count(), 1);
    await bar.locator('button').first().focus();
    await page.keyboard.press('ArrowRight');
    assert.equal(await page.evaluate(() => document.activeElement.dataset.format), 'italic');
    await page.keyboard.press('End');
    assert.equal(await page.evaluate(() => document.activeElement.dataset.format), 'rule');
    await page.keyboard.press('ArrowRight');
    assert.equal(await page.evaluate(() => document.activeElement.dataset.format), 'bold', 'wraps around');
    await page.keyboard.press('ArrowLeft');
    assert.equal(await page.evaluate(() => document.activeElement.dataset.format), 'rule');
    assert.equal(await stops.count(), 1, 'still one tab stop');
    await page.keyboard.press('Home');
    await page.keyboard.press('Tab');
    assert.equal(await page.evaluate(() => document.activeElement.closest('.format-bar') === null), true, 'Tab leaves the bar');
  }],

  ['the page carries its CSP and the violation listener works', async (page, _scheme, problems) => {
    await boot(page);
    const policy = await page.locator('meta[http-equiv="Content-Security-Policy"]').getAttribute('content');
    assert.ok(policy.includes("script-src 'self'") && !/script-src[^;]*unsafe/.test(policy), policy);
    assert.ok(policy.includes("object-src 'none'") && policy.includes("frame-src 'none'"));
    // Inline script is refused by the policy, and the listener records it. The record is cleared
    // afterwards because this violation is the point of the scenario, not a failure.
    await page.evaluate(() => {
      const script = document.createElement('script');
      script.textContent = 'window.__inlineRan = true';
      document.head.append(script);
    });
    await page.waitForFunction(() => window.__csp.length === 1);
    assert.equal(await page.evaluate(() => window.__inlineRan), undefined, 'inline script did not run');
    await page.evaluate(() => (window.__csp.length = 0));
    // The console reports the blocked script as an error too; it is expected here only.
    for (let i = 0; i < 40 && !problems.some((p) => /Content Security Policy/.test(p)); i++) await page.waitForTimeout(50);
    assert.ok(problems.some((p) => /Content Security Policy/.test(p)), 'the console reported it');
    problems.length = 0;
  }],

  ['document text is never run as script', async (page) => {
    await boot(page);
    await openFile(
      page,
      'x/hostile.md',
      '# Hostile\n\n<img src=x onerror="window.__pwned=1">\n\n<script>window.__pwned=2</script>\n\n[x](javascript:window.__pwned=3)\n\n![remote](https://example.invalid/a.png)\n',
    );
    await page.waitForSelector('.markdown-body h1');
    await page.waitForTimeout(300);
    assert.equal(await page.evaluate(() => window.__pwned), undefined);
    assert.equal(await page.locator('.markdown-body img[src^="http"], .markdown-body script').count(), 0, 'no remote content, no script');
  }],
];

// ---- title bar harness (desktop shell component) -------------------------------------------

const harnessScenarios = [
  ['desktop title bar: captions, labels, drag region', async (page, scheme) => {
    await page.waitForFunction(() => window.__ready === true);
    const bar = page.locator('.titlebar');
    assert.equal(await bar.isVisible(), true);
    assert.equal(await bar.getAttribute('data-tauri-drag-region'), '', 'the bar is the drag region');
    assert.equal(await page.locator('.titlebar-name').textContent(), 'ZTG Readout');
    for (const label of ['Minimize', 'Maximize', 'Close']) {
      const caption = page.getByRole('button', { name: label, exact: true });
      assert.equal(await caption.count(), 1, `${label} button`);
      assert.equal(await caption.getAttribute('title'), label);
      const box = await caption.boundingBox();
      assert.deepEqual([Math.round(box.width), Math.round(box.height)], [46, 32], `${label} is a 46 by 32 caption`);
    }
    await page.getByRole('button', { name: 'Minimize', exact: true }).click();
    await page.getByRole('button', { name: 'Maximize', exact: true }).click();
    await page.getByRole('button', { name: 'Close', exact: true }).click();
    assert.deepEqual(await page.evaluate(() => window.__calls), ['minimize', 'toggleMaximize', 'close']);
    // The maximize button turns into Restore when the window is maximized, and back.
    await page.evaluate(() => window.__setMaximized(true));
    assert.equal(await page.getByRole('button', { name: 'Restore', exact: true }).count(), 1);
    assert.equal(await page.getByRole('button', { name: 'Maximize', exact: true }).count(), 0);
    await page.evaluate(() => window.__setMaximized(false));
    assert.equal(await page.getByRole('button', { name: 'Maximize', exact: true }).count(), 1);
    // The bar follows the theme, so the whole window does.
    const color = await bar.evaluate((n) => getComputedStyle(n).backgroundColor);
    const l = luminance(color);
    assert.ok(scheme === 'dark' ? l < 0.3 : l > 0.7, `title bar ${color} fits ${scheme}`);
    // The window controls are a named group, each with a visible focus ring, and the bar is legible.
    assert.equal((await axNodes(page)).filter((n) => n.role === 'group' && n.name === 'Window controls').length, 1);
    assert.deepEqual(await contrastFailures(page), [], 'title bar contrast');
    for (const label of ['Minimize', 'Maximize', 'Close']) {
      await page.getByRole('button', { name: label, exact: true }).focus();
      await page.keyboard.press('Shift+Tab');
      await page.keyboard.press('Tab');
      const ring = await focusIndicator(page);
      assert.ok(ring.visible && ring.ratio >= 3 && ring.width >= 2, `${label} focus ring ${JSON.stringify(ring)}`);
    }
    // The close caption turns red on hover (Windows 11 behavior).
    await page.getByRole('button', { name: 'Close', exact: true }).hover();
    const hover = await page.getByRole('button', { name: 'Close', exact: true }).evaluate((n) => getComputedStyle(n).backgroundColor);
    const [r, g] = hover.match(/\d+/g).map(Number);
    assert.ok(r > 150 && g < 80, `close hover is red (${hover})`);
  }],
];

// ---- run ----------------------------------------------------------------------------------

try {
  for (const scheme of ['dark', 'light']) {
    for (const [name, body] of scenarios.filter(([n]) => n.includes(only))) await scenario(name, scheme, (page, problems) => body(page, scheme, problems));
    for (const [name, body] of harnessScenarios.filter(([n]) => n.includes(only))) await scenario(name, scheme, (page) => body(page, scheme), { url: harnessUrl, mock: false });
  }
} finally {
  await browser.close();
  await vite.close();
  staticServer.close();
}

console.log(`\n${results.filter((r) => r.startsWith('ok')).length} passed, ${results.filter((r) => r.startsWith('FAIL')).length} failed`);
process.exit(failed ? 1 : 0);
