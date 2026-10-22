import { Buffer } from 'node:buffer';
import { createHash, X509Certificate } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { URLSearchParams, fileURLToPath } from 'node:url';

// Azure Artifact Signing (formerly Trusted Signing) trust for ZTG tool releases. Ported from
// zerotrustgarage/infrasec scripts/releases/azure-signing.mjs (commit d6baf53), which signs the
// Jackpoint agent: the same account, certificate profile, pinned publisher and pinned Microsoft
// roots, so both repositories enforce one trust. Differences: the signature description is a
// parameter, and the access token can come from a GitHub OIDC federated credential instead of a
// client secret. Everything here is public configuration or pure checking; credentials are
// handled only by sign() in sign.mjs.
const directory = dirname(fileURLToPath(import.meta.url));
export const AZURE_CONFIG = resolve(directory, 'azure.json');
export const CODE_SIGNING_ROOTS = resolve(directory, 'microsoft-code-signing-roots.pem');
export const TIMESTAMP_ROOTS = resolve(directory, 'microsoft-timestamp-roots.pem');
export const AZURE_STATUS = 'azure-artifact-signing';
export const CODE_SIGNING_USAGE = '1.3.6.1.5.5.7.3.3';
const PUBLIC_TRUST_EKU = '1.3.6.1.4.1.311.97.1.0';
export const PLACEHOLDER = 'REPLACE_ME';
export const TIMESTAMP_URL = 'http://timestamp.acs.microsoft.com';
export const TOKEN_SCOPE = 'https://codesigning.azure.net/.default';

// SHA-256 of the DER encoding of each pinned Microsoft root. Source: Microsoft PKI repository,
// https://www.microsoft.com/pkiops/certs/microsoft%20identity%20verification%20root%20certificate%20authority%202020.crt
// (DER, converted to PEM). The same value is listed for "Code Signing;Time Stamping" in
// Microsoft's CCADB Included CA Certificate Report. Microsoft documents that Artifact Signing
// Public Trust leaves are issued from this root, and timestamp.acs.microsoft.com answers with a
// chain (Microsoft Public RSA Timestamping CA 2020) that ends at the same root.
export const IDENTITY_VERIFICATION_ROOT_2020 =
  '5367f20c7ade0e2bca790915056d086b720c33c1fa2a2661acf787e3292e1270';
export const PINNED_CODE_SIGNING_ROOTS = [IDENTITY_VERIFICATION_ROOT_2020];
export const PINNED_TIMESTAMP_ROOTS = [IDENTITY_VERIFICATION_ROOT_2020];

// jsign 7.5 from Maven Central (net.jsign:jsign). The same bytes are published as the GitHub
// release asset ebourg/jsign 7.5; both sources were compared when this pin was made. The
// maximum size stops an unexpected download from being buffered before the digest is checked.
export const JSIGN = Object.freeze({
  version: '7.5',
  url: 'https://repo1.maven.org/maven2/net/jsign/jsign/7.5/jsign-7.5.jar',
  sha256: '602a51c3545a6dc4fb99bd2ea7152b26d1345916d0c93ddfbd5936cb735af91c',
  maxBytes: 8 * 1024 * 1024,
});

const GUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const placeholder = (value) =>
  typeof value !== 'string' || !value.trim() || value.includes(PLACEHOLDER);

const sha256Hex = (bytes) => createHash('sha256').update(bytes).digest('hex');
const certificateBlocks = (pem) =>
  pem.match(/-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/g) ?? [];

/** Why a pinned root file is unacceptable, or null when it holds exactly the pinned roots. */
export function pinnedRootsProblem(pem, fingerprints) {
  if (/PRIVATE KEY/.test(pem)) return 'must not contain a private key';
  const blocks = certificateBlocks(pem);
  // Nothing but whole certificates may sit in the file, so extra material cannot ride along.
  if (blocks.reduce((rest, block) => rest.replace(block, ''), pem).trim() !== '')
    return 'must contain only PEM certificates';
  let certificates;
  try {
    certificates = blocks.map((block) => new X509Certificate(block));
  } catch {
    return 'is not a certificate';
  }
  const actual = certificates.map((certificate) => sha256Hex(certificate.raw)).sort();
  if (
    actual.length !== fingerprints.length ||
    actual.some((value, index) => value !== [...fingerprints].sort()[index])
  )
    return `does not hold exactly the pinned certificates (sha256 ${actual.join(', ') || 'none'})`;
  for (const certificate of certificates)
    if (
      !certificate.ca ||
      certificate.subject !== certificate.issuer ||
      !certificate.verify(certificate.publicKey)
    )
      return 'must hold self-signed CA certificates only';
  return null;
}
/** Throws unless both committed Microsoft root files still match their pinned fingerprints. */
export async function verifyPinnedRoots(
  files = [
    [CODE_SIGNING_ROOTS, PINNED_CODE_SIGNING_ROOTS],
    [TIMESTAMP_ROOTS, PINNED_TIMESTAMP_ROOTS],
  ],
) {
  for (const [file, fingerprints] of files) {
    let pem;
    try {
      pem = await readFile(file, 'utf8');
    } catch {
      throw new Error(`Pinned Microsoft root file ${file} is missing`);
    }
    const problem = pinnedRootsProblem(pem, fingerprints);
    if (problem) throw new Error(`Pinned Microsoft root file ${file} ${problem}`);
  }
}

/** Validates reviewed non-secret settings. The publisher pin may still be a placeholder. */
export function azureConfigProblem(config) {
  if (!config || typeof config !== 'object' || Array.isArray(config))
    return 'must be a JSON object';
  const unknown = Object.keys(config).filter(
    (key) => !['endpoint', 'account', 'profile', 'publisher'].includes(key),
  );
  if (unknown.length) return `has unknown field ${unknown[0]}`;
  for (const field of ['endpoint', 'account', 'profile'])
    if (placeholder(config[field]))
      return `field ${field} is missing or still ${PLACEHOLDER}`;
  // The access token is sent to this host by jsign, so it is restricted to the service domain.
  if (!/^[a-z0-9]{2,10}\.codesigning\.azure\.net$/.test(config.endpoint))
    return 'field endpoint must be an Artifact Signing endpoint host such as eus.codesigning.azure.net';
  if (!/^[A-Za-z0-9](?:-?[A-Za-z0-9]){2,23}$/.test(config.account))
    return 'field account must be an Artifact Signing account name';
  if (!/^[A-Za-z](?:-?[A-Za-z0-9]){3,99}$/.test(config.profile))
    return 'field profile must be a certificate profile name';
  const publisher = config.publisher;
  if (!publisher || typeof publisher !== 'object' || Array.isArray(publisher))
    return 'field publisher must be an object';
  const extra = Object.keys(publisher).filter(
    (key) => !['subject', 'profileEku'].includes(key),
  );
  if (extra.length) return `publisher has unknown field ${extra[0]}`;
  if (typeof publisher.subject !== 'string' || !publisher.subject.trim())
    return 'publisher.subject is required (use REPLACE_ME until it is known)';
  if (!placeholder(publisher.subject) && !/^[^\r\n]{3,1024}$/.test(publisher.subject))
    return 'publisher.subject must be a single line';
  if (
    publisher.profileEku !== undefined &&
    (!/^1\.3\.6\.1\.4\.1\.311\.97(?:\.\d+)+$/.test(publisher.profileEku) ||
      publisher.profileEku === PUBLIC_TRUST_EKU)
  )
    return 'publisher.profileEku must be an Artifact Signing profile OID (1.3.6.1.4.1.311.97...)';
  return null;
}
/**
 * Reads the reviewed settings. Endpoint, account and profile must be real: signing and
 * verification both stop while any of them is a placeholder. The publisher subject alone may
 * remain a placeholder: signing can proceed, and `publisher.pinned` is false, which makes every
 * verification refuse and report the observed signer so it can be pinned.
 */
export async function readAzureConfig(file = AZURE_CONFIG) {
  let config;
  try {
    config = JSON.parse(await readFile(file, 'utf8'));
  } catch (error) {
    throw new Error(
      error?.code === 'ENOENT'
        ? `Azure Artifact Signing is not configured: ${file} is missing`
        : `Azure Artifact Signing settings ${file} are not valid JSON`,
      { cause: error },
    );
  }
  const problem = azureConfigProblem(config);
  if (problem) throw new Error(`Azure Artifact Signing is not configured: ${problem}`);
  return {
    endpoint: config.endpoint,
    account: config.account,
    profile: config.profile,
    publisher: {
      subject: config.publisher.subject,
      pinned: !placeholder(config.publisher.subject),
      profileEku: config.publisher.profileEku,
    },
  };
}

/**
 * Names are rendered most specific first with commas, the order and escaping of
 * `openssl x509 -nameopt RFC2253` (Node already escapes commas in values). This is the exact
 * string azure.json pins, and the one verification failures print.
 */
export const canonicalName = (name) => name.split('\n').reverse().join(',');
export function signerIdentity(certificate) {
  return {
    subject: canonicalName(certificate.subject),
    issuer: canonicalName(certificate.issuer),
    sha256: sha256Hex(certificate.raw),
    // 1.3.6.1.4.1.311.97.1.0 only marks a Public Trust certificate; the durable per-profile
    // value is the other Artifact Signing EKU.
    profileEku: (certificate.keyUsage ?? []).find(
      (usage) => usage.startsWith('1.3.6.1.4.1.311.97.') && usage !== PUBLIC_TRUST_EKU,
    ),
  };
}
const observed = ({ subject, issuer, sha256, profileEku }) =>
  `observed signer subject: ${subject} (issuer ${issuer}, sha256 ${sha256}` +
  `${profileEku ? `, profile EKU ${profileEku}` : ''})`;

/**
 * The signer leaf must be the pinned publisher. Why this is needed on top of the chain check:
 * every Artifact Signing customer, not only this repository, chains to the same Microsoft root,
 * so a valid chain says nothing about WHO signed. The subject is the validated organization
 * identity and stays constant across the daily certificate renewals (the certificate itself,
 * its key and its thumbprint change every day, so none of those can be pinned). The optional
 * profile EKU is Microsoft's durable per-profile identity and, when set, must also be present.
 */
export function publisherProblem(certificate, publisher) {
  const identity = signerIdentity(certificate);
  if (!publisher?.pinned)
    return (
      `Publisher is not pinned yet; ${observed(identity)}. ` +
      'Pin it in scripts/signing/azure.json'
    );
  if (identity.subject !== publisher.subject)
    return (
      `Signer subject differs from the pinned publisher ${publisher.subject}; ` +
      observed(identity)
    );
  if (certificate.ca)
    return `Signer certificate must not be a CA; ${observed(identity)}`;
  if (!(certificate.keyUsage ?? []).includes(CODE_SIGNING_USAGE))
    return `Signer certificate is not allowed for code signing; ${observed(identity)}`;
  if (
    publisher.profileEku &&
    !(certificate.keyUsage ?? []).includes(publisher.profileEku)
  )
    return `Signer certificate lacks the pinned profile EKU ${publisher.profileEku}; ${observed(identity)}`;
  return null;
}
/** The signature must carry exactly one non-CA certificate: the signer. Extra leaves are refused. */
export function signerFromBundle(pem) {
  const blocks = certificateBlocks(pem);
  if (!blocks.length || blocks.length > 16)
    throw new Error('Signature carries no usable certificate chain');
  const leaves = blocks
    .map((block) => new X509Certificate(block))
    .filter((certificate) => !certificate.ca);
  if (leaves.length !== 1)
    throw new Error(
      `Signature must carry exactly one signer certificate (found ${leaves.length})`,
    );
  return leaves[0];
}
/**
 * osslsigncode exits 0 for an untimestamped signature whose short-lived leaf is still valid, and
 * for a timestamp it could not verify. Artifact Signing leaves live about 3 days, so the
 * timestamp is the only thing that keeps a signature valid later: demand proof of it, and of
 * exactly one signature (extra signatures are not allowed to pass for the primary one).
 */
export function verificationOutputProblem(output) {
  if (!/^Number of verified signatures: 1$/m.test(output))
    return 'File must carry exactly one Authenticode signature';
  if (/^Timestamp is not available$/m.test(output))
    return 'Signature has no timestamp; a timestamp is required';
  if (!/^Timestamp Server Signature verification: ok$/m.test(output))
    return 'Signature timestamp did not verify against the pinned Microsoft timestamp roots';
  if (
    !/^Signature verification: ok$/m.test(output) ||
    /verification: failed/.test(output)
  )
    return 'Signature did not verify against the pinned Microsoft code signing roots';
  return null;
}

export const FEDERATED_AUDIENCE = 'api://AzureADTokenExchange';
const ASSERTION_TYPE = 'urn:ietf:params:oauth:client-assertion-type:jwt-bearer';

/**
 * The GitHub Actions OIDC token for Entra's federated credential exchange. It is short lived and
 * names this repository, environment and workflow in its subject, which the federated
 * credential matches, so no long-lived secret exists anywhere.
 */
export async function requestGithubAssertion(env, fetcher = fetch) {
  const url = env.ACTIONS_ID_TOKEN_REQUEST_URL;
  const bearer = env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
  if (!url || !bearer)
    throw new Error('No GitHub OIDC token is available: the job needs permissions id-token: write');
  const target = new URL(url);
  if (target.protocol !== 'https:') throw new Error('GitHub OIDC token URL must be https');
  target.searchParams.set('audience', FEDERATED_AUDIENCE);
  const response = await fetcher(target, {
    redirect: 'error',
    signal: globalThis.AbortSignal.timeout(30_000),
    headers: { authorization: `Bearer ${bearer}` },
  });
  if (!response.ok) throw new Error(`GitHub OIDC token request failed (HTTP ${response.status})`);
  const value = (await response.json().catch(() => ({})))?.value;
  if (typeof value !== 'string' || value.length > 16 * 1024 || !/^[A-Za-z0-9._-]+$/.test(value))
    throw new Error('GitHub OIDC token response is invalid');
  return value;
}

/**
 * OAuth2 client credentials grant for the Artifact Signing data plane, authenticated either by
 * a federated assertion (preferred) or by a client secret (the infrasec setup's method).
 */
export async function requestAccessToken(
  { tenantId, clientId, clientSecret, clientAssertion },
  fetcher = fetch,
) {
  if (!GUID.test(tenantId ?? '') || !GUID.test(clientId ?? '') || !(clientSecret || clientAssertion))
    throw new Error(
      'Azure signing credentials are invalid: tenant and client ids must be GUIDs and a federated assertion or secret is required',
    );
  const proof = clientAssertion
    ? { client_assertion_type: ASSERTION_TYPE, client_assertion: clientAssertion }
    : { client_secret: clientSecret };
  const response = await fetcher(
    `https://login.microsoftonline.com/${tenantId}/oauth2/v2.0/token`,
    {
      method: 'POST',
      redirect: 'error',
      signal: globalThis.AbortSignal.timeout(30_000),
      headers: { 'content-type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({
        grant_type: 'client_credentials',
        client_id: clientId,
        ...proof,
        scope: TOKEN_SCOPE,
      }),
    },
  );
  let body;
  try {
    body = await response.json();
  } catch {
    body = {};
  }
  // Only the status and the OAuth error code are reported; the body is never echoed.
  if (!response.ok) {
    const code = /^[a-z_]{1,64}$/.test(body?.error ?? '') ? `, ${body.error}` : '';
    throw new Error(`Azure access token request failed (HTTP ${response.status}${code})`);
  }
  const token = body.access_token;
  if (
    String(body.token_type).toLowerCase() !== 'bearer' ||
    typeof token !== 'string' ||
    token.length > 16 * 1024 ||
    !/^[A-Za-z0-9._~+/=-]+$/.test(token)
  )
    throw new Error('Azure access token response is invalid');
  return token;
}

/** Downloads the pinned jsign jar into directory and refuses anything but the pinned bytes. */
export async function fetchJsign(target, fetcher = fetch, pin = JSIGN) {
  const response = await fetcher(pin.url, {
    redirect: 'error',
    signal: globalThis.AbortSignal.timeout(120_000),
  });
  if (!response.ok) throw new Error(`jsign ${pin.version} download failed`);
  const bytes = Buffer.from(await response.arrayBuffer());
  if (bytes.length === 0 || bytes.length > pin.maxBytes)
    throw new Error(`jsign ${pin.version} download has an unexpected size`);
  if (sha256Hex(bytes) !== pin.sha256)
    throw new Error(`jsign ${pin.version} download does not match its pinned SHA-256`);
  const jar = resolve(target, `jsign-${pin.version}.jar`);
  await writeFile(jar, bytes, { mode: 0o600 });
  return jar;
}
/** The token travels in a 0600 file named by `file:`, never on argv. */
export function jsignArguments({ jar, config, tokenFile, executable, name, url }) {
  return [
    '-jar',
    jar,
    '--storetype',
    'TRUSTEDSIGNING',
    '--keystore',
    config.endpoint,
    '--storepass',
    `file:${tokenFile}`,
    '--alias',
    `${config.account}/${config.profile}`,
    '--tsaurl',
    TIMESTAMP_URL,
    '--tsmode',
    'RFC3161',
    '--alg',
    'SHA-256',
    '--name',
    name,
    '--url',
    url,
    executable,
  ];
}
