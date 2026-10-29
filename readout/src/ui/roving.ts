// The WAI-ARIA toolbar pattern for a row of buttons: one tab stop for the whole bar, Left and
// Right move between the buttons (Home and End jump to the ends), and focus keeps its place when
// the reader tabs away and back. Disabled and hidden buttons are skipped.

export function rovingToolbar(bar: HTMLElement): void {
  const buttons = () => [...bar.querySelectorAll<HTMLButtonElement>('button')].filter((b) => !b.disabled && !b.hidden);
  const setStop = (target: HTMLElement | undefined) => {
    for (const b of bar.querySelectorAll<HTMLElement>('button')) b.tabIndex = b === target ? 0 : -1;
  };
  setStop(buttons()[0]);
  bar.addEventListener('focusin', (event) => {
    if ((event.target as HTMLElement).matches('button')) setStop(event.target as HTMLElement);
  });
  bar.addEventListener('keydown', (event) => {
    if (event.ctrlKey || event.altKey || event.metaKey || event.shiftKey) return;
    const all = buttons();
    const at = all.indexOf(event.target as HTMLButtonElement);
    if (at < 0) return;
    const next = event.key === 'ArrowRight' ? (at + 1) % all.length : event.key === 'ArrowLeft' ? (at - 1 + all.length) % all.length : event.key === 'Home' ? 0 : event.key === 'End' ? all.length - 1 : -1;
    if (next < 0) return;
    event.preventDefault();
    all[next]!.focus();
  });
}
