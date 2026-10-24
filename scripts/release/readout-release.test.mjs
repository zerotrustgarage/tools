import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { createTestCa, hasTool } from '../signing/test-ca.mjs';
import {
  UPGRADE_CODE,
  assemble,
  checkMetadata,
  dryRunManifest,
  expectedAssets,
  readSources,
  verifyDist,
  versionProblem,
} from './readout-release.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const tmp = (prefix) => mkdtempSync(join(tmpdir(), prefix));

test('the repository metadata agrees and the tag must match it', async () => {
  const sources = await readSources(root);
  assert.deepEqual(checkMetadata(sources), []);
  assert.deepEqual(checkMetadata(sources, { tag: `readout-v${sources.packageVersion}` }), []);
  assert.match(checkMetadata(sources, { tag: 'readout-v9.9.9' }).join('\n'), /does not match/);
  assert.match(checkMetadata(sources, { tag: `scanner-v${sources.packageVersion}` }).join('\n'), /does not match/);
});

test('each kind of drift is reported', async () => {
  const good = await readSources(root);
  const mutate = (change) => checkMetadata(structuredClone({ ...good, ...change(structuredClone(good)) })).join('\n');
  assert.match(mutate(() => ({ cargoVersion: '9.9.9' })), /Cargo.toml version/);
  assert.match(mutate(() => ({ lockVersion: '9.9.9' })), /Cargo.lock/);
  assert.match(mutate(() => ({ tauriVersion: '9.9.9' })), /tauri.conf.json/);
  assert.match(mutate((s) => ({ wxs: { ...s.wxs, version: '0.1.0' } })), /Product Version/);
  assert.match(mutate((s) => ({ wxs: { ...s.wxs, upgradeCode: '00000000-0000-0000-0000-000000000000' } })), /UpgradeCode.*permanent/);
  assert.match(mutate((s) => ({ wxs: { ...s.wxs, name: 'Readout' } })), /Product Name/);
  assert.match(mutate((s) => ({ wxs: { ...s.wxs, manufacturer: 'Someone' } })), /Manufacturer/);
  assert.match(mutate((s) => ({ wxs: { ...s.wxs, hasMajorUpgrade: false } })), /MajorUpgrade/);
  assert.match(mutate((s) => ({ nsi: { ...s.nsi, product: 'X' } })), /PRODUCT/);
  assert.match(mutate(() => ({ updatesDocNamesSetup: false })), /update installer/);
  assert.match(mutate(() => ({ packageVersion: '1.0.0-rc1' })), /not numeric/);
});

test('version limits follow the MSI ProductVersion rules', () => {
  assert.equal(versionProblem('0.1.0'), null);
  assert.equal(versionProblem('255.255.65535'), null);
  assert.match(versionProblem('256.0.0'), /exceeds/);
  assert.match(versionProblem('0.0.65536'), /exceeds/);
  assert.match(versionProblem('01.0.0'), /not numeric/);
  assert.match(versionProblem('1.0'), /not numeric/);
});

test('the pinned UpgradeCode is the one in the wxs', () => {
  assert.match(readFileSync(join(root, 'readout/installer/ztg-readout.wxs'), 'utf8'), new RegExp(`UpgradeCode="${UPGRADE_CODE}"`));
});

test('the expected asset set names the update checker installer and the MSI', () => {
  assert.deepEqual(expectedAssets('1.2.3').map((a) => a.name), [
    'ZTG-Readout-1.2.3-Setup.exe',
    'ZTG-Readout-1.2.3-x64.msi',
    'ztg-readout-1.2.3.exe',
    'THIRD-PARTY-NOTICES.txt',
    'SHA256SUMS.txt',
  ]);
  assert.throws(() => expectedAssets('1.2'), /not numeric/);
});

function fixture() {
  const dir = tmp('readout-assets-');
  const inputs = { exe: 'exe', setup: 'setup', msi: 'msi', notices: 'notices' };
  for (const [key, text] of Object.entries(inputs)) {
    writeFileSync(join(dir, `${key}.bin`), `${text} bytes`);
    inputs[key] = join(dir, `${key}.bin`);
  }
  return { dir, inputs, out: join(dir, 'dist') };
}

test('assemble produces exactly the expected set and verify-dist detects tampering', async () => {
  const { dir, inputs, out } = fixture();
  try {
    await assemble({ version: '1.2.3', ...inputs, out });
    await verifyDist(out, '1.2.3');
    const manifest = await dryRunManifest(out, '1.2.3');
    assert.match(manifest.status, /UNSIGNED DRY RUN/);
    assert.equal(manifest.assets.length, 5);
    assert.ok(manifest.assets.every((a) => /^[0-9a-f]{64}$/.test(a.sha256)));
    await assert.rejects(assemble({ version: '1.2.3', ...inputs, out }), /not empty/);

    writeFileSync(join(out, 'ZTG-Readout-1.2.3-x64.msi'), 'tampered');
    await assert.rejects(verifyDist(out, '1.2.3'), /checksum mismatch/);
    writeFileSync(join(out, 'ZTG-Readout-1.2.3-x64.msi'), 'msi bytes');
    await verifyDist(out, '1.2.3');
    writeFileSync(join(out, 'extra.zip'), 'x');
    await assert.rejects(verifyDist(out, '1.2.3'), /unexpected \[extra.zip\]/);
    rmSync(join(out, 'extra.zip'));
    rmSync(join(out, 'ZTG-Readout-1.2.3-x64.msi'));
    await assert.rejects(verifyDist(out, '1.2.3'), /missing \[ZTG-Readout-1.2.3-x64.msi\]/);
    await assert.rejects(verifyDist(out, '1.2.4'), /asset set differs/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

const msiTools = hasTool('wixl') && hasTool('msibuild') && hasTool('msiinfo') && hasTool('osslsigncode') && hasTool('openssl');
test('the MSI builds with the pinned identity and takes an Authenticode signature', { skip: !msiTools && 'wixl, msitools and osslsigncode are required' }, () => {
  const dir = tmp('readout-msi-');
  let ca;
  try {
    // A stand-in executable; the MSI test needs the package structure, not the real app.
    mkdirSync(join(dir, 'in'));
    writeFileSync(join(dir, 'in/app.exe'), 'MZ placeholder application');
    writeFileSync(join(dir, 'in/N.txt'), 'notices');
    const script = join(root, 'readout/installer/build-msi.sh');
    const msi = join(dir, 'readout.msi');
    execFileSync(script, [join(dir, 'in/app.exe'), join(dir, 'in/N.txt'), '1.2.3', msi]);
    const property = (name) => {
      const rows = execFileSync('msiinfo', ['export', msi, 'Property'], { encoding: 'utf8' }).split(/\r?\n/);
      return rows.find((r) => r.startsWith(`${name}\t`))?.split('\t')[1];
    };
    assert.equal(property('ProductVersion'), '1.2.3');
    assert.equal(property('ProductName'), 'ZTG Readout');
    assert.equal(property('Manufacturer'), 'Proudlock Technology LLC');
    assert.equal(property('UpgradeCode')?.toLowerCase(), `{${UPGRADE_CODE}}`);
    assert.match(execFileSync('msiinfo', ['export', msi, 'LaunchCondition'], { encoding: 'utf8' }), /WEBVIEW2_VERSION/);
    for (const bad of ['256.0.0', '1.2.3-beta', '1.2']) {
      assert.notEqual(spawnSync(script, [join(dir, 'in/app.exe'), join(dir, 'in/N.txt'), bad, join(dir, 'bad.msi')]).status, 0, bad);
    }
    // Same Authenticode tooling the release verifies with; a throwaway CA stands in for Azure.
    ca = createTestCa();
    const signed = join(dir, 'signed.msi');
    execFileSync('osslsigncode', ['sign', '-certs', ca.publisher.cert, '-key', ca.publisher.key, '-in', msi, '-out', signed]);
    assert.match(execFileSync('osslsigncode', ['verify', '-CAfile', ca.ca, '-in', signed], { encoding: 'utf8' }), /Signature verification: ok/);
    assert.notEqual(spawnSync('osslsigncode', ['verify', '-CAfile', ca.ca, '-in', msi]).status, 0, 'the unsigned MSI must not verify');
    assert.deepEqual(readdirSync(join(dir, 'in')).sort(), ['N.txt', 'app.exe']);
  } finally {
    rmSync(dir, { recursive: true, force: true });
    if (ca) rmSync(ca.dir, { recursive: true, force: true });
  }
});
