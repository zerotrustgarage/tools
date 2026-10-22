import { el } from './dom';

// A draggable divider between two panes. It reports a new size while dragging; the owner
// clamps it, applies it and stores it. Works with mouse, pen and touch (pointer events) and
// with the keyboard: arrow keys move it, Shift moves it further, Home resets it, and so does a
// double-click.

export interface SplitterOptions {
  label: string;
  /** Size of the pane the divider controls, in pixels, when dragging starts. */
  current: () => number;
  /** Applies a size in pixels; returns the size actually applied after clamping. */
  apply: (pixels: number) => number;
  /** Called once a drag or key press is finished, to persist the size. */
  commit: () => void;
  reset: () => void;
  /** +1 when dragging right grows the pane, -1 when it shrinks it. */
  direction?: 1 | -1;
}

const KEY_STEP = 16;
const KEY_STEP_LARGE = 64;

export function splitter(options: SplitterOptions): HTMLElement {
  const direction = options.direction ?? 1;
  const handle = el('div', {
    class: 'splitter',
    role: 'separator',
    'aria-orientation': 'vertical',
    'aria-label': options.label,
    title: `${options.label}: drag to resize, double-click to reset`,
    tabindex: '0',
  });
  const sync = (size: number) => handle.setAttribute('aria-valuenow', String(Math.round(size)));

  handle.addEventListener('pointerdown', (event) => {
    if (event.button !== 0) return;
    event.preventDefault();
    const startX = event.clientX;
    const startSize = options.current();
    handle.setPointerCapture(event.pointerId);
    document.body.classList.add('resizing');
    const move = (e: PointerEvent) => sync(options.apply(startSize + direction * (e.clientX - startX)));
    const end = () => {
      handle.removeEventListener('pointermove', move);
      handle.removeEventListener('pointerup', end);
      handle.removeEventListener('pointercancel', end);
      document.body.classList.remove('resizing');
      options.commit();
    };
    handle.addEventListener('pointermove', move);
    handle.addEventListener('pointerup', end);
    handle.addEventListener('pointercancel', end);
  });

  handle.addEventListener('dblclick', () => {
    options.reset();
    options.commit();
  });

  handle.addEventListener('keydown', (event) => {
    const step = event.shiftKey ? KEY_STEP_LARGE : KEY_STEP;
    let delta = 0;
    if (event.key === 'ArrowLeft') delta = -step;
    else if (event.key === 'ArrowRight') delta = step;
    else if (event.key === 'Home') {
      event.preventDefault();
      options.reset();
      options.commit();
      return;
    } else return;
    event.preventDefault();
    sync(options.apply(options.current() + direction * delta));
    options.commit();
  });

  return handle;
}
