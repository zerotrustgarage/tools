# Release readiness: Readout MSI, Scanner and TFTP

Status as of October 24, 2026 (packet READOUT-04, roadmap RD3). This page separates what
pull request CI proves without any secret from what only a protected, credentialed release run
can prove. Nothing here signs or publishes anything. No unsigned package is a production
download. In the dry run only the artifact name (`readout-release-dryrun-UNSIGNED`),
`DRY-RUN-UNSIGNED.txt` and `asset-manifest.json` are labelled unsigned. The files inside
`dist/` carry the production names on purpose (that is what the manifest checks) but are
unsigned test packages; never publish or distribute them.

## 1. Readout release assets

`ZTG-Readout-<X.Y.Z>` is released by `.github/workflows/release-readout.yml` (manual, `main`
only). The exact asset set, produced by `scripts/release/readout-release.mjs`:

| Asset | Signed in production | Purpose |
| --- | --- | --- |
| `ZTG-Readout-<X.Y.Z>-Setup.exe` | yes (installer and its uninstaller) | NSIS installer; the file the update checker selects (`docs/updates.md`) |
| `ZTG-Readout-<X.Y.Z>-x64.msi` | yes | per-machine MSI for Intune, Group Policy and RMM |
| `ztg-readout-<X.Y.Z>.exe` | yes | portable executable |
| `THIRD-PARTY-NOTICES.txt` | no | license texts |
| `SHA256SUMS.txt` | no | SHA-256 of the four files above |

The update checker contract is unchanged: it still requires `SHA256SUMS.txt` and the Setup
installer, so adding the MSI to a release cannot change what Check for updates selects.

### How the MSI joins the shared signing path

- The sign job installs `wixl` and `msitools` from Ubuntu's archive (as the Scanner and TFTP
  releases do), builds the MSI with `readout/installer/build-msi.sh` from the already signed
  `ztg-readout.exe`, in a step that has no credential.
- The MSI is then signed by the same `node scripts/signing/sign.mjs sign <file>` that signs the
  exe and the Scanner and TFTP MSIs: Azure Artifact Signing through jsign, the pinned Microsoft
  roots and timestamp roots, one timestamped signature, the pinned publisher subject and profile
  EKU. A signed file replaces the unsigned one only after that verification passes. No
  verification code was copied or changed; `scripts/signing/` is untouched.
- A second, credential-free step runs `sign.mjs verify` on the exe, Setup.exe and the MSI, then
  `readout-release.mjs assemble` names the files, writes `SHA256SUMS.txt` and fails unless the
  set is exactly the table above.
- A new `windows-test` job (real `windows-2025`, no signing identity) downloads the signed set,
  checks the hashes, and runs `scripts/release/test-readout-msi.ps1 -RequireSignatures`: the MSI
  and installed exe must be Valid, timestamped and signed by `CN=Proudlock Technology LLC`;
  the installed exe must be byte-identical to the released `ztg-readout-<X.Y.Z>.exe`; then
  install, repair (`/fa` after deleting files), launch with a file, and uninstall.
- `publish` needs `windows-test`, re-checks the set with `verify-dist`, attests provenance for
  `dist/*.exe` and `dist/*.msi`, and creates the release through the existing
  `scripts/ci/github-release.mjs` (a draft that becomes public only after every upload).

### Metadata consistency (every run, no secret)

`node scripts/release/readout-release.mjs check [--tag readout-vX.Y.Z]` fails unless:

- `readout/package.json`, `src-tauri/Cargo.toml` and the `ztg-readout` entry of `Cargo.lock`
  carry the same version, and `tauri.conf.json` takes its version from `package.json`.
- The version is numeric `X.Y.Z` within the MSI limits (255.255.65535). `build-msi.sh` enforces
  the same range, so an out-of-range package cannot be built.
- `ztg-readout.wxs` takes its ProductVersion from the build (`$(var.Version)`), keeps the
  permanent UpgradeCode `231e4623-6212-429c-a15b-44729806918b` (pinned in the script, so changing
  it needs a reviewed change in two places), the product name `ZTG Readout`, the manufacturer
  `Proudlock Technology LLC`, `MajorUpgrade` with a downgrade message, and per-machine scope.
- The NSIS script names the same product and publisher, and `docs/updates.md` still documents
  `ZTG-Readout-<X.Y.Z>-Setup.exe` as the update installer.
- If a tag is given, it equals `readout-v<package version>`. The release workflow also refuses a
  version whose tag already exists, and runs `check --require-newer`, which requires the version
  to be strictly greater than the highest `readout-v*` tag on the remote (`git ls-remote`, no
  credential).
- On Windows, `windows-test` and the lifecycle job also require the installed exe's file
  version to start with the package version, which catches a regression in the version stamping
  that real MSI upgrades depend on.

## 2. Uncredentialed checks (pull request CI, no secret)

| Check | Where | Proves |
| --- | --- | --- |
| Metadata check, `readout-release.test.mjs` | `ci.yml` job `signing`, and the release `build` job | the rules above, including that each kind of drift is reported; asset assemble/verify logic rejects missing, extra and tampered files; the MSI built by `build-msi.sh` carries the pinned ProductVersion, name, manufacturer, UpgradeCode and the WebView2 launch condition, rejects bad versions, and accepts an Authenticode signature from a throwaway test CA (the MSI part skips where wixl is absent, and runs in CI) |
| Release dry run | `ci.yml` job `windows`, artifact `readout-release-dryrun-UNSIGNED` | builds Setup.exe and the MSI at the real package version, assembles the five-file set through the release's own code, verifies the set and checksums, and shows that the production verifier (`sign.mjs verify`) refuses every unsigned binary in it |
| MSI lifecycle | `ci.yml` job `windows-msi-lifecycle`, artifact `readout-msi-lifecycle-logs` | on real Windows, with unsigned test packages: install of a prior build, repair, major upgrade to the real exe, refused downgrade (1603), launch opening a file, repair after upgrade, uninstall leaving no files, shortcut, registry keys or product registration, and exactly one registration at each step |
| Existing install test | `ci.yml` job `windows-install` | unchanged: NSIS and MSI install, launch, open a file, uninstall |

The dry-run manifest (`asset-manifest.json` in the artifact, also in the job summary) lists the
five names with SHA-256 and size, headed `UNSIGNED DRY RUN`. The names are exactly what the
release will upload. The checksums are those of the unsigned test packages and will differ in
the release, because signing changes the bytes. They show that the set is complete and
consistently named, not what the final hashes are.

Limits of the prior-build fixture: no Readout MSI has been released (v0.1.0 shipped the Setup
installer and the portable exe only), so there is no real prior MSI to upgrade from. The fixture
is synthetic: an unversioned stand-in file packaged as 0.0.1, upgraded to the real exe packaged
as 0.0.2. (A first attempt used the real exe with bytes appended; both builds then had the same
PE file version, Windows Installer kept the old file, and the old product's removal deleted it,
see PR run 40065420159. That cannot happen across real releases because Tauri stamps the exe's
file version from package.json, which every release bumps. Two MSIs with the same exe version are
not a supported upgrade.) A real next-release upgrade test becomes possible once an MSI release exists.
Upgrading an NSIS install with the MSI (or the reverse) is not tested; both write the same
folder and registry entries and neither detects the other.

## 3. What only a protected release can prove

These need the `tools-signing` environment (owner approval) and cannot be shown on a pull
request:

- Azure Artifact Signing accepts the request for the MSI and returns a certificate chaining to
  the pinned Microsoft root, timestamped, from the pinned publisher. (Also proven only for exe
  and Setup.exe by the v0.1.0 run; the MSI file type is new for Readout, though Scanner and TFTP
  already use the same call.)
- `SHA256SUMS.txt` of the signed files, and `gh attestation verify` on the exe and the MSI.
- The signed MSI on Windows: `windows-test` with `-RequireSignatures`.
- A real GitHub release with the five assets, and `Check for updates` seeing it.
- SmartScreen and Intune or Group Policy deployment behavior, and interactive checks (roadmap
  RD1), which remain a person's job.

Release steps for Readout (owner): bump `readout/package.json`, `src-tauri/Cargo.toml` and
`Cargo.lock` together, merge to `main`, confirm the dry-run artifact on the merged commit, then
Actions, Release ZTG Readout, Run workflow on `main`, approve the `tools-signing` deployment.
Afterwards verify as in `docs/signing.md` ("Verifying a download"), for the MSI as well.

## 4. Scanner release checklist (`scanner-v<version>`)

Workflow: `.github/workflows/release-scanner.yml`. Current `scanner/VERSION` on main is 0.7.0, so
the tag would be `scanner-v0.7.0` and no Scanner release has been published yet. Re-read the
version on the commit being released; every name below follows it.

Expected assets (`<v>` = `scanner/VERSION`):

1. `ZTG-Scanner-<v>-x64.msi` (signed)
2. `ZTG-Scanner-<v>-x64-portable.zip` (contains the signed `ZTGScanner.exe` and `ztg-scanner.exe`,
   `README.txt`, `THIRD-PARTY-NOTICES.txt`, `scanner.cdx.json`)
3. `scanner.cdx.json`
4. `THIRD-PARTY-NOTICES.txt`
5. `SHA256SUMS.txt`

Before dispatch:

- [ ] Latest `CI` and `Scanner` workflow runs on the exact `main` commit are green, including the
      Scanner MSI repair, upgrade, refused downgrade and removal job.
- [ ] `scanner/VERSION` is the intended `X.Y.Z` (MSI limits 255.255.65535) and `scanner-v<v>` does
      not exist (`git ls-remote --tags origin 'refs/tags/scanner-v*'`).
- [ ] `node scripts/updates/sync.mjs --check` is clean; `docs/updates.md` still lists
      `ZTG-Scanner-<X.Y.Z>-x64.msi`.
- [ ] The `tools-signing` environment is unchanged: `main` only, reviewers set, client secret not
      expired (`docs/signing.md`).
- [ ] The owner has approved publication.

Run: Actions, Release ZTG Scanner, Run workflow on `main`, approve `tools-signing`. The job order
is build (reproducible unsigned EXEs compared byte for byte, SBOM, packaging tests) then sign,
then `windows-test` on the signed MSI and ZIP (`-RequireSignatures`), then publish.

After: [ ] release has exactly the five assets; [ ] `sha256sum --check SHA256SUMS.txt`;
[ ] `Get-AuthenticodeSignature` Valid with a timestamp on the MSI and both extracted EXEs;
[ ] `gh attestation verify` on the MSI and ZIP; [ ] Check for updates in the Scanner app and
`ztg-update -tool scanner` report the new version; [ ] interactive checks still owed (roadmap SC4).

## 5. TFTP release checklist (`tftp-v<version>`)

Workflow: `.github/workflows/release-tftp.yml`. `tftp/VERSION` on main is 0.3.0. No TFTP release
exists. **Do not release until tools#22 is merged and shows a passing Windows run.** tools#22
(`fix(tftp): pin firewall custom actions after costing in the MSI sequence`, still an open draft
when this was written) explains and fixes the intermittent MSI lifecycle failure: Windows
Installer error 2732 ("Directory Manager not initialized") and removal error 1603, caused by
wixl sometimes placing the `FirewallRestore` and `FirewallRemove` actions before CostInitialize
in `InstallExecuteSequence`. A release run would build, sign and test an MSI whose sequence is
nondeterministic until that lands.

Expected assets (`<v>` = `tftp/VERSION`):

1. `ZTG-TFTP-<v>-x64.msi` (signed)
2. `ZTG-TFTP-<v>-x64-portable.zip` (signed `ZTGTFTP.exe` and `ztg-tftp.exe`, README, notices, SBOM)
3. `tftp.cdx.json`
4. `THIRD-PARTY-NOTICES.txt`
5. `SHA256SUMS.txt`

Before dispatch:

- [ ] tools#22 is merged, its Windows run showed the firewall actions above 1000 in both package
      listings, a full install, repair, upgrade, refused downgrade and uninstall pass, and no
      2732 in the final uninstall log. Re-run CI on `main` after the merge and expect the same.
- [ ] Decide whether `tftp/VERSION` must be bumped so the release contains the fix (the
      version-already-released check only stops a repeat tag).
- [ ] Latest `CI` and `TFTP` runs on the exact `main` commit are green.
- [ ] `tftp/VERSION` is `X.Y.Z` within MSI limits and `tftp-v<v>` does not exist.
- [ ] `docs/updates.md` still lists `ZTG-TFTP-<X.Y.Z>-x64.msi`; update sync check is clean.
- [ ] `tools-signing` unchanged and the secret unexpired; owner approval given.

Run: Actions, Release ZTG TFTP, Run workflow on `main`, approve `tools-signing`.

After: the same list as Scanner (five assets, checksums, signatures on the MSI and both EXEs,
attestations, Check for updates for `tftp`), plus install, upgrade from the previous TFTP MSI if
any, and uninstall once on a clean machine to confirm the firewall rules are created and removed.
