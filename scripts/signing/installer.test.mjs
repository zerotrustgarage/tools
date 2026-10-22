import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { childEnvironment, sign } from './sign.mjs';
import { createTestCa, hasTool } from './test-ca.mjs';

// End-to-end checks of the release mechanics with a throwaway CA standing in for Azure:
// the NSIS script signs both binaries it produces, and sign() never lets a signature that
// fails verification replace the original file.

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const nsi = join(repo, 'installer', 'windows', 'ztg-markdown.nsi');
const tools = hasTool('makensis') && hasTool('osslsigncode') && hasTool('openssl');
const skip = !tools && 'makensis, osslsigncode and openssl are required';

function testSigner(ca, dir) {
  const script = join(dir, 'test-sign.sh');
  writeFileSync(
    script,
    `#!/bin/sh
set -e
osslsigncode sign -certs '${ca.publisher.cert}' -key '${ca.publisher.key}' -n 'ZTG Markdown' -i https://ztg.sh -in "$1" -out "$1.signed" >/dev/null
osslsigncode verify -CAfile '${ca.ca}' -in "$1.signed" | grep -q 'Signature verification: ok'
mv "$1.signed" "$1"
echo "$1" >> '${join(dir, 'signed.log')}'
`,
  );
  chmodSync(script, 0o755);
  return script;
}

function makensis(dir, defines) {
  const args = ['-V2', ...Object.entries(defines).map(([k, v]) => `-D${k}=${v}`), nsi];
  const result = spawnSync('makensis', args, { cwd: dirname(nsi), encoding: 'utf8' });
  assert.equal(result.status, 0, result.stdout + result.stderr);
}

test('the installer and its generated uninstaller are both signed', { skip }, () => {
  const ca = createTestCa();
  const dir = mkdtempSync(join(tmpdir(), 'ztg-nsis-'));
  writeFileSync(join(dir, 'app.exe'), 'placeholder application');
  writeFileSync(join(dir, 'NOTICES.txt'), 'notices');
  const out = join(dir, 'Setup.exe');
  makensis(dir, { VERSION: '1.2.3', APP_EXE: join(dir, 'app.exe'), NOTICES: join(dir, 'NOTICES.txt'), OUTFILE: out, SIGN_SCRIPT: testSigner(ca, dir) });
  const signed = readFileSync(join(dir, 'signed.log'), 'utf8').trim().split('\n');
  assert.equal(signed.length, 2, 'uninstaller and installer');
  assert.equal(signed[1], out);
  const verify = execFileSync('osslsigncode', ['verify', '-CAfile', ca.ca, '-in', out], { encoding: 'utf8' });
  assert.match(verify, /Signature verification: ok/);
  assert.match(verify, /Number of verified signatures: 1/);
});

test('the installer refuses to build without its inputs', { skip }, () => {
  const result = spawnSync('makensis', ['-V2', '-DVERSION=1.0.0', nsi], { cwd: dirname(nsi), encoding: 'utf8' });
  assert.notEqual(result.status, 0);
  assert.match(result.stdout + result.stderr, /APP_EXE must be defined/);
});

test('an unsigned build is possible for CI, with no signing hook', { skip }, () => {
  const dir = mkdtempSync(join(tmpdir(), 'ztg-nsis-'));
  writeFileSync(join(dir, 'app.exe'), 'x');
  writeFileSync(join(dir, 'N.txt'), 'n');
  makensis(dir, { VERSION: '1.0.0', APP_EXE: join(dir, 'app.exe'), NOTICES: join(dir, 'N.txt'), OUTFILE: join(dir, 'S.exe') });
  const verify = spawnSync('osslsigncode', ['verify', '-in', join(dir, 'S.exe')], { encoding: 'utf8' });
  assert.notEqual(verify.status, 0);
});

test('a signature without a timestamp never replaces the original', { skip }, async () => {
  const ca = createTestCa();
  const dir = mkdtempSync(join(tmpdir(), 'ztg-sign-'));
  // A real PE file to sign: an unsigned installer.
  writeFileSync(join(dir, 'app.exe'), 'x');
  writeFileSync(join(dir, 'N.txt'), 'n');
  const target = join(dir, 'target.exe');
  makensis(dir, { VERSION: '1.0.0', APP_EXE: join(dir, 'app.exe'), NOTICES: join(dir, 'N.txt'), OUTFILE: target });
  const before = createHash('sha256').update(readFileSync(target)).digest('hex');

  const jar = Buffer.from('stand-in jar');
  const jsign = { version: 'test', url: 'https://jsign.example/j.jar', sha256: createHash('sha256').update(jar).digest('hex'), maxBytes: 1024 };
  const requests = [];
  const fetcher = async (url, init) => {
    requests.push(String(url));
    if (String(url).startsWith('https://jsign.example/')) return { ok: true, arrayBuffer: async () => jar };
    if (String(url).startsWith('https://token.actions.example/')) return { ok: true, status: 200, json: async () => ({ value: 'gh.oidc.jwt' }) };
    assert.match(String(init.body), /client_assertion=gh.oidc.jwt/);
    return { ok: true, status: 200, json: async () => ({ token_type: 'Bearer', access_token: 'azure.token' }) };
  };
  // Stands in for java/jsign: signs with the test publisher (no timestamp server is reachable
  // offline), then lets the real osslsigncode and openssl verification run.
  const runner = (command, args, capture) => {
    if (command === 'java') {
      const file = args.at(-1);
      assert.ok(args.includes('file:' + join(dirname(file), 'access-token')));
      execFileSync('osslsigncode', ['sign', '-certs', ca.publisher.cert, '-key', ca.publisher.key, '-in', file, '-out', `${file}.s`]);
      execFileSync('mv', [`${file}.s`, file]);
      return '';
    }
    const result = spawnSync(command, args, { encoding: 'utf8' });
    if (result.status !== 0 && !(command === 'osslsigncode' && args[0] === 'verify')) throw new Error(`${command} failed`);
    return capture ? result.stdout : '';
  };
  const trust = {
    azure: { endpoint: 'eus.codesigning.azure.net', account: 'acct', profile: 'prof' },
    publisher: { subject: ca.publisherSubject, pinned: true },
    verifyArgs: ['-CAfile', ca.ca, '-TSA-CAfile', ca.ca],
  };
  const env = {
    AZURE_SIGNING_TENANT_ID: '11111111-2222-3333-4444-555555555555',
    AZURE_SIGNING_CLIENT_ID: '66666666-7777-8888-9999-000000000000',
    ACTIONS_ID_TOKEN_REQUEST_URL: 'https://token.actions.example/t',
    ACTIONS_ID_TOKEN_REQUEST_TOKEN: 'runner',
    ZTG_SIGN_NAME: 'ZTG Markdown',
    ZTG_SIGN_URL: 'https://ztg.sh',
  };
  await assert.rejects(sign(target, trust, { env, runner, fetcher, jsign }), /no timestamp/);
  assert.equal(createHash('sha256').update(readFileSync(target)).digest('hex'), before, 'original untouched');
  assert.equal(requests.length, 3, 'jsign, GitHub OIDC, Entra token');
});

test('child processes never see signing credentials', () => {
  const env = childEnvironment({
    PATH: '/bin',
    AZURE_SIGNING_CLIENT_SECRET: 's',
    AZURE_SIGNING_CLIENT_ID: 'c',
    ACTIONS_ID_TOKEN_REQUEST_TOKEN: 't',
    ACTIONS_ID_TOKEN_REQUEST_URL: 'u',
  });
  assert.deepEqual(env, { PATH: '/bin' });
});
