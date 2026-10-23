# Checking for updates

Every ZTG tool must provide an explicit **Check for updates** action, or a `check-updates`
command in a CLI-only build. This is a release requirement for new tools, including Copy and
TFTP's desktop application. Automatic installation is optional future work; the current
implementation checks versions and opens official downloads for the user to install.

## Available controls

| Tool | Control | Status |
| --- | --- | --- |
| Scanner | **Check for updates** in the desktop toolbar; `ztg-scanner check-updates [--json]` | Implemented |
| Readout | **Check for updates** in the status bar, in desktop and browser builds | Implemented |
| TFTP | **Check for updates** in the desktop header; `ztg-tftp check-updates [--json]` | Implemented |
| Copy | Shared checker accepts `copy`; application action required when RC1/desktop ships | Copy is currently an RC0 capture harness, not an end-user app |
| Directory | `ztg-directory check-updates` from D1, **Check for updates** in the console from D2; tag prefix `directory-v` added to the shared checker with the first package (D7) | Planned (`directory/docs/design.md`) |

The shared standalone checker also supports all four products, including Copy before its app
exists. From `scripts/updates/`, with the pinned Go toolchain:

```sh
go run ./cmd/ztg-update -tool copy -current dev -json
go run ./cmd/ztg-update -tool readout -current 0.1.0
```

No check runs at startup, on a schedule, during scans or during transfers. Closing a dialog
cancels its check. Offline checks show an error and allow retry; they do not report “up to date.”
Manual checks work in managed environments without an updater service or administrator rights.
An installed MSI can be upgraded with the newer signed MSI; a portable user replaces their
extracted copy after closing the old app. The check never overwrites an open application.

## Release selection and support contract

The only API endpoint is `https://api.github.com/repos/zerotrustgarage/tools/releases`.
The browser client sends no credentials or referrer. The CLI uses no token. No document, scan,
target, transfer record or application credential is sent. GitHub receives the request and its
normal connection metadata; there is no telemetry endpoint or update registration.

Each tool has a separate stable tag prefix: `scanner-v`, `readout-v`, `tftp-v` or `copy-v`.
Drafts, prereleases, invalid tags, incomplete uploads and releases for other tools are ignored.
Selection compares numeric semantic versions, not release dates or lexical strings. Build
metadata does not cause an upgrade, stable releases replace prereleases of the same version,
and a development/newer build is never presented as needing a downgrade.

A candidate must contain an uploaded, nonempty `SHA256SUMS.txt` and its supported Windows installer:

| Tool | Installer name |
| --- | --- |
| Scanner | `ZTG-Scanner-<X.Y.Z>-x64.msi` |
| Readout | `ZTG-Readout-<X.Y.Z>-Setup.exe` |
| TFTP | `ZTG-TFTP-<X.Y.Z>-x64.msi` (contract for T5) |
| Copy | `ZTG-Copy-<X.Y.Z>-x64.msi` (contract for its packaging slice) |

The release workflows must verify signatures against the shared Azure Artifact Signing
publisher/profile, timestamp binaries, test their packages and publish checksums and provenance
before exposing a completed release. An API asset listing is not signature verification. The
checker opens the official GitHub release page, never an API-provided URL or executable. It
does not download, verify or run an installer. Signed download verification is documented in
[signing.md](signing.md). The Windows desktop apps currently target x64; this check does not
claim that a Windows download can update Linux, macOS or an ARM64 CLI.

Checks have a 15-second total deadline, a 4 MiB limit per page and a ten-page limit (100 releases
per page). API redirects are refused. Rate limits and access restrictions are reported so the
user can retry or view releases. No-releases is distinct from an up-to-date result. CLI JSON
statuses are `available`, `current`, `newer`, `development` and `unreleased`; successful checks
exit 0, while network/response errors exit nonzero. No action is taken automatically.

## Maintaining it

The shared Go and browser implementations live in `scripts/updates/`. The Go code uses only the
standard library. `sync.mjs` vendors identical source into the independent Scanner and TFTP Go
modules, and into Scanner's and TFTP's embedded frontends. Readout imports the browser source directly.
This adds no module or shipping dependency. CI and release builds reject stale copies.

```sh
node scripts/updates/sync.mjs          # after changing shared source
npm run check:updates                 # JavaScript fixtures and copy consistency
cd scripts/updates && go test -race ./... && go vet ./...
```

Both clients use the same release/version fixtures. Tests cover mixed-product history, numeric
version ordering, build metadata, prereleases, missing checksums/installers, untrusted URLs,
oversized responses, rate limits, pagination and offline failures. Desktop tests cover explicit
checks, safe release handoff, retry and close behavior. Future automatic updates must verify
the downloaded artifact and publisher before execution, preserve unsaved work and active jobs,
support installed and portable modes, and expose opt-out and managed deployment policy.
