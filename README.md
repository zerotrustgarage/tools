# ZTG Tools

Downloadable tools offered from [ztg.sh](https://ztg.sh). Each tool ships as a signed Windows
installer, and where it makes sense as a browser version that runs entirely on the user's
machine.

| Tool | Folder | What it is |
| --- | --- | --- |
| ZTG Readout | [readout/](readout) | Source-faithful Markdown editor and viewer for files and whole folders (git repositories, docs trees, Obsidian vaults) |
| ZTG Scanner | [scanner/](scanner) | Network discovery and device inventory for technicians: CLI and Windows desktop app, ZTG's replacement for Advanced IP Scanner |
| ZTG TFTP | [tftp/](tftp) | Time-boxed TFTP server and CLI client, read-only by default, with opt-in private collection, configuration findings, hashes and change records (T1 and T2 implemented) |

## Layout

| Path | Purpose |
| --- | --- |
| `<tool>/` | One top-level folder per tool, named after it: the app, its `installer/` script and its README. Each is an npm workspace listed in the root `package.json` |
| `scripts/signing/` | Authenticode signing and verification through Azure Artifact Signing, shared by every tool |
| `scripts/ci/` | Runner setup, the Windows cross-compile, the Wine installer test and the release publisher |
| `scripts/notices.mjs` | Generates THIRD-PARTY-NOTICES.txt from the npm and Rust dependency graphs |
| `.github/workflows/ci.yml` | Checks on every pull request and push to main; no secrets |
| `.github/workflows/scanner.yml` | ZTG Scanner checks (Go, packaging, portable/MSI lifecycle on Windows), path-filtered; no secrets |
| `.github/workflows/release-<tool>.yml` | Manual release per tool: build, sign, package, attest, publish |
| `docs/signing.md` | Signing design and the one-time owner setup |
| `docs/roadmap.md` | Open work and recommended improvements for every tool |
| `design-tokens.json` | The ZTG palette every tool's stylesheet is checked against |

## Checks

```bash
npm ci
npm run check                      # editor typecheck and tests, signing tests
cd readout/src-tauri && cargo test --locked && cargo clippy --all-targets -- -D warnings
```

ZTG Scanner is a Go module (`scanner/`, with the desktop app as a second module in `scanner/app/`), not an npm
workspace; its checks are listed in [scanner/README.md](scanner/README.md).

The signing tests need `makensis`, `osslsigncode` and `openssl` (on Ubuntu:
`apt-get install nsis osslsigncode`). They use a throwaway test CA, never real credentials.

## Runners

The repository is public, so every workflow runs on GitHub-hosted runners (`ubuntu-24.04` and
`windows-2025`, free for public repositories). The organization's self-hosted pool must never
serve this repository: anyone can open a pull request, and its workflow would run inside the
organization's network. Windows executables are cross-compiled on Linux
(`scripts/ci/build-windows.sh`), then installed, launched and uninstalled on a real Windows runner
(`scripts/ci/test-installer-windows.ps1`).

## One look

Every tool uses the ZTG palette. `design-tokens.json` is a byte-identical copy of the canonical
token file (`zerotrustgarage/ztg`, generated in `zerotrustgarage/infrasec`), and
`scripts/ci/check-palette.mjs` (CI job "ZTG palette") fails when a tool's stylesheet drifts from
it. To change a colour, change the canonical file and copy it here.

## Dependency policy

- Versions are exact (`.npmrc` `save-exact`, `Cargo.toml` `=` pins, both lockfiles committed).
- New versions are taken only after they have been public for at least 14 days
  (`npm install --before=<date>`), except releases that fix a published advisory.
- Install scripts never run (`.npmrc` `ignore-scripts`).
- GitHub Actions are pinned by commit SHA.
- Dependabot (`.github/dependabot.yml`) proposes updates weekly for npm, Cargo, Go modules and
  Actions with the same 14-day cooldown; minor and patch updates arrive grouped. Each pull request
  runs CI like any other change.
