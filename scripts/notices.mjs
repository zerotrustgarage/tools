#!/usr/bin/env node
// Writes THIRD-PARTY-NOTICES.txt for a tool: the license text of every npm package in the
// production bundle and every Rust crate compiled into the Windows binary. MIT, BSD and Apache
// licenses require their notices to travel with redistributed binaries, and the installer ships
// this file beside the executable.
//
//   node scripts/notices.mjs <app dir> <output file>
//
// The Rust list uses `cargo metadata --filter-platform x86_64-pc-windows-msvc`, so it covers what
// the Windows build links, and it fails if a crate's source is not on disk (run cargo fetch
// first) rather than silently omitting a notice.
//
// A few packages publish no license file. For those the standard SPDX text of their declared
// license is used (scripts/licenses/, verbatim from spdx/license-list-data v3.27.0) with the
// package's authors or repository in the copyright line. A package with neither a license file
// nor a known SPDX text fails the run.

import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';

const LICENSE_FILE = /^(licen[cs]e|copying|notice)(\.|-|$)/i;
const RUST_TARGET = 'x86_64-pc-windows-msvc';
const SPDX_DIR = join(dirname(new URL(import.meta.url).pathname), 'licenses');
// For "A OR B" the redistributor picks one; prefer the shortest obligations.
const SPDX_PREFERENCE = ['MIT', 'BSD-3-Clause', 'Apache-2.0', 'MPL-2.0'];
const TEMPLATE_COPYRIGHT = /^Copyright \(c\) <year> <[^>]+>\.?[ \t]*$/m;

// The standard text for a package that ships none, or null when its license is not one we hold.
function spdxFallback(pkg) {
  const choices = String(pkg.license ?? '')
    .replace(/[()]/g, ' ')
    .split(/\s+OR\s+|\//)
    .map((s) => s.trim());
  // An AND expression needs every text; only single licenses and OR choices are handled.
  if (choices.some((c) => /\s(AND|WITH)\s/.test(c))) return null;
  const id = SPDX_PREFERENCE.find((l) => choices.includes(l));
  if (!id) return null;
  const holders = pkg.authors?.length ? pkg.authors.join(', ') : `the ${pkg.name} authors (${pkg.repository ?? 'see the package'})`;
  const text = readFileSync(join(SPDX_DIR, `${id}.txt`), 'utf8')
    .trim()
    .replace(TEMPLATE_COPYRIGHT, `Copyright (c) ${holders}`);
  const note = `${pkg.name} publishes no license file. This is the standard SPDX text of ${id}${choices.length > 1 ? `, chosen from ${pkg.license}` : ''}${pkg.repository ? `; source: ${pkg.repository}` : ''}.`;
  return `${note}\n\n${text}`;
}

function licenseTexts(dir) {
  if (!existsSync(dir)) return [];
  return readdirSync(dir)
    .filter((name) => LICENSE_FILE.test(name))
    .sort()
    .map((name) => readFileSync(join(dir, name), 'utf8').trim());
}

function npmPackages(appDir) {
  const tree = JSON.parse(
    execFileSync('npm', ['ls', '--omit=dev', '--all', '--json'], { cwd: appDir, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 }),
  );
  const seen = new Map();
  const visit = (deps) => {
    for (const [name, info] of Object.entries(deps ?? {})) {
      const key = `${name}@${info.version}`;
      // The tool itself is the root of the tree: list what it depends on, not the tool.
      if (name.startsWith('@ztg-tools/')) {
        visit(info.dependencies);
        continue;
      }
      if (seen.has(key) || !info.version) continue;
      // npm workspaces hoist packages towards the repository root, so look upwards.
      const candidates = [];
      for (let dir = appDir; ; dir = dirname(dir)) {
        candidates.push(join(dir, 'node_modules', name));
        if (dirname(dir) === dir) break;
      }
      const dir = candidates.find((d) => existsSync(join(d, 'package.json')));
      const manifest = dir ? JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8')) : {};
      const author = typeof manifest.author === 'string' ? manifest.author : manifest.author?.name;
      seen.set(key, {
        name,
        version: info.version,
        license: typeof manifest.license === 'string' ? manifest.license : 'see text',
        authors: author ? [author] : [],
        repository: typeof manifest.repository === 'string' ? manifest.repository : manifest.repository?.url,
        texts: dir ? licenseTexts(dir) : [],
      });
      visit(info.dependencies);
    }
  };
  visit(tree.dependencies);
  // The type-only Tauri API bridge is in the bundle too; nothing is omitted.
  return [...seen.values()].sort((a, b) => a.name.localeCompare(b.name));
}

function rustCrates(appDir) {
  const manifest = join(appDir, 'src-tauri', 'Cargo.toml');
  const meta = JSON.parse(
    execFileSync('cargo', ['metadata', '--format-version', '1', '--locked', '--filter-platform', RUST_TARGET, '--manifest-path', manifest], {
      encoding: 'utf8',
      maxBuffer: 256 * 1024 * 1024,
    }),
  );
  const used = new Set(meta.resolve.nodes.map((n) => n.id));
  const root = meta.resolve.root;
  return meta.packages
    .filter((p) => used.has(p.id) && p.id !== root)
    .map((p) => {
      const dir = dirname(p.manifest_path);
      const texts = p.license_file ? [readFileSync(resolve(dir, p.license_file), 'utf8').trim()] : licenseTexts(dir);
      return { name: p.name, version: p.version, license: p.license ?? 'see text', authors: p.authors, repository: p.repository, texts };
    })
    .sort((a, b) => a.name.localeCompare(b.name) || a.version.localeCompare(b.version));
}

function section(title, packages) {
  return packages
    .map((p) => {
      const body = p.texts.length ? p.texts.join('\n\n---\n\n') : `License: ${p.license} (no license file is distributed with this package)`;
      return `${'='.repeat(78)}\n${title}: ${p.name} ${p.version} (${p.license})\n${'='.repeat(78)}\n\n${body}\n`;
    })
    .join('\n');
}

const [appArg, outArg] = process.argv.slice(2);
if (!appArg || !outArg) {
  console.error('Usage: notices.mjs <app dir> <output file>');
  process.exit(2);
}
const appDir = resolve(appArg);
const npm = npmPackages(appDir);
const rust = rustCrates(appDir);
if (npm.length === 0 || rust.length === 0) throw new Error('No npm packages or no crates were found; refusing to write an incomplete notices file');
const unresolved = npm.filter((p) => p.license === 'see text' && p.texts.length === 0);
if (unresolved.length > npm.length / 10) throw new Error(`Could not read ${unresolved.length} npm package manifests; is node_modules installed?`);
const fallbacks = [];
for (const p of [...npm, ...rust].filter((q) => q.texts.length === 0)) {
  const text = spdxFallback(p);
  if (text) {
    p.texts = [text];
    fallbacks.push(`${p.name} ${p.version} (${p.license})`);
  }
}
const missing = [...npm, ...rust].filter((p) => p.texts.length === 0).map((p) => `${p.name} ${p.version} (${p.license})`);
if (missing.length) {
  throw new Error(`No license text for:\n  ${missing.join('\n  ')}\nAdd its SPDX text to scripts/licenses/ and SPDX_PREFERENCE, or vendor the package's own file.`);
}
const header = [
  'THIRD-PARTY SOFTWARE NOTICES',
  '',
  'This product includes the open source components listed below, each under its own license.',
  `npm packages: ${npm.length}. Rust crates: ${rust.length}.`,
  '',
].join('\n');
writeFileSync(resolve(outArg), `${header}\n${section('npm', npm)}\n${section('crate', rust)}`);
console.log(`Wrote ${outArg}: ${npm.length} npm packages, ${rust.length} crates.`);
if (fallbacks.length) console.log(`Standard SPDX text used for packages without a license file:\n  ${fallbacks.join('\n  ')}`);
