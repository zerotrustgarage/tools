// WCAG 2.1 contrast math, shared by the browser UI suite (run.mjs) and the stylesheet test
// (test/contrast.test.ts). Colours are [r, g, b, a] with r, g, b in 0..255 and a in 0..1.

/** Parses #rgb, #rrggbb, rgb(), rgba() (comma or space syntax) and color(srgb ...). */
export function parseColor(text) {
  const s = String(text).trim().toLowerCase();
  let m = /^#([0-9a-f]{3})$/.exec(s);
  if (m) return [...m[1]].map((c) => parseInt(c + c, 16)).concat(1);
  m = /^#([0-9a-f]{6})([0-9a-f]{2})?$/.exec(s);
  if (m) return [0, 2, 4].map((i) => parseInt(m[1].slice(i, i + 2), 16)).concat(m[2] ? parseInt(m[2], 16) / 255 : 1);
  m = /^rgba?\(([^)]*)\)$/.exec(s);
  if (m) {
    const parts = m[1].split(/[\s,/]+/).filter(Boolean).map((p) => (p.endsWith('%') ? parseFloat(p) / 100 : parseFloat(p)));
    const alpha = parts[3] ?? 1;
    return [parts[0], parts[1], parts[2], alpha];
  }
  m = /^color\(srgb ([^)]*)\)$/.exec(s);
  if (m) {
    const parts = m[1].split(/[\s/]+/).filter(Boolean).map(parseFloat);
    return [parts[0] * 255, parts[1] * 255, parts[2] * 255, parts[3] ?? 1];
  }
  throw new Error(`cannot parse colour: ${text}`);
}

/** The colour fg shows as when painted over the opaque colour bg. */
export function over(fg, bg) {
  const a = fg[3];
  return [0, 1, 2].map((i) => fg[i] * a + bg[i] * (1 - a)).concat(1);
}

export function luminance([r, g, b]) {
  const lin = (v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

/** WCAG contrast ratio between two opaque colours. */
export function ratio(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** Large text (WCAG 1.4.3): at least 24px, or 18.66px and bold. */
export const isLarge = (px, weight) => px >= 24 || (px >= 18.66 && weight >= 700);
