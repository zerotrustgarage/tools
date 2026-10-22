// Inspect the actual WebView2 desktop app through its CI-only localhost debug port.
// The production app never enables a debug port; the test runner supplies the WebView2 flag.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
const { chromium } = createRequire(import.meta.url)('playwright-core');
const [version, shots] = process.argv.slice(2);
const browser = await chromium.connectOverCDP('http://127.0.0.1:9227', { timeout: 15000 });
try {
  const pages = browser.contexts().flatMap(context => context.pages());
  const page = pages.find(page => page.url().includes('wails')) || pages[0];
  assert.ok(page, 'desktop WebView2 page');
  await page.waitForFunction(v => document.getElementById('build-version')?.textContent.startsWith('v' + v + '+'), version);
  assert.ok(await page.locator('#window-controls').isVisible(), 'native caption controls are visible');
  for (const selector of ['#theme', '#settings-btn', '#export-btn', '#check-updates', '#window-controls'])
    assert.equal(await page.locator(`#titlebar ${selector}`).count(), 1, `${selector} is in the unified app bar`);
  if (shots) await mkdir(shots, { recursive: true });
  for (const [theme, expected] of [['dark', 'rgb(2, 12, 27)'], ['light', 'rgb(245, 247, 249)']]) {
    await page.selectOption('#theme', theme);
    await page.waitForFunction(bg => getComputedStyle(document.body).backgroundColor === bg, expected);
    assert.equal(await page.inputValue('#theme'), theme);
    if (shots) await page.screenshot({ path: join(shots, `scanner-${version}-${theme}.png`) });
  }
  await page.selectOption('#theme', 'auto');
  const clipped = await page.locator('#titlebar button, #titlebar select').evaluateAll(nodes => nodes
    .filter(node => node.getBoundingClientRect().right > innerWidth).map(node => node.id));
  assert.deepEqual(clipped, [], 'desktop app bar controls fit the window');
  console.log(`Desktop WebView2 ${version}: unified controls, explicit dark/light themes and visible build pass.`);
} finally { await browser.close(); }
