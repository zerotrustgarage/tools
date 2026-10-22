# RC0 evidence and remaining work

RC0 is in progress. No Windows behavior is treated as established until a real capture
supports it. This harness is the first implementation, not completion of RC0 or RC1.

## Automated now

The collector runs only synthetic local data. It records:

| Question from design section 14 | Evidence produced |
| --- | --- |
| 1. File-line and percentage timing | Raw serial and /MT:8 pipe chunks with receive timestamps. Completion timing still needs a controlled slow copy and destination observations. |
| 2. Unicode redirected output | /UNICODE raw bytes, strict UTF-16LE decoding, non-ASCII and supplementary-character filename assertions. /UNILOG equivalence remains open. |
| 3. Process I/O counters | 100 ms samples where the process is queryable. Short copies can finish between samples; SMB, offload and cloning remain open. |
| 4. /QUIT validation | Separate valid and deliberately unknown-option captures, with native exit assertions. |
| 5. Installed help tokens | Both raw default and Unicode help and extracted invariant tokens. Catalog comparison comes in RC1. |
| 6. Go argument quoting | Real copies using spaces, Unicode and trailing backslashes; content equality checks. Volume roots and extended paths remain open. |
| 10. Engine signing | System-resolved path, SHA-256, file version, Authenticode status and SignatureType from Windows. Whether production should enforce signature validation remains undecided. |

## First observed decisions

Real Server 2025 captures are committed under `internal/spike/testdata/windows-2025-en/`,
with provenance. On that build, /UNICODE console output mixes narrow-byte text with
UTF-16LE fragments despite a BOM. Keep exact bytes; do not decode the whole stream as
UTF-16LE or use it to establish filename correctness. The /UNILOG experiment passed: the complete native log decoded as UTF-16LE and preserved
all fixture filename characters while the simultaneous pipe still mixed encodings. Use native
logs for filename correctness on this build; repeat the experiment across the support matrix.
Help option detection must use /? alone: /UNICODE /? is rejected as an invalid option.
Both the full help and that invalid combination return 16, so exit 16 does not by itself
establish that a valid help request failed.

## Still required before the RC0 review

- Controlled large-file capture proving when file lines occur, serial and /MT.
- A locked file, long paths, every exit-code bit and preview/copy summary comparisons.
- Disposable destination junction experiments with /PURGE and /XD (question 7).
- A temporary test SMB share: encryption/signing, alternate connections, error 1219 and
  cleanup (question 9). Never commit real account names, server names or credentials.
- Network-only logon with job-object containment and I/O measurement (question 8).
- Captures on Windows 10 22H2 with ESU, Windows 11 24H2 and 25H2, Server 2016, 2019,
  2022 and 2025; localized German, French, Spanish and Japanese captures.
- Promote reviewed synthetic captures to versioned testdata, then record technical decisions
  in design.md. CI artifacts expire and are not a substitute for committed fixtures.
- Resolve the owner's default, retention and support choices in design section 16.

RC1 remains gated by the plan's RC0 review. The spike does not run production jobs,
manage credentials, promise progress/ETA correctness or provide a desktop UI.
