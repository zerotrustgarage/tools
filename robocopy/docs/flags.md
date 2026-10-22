# ZTG Robocopy: option reference

Status (October 22, 2026): the source for the embedded flag catalog (design section 4). Descriptions follow
Microsoft's reference ([Microsoft Learn](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/robocopy),
updated February 2026); the "Tool" column is ZTG's handling. A CI check (slice RC1) fails when the catalog and this
file disagree.

Tags used in the Tool column:

- destructive: can delete or overwrite data; the app previews first and asks for confirmation (design section 7).
- privileged: needs a Windows privilege the technician may not hold; the app checks before running.
- conflict: cannot be combined with the options named.
- detect: not in every Windows build; offered only when the installed Robocopy lists it (design section 5).
- hidden: never offered in the builder; recognized when importing a command line and explained.

## Syntax

`robocopy <source> <destination> [<file> ...] [<options>]`. `<file>` defaults to `*.*` and accepts `*` and `?`.

## Copy options

| Option | What it does | Tool |
| --- | --- | --- |
| `/S` | Copy subdirectories, excluding empty ones | |
| `/E` | Copy subdirectories, including empty ones | Default for folder copies |
| `/LEV:n` | Copy only the top n levels of the tree | |
| `/Z` | Restartable mode: an interrupted file resumes where it stopped | Slower on fast links; recommended for large files over unreliable links |
| `/B` | Backup mode: overrides ACLs that would block access | privileged (`SeBackupPrivilege`, `SeRestorePrivilege`); needs elevation |
| `/ZB` | Restartable mode, switching to backup mode when access is denied | privileged; SS64 notes checkpointing makes it much slower |
| `/J` | Unbuffered I/O, for large files | Suggested when the average file is large |
| `/EFSRAW` | Copy EFS-encrypted files in raw mode | conflict: `/MT`, `/LFSM` |
| `/COPY:flags` | What to copy for files: D data, A attributes, T timestamps, X skip alternate data streams, S ACLs, O owner, U auditing. Default `DAT` | `S`, `O`, `U` shown with their privilege needs; `U` needs the auditing privilege (`SeSecurityPrivilege`); `X` is ignored with `/B` or `/ZB` |
| `/DCOPY:flags` | What to copy for directories: D data, A attributes, T timestamps, E extended attributes, X skip alternate data streams. Default `DA` | Builder suggests `DAT` so folder dates survive a migration |
| `/SEC` | Same as `/COPY:DATS` | Shown as a shortcut that sets the `/COPY` boxes |
| `/COPYALL` | Same as `/COPY:DATSOU` | Shortcut; privileged for `U` and often `O` |
| `/NOCOPY` | Copy no file information (for use with `/PURGE`) | destructive in combination |
| `/SECFIX` | Fix file security on all files, including skipped ones | Requires one of `/COPYALL`, `/COPY:O`, `/COPY:S`, `/COPY:U`, `/SEC`; validator enforces it |
| `/TIMFIX` | Fix file times on all files, including skipped ones | |
| `/PURGE` | Delete destination files and directories that no longer exist in the source | destructive; follows junctions in the destination (SS64) |
| `/MIR` | Mirror a tree: `/E` plus `/PURGE`; overwrites destination directory security | destructive |
| `/MOV` | Move files: delete from the source after copying | destructive (source) |
| `/MOVE` | Move files and directories | destructive (source) |
| `/A+:[RASHCNET]` | Add attributes to copied files | |
| `/A-:[RASHCNETO]` | Remove attributes from copied files | Suggested as `/A-:SH` when the source is a volume root (research G14) |
| `/CREATE` | Create the directory tree and zero-length files only | |
| `/FAT` | Create destination files with 8.3 names only | Warns: rarely wanted |
| `/256` | Turn off support for paths longer than 256 characters | Warns: rarely wanted |
| `/MON:n` | Monitor the source; run again after more than n changes | Keeps the process running; offered only in the CLI and export (design section 9) |
| `/MOT:m` | Monitor the source; run again in m minutes if changed | Same as `/MON` |
| `/RH:hhmm-hhmm` | Run hours: new copies start only inside the window | |
| `/PF` | Check run hours per file rather than per pass | |
| `/IPG:n` | Inter-packet gap in milliseconds to free bandwidth | conflict: `/MT`; builder prefers `/IORATE` where detected |
| `/SJ` | Copy junctions as junctions instead of their targets | |
| `/SL` | Copy symbolic links as links instead of their targets | |
| `/MT[:n]` | Multithreaded copy, 1 to 128 threads, default 8 | conflict: `/IPG`, `/EFSRAW`, `/LFSM`; builder pairs it with a log file |
| `/NODCOPY` | Copy no directory information | |
| `/NOOFFLOAD` | Do not use Windows copy offload | |
| `/COMPRESS` | Request SMB network compression where applicable | detect (Windows 10 and Server 2019 onward, per Wikipedia); the app states it is not encryption |
| `/SPARSE:y|n` | Keep or drop the sparse state of files; default yes | detect |
| `/NOCLONE` | Do not attempt block cloning | detect |

## Throttling options

| Option | What it does | Tool |
| --- | --- | --- |
| `/IOMAXSIZE:n[kmg]` | Requested maximum I/O size per read or write | detect (Computer Hope says Windows 11; Microsoft states no version) |
| `/IORATE:n[kmg]` | Requested I/O rate per second; the minimum throttled rate is 524,288 bytes per second | detect; the builder shows rates in Mbit/s and MB/s |
| `/THRESHOLD:n[kmg]` | Files smaller than this are not throttled | detect |

## File selection options

| Option | What it does | Tool |
| --- | --- | --- |
| `/A` | Copy only files with the Archive attribute | |
| `/M` | Copy only files with the Archive attribute and reset it | Changes the source (clears Archive); stated in the preview |
| `/IA:[RASHCNETO]` | Include only files with any of these attributes | |
| `/XA:[RASHCNETO]` | Exclude files with any of these attributes | |
| `/XF name ...` | Exclude files by name or path; wildcards allowed | List editor |
| `/XD dir ...` | Exclude directories by name or path | List editor; profile preset adds common caches |
| `/XC` | Exclude changed files (same timestamp, different size) | |
| `/XN` | Exclude source files newer than the destination | |
| `/XO` | Exclude source files older than the destination | Suggested for "never overwrite newer" copies |
| `/XX` | Exclude extra files and directories (prevents deletions by `/PURGE` or `/MIR`) | Shown as the brake on `/MIR` |
| `/XL` | Exclude lonely files and directories (nothing new is added) | |
| `/IM` | Include modified files (differing change times) | |
| `/IS` | Include same files (forces recopy) | |
| `/IT` | Include tweaked files (same size and times, different attributes) | |
| `/MAX:n` | Exclude files larger than n bytes | Size field with units |
| `/MIN:n` | Exclude files smaller than n bytes | Size field with units |
| `/MAXAGE:n` | Exclude files last modified more than n days ago or before a date (YYYYMMDD) | Date picker or days |
| `/MINAGE:n` | Exclude files last modified less than n days ago or after a date | Date picker or days |
| `/MAXLAD:n` | Exclude files not accessed since n | Warns: last-access updates are often disabled on NTFS |
| `/MINLAD:n` | Exclude files accessed since n | Same warning |
| `/XJ` | Exclude junction points | Default on in the builder (research G11) |
| `/XJD` | Exclude directory junction points | |
| `/XJF` | Exclude file junction points | |
| `/FFT` | Assume FAT file times (two-second precision) | Suggested automatically when either side is FAT, exFAT or a non-Windows SMB server (design section 5) |
| `/DST` | Compensate for one-hour DST differences | Suggested with `/FFT` for FAT targets |

## Retry options

| Option | What it does | Tool |
| --- | --- | --- |
| `/R:n` | Retries on failed copies; default 1,000,000 | Builder default `/R:2` |
| `/W:n` | Wait between retries in seconds; default 30 | Builder default `/W:5` |
| `/REG` | Save `/R` and `/W` as the user's registry defaults | hidden: silently changes every later Robocopy run for that user |
| `/TBD` | Wait for share names to be defined (retry error 67) | |
| `/LFSM[:n[kmg]]` | Low free space mode: pause when the destination would drop below a floor (default 10% of the volume) | conflict: `/MT`, `/EFSRAW`, and per SS64 `/B`, `/ZB` |

## Logging options

The app reads Robocopy's console output itself (design section 6), so most logging options are set by the runner,
not by the technician. They are still explained, because imported commands use them.

| Option | What it does | Tool |
| --- | --- | --- |
| `/L` | List only: nothing is copied, deleted or timestamped | Used for every preview |
| `/X` | Report all extra files | Used by the preview when deletions are possible |
| `/V` | Verbose; show skipped files | Optional |
| `/TS` | Include source timestamps | Runner sets as needed |
| `/FP` | Full path names | Runner sets |
| `/BYTES` | Sizes in bytes | Runner sets |
| `/NS`, `/NC`, `/NFL`, `/NDL` | Omit sizes, classes, file names, directory names | Runner controls; the app warns that a run without file names cannot be verified or reported per file |
| `/NP` | No per-file progress percentage | Ignored with `/MT` (Wikipedia) |
| `/ETA` | Per-file estimated time of arrival | Superseded by the app's overall ETA |
| `/LOG:file`, `/LOG+:file` | Write output to a log file (overwrite or append) | The app keeps Robocopy's native log beside its own run record |
| `/UNILOG:file`, `/UNILOG+:file` | Same, as Unicode | Preferred, so non-ASCII names survive |
| `/TEE` | Write to the console and the log | Runner sets when a native log is kept |
| `/NJH`, `/NJS` | No job header, no job summary | Runner keeps both |
| `/UNICODE` | Unicode console output | Decided in slice RC0 |

## Job options

| Option | What it does | Tool |
| --- | --- | --- |
| `/JOB:name` | Load parameters from a job file | Import reads `.RCJ` files |
| `/SAVE:name` | Save parameters to a job file | Export writes `.RCJ` files |
| `/QUIT` | Process the command line and quit | Used to validate a command against the installed Robocopy |
| `/NOSD`, `/NODD` | No source or destination in the job file | Template jobs |
| `/IF` | Include the following files | |

## Not documented by Microsoft

Recognized when importing a command and explained, never offered: `/DEBUG` (listed as undocumented by
[SS64](https://ss64.com/nt/robocopy.html)). Anything else unknown is flagged as "not recognized by this catalog".
