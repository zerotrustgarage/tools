# RC1 implementation and remaining acceptance

The initial CLI implements flags, explain, import, validate, plan, run, version, check-updates
and template list/show/save/command/run/export/import. Examples and exact restrictions are in
[the README](../README.md). This is an unsigned development preview, not a completed RC1 or
production support claim.

## Implemented

- Embedded 97-option catalog checked against flags.md; searchable help, tags, symmetric conflicts,
  implications and the security-copy requirement.
- Strict schema-versioned jobs. Unknown switches survive import and are flagged. Unknown fields
  and password fields are rejected.
- Windows argument tokenization/rendering, fuzzed round trips and a native Windows tokenizer check.
  No shell execution or environment expansion.
- Per-user versioned templates with typed placeholders, built-ins, defaults, import acceptance,
  strict decoding and argument-bound substitution. Template runs enter the same execution policy.
- System-directory engine discovery and SHA-256; installed help-token probing and /QUIT validation.
- A suspended process, verified job membership before resume, kill-on-close containment and a
  standard-handle allowlist. Sticky cancellation tested with real monitoring Robocopy.
- Existing local fixed-drive ordinary DAT copies, with mandatory preview and convergence.
  Overlap, roots, reparse points, unsupported options and record locations inside the trees are
  refused. Endpoint file identities are rechecked before each phase.
- Strict UTF-16LE native-log parsing and positional numeric totals, checked against real Server
  2022 and 2025 captures. Raw mixed-encoding console bytes are preserved separately.
- Per-user owner-only evidence folders, phase events, exact argument lists, log hashes, summaries,
  technician/host, native exits and normalized outcomes.
- Explicit update checks using the shared release contract, without background requests.

Real-engine testing showed that /MT reports directory traversal as copied on a subsequent
list-only pass even with native exit zero. Convergence therefore removes /MT only, retaining
the same selection and copy semantics, and uses serial directory create/skip totals.
The copy itself retains the selected thread count. Neither file announcements nor I/O counters
are treated as completed bytes.

## Still required for full RC1 acceptance

- Accurate activity/rate/stall metrics and replayable clock interfaces. Progress is currently phase
  status and final totals, with no percentage or ETA claim. The roadmap's minute-long ETA accuracy
  criterion has not been met.
- Full production probe metadata, including engine file version, elevation/privileges and a
  catalog-aware signature decision.
- Broader option/type/preset validation and RCJ import/export from the design.
- More Windows releases/locales and optimized-copy fixtures as tracked in rc0.md.
- Error-by-cause detail and the full run-record metadata required by design section 10.
- Additional acceptance tests for large trees, locked files and changing-source jobs in the
  production orchestration, beyond the separate RC0 experiments.

## Later slices

Destructive operations, alternate credentials, network execution, the desktop interface,
hash/security verification, HTML/CSV reports and signed packaging remain unimplemented.
The initial runner refuses those execution modes. The reference and template designer can
still explain or retain their switches for review.

The safety slice must replace the blanket reparse/destructive restrictions with the full
S1 to S13 guard behavior before those modes become available. In particular, /XJ alone is
not destination-junction protection; the RC0 purge experiment requires explicit /XD exclusions.
