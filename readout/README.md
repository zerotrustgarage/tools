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
| Callouts and admonitions | Obsidian `> [!type] Title` with every type and alias, foldable with `+` or `-`; MkDocs `!!! type "Title"` and `???`/`???+`; Docusaurus `:::type[Title]` to `:::`. Folded callouts print open |
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

### Links to other files

A link to a file that is not Markdown (`[Spec](files/spec.pdf)`) opens it in the program your
system uses for that type, on the desktop only, after one confirmation. The dialog shows the full
path and the file type; Cancel is the default button, so Enter and Escape both decline. The browser
version cannot open local programs and keeps saying that only Markdown files open.

Readout never runs programs. A Markdown file is untrusted input, so a link must not become code
execution by one click. Only these types open: pdf, png, jpg, jpeg, gif, webp, txt, csv, log, docx,
xlsx, pptx, odt, ods, odp, rtf, json, xml, yaml and yml. Programs and scripts are refused with a
message that names the file: exe, com, bat, cmd, ps1, psm1, vbs, vbe, js, jse, wsf, wsh, msi, msp,
scr, pif, lnk, url, hta, cpl, jar, reg, inf, chm, appx, msix, application and gadget, plus dll,
sys, py, sh and other script and library types, and any file with no extension. Other types (HTML,
SVG, archives, macro-enabled Office files) are refused too, because their default program may run
their content. Open those from File Explorer yourself if you trust them.

The path is resolved in Rust, never in the page: it must be relative, exist, be a regular file and
stay inside the open folder (a document opened on its own may link only to files in its own
folder). A drive, a root, a network share (`\\host\share`), a `file:` address and an alternate
data stream (`a.pdf:stream`) are refused. The type is checked on the name as written and on the
real file, so a shortcut or symbolic link cannot rename a program into a document. The file is
handed to the system with no arguments and no shell (`ShellExecuteExW` on Windows through the
opener already bundled for web links).

## Working with it

- The app reopens what you had open last: the folder and the document you were reading in it,
  or a single file. It remembers each time you open, follow a link or save, so the session
  survives a crash too; closing the folder or starting a new document outside one forgets it.
  A file or folder given on the command line (or through "Open with") wins over the session.
  On the desktop the session is kept by the Rust side in `%LOCALAPPDATA%\sh.ztg.readout` and
  holds only paths you had already opened; anything that has since been moved or deleted is
  skipped. In the browser, Chromium keeps the folder or file handle, and unless you allowed
  access on every visit the page asks with a Reopen button, since access needs a click.
- Recent files (the Recent button in the top bar, or Ctrl+Shift+E) lists the last 10 documents you
  opened or saved, newest first, with the folder each is in. The panel is a plain list of
  buttons: Tab and the arrow keys move through it, Enter opens the file, Delete removes the
  focused line, and Escape closes it. Choosing a file opens it the way Open does, so unsaved
  edits are asked about first; a file inside the open folder stays in it with the file list
  beside it, and any other file replaces the folder. A file that no longer exists is reported in
  the note under the top bar and marked Not found in the list, where it can be removed with its
  x button; Clear list empties the whole list. A file opened again moves to the top, and a file
  reached two ways is one line.
  - Desktop: the list is kept by the Rust side next to the session (`recent.json` in
    `%LOCALAPPDATA%\sh.ztg.readout`) and holds only paths you had already opened. Choosing a line
    is a request to Rust, which opens only a path that is on the list, so the list is not a way
    for the page to reach any other file. Lines whose file is gone are marked when the panel
    opens.
  - Browser with the File System Access API (Chromium): the list keeps file handles in
    IndexedDB, beside the stored session. The browser may ask for access again, with the click
    that opens the line. A file with no handle (a file dropped on the page, one chosen through
    the upload fallback, a download-only save) is not kept, and a file the browser reports as
    gone is only found out when you choose it.
  - Browser without that API (Firefox, Safari): there is nothing to reopen a file with, so
    the Recent button and the shortcut are not offered at all.
- On the desktop the window opens where and how large you left it, maximized again if it was.
  Rust saves the window's normal size and position when the window closes and restores them
  before the window is shown, using Tauri's own window API (no plugin). The saved rectangle is
  checked against the monitors connected now: it is moved and shrunk to lie inside the usable
  area (taskbar excluded) of the monitor it overlaps most, and with no overlap (a monitor that
  was unplugged) it opens on the primary monitor, so the window never opens off screen. A
  damaged or missing `window.json` is ignored. A crash or a forced kill loses a change made
  since the last normal close. Opening a second file from Explorer starts a second window; a
  single running instance is not built, because it needs a plugin.
- Changes made outside Readout (a git pull, an AI agent rewriting a file, a sync client) are
  picked up. The open file is checked every 1.5 seconds and the folder tree every 6 seconds,
  and again when the window regains focus; a change is acted on only after it has looked the
  same on two checks in a row, so a pull that touches many files settles into one refresh.
  With no unsaved edits the file reloads by itself (the preview scroll position is kept). With
  unsaved edits you are asked: **Keep my version**, **Load disk version** (discards your edits)
  or **Decide later**; Escape counts as Decide later, your edits stay, and a Review button in
  the banner asks again. Neither version is overwritten silently: Save also asks first when the
  file on disk changed since you opened it. A file renamed inside the folder is followed (found
  by identical content); a deleted file stays open in the editor and Save writes it back (in the browser the saved-to handle may no longer exist; the save then fails with an error, and the buffer is kept). Only
  the folder you chose is listed or read. This is polling through the existing file commands,
  not an operating-system watcher: no new dependency or permission was added, so a change can
  take a few seconds to show.
  - Desktop: the open file, a lone file and the opened folder are all watched.
  - Browser with the File System Access API (Chromium): a file or folder opened through the
    picker is watched the same way, but only while the page is visible. A file dropped on the
    page, a folder opened through the upload fallback (Firefox, Safari) and a file saved with
    the download fallback have no handle to read again, so they are not watched.
- Copy as rich text (the copy button in the top bar) puts the rendered preview on the clipboard
  so it pastes into Outlook, Teams or Word with its tables, code blocks, callouts and colours.
  It copies the selection in the preview, or the whole document when nothing is selected, as
  `text/html` and `text/plain` together. The HTML is a sanitized copy of the rendered output with
  its styles inline, in the ZTG palette on a white background whatever theme the preview shows:
  no script, no event handlers, no classes or ids, and no address that could be fetched. A
  remote image is replaced by a note that names it without its address (remote images are never
  fetched), a local image is embedded as a data URL (up to 2 MiB each), math is pasted as its
  TeX source, and a diagram is replaced by a note because diagrams do not paste as rich text.
  Folded callouts and `<details>` blocks are copied open. Callouts and code blocks are built as
  single-cell tables, which is what Outlook and Word keep. The preview is only read.
- The Outline button opens a panel with the document's headings as a tree (ATX and setext
  headings; none are taken from code blocks, comments or front matter). Click a heading, or use
  the arrow keys, Home and End and press Enter, to jump to it: the editor in edit and split view,
  the preview in preview view. Duplicate headings stay separate entries. The list follows your
  edits and marks the heading the cursor is under; Escape closes it. It works the same in the
  browser and on the desktop. On a narrow window the panel covers the document and closes after a
  jump.
- Search folder (Ctrl+Shift+F, or the Search button; needs an open folder) finds text in the
  folder's Markdown files, case-insensitively and literally (no patterns), and lists the matching
  lines grouped by file with their line numbers. Click a result, or arrow to it and press Enter,
  to open that file with the match selected; unsaved edits are asked about first, exactly as when
  opening a file from the file list. The open file is searched as it is in the editor. Only the
  files in the folder's list are read, through the same folder reader as the file list, so
  nothing outside the folder is touched and nothing is fetched. The search is bounded: at most
  2,000 files, 2 MiB per file, 500 matching lines (50 per file) and 10 seconds. Binary files
  (a NUL byte or invalid UTF-8), files over the size limit and unreadable files are skipped and
  counted in the line under the field, which also says when a limit cut the search short. Typing
  a new query, pressing Stop, or opening or closing a folder cancels the running search; a folder
  change also clears the query and results. In the browser build the size limit is checked
  against the file's size before it is read, so an oversized file is never loaded. The desktop
  build cannot ask a file's size first: the limit is checked after the read, and the desktop
  reader itself refuses anything over 64 MiB.
  - Desktop and Chromium browsers (File System Access API): each file is read from disk when
    searched, so results reflect the current files.
  - Browser fallback for Firefox and Safari (folder upload): the search covers the snapshot taken
    when the folder was chosen, as the file list does.
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
- A file that is read-only only because its line endings are mixed offers Convert line endings in
  the note. The dialog asks for LF or CRLF (the ending most of the file's lines use comes first),
  turns every ending into the chosen one in the editor, and makes the document editable and
  unsaved. The file on disk is not touched until you save, and the byte order mark and every
  other character are kept. A file that is not valid UTF-8 is never converted. If the file
  changes on disk while a conversion is unsaved, you are asked what to do, as with any unsaved
  edit.
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
   open folder asks first, in a native dialog page script cannot answer. A link to a document file
   (PDF, spreadsheet) opens it in its default app only after a confirmation, and a link to a
   program or script is refused outright (see Links to other files). Paths written in
   documents may not name a drive or UNC host, which closes the Windows NTLM hash leak through
   `\\host\share` image paths. Only Markdown and text files can be written. The stored session
   and the recent-files list can only hold paths already on that allow-list, and are checked
   again before they grant anything; a recent file is reopened only if it is on the list.
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

## Updates

Use **Check for updates** in the status bar of the desktop or browser app.
Checks contact GitHub only when requested and open official release downloads without installing
anything. Offline errors and unpublished releases are shown explicitly. See
[../docs/updates.md](../docs/updates.md).
