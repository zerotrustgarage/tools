// What every modal dialog shares: how many are open (the file watcher pauses while one is) and
// the Tab wrap that keeps focus inside.

export const dialogState = { open: 0 };

/**
 * A modal dialog makes the page behind it inert, but a browser lets Tab run on into its own
 * toolbar. Tab on the last control and Shift+Tab on the first wrap inside the dialog, so focus
 * stays put in every shell. When the dialog closes the browser returns focus to the control
 * that opened it.
 */
export function trapTab(dialog: HTMLDialogElement): void {
  dialog.addEventListener('keydown', (event) => {
    if (event.key !== 'Tab') return;
    const controls = [...dialog.querySelectorAll<HTMLElement>('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])')].filter((c) => !(c as HTMLButtonElement).disabled);
    const first = controls[0];
    const last = controls[controls.length - 1];
    if (!first || !last) return;
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  });
}
