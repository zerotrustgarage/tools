import type { WindowControls } from '../platform/types';
import { el } from './dom';

// The desktop window draws its own title bar (the native frame is off, tauri.conf.json), so the
// whole window follows the app's theme. The bar is a drag region: dragging it moves the window
// and double-clicking it maximizes, as with a native title bar. Window buttons follow the
// Windows 11 caption layout: 46 by 32 pixels, close turns red on hover.

const NS = 'http://www.w3.org/2000/svg';

function glyph(paths: string[]): SVGSVGElement {
  const svg = document.createElementNS(NS, 'svg');
  svg.setAttribute('viewBox', '0 0 10 10');
  svg.setAttribute('aria-hidden', 'true');
  svg.classList.add('caption-glyph');
  for (const d of paths) {
    const path = document.createElementNS(NS, 'path');
    path.setAttribute('d', d);
    svg.append(path);
  }
  return svg;
}

const GLYPHS = {
  minimize: ['M0 5.5h10'],
  maximize: ['M.5 .5h9v9h-9z'],
  restore: ['M2.5 2.5h7v7h-7z', 'M2.5 .5h7v7'],
  close: ['M.5 .5l9 9', 'M9.5 .5l-9 9'],
};

export function titleBar(controls: WindowControls, appName: string): HTMLElement {
  const caption = (name: keyof typeof GLYPHS, label: string, action: () => void) => {
    const b = el('button', { type: 'button', class: `caption caption-${name}`, title: label, 'aria-label': label });
    b.append(glyph(GLYPHS[name]));
    b.addEventListener('click', action);
    return b;
  };
  const maximize = caption('maximize', 'Maximize', () => void controls.toggleMaximize());
  const showState = (maximized: boolean) => {
    const label = maximized ? 'Restore' : 'Maximize';
    maximize.replaceChildren(glyph(GLYPHS[maximized ? 'restore' : 'maximize']));
    maximize.title = label;
    maximize.setAttribute('aria-label', label);
  };
  controls.onMaximizedChange(showState);

  const bar = el(
    'div',
    { class: 'titlebar', 'data-tauri-drag-region': '' },
    el('span', { class: 'titlebar-mark', 'aria-hidden': 'true' }, 'ZTG'),
    el('span', { class: 'titlebar-name' }, appName),
    el('span', { class: 'titlebar-fill' }),
    el(
      'div',
      { class: 'captions' },
      caption('minimize', 'Minimize', () => void controls.minimize()),
      maximize,
      caption('close', 'Close', () => void controls.close()),
    ),
  );
  // Tauri starts a drag only when the pressed element itself carries the attribute; the text
  // spans ignore the pointer (CSS) so presses fall through to the bar.
  return bar;
}
