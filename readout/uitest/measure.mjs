// Accessibility measurements for the browser UI suite: contrast of what is on screen (text, icons,
// input boundaries) and the focus indicator of the element that has focus. Everything is read
// from computed styles in the page, so it follows the real CSS variables in both themes.
import { isLarge, over, parseColor, ratio } from './contrast.mjs';

const TEXT_MIN = 4.5;
const LARGE_TEXT_MIN = 3;
const GRAPHIC_MIN = 3;

/** Runs in the page: the visible text, icons and input boundaries with their colours and the stack of backgrounds under them. */
function collect() {
  const visible = (node) => {
    const r = node.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) return false;
    for (let e = node; e; e = e.parentElement) {
      const s = getComputedStyle(e);
      if (s.display === 'none' || s.visibility === 'hidden') return false;
    }
    return true;
  };
  const layersOf = (node) => {
    const layers = [];
    let gradient = false;
    let opacity = 1;
    for (let e = node; e; e = e.parentElement) {
      const s = getComputedStyle(e);
      opacity *= Number(s.opacity);
      if (s.backgroundImage !== 'none') gradient = true;
      layers.push(s.backgroundColor);
      if (/^rgb\(|^color\(srgb [^/]*\)$/.test(s.backgroundColor)) break;
    }
    return { layers: layers.reverse(), gradient, opacity };
  };
  const describe = (node) => {
    const cls = typeof node.className === 'string' && node.className ? `.${node.className.trim().split(/\s+/).slice(0, 2).join('.')}` : '';
    return `${node.tagName.toLowerCase()}${cls}`;
  };
  const out = [];
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let t = walker.nextNode(); t; t = walker.nextNode()) {
    const text = t.nodeValue.trim();
    const node = t.parentElement;
    if (!text || !node || ['SCRIPT', 'STYLE', 'NOSCRIPT'].includes(node.tagName) || !visible(node) || node.closest(':disabled')) continue;
    const s = getComputedStyle(node);
    const { layers, gradient, opacity } = layersOf(node);
    out.push({ kind: 'text', what: `${describe(node)} "${text.slice(0, 28)}"`, fg: s.color, layers, gradient, opacity, px: parseFloat(s.fontSize), weight: Number(s.fontWeight) });
  }
  for (const svg of document.querySelectorAll('svg.icon, svg.caption-glyph')) {
    if (!visible(svg) || svg.closest(':disabled')) continue;
    const s = getComputedStyle(svg);
    const { layers, gradient, opacity } = layersOf(svg.parentElement);
    out.push({ kind: 'graphic', what: `icon in ${describe(svg.parentElement)}`, fg: s.stroke, layers, gradient, opacity, px: 0, weight: 0 });
  }
  for (const input of document.querySelectorAll('input:not([type=checkbox]):not([type=radio]), textarea')) {
    if (!visible(input) || input.closest('.markdown-body')) continue;
    const s = getComputedStyle(input);
    const { layers, gradient, opacity } = layersOf(input.parentElement);
    out.push({ kind: 'boundary', what: `border of ${describe(input)}`, fg: s.borderTopColor, layers, gradient, opacity, px: 0, weight: 0 });
  }
  return out;
}

/** The measurements on the page now, with the ratio and the minimum each one has to meet. */
export async function measureContrast(page) {
  const found = await page.evaluate(collect);
  const results = [];
  for (const f of found) {
    if (f.gradient || f.layers.length === 0) continue;
    // The stack starts at the first opaque layer (or the page, which is white when none is).
    let bg = [255, 255, 255, 1];
    for (const layer of f.layers) bg = over(parseColor(layer), bg);
    const fgColor = parseColor(f.fg);
    fgColor[3] *= f.opacity;
    if (fgColor[3] === 0) continue;
    const fg = over(fgColor, bg);
    const large = f.kind === 'text' && isLarge(f.px, f.weight);
    const min = f.kind === 'text' ? (large ? LARGE_TEXT_MIN : TEXT_MIN) : GRAPHIC_MIN;
    results.push({ kind: f.kind, what: f.what, ratio: ratio(fg, bg), min });
  }
  return results;
}

/** Contrast failures among the measurements on the page now, worst first, one line each. */
export async function contrastFailures(page) {
  const seen = new Set();
  return (await measureContrast(page))
    .filter((r) => r.ratio < r.min)
    .sort((a, b) => a.ratio - b.ratio)
    .map((r) => `${r.ratio.toFixed(2)}:1 < ${r.min}:1  ${r.kind} ${r.what}`)
    .filter((line) => !seen.has(line) && seen.add(line));
}

/**
 * The focus indicator of the focused element: its outline (or a ring drawn as a box shadow)
 * against the background around it. Returns null when focus is not visible.
 */
export async function focusIndicator(page) {
  const found = await page.evaluate(() => {
    const e = document.activeElement;
    if (!e || e === document.body) return null;
    // The indicator may sit on the element, or on a part that stands for it (a tree row, the editor).
    const targets = [e, ...e.querySelectorAll(':scope > .tree-row'), ...(e.matches('.cm-content') ? [e.closest('.cm-editor')] : [])];
    const name = `${e.tagName.toLowerCase()}${e.getAttribute('role') ? `[role=${e.getAttribute('role')}]` : ''}${e.className && typeof e.className === 'string' ? `.${e.className.trim().split(/\s+/)[0]}` : ''} ${(e.getAttribute('aria-label') || e.textContent || '').trim().slice(0, 24)}`;
    for (const t of targets) {
      if (!t) continue;
      const s = getComputedStyle(t);
      // outline-style auto is the browser's own ring: it is not drawn from the palette, so it does not count.
      const outline = s.outlineStyle !== 'none' && s.outlineStyle !== 'auto' && parseFloat(s.outlineWidth) > 0 ? { color: s.outlineColor, width: parseFloat(s.outlineWidth) } : null;
      if (!outline) continue;
      // A ring drawn outside the element (offset 0 or more) lies on what is around it; one drawn inside lies on the element.
      const layers = [];
      for (let p = parseFloat(s.outlineOffset) >= 0 && t.parentElement ? t.parentElement : t; p; p = p.parentElement) {
        const b = getComputedStyle(p).backgroundColor;
        layers.push(b);
        if (/^rgb\(|^color\(srgb [^/]*\)$/.test(b)) break;
      }
      return { name, outline, layers: layers.reverse() };
    }
    return { name, outline: null, layers: [] };
  });
  if (!found) return null;
  if (!found.outline) return { name: found.name, visible: false, ratio: 0, width: 0 };
  let bg = [255, 255, 255, 1];
  for (const layer of found.layers) bg = over(parseColor(layer), bg);
  const ring = over(parseColor(found.outline.color), bg);
  return { name: found.name, visible: true, ratio: ratio(ring, bg), width: found.outline.width };
}
