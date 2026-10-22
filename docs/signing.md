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

What is not shared is the credential. infrasec's `retained-agent-signing` environment holds a
client secret. Copying it here would put a second copy of a long-lived secret in a second
repository. This repository instead uses a GitHub OIDC federated credential by default: the
release job proves which repository, environment and workflow it is to Entra, and receives a
short-lived token. No secret is stored anywhere. A client secret still works if one is set (see
option C below).

If infrasec ever changes the profile, publisher or roots, copy the change here in a reviewed
commit; `node scripts/signing/sign.mjs check` and CI fail if the root files do not match their
pins.

## Release pipeline

`.github/workflows/release-readout.yml`, run by hand from `main`:

| Job | Runner | Sees | Does |
| --- | --- | --- | --- |
| build | windows-latest | nothing secret | Compiles all third-party code (npm, cargo), runs the tests, writes the unsigned exe, the notices file and their SHA-256 digests |
| sign | ubuntu-latest, environment `tools-signing` | the signing identity, in one step | Checks the digests, signs the exe, runs makensis, which signs the uninstaller (`!uninstfinalize`) and the installer (`!finalize`) through `sign-file.sh`, then verifies everything again with no credential in scope |
| publish | ubuntu-latest | `contents: write` | Attests build provenance and creates the release `readout-v<version>` with `SHA256SUMS.txt` |

The split matters. A malicious dependency would run during build, which holds no signing
identity. The sign job runs only this repository's scripts, official actions, and makensis,
osslsigncode and Java from Ubuntu's archive. A signed file replaces the unsigned one only
after it verifies against the pinned roots and publisher.

## One-time owner setup

### 1. Entra: let this repository sign

Pick one.

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

**C. Copy the client secret (not recommended).** Add the existing
`AZURE_SIGNING_CLIENT_SECRET` to this repository's `tools-signing` environment as a secret. The
release then uses it instead of OIDC. infrasec's own signing README already recommends moving
away from the client secret.

### 2. GitHub: the `tools-signing` environment

In `zerotrustgarage/tools`, Settings, Environments, New environment `tools-signing`:

- Deployment branches and tags: **Selected branches**, `main` only.
- Required reviewers: recommended, so each signing run needs a person's approval.
- Environment **variables** (identifiers, not secrets):
  - `AZURE_SIGNING_TENANT_ID`: the Directory (tenant) ID
  - `AZURE_SIGNING_CLIENT_ID`: the app registration's Application (client) ID

If the organization's plan does not include artifact attestations for private repositories
(GitHub Enterprise Cloud does), set the repository variable `ATTEST_PROVENANCE` to `false`.

### 3. Release

Actions, Release ZTG Readout, Run workflow on `main`. The version comes from
`readout/package.json` (it must match `src-tauri/Cargo.toml`), and the run refuses a
version that is already released.

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
