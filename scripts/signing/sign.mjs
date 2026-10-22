#!/usr/bin/env node
// Authenticode signing and verification for ZTG tool releases, through Azure Artifact Signing.
//
//   node scripts/signing/sign.mjs check                 pinned roots and settings are intact
//   node scripts/signing/sign.mjs sign <file>           sign in place, then verify
//   node scripts/signing/sign.mjs verify <file>         verify an already signed file
//
// sign reads ZTG_SIGN_NAME and ZTG_SIGN_URL for the signature's description (shown by Windows
// in the UAC prompt and file properties), so makensis can call it with only the file path.
//
// Credentials: AZURE_SIGNING_TENANT_ID and AZURE_SIGNING_CLIENT_ID, plus either the job's
// GitHub OIDC token (permissions: id-token: write; preferred, no stored secret) or
// AZURE_SIGNING_CLIENT_SECRET. The secret or assertion is used once per signature to request an
// access token; the token reaches jsign through a 0600 file, never argv, and no child process
// sees any credential variable. A signed file replaces the original only after it verifies
// against the pinned Microsoft roots, with a timestamp, from the pinned publisher.

import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { copyFile, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  CODE_SIGNING_ROOTS,
  JSIGN,
  TIMESTAMP_ROOTS,
  fetchJsign,
  jsignArguments,
  publisherProblem,
  readAzureConfig,
  requestAccessToken,
  requestGithubAssertion,
  signerFromBundle,
  verificationOutputProblem,
  verifyPinnedRoots,
} from './azure-signing.mjs';

const SECRET_ENVIRONMENT = [
  'AZURE_SIGNING_CLIENT_ID',
  'AZURE_SIGNING_TENANT_ID',
  'AZURE_SIGNING_CLIENT_SECRET',
  'AZURE_TENANT_ID',
  'AZURE_CLIENT_ID',
  'AZURE_CLIENT_SECRET',
  'ACTIONS_ID_TOKEN_REQUEST_TOKEN',
  'ACTIONS_ID_TOKEN_REQUEST_URL',
];

/** The environment for child processes: everything except credentials. */
export function childEnvironment(env = process.env) {
  return Object.fromEntries(Object.entries(env).filter(([name]) => !SECRET_ENVIRONMENT.includes(name)));
}

function run(command, args, capture = false) {
  const result = spawnSync(command, args, {
    stdio: capture ? ['ignore', 'pipe', 'inherit'] : 'inherit',
    env: childEnvironment(),
    encoding: 'utf8',
    maxBuffer: 16 * 1024 * 1024,
  });
  if (result.status !== 0) {
    if (capture && result.stdout) process.stderr.write(result.stdout);
    throw new Error(`${command} failed`);
  }
  return capture ? result.stdout : '';
}

/** The trust every release must meet: reviewed settings plus the pinned roots. */
export async function signingTrust() {
  await verifyPinnedRoots();
  const config = await readAzureConfig();
  return {
    azure: { endpoint: config.endpoint, account: config.account, profile: config.profile },
    publisher: config.publisher,
    verifyArgs: ['-CAfile', CODE_SIGNING_ROOTS, '-TSA-CAfile', TIMESTAMP_ROOTS],
  };
}

/** Verifies the signature chain and timestamp against the pinned roots, then who signed. */
export async function verifyAuthenticode(file, trust, runner = run) {
  const temp = await mkdtemp(resolve(tmpdir(), 'ztg-verify-'));
  try {
    const signature = resolve(temp, 'signature.der');
    runner('osslsigncode', ['extract-signature', '-in', file, '-out', signature]);
    const signer = signerFromBundle(runner('openssl', ['pkcs7', '-inform', 'DER', '-in', signature, '-print_certs'], true));
    // The leaf hash makes osslsigncode confirm that the certificate examined here is the one
    // that actually signed, so a decoy certificate in the signature cannot stand in for it.
    const output = runner(
      'osslsigncode',
      ['verify', ...trust.verifyArgs, '-require-leaf-hash', `sha256:${createHash('sha256').update(signer.raw).digest('hex')}`, '-in', file],
      true,
    );
    const problem = verificationOutputProblem(output) ?? publisherProblem(signer, trust.publisher);
    if (problem) throw new Error(`${file}: ${problem}`);
  } finally {
    await rm(temp, { recursive: true, force: true });
  }
}

async function credentials(env, fetcher) {
  const base = { tenantId: env.AZURE_SIGNING_TENANT_ID, clientId: env.AZURE_SIGNING_CLIENT_ID };
  if (!base.tenantId || !base.clientId)
    throw new Error('AZURE_SIGNING_TENANT_ID and AZURE_SIGNING_CLIENT_ID are required for signing');
  if (env.AZURE_SIGNING_CLIENT_SECRET) return { ...base, clientSecret: env.AZURE_SIGNING_CLIENT_SECRET };
  return { ...base, clientAssertion: await requestGithubAssertion(env, fetcher) };
}

export async function sign(file, trust, { env = process.env, runner = run, fetcher = fetch, jsign = JSIGN } = {}) {
  const name = env.ZTG_SIGN_NAME;
  const url = env.ZTG_SIGN_URL;
  if (!name || !/^https:\/\/[^\s]+$/.test(url ?? ''))
    throw new Error('ZTG_SIGN_NAME and an https ZTG_SIGN_URL are required for signing');
  const temp = await mkdtemp(resolve(tmpdir(), 'ztg-sign-'));
  try {
    const jar = await fetchJsign(temp, fetcher, jsign);
    const tokenFile = resolve(temp, 'access-token');
    await writeFile(tokenFile, await requestAccessToken(await credentials(env, fetcher), fetcher), { mode: 0o600 });
    const output = resolve(temp, 'signed.bin');
    await copyFile(file, output);
    runner('java', jsignArguments({ jar, config: trust.azure, tokenFile, executable: output, name, url }));
    await rm(tokenFile, { force: true });
    await verifyAuthenticode(output, trust, runner);
    await writeFile(file, await readFile(output));
  } finally {
    await rm(temp, { recursive: true, force: true });
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [command, file] = process.argv.slice(2);
  try {
    if (command === 'check') {
      await signingTrust();
      console.log('Signing trust: pinned roots and settings are intact.');
    } else if ((command === 'sign' || command === 'verify') && file) {
      const trust = await signingTrust();
      if (command === 'sign') await sign(resolve(file), trust);
      else await verifyAuthenticode(resolve(file), trust);
      console.log(`${command === 'sign' ? 'Signed and verified' : 'Verified'}: ${file}`);
    } else {
      console.error('Usage: sign.mjs check | sign <file> | verify <file>');
      process.exitCode = 2;
    }
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
