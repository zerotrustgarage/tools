# Signing

Every Windows binary this repository releases (the app, its installer and the uninstaller the
installer writes) is Authenticode-signed through Azure Artifact Signing (formerly Trusted
Signing) and timestamped. It uses the same identity as the Jackpoint agent in
`zerotrustgarage/infrasec`: account `proudlock-signing`, certificate profile `echowire-public`,
and the publisher Windows shows as **Proudlock Technology LLC**.

## What is shared with infrasec, and what is not

`scripts/signing/azure-signing.mjs` is ported from infrasec's `scripts/releases/azure-signing.mjs`
(commit `d6baf53`), and `azure.json` and the two Microsoft root files are byte-identical copies.
Both repositories therefore enforce the same trust:

- The signature must chain to the pinned Microsoft Identity Verification Root Certificate
  Authority 2020 (SHA-256 `5367f20c...1270`), and so must the RFC 3161 timestamp.
- Exactly one signature, timestamped. Artifact Signing certificates live about three days, so
  without a timestamp a signature would stop validating almost at once; an untimestamped one is
  refused even while it is still valid.
- The signer's subject must equal the pinned publisher and carry the pinned profile EKU. Every
  Artifact Signing customer chains to the same Microsoft root, so the chain alone says nothing
  about who signed.
- jsign is a pinned jar checked against a pinned SHA-256.

The credential is the owner's choice (October 22, 2026): a client secret in the `tools-signing`
environment (option C below), with the signer role scoped to the certificate profile only,
the environment limited to `main` with required reviewers, and an expiry on the secret. A
client secret is a long-lived password that can sign as Proudlock Technology LLC from anywhere
until it expires, so those limits are what keep it small: renew it before it expires, and
revoke it at once if it may have leaked. `sign.mjs` also supports a GitHub OIDC federated
credential (option A or B), used whenever no secret is set: the release job proves which
repository, environment and workflow it is to Entra and receives a short-lived token, so no
secret is stored. Moving to it later needs no code change; delete the secret once the federated
credential works.

If infrasec ever changes the profile, publisher or roots, copy the change here in a reviewed
commit; `node scripts/signing/sign.mjs check` and CI fail if the root files do not match their
pins.

## Release pipeline

`.github/workflows/release-readout.yml`, run by hand from `main`:

All three jobs run on GitHub-hosted runners. The repository is public, so the organization's
self-hosted pool must never serve it: anyone can open a pull request, and its workflow would run
inside the organization's network. GitHub-hosted runners are free for public repositories and
isolated from it. The Windows executable is cross-compiled on Linux, and CI installs and launches
it on a real Windows runner (`windows-install` in `ci.yml`).

| Job | Sees | Does |
| --- | --- | --- |
| build | nothing secret | Compiles all third-party code (npm, cargo), runs the tests, cross-compiles the Windows exe (`scripts/ci/build-windows.sh`), writes the notices file and the SHA-256 digests |
| sign (environment `tools-signing`) | the signing identity: the client secret in two steps that run only signing code (makensis with `sign-file.sh`, and `sign.mjs` for the MSI); with OIDC, the request token is available job-wide (`id-token: write`) | Checks the digests, signs the exe, runs makensis, which signs the uninstaller (`!uninstfinalize`) and the installer (`!finalize`) through `sign-file.sh`, then verifies everything again with no credential in scope |
| publish | `contents: write` | Attests build provenance and publishes the release `readout-v<version>` with `SHA256SUMS.txt` through the REST API (`scripts/ci/github-release.mjs`; `gh` may be absent on the pool) |

The sign job also builds the Readout MSI (wixl, from the signed exe, in a step with no credential)
and signs it with the same `sign.mjs sign`; a `windows-test` job then installs the signed MSI on
a real Windows runner before `publish` may run. The release carries `ZTG-Readout-<version>-Setup.exe`,
`ZTG-Readout-<version>-x64.msi`, `ztg-readout-<version>.exe`, the notices and `SHA256SUMS.txt`.
`scripts/release/readout-release.mjs` checks version, tag and MSI identity metadata and the exact
asset set, and pull requests dry-run it with unsigned test packages; see
[release-readiness-readout.md](release-readiness-readout.md).

The split matters. A malicious dependency would run during build, which holds no signing
identity. The sign job runs only this repository's scripts, official actions, and makensis,
osslsigncode and Java from Ubuntu's archive. A signed file replaces the unsigned one only
after it verifies against the pinned roots and publisher.

## One-time owner setup

### 1. Entra: let this repository sign

Pick one. This repository uses C, with the limits above.

**A. New app registration (recommended).** A separate identity for tools means it can be
revoked without touching the Jackpoint agent's releases.

1. Entra ID, App registrations, New registration: name it `ZTG Tools Signing`, single tenant.
   Note the Application (client) ID and the Directory (tenant) ID.
2. On the certificate profile `echowire-public` (Artifact Signing account `proudlock-signing`),
   Access control (IAM), Add role assignment: **Artifact Signing Certificate Profile Signer**,
   member `ZTG Tools Signing`. Scope it to the certificate profile only, not the account,
   resource group or subscription.
3. In the app registration, Certificates & secrets, Federated credentials, Add credential,
   scenario "GitHub Actions deploying Azure resources":
   - Organization `zerotrustgarage`, repository `tools`
   - Entity type **Environment**, name `tools-signing`
   - The subject this produces is `repo:zerotrustgarage/tools:environment:tools-signing`;
     the audience stays `api://AzureADTokenExchange`.
4. Do not create a client secret.

**B. Reuse the Jackpoint signing app registration.** Add the same federated credential (step 3
above) to the existing app. Still no secret is copied, but one identity then signs both
products, and revoking it stops both.

**C. Client secret (in use).** Add `AZURE_SIGNING_CLIENT_SECRET` to this repository's
`tools-signing` environment as a secret. The release then uses it instead of OIDC. Give the app
registration only the signer role from step 2, scoped to the profile, and set the shortest
expiry you will renew (6 to 12 months). infrasec's own signing README recommends moving away from
client secrets, and A or B remains the better end state.

### 2. GitHub: the `tools-signing` environment

In `zerotrustgarage/tools`, Settings, Environments, New environment `tools-signing`:

- Deployment branches and tags: **Selected branches**, `main` only.
- Required reviewers: recommended, so each signing run needs a person's approval.
- `AZURE_SIGNING_TENANT_ID`: the Directory (tenant) ID
- `AZURE_SIGNING_CLIENT_ID`: the app registration's Application (client) ID
- `AZURE_SIGNING_CLIENT_SECRET` (option C only): a **secret**

  The two IDs are identifiers, not secrets; the release reads them from environment variables
  or environment secrets alike.

Build provenance attestations need no setting: the repository is public, and public repositories
get them on every plan.

### 3. Release

Actions, Release ZTG Readout, Run workflow on `main`. The version comes from
`readout/package.json` (it must match `src-tauri/Cargo.toml`), and the run refuses a
version that is already released.

For Scanner, run **Release ZTG Scanner** (`.github/workflows/release-scanner.yml`) on `main`.
The numeric version comes from `scanner/VERSION`; its tag is `scanner-v<version>`. It uses the
same `tools-signing` environment, `proudlock-signing` account, `echowire-public` certificate
profile, publisher and verification policy as Readout and Jackpoint. Scanner reads the tenant and client
IDs from environment variables with the same environment-secret fallback as Readout. Owner setup
O2/O3 is recorded complete; Scanner still needs its first successful production release run. Its Go cross-compile
does not download the Microsoft SDK or require `XWIN_ACCEPT_LICENSE`.

Scanner's build job checks reproducible unsigned EXEs and produces the license notices and
CycloneDX module SBOM. The sign job signs both the desktop app and CLI, packages them with
Ubuntu's wixl/msitools, signs the MSI and creates the portable ZIP from those same signed EXEs.
The MSI's standard Windows Installer uninstall needs no separate uninstaller binary.
A Windows job then verifies signatures and timestamps, compares the MSI and ZIP payloads,
and launches both installed and portable applications before the publish job attests all
download assets and publishes them. PR CI tests unsigned packages, including MSI repair,
major upgrades, downgrade refusal and removal. Interactive theme and title bar checks still
need a person (roadmap O7/SC4).

Scanner downloads are a signed x64 MSI, a portable ZIP containing signed EXEs, the SBOM,
notices and `SHA256SUMS.txt`. For example:

```powershell
Get-AuthenticodeSignature .\ZTG-Scanner-0.4.0-x64.msi | Format-List Status, SignerCertificate, TimeStamperCertificate
(Get-FileHash .\ZTG-Scanner-0.4.0-x64-portable.zip -Algorithm SHA256).Hash
gh attestation verify .\ZTG-Scanner-0.4.0-x64-portable.zip --repo zerotrustgarage/tools
```

After extracting the portable ZIP, also check `Get-AuthenticodeSignature` on both EXEs.
An archive itself has no Authenticode signature; the release checksum and provenance bind
the archive, and Authenticode binds its executables.

## Cross-compiling for Windows

`scripts/ci/build-windows.sh` runs the Tauri CLI with `cargo-xwin` (pinned version) as its
runner, targeting `x86_64-pc-windows-msvc` with clang and lld. It has to be the Tauri CLI, not
plain cargo: the CLI turns on the production build, without which the executable loads its UI
from a development server.

cargo-xwin downloads Microsoft's MSVC C runtime and Windows SDK files at build time, and
`XWIN_ACCEPT_LICENSE=1` accepts Microsoft's license terms for them on the organization's
behalf. **This is an owner decision**: it is the same license any Visual Studio Build Tools
install accepts, but confirm it is acceptable before the first release. The alternative is a
Windows build runner (see below).

## Testing on Windows

- `scripts/ci/test-installer-wine.sh <Setup.exe>` checks the installer under Wine: the WebView2
  refusal, silent install, files, shortcut, registry entries, reinstall and a clean uninstall.
- `scripts/ci/test-installer-windows.ps1` runs in CI on a GitHub-hosted `windows-2025` runner:
  both installers, the files, shortcut and registry entries, launching the app (its window title
  proves the UI loaded on WebView2), opening a file, and uninstalling.
- Interactive checks (dragging the title bar, drag and drop from Explorer, the look of each theme)
  still need a person; Windows Sandbox on a technician's PC is the quickest way.

## Verifying a download

```powershell
Get-AuthenticodeSignature .\ZTG-Readout-0.1.0-Setup.exe | Format-List Status, SignerCertificate, TimeStamperCertificate
(Get-FileHash .\ZTG-Readout-0.1.0-Setup.exe -Algorithm SHA256).Hash   # compare with SHA256SUMS.txt
gh attestation verify .\ZTG-Readout-0.1.0-Setup.exe --repo zerotrustgarage/tools
```

`Status` must be `Valid` and the signer `CN=Proudlock Technology LLC`.

## Notes and limits

- SmartScreen reputation: a signed file can still show a SmartScreen prompt until its
  reputation builds. How quickly that happens for Artifact Signing certificates is Microsoft's
  call and is not documented as a fixed rule; expect early downloads to be the most likely to
  prompt.
- Revocation checks during verification fetch Microsoft's CRLs, so the sign job needs outbound
  HTTP to `www.microsoft.com`; GitHub-hosted runners have it.
- The installer does not bundle the WebView2 Runtime. It checks for it and exits with code 4 if
  it is missing, so no third-party binary rides inside a ZTG-signed installer.
