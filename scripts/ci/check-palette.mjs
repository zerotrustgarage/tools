#!/usr/bin/env node
// Every tool uses the ZTG palette. design-tokens.json at the repository root is a byte-identical
// copy of the canonical file (zerotrustgarage/ztg, generated in zerotrustgarage/infrasec); this
// check fails when a tool's stylesheet drifts from it, so the tools keep looking like one family.
//
//   node scripts/ci/check-palette.mjs
//
// Each tool names its CSS variables its own way, so the mapping from a tool's variable to a token
// is listed here. Values are compared after normalizing case, spaces and number formatting
// ("rgba(255, 255, 255, 0.1)" equals "rgba(255,255,255,.10)").

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const tokens = JSON.parse(readFileSync(join(root, 'design-tokens.json'), 'utf8')).tokens;

const TOOLS = [
  {
    name: 'ZTG Copy',
    file: 'copy/app/frontend/app.css',
    blocks: { dark: ':root {', light: ':root[data-theme="light"] {', lightAuto: ':root:not([data-theme="dark"]) {' },
    dark: {"bg":"bg","panel":"panel","panel-2":"panel2","line":"line","line-strong":"line-strong","text":"text","bright":"bright","muted":"dim","dim2":"dim2","accent":"acc","accent-ink":"acc-ink","up":"ok","high":"crit","medium":"warn","info":"info"},
    light: {"bg":"light-bg","panel":"light-panel","panel-2":"light-panel2","line":"light-line","line-strong":"light-line-strong","text":"light-text","bright":"light-bright","muted":"light-dim","dim2":"light-dim2","accent":"light-ok","accent-ink":"light-panel","up":"light-ok","high":"light-crit","medium":"light-warn","info":"light-info"},
  },
  {
    name: 'ZTG Readout',
    file: 'readout/src/styles/app.css',
    blocks: {
      dark: ":root[data-theme='dark'] {",
      light: ":root,\n:root[data-theme='light'] {",
    },
    dark: {
      bg: 'bg', panel: 'panel', panel2: 'panel2', line: 'line', 'line-strong': 'line-strong', text: 'text',
      bright: 'bright', dim: 'dim', dim2: 'dim2', acc: 'acc', 'acc-ink': 'acc-ink', crit: 'crit', warn: 'warn',
    },
    light: {
      bg: 'light-bg', panel: 'light-panel', panel2: 'light-panel2', line: 'light-line', 'line-strong': 'light-line-strong',
      text: 'light-text', bright: 'light-bright', dim: 'light-dim', dim2: 'light-dim2', acc: 'light-ok',
      crit: 'light-crit', warn: 'light-warn',
    },
  },
  {
    name: 'ZTG Scanner',
    file: 'scanner/app/frontend/app.css',
    blocks: {
      dark: ':root {',
      light: ':root[data-theme="light"] {',
      // The same light palette also applies when the user chose Auto and Windows is light.
      lightAuto: ':root:not([data-theme="dark"]) {',
    },
    dark: {
      bg: 'bg', panel: 'panel', 'panel-2': 'panel2', line: 'line', text: 'text', muted: 'dim', accent: 'acc',
      'accent-ink': 'acc-ink', up: 'ok', down: 'dim2', high: 'crit', medium: 'warn', info: 'info',
    },
    light: {
      bg: 'light-bg', panel: 'light-panel', 'panel-2': 'light-panel2', line: 'light-line', text: 'light-text',
      muted: 'light-dim', accent: 'light-ok', up: 'light-ok', down: 'light-dim2', high: 'light-crit',
      medium: 'light-warn', info: 'light-info',
    },
  },
  {
    name: 'ZTG Scanner reports',
    file: 'scanner/internal/report/report.html.tmpl',
    blocks: { light: ':root {', dark: '@media screen and (prefers-color-scheme: dark) {', print: '@media print {' },
    dark: {
      bg: 'bg', panel: 'panel', 'panel-2': 'panel2', line: 'line', text: 'text', muted: 'dim',
      accent: 'acc', high: 'crit', medium: 'warn', info: 'info',
    },
    light: {
      bg: 'light-bg', panel: 'light-panel', 'panel-2': 'light-panel2', line: 'light-line', text: 'light-text',
      muted: 'light-dim', accent: 'light-ok', high: 'light-crit', medium: 'light-warn', info: 'light-info',
    },
    print: {
      bg: 'light-panel', panel: 'light-panel', 'panel-2': 'light-panel2', line: 'light-line', text: 'light-text',
      muted: 'light-dim', accent: 'light-ok', high: 'light-crit', medium: 'light-warn', info: 'light-info',
    },
  },
  {
    name: 'ZTG TFTP',
    file: 'tftp/app/frontend/app.css',
    blocks: {
      dark: ':root {',
      light: ':root[data-theme="light"] {',
      // The same light palette also applies when the user chose Auto and Windows is light.
    },
    dark: {
      bg: 'bg', panel: 'panel', 'panel-2': 'panel2', line: 'line', text: 'text', muted: 'dim', accent: 'acc',
      'accent-ink': 'acc-ink', up: 'ok', down: 'dim2', high: 'crit', medium: 'warn', info: 'info',
    },
    light: {
      bg: 'light-bg', panel: 'light-panel', 'panel-2': 'light-panel2', line: 'light-line', text: 'light-text',
      muted: 'light-dim', accent: 'light-ok', up: 'light-ok', down: 'light-dim2', high: 'light-crit',
      medium: 'light-warn', info: 'light-info',
    },
  },
];

// Hex colours compare as text; the numbers inside a function such as rgba() compare as numbers.
const normalize = (value) =>
  value
    .trim()
    .toLowerCase()
    .replace(/\s+/g, '')
    .replace(/\(([^)]*)\)/g, (_, inner) => `(${inner.split(',').map((n) => (Number.isNaN(Number(n)) ? n : String(Number(n)))).join(',')})`);

/** The custom properties declared in the block that starts with selector. */
function blockVars(css, selector, file) {
  const start = css.indexOf(selector);
  if (start < 0) throw new Error(`${file}: selector not found: ${JSON.stringify(selector)}`);
  const open = css.indexOf('{', start);
  const close = css.indexOf('}', open);
  const vars = new Map();
  for (const m of css.slice(open + 1, close).matchAll(/--([\w-]+)\s*:\s*([^;]+);/g)) vars.set(m[1], m[2]);
  return vars;
}

const problems = [];
for (const tool of TOOLS) {
  const css = readFileSync(join(root, tool.file), 'utf8');
  for (const theme of ['dark', 'light', ...(tool.print ? ['print'] : [])]) {
    const vars = blockVars(css, tool.blocks[theme], tool.file);
    for (const [name, token] of Object.entries(tool[theme])) {
      if (!(token in tokens)) problems.push(`${tool.name}: unknown token ${token}`);
      else if (!vars.has(name)) problems.push(`${tool.name} (${theme}): --${name} is missing; expected ${tokens[token]} (${token})`);
      else if (normalize(vars.get(name)) !== normalize(tokens[token]))
        problems.push(`${tool.name} (${theme}): --${name} is ${vars.get(name).trim()}, the ZTG token ${token} is ${tokens[token]}`);
    }
  }
  if (tool.blocks.lightAuto) {
    const a = blockVars(css, tool.blocks.light, tool.file);
    const b = blockVars(css, tool.blocks.lightAuto, tool.file);
    for (const [name, value] of a)
      if (normalize(b.get(name) ?? '') !== normalize(value))
        problems.push(`${tool.name}: the two light palettes differ at --${name} (${value.trim()} vs ${b.get(name) ?? 'missing'})`);
  }
}

if (problems.length) {
  console.error(`Palette drift from design-tokens.json:\n  ${problems.join('\n  ')}`);
  process.exit(1);
}
console.log(`Palette: ${TOOLS.map((t) => t.name).join(' and ')} match design-tokens.json.`);
