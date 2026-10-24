#!/usr/bin/env node
// Release metadata, asset manifest and download-set checks for ZTG Readout. It signs nothing and
// reads no credential: the same checks run in PR CI (dry run, unsigned test packages) and in the
// release workflow, so what CI proves is what the release uses. Signing and signature
// verification stay in scripts/signing/sign.mjs; this file only decides which files must exist.
//
//   node scripts/release/readout-release.mjs check [--tag readout-vX.Y.Z] [--print-version]
//   node scripts/release/readout-release.mjs manifest <version>
//   node scripts/release/readout-release.mjs assemble --version V --exe F --setup F --msi F --notices F --out DIR
//   node scripts/release/readout-release.mjs verify-dist <dir> <version>
//   node scripts/release/readout-release.mjs dry-run-manifest <dir> <version>
//
// assemble copies the built files to their release names and writes SHA256SUMS.txt.
// verify-dist fails unless the directory holds exactly the expected assets and every checksum
// matches. dry-run-manifest prints the names and SHA-256 of a verified directory as JSON.

import { createHash } from 'node:crypto';
import { copyFile, mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const UPGRADE_CODE = '231e4623-6212-429c-a15b-44729806918b';
export const PRODUCT = 'ZTG Readout';
export const MANUFACTURER = 'Proudlock Technology LLC';
export const TAG_PREFIX = 'readout-v';
export const CHECKSUMS = 'SHA256SUMS.txt';
const VERSION_PATTERN = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;

/** The files a Readout release must carry. Setup.exe is the name the update checker selects. */
export function expectedAssets(version) {
  assertVersion(version);
  return [
    { name: `ZTG-Readout-${version}-Setup.exe`, role: 'installer', signed: true },
    { name: `ZTG-Readout-${version}-x64.msi`, role: 'msi', signed: true },
    { name: `ztg-readout-${version}.exe`, role: 'portable-exe', signed: true },
    { name: 'THIRD-PARTY-NOTICES.txt', role: 'notices', signed: false },
    { name: CHECKSUMS, role: 'checksums', signed: false },
  ];
}

export function versionProblem(version) {
  const match = VERSION_PATTERN.exec(version ?? '');
  if (!match) return `version "${version}" is not numeric X.Y.Z`;
  const [major, minor, build] = match.slice(1).map(Number);
  // Windows Installer ignores everything beyond these ranges when it compares ProductVersion.
  if (major > 255 || minor > 255 || build > 65535) return `version ${version} exceeds MSI limits (255.255.65535)`;
  return null;
}

function assertVersion(version) {
  const problem = versionProblem(version);
  if (problem) throw new Error(problem);
}

const attribute = (text, pattern) => pattern.exec(text)?.[1];

/** Reads every place a version, product name or upgrade identity is written. */
export async function readSources(root) {
  const read = (path) => readFile(join(root, path), 'utf8');
  const [pkg, cargo, lock, tauri, wxs, nsi, updates] = await Promise.all([
    read('readout/package.json'),
    read('readout/src-tauri/Cargo.toml'),
    read('readout/src-tauri/Cargo.lock'),
    read('readout/src-tauri/tauri.conf.json'),
    read('readout/installer/ztg-readout.wxs'),
    read('readout/installer/ztg-readout.nsi'),
    read('docs/updates.md'),
  ]);
  const product = attribute(wxs, /<Product\b([\s\S]*?)>/);
  return {
    packageVersion: JSON.parse(pkg).version,
    cargoVersion: attribute(cargo, /^version\s*=\s*"([^"]+)"/m),
    lockVersion: attribute(lock, /name = "ztg-readout"\nversion = "([^"]+)"/),
    tauriVersion: JSON.parse(tauri).version,
    wxs: {
      name: attribute(product ?? '', /\bName="([^"]*)"/),
      manufacturer: attribute(product ?? '', /\bManufacturer="([^"]*)"/),
      upgradeCode: attribute(product ?? '', /\bUpgradeCode="([^"]*)"/),
      version: attribute(product ?? '', /\bVersion="([^"]*)"/),
      hasMajorUpgrade: /<MajorUpgrade\b[^>]*DowngradeErrorMessage=/.test(wxs),
      perMachine: /InstallScope="perMachine"/.test(wxs),
    },
    nsi: {
      product: attribute(nsi, /^!define PRODUCT "([^"]*)"/m),
      publisher: attribute(nsi, /^!define PUBLISHER "([^"]*)"/m),
    },
    updatesDocNamesSetup: updates.includes('`ZTG-Readout-<X.Y.Z>-Setup.exe`'),
  };
}

/** Returns a list of problems; an empty list means the metadata agrees. */
export function checkMetadata(sources, { tag } = {}) {
  const problems = [];
  const version = sources.packageVersion;
  const bad = versionProblem(version);
  if (bad) problems.push(`readout/package.json: ${bad}`);
  if (sources.cargoVersion !== version) problems.push(`Cargo.toml version ${sources.cargoVersion} differs from package.json ${version}`);
  if (sources.lockVersion !== version) problems.push(`Cargo.lock ztg-readout ${sources.lockVersion} differs from package.json ${version}`);
  if (sources.tauriVersion !== '../package.json' && sources.tauriVersion !== version)
    problems.push(`tauri.conf.json version ${sources.tauriVersion} must be "../package.json" or ${version}`);
  const { wxs } = sources;
  // The package version reaches the MSI only through wixl -D Version=..., so a literal here
  // would freeze every build at one ProductVersion.
  if (wxs.version !== '$(var.Version)') problems.push(`ztg-readout.wxs Product Version is "${wxs.version}", expected "$(var.Version)"`);
  if (wxs.upgradeCode !== UPGRADE_CODE) problems.push(`ztg-readout.wxs UpgradeCode is "${wxs.upgradeCode}"; it is permanent and must stay ${UPGRADE_CODE}`);
  if (wxs.name !== PRODUCT) problems.push(`ztg-readout.wxs Product Name is "${wxs.name}", expected "${PRODUCT}"`);
  if (wxs.manufacturer !== MANUFACTURER) problems.push(`ztg-readout.wxs Manufacturer is "${wxs.manufacturer}", expected "${MANUFACTURER}"`);
  if (!wxs.hasMajorUpgrade) problems.push('ztg-readout.wxs needs MajorUpgrade with a DowngradeErrorMessage');
  if (!wxs.perMachine) problems.push('ztg-readout.wxs must stay a per-machine package');
  if (sources.nsi.product !== PRODUCT) problems.push(`ztg-readout.nsi PRODUCT is "${sources.nsi.product}", expected "${PRODUCT}"`);
  if (sources.nsi.publisher !== MANUFACTURER) problems.push(`ztg-readout.nsi PUBLISHER is "${sources.nsi.publisher}", expected "${MANUFACTURER}"`);
  if (!sources.updatesDocNamesSetup) problems.push('docs/updates.md no longer documents ZTG-Readout-<X.Y.Z>-Setup.exe as the update installer');
  if (tag !== undefined && tag !== `${TAG_PREFIX}${version}`) problems.push(`tag ${tag} does not match ${TAG_PREFIX}${version}`);
  return problems;
}

const sha256 = async (path) => createHash('sha256').update(await readFile(path)).digest('hex');

/** Copies the built files to their release names and writes the checksum file. */
export async function assemble({ version, exe, setup, msi, notices, out }) {
  assertVersion(version);
  const names = Object.fromEntries(expectedAssets(version).map((a) => [a.role, a.name]));
  await mkdir(out, { recursive: true });
  const present = await readdir(out);
  if (present.length) throw new Error(`${out} is not empty`);
  await copyFile(setup, join(out, names.installer));
  await copyFile(msi, join(out, names.msi));
  await copyFile(exe, join(out, names['portable-exe']));
  await copyFile(notices, join(out, names.notices));
  const lines = [];
  for (const name of (await readdir(out)).sort()) lines.push(`${await sha256(join(out, name))}  ${name}`);
  await writeFile(join(out, CHECKSUMS), `${lines.join('\n')}\n`);
}

/** Throws unless dir holds exactly the expected assets and SHA256SUMS.txt covers the rest. */
export async function verifyDist(dir, version) {
  const expected = expectedAssets(version).map((a) => a.name).sort();
  const found = (await readdir(dir)).sort();
  const missing = expected.filter((n) => !found.includes(n));
  const extra = found.filter((n) => !expected.includes(n));
  if (missing.length || extra.length) throw new Error(`asset set differs: missing [${missing}] unexpected [${extra}]`);
  const listed = new Map();
  for (const line of (await readFile(join(dir, CHECKSUMS), 'utf8')).split('\n').filter(Boolean)) {
    const m = /^([0-9a-f]{64}) [ *](.+)$/.exec(line);
    if (!m) throw new Error(`malformed ${CHECKSUMS} line: ${line}`);
    if (listed.has(m[2])) throw new Error(`${CHECKSUMS} lists ${m[2]} twice`);
    listed.set(m[2], m[1]);
  }
  const covered = expected.filter((n) => n !== CHECKSUMS);
  const stray = [...listed.keys()].filter((n) => !covered.includes(n));
  if (stray.length) throw new Error(`${CHECKSUMS} lists unexpected files: ${stray}`);
  for (const name of covered) {
    if (!listed.has(name)) throw new Error(`${CHECKSUMS} does not list ${name}`);
    if ((await sha256(join(dir, name))) !== listed.get(name)) throw new Error(`checksum mismatch: ${name}`);
    if ((await readFile(join(dir, name))).length === 0) throw new Error(`${name} is empty`);
  }
}

export async function dryRunManifest(dir, version) {
  await verifyDist(dir, version);
  const assets = [];
  for (const asset of expectedAssets(version)) {
    const path = join(dir, asset.name);
    assets.push({ name: asset.name, role: asset.role, signedInProduction: asset.signed, sha256: await sha256(path), bytes: (await readFile(path)).length });
  }
  return {
    status: 'UNSIGNED DRY RUN: test packages, not for distribution',
    tag: `${TAG_PREFIX}${version}`,
    version,
    upgradeCode: UPGRADE_CODE,
    assets,
  };
}

function flags(args) {
  const out = {};
  for (let i = 0; i < args.length; i += 2) {
    if (!args[i].startsWith('--') || args[i + 1] === undefined) throw new Error(`bad argument: ${args[i]}`);
    out[args[i].slice(2)] = args[i + 1];
  }
  return out;
}

async function main(argv) {
  const [command, ...rest] = argv;
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
  if (command === 'check') {
    const printVersion = rest.includes('--print-version');
    const options = flags(rest.filter((a) => a !== '--print-version'));
    const sources = await readSources(root);
    const problems = checkMetadata(sources, { tag: options.tag });
    if (problems.length) throw new Error(`Readout release metadata is inconsistent:\n- ${problems.join('\n- ')}`);
    console.error(`Readout ${sources.packageVersion}: package, Cargo, lock, MSI, NSIS and update contract agree; UpgradeCode ${UPGRADE_CODE}.`);
    if (printVersion) console.log(sources.packageVersion);
  } else if (command === 'manifest' && rest[0]) {
    for (const a of expectedAssets(rest[0])) console.log(a.name);
  } else if (command === 'assemble') {
    const o = flags(rest);
    await assemble({ version: o.version, exe: o.exe, setup: o.setup, msi: o.msi, notices: o.notices, out: o.out });
    await verifyDist(o.out, o.version);
    console.error(`Assembled ${o.out}`);
  } else if (command === 'verify-dist' && rest.length === 2) {
    await verifyDist(rest[0], rest[1]);
    console.error(`${rest[0]} holds exactly the expected Readout ${rest[1]} assets and their checksums match.`);
  } else if (command === 'dry-run-manifest' && rest.length === 2) {
    console.log(JSON.stringify(await dryRunManifest(rest[0], rest[1]), null, 2));
  } else {
    throw Object.assign(new Error('usage: readout-release.mjs check | manifest | assemble | verify-dist | dry-run-manifest'), { usage: true });
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = error.usage ? 2 : 1;
  });
}
