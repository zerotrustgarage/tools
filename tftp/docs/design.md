# ZTG TFTP: design

Status (October 22, 2026): T1 implemented, pending review. Later slices remain proposed. The market research is in [research.md](research.md);
section numbers below refer to that document as "R" (R5 is its list of gaps).

## 1. Purpose

ZTG TFTP is a TFTP server and client for technicians: serve a firmware image, a boot file or phone configurations,
or collect device configurations, for the length of a job. It replaces Tftpd64, SolarWinds TFTP Server and similar
third-party binaries in ZTG and client environments with a tool whose source, build chain and signing ZTG owns.

It is built for the job-shaped use of TFTP (start, move files, stop) first, and for an unattended service second.

## 2. Principles

These are requirements, not preferences. Each answers a failure in R3 and R4.

1. Read-only by default. A server started with no write options accepts no WRQ. Writes are opt-in, go only to a
   drop box separate from the served root, never overwrite, and are never served until a person promotes them.
2. Contained by construction. Every file access goes through `os.Root`, which refuses `..` and symlinks that leave
   the root. A name policy runs first (section 5). No path is ever checked by filtering strings (R4, CVE-2006-1951).
3. Scope guard. The server listens only on private (RFC 1918), CGNAT, link-local, unique local and loopback
   addresses unless the technician passes `--allow-public`. A wildcard bind expands to the eligible addresses only.
   Requests from outside the allowlist (default: the subnets of the bound addresses) get no reply, so a refused
   request cannot be used for reflection.
4. Time-boxed. An interactive session stops after a fixed time (default 60 minutes; `--for`), and the GUI shows the
   time left. Running indefinitely is a service-mode setting made in a configuration file, never a default (CM.L2-3.4.7).
5. Bounded. Retransmissions, concurrent transfers (global and per source), new transfers per second per source,
   upload size and the traffic sent to peers that have never acknowledged anything are all capped (section 6).
6. Accountable. Every request, served or refused, produces an audit record: source, file, direction, options, bytes,
   SHA-256, duration, outcome. File contents and anything a finding detects as a secret are never logged.
7. Honest about the protocol. TFTP has no authentication and no encryption. The allowlist is access control, not
   identity. The tool says so in its help, its GUI and its audit records, and does not imply it can carry CUI.
8. Memory-safe and fuzzed. Go, with native fuzz targets on every decoder that unauthenticated packets reach.
9. Clean room and few dependencies. Written from the RFCs. Standard library first; `golang.org/x/sys` and
   `golang.org/x/net` where the platform requires them; Wails for the desktop app, as in ZTG Scanner.
10. One job. No DHCP, DNS, SNTP or syslog server bundled (R3, Tftpd64): a rogue DHCP server on a client LAN is an
    outage, and every extra listener is attack surface.

## 3. Protocol support

| Feature | Server | Client | Notes |
| --- | --- | --- | --- |
| RFC 1350 `octet` | Yes | Yes | |
| RFC 1350 `netascii` | Yes | Yes | CR LF and CR NUL translation; `mail` mode is refused (obsolete) |
| RFC 1123 Sorcerer's Apprentice fix | Yes | Yes | DATA is retransmitted only on timeout, never because of a duplicate ACK |
| RFC 2347 option negotiation | Yes | Yes | Unknown options are ignored, as the RFC requires |
| RFC 2348 `blksize` | 8 to 65,464 | Yes | Server cap configurable; the client defaults to 1,468 (no fragmentation on a 1,500 MTU) |
| RFC 2349 `timeout`, `tsize` | Yes | Yes | `tsize` on a WRQ is checked against the upload cap and free space before any data is accepted |
| RFC 7440 `windowsize` | Yes | Yes | Server cap configurable (default 16); a lost block restarts the window from that block |
| Block number rollover | Wraps to 0 | Wraps to 0 | No RFC specifies it; 0 is what curl and tftp-hpa expect (R2). Files past 4 GiB need `tsize` larger than 32 bits, which the option text allows |
| Single-port mode | Optional | n/a | All transfers answered from port 69 for firewalls and NAT that cannot follow TFTP's per-transfer ports; replies leave from the address the request arrived on (`IP_PKTINFO`, `IPV6_PKTINFO`) |
| IPv6 | Yes | Yes | |
| RFC 2090 multicast | No | No | Experimental, rarely supported; revisit only with a client that needs it |

The protocol engine is written here rather than taken from `github.com/pin/tftp/v3`, which has no `windowsize`, only
server-side `tsize`, and no hooks for the limits in section 6 (R3). The engine is the security boundary, it is small
(an estimated two to three thousand lines with tests), and owning it is the point of the tool.

## 4. Architecture

```
cmd/ztg-tftp        CLI: serve, get, put, findings, version
internal/wire       packet encode and decode, option parsing (fuzzed)
internal/xfer       transfer state machines for both directions, windowing, retransmission, rollover
internal/server     listeners, demultiplexing, single-port mode, session clock
internal/client     get and put
internal/store      os.Root-backed served root and drop box, name policy, atomic upload commit
internal/policy     scope guard, source allowlist, rate limits, unverified-peer budget
internal/audit      JSON Lines audit log; syslog and Windows Event Log forwarding (T4)
internal/findings   secret and hygiene checks on collected configurations (T2)
app/                desktop app (separate Go module, as in ZTG Scanner)
```

The transfer state machines take a clock and a packet transport as interfaces, so tests drive them through loss,
duplication, reordering and delay without real sockets or sleeps.

## 5. File access

Name policy, applied to every requested name before `os.Root` sees it. A rejected name gets TFTP error 2 (access
violation) and an audit record with the reason:

- Backslashes become slashes (Windows clients and PXE loaders send both); a leading slash is dropped, so `/pxelinux.0`
  and `pxelinux.0` are the same file.
- Rejected: empty names, `.` or `..` components, drive letters, UNC prefixes, NUL and other control characters, `:`
  anywhere (blocks NTFS alternate data streams), Windows reserved device names in any component and with any
  extension (`CON`, `PRN`, `AUX`, `NUL`, `COM1` to `COM9`, `LPT1` to `LPT9`, `CONIN$`, `CONOUT$`), components ending in a dot or space,
  components over 255 bytes and names over 1,024 bytes.
- Optional case-insensitive lookup for clients that request `PXELINUX.0` from a Linux host.

Then:

- Reads open through `os.Root` on the served root. Directories, devices and other non-regular files are refused.
- Writes open through `os.Root` on the drop box with exclusive create on a temporary name, are hashed as they stream,
  and are renamed into place only when the transfer completes. A failed or abandoned upload leaves nothing behind.
- Drop box layout: `<drop box>/<YYYY-MM-DD>/<source address>/<HHMMSS>-<name>`. Two uploads of the same name never
  collide, and nothing can overwrite a served file.
- The drop box may not be inside the served root, and the served root may not be inside the drop box; the server
  refuses to start otherwise.
- Created files and folders are owner-only (mode 0600 and 0700 on Linux; an owner-only DACL on Windows), because
  collected configurations hold credentials (R4).
- Requires Go 1.24.3 or later (CVE-2025-22873 in `os.Root`); the repository uses Go 1.26, as ZTG Scanner does.

## 6. Limits and amplification

TFTP reflection works because a ten-byte RRQ with a spoofed source makes a server send a full DATA block, then
retransmit it several times (R4). The defenses, all on by default:

| Control | Default | Effect |
| --- | --- | --- |
| Scope guard and allowlist (principle 3) | Bound subnets | Out-of-scope sources get no reply at all |
| Retransmissions before any ACK from the peer | 2 | Caps what one spoofed RRQ can produce |
| Retransmissions after the peer has ACKed | 5 | Ordinary loss recovery |
| Unverified-peer budget | 256 KiB/s across all peers | Total bytes sent to peers that have not yet ACKed anything; requests beyond it wait or are dropped |
| New transfers per source | 20 per second | |
| Concurrent transfers | 64 total, 8 per source | Error 0 ("server busy") beyond them |
| Upload size | 256 MiB | Checked against `tsize` up front and enforced while streaming |
| Drop box free space floor | 1 GiB | WRQ refused with error 3 (disk full) below it |
| Refusal audit records | 10 per second per source, then one summary | A flood cannot fill the disk with log lines |

With options, the server answers with an OACK and waits for ACK 0 before sending data, so an option-bearing RRQ is
only reflected as a small OACK.

## 7. Audit record

JSON Lines, one record per request, written to the session folder (and forwarded in T4):

```json
{"time":"2026-10-22T14:03:11.204Z","session":"ses_01HZX...","transfer":"xfr_01HZX...","op":"read",
 "source":"10.20.0.31:49152","local":"10.20.0.5:69","name":"c9300-universalk9.17.12.04.SPA.bin",
 "options":{"blksize":1468,"windowsize":8,"tsize":1324998656},"bytes":1324998656,
 "sha256":"9f2c...","duration_ms":41230,"result":"complete"}
```

`result` is one of `complete`, `refused`, `peer-error`, `timeout`, `aborted`; refusals carry `reason` (for example
`outside-allowlist`, `name-policy`, `write-disabled`, `too-large`, `rate-limited`). Uploads add `stored_as` and, after
T2, `findings` (codes only, never values) and `changed_since` (the previous upload from the same source and name).

## 8. Interface

Planned CLI (T1 and T2):

```
ztg-tftp serve ./images                                   # read-only, 60 minutes, private addresses only
ztg-tftp serve --for 15m --bind 192.168.1.10 ./images
ztg-tftp serve --upload ./collected --allow 10.20.0.0/24 ./images
ztg-tftp serve --single-port --blksize-max 8192 ./pxe
ztg-tftp get 192.168.1.1 startup-config
ztg-tftp put --windowsize 8 192.168.1.50 firmware.bin
ztg-tftp findings ./collected                             # rescan collected configs
```

Port 69 and privileges:

- Windows has no privileged ports, but Windows Defender Firewall blocks inbound UDP for a new program, and a standard
  user cannot approve the prompt. The MSI (T5) adds an inbound rule for the program on the Domain and Private
  profiles only, never Public. The portable build detects a blocked firewall and tells the technician what to ask
  for, rather than failing silently.
- Linux needs `CAP_NET_BIND_SERVICE` (the systemd unit grants it as an ambient capability) or a lowered
  `net.ipv4.ip_unprivileged_port_start`. `--port` allows a high port for testing.

## 9. Collected configuration findings (T2)

Collecting a configuration is the moment to tell the technician what is wrong with it. Checks run on every upload in
the drop box and on demand (`findings`). Findings name the rule and line number, never the value.

| Code | Severity | Condition (Cisco IOS and IOS XE first; other vendors by fixture) |
| --- | --- | --- |
| `cleartext-password` | high | `password 0`, `enable password`, or a `username ... password` line without a hashed type |
| `reversible-password` | high | Type 7 passwords or keys (reversible encoding, not a hash) |
| `snmp-community` | medium | SNMPv1 or v2c community configured (the access path in AA26-194A, R4) |
| `smart-install` | high | Smart Install not disabled (no `no vstack` line) on a switch platform; reported as "verify" because the default differs by release (CVE-2018-0171, in CISA's Known Exploited Vulnerabilities catalog) |
| `telnet` | medium | `transport input telnet` or `all` on VTY lines |
| `http-cleartext` | medium | `ip http server` enabled |
| `weak-hash` | low | Type 5 (MD5) secrets where type 8 or 9 is available |

Also in T2: each upload is compared with the previous one from the same source and name, and the audit record says
whether it changed, which turns a config backup run into evidence for CM.L2-3.4.3 (change tracking).

## 10. Desktop app (T3)

The same engine in a Wails v2 window, following ZTG Scanner's model (bound methods with no Wails imports, tested on
any platform; plain HTML, CSS and JavaScript; `textContent` only, because file names are client-supplied text; a
Content-Security-Policy with no inline script; UI tests in headless Chromium against a binding mock).

- Start panel: served folder, optional drop box, interface picker from local addresses, allowlist, session length.
- Live transfers: name, peer, direction, progress, rate, negotiated options; refused requests with reasons.
- Session timer with extend and stop; the server stops when the window closes.
- Collected files with findings and change status; promote a collected file into the served root (copies it,
  recording both hashes).
- Built-in client panel for get and put tests (Windows has no TFTP client by default, R5 item 9).
- Export the session's audit log.
- The ZTG palette from `design-tokens.json`; T3 adds the stylesheet to `scripts/ci/check-palette.mjs`
  (repository rule 6).

## 11. Compliance mapping

NIST SP 800-171 Rev 2, as CMMC 2.0 Level 2 uses it. ZTG TFTP supports these practices; it is not an assessment of
them, and TFTP itself cannot meet SC.L2-3.13.8.

| Practice | How the tool supports it |
| --- | --- |
| CM.L2-3.4.6, CM.L2-3.4.7 | Time-boxed sessions; read-only default; no bundled services |
| AU.L2-3.3.1, AU.L2-3.3.2 | Audit record of every request with hash and outcome; traceable to a source address and the technician who ran the session, not to a TFTP user (the protocol has none) |
| CM.L2-3.4.1, CM.L2-3.4.3 | Collected configurations with hashes and change status |
| SC.L2-3.13.1 | Scope guard; no public listening by default |
| AC.L1-3.1.1 | Source allowlist (network-level restriction, not authentication) |
| SI.L1-3.14.1 | Firmware delivery recorded with SHA-256, so an upgrade can be tied to an exact image |

## 12. Testing

- Transfer state machines under a simulated transport: loss, duplication, reordering, delay, the Sorcerer's Apprentice
  case, a peer that vanishes mid-window, and an ERROR at every step.
- Boundaries: empty file; a file that is an exact multiple of the block size (needs a final empty DATA); exactly
  65,535 and 65,536 blocks (rollover); a sparse file past 4 GiB.
- Fuzz targets: packet decode, option parsing, the name policy (with a property check that nothing accepted resolves
  outside the root) and the config findings parser.
- Amplification test: bytes sent per spoofed RRQ, with and without options, stay under the section 6 bound.
- Interoperability in CI against curl (`tftp://`, `--tftp-blksize`, `--tftp-no-options`) and the tftp-hpa and atftp
  clients where the runner has them, as ZTG Scanner tests SNMPv3 against net-snmp.

## 13. Slices

| Slice | What | Status |
| --- | --- | --- |
| T1 | Protocol engine (section 3), read-only server, client `get` and `put`, scope guard, allowlist, limits, name policy, audit log, fuzzing, interop tests, CI workflow `tftp.yml` | Implemented, pending review |
| T2 | Uploads: drop box, atomic commit, owner-only files, upload caps, findings on collected configs, change status | Proposed |
| T3 | Windows desktop app (section 10) | Proposed |
| T4 | Service mode: Windows service and systemd unit, JSON configuration file with reload, syslog (RFC 5424, TCP with TLS) and Windows Event Log forwarding, path remap rules, per-MAC templated files for phone provisioning | Proposed |
| T5 | Packaging and trust: Authenticode signing through `scripts/signing/`, MSI with the scoped firewall rule, portable ZIP, SBOM, `release-tftp.yml` with provenance attestation | Proposed |
| T6 | Candidates needing an owner decision: a read-only HTTP listener on the same root for iPXE and UEFI HTTP boot (faster than TFTP, same audit); proxyDHCP for PXE only (boot file options, never address assignment); a hash manifest that limits serving to approved images; pre-filling the allowlist from a ZTG Scanner export | Proposed |

Each slice is reviewed before the next starts.

T1 implementation notes: wildcard binds expand to individual addresses (IPv4 by default, IPv6 with `--bind ::`),
so single-port replies use the destination interface without packet-info socket options. The configurable window
cap is bounded at 64 blocks. Netascii does not negotiate octet-only `tsize`. Server uploads and `findings` start in
T2. The implementation follows the proposed name, own engine and 60-minute default; T6 and retention remain open.
The README describes the implemented flags, limits and test coverage.

## 14. Not in scope

- DHCP, DNS, SNTP or syslog servers (principle 10). T6's proxyDHCP never assigns addresses.
- Nonstandard authentication or encryption bolted onto TFTP. Clients would not support it. Where a device supports
  SCP, SFTP or HTTPS, the technician should use that instead, and the documentation says so.
- Internet-facing service, and any use with CUI.
- Credential storage of any kind.
- Cloud reporting. Audit logs and collected files stay where the technician puts them.

## 15. Open questions for the owner

1. Name: "ZTG TFTP" with folder `tftp/` and binary `ztg-tftp`, or a product name.
2. Own protocol engine (recommended, section 3) or `pin/tftp` extended.
3. Default session length (60 minutes proposed) and whether the GUI should allow "until stopped".
4. Which T6 candidates, if any, are wanted.
5. Retention for collected configurations: keep until deleted (proposed), or an automatic retention period.
