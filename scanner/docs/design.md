# ZTG Scanner: design

Status (October 29, 2026): slices S1 (engine and CLI), S2 (desktop app), S3 (SNMPv3 inventory) and S4 (packaging
and release pipeline), and S5 (actions, local inventories and comparison) are built, as are SC9 (saved target sets),
SC10 (Open with commands) and SC13 (gentle mode). Owner signing setup is
complete; S4's first signed release awaits a successful production release run. S6 is planned.

## 1. Purpose

ZTG Scanner finds every device on networks a technician specifies and reports what each one is: address, names, MAC
address, manufacturer, device type, open services and findings worth attention. It replaces Advanced IP Scanner
(Famatech) in ZTG and client environments with a tool whose source, build chain and signing ZTG owns, so a client's
assessor can be told exactly where every network tool came from.

It is a technician's tool. It runs on demand from a laptop, jump host or management server, needs no agent and no
cloud service, and keeps its results local unless the technician exports them.

## 2. Principles

1. Identify, never intrude. Every default probe is a single standard protocol exchange that any client on the LAN may
   make (an ICMP echo, an ARP request, a TCP connect, one HTTP GET for `/`, one TLS handshake, a NetBIOS node status
   query, an mDNS query, an SSDP search). Nothing authenticates, writes, or exploits. The one credentialed probe,
   SNMPv3 (slice S3), is off by default, read-only, and enabled per scan with credentials the technician enters.
2. Scope guard. Only private (RFC 1918), carrier-grade NAT, link-local and loopback space is scanned by default.
   Public space needs `--allow-public` (and, in the GUI, an explicit authorization confirmation). One scan is capped at
   65,536 addresses.
3. Identify ourselves. HTTP requests and SSDP searches carry `User-Agent: ZTG-Scanner/<version> (+https://ztg.sh)`,
   so a client's administrators can tell who looked.
4. No privileges required. On Windows every probe works as a standard user: ICMP through `IcmpSendEcho` and ARP
   through `SendARP` (both `iphlpapi.dll`), with no Npcap or other capture driver. On Linux, ICMP uses an unprivileged
   datagram socket where `net.ipv4.ping_group_range` allows it, else a raw socket (root or `CAP_NET_RAW`); ARP comes
   from the kernel neighbor table after one UDP datagram makes the kernel resolve the address.
5. Clean room. The scanner is written from public protocol specifications. No code, data or assets come from Advanced
   IP Scanner, Radmin, or Nmap (whose service probe database is under the NPSL, which does not suit closed
   redistribution). The MAC vendor table is generated from the IEEE Registration Authority's public listings.
6. Evidence over guesses. Every host records which probes proved it alive (`evidence`), and every inferred device
   type records why (`type_reason`). The OS hint from the reply TTL is labeled as an inference.
7. Few dependencies. Go standard library plus `golang.org/x/net` and `golang.org/x/sys`.

## 3. Architecture

```
cmd/ztg-scanner      CLI: scan, vendor, version
internal/targets     target parsing (CIDR, ranges, hostnames), scope guard, host cap
internal/probe       one file per probe; platform files for ICMP and ARP
internal/oui         embedded IEEE MAC vendor table and its generator (go generate)
internal/classify    device type rules and findings
internal/scan        the engine: worker pool, per-host pipeline, Observer callbacks
internal/report      table, CSV and JSON output
app/                 desktop app (separate Go module): Wails shell, bound methods, plain HTML/CSS/JS frontend
```

Per host, the engine runs:

1. Liveness, in parallel: ICMP echo (two attempts) and, for on-link addresses only, ARP. ARP is never used off-link,
   so a router's MAC is never attributed to a host behind it.
2. Ports: a TCP connect to each port in the list (40 by default). A host that ignored ICMP and is off-link gets a short
   discovery list first; any connection or reset proves it is up, and the rest of the list follows. A reset counts as
   proof of life because only a live host sends one.
3. Identification, in parallel, for live hosts: reverse DNS, NetBIOS node status (UDP 137), mDNS legacy unicast
   (reverse name and DNS-SD service list, UDP 5353), service banners on open ports, and the UPnP device description
   for hosts that answered the scan's one SSDP multicast search.
4. MAC vendor lookup (ARP MAC, else the NetBIOS unit ID, which crosses routers), best-name selection
   (DNS, then NetBIOS, then mDNS, then UPnP friendly name), classification and findings.

Results stream to an `Observer` as each host finishes, so the CLI and GUI show progress and partial results, and a
canceled scan keeps everything already established.

## 4. Probe catalogue (S1)

| Probe            | Wire exchange                                                | Learns                                         |
| ---------------- | ------------------------------------------------------------ | ---------------------------------------------- |
| ICMP echo        | Echo request and reply                                       | Alive, round trip, reply TTL (OS family hint)  |
| ARP              | ARP request (on-link only)                                   | Alive (even behind a host firewall), MAC       |
| TCP connect      | Full three-way handshake, then close                         | Open ports; a reset proves the host is up      |
| Greeting banner  | Read what the service sends first                            | SSH, FTP, SMTP, POP3, IMAP, MySQL, VNC version |
| HTTP             | `GET /` with the ZTG user agent; no redirects followed       | Status, Server, page title, auth realm         |
| TLS              | One handshake; certificate recorded, not trusted             | Subject, issuer, names, expiry, self-signed    |
| NetBIOS          | Node status request for `*` (RFC 1002)                       | Computer name, workgroup or domain, MAC        |
| mDNS             | Legacy unicast PTR queries to the host (RFC 6762 6.7)        | `.local` hostname, advertised service types    |
| SSDP and UPnP    | One multicast M-SEARCH; GET of the description on the device | Friendly name, manufacturer, model, type       |
| Reverse DNS      | PTR lookup through the system resolver                       | DNS name                                       |

## 5. Findings

Findings are where ZTG Scanner goes past Advanced IP Scanner for regulated clients:

| Code           | Severity | Condition                                                                                               |
| -------------- | -------- | ------------------------------------------------------------------------------------------------------- |
| `ndaa-889`     | high     | MAC or UPnP manufacturer is a producer named in FY2019 NDAA section 889 / FAR 52.204-25 (Huawei, ZTE, Hytera, Hikvision, Dahua, and the Hikvision brand EZVIZ) |
| `telnet`       | high     | TCP 23 open                                                                                             |
| `ftp`          | medium   | TCP 21 open                                                                                             |
| `http-admin`   | medium   | A management page on HTTP with no HTTPS on 443                                                          |
| `cert-expired` | info     | A TLS certificate on the host has expired                                                               |

The NDAA match uses IEEE assignee names, so OEM rebrands of covered equipment (sold under another brand's name and
MAC block) are not caught. Treat a clean result as "no covered vendor visible", not "no covered equipment".

Compliance context (NIST SP 800-171 Rev 2, as a CMMC Level 2 assessment uses it): the scan supports CM.L2-3.4.1
(baseline inventory of organizational systems) and gives evidence for CM.L2-3.4.7 (nonessential services: Telnet,
FTP) and SC.L2-3.13.8 (cleartext administration). It is not itself an assessment of those practices.

## 6. Output

- Table (terminal default): live hosts, with `--all` for every address.
- CSV: one row per host; cells that begin with `=`, `+`, `-`, `@`, tab or carriage return are prefixed with `'` so
  device-supplied text cannot run as a spreadsheet formula.
- JSON: the full record (summary, methods, warnings, every host with all probe detail).
- HTML: a self-contained client inventory report, with the ZTG palette and Scanner logo, site label, scope,
  observation counts, scan state, findings, notes and device evidence. It renders recorded observations only;
  it performs no probes and loads no external resources. Device/user text is escaped by `html/template`, with
  no scripts or active device links. Output is capped at 64 MiB. Desktop reports include every recorded host
  regardless of grid filters, and file writes replace the destination atomically. An export captures its
  inventory snapshot before the save dialog so later scans cannot change the selected report.

The CLI's `report` command renders a saved Scanner JSON inventory offline using the same bounded reader as the
desktop. Reports distinguish recorded observations from the requested address count, label canceled/unfinished
scans, and keep historical MACs separate from current evidence. A report is not a compliance determination.
The browser can print the file to PDF using the light palette; XLSX and RMM/PSA CSV layouts remain future work.

## 7. Desktop app (S2)

`app/` is its own Go module so the CLI module carries no GUI dependency. It imports the engine through a `replace`
directive.

- Shell: Wails v2.16.0 with WebView2 on Windows. The binary builds with `CGO_ENABLED=0`, so CI cross-compiles it
  from Linux. Non-Windows builds print a pointer to the CLI.
- Bound methods (`app.go`, no Wails imports, tested on any platform): `Suggestions` (the machine's IPv4 networks),
  `Defaults`, `StartScan`, `StopScan`, `Results`, `Export` (CSV or JSON through a save dialog) and `OpenWeb`.
  `OpenWeb` only opens a host and port that appear in the current results, so the UI cannot open arbitrary URLs.
- Events: `scan:host` for each live host as it finishes, `scan:progress` (at most ten a second), `scan:done`.
- Frontend: plain HTML, CSS and JavaScript, no npm. Every device-supplied string is inserted with `textContent`
  under a Content-Security-Policy that forbids inline script, because a scanned device controls its own page title,
  banner and names. `uitest/` runs the UI in headless Chromium against a binding mock, including an HTML injection
  attempt.
- Public ranges: the engine refuses them unless the request sets `allow_public`, which the UI does only after a
  confirmation that the technician is authorized.
- Settings persist in the WebView's local storage, except SNMP passphrases and the SNMP switch.

## 8. SNMPv3 inventory (S3)

- Opt-in per scan. The CLI enables it with `--snmp-user`; the GUI with a switch that is off at every launch.
- authPriv only, with SHA-family authentication and AES privacy. SNMP v1 and v2c (cleartext community strings), MD5
  and DES are refused. Passphrases must be at least 8 characters (RFC 3414).
- GET and GETNEXT only, on standard objects: MIB-II system group and `ifNumber`, ENTITY-MIB (RFC 6933) columns of the
  first chassis entity (manufacturer, model, serial, hardware, firmware and software revisions), and the Printer-MIB
  (RFC 3805) serial number as a fallback. Nothing writable or vendor-specific is touched.
- Credentials: `probe.SNMPCredentials` excludes every field from JSON and redacts itself in `String`, so it cannot
  reach an export or a log. The CLI reads passphrases from an echo-off prompt or `ZTG_SNMP_AUTH_PASS` and
  `ZTG_SNMP_PRIV_PASS`, never from flags (shell history, process lists).
- Results feed naming (sysName), classification (sysDescr, manufacturer, sysObjectID enterprise) and the NDAA
  section 889 finding.
- Library: `github.com/gosnmp/gosnmp` (BSD-2-Clause) for the SNMPv3 user security model. Interoperability is tested
  against net-snmp `snmpd` when it is installed.

## 9. Slices

| Slice | What                                                                                                                                        | Status  |
| ----- | ------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| S1    | Engine, probes, IEEE vendor table, classification, findings, CLI with table, CSV and JSON, CI                                                | Built   |
| S2    | Desktop app (Wails v2, WebView2 on Windows): range picker from local interfaces, live results grid, detail pane, filters, export | Built   |
| S3    | SNMPv3 inventory, opt-in per scan: authPriv only, GET and GETNEXT only, model, serial, firmware, location, interfaces | Built   |
| S4    | Packaging and trust: Authenticode signing, MSI (WiX) and a portable ZIP, reproducible unsigned executable builds, SBOM, release workflow with provenance attestation | Built; signing setup complete, first production release run pending |
| S5    | Actions from results: browser, SSH, RDP, copy, Wake-on-LAN, ping and traceroute; site inventories, notes, tags and favorites; compare scans | Built |
| S6    | macOS build (ARP through the routing socket), IPv6 neighbor discovery on the local link                                                     | Planned |

Each slice is reviewed before the next starts.

### S5 boundaries

Site inventories are explicit local JSON files, with bounded parsing and atomic replacement. They contain no
scan request credentials or executable command templates. Loading data starts no scan and grants no public-range
authorization. Unknown JSON fields are refused. Annotations use address/MAC pairs; they follow a move only when
that stable MAC is unique in consecutive scans. Historical MACs on unresponsive hosts are labeled separately.

Comparison matches unique stable MACs first, then addresses, and ignores timing and technician annotations.
Only a visited, unresponsive address in a completed scan can be called missing. Canceled scans, unvisited ranges
and ambiguous matches are not assessed. Observed name, type, vendor, MAC, services, model, serial and firmware
changes are reported with their before/after evidence. The result is an observation comparison, not an assertion
of unauthorized change or removal.

SSH and RDP hand off to installed Windows clients; Scanner bundles no remote access software. Only numeric result
addresses reach fixed executables in the Windows system directory, without a shell. SSH/RDP require an observed
standard service port; imported public addresses require a fresh authorization. Diagnostics have a 30-second limit.
Wake-on-LAN is a separate, explicitly confirmed action: one magic packet to the most specific directly connected
internal IPv4 network's broadcast address, never a forwarded or arbitrary broadcast. No credentials are persisted.

### Open with commands (SC10)

The technician can define up to 20 "Open with" commands in Settings, Open with commands. Each is either an installed
program (an absolute path to an `.exe` plus one template per argument) or an `https://` address. They are chosen from
an Open with menu on a result (the device pane button, or right-click on the row). Nothing launches except on that
explicit choice, and Scanner bundles no remote access software: it starts whatever the technician already has
installed, such as PuTTY or mstsc, or opens an RMM console link in the browser.

Where commands live: only in the technician's local settings. The desktop UI keeps them in the WebView's local storage
beside saved target sets, and hands the list to the app (`SetOpenWithCommands`), which validates it again and keeps
the copy it launches from. A launch sends a command name and a result address; the app reads the hostname from its own
results. Inventory files, exports and scan requests never carry commands, and a device cannot add or change one.

Rules, all in `internal/openwith` and tested there:

- Argument vectors, never shell strings. A program command is split into one template per argument when it is
  saved, and a placeholder is substituted only inside its own element, so a value cannot add, remove or reorder
  arguments. The process is started with CreateProcess and an exact vector (the same path S5 uses for SSH and RDP),
  with the program as the application name so Windows does not search for it. No `cmd.exe /c`, PowerShell, `sh -c`
  or other shell is used; those and other script hosts (`wscript`, `cscript`, `mshta`, `wsl`, `bash`, `rundll32`,
  `regsvr32`, `cmdkey`, `runas` and similar) are refused as the program, as are `.bat`, `.cmd`, `.ps1`, `.vbs` and
  `.lnk`. This guards against a mistake; it is not a sandbox, because any installed `.exe` the technician chooses
  runs with the technician's rights.
- Program path: absolute, on a drive letter, backslashes only, at most 260 ASCII characters, no `..`, UNC or device
  paths, no environment variables, and it must exist as a regular file at launch.
- `{ip}` is a parsed `net/netip` address rendered canonically (an IPv4-mapped IPv6 address becomes IPv4; zones,
  unspecified and multicast addresses are refused).
- `{hostname}` is the reverse DNS name if there is one, else the best name, and must be a strict DNS name: ASCII
  letters, digits, hyphens and dots, labels of 1 to 63 characters that do not start or end with a hyphen, at most 253
  characters, no trailing dot, and a last label that is not all digits. A name that fails is never cleaned up or
  quoted; the command is shown as unavailable for that device, with the reason, and a command that does not use
  `{hostname}` still works. This also means no device name can start with a hyphen and become an option.
- URL commands must start with `https://` and have a literal host: placeholders, user names and passwords are
  refused in the scheme and host part, so a device value can never choose the destination. Placeholders in the path
  are path-escaped and in the query or fragment are query-escaped. The result is opened through the same browser
  handoff as other links (`OpenURL`).
- No other placeholders exist. There is no password placeholder, and a template that looks like it carries a password
  or token (`-pw`, `--password`, `/pass:`, `token=` and similar) is refused. This is a heuristic for obvious cases;
  it does not make storing a secret in some other form safe, and the settings text says Scanner stores no credentials.
- Bounds: 20 commands, 60-character names (unique ignoring case), 16 arguments of up to 512 characters, 2,048-character
  URLs, no control characters. A list is validated whole; an invalid one is refused and the previous list stays active.
- The editor's Preview renders a command with a test address and hostname (checked by the same rules) and shows the
  exact program and one bracketed line per argument, or the exact URL. It starts nothing.
- Public addresses need the same fresh authorization as the other connection actions, and the address must be in the
  current results.

Unreadable or invalid stored commands leave none active, say why in the menu, and offer a reset in the editor.
Real Windows launching of third-party clients has not been exercised in CI (Linux tests use a fake platform and the
UI tests use a binding mock); the Windows paths are cross-compiled and vetted.

### Gentle mode (SC13)

A named, opt-in scan mode for fragile networks (OT, medical, old printers). The user selects it (`--mode gentle`, or
Scan mode in the settings panel); nothing selects it automatically, and the default mode's plan is unchanged.

One definition drives both front ends. `scan.BuildPlan` resolves a `PlanRequest` into a `Plan`; the CLI and the
Desktop app both call it, and `Plan.Config` produces the engine settings. Gentle fixes: 4 hosts in parallel, 2
connections per host, one echo request per host, a 1500 ms connect timeout, TCP ports 22, 80, 443, 445, 515, 631,
8080 and 9100, and a shared pacer that allows one probe start every 100 ms (10 per second) across all hosts. The
pacer gates every probe start: echo, ARP, TCP connect, reverse DNS and, if ever enabled, banner grabs. It uses an
injectable clock, so tests assert the schedule without real time. Probes in gentle mode are ICMP, ARP, TCP connect
and reverse DNS only; banner grabs, NetBIOS, mDNS, SSDP and SNMP are off and a request to enable them is ignored
(SNMP is rejected with an error). A request can ask for fewer hosts, a subset of the gentle ports, fewer probes or any positive connect timeout,
never more, and malformed values fail with a message instead of being clamped. Cancellation interrupts a pending pacer wait. A gentle scan records
`"mode": "gentle"` in the summary; default scans omit the field.

Gentle mode lowers load and skips the more intrusive probes; it is slower and identifies less. It does not make a
scan safe for every device, and the CLI help and the settings panel say so. The Desktop UI shows the selected numbers
and the tradeoff from the same constants. Validation on real fragile equipment needs an approved lab and is not
claimed here.

### Saved target sets (SC9)

Target sets contain a client/site label, a set name and target specifications only. Up to 50 sets are kept in the
current user's WebView storage. They contain no probe settings, SNMP credentials or public-range authorization.
Saving, selecting and importing sets never resolves a hostname or starts a scan. Syntax validation is local;
the existing scope guard, DNS resolution and 65,536-host limit still run when the technician presses Scan.

Text imports use an explicit file chooser and are limited to 64 KiB of UTF-8 text and 4,096 target specifications.
Blank lines and `#` comments are allowed, with comma, semicolon or whitespace separators. Addresses, IPv4 CIDR
blocks, IPv4 ranges and hostnames use the same syntax as the scan input. Invalid files leave the draft and active
inventory unchanged. Public target specifications may be saved, but still require fresh authorization at scan time.

Using a target set starts a new site inventory after an unsaved-changes confirmation, so annotations cannot carry
into another client's scan. It clears SNMP credentials and disables SNMP. Importing fills the editable draft;
the technician explicitly chooses when to use it. Duplicate names within a client require overwrite confirmation,
and deletion requires confirmation. Storage failures must be reported as failures to save.

## 10. Not in scope

- Remote access (Advanced IP Scanner bundles Radmin; ZTG Scanner bundles no remote access software and installs
  nothing on scanned hosts). SC10 only starts clients the technician already has, on the technician's choice.
- Vulnerability scanning or exploitation.
- Credential storage. SNMPv3 passphrases are entered per scan (CLI prompt or environment variable, GUI password
  fields), held in memory, and never written to disk, settings or exports.
- Any cloud reporting. Results stay on the technician's machine unless exported.
