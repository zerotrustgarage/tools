# RC0 engineering review and evidence

Review date: October 22, 2026. The local, temporary SMB and network-only logon experiments
passed on English Server 2022 and Server 2025 in
[run 39834396093](https://github.com/zerotrustgarage/tools/actions/runs/39834396093), commit
`93b11840ce3dd5aa249ac8c12c24c631824377f8`. Raw selected fixtures, timing events, environment
facts and network results are committed under `internal/spike/testdata/rc0-{2022,2025}-en/`.
The native harness remains separate from the production runner.

## Review decisions

| Question from design section 14 | Result and implementation decision |
| --- | --- |
| 1. File-line timing, serial and /MT | File lines arrived about 15 to 18 ms into controlled throttled copies; matching readable destination tails about 3.7 seconds later. A line is an announcement, never proof of completion. Preserve raw timing. |
| 2. Redirected Unicode | /UNICODE pipes mix narrow bytes and UTF-16 fragments despite a BOM. Native /UNILOG decodes completely and preserves café-日本語-😀.txt. The native Unicode log is authoritative for names and summary. |
| 3. Process I/O | Controlled 4 MiB local copies report 4,194,304 bytes read, but writes include logging. Network child counters are queryable. Treat counters as activity, not completed bytes. Optimized/offloaded/cloned transfers remain unvalidated. |
| 4. /QUIT | Valid commands succeed and unknown options return 16. Run validation before preview. Help is different: /? and rejected /UNICODE /? both return 16. |
| 5. Help tokens | Capture /? alone and detect option tokens, independent of localized help. Check catalog coverage against these fixtures; detect newer options at runtime. |
| 6. Argument quoting | Plain long paths, spaces, Unicode, trailing separators and volume-root validation pass. Explicit device-prefix arguments fail with engine error 53 even when Win32 file APIs read them. Keep canonical identity paths separate from invocation paths; refuse unsupported spelling. |
| 7. Destination junctions | /PURGE deletes a private sentinel through a destination junction both with and without /XJ. Explicit /XD of that junction protects it. Guard S7 must enumerate destination junctions and exclude them explicitly. |
| 8. Network-only logon | CreateProcessWithLogonW suspended, job assignment verified before resume, I/O query and job-close stop all pass. The stopped process returns native 0, so cancellation must remain a separate, sticky outcome. |
| 9. Temporary connections | CONNECT_TEMPORARY works. Different credentials to the same host reproduce 1219; cancel/reconnect succeeds. Created connections, users and shares are removed and native password buffers zeroed. No secret reaches a managed string, command or record. |
| 10. Signing | Both inbox engines have valid catalog signatures. Production trust checks must understand catalogs; an embedded-signature-only check would reject the real engine. Resolve only through GetSystemDirectoryW and record version/hash. |

The local suite reproduces native exits 0, 1, 2, 3, 4, 8 and 16. Positional six-column numeric
summary rows agree between preview and copy for a known 81,920-byte Unicode fixture. The RC0
summary helper rejects ambiguous tables, overflow and non-byte units. Exit bitmap unit tests
cover every value from 0 through 31 and unknown values.

## Scope and next slice

The review supports beginning the RC1 data model, catalog, command import, templates and CLI
foundation. The owner accepted the defaults recorded in design section 16 and requested
continued implementation. These components do not depend on unvalidated progress assumptions.
The support proposal remains a proposal; the passing CI matrix is exactly Server 2022/2025
English. No production support or ETA guarantee follows from this review.

The following remain release gates and must stay visible as RC1 develops:

- Windows 10 22H2 with ESU, Windows 11 24H2/25H2 and Server 2016/2019 captures.
- German, French, Spanish and Japanese native logs and positional-summary fixtures.
- Real SMB offload and block-clone behavior. Keep progress indeterminate/advisory until completion
  can be established, including when counters are misleading.
- A production catalog-aware signature policy, complete guard suite and owner Windows testing.
- The remaining supported-release decision. Optional RC7 scope is deferred.

The spike does not provide production credential UX, a desktop interface, reports or release
packaging. RC1 remains in progress until its real-engine plan/run and metrics acceptance checks pass.
