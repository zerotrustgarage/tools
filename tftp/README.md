# ZTG TFTP

A TFTP server and client for technicians: serve a firmware image, boot files or phone configurations, or collect
device configurations, for the length of a job and no longer.

ZTG TFTP is ZTG's replacement for Tftpd64 and SolarWinds TFTP Server: source, build chain and signing owned by ZTG,
and safer defaults than any of them.

- Read-only unless you ask for uploads; uploads go to a separate drop box and never overwrite anything.
- Path containment enforced by the Go runtime (`os.Root`), not string filters, after twenty years of traversal CVEs in
  other TFTP servers.
- Sessions stop on their own (60 minutes by default), and the server listens only on private addresses.
- An audit record for every request, with the SHA-256 of every file served or collected.
- Collected configurations are checked for cleartext and reversible passwords, SNMP communities, Smart Install and
  Telnet, and compared with the previous copy.
- Modern options (`blksize`, `tsize`, `timeout`, `windowsize`), correct block rollover, files past 4 GiB, IPv6, and an
  optional single-port mode for firewalls.

TFTP has no authentication and no encryption. Use it only on networks you control, never for CUI, and prefer SCP,
SFTP or HTTPS wherever the device supports them.

Status: proposed. The design is in [docs/design.md](docs/design.md) and the market research behind it in
[docs/research.md](docs/research.md).
