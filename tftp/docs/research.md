# ZTG TFTP: market research

Status (October 22, 2026): research for the design in [design.md](design.md). Facts are cited; statements marked
"assessment" are ZTG's reading and should be rechecked against the product before they are repeated to a client.

## 1. Why technicians still need TFTP

TFTP (RFC 1350) is a tiny, unauthenticated, unencrypted UDP file transfer protocol. It survives because firmware
and boot loaders can fit it in a few kilobytes, and many devices offer nothing else at the moment they need a file:

| Job | Typical client | Direction |
| --- | --- | --- |
| Network device config backup and restore (`copy running-config tftp:`) | Switches, routers, firewalls | Upload to the server, download for restore |
| Firmware and OS image upgrades | Switches, routers, APs, UPS and PDU cards, printers | Download |
| PXE network boot (BIOS and UEFI) | NIC option ROMs, iPXE, WDS, U-Boot | Download |
| VoIP phone provisioning (per-MAC config files) | Desk phones, ATAs | Download, sometimes upload of logs |
| Recovery of bricked devices | ROMMON, U-Boot, vendor recovery loaders | Download |

For an MSP technician the usual pattern is short-lived: start a server on a laptop or jump host, move one image or
collect a batch of configs, stop it. That pattern matters for the design, because TFTP left running afterwards is a
standing exposure.

## 2. The protocol and its extensions

| RFC | What it adds | Notes |
| --- | --- | --- |
| [1350](https://www.rfc-editor.org/rfc/rfc1350) | TFTP revision 2: RRQ, WRQ, DATA, ACK, ERROR; 512-byte lockstep | 65,535 blocks of 512 bytes is about 32 MiB, so larger files depend on block number rollover |
| [1123](https://www.rfc-editor.org/rfc/rfc1123) section 4.2.3.1 | Fix for the Sorcerer's Apprentice Syndrome: never retransmit DATA because of a duplicate ACK | Implementations that miss this double their traffic on every delay |
| [2347](https://www.rfc-editor.org/rfc/rfc2347) | Option negotiation (OACK, error code 8) | |
| [2348](https://www.rfc-editor.org/rfc/rfc2348) | `blksize`, 8 to 65,464 bytes | 1,468 avoids IP fragmentation on a 1,500-byte MTU (1500 minus IP, UDP and TFTP headers) |
| [2349](https://www.rfc-editor.org/rfc/rfc2349) | `timeout` and `tsize` (transfer size) | `tsize` on a WRQ lets a server refuse an oversize upload before any data moves |
| [7440](https://www.rfc-editor.org/rfc/rfc7440) | `windowsize`: several blocks per ACK instead of lockstep | Standards Track (2015); barebox reports 4 to 30 times faster downloads, and too large a window can be slower ([barebox](https://www.barebox.org/doc/latest/filesystems/tftp.html), [U-Boot patch](https://lists.denx.de/pipermail/u-boot/2020-May/412633.html)) |
| 2090 | Multicast option | Experimental; few clients |

Block number rollover is not specified by any RFC. Common clients (curl, the Fedora tftp client) expect block 65,535
to be followed by block 0, and clients that did not handle the wrap were limited to about 32 MB
([curl tracker](https://curl.se/mail/tracker-2010-05/0019.html), [Apache Commons Net NET-181](https://issues.apache.org/jira/browse/NET-181)).

## 3. The servers technicians use

| Server | Platform | Strengths | Gaps (assessment unless cited) |
| --- | --- | --- | --- |
| Tftpd64 / Tftpd32 (Ph. Jounin) | Windows | The de facto free Windows choice; TFTP, DHCP, DNS, SNTP and syslog servers in one program ([GitHub](https://github.com/PJO2/tftpd64/)); GUI; service edition; v4.74 adds files over 4 GB ([releases](https://github.com/PJO2/tftpd64/releases/)) | Bundled DHCP server makes a rogue DHCP server on a client LAN one click away; no structured transfer record with hashes; no time limit on how long it serves; unsigned-by-ZTG third-party binary in client environments |
| SolarWinds TFTP Server | Windows | Free; source IP allowlist; concurrent transfers; files up to 4 GB with block size negotiation ([Comparitech](https://comparitech.com/net-admin/solarwinds-tftp-server-review), [THWACK FAQ](https://thwack.solarwinds.com/discussion/145572/tftp-faqs)) | Long CVE history: path traversal ([CVE-2002-1209](https://nvd.nist.gov/vuln/detail/cve-2002-1209), [CVE-2006-1951](https://nvd.nist.gov/vuln/detail/cve-2006-1951)) and crashes from a crafted OACK or a long WRQ (CVE-2009-3115, CVE-2010-2310); licensing terms disputed between reviewers ([AddictiveTips](https://www.addictivetips.com/net-admin/best-free-tftp-servers)) |
| tftpd-hpa | Linux | Distribution default; `--secure` chroot, regex remap rules, `--create` off by default, `blksize`/`tsize`/`timeout` ([man page](https://manpages.ubuntu.com/manpages/questing/man8/tftpd.8.html)) | No Windows build; with `--create`, new files are world-readable and writable unless `--umask` or `--permissive` is set; with `--secure`, path checks must be written as remap rules; RFC 7440 was an open enhancement request at Red Hat ([bug 1314744](https://bugzilla.redhat.com/show_bug.cgi?id=1314744)) |
| atftpd | Linux | Multicast, `windowsize`, the RFC set through 7440 ([ecosyste.ms](https://repos.ecosyste.ms/topics/tftp-protocol)) | Memory-safety history: stack overflow from a 3-byte error packet and a NULL dereference ([CVE-2019-11365, CVE-2019-11366](https://pulsesecurity.co.nz/advisories/atftpd-multiple-vulnerabilities)), remote DoS and `/etc/group` disclosure (CVE-2020-6097, CVE-2021-41054, CVE-2021-46671, [USN-6334-1](https://ubuntu.com/security/notices/USN-6334-1)) |
| dnsmasq | Linux | Read-only by design, which is the right default; PXE with its own DHCP ([netbeez](https://netbeez.net/blog/read-only-tftp-dnsmasq)) | No uploads at all, so it cannot collect configs; without `tftp-secure` it serves any file the process can read, and as root only world-readable files ([dnsmasq-discuss](https://lists.thekelleys.org.uk/pipermail/dnsmasq-discuss/2010q2/004017.html)) |
| Open TFTP Server | Windows, Linux | Multithreaded and single-port editions (the single-port one answers from port 69, which suits firewalls), block number rollover, interface binding ([OnWorks](https://www.onworks.net/software/linux/app-open-tftp-server?amp=1)) | Default install directory let a local user replace the binary and elevate ([CVE-2020-26130](https://www.incibe.es/index.php/en/incibe-cert/early-warning/vulnerabilities/cve-2020-26130)) |
| haneWIN TFTP | Windows | Access control by operation (upload or download), IP address and directory ([wingetly](https://www.wingetly.io/apps/hane-win/tftpserver)) | Commercial shareware; closed source |
| WDS TFTP (Windows Server) | Windows Server | Variable window extension and large block sizes for WinPE images ([HCL BigFix](https://help.hcl-software.com/bigfix/10.0/lifecycle/Lifecycle/OSD_Users_Guide/c_tuning_tftp_settings.html)) | Tied to Windows Deployment Services; not a general tool |
| Windows TFTP Client | Windows | Built into Windows | Optional feature, off by default, client only, no options |

Smaller Windows servers show the same traversal pattern: Distinct TFTP ([CVE-2012-6664](https://nvd.nist.gov/vuln/detail/CVE-2012-6664), with a
Metasploit module), TFTPdWin ([CVE-2007-2639](https://cve.cert.hr/cve/CVE-2007-2639)) and PacketTrap pt360
([CVE-2008-1310](https://nvd.nist.gov/vuln/detail/cve-2008-1310)).

Libraries: the main Go library, `github.com/pin/tftp/v3` (MIT), implements RFC 1350, 2347 and 2348, `tsize` on the
server side only, no `windowsize`, and an experimental single-port mode ([GitHub](https://github.com/pin/tftp)).

## 4. The threat picture

- Configuration theft over TFTP is an active nation-state technique. CISA and partners' July 2026 advisory
  [AA26-194A](https://www.cisa.gov/news-events/cybersecurity-advisories/aa26-194a) describes FSB Center 16 actors
  (Berserk Bear, Dragonfly, Static Tundra) scanning for default SNMP communities and exfiltrating device configurations
  over TFTP, and exploiting CVE-2018-0171 (Cisco Smart Install) and CVE-2008-4128. Mitigations listed include
  blocking TFTP at edge firewalls ([WaterISAC summary](https://www.waterisac.org/tlpclear-cisa-and-partners-urge-improved-router-hygiene-to-counter-russian-state-sponsored-targeting)).
  A server that collects configs is collecting the secrets in them.
- Reflection and amplification. Sieklik, Macfarlane and Buchanan measured an amplification factor of about 60 and
  about 599,600 publicly open TFTP servers; repeated retransmission of the first DATA packet adds to it. Akamai SIRT
  mitigated attacks using the method in 2016 ([SecurityWeek](https://www.securityweek.com/ddos-attacks-abuse-tftp-reflection-and-amplification),
  [Napier](https://napier-repository.worktribe.com/output/169028/evaluation-of-tftp-ddos-amplification-attack)).
- Path traversal, two decades running (section 3). CVE-2006-1951 is the instructive one: the server filtered `../`,
  and `....//` collapsed into `../` after filtering. String filtering is not containment.
- Memory corruption in C parsers fed by unauthenticated UDP (atftpd, SolarWinds).
- Writable roots. A server that lets a client overwrite the files it serves lets anyone on the segment replace a boot
  image, a firmware file or a phone configuration.
- Forgotten servers. A TFTP server left running after the job is an unauthenticated file service with no owner.

## 5. What is missing across the field

Assessment, from sections 3 and 4:

1. Containment by construction. Most servers validate path strings; the history says that fails. Nobody advertises
   kernel- or runtime-enforced containment on Windows.
2. Uploads that cannot hurt the served files. Write access is usually all-or-nothing on the same root; the safer
   pattern (a separate drop box, no overwrite, nothing served until a person promotes it) is not offered.
3. Evidence. No common server writes a structured record of each transfer with the file's hash, so a technician
   cannot show which image went to which device, or prove a collected config was not altered.
4. Time-boxed operation. No server is built for "serve for this job, then stop".
5. Amplification limits. Retransmission caps, per-source rate limits and refusing to listen on public addresses are
   rarely defaults.
6. A modern option set on Windows with a GUI. `windowsize`, MTU-aware `blksize`, correct rollover and files past
   4 GB exist, but not together in one signed, maintained Windows tool.
7. Help with what was collected. Collected configs routinely carry Cisco type 7 or type 0 passwords and SNMPv1/v2c
   communities; no TFTP server tells the technician, and configs land in world-readable folders.
8. A trustworthy supply chain. The Windows options are third-party binaries; for a client's assessor, ZTG cannot
   say where they came from or how they were built.
9. A built-in client. Windows ships none by default, so testing a server needs another download.

## 6. Compliance context

NIST SP 800-171 Rev 2, as a CMMC 2.0 Level 2 assessment uses it (Rev 3 renumbers these as 03.xx.xx and is not the
basis for CMMC assessments today):

| Practice | Relevance |
| --- | --- |
| CM.L2-3.4.6, CM.L2-3.4.7 | Least functionality; restrict nonessential protocols. A TFTP server left running is the classic finding; a time-boxed session is the control |
| AU.L2-3.3.1, AU.L2-3.3.2 | Audit records; trace actions. TFTP has no user identity, so the record can trace only a source address, and the design must say so |
| CM.L2-3.4.1, CM.L2-3.4.3 | Baselines and change tracking. Collected configs with hashes and diffs are evidence for both |
| SC.L2-3.13.1 | Boundary protection. Never on public addresses; the advisory above says block TFTP at the edge |
| SC.L2-3.13.8 | Cryptographic protection of CUI in transit. TFTP cannot provide it. TFTP must not carry CUI, and the tool should say so rather than imply otherwise |
| SI.L1-3.14.1 | Flaw remediation. Firmware delivery with a recorded hash supports it |
| AC.L1-3.1.1 | Limit access to authorized users and devices. A source allowlist is the only access control TFTP offers; it is not authentication |

Device configurations are usually not CUI, but they hold credentials, which makes them sensitive regardless.
