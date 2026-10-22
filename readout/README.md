# ZTG Readout

A Markdown editor and viewer built for documents that AI tools and engineers produce: tables,
colour, alerts, task lists, math, diagrams and code, shown the way GitHub shows them, and saved
without the editor ever rewriting a byte you did not touch.

It runs as a Windows desktop app (Tauri) and as a browser page. Both builds come from the same
source.

## What it renders

| Feature | Notes |
| --- | --- |
| CommonMark and GitHub Flavored Markdown | Tables with alignment, task lists, strikethrough, autolinks, footnotes |
| GitHub alerts | `> [!NOTE]`, `[!TIP]`, `[!IMPORTANT]`, `[!WARNING]`, `[!CAUTION]` |
| Inline HTML | Colour spans, highlights, `<details>`, `<kbd>`, `<sub>`, `<sup>`, `<picture>` (falls back to its `<img>`) |
| Math | `$inline$`, `$$display$$` and ` ```math ` fences through KaTeX; `$5 to $10` stays money |
| Diagrams | ` ```mermaid ` fences, loaded only when a document has one |
| Code | Syntax highlighting for the common languages plus PowerShell, batch, Dockerfile, nginx and Apache |
| Front matter | YAML front matter shown as a table, as GitHub does |
| Wiki links | `[[Page]]`, `[[Page\|label]]`, `[[Page#Heading]]`, `![[image.png]]` |

## Folders and repositories

Open a folder (Ctrl+Shift+O, a drop onto the window, or "Open in ZTG Readout" on a folder in
Explorer) to browse every Markdown file in it. The folder's README opens first. Links between
files work the way they do on GitHub, including `docs/setup.md#section`, `../README.md`,
repository-root links such as `/docs/x.md`, and links to a folder, which open its README.
Relative images load from the folder. The listing skips `.git`, `node_modules`, build output and
other noise, keeps `.github`, and never follows symbolic links or junctions. Back (Alt+Left)
returns along the links you followed.

## Working with it

- The app reopens what you had open last: the folder and the document you were reading in it,
  or a single file. It remembers each time you open, follow a link or save, so the session
  survives a crash too; closing the folder or starting a new document outside one forgets it.
  A file or folder given on the command line (or through "Open with") wins over the session.
  On the desktop the session is kept by the Rust side in `%LOCALAPPDATA%\sh.ztg.readout` and
  holds only paths you had already opened; anything that has since been moved or deleted is
  skipped. In the browser, Chromium keeps the folder or file handle, and unless you allowed
  access on every visit the page asks with a Reopen button, since access needs a click.
- Drag a Markdown file or a folder onto the window to open it. Anything else is refused with a
  note saying why, and in the browser a dropped file is never pasted into the open document.
- Drag the dividers between the file list, the editor and the preview to set their widths. The
  arrow keys move a focused divider (Shift for larger steps), Home or a double-click resets it,
  and the widths are remembered. The editor's share is kept as a proportion, so it scales with
  the window.
- The reading-width button switches the preview between a comfortable column and the full pane.
- On the desktop the app draws its own title bar in the current theme (the native frame is off):
  drag it to move the window, double-click it to maximize. Windows 11's snap layout flyout,
  which appears when hovering a native maximize button, is not available on it; Win+Z and
  Win+arrow keys still snap the window.

## Fidelity

The editor's text is the file. There is no document model in between, so opening and saving a
file without edits reproduces it byte for byte, and an edit changes only what you typed:

- The UTF-8 byte order mark is kept when present and never added.
- CRLF files stay CRLF, including text pasted from elsewhere.
- A missing final newline stays missing.
- Files with mixed line endings or invalid UTF-8 open read-only, with a note saying why, so
  saving cannot alter them.
- Formatting buttons insert or remove only their own markers.

`test/roundtrip.test.ts` holds this to a corpus of real-world files.

## Security model

A Markdown file is untrusted input. Earlier editors have turned crafted files into code
execution (MarkText: CVE-2021-29996, CVE-2022-24123, CVE-2022-25069, CVE-2023-2318). Here the
defences are layered:

1. Rendered HTML passes DOMPurify, then hooks that keep only presentational inline styles,
   remove forms, and drop any style value that could load a resource.
2. Remote images are never fetched; a placeholder shows the address. Opening a file cannot
   notify a tracking server.
3. A strict Content Security Policy forbids inline and remote script and remote loads.
4. On the desktop, the WebView has no file system, dialog or shell permission. Rust shows the
   native dialogs and keeps an allow-list of what the user chose. A link to a file outside the
   open folder asks first, in a native dialog page script cannot answer. Paths written in
   documents may not name a drive or UNC host, which closes the Windows NTLM hash leak through
   `\\host\share` image paths. Only Markdown and text files can be written. The stored session
   can only hold paths already on that allow-list, and is checked again before the next start
   grants them.
5. Mermaid runs with `securityLevel: 'strict'` and its SVG is sanitized again; KaTeX runs with
   `trust: false`.

`test/sanitize.test.ts` replays the CVE payload shapes.

## Develop

```bash
npm ci                       # from the repository root
npm run dev                  # browser version at http://localhost:1420
npx tauri dev                # desktop app (needs Rust and, on Linux, WebKitGTK)
npm run check                # typecheck and tests
```

## Windows build and test

```bash
scripts/ci/build-windows.sh readout                      # cross-compile on Linux
scripts/ci/test-installer-wine.sh <ZTG-Readout-…-Setup.exe>   # installer checks under Wine
readout/installer/build-msi.sh <exe> <notices> <version> <out.msi>   # MSI (needs wixl, msitools)
```

The MSI (`installer/ztg-readout.wxs`) installs the same files and registry entries as the NSIS
installer, per machine, for Intune, Group Policy and RMM: `msiexec /i ZTG-Readout-<version>.msi
/qn` and `msiexec /x … /qn`. Its UpgradeCode is permanent, so a newer MSI upgrades the installed
version in place. It refuses to install without the WebView2 Runtime (exit code 1603). Releases
do not include the MSI yet.

The UI itself needs a real Windows machine (WebView2 does not run under Wine).

## Release

Releases are built and signed only by `.github/workflows/release-readout.yml`, run by hand
from `main`. See [docs/signing.md](../docs/signing.md).

Installer switches for RMM and Intune: `/S` installs silently per machine; uninstall with
`"%ProgramFiles%\ZTG Readout\uninstall.exe" /S`. Exit code 4 means the Microsoft Edge WebView2
Runtime is missing (Windows 11 and current Windows 10 include it). Close the app before an
upgrade; a running copy holds its executable open.
