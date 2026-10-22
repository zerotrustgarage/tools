# ZTG Copy

A planned Windows Robocopy designer, runner and evidence recorder. The build plan is in
[docs/design.md](docs/design.md). This branch begins RC0; the desktop app and production
runner are not implemented yet.

The first executable is **ztg-copy-spike**, an RC0 evidence collector. It accepts only an
output folder, creates its own synthetic source and destination under the Windows temporary
directory, and never accepts real copy endpoints or credentials. It resolves the in-box
Robocopy with GetSystemDirectoryW, never PATH.

## Run the spike on Windows

With Go 1.26 and PowerShell 7:

```powershell
cd copy
go build -trimpath -o dist/ztg-copy-spike.exe ./cmd/ztg-copy-spike
./scripts/collect.ps1 -Spike ./dist/ztg-copy-spike.exe -Output ./testdata/captures/my-windows-build
```

The output directory must not already exist. Keep each capture separately. The collector
creates:

- Exact stdout and stderr bytes for help, list-only previews, serial and multithreaded
  copies, trailing backslashes in paths with spaces, and valid/invalid /QUIT commands.
- UTF-16LE decode attempts when /UNICODE is requested, with errors recorded. This is an
  experiment: captured console output on Server 2025 mixes encodings despite a BOM.
- A /UNILOG copy with Unicode filename checks against the native log.
- A JSON Lines timeline of pipe chunks (bytes are base64) and process I/O counters.
  Receipt time does not establish when a file completed. I/O counters are not copy totals.
- The native and normalized exit codes, arguments, elapsed time and content/Unicode checks.
- The engine SHA-256, Windows build, locale, file version and observed signature status/type.

Synthetic fixture files are removed afterwards; evidence is preserved. Capture collection
stops if a command takes more than 45 seconds. The CI workflow builds on Linux and executes
the unsigned spike on the GitHub-hosted windows-2025 runner, retaining evidence for 14 days.

## Checks

```bash
cd copy
go test -race ./...
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=windows GOARCH=amd64 go build ./cmd/ztg-copy-spike
```

There are no third-party Go dependencies. Unit tests cover Unicode decoding, help-token
extraction and every native exit bitmap. Raw Windows captures are experimental evidence,
not invented golden fixtures. See [docs/rc0.md](docs/rc0.md) for the remaining questions.
