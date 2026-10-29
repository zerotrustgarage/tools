# RC1 implementation and remaining acceptance

The initial CLI implements flags, explain, import, export, validate, plan, run, version, check-updates
and template list/show/save/command/run/export/import. Examples and exact restrictions are in
[the README](../README.md). This is an unsigned development preview, not a completed RC1 or
production support claim.

## Implemented

- Live metrics and replayable clock (October 29, 2026; design section 6 note, `internal/metrics`): an injectable
  `Clock` (system and fake), a tracker driven by explicit inputs, a replay harness, and a tailer that reads the native
  Unicode log while Robocopy writes it. Signals, each kept apart: completed files and bytes (a file counts only after
  its own `100%` marker, with no error header and a successor line; serial only; never from announcements or I/O
  counters), transfer activity (process read bytes over a 5 second window, never completed), and status (starting,
  active, retrying, stalled after 60 seconds without a progress signal, retry waits excluded; a stall is not a
  failure). Rates are over 5 seconds (completed and activity) and 30 seconds (files per second); a percentage needs a
  serial copy and preview totals; with `/MT`, `/NP`, `/NFL`, `/NC` or `/NS` no completed total, rate or percentage
  is shown until the summary arrives. Wired to `run.json` (a `metrics` object per preview, copy and convergence
  phase with a timeline of at most 240 points and no file names), to the CLI (one reading line every 10 seconds and
  on a status change) and to the desktop Activity view (live and final readings, stall and retry status, no percentage
  where it is not allowed). Replay tests cover each signal, stall and retry, the visibility rules, timeline bounds and
  JSON round trips; `FuzzMetricsScanner` is in copy.yml.
- ETA: the design formula is implemented (least squares over the completions of the last minute) and measured by
  replay, but it is switched off (`Config.AllowETA`), so no surface shows an ETA. See "Measured ETA error" below.
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

- ETA accuracy. The roadmap's criterion (within 15% after the first minute on the fixture jobs) is not met and
  no ETA is shown. The RC0 timing captures are single throttled 4 MiB files of 3.77 to 3.80 seconds, so they have no
  readings after the first minute. Needed: a real-engine fixture set with jobs longer than a minute (many small files,
  large files, and a mix, serial and `/MT`), recorded with timestamps in the replay format, then enable
  `Config.AllowETA` only if the replay error stays within 15%.
- Metrics on real Windows (the code is built and compiles for Windows; these were not run here): the Windows CI
  tests added with this work (`TestObservedRunReportsMonotoneProcessIOAndKeepsTheResult`, the `metrics` checks in
  `TestPreviewCopyConvergenceAndRecord`) need a green run on `windows-2022` and `windows-2025`; and four facts the
  captures do not show: whether Robocopy flushes the `/UNILOG` file while it runs (the live view relies on it; when it
  does not, only the I/O reading moves and a long quiet copy can look stalled), whether the per-file `NN%` markers
  reach the `/UNILOG` for large files as they do the console (only the final `100%` of an 81,920-byte file is
  captured), how zero-byte files and `/MT` with many files print, and what the I/O counters do for SMB, offloaded and
  block-cloned copies (the design says the view keeps activity separate so a misleading counter cannot become
  progress). Localized builds were not read; the scanner reads structure, but German, French, Spanish and Japanese
  logs still need fixtures.
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

## Measured ETA error

Command: `go test ./internal/metrics -run 'ActivityETAError|ETAFit|FixturesCannotSupport' -v`. Replayed inputs are the
four RC0 timing captures (Server 2022 and 2025, English, serial and `/MT:8`): one 4 MiB file throttled to about
1 MiB per second, 3.77 to 3.80 seconds long, I/O counters every 100 ms.

- ETA from completed-file accounting (the design formula): no estimate at any point of any capture. The only file is
  counted when the run ends, so there is nothing to fit, and every capture ends before the one-minute gate. The
  production setting also keeps the ETA hidden, tested on all four. Measured error: not measurable, and no claim made.
- For information only, an ETA built from the transfer-activity signal (remaining bytes over the 5 second activity
  rate, never shown): between 1 second and 3.3 seconds the mean error was 15.0% (2022 serial), 13.8% (2022 `/MT`),
  15.1% (2025 serial) and 11.7% (2025 `/MT`), and the worst reading was 52.9%, 54.0%, 55.7% and 48.1%. That is on a
  perfectly steady throttled copy of one file and not after a minute, so it supports no claim, and activity is not
  completed bytes in any case.
- Synthetic checks of the fit arithmetic (not evidence for the roadmap): an equal-files job at 1 file per second gave a
  worst error of 1.64% and a mix of 40 small files and one 256 MiB file per cycle (0.2 s per file, 20 MiB/s) gave
  1.33%, both from the first minute until 30 seconds remained (below that one file is too large a share of what is
  left for a relative error to mean much). A job whose speed changes fourfold midway gives a range, not a point.

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
