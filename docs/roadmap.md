# Roadmap: open work and recommended improvements

Status as of October 23, 2026. This file lists what is not finished and what is recommended for
ZTG Readout, ZTG Scanner, ZTG TFTP and the repository as a whole. The recommendations come from the work so
far and from comparing both tools with established products in their categories (sources at the
end). Each tool's own design and checks are in its folder (`readout/`, `scanner/`, `tftp/`); Scanner's
slices are defined in `scanner/docs/design.md` and ZTG TFTP's in `tftp/docs/design.md`.

Priority: **High** is visible in daily use or blocks a release; **Medium** is clearly worth doing;
**Low** is polish or depends on outside changes.

## 1. Owner actions (completed setup)

These actions were completed by the repository owner. New release workflows still need their own successful production run.

| # | Action | Why | Status |
| --- | --- | --- | --- |
| O1 | Set the default branch to `main` (Settings, General, Default branch) | GitHub shows it on the front page, new clones start from it, and Dependabot reads its configuration only from the default branch | **Done** |
| O2 | Create the Entra app registration and federated credential for signing (`docs/signing.md`, section "One-time owner setup") | No signed release can exist without it | **Done** (client secret, `docs/signing.md` option C, instead of the federated credential; role scoped to the profile) |
| O3 | Create the `tools-signing` environment (main only, required reviewers) with `AZURE_SIGNING_TENANT_ID`, `AZURE_SIGNING_CLIENT_ID` and the `AZURE_SIGNING_CLIENT_SECRET` secret | Same | **Done** |
| O4 | Approve accepting Microsoft's license for the CRT and Windows SDK files the cross-compile downloads (`XWIN_ACCEPT_LICENSE`) | It is accepted on the organization's behalf at every Windows build | **Done** |
| O5 | Build provenance attestations | Public repositories get them on every plan, so every release is attested; the `ATTEST_PROVENANCE` switch is gone | **Done** (repository made public) |
| O6 | Enable Dependabot security updates (Settings, Code security) | Version updates are configured; security updates are a separate switch and are not held by the 14-day cooldown | **Done** |
| O7 | Test both apps on real Windows (Windows Sandbox is enough) | Owner checks are recorded complete. CI also validates Readout and Scanner on Windows. New S5 SSH/RDP handoff, unsaved-close dialogs and physical Wake-on-LAN still need focused checks (SC4) | **Done** |
| O8 | Keep the organization's self-hosted runner group (`proxmox-ci`) closed to public repositories (Organization settings, Actions, Runner groups: "Allow public repositories" off) | The repository is public, so anyone can open a pull request whose workflow would run on those runners, inside the organization's network and behind the egress IP that infrasec's deploy tokens trust. CI here runs on GitHub-hosted runners instead | **Done** |
| O9 | Require approval for workflows from outside contributors (Settings, Actions, General, "Fork pull request workflows from outside collaborators": all outside collaborators) | Fork pull requests then run only after a maintainer has read them | **Done** |
| O10 | Enable secret scanning with push protection and private vulnerability reporting (Settings, Code security) | Free on public repositories; push protection stops a credential from being committed in the first place | **Done** |
| O11 | Protect `main`: require pull requests and the CI checks (Settings, Branches or Rules) | Free on public repositories; see R1 | **Done** |

## 2. Repository and CI

| # | Item | Priority | Status |
| --- | --- | --- | --- |
| R1 | Pull requests, branch protection and required CI on `main`; change control supports review of how these tools are shipped | High | **Done** (O11: `main` requires pull requests) |
| R2 | Downloads page on ztg.sh with the signed installers, checksums and the browser version of Readout. This was the original goal and is not built | High | Open |
| R3 | One look: both tools use the ZTG palette from `design-tokens.json`; `scripts/ci/check-palette.mjs` fails CI on drift | Medium | **Done** |
| R4 | Automated dependency updates with the 14-day cooldown (`.github/dependabot.yml`: npm, Cargo, Go modules, Actions) | Medium | **Done** (default branch is `main`) |
| R5 | Retry `apt-get` when a mirror is mid-sync (`scripts/ci/apt-install.sh`) | Medium | **Done** |
| R6 | CI time: cache the Microsoft SDK download (about 2 GB per Windows build) and the Rust build with `actions/cache`. This affects build time only; nothing cached ships, so installer size does not change. Built in `.github/workflows/ci.yml` (the Readout `windows` and `rust` jobs): the cargo-xwin SDK directory, the `cargo-xwin` binary, and `~/.cargo` registry, git and `target/`, with `actions/cache` pinned by SHA. Keys carry the runner OS and architecture, the pinned cargo-xwin version, the Rust channel, the `Cargo.lock` hash and the month. Release workflows never use a cache, so signed artifacts always come from clean builds. On a hit cargo-xwin does not re-hash the SDK (it hashes only when it downloads), so a hit trusts an entry saved by trusted CI. Scanner, TFTP and Copy download no SDK and have no Rust; their Playwright browser download is not cached yet. Proof pending: the first PR run should show a miss and the second a hit, with the same artifacts (see the Build caches step summary) | Medium | Built, pending PR CI proof |
| R7 | Public repository: every workflow runs on GitHub-hosted runners (free for public repositories, isolated from the organization's network); none may use the self-hosted pool (O8) | High | **Done** |
| R8 | Real Windows in CI: the `windows-install` job installs the NSIS installer and the MSI on `windows-2025`, checks files, shortcut and registry, launches Readout (the window title proves the UI loaded) and opens a file, then uninstalls (`scripts/ci/test-installer-windows.ps1`) | High | **Done** |
| R9 | Distinct application and tray icon assets using the ZTG palette and wordmark cursor; transparent backgrounds | Medium | **Built** for Scanner, Copy, TFTP and Readout. Scanner, TFTP and Readout consumers updated; Copy desktop consumers remain in its future slices |
| R10 | Every tool must support Check for updates; shared version/release checks, safe browser handoff and CLI JSON status | High | **Built** for Scanner/Readout/TFTP UI and Scanner/TFTP CLI; shared Copy support ready, application action required when Copy ships. Automatic installation remains future work; `docs/updates.md` |

## 3. ZTG Readout

### Release and validation

| # | Item | Priority |
| --- | --- | --- |
| RD1 | Done by the owner (O7): interactive checks on real Windows by a person (title bar dragging and shadow, drag and drop from Explorer, the folder context menu, both themes). Installing, launching and opening a file run in CI (R8) | High |
| RD2 | **Done**: signed Readout `v0.1.0` published October 22, 2026, with the setup EXE, checksums and notices. It uses the shared Azure Artifact Signing identity | High |
| RD3 | Source ready, not released: `release-readout.yml` now builds the MSI from the signed exe, signs it through `scripts/signing/sign.mjs`, verifies it on Windows and attaches it with checksums and provenance. PR CI dry-runs the asset set unsigned and tests MSI install, repair, upgrade, refused downgrade, launch and uninstall (`docs/release-readiness-readout.md`). No signed Readout MSI exists until the owner runs the protected release; Intune and Group Policy deployment remain unverified | High |
| RD4 | THIRD-PARTY-NOTICES.txt now carries a license text for every package: six Rust crates publish no license file, so `scripts/notices.mjs` uses the standard SPDX text of their license (`scripts/licenses/`), and a package with neither fails the build instead of warning | **Done** |
| RD5 | **Done**: the browser-driven checks are the `readout/uitest/` suite (`npm run uitest --workspace @ztg-tools/readout` after a build), run by the `web-ui` job in `ci.yml` in headless Chromium against the built bundle, in both themes. It covers open and render, save round trip through a file handle, drag and drop, both dividers, folder mode and search, callouts, outline, mixed line endings, Copy as rich text, recent files, a CSP violation listener and the desktop title bar component (through a harness page). The real Windows title bar and window behavior are still checked only by the Windows jobs and the owner (O7) | Medium |

### Improvements

| # | Improvement | Priority | Reference |
| --- | --- | --- | --- |
| RD6 | Obsidian callouts (all types and aliases, custom titles, foldable `+`/`-`, nested), MkDocs `!!!`/`???` admonitions and Docusaurus `:::` blocks render as one callout element (`src/core/plugins/callouts.ts`); folds the reader opens survive live re-renders and print open | **Done** | Obsidian, MkDocs |
| RD7 | Reload when the open file changes on disk (git pull, an AI agent rewriting it), asking first if there are unsaved edits; refresh the folder tree (`readout/src/core/reload.ts`: polling with a two-poll settle, clean reload, ask when dirty, rename and delete handled) | **Done** | VS Code, Typora |
| RD8 | Outline panel: the heading tree, click to jump (`readout/src/core/outline.ts`; keyboard navigation, follows edits) | **Done** | Typora, MarkText |
| RD9 | Search across the open folder, with results that jump to the line (`readout/src/core/search.ts`: bounded by file count, size, results and time; cancellable; skips binary, oversized and unreadable files) | **Done** | Obsidian, VS Code |
| RD10 | **Partly built**: a recent-files list instead of tabs (the last 10 documents, from the Recent button or Ctrl+Shift+E, opened through the normal open path with its unsaved-changes prompt; a vanished file is reported and removable; Clear list), and remembered window size and position on the desktop (restored onto a monitor that exists now, so the window never opens off screen). Reopening the last folder and document at start was already built. The list is Rust's on the desktop and handle-based in Chromium (`readout/src/core/recent.ts`, `readout/src/platform/recent-web.ts`, `readout/src/ui/recent.ts`, `readout/src-tauri/src/recent.rs`, `readout/src-tauri/src/window_state.rs`); Firefox and Safari have no handle to keep, so the feature is hidden there. **Still open**: a single instance (a second file from Explorer opens in the running window), which needs the single-instance plugin, a new crate and so an explicit dependency decision; tabs, if the list proves not enough. Window restore and the recent list on real Windows (multi-monitor, mixed scale factors, a disconnected monitor) still need a manual check | Medium | Typora, MarkText |
| RD11 | **Built**: Copy as rich text, so a rendered section pastes into Outlook, Teams or Word with tables and colours. The preview selection (or the whole document) goes to the clipboard as sanitized `text/html` with inline styles in the ZTG palette, plus `text/plain` (`readout/src/core/richtext.ts`, `readout/src/platform/clipboard.ts`; ClipboardItem with a copy-event fallback, no new permission or dependency). Remote images are never emitted; diagrams and math become notes and TeX. Tests run in jsdom; a real paste into Outlook, Teams and Word, and the Tauri WebView clipboard, still need a manual check on Windows | Medium | Typora |
| RD12 | **Partly built**: the ZTG-branded print template. Print (Ctrl+P) asks for an optional client name (typed by the reader, never read from the document, remembered per document path in the app's storage, with a Clear action; a labelled dialog with a focus trap, Escape to cancel), then prints every page with a header (ZTG mark, document title, client name) and footer (print date, "Page N of M") in CSS `@page` margin boxes, black on white in both themes, with table rows, code blocks, callouts and images kept whole, repeated table headers, wrapped code lines, headings kept with their text and folded callouts open (`readout/src/core/print.ts`, `readout/src/ui/print-dialog.ts`, `readout/src/styles/print.css`; held by `readout/test/print.test.ts` and the `print:` scenarios in `readout/uitest/run.mjs`, which print to PDF in Chromium and read the text of every page). **Still open**: DOCX export and PDF with bookmarks without the print dialog. Both need a library or a plugin (a DOCX writer; a PDF engine that can write an outline, since the WebView's print path cannot) and so a new dependency, which needs an owner decision. A run on real Windows (WebView2 print preview, a physical or PDF printer) is also still to do | Medium | Typora (Pandoc) |
| RD13 | **Built**: Paste or drop an image into a document: it is saved in `images/` beside the file and `![](images/<name>)` is inserted at the cursor as one undo step. PNG, JPEG, GIF and WebP only (magic bytes checked, 20 MiB), generated or sanitized names, create-new so nothing is overwritten, refused when `images` is a link or not a folder. Desktop: the new `paste_image` and `copy_dropped_image` commands write only beside a granted document and copy only a file Rust saw dropped (`readout/src-tauri/src/images.rs`, `readout/src/core/images.ts`, `readout/test/images.test.ts`). Browser: through the open folder's directory handle after the browser's write prompt. Needs a check on real Windows (clipboard image from Snipping Tool, Explorer drop, junction) | Medium | Typora, Obsidian |
| RD14 | **Partly built, loading blocked on an owner decision**: Remote images are still never fetched (sanitizer placeholder, CSP `img-src 'self' data: blob:`, rich-text copy omits them). New: the status bar says once "N remote images not loaded" with the hosts and a count of plain http ones in its tooltip, hidden in edit view (`readout/src/core/remote-images.ts`, `readout/test/remote-images.test.ts`, uitest scenario proving zero remote requests). The per-document "Load remote images" choice is not built, because neither safe route exists today: a page's CSP cannot be widened for one document (so loading in the WebView means `img-src https:` for every document, permanently), and the desktop shell has no HTTPS client (no TLS crate in `Cargo.lock`; `reqwest` there is Tauri's, without TLS). Options for the owner: (1) add one audited HTTPS client crate (rustls, no default features, no install scripts, 14-day rule) behind a narrow `fetch_remote_image` command with https only, no non-https redirects, no private, loopback or link-local targets (also after DNS resolution), size and time caps, image magic bytes, no credentials, `blob:` result, per-document session opt-in and optional remembered path with Forget; the browser build stays off; (2) WinHTTP through the `windows` crate (already in the lock, but a new direct dependency with new features, Windows only, untestable on Linux); (3) widen `img-src` (not recommended). Choosing (1) or (2) updates `readout/CLAUDE.md` invariant 2 | Medium | Email clients |
| RD15 | **Built**: Convert line endings, offered for files with mixed endings that open read-only. An explicit choice of LF or CRLF (the majority ending first) normalizes the text in memory and leaves the document editable and unsaved; nothing is written until Save, BOM and other characters are kept, invalid UTF-8 is never converted, and an unsaved conversion is protected by the usual disk-change prompt (`readout/src/core/document.ts`, `readout/src/ui/app.ts`). Also fixes a mixed file showing as unsaved when opened | Medium | VS Code |
| RD16 | **Built**: Links to non-Markdown files open in the default app after a confirmation (desktop only). Rust resolves the link (relative, canonical, existing regular file, inside the open folder or the lone document's folder; no drive, UNC, `file:` or alternate data stream) and allows document types only (pdf, images, Office and OpenDocument files, txt, csv, log, rtf, json, xml, yaml); programs and scripts, extensionless files, HTML and archives are refused with a message, so "scripts" are refused, not run. The dialog shows the full path and type with Cancel as the default (`readout/src-tauri/src/linked.rs`, `readout/src/ui/app.ts`, `readout/test/linked-files.test.ts`). Needs a check on real Windows | Medium | VS Code |
| RD17 | **Built**: Focus mode (F8 or the top bar button) dims every editor line outside the current paragraph, the run of non-blank lines around the cursor or around each end of a selection, in the palette's `dim2` colour (5.06:1 light, 3.70:1 dark against the editor background; a find match in a dimmed line uses `bright`). Typewriter mode (F9 or its button) keeps the cursor line vertically centered while typing, deleting, undoing and moving by keyboard (a click is not re-centered) and pads the editor by half its height so the first and last lines can center. Both are off by default, remembered per viewer, announced through the polite live region, and held by `readout/test/focus.test.ts`, `editing-modes.test.ts`, the dimmed-text pairs in `test/contrast.test.ts` and the focus and typewriter scenarios in `readout/uitest/run.mjs` (both themes). Focus mode is blank-line based, not syntax based, so a fenced code block with a blank line inside counts as two paragraphs. **Still open**: a run with a screen reader on Windows | Low | Typora |
| RD18 | **Built**: scrolling the preview moves the editor to the source line at the top of the preview, interpolated within a block and snapped to the end at the end of the preview; the editor already moved the preview. Blocks carry `data-line` (the sanitizer keeps it only with a plain number); an echo guard (`readout/src/core/scrollsync.ts`) stops the two panes driving each other, preview scrolls are followed once per animation frame, and Edit and Preview views are not synced. No setting: the existing settings are toolbar toggles and there is no natural place for one. Raw HTML blocks have no marker, and the editor-to-preview direction still snaps to block starts. Held by `readout/test/scrollsync.test.ts` and the scroll sync scenario in `readout/uitest/run.mjs` | Low | MarkText |
| RD19 | **Built**: explicit Check for updates through shared per-tool releases. Automatic installation through a verified signed feed remains future work | Medium | `docs/updates.md`; Typora, Obsidian |
| RD20 | **Built**: accessibility pass against WCAG 2.1 AA. Landmarks (banner, main, named complementary panels, footer) and names on every control; one polite live region for banners, open, save and setting changes (never keystrokes); dialogs labelled, with Tab wrapping and focus returned; the folder tree is a WAI-ARIA tree (roving tabindex, arrows, Home, End, Enter, type-ahead, `aria-expanded`, levels), the formatting bar a roving toolbar, the editor leavable with Escape then Tab; a 2px token-coloured focus ring on every focusable element; text and component contrast fixed in both themes by choosing existing palette tokens (editor marks, code comments, line numbers, field edges; `design-tokens.json` unchanged). Held by `readout/test/contrast.test.ts`, `sidebar-tree.test.ts`, `roving.test.ts` and the `accessibility:` scenarios in `readout/uitest/run.mjs` (accessibility tree, tab walk, computed-style contrast in both themes). **Still open**: a run with NVDA and Narrator on Windows; High Contrast mode; no text alternative for Mermaid diagrams | Low | WCAG 2.1; WAI-ARIA Authoring Practices |
| RD21 | Windows 11 Snap Layouts on the custom maximize button; neither Tauri nor Wails supports it yet | Low | |
| RD22 | Folder mode in Firefox and Safari is an upload and read-only (no File System Access API) | Low | |

## 4. ZTG Scanner

### Not completed

| # | Item | Priority | Status |
| --- | --- | --- | --- |
| SC1 | S4, packaging and trust: Authenticode signing, MSI, portable ZIP, SBOM, release workflow with provenance. Reuses `scripts/signing/` and Readout's MSI approach; CI checks reproducible unsigned EXEs and Windows portable/install/launch/repair/upgrade/uninstall | High | **Built and merged** (#3); O2/O3 complete. First signed Scanner release still needs a successful release run, using the shared `proudlock-signing` / `echowire-public` identity |
| SC2 | S5, actions from results: SSH, RDP, Wake-on-LAN, ping and traceroute, saved favorites, compare two scans | High | **Built and merged** (#5); installed Windows clients, bounded diagnostics, explicit on-link wake, local inventory files |
| SC3 | S6: macOS build, IPv6 neighbor discovery | Low | **IPv6 built; the macOS build is still open.** IPv6 neighbor discovery on the local link: Scanner never sweeps a prefix; it reads the system's IPv6 neighbor table (Windows `GetIpNetTable2`, Linux netlink, no privilege) for the links the targets are on, after one optional ICMPv6 echo to `ff02::1` (`Icmp6SendEcho2` on Windows, no raw socket; off in gentle mode), links neighbors to IPv4 devices by MAC address, and scans neighbors that match no IPv4 device as IPv6-only devices (same ports, limits and pacing, link-local zones handled, canonical `net/netip` text). Only link-local, unique local and the scanner's own on-link prefixes count as internal; other global addresses need `--allow-public`. JSON (`ipv6`, `ipv6_only`), CSV (`ipv6`), HTML, the desktop grid and the SC6 comparison carry the addresses; old files read unchanged. `--no-ipv6` and `--no-ipv6-ping`, plus two Settings switches. Scope limits: connection actions (Ping, SSH, RDP, Wake-on-LAN, Open with) stay IPv4; NetBIOS, LLMNR, SSDP, WS-Discovery and SNMP are not used over IPv6; mDNS or WS-Discovery replies do not link addresses (only the MAC does). Not yet validated on real Windows or a real dual-stack LAN (the table layout, `Icmp6SendEcho2` and multicast answers are tested with constructed bytes and fakes). macOS (ARP through the routing socket, and the IPv6 table there) is not built |
| SC4 | Owner Windows checks are recorded complete (O7); native Windows CI covers install and launch. New S5 SSH/RDP console handoff, the unsaved-close dialog and physical Wake-on-LAN need focused Windows/LAN checks | High | Open for new S5 checks |
| SC5 | With the Light theme, the native window behind the page could flash dark while opening or resizing | Medium | **Done** (the frontend sets the window background with the theme) |

### Improvements

Within Scanner's design rules: no bundled remote access, no vulnerability scanning, no stored
credentials, no cloud reporting.

| # | Improvement | Priority | Reference |
| --- | --- | --- | --- |
| SC6 | Saved scans per client site and change detection: new, missing and changed devices since the last scan (extends S5's compare). Shadow IT discovery is the main reason MSPs run these tools | High | **Built**: site-labeled files and comparison, with coverage and identity checks; Fing |
| SC7 | Scheduled rescans with a desktop notification when a new device appears; local only | Medium | **Built**: opt-in per saved target set, 15 minutes to 24 hours with jitter, runs only while the app is open (no service, task or autostart), private and link-local targets only, never overlaps or runs during a manual scan, reuses the scan plan (gentle included) and SC6 comparison, banner plus taskbar flash and an optional Windows toast with counts and addresses only; real Windows toast, flash and battery-saver behavior not yet exercised, metered networks not detected; Fing |
| SC8 | Per-device notes and tags (owner, location, inside the CUI boundary) kept in the scan file and exported, so an export is a starting point for a CMMC CM.L2-3.4.1 asset inventory and a client's SSP | High | **Built**: notes, tags, favorites and historical MACs in JSON/CSV; Lansweeper |
| SC9 | Saved target sets per client; import target lists from a file | Medium | **Built**: named local sets, bounded UTF-8 text import, explicit use and scan, fresh public-range authorization; Angry IP Scanner |
| SC10 | Technician-defined "Open with" commands with `{ip}` and `{hostname}` placeholders (PuTTY, mstsc, an RMM console URL). Commands come from the technician's settings, never from what a device reports | Medium | **Built**: up to 20 commands in local settings (program path plus one template per argument, or an `https://` URL with a fixed host), `{ip}` and a strictly validated `{hostname}`, exact argument vectors with no shell, Open with menu and preview in the desktop app; no CLI surface, and launching real third-party clients on Windows is not yet exercised in CI; Angry IP Scanner, Advanced IP Scanner |
| SC11 | Deeper identification through single standard exchanges: WS-Discovery (printers, ONVIF cameras), LLMNR names, an SMB2 negotiate for OS and version, an HTTP favicon fingerprint | Medium | **Built** (with two scope limits below), each probe a separate setting and `--no-...` flag, on in default mode and off in gentle mode: one WS-Discovery multicast Probe per scan (on-link internal targets only); an LLMNR PTR query over TCP 5355 with TTL 1 as RFC 4795 section 2.4 requires (unicast UDP would be discarded); one SMB2 NEGOTIATE where TCP 445 was open (dialect, signing flags, capabilities, server clock; never SESSION_SETUP or NTLM); one `/favicon.ico` GET on identified web ports (64 KiB cap, SHA-256 and the search-engine MurmurHash3 recorded). Scope limits: the operating system build and version are not read, because SMB gives them only inside the NTLMSSP challenge of a session setup (an authentication exchange), so SMB yields a dialect-based era hint labeled as an inference; and no favicon-to-product table ships, because no table with a redistributable, checkable source was available (hash only). Results are untrusted text, bounded and cleaned, and flow into names, classification, JSON, CSV, HTML and the SC6 comparison as optional fields. Not yet validated on a real LAN (multicast and the Windows firewall, real Windows LLMNR and SMB servers, real device icons); Fing |
| SC12 | Client-ready reports: branded HTML or XLSX export, and CSV layouts that match common RMM and PSA import templates | Medium | **HTML built**: offline, printable reports with scope, findings, notes and evidence from desktop or CLI; XLSX and RMM/PSA CSV layouts remain open |
| SC13 | A gentle scan mode (lower rate, fewer probes) for fragile OT, medical or old printer networks | Medium | **Built**: opt-in `--mode gentle` and Desktop Scan mode share one plan (4 hosts, 2 connections each, 10 probe starts per second, 8 ports, no banners or name/UPnP/SNMP probes); reduces load, not a guarantee for fragile devices, and lab validation on real equipment is still open |
| SC14 | Shared folder listing, only as an opt-in per scan; needs a design decision under Scanner's principles | Low | Advanced IP Scanner |

### Deliberately excluded

From `scanner/docs/design.md`, section 10: remote control (Advanced IP Scanner bundles Radmin),
remote shutdown, vulnerability scanning, credential storage and cloud reporting. Keeping them out
is what makes Scanner easy to approve in client and CMMC environments.

## 5. ZTG TFTP

T1 to T5 and the two approved T6 features are merged. The research behind it (Tftpd64, SolarWinds TFTP Server, tftpd-hpa, atftpd,
dnsmasq, Open TFTP Server, their CVE history and the gaps across them) is in
`tftp/docs/research.md`, with its sources.

| # | Item | Priority | Status |
| --- | --- | --- | --- |
| TF1 | T1 follows the proposed name, own protocol engine and 60-minute session; retention is manual deletion; include image manifests and Scanner import, defer HTTP boot and ProxyDHCP | High | Resolved |
| TF2 | T1: protocol engine, read-only server, client, scope guard, limits, name policy, audit log, fuzzing, interop tests, `tftp.yml` | High | Merged |
| TF3 | T2: drop box uploads and findings on collected configs | High | Merged |
| TF4 | T3: Windows desktop app; palette enforcement, lifecycle and UI tests | Medium | Merged |
| TF5 | T4 service mode and T5 packaging and signing (reuses Scanner's S4 work, SC1) | Medium | Merged; first production signing/publication run remains |

## 6. ZTG Directory

D1 is in development: a read-only Windows LDAP engine and CLI, portable fake tests and path-filtered CI.
Live Samba and Windows Server 2025 acceptance remains open (`directory/docs/validation.md`).
The workflows and ideas, with priorities and slices, are in `directory/docs/runbook.md`;
slices D1 to D7 and the owner's open questions (Q1 to Q9) are in `directory/docs/design.md`.

| # | Item | Priority | Status |
| --- | --- | --- | --- |
| DR1 | Owner decisions Q1 to Q9 (host, stack, Exchange Online method, approvals, console location, RADIUS, certificate storage, test estate, release timing) | High | Open |
| DR2 | D1, read-only engine and CLI with signed and sealed LDAP | High | In development; live lab validation pending |

## 7. Questions answered

**Would caching the build downloads make the installers larger?** No. The 2 GB is Microsoft's
CRT and SDK and the test browser, used on the build machine while compiling and testing. None of it
ships. Caching only keeps those files on the runners between builds. The Readout MSI stays about
5 MB and the Scanner executable about 13 MB.

## 8. ZTG Copy

The build plan is in [copy/docs/design.md](../copy/docs/design.md). Owner-selected defaults are
recorded there. RC0 has passing local, SMB and contained-logon experiments on English Server
2022 and 2025. Other releases, locales and optimized-copy progress remain validation work.
The CLI and initial Windows desktop now provide a designer, templates, preview, guarded local
copies, convergence and history, with live activity, rate and stall readings (percentage only for
serial copies with preview totals; ETA built but hidden until measured). Full destructive guards,
network/credentials UX, a validated ETA, reports and signed packaging remain in their slices. Desktop execution reuses the current runner. See [the evidence review](../copy/docs/rc0.md),
[RC1 acceptance work](../copy/docs/rc1.md) and [PR #6](https://github.com/zerotrustgarage/tools/pull/6).

## Sources

Feature claims about other products come mostly from third-party directories and reviews, not
vendor documentation; check them before relying on them.

- Typora Quick Start: https://support.typora.io/Quick-Start
- Best Markdown editors 2026: https://unmarkdown.com/blog/best-markdown-editors-2026
- Typora vs MarkText vs Obsidian: https://git.handmade.network/typora-vs-marktext-vs-obsidian-in-2025-which-markdown-editor-is-best.html
- Obsidian callouts (official help): https://help.obsidian.md/Editing+and+formatting/Callouts
- Angry IP Scanner review and alternatives (Comparitech): https://comparitech.com/net-admin/angry-ip-scanner-review-best-alternatives
- Advanced IP Scanner alternatives (AlternativeTo): https://www.alternativeto.net/software/advanced-ip-scanner/
- SoftPerfect Network Scanner alternatives (AlternativeTo): https://alternativeto.net/software/softperfect-network-scanner
- Angry IP Scanner feeders and fetchers: https://www.aldeid.com/wiki/Angry-IPScan and https://angryip.org/about/
- Fing FAQ: https://www.fing.com/faq/ and https://fing.com/
