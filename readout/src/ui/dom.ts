// Small DOM helpers. Text is always set as text, never parsed as HTML.

export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Record<string, string> = {},
  ...children: (Node | string)[]
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  for (const [name, value] of Object.entries(attrs)) node.setAttribute(name, value);
  node.append(...children);
  return node;
}

// 24px stroke icons drawn here so the app ships no icon font.
const ICONS: Record<string, string[]> = {
  new: ['M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8z', 'M14 3v5h5', 'M12 11v6', 'M9 14h6'],
  open: ['M3 7a1 1 0 0 1 1-1h5l2 2h9a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z'],
  save: ['M5 3h11l3 3v14a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1z', 'M8 3v5h7V3', 'M8 21v-7h8v7'],
  'save-as': ['M5 3h11l3 3v6', 'M5 3v17a1 1 0 0 0 1 1h6', 'M8 3v5h7V3', 'M15 21l6-6-2-2-6 6v2z'],
  export: ['M12 3v12', 'M7 8l5-5 5 5', 'M5 15v4a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-4'],
  copy: ['M9 9h11v11H9z', 'M5 15V4h11'],
  print: ['M7 9V3h10v6', 'M6 18H4a1 1 0 0 1-1-1v-6a1 1 0 0 1 1-1h16a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1h-2', 'M7 14h10v7H7z'],
  'theme-auto': ['M12 3a9 9 0 1 0 0 18z', 'M12 3a9 9 0 0 1 0 18'],
  'theme-light': ['M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8z', 'M12 2v2', 'M12 20v2', 'M4.9 4.9l1.4 1.4', 'M17.7 17.7l1.4 1.4', 'M2 12h2', 'M20 12h2', 'M4.9 19.1l1.4-1.4', 'M17.7 6.3l1.4-1.4'],
  'theme-dark': ['M20 14.5A8 8 0 0 1 9.5 4 8 8 0 1 0 20 14.5z'],
  folder: ['M3 7a1 1 0 0 1 1-1h5l2 2h9a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z'],
  'open-folder': ['M3 7a1 1 0 0 1 1-1h5l2 2h8a1 1 0 0 1 1 1v2', 'M3 7v11a1 1 0 0 0 1 1h13l3-7H7l-3 7'],
  file: ['M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8z', 'M14 3v5h5'],
  chevron: ['M9 6l6 6-6 6'],
  close: ['M6 6l12 12', 'M18 6L6 18'],
  back: ['M15 6l-6 6 6 6'],
  sidebar: ['M4 5h16v14H4z', 'M9 5v14'],
  'width-readable': ['M8 6h8', 'M8 10h8', 'M8 14h8', 'M8 18h5', 'M4 4v16', 'M20 4v16'],
  'width-full': ['M3 6h18', 'M3 10h18', 'M3 14h18', 'M3 18h12', 'M7 9l-3 3 3 3', 'M17 9l3 3-3 3'],
};

export function icon(name: string): SVGSVGElement {
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  svg.classList.add('icon');
  for (const d of ICONS[name] ?? []) {
    const path = document.createElementNS(ns, 'path');
    path.setAttribute('d', d);
    svg.append(path);
  }
  return svg;
}
