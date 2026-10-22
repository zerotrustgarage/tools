#!/usr/bin/env node
// Validate committed icon exports without a browser or image library.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { inflateSync } from 'node:zlib';

const root = new URL('../../', import.meta.url);
const read = path => readFileSync(new URL(path, root));
const tokens = JSON.parse(read('design-tokens.json')).tokens;
const colors = new Set([tokens.dim2.toLowerCase(), tokens.acc.toLowerCase()]);
const pngMagic = Buffer.from('89504e470d0a1a0a', 'hex');

function checkPng(data, size, name) {
  assert.ok(data.subarray(0, 8).equals(pngMagic), `${name}: PNG signature`);
  assert.equal(data.readUInt32BE(16), size, `${name}: width`);
  assert.equal(data.readUInt32BE(20), size, `${name}: height`);
  assert.equal(data[24], 8, `${name}: 8-bit channels`);
  assert.equal(data[25], 6, `${name}: RGBA with alpha`);
  assert.equal(data[28], 0, `${name}: noninterlaced`);
  const chunks = [];
  for (let pos = 8; pos < data.length;) {
    const len = data.readUInt32BE(pos);
    assert.ok(pos + len + 12 <= data.length, `${name}: complete PNG chunk`);
    if (data.toString('ascii', pos + 4, pos + 8) === 'IDAT') chunks.push(data.subarray(pos + 8, pos + 8 + len));
    pos += len + 12;
  }
  const scan = inflateSync(Buffer.concat(chunks));
  const stride = size * 4;
  assert.equal(scan.length, size * (stride + 1), `${name}: complete pixels`);
  const pixels = Buffer.alloc(size * stride);
  const paeth = (a, b, c) => {
    const p = a + b - c, da = Math.abs(p - a), db = Math.abs(p - b), dc = Math.abs(p - c);
    return da <= db && da <= dc ? a : db <= dc ? b : c;
  };
  for (let y = 0; y < size; y++) {
    const filter = scan[y * (stride + 1)];
    assert.ok(filter <= 4, `${name}: PNG filter`);
    for (let x = 0; x < stride; x++) {
      const at = y * stride + x;
      const a = x >= 4 ? pixels[at - 4] : 0, b = y ? pixels[at - stride] : 0;
      const c = x >= 4 && y ? pixels[at - stride - 4] : 0;
      const predictor = [0, a, b, Math.floor((a + b) / 2), paeth(a, b, c)][filter];
      pixels[at] = (scan[y * (stride + 1) + x + 1] + predictor) & 255;
    }
  }
  let transparent = 0, opaque = 0, brand = 0;
  for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) {
    const at = (y * size + x) * 4, [r, g, b, a] = pixels.subarray(at, at + 4);
    if (!a) transparent++;
    if (a === 255) {
      opaque++;
      assert.ok(!(r < 30 && g < 40 && b < 50), `${name}: opaque black/navy background pixel`);
    }
    // Chromium's premultiplied-alpha round trip can change an antialiased channel by one.
    if (a >= 128 && r <= 1 && g >= 254 && Math.abs(b - 65) <= 1) brand++;
    if (!x || !y || x === size - 1 || y === size - 1)
      assert.equal(a, 0, `${name}: transparent outside edge`);
  }
  assert.ok(transparent > size * size * 0.45, `${name}: no background tile`);
  assert.ok(opaque > size * size * 0.03 && brand > 0, `${name}: visible glyph and ZTG green`);
}

function checkIco(data, sizes, name) {
  assert.equal(data.readUInt16LE(0), 0, `${name}: reserved`);
  assert.equal(data.readUInt16LE(2), 1, `${name}: icon type`);
  assert.equal(data.readUInt16LE(4), sizes.length, `${name}: frame count`);
  let end = 6 + sizes.length * 16;
  const frames = new Map();
  sizes.forEach((size, i) => {
    const at = 6 + i * 16, len = data.readUInt32LE(at + 8), offset = data.readUInt32LE(at + 12);
    assert.equal(data[at] || 256, size, `${name}: frame width`);
    assert.equal(data[at + 1] || 256, size, `${name}: frame height`);
    assert.equal(data.readUInt16LE(at + 4), 1, `${name}: color planes`);
    assert.equal(data.readUInt16LE(at + 6), 32, `${name}: RGBA depth`);
    assert.equal(offset, end, `${name}: contiguous frames`);
    assert.ok(offset + len <= data.length, `${name}: frame bounds`);
    const png = data.subarray(offset, offset + len);
    checkPng(png, size, `${name}/${size}`);
    frames.set(size, png);
    end += len;
  });
  assert.equal(end, data.length, `${name}: no trailing data`);
  return frames;
}

const distinct = new Set();
for (const tool of ['scanner', 'copy', 'tftp', 'readout']) {
  const dir = `assets/icons/${tool}`;
  for (const source of ['icon.svg', 'small.svg']) {
    const svg = read(`${dir}/${source}`).toString();
    assert.match(svg, /viewBox="0 0 64 64"/, `${tool}: shared grid`);
    for (const match of svg.matchAll(/#[0-9a-f]{6}/gi)) assert.ok(colors.has(match[0].toLowerCase()), `${tool}: canonical colors`);
    assert.ok(!/<(?:image|text|script|foreignObject)\b/.test(svg), `${tool}: self-contained vector glyph`);
  }
  const app = checkIco(read(`${dir}/icon.ico`), [16, 20, 24, 32, 40, 48, 64, 128, 256], `${tool} app`);
  const tray = checkIco(read(`${dir}/tray.ico`), [16, 20, 24, 32, 40, 48], `${tool} tray`);
  for (const [size, png] of tray) assert.ok(png.equals(app.get(size)), `${tool}: shared app/tray small frame`);
  checkPng(read(`${dir}/icon.png`), 512, `${tool} PNG`);
  checkPng(read(`${dir}/tray.png`), 32, `${tool} tray PNG`);
  assert.ok(read(`${dir}/tray.png`).equals(app.get(32)), `${tool}: matching 32px PNG`);
  distinct.add(app.get(16).toString('base64'));
  const copies = tool === 'scanner' ? {
    'scanner/app/winres/icon.png': read(`${dir}/icon.png`),
    'scanner/installer/icon.ico': read(`${dir}/icon.ico`),
    'scanner/app/frontend/favicon.png': app.get(32),
  } : tool === 'readout' ? {
    'readout/src-tauri/icons/icon.png': read(`${dir}/icon.png`),
    'readout/src-tauri/icons/icon.ico': read(`${dir}/icon.ico`),
    'readout/src-tauri/icons/32x32.png': app.get(32),
    'readout/src-tauri/icons/64x64.png': app.get(64),
    'readout/src-tauri/icons/128x128.png': app.get(128),
    'readout/src-tauri/icons/128x128@2x.png': app.get(256),
    'readout/public/favicon.png': app.get(32),
  } : {};
  for (const [path, expected] of Object.entries(copies)) assert.ok(read(path).equals(expected), `${path}: current icon export`);
}
assert.equal(distinct.size, 4, 'four distinct glyphs at the smallest Windows size');
const winres = JSON.parse(read('scanner/app/winres/winres.json'));
assert.equal(winres.RT_GROUP_ICON.APP['0000'], '../../installer/icon.ico', 'Scanner embeds the same ICO used by its MSI');
console.log('Icons: four distinct ZTG glyphs, canonical colors, transparent PNG/ICO frames and synchronized app/installer exports.');
