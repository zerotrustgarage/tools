# ZTG Copy: research

Status (October 22, 2026): research for the design in [design.md](design.md). The full option reference is in
[flags.md](flags.md). Facts are cited; statements marked "assessment" are ZTG's reading and should be rechecked on a
real Windows build before they are repeated to a client.

## 1. Why technicians use Robocopy

Robocopy ("Robust File Copy") is the copy engine built into every supported Windows release. Kevin Allen wrote it for
the Windows NT 4.0 Resource Kit in 1996; it has shipped in Windows since Vista and Server 2008
([Wikipedia](https://en.wikipedia.org/wiki/Robocopy)). It is the default answer for MSP file work because it is
already on the machine, Microsoft-signed, scriptable, and understands NTFS security:

| Job | Typical options | Why Robocopy |
| --- | --- | --- |
| File server migration to a new server or NAS | `/E /COPYALL /DCOPY:DAT /B /MT`, repeated delta passes, final `/MIR` at cutover | Keeps ACLs, owners and timestamps; restartable; only changed files move on later passes |
| User profile or home folder moves | `/E /XJ /XD AppData\Local\Temp` | Skips junction loops; preserves attributes |
| Scheduled mirror to a backup target | `/MIR /R:2 /W:5 /LOG+:` | In-box; exit code drives scripts |
| Moving or archiving old data | `/MOV /MINAGE:365` | Selection by age, size, attributes |
| Seeding a new share over a WAN | `/IPG` or `/IORATE`, `/COMPRESS`, `/RH` | Throttling and run-hour windows |
| Fixing permissions on an existing copy | `/E /COPY:S /SECFIX /IS` or `/SECFIX` with a mirror | Refreshes ACLs on files that did not change |

## 2. Capabilities

Summarized from Microsoft's reference ([Microsoft Learn](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/robocopy),
updated February 2026); every option is in [flags.md](flags.md).

- Copy what: file data, attributes, timestamps, NTFS ACLs, owner and auditing information (`/COPY:DATSOU`), directory
  timestamps and extended attributes (`/DCOPY`), alternate data streams (skip with `X`), sparse state (`/SPARSE`).
- Copy how: restartable mode (`/Z`), backup mode that bypasses ACLs with `SeBackupPrivilege` (`/B`, `/ZB`), unbuffered
  I/O for large files (`/J`), EFS raw copies (`/EFSRAW`), 1 to 128 threads (`/MT`, default 8 when given), SMB
  compression (`/COMPRESS`), Windows copy offload and block cloning (on by default, off with `/NOOFFLOAD`,
  `/NOCLONE`), symbolic links and junctions as links (`/SL`, `/SJ`).
- Select what: wildcards, include and exclude by name, directory, attribute, size, modified age and last-access age;
  classification of each file against the destination (new, newer, older, changed, tweaked, same, extra, lonely,
  modified with `/IM`).
- Synchronize: `/PURGE` deletes destination items missing from the source; `/MIR` is `/E` plus `/PURGE`; `/MOV` and
  `/MOVE` delete from the source after copying.
- Run control: retries and wait (`/R`, `/W`), wait for a share to appear (`/TBD`), low free space mode (`/LFSM`), run
  hours (`/RH`, `/PF`), monitor and re-run (`/MON`, `/MOT`), bandwidth throttling (`/IPG`, `/IORATE`, `/IOMAXSIZE`,
  `/THRESHOLD`).
- Report: list-only dry run (`/L`), verbose and skipped files (`/V`, `/X`), sizes in bytes, full paths, timestamps,
  per-file ETA (`/ETA`), ANSI or Unicode log files with overwrite or append, console plus log (`/TEE`), a job header
  and a summary table with totals and speed.
- Reuse: save and load parameter sets as job files (`/SAVE`, `/JOB`).
- Exit code: a bitmap. 1 files copied, 2 extra files or directories in the destination, 4 mismatches, 8 copy failures
  after retries, 16 a serious error with nothing copied. 0 to 7 is success and 8 or more is a failure
  ([Microsoft Learn](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/robocopy),
  [SS64](https://ss64.com/nt/robocopy-exit.html); Microsoft's current table lists values through 8 only, and 16 comes
  from SS64 and older Microsoft documentation).

Version history worth knowing: `/MT` arrived with Windows 7 and Server 2008 R2; `/COMPRESS` with Windows 10 and
Server 2019 ([Wikipedia](https://en.wikipedia.org/wiki/Robocopy)). Computer Hope states the throttling options need
Windows 11 ([Computer Hope](https://www.computerhope.com/robocopy.htm)); Microsoft gives no version for them,
`/SPARSE` or `/NOCLONE`. Robocopy has no version switch, and its help text differs between builds, so the tool must
detect what the installed copy supports rather than assume (design section 5).

## 3. Shortcomings

Each row is a gap the tool can close. Items marked "assessment" are ZTG's reading; the rest are cited.

| # | Shortcoming | Consequence | Source |
| --- | --- | --- | --- |
| G1 | Terse help, about 80 options with subtle interactions (`/MIR` versus `/E /PURGE` and destination security, `/MOV` versus `/MOVE`, `/XX` overriding `/PURGE`, `/SECFIX` needing a security flag, `/LFSM` incompatible with `/MT`) | Commands are copied from forums and old scripts without being understood | Microsoft Learn; assessment |
| G2 | Default retries are one million with a 30 second wait | One locked file can stall an unattended job for about a year; `/REG` can make other values the user's default silently | Microsoft Learn |
| G3 | No credential parameter. Robocopy runs as the caller's token | Technicians fall back to `net use \\server\share password /user:...`, which puts the password on a command line (process list, shell history, RMM script logs) and can leave a persistent mapping behind | Assessment from Microsoft Learn (no such option) |
| G4 | No overall progress or ETA. `/ETA` is per file; the summary only appears at the end; with `/MT`, `/NP` is ignored | Nobody can say when a 4 TB migration will finish, or whether it is stalled | Microsoft Learn, [Wikipedia](https://en.wikipedia.org/wiki/Robocopy) |
| G5 | Output is human text, localized, with no machine-readable format | Scripts that parse "Files :" lines break on a German or French server; RMM tools cannot ingest results | Assessment |
| G6 | Exit codes are a bitmap where 1 means success | RMM tools and scheduled tasks that treat any non-zero code as failure raise false alarms; scripts that test `== 0` miss real failures. SS64 reports XP026 returns success even on failure | [SS64](https://ss64.com/nt/robocopy-exit.html) |
| G7 | No content verification. Files are compared by size, timestamps and attributes only | A copy cannot be proven identical, which matters for migrations and evidence | Assessment from Microsoft Learn (no hash option) |
| G8 | `/MIR` and `/PURGE` delete without asking. A wrong destination, swapped source and destination, or an empty or unreachable source wipes data. `/PURGE` follows a junction in the destination and deletes its target's contents | The classic Robocopy data loss incident | [Wikipedia](https://en.wikipedia.org/wiki/Robocopy), [SS64](https://ss64.com/nt/robocopy.html) |
| G9 | Quoted paths ending in a backslash: `"C:\some path\"` escapes the closing quote | The destination silently becomes something else, or arguments shift | [SS64](https://ss64.com/nt/robocopy.html) |
| G10 | Open or locked files are not copied; no Volume Shadow Copy support. `/B` bypasses ACLs, not locks | Live data needs a shadow copy first; failures show up only at the end | [Wikipedia](https://en.wikipedia.org/wiki/Robocopy) |
| G11 | Junction loops (for example `Application Data` inside profiles) without `/XJ` | Endless recursion, path-too-long failures | [Wikipedia](https://en.wikipedia.org/wiki/Robocopy) |
| G12 | Timestamp granularity on FAT, exFAT and many NAS file systems, and DST shifts | Every file looks changed and is recopied on each pass unless `/FFT` or `/DST` is used | Microsoft Learn, SS64 |
| G13 | ACL changes on unchanged files are not copied without `/SECFIX`; copying security across domains leaves unresolved SIDs; `/COPY:U` needs the auditing privilege | Permissions on the destination drift from the source without anyone noticing | SS64; assessment |
| G14 | Copying from a volume root gives the destination folder Hidden and System attributes | The copy "disappears" in Explorer | Microsoft Learn |
| G15 | Interrupted files are left with a 1970-01-01 timestamp | A half-copied file looks like a real one until the next pass | [Wikipedia](https://en.wikipedia.org/wiki/Robocopy) |
| G16 | Logging slows `/MT` runs; console output is noisy; logs without `/NP` fill with percentage lines | Technicians turn logging down and lose evidence | Microsoft Learn, SS64 |
| G17 | Error messages are raw Win32 codes (5, 32, 53, 64, 67, 112, 1219, 1326) | The technician has to look each one up | Assessment |
| G18 | No insight into transport security: Robocopy does not report whether the SMB session is encrypted or signed, and `/COMPRESS` is not encryption | A CUI migration over an unencrypted SMB session goes unnoticed | Assessment |

## 4. Existing front ends and alternatives

| Tool | What it is | Gaps for ZTG (assessment unless cited) |
| --- | --- | --- |
| Robocopy GUI (Derk Benisch, Microsoft TechNet, 2005 to 2008) | The original Microsoft-published front end, bundled XP026 | No longer offered by Microsoft; .NET 2.0; predates `/MT` and every newer option ([Wikipedia](https://en.wikipedia.org/wiki/Robocopy)) |
| RoboCopy GUI (commercial) | Job queue, presets, live progress, import of existing command lines | Paid; scheduling and notifications only in a business edition; closed source, so ZTG cannot vouch for it to an assessor ([Capterra](https://www.capterra.com/p/10016130/RoboCopy-GUI/), [GetApp](https://www.getapp.com/security-software/a/robocopy-gui/)) |
| WinRoboCopy, ChoEazyCopy and similar community GUIs | Free option builders | Mostly checkbox forms over the command line; little help on what each option does, no overall ETA, no credentials handling, no verification ([AlternativeTo](https://alternativeto.net/software/robocopy-gui-1)) |
| TeraCopy, FastCopy | Their own copy engines with checksum verification | Not Robocopy: no `/COPYALL` style security copy in the same form, no mirror semantics technicians already script against; freemium and closed source ([AlternativeTo](https://www.alternativeto.net/software/teracopy/), [AlternativeTo](https://www.alternativeto.net/software/fastcopy/)) |
| FreeFileSync | Open source, cross-platform synchronization | Its own engine; general sync tool, not a Robocopy designer; NTFS security copy is not its focus ([AlternativeTo](https://www.alternativeto.net/software/teracopy/)) |
| Windows Storage Migration Service | Server-to-server migration orchestrated from Windows Admin Center | Heavyweight, server-only, needs WAC; not for ad hoc copies or NAS targets (assessment) |

The field splits in two: thin GUIs over Robocopy that add little understanding, and replacement engines that lose
what makes Robocopy the expected tool. Nothing combines an explained option designer, safe handling of credentials,
an honest overall ETA, verification and an evidence-grade record of the run.

## 5. Credentials: what Windows offers

Robocopy inherits the security context of its process. A technician who must reach a share with other credentials
has these choices:

| Method | Behaviour | Fit |
| --- | --- | --- |
| `net use \\server\share pass /user:...` | Password on the command line; persistent unless `/persistent:no` | Avoid (G3) |
| `net use ... *` | Prompts for the password on the console | Acceptable for exported scripts |
| `WNetAddConnection2W` with a deviceless connection and `CONNECT_TEMPORARY` | The program passes the credential in memory; no drive letter; not restored at logon; removed with `WNetCancelConnection2W` ([Microsoft Learn](https://learn.microsoft.com/en-us/windows/win32/api/winnetwk/nf-winnetwk-wnetaddconnection2w)) | Preferred for the app |
| `CreateProcessWithLogonW` with `LOGON_NETCREDENTIALS_ONLY` (what `runas /netonly` does) | The child process uses the alternate credential for every network connection; local access stays as the caller | Useful when both ends need the same alternate account |
| Credential Manager saved credentials | Stored on disk under the user profile | Out of scope: the tool stores no credentials |

Windows allows one set of credentials per server per logon session; a second connection to the same server with
different credentials fails with error 1219 (`ERROR_SESSION_CREDENTIAL_CONFLICT`). The usual workaround is to reach
the server by another name (FQDN, IP address or a DNS alias). The tool must explain this rather than fail with the
code (assessment from field experience; confirm in slice RC0).

## 6. Compliance context

NIST SP 800-171 Rev 2, as a CMMC 2.0 Level 2 assessment uses it (Rev 3 renumbers these as 03.xx.xx and is not the
basis for CMMC assessments today). Level 1 practices are noted where they apply.

| Practice | Relevance |
| --- | --- |
| AC.L1-3.1.1, AC.L2-3.1.5 | Copying with `/COPYALL` or `/SEC` keeps access control on migrated data; backup mode (`/B`) bypasses ACLs and needs `SeBackupPrivilege`, so it should be explicit, visible and used by the least privileged account that works |
| AU.L2-3.3.1, AU.L2-3.3.2 | A record of who ran which copy, with which options, from where to where, and what happened. Robocopy's own log has no user or host identity in a structured form |
| SC.L2-3.13.8 | CUI in transit needs cryptographic protection. Robocopy does not encrypt; protection comes from SMB 3 encryption on the share or the session. `/COMPRESS` is not encryption |
| SC.L2-3.13.11 | Whether SMB encryption uses FIPS-validated cryptography depends on Windows and its FIPS configuration, not on Robocopy |
| MP.L2-3.8.6, MP.L2-3.8.7 | Copies to removable media: CUI must be encrypted on it (BitLocker To Go) and removable media use must be controlled |
| MP.L2-3.8.9 | Backup copies of CUI need the same confidentiality protection; a mirror target's ACLs matter |
| IA.L2-3.5.3, IA.L2-3.5.10 | Passwords must be protected in storage and transmission; a password typed on a command line is neither. How an SMB logon fits the client's MFA design is a question for their SSP, not something the tool can satisfy |
| CM.L2-3.4.6 | Robocopy is in-box, so using it adds no new software to a baseline; a signed ZTG front end is one known addition |

File names are not usually CUI, but in some programs they are descriptive enough to be sensitive. Logs and run
records that list them need owner-only permissions and must stay local.

## Sources

- Microsoft Learn, robocopy reference: https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/robocopy
- Microsoft Learn, WNetAddConnection2W: https://learn.microsoft.com/en-us/windows/win32/api/winnetwk/nf-winnetwk-wnetaddconnection2w
- Wikipedia, Robocopy (history, versions, known issues, Robocopy GUI): https://en.wikipedia.org/wiki/Robocopy
- SS64, Robocopy and its exit codes: https://ss64.com/nt/robocopy.html and https://ss64.com/nt/robocopy-exit.html
- Computer Hope, Robocopy command: https://www.computerhope.com/robocopy.htm
- RoboCopy GUI listings: https://www.capterra.com/p/10016130/RoboCopy-GUI/ and https://www.getapp.com/security-software/a/robocopy-gui/
- AlternativeTo listings: https://alternativeto.net/software/robocopy-gui-1, https://www.alternativeto.net/software/teracopy/, https://www.alternativeto.net/software/fastcopy/

Third-party listings are directory pages, not vendor documentation; check a product's own site before repeating a
claim about it.
