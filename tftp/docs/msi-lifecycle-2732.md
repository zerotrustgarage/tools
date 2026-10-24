# MSI lifecycle failure: error 2732 and removal result 1603

Work packet TFTP-01 (A11). Failing run 39961622459 (push to main, head 068f4f19, Windows job 120441821024).
Passing run 39965993671 (PR, head 8a42ee61, Windows job 120456907955).

## Symptom

The Windows lifecycle test passed install, repair, upgrade 0.3.0 to 0.3.1 and the refused downgrade. The final
`msiexec /x upgrade.msi` (cleanup in the `finally` block) exited 1603:

    Note: 1: 2732 2: 0
    Action start 13:32:15: FirewallRestore.
    DEBUG: Error 2732:  Directory Manager not initialized.
    Action ended 13:32:15: FirewallRestore. Return value 3.
    Windows Installer removed the product. Product Version: 0.3.1 ... Removal success or error status: 1603.

## Evidence

The `tftp-windows-install-logs` artifact of the failing run was still downloadable (the five msiexec logs), as were
both runs' `tftp-windows-packages` artifacts. The `InstallExecuteSequence` and `CustomAction` tables were decoded from
the shipped MSIs (OLE compound file, no Windows needed).

1. In the failing removal log the first action after `INSTALL` is `FirewallRestore`. Nothing ran before it, not even
   `CostInitialize`, `FileCost` or `CostFinalize`. The 2732 note is logged immediately before that action starts.
   The custom action is directory-sourced (`INSTALLDIR`, see `sequence.py`), and a directory source needs costing.
2. The failing run's `upgrade.msi` (0.3.1) has `FirewallRestore` at sequence 1 and `FirewallRemove` at sequence 2.
   `CostInitialize` is 800 and `CostFinalize` is 1000.
3. The failing run's `tftp.msi` (0.3.0), built in the same job a few seconds earlier from the same sources and
   payload, has them at 3201 and 3202. So did both MSIs of the passing run. The two packages of one job differ
   in sequencing only; the `.wxs`, `build-msi.sh` and `sequence.py` are identical.
4. All other lifecycle steps in the failing run succeeded (logs 85ad2b3f install, cc3a4e14 repair `/fa`, be8da177
   upgrade). The downgrade attempt (c50104a3) returning 1603 is the expected refusal from `LaunchConditions`.
   The failure surfaced only at the `/x` of the bad 0.3.1 package, because `FirewallRestore` and `FirewallRemove` carry
   the condition `REMOVE~="ALL"` and never ran during the earlier steps. The upgrade removed the old 0.3.0 package,
   which was well sequenced.
5. `wixl` resolves `Before=` and `After=` between custom actions (here `FirewallRestore` Before `FirewallRemove`
   Before `RemoveFiles`) and, for this chain, produced a valid table in most builds and an unanchored one
   (1 and 2) in one. `sequence.py` only fixed `RemoveExistingProducts`, and the packaging test only asserted
   `FirewallRemove < RemoveFiles`, which 2 also satisfies, so it did not detect the bad table.

## Cause

Cause (supported by logs and decoded tables): `wixl` emitted an `InstallExecuteSequence` in which the two removal
custom actions were scheduled at 1 and 2, before costing, in one of two builds of identical source. On uninstall the
engine ran a directory-sourced action before the Directory Manager existed (2732) and aborted with 1603. The
rollback action `FirewallRestore` was the first to run, which is why it is named in the log.

Not proven: the exact mechanism inside `wixl` (an unstable resolution order is the leading explanation; I did not
have `wixl` available to rebuild repeatedly). The fix does not depend on it: every firewall action is now pinned.
The failure was intermittent because the bad layout is only produced by some builds, and PR #15 passed because its
packages were built with the good layout. Rerunning without a fix would pass most of the time.

Ranked alternatives, all rejected by the evidence: (a) harness timing (msiexec was `-Wait`ed, the log shows the
product fully installed before the `/x` began, and the table differs in the package itself); (b) runner differences
(same image family, no 2732 on the 0.3.0 package on the same machine); (c) WebView2 or service state (the failing
step precedes any file removal).

The harness also hid the first problem: the cleanup `/x` in `finally` threw and replaced any earlier error, and logs
were only kept on failure.

## Fix

- `tftp/installer/sequence.py`: pins `FirewallRestore` and `FirewallRemove` just after `RemoveShortcuts` and the three
  install-side actions just after `InstallFiles` (the layout of the good builds), then verifies the whole table
  (every firewall action after `CostFinalize` and `InstallInitialize`, restore before remove before `RemoveFiles`,
  install-side actions between `InstallFiles` and `InstallFinalize`, no duplicate numbers) and fails the build if not.
- `tftp/ci/sequence_test.py` (new, run in the `desktop` job): reproduces the run 39961622459 layout (1 and 2) and
  other bad layouts; the tests fail against the previous `sequence.py`.
- `tftp/ci/packaging.test.mjs`: asserts every firewall action follows `CostFinalize`/`InstallInitialize`, and rebuilds the
  MSI 12 times requiring an identical sequence table each time.
- `tftp/ci/test-windows.ps1`: prints and asserts the `InstallExecuteSequence` of both MSIs via the Windows Installer
  COM API before installing; names every msiexec log `NN-op-package`; a failing final `/x` no longer masks the
  earlier error (it is a warning, then a hard failure if nothing else failed).
- `tftp.yml`: runs `sequence_test.py`; the msiexec logs are uploaded on every run (`if: always()`), so a green
  run also carries the evidence.

## What the PR CI run must show

1. `desktop` job: `python3 tftp/ci/sequence_test.py` OK; `packaging.test.mjs` passes, including 12 identical rebuilds.
2. `windows-install` job log: two `InstallExecuteSequence of ...` listings (tftp.msi, upgrade.msi) with
   `FirewallRestore`/`FirewallRemove` between `RemoveShortcuts` and `RemoveFiles` and all firewall actions above
   `CostFinalize` (1000).
3. The full lifecycle passes: install, repair, upgrade, refused downgrade, `/x`, firewall rules and registry gone.
4. The `tftp-windows-install-logs` artifact holds `01-i-tftp.log` ... through the final `/x`; in the final `/x` log
   `Action start ... CostInitialize` precedes `FirewallRestore`/`FirewallRemove` and there is no `Note: 1: 2732`.
5. Because the defect was intermittent, a single green run is not the proof; the deterministic proof is items 1
   and 2. Re-running the Windows job two or three times adds confidence but is not required.

Rollback coverage (a failure inside an install, to exercise `FirewallRollback`) is not added here: it would need a
failing custom action in a test-only package. Recommend as a follow-up if rollback regression evidence is wanted.
