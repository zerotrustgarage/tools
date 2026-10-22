# ZTG Copy

A Windows Robocopy job designer, runner and evidence recorder. The initial CLI implements
ordinary local copies, a searchable 97-option reference, command import/explanation and
per-user templates. Development follows [docs/design.md](docs/design.md); RC1 is in progress.

## Build and use the CLI

With Go 1.26.10 on Windows:

```powershell
cd copy
go build -trimpath -o dist/ztg-copy.exe ./cmd/ztg-copy
./dist/ztg-copy.exe flags --search encryption
./dist/ztg-copy.exe explain 'robocopy "C:\data space" "D:\backup" /MIR /R:2'
./dist/ztg-copy.exe import --output my-job.json 'robocopy "C:\data space" "D:\backup" /E /MT:8'
./dist/ztg-copy.exe validate my-job.json
./dist/ztg-copy.exe plan my-job.json
./dist/ztg-copy.exe run my-job.json
./dist/ztg-copy.exe check-updates --json
```

Both copy directories must already exist. The runner resolves the inbox engine through
GetSystemDirectoryW, validates with /QUIT, runs a list-only preview, copies, then runs a
serial list-only convergence check. The serial verification avoids /MT directory totals that count traversal even on a native-zero pass. Absent defaults are /E /DCOPY:DAT /R:2 /W:5 /XJ. An explicit /S
keeps its empty-directory behavior. Flags go before positional filenames in CLI commands.

Every child starts suspended, is assigned to a kill-on-close job object and has membership
verified before resume. Only its three standard handles are inherited. Ctrl+C cancels the
operation; cancellation remains an error even if the stopped engine returns zero.

The initial runner permits existing local fixed-drive folders, ordinary DAT copying and
the safe option subset enforced in `internal/guard/`. It refuses overlap after path resolution,
volume roots, reparse points in either tree, network endpoints, destructive/privileged/monitor
options, external Robocopy jobs, and user-supplied logging controls. Those switches remain
available in `explain`. These restrictions stay until their later safety/network slices pass.
Long identity paths are kept separate from the original invocation paths.

## Jobs and evidence

A job is a versioned JSON document, as in [examples/copy-tree.job.json](examples/copy-tree.job.json).
Unknown switches remain in imported jobs and appear as errors when validated. Unknown JSON
fields, including password fields, are rejected. No command invokes a shell, expands environment
variables or accepts a password. Printed commands use direct Windows process quoting; they
are not shell scripts.

Records are retained until manually deleted under
`%LOCALAPPDATA%\ZTG\Copy\runs\<date>\<id>\`.
Use `--report DIRECTORY` before the job filename to select another record root. It must be
outside both copy trees, including after resolving existing path aliases. The run folder holds:

- `run.json`: job, engine path/hash, technician/host, guard facts, exact phase arguments, native
  exits, summary totals, log hashes, timings, cancellation and normalized outcome.
- `events.jsonl`: phase transitions.
- Per-phase raw stdout/stderr and native Unicode logs for preview, copy and convergence.

Windows folders use a protected owner-only DACL. Native /UNILOG is authoritative for names
and summaries: redirected /UNICODE console output mixes encodings on tested builds.
Progress currently reports phases and final counts. It does not infer completed bytes or ETA
from file announcements, allocation sizes or process I/O.

Default CLI exits: 0 completed/planned, 1 review or non-convergence, 2 engine failure,
3 tool error/cancellation. `--native-exit` returns the copy bitmap after a completed run
(or preview bitmap for plan); tool errors and cancellation remain 3. A raw Robocopy bitmap
must be interpreted as a bitmap, so 1 does not itself mean failure.

## Templates

Templates live in `%APPDATA%\ZTG\Copy\templates\`, with schema/catalog versions.
[examples/nas-copy.template.json](examples/nas-copy.template.json) shows typed placeholders.
Network templates can be designed and printed now; network execution is a later slice.

```powershell
./dist/ztg-copy.exe template save --from my-job.json local-copy
./dist/ztg-copy.exe template list
./dist/ztg-copy.exe template show local-copy
./dist/ztg-copy.exe template command local-copy
./dist/ztg-copy.exe template run local-copy
./dist/ztg-copy.exe template export local-copy local-copy.template.json
./dist/ztg-copy.exe template import ./examples/nas-copy.template.json
./dist/ztg-copy.exe template import --accept ./examples/nas-copy.template.json
./dist/ztg-copy.exe template command --set 'source=C:\data space' --set client=Acme nas-copy
```

Import displays the full template and explanation, and saves only when repeated with
`--accept`. Importing a command with `template save --command COMMAND` also requires
`--accept`. Existing files are never overwritten. Placeholders are substituted into individual
arguments, with type checks and validation afterward. They cannot add or change an option
token. Unfilled, unknown and invalid fields block resolution. Built-ins are {date}, {time},
{computer} and {user}. Templates never bypass runner checks.

## Validation and remaining slices

CI cross-builds on Linux and runs native tests and synthetic copy experiments on English
Server 2022 and Server 2025. It includes Linux/Windows vet, race tests, catalog/reference
consistency, fuzzing, native Windows argument parsing, owner-only template storage, contained
copy/cancellation, and preview/copy/convergence evidence.

```bash
cd copy
go test -race ./...
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=windows GOARCH=amd64 go build ./cmd/ztg-copy
```

The Windows API dependency is the same pinned x/sys version used by Scanner; the update
checker is vendored from `scripts/updates/` and checked by the shared sync tool.

The desktop app, complete destructive guards, alternate credential UX, network copying,
hash verification, reports, reliable ETA, installers and signing are still in the roadmap.
Other Windows releases, localized logs and optimized/offloaded copy behavior remain release
validation gates. See [docs/rc0.md](docs/rc0.md) for the scoped feasibility review.

## RC0 capture harness

`ztg-copy-spike` is a separate synthetic feasibility collector. It accepts only a new output
folder and creates its own source/destination under TEMP. It never accepts production copy
endpoints or credentials.

```powershell
go build -trimpath -o dist/ztg-copy-spike.exe ./cmd/ztg-copy-spike
./scripts/collect.ps1 -Spike ./dist/ztg-copy-spike.exe -Output ./testdata/captures/my-build
```

The CI-only `scripts/collect-network.ps1` requires an elevated disposable Windows machine.
It creates and removes synthetic users/shares, tests temporary connections, error 1219 and
network-only logon containment. Passwords remain in native buffers and are zeroed. Raw selected
captures and provenance are committed under `internal/spike/testdata/`; CI artifacts expire
after 14 days.
