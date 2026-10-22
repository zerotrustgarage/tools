#!/usr/bin/env node
// Export original SVG masters to transparent PNGs and Windows multi-resolution ICOs.
// Uses the same optional Playwright installation as scanner/app/uitest; no shipping dependency.
import { createRequire } from 'node:module';
import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const { chromium } = createRequire(import.meta.url)('playwright');
const root = new URL('../../', import.meta.url);
const sizes = [16, 20, 24, 32, 40, 48, 64, 128, 256];
const browser = await chromium.launch(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {});
const save = (path, data) => writeFile(new URL(path, root), data);

function ico(frames) {
  const header = Buffer.alloc(6 + frames.length * 16);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(frames.length, 4);
  let offset = header.length;
  frames.forEach(({ size, png }, i) => {
    const entry = 6 + i * 16;
    header[entry] = header[entry + 1] = size === 256 ? 0 : size;
    header.writeUInt16LE(1, entry + 4);
    header.writeUInt16LE(32, entry + 6);
    header.writeUInt32LE(png.length, entry + 8);
    header.writeUInt32LE(offset, entry + 12);
    offset += png.length;
  });
  return Buffer.concat([header, ...frames.map(f => f.png)]);
}

try {
  for (const name of ['scanner', 'copy', 'tftp', 'readout']) {
    const dir = `assets/icons/${name}`;
    const page = await browser.newPage({ deviceScaleFactor: 1 });
    const frames = new Map();
    for (const size of [...sizes, 512]) {
      const svg = await readFile(new URL(`${dir}/${size <= 32 ? 'small' : 'icon'}.svg`, root), 'utf8');
      await page.setViewportSize({ width: size, height: size });
      await page.setContent(`<style>html,body{margin:0;background:transparent}svg{display:block;width:100vw;height:100vh}</style>${svg}`);
      frames.set(size, { size, png: await page.screenshot({ omitBackground: true }) });
    }
    await save(`${dir}/icon.png`, frames.get(512).png);
    await save(`${dir}/tray.png`, frames.get(32).png);
    await save(`${dir}/icon.ico`, ico(sizes.map(size => frames.get(size))));
    await save(`${dir}/tray.ico`, ico(sizes.filter(size => size <= 48).map(size => frames.get(size))));
    if (name === 'scanner') {
      await save('scanner/app/winres/icon.png', frames.get(512).png);
      await save('scanner/installer/icon.ico', ico(sizes.map(size => frames.get(size))));
      await save('scanner/app/frontend/favicon.png', frames.get(32).png);
    }
    if (name === 'tftp') {
      await save('tftp/app/winres/icon.png', frames.get(512).png);
      await save('tftp/installer/icon.ico', ico(sizes.map(size => frames.get(size))));
      await save('tftp/app/frontend/favicon.png', frames.get(32).png);
    }
    if (name === 'readout') {
      for (const [file, size] of Object.entries({ '32x32': 32, '64x64': 64, '128x128': 128, '128x128@2x': 256, icon: 512 }))
        await save(`readout/src-tauri/icons/${file}.png`, frames.get(size).png);
      await save('readout/src-tauri/icons/icon.ico', ico(sizes.map(size => frames.get(size))));
      await save('readout/public/favicon.png', frames.get(32).png);
    }
    await page.close();
    console.log(`${name}: PNG and ICO assets exported from ${fileURLToPath(new URL(dir, root))}`);
  }
} finally {
  await browser.close();
}
