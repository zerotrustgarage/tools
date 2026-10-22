import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { createTestCa } from '../../scripts/signing/test-ca.mjs';

// Required in TFTP CI, never silently skipped. TFTP_PAYLOAD holds real cross builds.
const payload = process.env.TFTP_PAYLOAD;
const root = fileURLToPath(new URL('../..', import.meta.url));
const msiScript = join(root, 'tftp/installer/build-msi.sh');
const portable = join(root, 'tftp/installer/portable.sh');
const run = (cmd, args) => execFileSync(cmd, args, { encoding: 'utf8' });
test('packages preserve the binaries, ZIP is reproducible and MSI is signable', () => {
  assert.ok(payload, 'set TFTP_PAYLOAD to the real Windows build with metadata');
  const dir = mkdtempSync(join(tmpdir(), 'tftp packaging '));
  let ca;
  try {
    const zip = join(dir, 'portable.zip');
    const other = join(dir, 'second.zip');
    run(portable, [resolve(payload), zip]);
    // An old entry must not survive rebuilding an archive at the same path.
    writeFileSync(join(dir, 'stale.txt'), 'do not ship');
    execFileSync('zip', ['-j', zip, join(dir, 'stale.txt')]);
    run(portable, [resolve(payload), zip]);
    run(portable, [resolve(payload), other]);
    assert.deepEqual(readFileSync(zip), readFileSync(other));
    const expected = ['ZTGTFTP.exe', 'ztg-tftp.exe', 'THIRD-PARTY-NOTICES.txt', 'tftp.cdx.json', 'README.txt'];
    assert.deepEqual(run('unzip', ['-Z1', zip]).trim().split('\n'), expected);
    for (const file of expected) {
      assert.deepEqual(execFileSync('unzip', ['-p', zip, file], { maxBuffer: 32 * 1024 * 1024 }), readFileSync(join(payload, file)));
    }
    const msi = join(dir, 'tftp.msi');
    run(msiScript, [resolve(payload), '0.4.0', msi]);
    const files = run('msiinfo', ['export', msi, 'File']);
    for (const file of expected) assert.ok(files.includes(file));
    const conditions = run('msiinfo', ['export', msi, 'LaunchCondition']);
    assert.match(conditions, /WEBVIEW2_VERSION/);
    assert.match(conditions, /Installed OR/); // removal and repair work without WebView2
    assert.match(conditions, /newer version/);
    const actions = run('msiinfo', ['export', msi, 'CustomAction']);
    assert.match(actions, /firewall install/);
    assert.match(actions, /firewall remove/);
    assert.match(actions, /FirewallRestore/);
    const sequence = run('msiinfo', ['export', msi, 'InstallExecuteSequence']);
    const rows = new Map(sequence.trim().split(/\r?\n/).slice(3).map(line => { const fields=line.split('\t'); return [fields[0],Number(fields[2])]; }));
    assert.ok(rows.get('RemoveExistingProducts') < rows.get('InstallFiles'), 'remove old firewall rules before installing new ones');
    assert.ok(rows.get('FirewallInstall') > rows.get('InstallFiles'));
    assert.ok(rows.get('FirewallRemove') < rows.get('RemoveFiles'));
    const registry = run('msiinfo', ['export', msi, 'Registry']);
    assert.doesNotMatch(registry, /RunOnce|CurrentVersion\\Run\b/);
    assert.notEqual(spawnSync(msiScript, [resolve(payload), '256.0.0', msi]).status, 0);
    assert.notEqual(spawnSync(msiScript, [resolve(payload), '0.4.0-beta', msi]).status, 0);
    // MSI signing uses the same Authenticode path as the EXEs, with a test CA only.
    ca = createTestCa();
    const signed = join(dir, 'signed.msi');
    run('osslsigncode', ['sign', '-certs', ca.publisher.cert, '-key', ca.publisher.key, '-in', msi, '-out', signed]);
    assert.match(run('osslsigncode', ['verify', '-CAfile', ca.ca, '-in', signed]), /Signature verification: ok/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
    if (ca) rmSync(ca.dir, { recursive: true, force: true });
  }
});
