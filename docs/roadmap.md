# Roadmap: open work and recommended improvements

Status as of October 22, 2026. This file lists what is not finished and what is recommended for
ZTG Readout, ZTG Scanner, ZTG TFTP and the repository as a whole. The recommendations come from the work so
far and from comparing both tools with established products in their categories (sources at the
end). Each tool's own design and checks are in its folder (`readout/`, `scanner/`, `tftp/`); Scanner's
slices are defined in `scanner/docs/design.md` and ZTG TFTP's in `tftp/docs/design.md`.

Priority: **High** is visible in daily use or blocks a release; **Medium** is clearly worth doing;
**Low** is polish or depends on outside changes.

## 1. Owner actions (blocking)

These need the repository owner; no code change can do them.

| # | Action | Why | Status |
| --- | --- | --- | --- |
| O1 | Set the default branch to `main` (Settings, General, Default branch) | GitHub shows it on the front page, new clones start from it, and Dependabot reads its configuration only from the default branch | **Done** |
| O2 | Create the Entra app registration and federated credential for signing (`docs/signing.md`, section "One-time owner setup") | No signed release can exist without it | **Done** (client secret, `docs/signing.md` option C, instead of the federated credential; role scoped to the profile) |
| O3 | Create the `tools-signing` environment (main only, required reviewers) with `AZURE_SIGNING_TENANT_ID`, `AZURE_SIGNING_CLIENT_ID` and the `AZURE_SIGNING_CLIENT_SECRET` secret | Same | **Done** |
| O4 | Approve accepting Microsoft's license for the CRT and Windows SDK files the cross-compile downloads (`XWIN_ACCEPT_LICENSE`) | It is accepted on the organization's behalf at every Windows build | **Done** |
| O5 | Build provenance attestations | Public repositories get them on every plan, so every release is attested; the `ATTEST_PROVENANCE` switch is gone | **Done** (repository made public) |
| O6 | Enable Dependabot security updates (Settings, Code security) | Version updates are configured; security updates are a separate switch and are not held by the 14-day cooldown | **Done** |
| O7 | Test both apps on real Windows (Windows Sandbox is enough) | CI now installs, launches and uninstalls Readout on a real Windows runner (R8); what still needs a person is the interactive side: dragging the title bar, drag and drop from Explorer, the look of both themes, and all of Scanner | **Done** |
| O8 | Keep the organization's self-hosted runner group (`proxmox-ci`) closed to public repositories (Organization settings, Actions, Runner groups: "Allow public repositories" off) | The repository is public, so anyone can open a pull request whose workflow would run on those runners, inside the organization's network and behind the egress IP that infrasec's deploy tokens trust. CI here runs on GitHub-hosted runners instead | **Done** |
| O9 | Require approval for workflows from outside contributors (Settings, Actions, General, "Fork pull request workflows from outside collaborators": all outside collaborators) | Fork pull requests then run only after a maintainer has read them | **Done** |
| O10 | Enable secret scanning with push protection and private vulnerability reporting (Settings, Code security) | Free on public repositories; push protection stops a credential from being committed in the first place | **Done** |
| O11 | Protect `main`: require pull requests and the CI checks (Settings, Branches or Rules) | Free on public repositories; see R1 | **Done** |

## 2. Repository and CI

| # | Item | Priority | Status |
| --- | --- | --- | --- |
| R1 | Pull requests, branch protection and required CI on `main`. Two sessions push straight to `main` with no review. These tools are not CUI systems, but change control in the spirit of CMMC CM.L2-3.4.3 is good practice and clients ask how tools are shipped | High | **Done** (O11: `main` requires pull requests) |
| R2 | Downloads page on ztg.sh with the signed installers, checksums and the browser version of Readout. This was the original goal and is not built | High | Open |
| R3 | One look: both tools use the ZTG palette from `design-tokens.json`; `scripts/ci/check-palette.mjs` fails CI on drift | Medium | **Done** |
| R4 | Automated dependency updates with the 14-day cooldown (`.github/dependabot.yml`: npm, Cargo, Go modules, Actions) | Medium | **Done** (needs O1 to take effect) |
| R5 | Retry `apt-get` when a mirror is mid-sync (`scripts/ci/apt-install.sh`) | Medium | **Done** |
| R6 | CI time: cache the Microsoft SDK download (about 2 GB per Windows build) and the Rust build with `actions/cache`. This affects build time only; nothing cached ships, so installer size does not change | Medium | Open |
| R7 | Public repository: every workflow runs on GitHub-hosted runners (free for public repositories, isolated from the organization's network); none may use the self-hosted pool (O8) | High | **Done** |
| R8 | Real Windows in CI: the `windows-install` job installs the NSIS installer and the MSI on `windows-2025`, checks files, shortcut and registry, launches Readout (the window title proves the UI loaded) and opens a file, then uninstalls (`scripts/ci/test-installer-windows.ps1`) | High | **Done** |

## 3. ZTG Readout

### Not completed

| # | Item | Priority |
| --- | --- | --- |
| RD1 | Done by the owner (O7): interactive checks on real Windows by a person (title bar dragging and shadow, drag and drop from Explorer, the folder context menu, both themes). Installing, launching and opening a file run in CI (R8) | High |
| RD2 | No signed release yet. The owner setup (O2 to O4) is done; what remains is the first run of the release workflow, which also proves the credential and the publisher pin | High |
| RD3 | The MSI is not in the release workflow and is not signed; it has been tested under Wine only, not through Intune or Group Policy | High |
| RD4 | THIRD-PARTY-NOTICES.txt lacks the license text for six Rust crates (`alloc-stdlib`, `selectors`, `tauri-plugin`, `webview2-com`, `webview2-com-macros`, `webview2-com-sys`); it names their licenses only. Add standard SPDX license texts as a fallback in `scripts/notices.mjs` | High |
| RD5 | Browser-driven checks (drag and drop, dividers, folder mode, title bar) ran as one-off scripts. Commit them as a `uitest/` suite in CI, as Scanner has | Medium |

### Improvements

| # | Improvement | Priority | Reference |
| --- | --- | --- | --- |
| RD6 | Obsidian callouts (all types, custom titles, foldable `+`/`-`), MkDocs `!!!` admonitions and Docusaurus `:::` blocks. Today only the five GitHub alerts render, so a vault note with `> [!warning] Title` shows as a plain quote | High | Obsidian, MkDocs |
| RD7 | Reload when the open file changes on disk (git pull, an AI agent rewriting it), asking first if there are unsaved edits; refresh the folder tree | High | VS Code, Typora |
| RD8 | Outline panel: the heading tree, click to jump | High | Typora, MarkText |
| RD9 | Search across the open folder, with results that jump to the line | High | Obsidian, VS Code |
| RD10 | Tabs or a recent-files list; a single instance (a second file from Explorer opens in the running window); remembered window size and position | Medium | Typora, MarkText |
| RD11 | Copy as rich text, so a rendered section pastes into Outlook, Teams or Word with tables and colours | Medium | Typora |
| RD12 | Export to DOCX and to PDF with bookmarks without the print dialog; a ZTG-branded print template (header, footer, client name) | Medium | Typora (Pandoc) |
| RD13 | Paste or drop an image into a document: save it beside the file and insert a relative link | Medium | Typora, Obsidian |
| RD14 | Remote images: a per-document "Load remote images" choice; they are never fetched today, by design | Medium | Email clients |
| RD15 | Convert line endings, for files with mixed endings that now open read-only | Medium | VS Code |
| RD16 | Links to non-Markdown files (PDF, scripts) open in the default app after a confirmation | Medium | VS Code |
| RD17 | Focus and typewriter modes | Low | Typora |
| RD18 | Two-way scroll sync (preview to editor) | Low | MarkText |
| RD19 | Auto-update through a signed feed, as the Jackpoint agent has | Low | Typora, Obsidian |
| RD20 | Accessibility pass: screen reader names, keyboard navigation of the file tree, light-theme contrast | Low | |
| RD21 | Windows 11 Snap Layouts on the custom maximize button; neither Tauri nor Wails supports it yet | Low | |
| RD22 | Folder mode in Firefox and Safari is an upload and read-only (no File System Access API) | Low | |

## 4. ZTG Scanner

### Not completed

| # | Item | Priority | Status |
| --- | --- | --- | --- |
| SC1 | S4, packaging and trust: Authenticode signing, MSI, portable ZIP, SBOM, release workflow with provenance. `scripts/signing/` and Readout's MSI approach are reusable | High | Open |
| SC2 | S5, actions from results: SSH, RDP, Wake-on-LAN, ping and traceroute, saved favorites, compare two scans | High | Open |
| SC3 | S6: macOS build, IPv6 neighbor discovery | Low | Open |
| SC4 | The frameless title bar and theme switch have been tested in Chromium only, not on Windows (O7) | High | Open |
| SC5 | With the Light theme, the native window behind the page could flash dark while opening or resizing | Medium | **Done** (the frontend sets the window background with the theme) |

### Improvements

Within Scanner's design rules: no bundled remote access, no vulnerability scanning, no stored
credentials, no cloud reporting.

| # | Improvement | Priority | Reference |
| --- | --- | --- | --- |
| SC6 | Saved scans per client site and change detection: new, missing and changed devices since the last scan (extends S5's compare). Shadow IT discovery is the main reason MSPs run these tools | High | Fing |
| SC7 | Scheduled rescans with a desktop notification when a new device appears; local only | Medium | Fing |
| SC8 | Per-device notes and tags (owner, location, inside the CUI boundary) kept in the scan file and exported, so an export is a starting point for a CMMC CM.L2-3.4.1 asset inventory and a client's SSP | High | Lansweeper |
| SC9 | Saved target sets per client; import target lists from a file | Medium | Angry IP Scanner |
| SC10 | Technician-defined "Open with" commands with `{ip}` and `{hostname}` placeholders (PuTTY, mstsc, an RMM console URL). Commands come from the technician's settings, never from what a device reports | Medium | Angry IP Scanner, Advanced IP Scanner |
| SC11 | Deeper identification through single standard exchanges: WS-Discovery (printers, ONVIF cameras), LLMNR names, an SMB2 negotiate for OS and version, an HTTP favicon fingerprint | Medium | Fing |
| SC12 | Client-ready reports: branded HTML or XLSX export, and CSV layouts that match common RMM and PSA import templates | Medium | Advanced IP Scanner |
| SC13 | A gentle scan mode (lower rate, fewer probes) for fragile OT, medical or old printer networks | Medium | |
| SC14 | Shared folder listing, only as an opt-in per scan; needs a design decision under Scanner's principles | Low | Advanced IP Scanner |

### Deliberately excluded

From `scanner/docs/design.md`, section 10: remote control (Advanced IP Scanner bundles Radmin),
remote shutdown, vulnerability scanning, credential storage and cloud reporting. Keeping them out
is what makes Scanner easy to approve in client and CMMC environments.

## 5. ZTG TFTP

Proposed; design only. The research behind it (Tftpd64, SolarWinds TFTP Server, tftpd-hpa, atftpd,
dnsmasq, Open TFTP Server, their CVE history and the gaps across them) is in
`tftp/docs/research.md`, with its sources.

| # | Item | Priority | Status |
| --- | --- | --- | --- |
| TF1 | Owner answers to the open questions in `tftp/docs/design.md` section 15: name, own protocol engine, default session length, which T6 candidates, retention of collected configs | High | Open |
| TF2 | T1: protocol engine, read-only server, client, scope guard, limits, name policy, audit log, fuzzing, interop tests, `tftp.yml` | High | Open |
| TF3 | T2: drop box uploads and findings on collected configs | High | Open |
| TF4 | T3: Windows desktop app; its stylesheet joins `scripts/ci/check-palette.mjs` | Medium | Open |
| TF5 | T4 service mode and T5 packaging and signing (reuses Scanner's S4 work, SC1) | Medium | Open |

## 6. Questions answered

**Would caching the build downloads make the installers larger?** No. The 2 GB is Microsoft's
CRT and SDK and the test browser, used on the build machine while compiling and testing. None of it
ships. Caching only keeps those files on the runners between builds. The Readout MSI stays about
5 MB and the Scanner executable about 13 MB.

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
