// Browser stand-ins, injected by run.mjs before the page loads. Nothing here touches the app: the
// build under test is the unmodified browser bundle (web platform), so these replace only what a
// headless Chromium cannot show by itself.
//
// - File System Access API: showOpenFilePicker, showSaveFilePicker and showDirectoryPicker return
//   handles from the browser's own origin private file system (OPFS). Those are real
//   FileSystemFileHandle and FileSystemDirectoryHandle objects (getFile, createWritable, values,
//   resolve, isSameEntry, structured clone into IndexedDB), so the recent files list and the
//   save path run for real. Only the pickers, which need a person, are queued by the tests.
// - IndexedDB: the app stores the session and the recent files list there, with file handles in
//   it. Chromium's headless shell crashes the whole browser when a file handle is cloned into
//   IndexedDB, so a memory store with the small surface the app uses stands in. It keeps the
//   live handles themselves, so reopening a recent file still goes through a real handle.
// - Clipboard: navigator.clipboard.write is replaced with a recorder, because headless Chromium
//   grants no clipboard permission. The app still builds a real ClipboardItem.
// - Content Security Policy: every violation is recorded in window.__csp.
(() => {
  window.__csp = [];
  document.addEventListener('securitypolicyviolation', (event) => {
    window.__csp.push(`${event.violatedDirective} blocked ${event.blockedURI || 'inline'} (${event.sourceFile || 'page'}:${event.lineNumber})`);
  });

  // What the clipboard received: { html, text }, or null.
  window.__clipboard = null;
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      async write(items) {
        const item = items[0];
        const read = async (type) => (item.types.includes(type) ? await (await item.getType(type)).text() : null);
        window.__clipboard = { types: [...item.types], html: await read('text/html'), text: await read('text/plain') };
      },
      async writeText(text) {
        window.__clipboard = { types: ['text/plain'], html: null, text };
      },
    },
  });

  // ---- IndexedDB in memory: open, createObjectStore, transaction, get, put, delete, close ---
  const databases = new Map();
  const later = (run) => {
    const request = { result: undefined, error: null, onsuccess: null, onerror: null };
    setTimeout(() => {
      try {
        request.result = run(request);
        request.onsuccess?.({ target: request });
      } catch (error) {
        request.error = error;
        request.onerror?.({ target: request });
      }
    }, 0);
    return request;
  };
  Object.defineProperty(window, 'indexedDB', {
    configurable: true,
    value: {
      open(name) {
        return later((request) => {
          const fresh = !databases.has(name);
          if (fresh) databases.set(name, new Map());
          const stores = databases.get(name);
          request.result = {
            createObjectStore: (store) => stores.set(store, new Map()),
            transaction: (store) => ({
              objectStore: () => {
                const data = stores.get(store);
                return {
                  get: (key) => later(() => data.get(key)),
                  put: (value, key) => later(() => (data.set(key, value), key)),
                  delete: (key) => later(() => void data.delete(key)),
                };
              },
            }),
            close() {},
          };
          if (fresh) request.onupgradeneeded?.({ target: request });
          return request.result;
        });
      },
    },
  });

  // ---- a small file system in OPFS ---------------------------------------------------------
  const rootPromise = navigator.storage.getDirectory();
  const encoder = new TextEncoder();
  const split = (path) => path.split('/').filter(Boolean);

  async function dirAt(parts, create) {
    let dir = await rootPromise;
    for (const part of parts) dir = await dir.getDirectoryHandle(part, { create });
    return dir;
  }
  async function fileAt(path, create) {
    const parts = split(path);
    const dir = await dirAt(parts.slice(0, -1), create);
    return dir.getFileHandle(parts.at(-1), { create });
  }
  async function write(handle, content) {
    const writable = await handle.createWritable();
    await writable.write(typeof content === 'string' ? encoder.encode(content) : new Uint8Array(content));
    await writable.close();
  }

  // The picker answers, queued by a test before it clicks the button that opens a picker.
  const queue = { open: [], save: [], directory: [] };
  const abort = () => new DOMException('The user aborted a request.', 'AbortError');
  window.showOpenFilePicker = async () => {
    const next = queue.open.shift();
    if (!next) throw abort();
    return [next];
  };
  window.showSaveFilePicker = async () => {
    const next = queue.save.shift();
    if (!next) throw abort();
    return next;
  };
  window.showDirectoryPicker = async () => {
    const next = queue.directory.shift();
    if (!next) throw abort();
    return next;
  };

  window.__fs = {
    /** Writes files: { 'docs/a.md': 'text' }. A value may also be an array of byte values. */
    async seed(files) {
      for (const [path, content] of Object.entries(files)) await write(await fileAt(path, true), content);
    },
    /** The next Open file dialog returns this file. */
    async queueOpen(path) {
      queue.open.push(await fileAt(path, false));
    },
    /** The next Save as dialog returns this file (created empty when it does not exist). */
    async queueSave(path) {
      queue.save.push(await fileAt(path, true));
    },
    /** The next Open folder dialog returns this folder. */
    async queueDirectory(path) {
      queue.directory.push(await dirAt(split(path), false));
    },
    /** The bytes on disk, as an array of numbers. */
    async bytes(path) {
      return [...new Uint8Array(await (await (await fileAt(path, false)).getFile()).arrayBuffer())];
    },
    async text(path) {
      return (await (await fileAt(path, false)).getFile()).text();
    },
  };
})();
