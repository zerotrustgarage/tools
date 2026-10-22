# CLAUDE.md

Downloadable ZTG tools, one top-level folder per tool, each with its own toolchain, tests,
release workflow and `CLAUDE.md`. Read the tool's own file before changing it:

- `readout/`: ZTG Readout, a source-faithful Markdown editor and viewer (TypeScript, Tauri).
  See `readout/CLAUDE.md`.
- `scanner/`: ZTG Scanner, network discovery and inventory (Go). See `scanner/CLAUDE.md`.
- `tftp/`: ZTG TFTP, a TFTP server and client (Go, proposed; design only). See `tftp/CLAUDE.md`.

## Repository-wide rules

1. A new tool gets its own folder named after it, its own `CLAUDE.md`, and its own
   `.github/workflows/release-<tool>.yml`. Shared code lives in `scripts/` only when a second
   tool uses it.
2. Every workflow runs on `[self-hosted, linux, ephemeral]`; GitHub-hosted runners are not
   assigned to this organization and there is no Windows runner. Windows executables are
   cross-compiled. Actions are pinned by commit SHA.
3. Dependencies: exact versions, lockfiles committed, new versions only after 14 days public
   unless they fix a published advisory, no install scripts, and each new dependency justified
   in its commit message.
4. Windows binaries are signed only by a release workflow, through `scripts/signing/` and the
   `tools-signing` environment (`docs/signing.md`). That trust is deliberately shared with the
   Jackpoint agent in `zerotrustgarage/infrasec`: the settings and pinned roots are copies, and
   a change there is copied here in a reviewed commit. Nothing else is shared with the platform
   repositories.
5. Writing style: no em dashes; plain, specific language. Conventional commits.
