#!/usr/bin/env node
// Shared source is vendored into independent Go modules and the embedded Scanner frontend.
// No new runtime or build dependency. CI rejects stale copies.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
const root = new URL('../../', import.meta.url);
const paths = {
  'scripts/updates/update.go': ['scanner/internal/update/update.go', 'tftp/internal/update/update.go'],
  'scripts/updates/client.js': ['scanner/app/frontend/updates.js', 'tftp/app/frontend/updates.js'],
  'scripts/updates/client.css': ['scanner/app/frontend/updates.css', 'tftp/app/frontend/updates.css'],
};
for (const [source, targets] of Object.entries(paths)) {
  const data = await readFile(new URL(source, root));
  for (const target of targets) {
    const url = new URL(target, root);
    if (process.argv.includes('--check')) {
      if (!data.equals(await readFile(url))) throw new Error(`${target} is stale; run node scripts/updates/sync.mjs`);
    } else {
      await mkdir(new URL('.', url), { recursive: true });
      await writeFile(url, data);
    }
  }
}
console.log(process.argv.includes('--check') ? 'Update checker copies match shared source.' : 'Update checker copies refreshed.');
