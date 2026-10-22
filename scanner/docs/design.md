# ZTG Scanner: design

Status (October 22, 2026): slice S1 (engine and CLI) is built. S2 onward are planned.

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
   query, an mDNS query, an SSDP search). Nothing authenticates, writes, or exploits. Probes that enumerate shares or
   users, or that use credentials, are separate and opt-in per scan (slice S3).
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
app/ (S2)            desktop shell (Wails) on the same engine
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

## 7. Slices

| Slice | What                                                                                                                                        | Status  |
| ----- | ------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| S1    | Engine, probes, IEEE vendor table, classification, findings, CLI with table, CSV and JSON, CI                                                | Built   |
| S2    | Desktop app (Wails v2, WebView2 on Windows): range picker from local interfaces, live results grid, detail pane, filters, export, scan history | Planned |
| S3    | Opt-in deep probes, off by default and confirmed per scan: SMB dialect and NTLM-challenge host info, SMB share listing with supplied credentials, SNMPv3 (authPriv, read-only) for model, serial and interfaces | Planned |
| S4    | Packaging and trust: Authenticode signing, MSI (WiX) and a portable ZIP, reproducible builds, SBOM, release workflow with provenance attestation | Planned |
| S5    | Actions from results: open in browser, SSH, RDP, copy, Wake-on-LAN, ping and traceroute; saved favorites; compare two scans (new and missing devices) | Planned |
| S6    | macOS build (ARP through the routing socket), IPv6 neighbor discovery on the local link                                                     | Planned |

Each slice is reviewed before the next starts.

## 8. Not in scope

- Remote access (Advanced IP Scanner bundles Radmin; ZTG Scanner bundles no remote access software and installs
  nothing on scanned hosts).
- Vulnerability scanning or exploitation.
- Credential storage. S3 credentials are entered per scan, held in memory, and never written to disk or exports.
- Any cloud reporting. Results stay on the technician's machine unless exported.
