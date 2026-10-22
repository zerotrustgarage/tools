import assert from 'node:assert/strict';
import { createHash, X509Certificate } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import {
  CODE_SIGNING_ROOTS,
  FEDERATED_AUDIENCE,
  IDENTITY_VERIFICATION_ROOT_2020,
  TIMESTAMP_ROOTS,
  azureConfigProblem,
  fetchJsign,
  jsignArguments,
  pinnedRootsProblem,
  publisherProblem,
  readAzureConfig,
  requestAccessToken,
  requestGithubAssertion,
  signerFromBundle,
  verificationOutputProblem,
  verifyPinnedRoots,
} from './azure-signing.mjs';
import { createTestCa, hasTool } from './test-ca.mjs';

const TENANT = '11111111-2222-3333-4444-555555555555';
const CLIENT = '66666666-7777-8888-9999-000000000000';
const response = (status, body) => ({ ok: status >= 200 && status < 300, status, json: async () => body });

test('the committed Microsoft roots match the pinned fingerprint', async () => {
  await verifyPinnedRoots();
  for (const file of [CODE_SIGNING_ROOTS, TIMESTAMP_ROOTS]) {
    const cert = new X509Certificate(readFileSync(file));
    assert.equal(createHash('sha256').update(cert.raw).digest('hex'), IDENTITY_VERIFICATION_ROOT_2020);
  }
});

test('a swapped or padded root file is refused', () => {
  const pem = readFileSync(CODE_SIGNING_ROOTS, 'utf8');
  assert.match(pinnedRootsProblem(`${pem}\nextra`, [IDENTITY_VERIFICATION_ROOT_2020]), /only PEM certificates/);
  assert.match(pinnedRootsProblem(pem, ['00'.repeat(32)]), /exactly the pinned certificates/);
  assert.match(pinnedRootsProblem(`${pem}\n-----BEGIN PRIVATE KEY-----\nx\n-----END PRIVATE KEY-----`, []), /private key/);
});

test('the settings are the same reviewed trust as the Jackpoint agent', async () => {
  const config = await readAzureConfig();
  assert.equal(config.endpoint, 'eus.codesigning.azure.net');
  assert.equal(config.account, 'proudlock-signing');
  assert.equal(config.profile, 'echowire-public');
  assert.equal(config.publisher.pinned, true);
  assert.match(config.publisher.subject, /^CN=Proudlock Technology LLC,/);
  assert.match(config.publisher.profileEku, /^1\.3\.6\.1\.4\.1\.311\.97\./);
});

test('settings validation refuses unsafe values', () => {
  const ok = { endpoint: 'eus.codesigning.azure.net', account: 'acct-1', profile: 'profile-one', publisher: { subject: 'CN=x' } };
  assert.equal(azureConfigProblem(ok), null);
  assert.match(azureConfigProblem({ ...ok, endpoint: 'evil.example.com' }), /endpoint/);
  assert.match(azureConfigProblem({ ...ok, extra: 1 }), /unknown field/);
  assert.match(azureConfigProblem({ ...ok, publisher: { subject: 'CN=x', profileEku: '1.3.6.1.4.1.311.97.1.0' } }), /profileEku/);
});

test('verification output must show one signature and a verified timestamp', () => {
  const good = 'Signature verification: ok\nTimestamp Server Signature verification: ok\nNumber of verified signatures: 1\n';
  assert.equal(verificationOutputProblem(good), null);
  assert.match(verificationOutputProblem(good.replace(': 1', ': 2')), /exactly one/);
  assert.match(verificationOutputProblem('Signature verification: ok\nTimestamp is not available\nNumber of verified signatures: 1\n'), /no timestamp/);
  assert.match(verificationOutputProblem('Signature verification: ok\nNumber of verified signatures: 1\n'), /timestamp did not verify/);
  assert.match(verificationOutputProblem(good.replace('Signature verification: ok', 'Signature verification: failed')), /did not verify/);
});

test('a client secret or a federated assertion buys an access token', async () => {
  const sent = [];
  const fetcher = async (url, init) => {
    sent.push(Object.fromEntries(new URLSearchParams(init.body)));
    assert.equal(String(url), `https://login.microsoftonline.com/${TENANT}/oauth2/v2.0/token`);
    return response(200, { token_type: 'Bearer', access_token: 'abc.def' });
  };
  assert.equal(await requestAccessToken({ tenantId: TENANT, clientId: CLIENT, clientSecret: 's3cret' }, fetcher), 'abc.def');
  assert.equal(await requestAccessToken({ tenantId: TENANT, clientId: CLIENT, clientAssertion: 'jwt.from.github' }, fetcher), 'abc.def');
  assert.equal(sent[0].client_secret, 's3cret');
  assert.equal(sent[0].client_assertion, undefined);
  assert.equal(sent[1].client_assertion, 'jwt.from.github');
  assert.equal(sent[1].client_assertion_type, 'urn:ietf:params:oauth:client-assertion-type:jwt-bearer');
  assert.equal(sent[1].client_secret, undefined);
  assert.equal(sent[1].scope, 'https://codesigning.azure.net/.default');
});

test('token failures report the status and OAuth code only', async () => {
  const fetcher = async () => response(401, { error: 'invalid_client', error_description: 'secret value echoed here' });
  await assert.rejects(requestAccessToken({ tenantId: TENANT, clientId: CLIENT, clientSecret: 'x' }, fetcher), (error) => {
    assert.equal(error.message, 'Azure access token request failed (HTTP 401, invalid_client)');
    return true;
  });
  await assert.rejects(requestAccessToken({ tenantId: 'not-a-guid', clientId: CLIENT, clientSecret: 'x' }), /GUIDs/);
  await assert.rejects(requestAccessToken({ tenantId: TENANT, clientId: CLIENT }), /assertion or secret/);
});

test('the GitHub assertion is requested for the Entra exchange audience', async () => {
  let seen;
  const fetcher = async (url, init) => {
    seen = { url: new URL(url), auth: init.headers.authorization };
    return response(200, { value: 'header.payload.signature' });
  };
  const env = { ACTIONS_ID_TOKEN_REQUEST_URL: 'https://token.actions.example/x?api-version=2.0', ACTIONS_ID_TOKEN_REQUEST_TOKEN: 'runner-token' };
  assert.equal(await requestGithubAssertion(env, fetcher), 'header.payload.signature');
  assert.equal(seen.url.searchParams.get('audience'), FEDERATED_AUDIENCE);
  assert.equal(seen.url.searchParams.get('api-version'), '2.0');
  assert.equal(seen.auth, 'Bearer runner-token');
  await assert.rejects(requestGithubAssertion({}, fetcher), /id-token: write/);
  await assert.rejects(requestGithubAssertion({ ...env, ACTIONS_ID_TOKEN_REQUEST_URL: 'http://plain.example/' }, fetcher), /https/);
});

test('the jsign download must match its pin', async () => {
  const bytes = Buffer.from('not jsign');
  const pin = { version: 't', url: 'https://x/jsign.jar', sha256: '00'.repeat(32), maxBytes: 1024 };
  const fetcher = async () => ({ ok: true, arrayBuffer: async () => bytes });
  await assert.rejects(fetchJsign('/tmp', fetcher, pin), /pinned SHA-256/);
});

test('jsign gets the token by file, never on the command line', () => {
  const args = jsignArguments({
    jar: 'j.jar',
    config: { endpoint: 'eus.codesigning.azure.net', account: 'a', profile: 'p' },
    tokenFile: '/tmp/t',
    executable: 'x.exe',
    name: 'ZTG Markdown',
    url: 'https://ztg.sh',
  });
  assert.equal(args[args.indexOf('--storepass') + 1], 'file:/tmp/t');
  assert.equal(args[args.indexOf('--name') + 1], 'ZTG Markdown');
  assert.equal(args[args.indexOf('--url') + 1], 'https://ztg.sh');
  assert.equal(args[args.indexOf('--tsmode') + 1], 'RFC3161');
  assert.equal(args.at(-1), 'x.exe');
});

test('the publisher pin tells customers of the same root apart', { skip: !hasTool('openssl') && 'openssl not installed' }, () => {
  const ca = createTestCa();
  const pinned = { subject: ca.publisherSubject, pinned: true };
  const cert = (path) => new X509Certificate(readFileSync(path));
  assert.equal(publisherProblem(cert(ca.publisher.cert), pinned), null);
  assert.match(publisherProblem(cert(ca.otherCustomer.cert), pinned), /differs from the pinned publisher/);
  assert.match(publisherProblem(cert(ca.tlsOnly.cert), pinned), /not allowed for code signing/);
  assert.match(publisherProblem(cert(ca.publisher.cert), { ...pinned, profileEku: '1.3.6.1.4.1.311.97.9.9' }), /lacks the pinned profile EKU/);
  assert.match(publisherProblem(cert(ca.publisher.cert), { subject: 'REPLACE_ME', pinned: false }), /not pinned yet/);
  const bundle = readFileSync(ca.publisher.cert, 'utf8') + readFileSync(ca.ca, 'utf8');
  assert.equal(signerFromBundle(bundle).subject, cert(ca.publisher.cert).subject);
  assert.throws(() => signerFromBundle(bundle + readFileSync(ca.otherCustomer.cert, 'utf8')), /exactly one signer/);
});
