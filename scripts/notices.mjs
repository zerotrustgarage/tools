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

import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';

const LICENSE_FILE = /^(licen[cs]e|copying|notice)(\.|-|$)/i;
const RUST_TARGET = 'x86_64-pc-windows-msvc';

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
      if (seen.has(key) || !info.version || name.startsWith('@ztg-tools/')) continue;
      // npm workspaces hoist to the repository root; look there as well as in the app.
      const candidates = [join(appDir, 'node_modules', name), join(appDir, '..', '..', 'node_modules', name)];
      const dir = candidates.find((d) => existsSync(join(d, 'package.json')));
      const manifest = dir ? JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8')) : {};
      seen.set(key, {
        name,
        version: info.version,
        license: typeof manifest.license === 'string' ? manifest.license : 'see text',
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
      return { name: p.name, version: p.version, license: p.license ?? 'see text', texts };
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
const missing = [...npm, ...rust].filter((p) => p.texts.length === 0).map((p) => `${p.name} ${p.version} (${p.license})`);
const header = [
  'THIRD-PARTY SOFTWARE NOTICES',
  '',
  'This product includes the open source components listed below, each under its own license.',
  `npm packages: ${npm.length}. Rust crates: ${rust.length}.`,
  '',
].join('\n');
writeFileSync(resolve(outArg), `${header}\n${section('npm', npm)}\n${section('crate', rust)}`);
console.log(`Wrote ${outArg}: ${npm.length} npm packages, ${rust.length} crates.`);
if (missing.length) console.warn(`No license file found for:\n  ${missing.join('\n  ')}`);
