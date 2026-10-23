import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';

// These exercise the real CLI with deterministic package metadata. Full notices
// generation against locked npm/Rust dependencies remains in the Windows build job.
function fixture(t, { npmLicense = 'MIT OR Apache-2.0', crateLicense = 'Apache-2.0', licenseFile = false } = {}) {
  const root = mkdtempSync(join(tmpdir(), 'ztg notices '));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const app = join(root, 'app');
  const pkg = join(app, 'node_modules', 'fixture-package');
  const crate = join(root, 'fixture-crate');
  const scripts = join(root, 'scripts with spaces');
  for (const dir of [pkg, crate, scripts]) mkdirSync(dir, { recursive: true });
  cpSync(new URL('./notices.mjs', import.meta.url), join(scripts, 'notices.mjs'));
  cpSync(new URL('./licenses/', import.meta.url), join(scripts, 'licenses'), { recursive: true });
  writeFileSync(join(pkg, 'package.json'), JSON.stringify({ name: 'fixture-package', version: '1.0.0', license: npmLicense, author: 'Fixture author' }));
  if (licenseFile) writeFileSync(join(pkg, 'LICENSE'), 'The actual package license text.');
  const npm = { dependencies: { 'fixture-package': { version: '1.0.0' } } };
  const cargo = {
    resolve: { root: 'app', nodes: [{ id: 'app' }, { id: 'fixture-crate' }] },
    packages: [{ id: 'fixture-crate', name: 'fixture-crate', version: '1.0.0', license: crateLicense, authors: ['Crate author'], manifest_path: join(crate, 'Cargo.toml') }],
  };
  const preload = join(root, 'metadata.mjs');
  writeFileSync(preload, `import cp from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
const metadata = ${JSON.stringify({ npm, cargo })};
cp.execFileSync = (command) => {
  if (!Object.hasOwn(metadata, command)) throw new Error('Unexpected metadata command');
  return JSON.stringify(metadata[command]);
};
syncBuiltinESMExports();
`);
  const out = join(root, 'NOTICES.txt');
  const result = spawnSync(process.execPath, ['--import', preload, join(scripts, 'notices.mjs'), app, out], {
    encoding: 'utf8',
  });
  return { result, out };
}

test('CLI supplies npm and crate license texts, attribution and an OR choice from a path with spaces', { skip: process.platform === 'win32' }, (t) => {
  const { result, out } = fixture(t);
  assert.equal(result.status, 0, result.stderr);
  const text = readFileSync(out, 'utf8');
  assert.match(text, /npm packages: 1\. Rust crates: 1\./);
  assert.match(text, /Permission is hereby granted/);
  assert.match(text, /Copyright \(c\) Fixture author/);
  assert.match(text, /Apache License/);
  assert.match(text, /chosen from MIT OR Apache-2\.0/);
  assert.doesNotMatch(text, /no license file is distributed/);
});

test('CLI fails without writing notices for unsupported or conjunctive fallback licenses', { skip: process.platform === 'win32' }, async (t) => {
  for (const license of ['LicenseRef-Unknown', 'MIT AND Apache-2.0', 'Apache-2.0 WITH LLVM-exception']) {
    await t.test(license, (child) => {
      const { result, out } = fixture(child, { npmLicense: license });
      assert.notEqual(result.status, 0);
      assert.match(result.stderr, /No license text for/);
      assert.equal(existsSync(out), false);
    });
  }
});

test('CLI preserves the package actual license text instead of inventing a fallback', { skip: process.platform === 'win32' }, (t) => {
  const { result, out } = fixture(t, { npmLicense: 'LicenseRef-Custom', licenseFile: true });
  assert.equal(result.status, 0, result.stderr);
  assert.match(readFileSync(out, 'utf8'), /The actual package license text\./);
});
