# RC1 implementation and remaining acceptance

The initial CLI implements flags, explain, import, export, validate, plan, run, version, check-updates
and template list/show/save/command/run/export/import. Examples and exact restrictions are in
[the README](../README.md). This is an unsigned development preview, not a completed RC1 or
production support claim.

## Implemented

- Embedded 97-option catalog checked against flags.md; searchable help, tags, symmetric conflicts,
  implications and the security-copy requirement.
- RCJ import and export (October 29, 2026; design section 9.4, `internal/rcj`): `explain --rcj`, `import --rcj`
  (explained first, saved only with `--output --accept`) and `export --rcj`. Bounded size and lines; UTF-16, UTF-8 and
  ANSI input; `::` comments; `/SD` `/DD` `/NOSD` `/NODD` `/IF`; every other switch goes through the catalog, so
  unknown, hidden (`/REG`, `/JOB`, `/SAVE`, `/DEBUG`) and badly valued switches are kept and flagged. Export refuses jobs
  with errors (never writes `/REG`), writes the safe defaults and no credentials, and reads back to the same job
  arguments (property tests over generated jobs and a fuzz target, `FuzzRCJReader`).
- RCJ in the desktop app (October 29, 2026; design sections 9.2 and 9.4; `app/rcj.go`, `app/frontend/rcj.js`):
  Import RCJ reads through `internal/rcj` with its limits from a native open dialog and shows the explanation
  (switches with unknown, hidden, destructive and not-enabled flags, list items, import notes, errors and warnings)
  before anything is loaded; the job reaches the designer only after confirmation (an acknowledgement is required
  when something is flagged), nothing is saved or run, and file text is inserted with `textContent` only. Export RCJ
  applies the exporter's refusals before any dialog, shows the text and size, offers encoding auto (default), UTF-8
  or UTF-16, writes through the native save dialog with the owner-only writer, never replaces a file, and shows the
  `robocopy /JOB:FILE /QUIT` hint. Go tests use the app's fake platform; the UI tests drive both flows with the
  binding mock. The native open and save dialogs and the Windows DACL still need a run on real Windows.
- Typed option values from the catalog `type` field (`job.CheckValue`): numbers, sizes with units, day counts or
  dates, attribute and copy flag letters, run windows, y or n, and log paths. Hidden options are job errors.
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
- Error-by-cause details from streaming native Unicode logs, grouped by agreeing decimal/hex
  Windows codes, with bounded excerpts and suggestions. Counts include retries and do not change
  summary/exit-based outcomes. Activity and saved-run details show the grouped errors.
- Probe metadata in the run record (`probe`): robocopy.exe file version from its PE version
  resource (read directly, not through the shimmed GetFileVersionInfo), process token
  elevation type, SeBackup/SeRestore/SeSecurity state (absent, present but disabled, enabled) and a signature decision (embedded, catalog, unsigned or unknown, from
  WinVerifyTrust and the catalog database, with no network or revocation lookup). Each field names
  its source or says why it is unavailable. Older records without `probe` still load. The signature
  decision says how the file is signed, not that the signer is trusted. Windows behavior is covered
  by tests in the Copy Windows job; the unit tests use fakes.
- A saved-run inspector with per-phase native exit bitmap meanings, including older records.
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
- RCJ on real Windows: the layout and quoting are a conservative reading because the design and sources give no
  grammar and the repository has no real sample (design section 9.4). Needed: `robocopy /JOB:file /QUIT` accepting an
  exported file in each encoding mode, a `/SAVE:` capture read back by `import --rcj`, and non-ASCII behavior. Wire
  `FuzzRCJReader` into `.github/workflows/copy.yml` (its seeds already run under `go test`).
- Preset validation: design section 9.2 lists seven presets but the designer has three buttons; the listed option
  sets validate (tests), but presets are not data yet.
- More Windows releases/locales and optimized-copy fixtures as tracked in rc0.md.
- Broader diagnostic format/locale coverage and the remaining run-record metadata required by
  design section 10. Grouped native-log error headers are implemented; other diagnostic formats
  remain available only in the raw log.
- Additional acceptance tests for large trees, locked files and changing-source jobs in the
  production orchestration, beyond the separate RC0 experiments.

## Later slices

The owner requested the GUI next. The initial Windows interface now wraps the guarded local
runner, with design, preview, confirmation, activity, templates, history, themes and updates.
Destructive operations, alternate credentials, network execution, hash/security verification,
HTML/CSV reports and signed packaging remain unimplemented.
The initial runner refuses those execution modes. The reference and template designer can
still explain or retain their switches for review.

The safety slice must replace the blanket reparse/destructive restrictions with the full
S1 to S13 guard behavior before those modes become available. In particular, /XJ alone is
not destination-junction protection; the RC0 purge experiment requires explicit /XD exclusions.
