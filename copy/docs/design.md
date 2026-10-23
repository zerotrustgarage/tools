# ZTG Copy: design and build plan

Status (October 23, 2026): RC0 core evidence is reviewed on Server 2022/2025 English.
The RC1 CLI designer and initial guarded local runner are implemented; acceptance remains
in progress. See [rc0.md](rc0.md) for remaining release/locale validation and [rc1.md](rc1.md)
for implemented behavior and remaining acceptance work. Research is in [research.md](research.md)
(its gaps are cited here as G1 to G18) and the option reference in [flags.md](flags.md).

## 1. Purpose

ZTG Copy helps a technician design, run and prove a Robocopy job:

- Design: every option explained in plain language, with what it does, when to use it, what it risks and whether the
  installed Windows supports it; source and destination pickers; alternate credentials for shares; a live command
  preview; import and explanation of an existing command line; saved templates for jobs a technician runs often.
- Run: an honest overall progress bar, current and average speed, files per second, elapsed time and an ETA for the
  whole job, with retries, failures and their causes shown as they happen.
- Prove: a structured record of what ran, by whom, with which options, what was copied, skipped, deleted or failed,
  optionally verified by hash, exportable as a report.

It is for MSP file work: server and NAS migrations, profile moves, mirrors to backup targets, archive moves and WAN
seeding. It replaces third-party Robocopy GUIs in ZTG and client environments with a tool whose source, build chain
and signing ZTG owns.

## 2. Principles

These are requirements, not preferences. Each answers a gap in the research.

1. Wrap Robocopy, do not replace it. The copy is done by the Microsoft-signed `robocopy.exe` in the system directory,
   so the result is exactly what the same command would do in a script, and assessors see a familiar engine
   (section 3).
2. Explain before running. Every option the builder offers has help text, risk tags and conflicts from one catalog
   (G1). Unknown options in an imported command are flagged, never silently dropped.
3. Destructive operations are previewed and confirmed. `/MIR`, `/PURGE`, `/MOV`, `/MOVE` and `/M` always run a
   list-only preview first; deletions are counted, shown and confirmed, and refused in the cases in section 7 (G8).
4. Credentials are never stored, never placed on a command line and never written to a job, export, log or record.
   They live in native memory for as long as the connection is being made, then are zeroed (G3).
5. Never trust localized text for correctness. Robocopy's exit code and the positional numbers of its summary decide
   outcomes; text parsing only adds detail (G5, G6).
6. Accountable. Every run produces a record: technician, host, elevation, Robocopy version and hash, the exact
   arguments, totals, timeline, exit code decoded, errors by cause, guard decisions and verification (G7, G16).
7. Safe defaults. `/R:2 /W:5` instead of one million retries, `/XJ` on, `/DCOPY:DAT` for folder copies, a log for
   every run. `/REG` is never offered (G2).
8. Honest about the transport. The tool shows whether each SMB connection is encrypted and signed, says that
   `/COMPRESS` is not encryption, and never claims a copy is protected when it cannot tell (G18).
9. Local only. No telemetry, no cloud reporting; records stay where the technician keeps them, owner-only.
10. Few dependencies: Go standard library, `golang.org/x/sys` for Windows APIs, Wails v2 for the desktop app, as in
    ZTG Scanner and ZTG TFTP.

## 3. Wrap Robocopy or write an engine

| | Wrap `robocopy.exe` (recommended) | Own copy engine |
| --- | --- | --- |
| Fidelity | Identical behaviour to scripts the technician already runs | A second implementation of ACL, owner, auditing, backup semantics, sparse, EFS, offload and block clone |
| Trust | Microsoft-signed, in-box, already in every baseline (CM.L2-3.4.6) | New code on the data path, to be justified to every assessor |
| Effort | Parsing and process control | Months, and a long tail of NTFS edge cases |
| Limits | Inherits Robocopy's limits (locked files, text output) and must parse output | Full control of progress and verification |

Wrapping keeps the tool's value where Robocopy is weak (design, safety, credentials, metrics, evidence) and leaves the
copy to the engine clients already accept. The costs of wrapping are handled by sections 6 and 11.

## 4. Architecture

```
cmd/ztg-copy     CLI: flags, explain, plan, run, verify, report, version
internal/catalog     embedded option catalog: groups, help, tags, conflicts, implications, privileges
internal/job         job model (endpoints, file filters, options, metadata), JSON load and save, validation
internal/template    saved templates: placeholders, substitution, library storage, import and export
internal/cmdline     argument rendering, Windows command-line tokenizer for import, cmd and PowerShell quoting
                     for display, .RCJ job file read and write (fuzzed)
internal/probe       robocopy.exe discovery, version, hash and supported options; privileges and elevation;
                     volume, drive and free space facts; SMB connection facts; BitLocker state
internal/netconn     temporary deviceless SMB connections with alternate credentials; Windows credential dialog;
                     cleanup of connections the tool created
internal/guard       destructive-operation checks (section 7)
internal/runner      process launch in a job object, output capture, stop
internal/parse       Robocopy output: file lines, retry and error lines, summary table (fuzzed, golden fixtures)
internal/metrics     progress model, rates, ETA, stall detection, timeline
internal/verify      convergence check, SHA-256 manifest, security descriptor comparison
internal/record      run record, JSON Lines event log, HTML and CSV reports, owner-only storage
app/                 desktop app (separate Go module, Wails v2, as in ZTG Scanner)
```

The catalog is data (embedded JSON), generated from or checked against [flags.md](flags.md) in CI. Each entry has:
token, group, argument type (none, number, size with units, date or days, attribute letters, copy flags, list,
time window, path), default, short and long help, tags (destructive, privileged, conflict, detect, hidden),
`conflicts_with`, `implies` (for example `/MIR` implies `/E` and `/PURGE`, `/SEC` sets `/COPY:DATS`), `requires`
(`/SECFIX` needs a security copy flag), required privileges, and suggested presets.

The runner, parser and metrics take a clock and an output stream as interfaces, so tests replay captured Robocopy
output at any speed without Windows.

## 5. Environment probes

Before a job is built or run, the tool learns the machine it is on:

- Robocopy: resolved as `GetSystemDirectoryW()\Robocopy.exe`, never through `PATH` or the current directory. The record
  keeps its file version and SHA-256. Whether to also verify its catalog signature is decided in RC0.
- Supported options: `robocopy /?` is run once and its option tokens collected (tokens are not localized, the help
  text is). Options tagged detect are offered only when present; an imported command using a missing option is
  flagged.
- Command validation: the final argument list is checked with `/QUIT` before a run, so the installed Robocopy, not
  the catalog, has the last word (behaviour confirmed in RC0).
- Privileges and elevation: whether the token holds `SeBackupPrivilege`, `SeRestorePrivilege` and
  `SeSecurityPrivilege`, and whether the process is elevated. `/B`, `/ZB`, `/COPY:U` and usually `/COPY:O` are marked
  unavailable with the reason when they are not.
- Each endpoint: drive type (fixed, removable, network), file system name, total and free space, whether the path is a
  volume root, reparse points on the path, and the canonical final path (to detect overlap through mapped drives,
  junctions and UNC aliases).
- SMB endpoints: dialect, encryption and signing for the session, read through the in-box `Get-SmbConnection` CIM
  class. Where PowerShell is blocked (constrained language mode, AppLocker), the state is shown as unknown, never
  assumed.
- Removable destinations: BitLocker protection state through the shell property `System.Volume.BitLockerProtection`,
  which a standard user can read; unknown if it cannot be read.
- Timestamp precision: `/FFT` (and `/DST` for FAT) is suggested when either side is FAT or exFAT; after a run, if a
  second pass recopies most files with unchanged sizes, the history page suggests `/FFT` for that pair (G12).

## 6. Run engine and metrics

A run has up to four phases: probe, preview, copy, verify.

Preview (pre-scan). The same command with `/L /BYTES /FP /X` added. Its summary table gives the totals the ETA needs:
directories, files and bytes to copy, to skip, mismatched and extra (to be deleted under `/PURGE` or `/MIR`). The
summary is read by column position, so it works in every language; per-file lines add detail (largest files,
deletions list). On very large trees the preview can take minutes; it may be skipped for non-destructive jobs, at the
cost of an ETA, and is mandatory for destructive ones.

Copy. The runner adds what it needs to the technician's options: `/BYTES /FP /TS`, and `/UNILOG:<run
folder>\robocopy.log` with `/TEE` so a native Robocopy log exists beside the tool's own record. It removes options
that would hide what it parses (`/NFL`, `/NJS`) and says so in the preview. The process is started with an argument list the
tool quotes itself (no shell, trailing backslashes normalized, G9), with no window, inside a job object that kills
Robocopy if the app closes, so no orphaned mirror keeps deleting in the background.

Progress comes from three sources, combined:

1. Completed-file accounting: each file line, once the file is known to be finished, adds its size and count.
   How completion is signalled with and without `/MT` is the first RC0 question.
2. Process I/O counters (`GetProcessIoCounters`), sampled every 500 ms, for the instantaneous byte rate, including
   inside one large file and under `/MT`. Copy offload and block cloning move data without the process reading it,
   so RC0 measures when the counters understate and the model falls back to source 1.
3. Retry and error lines: a file waiting between retries marks the job "retrying", not "stalled".

Metrics shown live and recorded:

| Metric | Definition |
| --- | --- |
| Progress | Bytes done over bytes to copy from the preview; files done over files to copy, shown side by side because small-file jobs move by files, not bytes |
| Current speed | Bytes per second over the last 5 seconds, in MB/s and Mbit/s |
| Average speed | Bytes copied over copy time, excluding the preview |
| Peak speed | Highest 5 second rate |
| Files per second | Files completed over the last 30 seconds |
| Elapsed | Wall time per phase and in total |
| ETA | Time remaining = remaining bytes / byte rate + remaining files x per-file overhead, both fitted over the last 60 seconds, so a job of many small files is not predicted from its large-file rate. Shown as a range when the rates are unsteady; not shown without a preview |
| Stall | No byte or file progress for 60 seconds while not in a retry wait |
| Outcome counts | Copied, skipped, mismatched, failed, extra, deleted; directories and files |
| Errors | Grouped by Win32 code with a plain-language cause and fix (5 access denied, 32 file in use, 53 and 67 path not found, 64 connection lost, 112 disk full, 1219 credential conflict, 1326 bad credentials), with the first paths of each |

Exit codes. The native bitmap is decoded into words in every view (G6). The CLI returns a normalized code by default,
so RMM tools and scheduled tasks read it correctly: 0 success (Robocopy 0 to 3), 1 success with warnings (bit 4,
mismatches), 2 failure (8 or more, or verification failed), 3 the tool refused or could not start. `--native-exit`
returns Robocopy's own code.

Stop. Stop ends the job object. Robocopy leaves an interrupted file with a 1970 timestamp (G15); the record lists it,
and the next run of the same job recopies it.

## 7. Safety guards

Checked after the preview and before the copy. "Refuse" can be overridden only where stated.

| # | Condition | Action |
| --- | --- | --- |
| S1 | Source missing or unreachable with `/MIR` or `/PURGE` | Refuse |
| S2 | Source holds no files with `/MIR` or `/PURGE` | Refuse; override in the GUI by typing the destination path |
| S3 | Source and destination are the same, or one is inside the other (after canonical paths) | Refuse |
| S4 | Destination is a volume root, the Windows directory, Program Files, a user profile root or a share root with `/MIR`, `/PURGE` or `/MOVE` | Refuse unless the technician enables the advanced override and confirms |
| S5 | Deletions found by the preview | GUI: show the count, bytes and list, and require the number typed back. CLI: refuse unless `--max-delete` covers the count (a number or a percentage of destination files) |
| S6 | Deletions exceed 10% of destination files in an unattended run | Refuse (protects scheduled mirrors from an empty or wrong source) unless `--max-delete` says otherwise |
| S7 | Junctions or symbolic links inside the destination with `/PURGE` or `/MIR` (Robocopy follows them when purging; `/XJ` protects only the source) | List them; refuse until each is excluded with `/XD` (protection confirmed in RC0) |
| S8 | The preview classes many destination files as newer than the source under `/MIR` | Warn: source and destination may be swapped |
| S9 | Bytes to copy exceed destination free space | Warn; suggest `/LFSM` where compatible |
| S10 | Source is a volume root | Suggest `/A-:SH` (G14) |
| S11 | `/M` clears the Archive attribute on the source | State it in the preview |
| S12 | `/MOV` or `/MOVE` | Preview lists what will be deleted from the source; confirmation as S5 |
| S13 | Job marked as containing CUI and an SMB endpoint is not encrypted, or a removable destination is not BitLocker-protected | Warn and require an acknowledgment that is written to the record (section 12) |

Every guard decision, including overrides and typed confirmations, goes into the run record.

## 8. Credentials

Robocopy has no credential option (G3). The tool offers three modes per job:

1. Current user (default). Kerberos or NTLM single sign-on, nothing to enter.
2. Connect as, per share. The technician enters the account name; the password is collected by the Windows
   credential dialog (`CredUIPromptForWindowsCredentialsW`) so it never enters the WebView or a Go string. The tool
   calls `WNetAddConnection2W` with a deviceless connection to the share and `CONNECT_TEMPORARY` (no drive letter, not
   restored at logon), zeroes the buffers, and after the run removes the connection with `WNetCancelConnection2W`.
   It removes only connections it created; their remote names (never credentials) are listed in a local file so a
   crash is cleaned up at next start.
3. Network-only logon. For jobs where both shares need the same other account, Robocopy is started with
   `CreateProcessWithLogonW` and `LOGON_NETCREDENTIALS_ONLY`, as `runas /netonly` does. Whether the child can still be
   held in the tool's job object and measured is an RC0 question; if not, this mode is dropped.

Error 1219 (credentials already in use for that server) gets an explanation and an offer to reach the server by
another name (FQDN or IP address), not a raw code. Error 1326 says the credentials were refused, without saying
which part was wrong.

The CLI asks for passwords only through the Windows credential dialog or an echo-off console prompt; there is no
password flag or environment variable. Unattended runs use the account they run as (for example a gMSA in a
scheduled task). Exported scripts use `net use \\server\share /user:DOMAIN\name *`, which prompts, and delete the
connection afterwards.

## 9. Interface

### 9.1 CLI

```
ztg-copy flags [--search text]                       # the catalog, explained
ztg-copy explain "robocopy C:\data \\nas\data /MIR"  # import a command and explain each option and its risks
ztg-copy plan job.json                               # probes and preview; changes nothing
ztg-copy run job.json --report .\runs                # preview, guards, copy, record
ztg-copy run job.json --max-delete 50 --verify sample
ztg-copy verify <run folder> --hash full
ztg-copy report <run folder> --html report.html
ztg-copy version
```

Jobs are JSON files the app also reads and writes. `/MON` and `/MOT` (which keep Robocopy running) are available
only through the CLI and exports. Template commands are in section 9.3.

### 9.2 Desktop app

- Design: source and destination (folder browser, UNC entry, recent paths, "Connect as" per share), file filters,
  presets, option groups with search. Selecting an option shows its help, risk tags, conflicts, implied options and
  whether this machine supports it. A live command preview (cmd or PowerShell quoting) with copy to clipboard, a
  validation panel (errors block the run), and an "Explain a command" box that imports a pasted command line or an
  `.RCJ` file.
- Preview: totals to copy, skip and delete; the largest files and folders; the deletions list; guard results; SMB
  encryption and signing per share; free space.
- Run: overall progress (bytes and files), current, average and peak speed, files per second, elapsed, ETA, a
  throughput chart, active files, retry state, live errors with causes, and Stop.
- History: past runs with outcome and speed, open the report, compare two runs of the same job, run again.
- Presets, each with the reason for every option: Copy tree (safe), Mirror (destructive), File server migration with
  permissions (`/E /COPYALL /DCOPY:DAT /B /MT:16 /R:2 /W:5 /XJ`), NAS target (`/FFT /DST /XJ`), User profile move,
  Move to archive by age, Throttled over WAN (`/IORATE` where detected, else `/IPG` without `/MT`).
- Templates: a library page and a quick-launch list (section 9.3).
- Exports: `.cmd` and `.ps1` scripts with exit-code handling (0 to 7 success), an `.RCJ` job file, the job JSON.
- Elevation: the app runs as invoker; options that need elevation offer "Restart as administrator", which keeps the
  job.
- Same model as ZTG Scanner: bound methods with no Wails imports, tested on any platform; plain HTML, CSS and
  JavaScript; file names are untrusted text inserted with `textContent` only; a Content-Security-Policy with no
  inline script; the ZTG palette from `design-tokens.json`, added to `scripts/ci/check-palette.mjs`.

### 9.3 Templates

A template is a saved command a technician can reuse in a few clicks: a name, a description, tags, the options, and
source, destination and other values that are either fixed or left as placeholders to fill in at use.

- Saving: from the designer ("Save as template"), from the history page (a past run's job), or by pasting a Robocopy
  command string, which goes through the same import and explain step as section 9.2, so unknown options are flagged
  before it is saved.
- Placeholders: `{source}`, `{destination}` and named text, path or number fields the template defines (for example
  `{client}` or `{share}`), plus built-ins filled automatically: `{date}` (`YYYY-MM-DD`), `{time}`, `{computer}` and
  `{user}`. A field can carry a default, a description and, for paths, the last values used. Placeholders are
  substituted into individual arguments, never into a command string, and each argument is validated after
  substitution, so a value cannot add options or break quoting.
- Quick use: pinned templates appear on the start page and in a search box (type part of the name or a tag); choosing
  one asks only for its empty placeholders, then opens the preview. Every option, guard and confirmation applies as
  for a hand-built job: a template never skips the preview, the deletion confirmation or the `/QUIT` check, and a
  template that contains a destructive option carries a badge in the library.
- Copy as command: any template, filled in, can be copied as a plain Robocopy command line (cmd or PowerShell
  quoting) for use in a script or an RMM tool, with the safe defaults it holds (`/R:2 /W:5`).
- Credentials: a template may name the account for "Connect as" per share, never a password; the password is asked
  for each time (principle 4).
- Built-in presets (section 9.2) are read-only templates; a technician can duplicate and edit them.
- Storage: one JSON file per template in `%APPDATA%\ZTG\Copy\templates\`, with a schema version and the catalog
  version it was written with. On load, options this Windows build does not support, or that the catalog no longer
  knows, are flagged rather than dropped.
- Sharing: export and import of template files, so a team can pass standard commands around. An imported template is
  untrusted: it is shown in full with the explain view and risk tags, and saved only after the technician accepts it.
  A shared read-only team library (a folder on a file share that the app lists beside the local one) is an RC7
  candidate.

CLI:

```
ztg-copy template list [--tag migration]
ztg-copy template show nas-mirror                          # options, placeholders, risk tags
ztg-copy template save nas-mirror --from job.json          # or --command "robocopy ..." to import a string
ztg-copy template run nas-mirror --set source=D:\Shares\Finance --set client=acme --max-delete 0
ztg-copy template command nas-mirror --set source=D:\Shares\Finance   # print the filled Robocopy command line
ztg-copy template export nas-mirror nas-mirror.json
ztg-copy template import nas-mirror.json                   # explains it and asks before saving
```

`template run` refuses to start while a placeholder without a default is unfilled.

## 10. Run record and reports

Each run gets a folder under `%LOCALAPPDATA%\ZTG\Copy\runs\<date>\<run id>\` with an owner-only DACL, holding:

- `run.json`: run id, tool version, Robocopy file version and SHA-256, host, technician (account and SID), elevation,
  the job, the exact argument list, connections (share, account name, SMB dialect, encryption, signing; never a
  secret), preview totals, guard decisions, outcome counts, metrics, a down-sampled timeline, native and normalized
  exit codes, errors by cause, verification result and the native log's SHA-256.
- `events.jsonl`: one line per file event and phase change.
- `robocopy.log`: Robocopy's own Unicode log.
- On request: `report.html` (printable, ZTG palette), `failures.csv`, `manifest.csv` (section 11). CSV cells that
  begin with `=`, `+`, `-`, `@`, tab or carriage return are prefixed with `'`, as in ZTG Scanner.

Implementation note (October 23, 2026): preview/copy/convergence phases now include a bounded
`diagnostics` object in `run.json`. It streams timestamped native Unicode error headers and requires
the decimal and hexadecimal Windows codes to agree, without depending on the localized error label.
Counts include repeated attempts. At most 32 error groups and three 512-character excerpts per group
are saved; omitted groups, malformed/oversized lines and unavailable diagnostics are disclosed.
Activity and History show causes, suggestions, excerpts and per-phase exit bitmap meanings.
Diagnostics remain supplemental to exit/summary correctness. Records without diagnostics still open.

## 11. Verification

Robocopy compares size, timestamps and attributes only (G7). Three levels, chosen per job:

1. Convergence (default after every copy): the job is run again with `/L`; anything left to copy, mismatched or
   extra is reported. Cheap, no data read.
2. Sample hash: SHA-256 of a random sample of copied files (default 1% or 200 files, whichever is larger) on both
   sides.
3. Full hash: SHA-256 of every file copied in the run on both sides, written as `manifest.csv` (path, size, modified
   time, source and destination hash). Reads all data again on both sides; the app states the estimated time before
   starting.

A source file modified after it was copied is reported as "changed at source", not as a copy failure. For jobs that
copy security (`/COPY:S`, `/SEC`, `/COPYALL`), an optional check compares owner and DACL of a sample, and lists SIDs
that do not resolve on the destination (G13).

## 12. Compliance mapping

NIST SP 800-171 Rev 2, as CMMC 2.0 Level 2 uses it. ZTG Copy supports these practices; it is not an assessment
of them, and it does not decide whether data is CUI. A job can be marked "contains CUI" by the technician, which
turns the transport and media warnings in S13 into acknowledgments.

| Practice | How the tool supports it |
| --- | --- |
| AC.L1-3.1.1, AC.L2-3.1.5 | Security-preserving presets; backup mode and auditing copy shown as privileged and recorded when used |
| AU.L2-3.3.1, AU.L2-3.3.2 | Run record tying each copy to the technician's account, host, time, arguments and outcome |
| SC.L2-3.13.8 | SMB encryption shown per share; warning for CUI jobs on unencrypted sessions; `/COMPRESS` labelled as not encryption |
| SC.L2-3.13.11 | Not provided by the tool; the record notes the SMB state so the client's FIPS configuration can be assessed separately |
| MP.L2-3.8.6, MP.L2-3.8.7 | Removable destination detected; BitLocker state shown; acknowledgment recorded for CUI jobs |
| MP.L2-3.8.9 | Mirror targets keep source ACLs with the migration preset; destination ACL comparison in verification |
| IA.L2-3.5.10 | Passwords never on command lines, in files or in logs; temporary connections removed after the run |
| CM.L2-3.4.6 | The copy engine is the in-box Robocopy; the tool adds one signed ZTG binary |

## 13. Testing

- Catalog: every option in `flags.md` is in the catalog and the reverse; conflicts are symmetric; implications and
  requirements resolve; every preset passes validation.
- Command line: argument rendering golden tests, including paths with spaces, trailing backslashes, volume roots,
  Unicode and long paths; a round trip property (render, tokenize, compare); fuzzing of the tokenizer and the
  `.RCJ` reader.
- Parser: golden fixtures captured in RC0 from each supported Windows release, with and without `/MT`, `/UNICODE`
  and `/BYTES`, plus localized fixtures (German, French, Spanish, Japanese at least); fuzzing with arbitrary file
  names in the output.
- Metrics: replayed fixtures under a fake clock; ETA error on recorded jobs; stall and retry detection.
- Guards: each S1 to S13 case with a fake file system.
- Templates: substitution of every placeholder kind; values containing quotes, spaces, `/` options and trailing
  backslashes stay one argument; a template run still triggers the preview and guards; an imported template with an
  unknown or unsupported option is flagged; a template file containing a password field is rejected; fuzzing of the
  template reader.
- Windows integration (`windows-2025` runner, real Robocopy): copy, mirror with deletions, `/MT`, a locked file with
  `/R:0` (exit 8), a junction loop with `/XJ`, paths over 260 characters, Unicode names, preview totals equal to
  copy totals, every exit code bit reproduced, a tampered byte caught by hash verification, empty-source mirror
  refused.
- SMB on the runner: a local share and local account (`New-SmbShare`), "Connect as" through `WNetAddConnection2W`,
  error 1219 reproduced, connections gone after the run.
- Desktop UI: headless Chromium against a binding mock, as in ZTG Scanner, including an HTML injection attempt
  through a file name.

## 14. Slices (build action plan)

Each slice is reviewed before the next starts. Sizes are relative (S small, M medium, L large).

| Slice | What | Done when | Size |
| --- | --- | --- | --- |
| RC0 | Spike on real Windows (10 22H2, 11 24H2 and 25H2, Server 2016, 2019, 2022, 2025): capture output fixtures; answer the open technical questions below; record decisions in this file | Fixtures committed under `testdata/`; each question answered | S |
| RC1 | Engine and CLI: catalog with its `flags.md` check, job model, templates with placeholders and the `template` commands, argument rendering, import and explain, probes, runner in a job object, parser, metrics, run record, normalized exit codes; CI workflow `copy.yml` (gofmt, vet for Linux and Windows, race tests, fuzz seeds, Windows integration job) | `plan`, `run`, `explain`, `flags` work against real Robocopy on the Windows runner; ETA within 15% on the fixture jobs after the first minute | L |
| RC2 | Safety: guards S1 to S12, previews for every destructive option, `--max-delete`, junction listing | Every guard case tested; no destructive run without a preview | M |
| RC3 | Credentials and network context: "Connect as", network-only logon (if RC0 allows), cleanup, 1219 and 1326 handling, SMB dialect, encryption and signing, BitLocker state, guard S13 | SMB tests pass on the runner; no credential reachable from any file, log or export (tested) | M |
| RC4 | Desktop app: design, preview, run dashboard, history, template library and quick launch, presets, exports, elevation restart; palette check | UI tests pass; owner tests on real Windows | L |
| RC5 | Verification and reports: convergence, sample and full hash, ACL comparison, HTML and CSV reports, run comparison | A tampered file and a changed-at-source file are told apart in tests | M |
| RC6 | Packaging and trust: Authenticode through `scripts/signing/`, MSI, portable ZIP, SBOM, `release-copy.yml` with provenance; reuses Scanner's S4 work (roadmap SC1) and TFTP's T5 | A signed release published by the workflow | M |
| RC7 | Candidates needing an owner decision: migration mode (planned pre-seed, delta and cutover passes with the delta trend and a cutover estimate); a shared read-only team template library on a file share; a job queue; Task Scheduler export with gMSA guidance; a Volume Shadow Copy source for locked files (needs administrator rights) | Per candidate | |

RC0 questions:

1. With and without `/MT`, when is a file line printed (start or finish), and how are per-file percentages written?
2. Is `/UNICODE` console output UTF-16LE when redirected to a pipe, and does it keep names that `/UNILOG` keeps?
3. Do `GetProcessIoCounters` follow the copy for local, SMB, offloaded and block-cloned copies?
4. Does `/QUIT` reliably reject an invalid option with exit code 16?
5. Do option tokens in `robocopy /?` match the documented set on each release?
6. Does Go's argument quoting survive Robocopy's parser for trailing backslashes, volume roots and quotes in names?
7. Does `/XD <path>` stop `/PURGE` from following a junction in the destination?
8. Can a process started with `CreateProcessWithLogonW` be kept in the tool's job object and measured?
9. How do deviceless `CONNECT_TEMPORARY` connections behave with error 1219 and on `WNetCancelConnection2W`?
10. How is `robocopy.exe` signed (catalog or embedded), and is checking it worth the code?

## 15. Not in scope

- An own copy engine (section 3).
- Cloud and non-SMB targets (SharePoint, OneDrive, S3, SFTP). Robocopy does not support them.
- Storing credentials in any form, including Windows Credential Manager.
- Telemetry or cloud reporting.
- A resident service. Scheduling, if wanted, goes through Task Scheduler (RC7).
- Platforms other than Windows. The CLI builds on Linux only so unit tests run there.
- Deciding what data is CUI.

## 16. Owner decisions

1. Name: resolved October 22, 2026. "ZTG Copy", folder `copy/`, binary `ztg-copy`. The product name avoids
   Microsoft's tool name; the documentation still says plainly that it runs Robocopy.
2. Resolved October 22, 2026: wrap the in-box Robocopy.
3. Resolved October 22, 2026: default retries `/R:2 /W:5`.
4. Resolved October 22, 2026: normalized CLI exit codes by default, with `--native-exit` as the option.
5. Resolved October 22, 2026: preview before every run.
6. Resolved October 22, 2026: convergence after every copy; hashing opt-in.
7. Resolved October 22, 2026: local per-user run records under `%LOCALAPPDATA%\\ZTG\\Copy\\runs`, kept until manually deleted.
8. Supported Windows releases: Windows 10 22H2 (where under extended security updates), Windows 11, and Server 2016
   onward is proposed, with option detection covering the differences.
9. Which RC7 candidates, if any, are wanted.
10. Resolved October 22, 2026: per-user templates in `%APPDATA%\\ZTG\\Copy\\templates`.


## 17. BITS command design (owner request, October 23, 2026)

The owner requested BITS Transfer options built like Robocopy command strings.
This adds a separate PowerShell Start-BitsTransfer designer in the existing desktop
app and CLI. It is a command-generation feature; the existing Robocopy execution
policy remains unchanged. BITS HTTP/HTTPS command design is the owner-requested
extension to the original Robocopy-only scope.

Use a separate schema-1 document with engine "bits", ordered file pairs and typed
named parameters. Unknown imported parameters remain visible and block generation.
Do not reinterpret BITS options as Robocopy switches. Validate enum values, Int32
limits, the 60-second retry interval minimum, proxy Override requirements and
single-file upload semantics. Data values become quoted PowerShell literals;
credential options produce fixed Get-Credential expressions with no stored values.

The UI offers download, background download and upload presets, searchable help,
option editing/removal, live command review, clipboard copy, JSON import/export
and PowerShell script export. Script export uses a UTF-8 BOM for Windows PowerShell
5.1 Unicode handling. Inline headers, stored credentials, notification programs
and certificate validation overrides are not enabled.

There is no BITS run button or claimed transfer result. BITS jobs persist outside
Copy and need a distinct future job-ownership, completion, cancellation and
evidence design before execution can be integrated. In particular, asynchronous
completion cannot be inferred from successful job creation.

Source: [Microsoft Start-BitsTransfer](https://learn.microsoft.com/en-us/powershell/module/bitstransfer/start-bitstransfer).
Validation includes native Windows PowerShell binding with WhatIf, literal-value
and injection checks, strict JSON/credential refusal, and both browser themes.
