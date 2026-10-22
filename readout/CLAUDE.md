# CLAUDE.md (readout)

ZTG Readout, a Markdown editor and viewer: one TypeScript frontend (`src/`) for the browser and
the Tauri desktop shell (`src-tauri/`). Repository-wide rules are in the root `CLAUDE.md`.

These properties are requirements, each held by tests:

1. Byte fidelity. The editor's text is the file; there is no document model in between. An
   unedited save reproduces the original bytes (BOM, CRLF, no final newline). Formats that
   cannot be saved safely (mixed endings, invalid UTF-8) open read-only. `test/roundtrip.test.ts`.
2. Documents are untrusted input. Rendered HTML goes through `src/core/sanitize.ts`; remote
   content is never fetched; the CSP forbids inline and remote script. `test/sanitize.test.ts`.
3. The WebView holds no file system, dialog or shell permission. Every file operation is a Rust
   command in `src-tauri/src/lib.rs` limited to paths the user chose, and paths written in
   documents never name a drive, root or UNC host. A new command must be added to `build.rs`
   and granted in `capabilities/default.json` deliberately.
4. Formatting commands change only the characters they add or remove.

The Windows executable is built only through the Tauri CLI (`scripts/ci/build-windows.sh`);
plain `cargo build` makes a development build that loads its UI from localhost.

## Checks

```
npm run check --workspace @ztg-tools/readout                  # typecheck and tests
cd src-tauri && cargo fmt --check && cargo clippy --all-targets --locked -- -D warnings && cargo test --locked
scripts/ci/build-windows.sh readout                           # from the repository root
scripts/ci/test-installer-wine.sh <Setup.exe>                 # installer under Wine
installer/build-msi.sh <exe> <notices> <version> <out.msi>    # MSI; never change its UpgradeCode
```
