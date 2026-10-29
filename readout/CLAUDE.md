# CLAUDE.md (readout)

ZTG Readout, a Markdown editor and viewer: one TypeScript frontend (`src/`) for the browser and
the Tauri desktop shell (`src-tauri/`). Repository-wide rules are in the root `CLAUDE.md`.

These properties are requirements, each held by tests:

1. Byte fidelity. The editor's text is the file; there is no document model in between. An
   unedited save reproduces the original bytes (BOM, CRLF, no final newline). Formats that
   cannot be saved safely (mixed endings, invalid UTF-8) open read-only. `test/roundtrip.test.ts`.
   Mixed endings can be converted, only by the reader's explicit choice: the text is normalized in
   memory, the document is unsaved, and nothing is written until Save (RD15, `convertDocument` in
   `src/core/document.ts`, `test/line-endings.test.ts`). Invalid UTF-8 is never converted.
2. Documents are untrusted input. Rendered HTML goes through `src/core/sanitize.ts`; remote
   content is never fetched; the CSP forbids inline and remote script. `test/sanitize.test.ts`.
   Copy as rich text (RD11) obeys the same rule: `src/core/richtext.ts` sanitizes the copy before and
   after rebuilding it, parses only in an inert document, fetches nothing, and takes its colours from
   `design-tokens.json`. `test/richtext.test.ts`.
3. The WebView holds no file system, dialog or shell permission. Every file operation is a Rust
   command in `src-tauri/src/lib.rs` limited to paths the user chose, and paths written in
   documents never name a drive, root or UNC host. A new command must be added to `build.rs`
   and granted in `capabilities/default.json` deliberately.
4. Formatting commands change only the characters they add or remove.
5. The recent-files list is Rust's on the desktop (`src-tauri/src/recent.rs`): only a granted
   document is added, and `open_recent` grants a path only when it is on the list. The window's
   remembered size and position (`src-tauri/src/window_state.rs`) is Rust's too, restored onto a
   monitor that exists now; neither gives the WebView any permission, and neither adds a plugin.

The Windows executable is built only through the Tauri CLI (`scripts/ci/build-windows.sh`);
plain `cargo build` makes a development build that loads its UI from localhost.

## Checks

```
npm run check --workspace @ztg-tools/readout                  # typecheck and tests
npm run build --workspace @ztg-tools/readout && npm run uitest --workspace @ztg-tools/readout   # browser UI suite (needs Playwright and Chromium, see uitest/run.mjs)
cd src-tauri && cargo fmt --check && cargo clippy --all-targets --locked -- -D warnings && cargo test --locked
scripts/ci/build-windows.sh readout                           # from the repository root
scripts/ci/test-installer-wine.sh <Setup.exe>                 # installer under Wine
installer/build-msi.sh <exe> <notices> <version> <out.msi>    # MSI; never change its UpgradeCode
```
