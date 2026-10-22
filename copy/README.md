# ZTG Copy

Design, run and prove Robocopy jobs. A proposed Windows tool for technicians that wraps the in-box `robocopy.exe`:

- Design: every option explained, with risks, conflicts and what this Windows build supports; source and destination
  pickers; alternate credentials for shares without a password ever reaching a command line; a live command preview;
  "explain this command" for existing scripts; saved templates with fill-in placeholders for commands you run
  often, which can also be copied out as a plain Robocopy command line.
- Run: overall progress, current, average and peak speed, files per second, elapsed time and an ETA for the whole job;
  errors explained as they happen.
- Prove: a preview and confirmation before anything is deleted, a run record per job, optional hash verification and
  printable reports.

Status: proposed, no code yet. The plan is in [docs/design.md](docs/design.md), the research behind it in
[docs/research.md](docs/research.md) and the option reference in [docs/flags.md](docs/flags.md).

Robocopy does not encrypt. Data in transit is protected only by SMB 3 encryption on the connection; the tool shows
that state per share but cannot provide it.
