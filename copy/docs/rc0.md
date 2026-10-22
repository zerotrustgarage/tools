# RC0 evidence and remaining work

RC0 is in progress. This harness collects feasibility evidence before RC1; it is not the
production runner or desktop app. Owner-selected defaults are recorded in design section 16.

## Automated experiments

CI runs the Go spike on real Server 2022 and Server 2025. Synthetic local experiments passed
on both releases in [run 39833135244](https://github.com/zerotrustgarage/tools/actions/runs/39833135244).
The additional native SMB/logon experiment is being validated separately.

| Question from design section 14 | Evidence |
| --- | --- |
| 1. File-line and percentage timing | Throttled 4 MiB serial and /MT:8 copies, raw pipe events and 100 ms destination observations. File lines precede a matching readable destination tail by seconds. |
| 2. Unicode redirected output | Raw /UNICODE pipes, strict decode attempts and /UNILOG filename checks. The pipe mixes encodings; the native log preserves Unicode. |
| 3. Process I/O counters | Samples and final counters for controlled local copies with offload disabled (and cloning disabled where supported). Raw counters are not copied-byte totals. |
| 4. /QUIT validation | Valid commands succeed; unknown options return 16. |
| 5. Installed help tokens | Full /? capture and extracted tokens. The catalog comparison comes in RC1. |
| 6. Go argument quoting | Content-equal copies with spaces, Unicode, trailing separators, paths over 260 characters and plain long endpoint arguments; volume-root /QUIT. Explicit device-prefix paths are a separate unsupported-input observation. |
| 7. Destination junctions | Real junctions created without a shell; /PURGE control, /XJ and explicit /XD variants, each with a private target sentinel. |
| 8. Network-only logon | CI-only native experiment: create suspended, assign and verify job membership before resume, query I/O, then test stop through job close. Validation in progress. |
| 9. Temporary connections | CI-only native experiment: generated local accounts, CONNECT_TEMPORARY, 1219, cancel/reconnect, and cleanup. No passwords enter strings, command lines or records. Validation in progress. |
| 10. Engine signing | System directory, engine hash, file version, signature status/type, Windows build and locale. Production signature enforcement remains a review decision. |

The local suite reproduces native exits 0, 1, 2, 3, 4, 8 and 16, including a real locked file
with /R:0 and a file/directory collision preview. Positional numeric summary rows match between
preview and copy for the controlled fixture. The summary helper is an RC0 assertion helper,
not the production parser; it rejects ambiguous tables and non-byte units.

## Observed constraints

- On both tested releases, /UNICODE pipes mix narrow-byte text and UTF-16LE fragments despite a
  BOM. Preserve raw bytes. Use the native Unicode log for filename correctness. /UNILOG passed
  full UTF-16LE decoding and preserved café-日本語-😀.txt.
- Help detection must use /? alone. /UNICODE /? is rejected. Both the valid help request and the
  rejected combination return 16, so that code alone cannot classify help discovery.
- A file line is not a completion event. In the throttled runs, it arrived around 11 to 17 ms;
  the destination tail became readable with matching bytes around 3.7 seconds. Allocation size,
  readable tail bytes and process I/O each have limits; a matching tail alone is not full-file
  verification. Artifacts preserve sampling and pipe timing for review.
- /XJ did not protect the target of a destination junction during /PURGE. Explicit /XD of the
  junction did. The private sentinel was deleted in both the control and /XJ cases and survived
  with /XD. This supports guard S7; source-side exclusion alone is insufficient.
- Plain long paths copy correctly. Explicit extended device-prefix arguments were readable
  through Windows file APIs but Robocopy rejected them with native exit 16 and Win32 error 53,
  with or without a trailing separator. The production probe must keep canonical paths for
  identity/overlap checks separate from invocation paths. It must not pass device-prefixed
  final paths through unexamined or strip prefixes in ways that change path semantics.

Raw initial console/native-log fixtures and provenance are committed under
`internal/spike/testdata/windows-2025-en/`. Later artifacts contain the additional experiments;
selected captures are promoted after inspection. Artifact retention is 14 days.

## Still required before RC0 review

- Finish and inspect the native SMB/logon experiment, including cleanup and job-close results.
- Capture Windows 10 22H2 with ESU, Windows 11 24H2/25H2, Server 2016/2019, and localized German,
  French, Spanish and Japanese. Server 2022/2025 English are now exercised in CI.
- Exercise real SMB/offload/block-clone counter behavior; local unoptimized I/O is not a
  substitute. Keep progress advisory where the source cannot establish completion.
- Review and record the remaining technical decisions, the supported-release choice and RC0
  evidence before RC1. Initial owner defaults are resolved; optional RC7 scope remains deferred.

RC1 remains gated by the plan's RC0 review. The spike does not provide production jobs,
credential UX, ETA guarantees, a desktop UI, reports or release packaging.
