# CLAUDE.md

Downloadable ZTG tools, one top-level folder per tool, each with its own toolchain, tests,
release workflow and `CLAUDE.md`. Read the tool's own file before changing it:

- `readout/`: ZTG Readout, a source-faithful Markdown editor and viewer (TypeScript, Tauri).
  See `readout/CLAUDE.md`.
- `scanner/`: ZTG Scanner, network discovery and inventory (Go). See `scanner/CLAUDE.md`.
- `tftp/`: ZTG TFTP, a TFTP server and client (Go, CLI and Windows desktop app, service mode and release packaging). See `tftp/CLAUDE.md`.
- `directory/`: ZTG Directory, Active Directory and Microsoft 365 management installed per customer (plan,
  slice D0; no code yet). See `directory/CLAUDE.md`.

- `copy/`: ZTG Copy, Robocopy job designer and evidence recorder (Go, desktop preview and guarded local CLI). See `copy/CLAUDE.md`.

## Repository-wide rules

1. A new tool gets its own folder named after it, its own `CLAUDE.md`, and its own
   `.github/workflows/release-<tool>.yml`. Shared code lives in `scripts/` only when a second
   tool uses it.
2. The repository is public. Every workflow runs on GitHub-hosted runners (`ubuntu-24.04`,
   `windows-2025`), never on the organization's self-hosted pool: anyone can open a pull request
   here, and its workflow would run inside the organization's network. Windows executables are
   cross-compiled on Linux and tested on a Windows runner. Actions are pinned by commit SHA.
3. Dependencies: exact versions, lockfiles committed, new versions only after 14 days public
   unless they fix a published advisory, no install scripts, and each new dependency justified
   in its commit message. Dependabot (`.github/dependabot.yml`) applies the same cooldown.
4. Windows binaries are signed only by a release workflow, through `scripts/signing/` and the
   `tools-signing` environment (`docs/signing.md`). That trust is deliberately shared with the
   Jackpoint agent in `zerotrustgarage/infrasec`: the settings and pinned roots are copies, and
   a change there is copied here in a reviewed commit. Apart from the palette (rule 6), nothing
   else is shared with the platform repositories.
5. Writing style: no em dashes; plain, specific language. Conventional commits.
6. One look: every tool uses the ZTG palette in `design-tokens.json`, enforced by
   `scripts/ci/check-palette.mjs`. Open work and recommendations are tracked in `docs/roadmap.md`.
7. Every application must offer an explicit Check for updates action (or `check-updates` in a CLI).
   Reuse `scripts/updates/` and its release contract in `docs/updates.md`. Do not introduce background
   checks or automatic installation without an explicit product decision.
