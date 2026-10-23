# CLAUDE.md (copy)

ZTG Copy, a designer, runner and evidence recorder for Windows Robocopy jobs, in Go. Status: RC0 core evidence reviewed on Server 2022/2025; CLI foundation and initial Windows GUI implemented; remaining acceptance is tracked in docs/rc1.md. The owner also requested a BITS command designer on October 23, 2026. This generates PowerShell commands only and does not enable BITS execution or change the Robocopy runner. See design section 17. The owner requested the GUI next, so it wraps the current restricted runner while later execution modes remain blocked. See docs/rc0.md for the review scope and outstanding release gates. Repository-wide conventions (one folder per tool, GitHub-hosted runners, actions pinned by SHA, dependencies
public for at least 14 days) are in the root README.

Read `docs/design.md` before changing anything here, `docs/research.md` for why, and `docs/flags.md` for the option
reference the catalog is built from. Its principles are requirements:

1. Wrap the in-box `robocopy.exe` from the system directory; never write an own copy engine, never resolve it
   through `PATH`.
2. Every option the builder offers comes from the catalog with help, risk tags and conflicts; unknown options in an
   imported command are flagged, never dropped.
3. `/MIR`, `/PURGE`, `/MOV`, `/MOVE` and `/M` always get a list-only preview and a confirmation; the guards in design
   section 7 refuse the unsafe cases.
4. Credentials are never stored, never on a command line, never in a job, export, log or record, and never in the
   WebView. Passwords come from the Windows credential dialog or an echo-off prompt, live in native buffers and are
   zeroed after use.
5. Outcomes come from the exit code and the positional summary numbers, never from localized text alone.
6. Every run writes a record (design section 10) to an owner-only folder. File names are untrusted text: the UI
   inserts them with `textContent` only and CSV cells are formula-escaped.
7. Safe defaults: `/R:2 /W:5`, `/XJ`, a log on every run; `/REG` is never offered.
8. Never claim a transfer is encrypted or compliant when the tool cannot tell; `/COMPRESS` is not encryption.
9. Local only: no telemetry, no cloud reporting.
10. Templates never hold a password and never skip the preview, guards or confirmations; placeholders are substituted
    into single arguments, never into a command string, and imported templates are explained before they are saved.

Dependencies: standard library, `golang.org/x/sys`, and Wails v2 for the app; justify any other in the commit
message. Writing style: no em dashes. Slices (RC0 to RC7) are built one at a time, each reviewed before the next
starts; owner-selected defaults and the scoped RC0 engineering review are recorded in docs/design.md and docs/rc0.md. Remaining release/locale coverage and support decisions stay explicit release gates.
