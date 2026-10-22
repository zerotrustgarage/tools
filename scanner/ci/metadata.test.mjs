import assert from 'node:assert/strict';
import { test } from 'node:test';
import { inventory } from './metadata.mjs';

const external = { Path: 'example.org/library', Version: 'v1.2.3', Sum: 'h1:source-checksum' };
const local = { Path: 'github.com/zerotrustgarage/tools/scanner', Version: 'v0.0.0', Replace: { Path: '../', Version: '(devel)' } };
function info(deps) {
  return { GoVersion: 'go1.26.10', Path: 'github.com/zerotrustgarage/tools/scanner/app', Deps: deps,
    Settings: [{ Key: 'GOOS', Value: 'windows' }, { Key: 'GOARCH', Value: 'amd64' }] };
}
test('the SBOM includes both binaries, shared modules once, runtime and the local engine', () => {
  const { bom, modules } = inventory([['ZTGScanner.exe', info([external, local])], ['ztg-scanner.exe', info([external])]], '0.4.0');
  assert.equal(modules.length, 1);
  assert.equal(bom.components.filter(c => c.name === external.Path).length, 1);
  assert.equal(bom.components.find(c => c.name === local.Path).version, '0.4.0');
  const refs = new Set(['ztg-scanner', ...bom.components.map(c => c['bom-ref'])]);
  for (const dep of bom.dependencies) {
    assert.ok(refs.has(dep.ref));
    for (const ref of dep.dependsOn) assert.ok(refs.has(ref));
  }
  assert.equal(bom.dependencies[0].dependsOn.length, 2);
  assert.ok(bom.components.some(c => c.name === 'Go'));
});
test('unreviewed replacements, unpinned sources and conflicting checksums fail closed', () => {
  for (const dep of [{ ...external, Replace: { Path: '/tmp/other' } }, { Path: external.Path }]) {
    assert.throws(() => inventory([['ZTGScanner.exe', info([dep])]], '0.4.0'));
  }
  assert.throws(() => inventory([['ZTGScanner.exe', info([external])], ['ztg-scanner.exe', info([{ ...external, Sum: 'different' }])]], '0.4.0'), /conflicting/);
});
test('a binary for another platform is refused', () => {
  const wrong = info([]);
  wrong.Settings[0].Value = 'linux';
  assert.throws(() => inventory([['ZTGScanner.exe', wrong]], '0.4.0'), /Windows amd64/);
});
