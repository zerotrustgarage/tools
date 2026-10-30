// The print options (RD12): an optional client name for the page header. The same dialog patterns as
// the other modals (RD20): named by its heading, Tab wraps, Escape cancels, focus returns to the
// control that opened it. Text goes in with textContent only.

import { MAX_CLIENT_LENGTH, cleanText } from '../core/print';
import { dialogState, trapTab } from './dialog';
import { el } from './dom';

export interface PrintDialogOptions {
  /** The name remembered for this document, or ''. */
  client: string;
  /** False for a document that has no saved place, which cannot be remembered. */
  remembered: boolean;
  /** Called when the reader clears a remembered name, so it is forgotten at once. */
  onClear: () => void;
}

/** Resolves with the client name to print ('' for none), or null when the reader cancelled. */
export function printOptionsDialog(options: PrintDialogOptions): Promise<string | null> {
  return new Promise((resolve) => {
    const dialog = el('dialog', { class: 'modal print-options', 'aria-labelledby': 'print-options-title', 'aria-describedby': 'print-options-body' }) as HTMLDialogElement;
    const input = el('input', { type: 'text', id: 'print-options-client', class: 'field-input', maxlength: String(MAX_CLIENT_LENGTH), autocomplete: 'off', spellcheck: 'false' });
    input.value = options.client;
    const status = el('p', { class: 'field-status', role: 'status' });
    const clear = el('button', { type: 'button', class: 'text-button' }, 'Clear');
    const cancel = el('button', { type: 'button', class: 'text-button' }, 'Cancel');
    const print = el('button', { type: 'button', class: 'primary' }, 'Print');
    let answer: string | null = null;

    print.addEventListener('click', () => {
      answer = cleanText(input.value, MAX_CLIENT_LENGTH);
      dialog.close();
    });
    cancel.addEventListener('click', () => dialog.close());
    clear.addEventListener('click', () => {
      input.value = '';
      options.onClear();
      status.textContent = options.remembered ? 'Client name cleared and forgotten.' : 'Client name cleared.';
      input.focus();
    });
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') {
        event.preventDefault();
        print.click();
      }
    });
    dialog.addEventListener('cancel', () => {
      answer = null;
    });
    dialog.addEventListener('close', () => {
      dialogState.open--;
      dialog.remove();
      resolve(answer);
    });
    dialog.append(
      el('h2', { id: 'print-options-title' }, 'Print options'),
      el(
        'p',
        { id: 'print-options-body' },
        options.remembered
          ? 'Every page gets a header with the document title and a footer with the date and page number. A client name is optional. You type it here; it is never read from the document, and it is remembered for this document on this computer.'
          : 'Every page gets a header with the document title and a footer with the date and page number. A client name is optional. You type it here; it is never read from the document. It is not remembered, because this document is not saved yet.',
      ),
      el('label', { for: 'print-options-client', class: 'field-label' }, 'Client name (optional)'),
      input,
      status,
      el('div', { class: 'modal-actions' }, clear, cancel, print),
    );
    dialogState.open++;
    document.body.append(dialog);
    trapTab(dialog);
    dialog.showModal();
    input.focus();
    input.select();
  });
}
