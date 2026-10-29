// Rich text on the clipboard, shared by the browser and desktop platforms: the desktop build is
// the same WebView page, so the same web APIs apply and no Tauri permission or plugin is needed
// (the WebView still holds no file system, dialog or shell permission).
//
// Two routes, tried in order:
// 1. The async Clipboard API with a ClipboardItem holding text/html and text/plain together.
// 2. A "copy" event handler that fills the clipboard data itself, triggered by execCommand('copy').
//    It is the older route that every WebView and browser that can copy at all still supports,
//    so a WebView that refuses or lacks the first route still works.
// Both need a user gesture, so callers invoke this from a click.

/** Writes html and text as one clipboard entry. Rejects when neither route is available. */
export async function writeRichText(html: string, text: string): Promise<void> {
  if (typeof ClipboardItem !== 'undefined' && navigator.clipboard?.write) {
    try {
      await navigator.clipboard.write([
        new ClipboardItem({
          'text/html': new Blob([html], { type: 'text/html' }),
          'text/plain': new Blob([text], { type: 'text/plain' }),
        }),
      ]);
      return;
    } catch {
      // Refused (no permission, no gesture) or unsupported type: try the copy event.
    }
  }
  if (typeof document.execCommand !== 'function') throw new Error('The clipboard is not available here.');
  let filled = false;
  const onCopy = (event: ClipboardEvent) => {
    if (!event.clipboardData) return;
    event.clipboardData.setData('text/html', html);
    event.clipboardData.setData('text/plain', text);
    event.preventDefault();
    filled = true;
  };
  document.addEventListener('copy', onCopy, { capture: true });
  try {
    const ok = document.execCommand('copy');
    if (!ok || !filled) throw new Error('The clipboard refused the copy.');
  } finally {
    document.removeEventListener('copy', onCopy, { capture: true });
  }
}
