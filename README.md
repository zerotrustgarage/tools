# ZTG Tools

Downloadable tools offered from [ztg.sh](https://ztg.sh). Each tool ships as a signed Windows
installer, and where it makes sense as a browser version that runs entirely on the user's
machine.

| Tool | Folder | What it is |
| --- | --- | --- |
| ZTG Readout | [readout/](readout) | Source-faithful Markdown editor and viewer for files and whole folders (git repositories, docs trees, Obsidian vaults) |
| ZTG Scanner | [scanner/](scanner) | Network discovery and device inventory for technicians: CLI and Windows desktop app, ZTG's replacement for Advanced IP Scanner |

## Layout

| Path | Purpose |
| --- | --- |
| `<tool>/` | One top-level folder per tool, named after it: the app, its `installer/` script and its README. Each is an npm workspace listed in the root `package.json` |
| `scripts/signing/` | Authenticode signing and verification through Azure Artifact Signing, shared by every tool |
| `scripts/ci/` | Runner setup, the Windows cross-compile, the Wine installer test and the release publisher |
| `scripts/notices.mjs` | Generates THIRD-PARTY-NOTICES.txt from the npm and Rust dependency graphs |
| `.github/workflows/ci.yml` | Checks on every pull request and push to main; no secrets |
| `.github/workflows/scanner.yml` | ZTG Scanner checks (Go), path-filtered to `scanner/`; no secrets |
| `.github/workflows/release-<tool>.yml` | Manual release per tool: build, sign, package, attest, publish |
| `docs/signing.md` | Signing design and the one-time owner setup |

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

Every workflow runs on the organization's self-hosted pool, `[self-hosted, linux, ephemeral]`.
GitHub-hosted runners are not assigned to this organization, and Windows executables are
cross-compiled on Linux (`scripts/ci/build-windows.sh`).

## Dependency policy

- Versions are exact (`.npmrc` `save-exact`, `Cargo.toml` `=` pins, both lockfiles committed).
- New versions are taken only after they have been public for at least 14 days
  (`npm install --before=<date>`), except releases that fix a published advisory.
- Install scripts never run (`.npmrc` `ignore-scripts`).
- GitHub Actions are pinned by commit SHA.
