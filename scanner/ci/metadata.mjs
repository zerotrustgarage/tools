#!/usr/bin/env node
// Inventory the modules actually linked into both Windows binaries, not the full
// development graph. Runs only in the build job, without a signing identity.
//   node scanner/ci/metadata.mjs <payload-dir> <version>
import { execFileSync } from 'node:child_process';
import { readFile, readdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const engine = 'github.com/zerotrustgarage/tools/scanner';
export function inventory(infos, version) {
  const modules = new Map();
  const components = [];
  const dependencies = [];
  for (const [filename, info] of infos) {
    if (!info.GoVersion || !info.Path?.startsWith(engine + '/')) throw new Error(`unexpected binary: ${filename}`);
    const settings = Object.fromEntries((info.Settings ?? []).map(({ Key, Value }) => [Key, Value]));
    if (settings.GOOS !== 'windows' || settings.GOARCH !== 'amd64') throw new Error('release inventory requires Windows amd64 binaries');
    const ref = `binary:${filename}`;
    components.push({ type: 'application', 'bom-ref': ref, name: filename, version });
    const refs = [];
    const goRef = `toolchain:${info.GoVersion}`;
    if (!components.some(c => c['bom-ref'] === goRef)) {
      components.push({ type: 'framework', 'bom-ref': goRef, name: 'Go', version: info.GoVersion });
    }
    refs.push(goRef);
    for (const dep of info.Deps ?? []) {
      const local = dep.Path === engine && ['..', '../'].includes(dep.Replace?.Path) &&
        [undefined, '(devel)'].includes(dep.Replace.Version);
      if (dep.Replace && !local) throw new Error(`unreviewed module replacement: ${dep.Path}`);
      if (!local && (!dep.Version || !dep.Sum)) throw new Error(`module has no pinned source: ${dep.Path}`);
      const depVersion = local ? version : dep.Version;
      const depRef = `module:${dep.Path}@${depVersion}`;
      refs.push(depRef);
      if (local) {
        if (!components.some(c => c['bom-ref'] === depRef)) {
          components.push({ type: 'library', 'bom-ref': depRef, name: dep.Path, version: depVersion });
        }
        continue;
      }
      if (modules.has(depRef) && modules.get(depRef).Sum !== dep.Sum) throw new Error(`conflicting module checksum: ${dep.Path}`);
      if (!modules.has(depRef)) {
        modules.set(depRef, dep);
        components.push({
          type: 'library', 'bom-ref': depRef, name: dep.Path, version: dep.Version,
          purl: `pkg:golang/${dep.Path}@${dep.Version}`,
          properties: [{ name: 'go:module:sum', value: dep.Sum }],
        });
      }
    }
    dependencies.push({ ref, dependsOn: refs.sort() });
  }
  const sort = (a, b) => a < b ? -1 : a > b ? 1 : 0;
  return {
    modules: [...modules.values()].sort((a, b) => sort(a.Path, b.Path)),
    bom: {
      bomFormat: 'CycloneDX', specVersion: '1.6', version: 1,
      metadata: { component: { type: 'application', 'bom-ref': 'ztg-scanner', name: 'ZTG Scanner', version } },
      components: components.sort((a, b) => sort(a['bom-ref'], b['bom-ref'])),
      dependencies: [{ ref: 'ztg-scanner', dependsOn: infos.map(([file]) => `binary:${file}`).sort() }, ...dependencies],
    },
  };
}

export async function generate(payload, version) {
  const go = (...args) => execFileSync('go', args, { encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
  const names = ['ZTGScanner.exe', 'ztg-scanner.exe'];
  const infos = names.map(file => [file, JSON.parse(go('version', '-m', '-json', join(payload, file)))]);
  const { bom, modules } = inventory(infos, version);
  const sections = ['ZTG Scanner: third-party notices\n\nLicense texts for modules linked into the desktop app and CLI.\n'];
  sections.push(`Go runtime and standard library\n\n${await readFile(join(go('env', 'GOROOT').trim(), 'LICENSE'), 'utf8')}`);
  for (const dep of modules) {
    const data = JSON.parse(go('mod', 'download', '-json', `${dep.Path}@${dep.Version}`));
    if (data.Error || !data.Dir || data.Sum !== dep.Sum) throw new Error(`source checksum mismatch: ${dep.Path}`);
    const files = (await readdir(data.Dir)).filter(name => /^(LICENSE|COPYING|NOTICE|PATENTS)([.-].*)?$/i.test(name)).sort();
    if (!files.some(name => /^(LICENSE|COPYING)([.-].*)?$/i.test(name))) throw new Error(`license text missing: ${dep.Path}`);
    for (const name of files) sections.push(`${dep.Path} ${dep.Version}: ${name}\n\n${await readFile(join(data.Dir, name), 'utf8')}`);
  }
  sections.push('IEEE Registration Authority data\n\nThe embedded MAC vendor table is generated from the public MA-L, MA-M, MA-S and IAB listings.\nSource: https://standards.ieee.org/products-programs/regauth/\nReview IEEE terms before redistributing this data outside ZTG.\n');
  await writeFile(join(payload, 'THIRD-PARTY-NOTICES.txt'), sections.join('\n\n' + '='.repeat(72) + '\n\n'));
  await writeFile(join(payload, 'scanner.cdx.json'), JSON.stringify(bom, null, 2) + '\n');
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [payload, version] = process.argv.slice(2);
  if (!payload || !/^\d+\.\d+\.\d+$/.test(version ?? '')) throw new Error('usage: metadata.mjs <payload-dir> <X.Y.Z>');
  await generate(resolve(payload), version);
}
