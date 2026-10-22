# ZTG TFTP

A TFTP server and client for technicians. T1 and T2 are implemented and ready for review: a time-boxed server,
CLI `get`, `put` and `findings`, an owned protocol engine, opt-in collection into a separate drop box, configuration
findings, change tracking and JSON Lines audit records. The plan is in
[docs/design.md](docs/design.md) and the research behind it in [docs/research.md](docs/research.md).

TFTP has no authentication or encryption. Use it only on networks you control, never for CUI, and prefer SCP,
SFTP or HTTPS wherever the device supports them. A source allowlist restricts network access; it does not identify
a user.

## Build

Go 1.26 or later. The protocol engine uses the standard library; `golang.org/x/sys` v0.49.0 supplies platform file
permissions, locking and free-space accounting (the same pinned version as Scanner). CI uses the checksum-verified
Go 1.26.10 toolchain in `../scripts/ci/install-go.sh` on Linux.

```sh
cd tftp
go build -trimpath -o dist/ztg-tftp ./cmd/ztg-tftp
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o dist/ztg-tftp.exe ./cmd/ztg-tftp
```

Set a version with `-ldflags '-X main.version=1.0.0'`. These are console binaries; Windows builds are unsigned.
The desktop app, service mode, signed installers and release workflow belong to later slices.

## Use

Flags go before positional arguments. The server refuses uploads unless `--upload` chooses a separate drop box.
`put` sends a local file to a server that permits uploads.

```sh
ztg-tftp serve ./images
ztg-tftp serve --for 15m --bind 192.168.1.10 --allow 192.168.1.0/24 ./images
ztg-tftp serve --single-port --blksize-max 8192 ./pxe
ztg-tftp serve --upload ./collected --allow 10.20.0.0/24 ./images
ztg-tftp serve --bind :: --port 6969 ./images
ztg-tftp get 192.168.1.10 firmware.bin ./download.bin
ztg-tftp get --port 6969 --timeout 1 127.0.0.1 firmware.bin ./download.bin
ztg-tftp put --windowsize 8 192.168.1.50 firmware.bin
ztg-tftp findings ./collected
ztg-tftp version
```

Defaults:

- Read-only sessions last 60 minutes. `--for` must be positive; Ctrl+C stops active transfers too.
- The IPv4 wildcard expands to private, CGNAT, link-local and loopback interface addresses. Use `--bind ::` for
  eligible IPv6 addresses, or choose a single local IP. Public local IPs require `--allow-public`.
- Sources must match the bound interface subnets, or the explicit comma-separated `--allow` CIDRs. Other sources
  get no response. Interface scope and source subnets are a snapshot taken at session startup.
- Port 69 normally needs `CAP_NET_BIND_SERVICE` on Linux. Use a high `--port` for unprivileged testing. Windows
  Firewall must permit inbound UDP for the executable on the intended private network; the CLI changes no firewall rules.
- Served files open through `os.Root`, after a portable name policy. Escaping symlinks, traversal, alternate data
  streams, reserved device names and non-regular files are refused. `--ignore-case` permits case-insensitive lookup
  but refuses ambiguous matches. A leading slash is removed and backslashes become slashes for PXE clients.
- Downloads commit only when complete and never overwrite an existing local destination. Temporary downloads use
  owner-only Unix permissions. The destination filesystem must support hard links for the atomic commit.

Every request produces a completion or refusal audit record. Refusals are capped at ten records per source per
second, with suppressed counts on the next permitted record or at shutdown. A failed audit write stops the session.
Logs contain names, source addresses, negotiated numeric options, byte counts, duration, outcome and SHA-256;
they never contain file contents or peer error messages. SHA-256 and byte counts describe original local file bytes
(before netascii encoding, or after decoding). In an aborted read, they describe the prefix read by the sender,
which may include unacknowledged bytes.

Uploads add `stored_as`, finding codes, `changed_since` and a `changed` boolean when a previous copy exists.
The comparison is against the last successfully committed copy from the same source IP and normalized remote name;
it survives server restarts. A source IP is not an authenticated device identity.

By default the audit file is `<root parent>/.ztg-tftp-sessions/<session directory id>/audit.jsonl`. `--audit-dir`
chooses a directory outside the served root; an existing `audit.jsonl` is never replaced. New Unix directories and
files use modes 0700 and 0600. On Windows they inherit the selected directory's access controls.

## Upload collection and findings

The drop box and served root must be separate directory trees, including through symlinks and root aliases. All
upload access uses `os.Root`. Uploads never overwrite files and never become readable through the TFTP server.
Files and directories in the drop box use owner-only permissions: Unix modes 0600/0700 and a protected Windows
DACL granting only the current user access. Existing drop box permissions are restricted when it opens. The
filesystem must support hard links (for example NTFS on Windows) for atomic publication without replacement.

The layout is `<drop>/<UTC date>/<source IP>/<HHMMSS>-<random id>/payload/<requested name>`, with a `receipt.json`
beside `payload`. IPv6 colons become underscores in directory names; receipts keep the original IP. The extra
transaction directory avoids collisions and keeps a 255-byte filename within filesystem limits. The receipt
contains hashes, finding codes and line numbers, never configuration text or credential values. `.index/` holds
private comparison metadata, `.incoming/` holds temporary uploads, and `.lock` prevents two servers from writing
the same drop box. Stale incoming data is removed on the next exclusive open after a crash. The comparison index
is rebuilt from committed receipts at startup, including files published just before a crash.

The default upload cap is 256 MiB (`--upload-max`, bytes), enforced before acceptance when `tsize` is present and
while streaming in all cases. The default free-space floor is 1 GiB (`--free-floor`, bytes; must be positive).
Concurrent transfers reserve declared sizes, or the full cap for peers omitting `tsize`, so they cannot collectively
spend that floor. Free space is checked again on each write. Errors, size mismatches, timeouts and session shutdown
remove unfinished upload files. The final ACK is sent only after the payload and receipt are committed. A committed
file remains collected if a final ACK is lost or the session ends during the final retry window.

Configuration checks run on each upload and on `findings DROP_BOX`, which emits one JSON object per committed
receipt. Cisco IOS/IOS XE fixtures cover cleartext passwords, type 7 passwords/keys, SNMP communities, VTY Telnet,
cleartext HTTP and type 5 hashes. Smart Install is a verification finding only when a switch provisioning line is
present and `no vstack` is absent; the tool does not infer a switch from arbitrary configuration text.
Findings report rule, severity and line number, never matched values. Binary files with NUL bytes are skipped.
Scanning is capped at 16 MiB, 64 KiB per line and 1000 findings; `truncated` reports incomplete checks. Findings are
heuristics for these fixtures, not proof that a device or configuration is secure. Collected files stay until an
operator deletes them; no automatic retention or promotion is implemented.

## Protocol and limits

RFC 1350 octet and netascii, RFC 2347 negotiation, `blksize` (RFC 2348), `timeout` and 64-bit `tsize` (RFC 2349),
and `windowsize` (RFC 7440). Block numbers wrap from 65535 to 0. Unknown options are ignored; netascii omits
`tsize`, which RFC 2349 defines for octet transfers. The server supports ordinary per-transfer ports and optional
single-port operation. Binding individual local addresses keeps replies on the requested destination address in
both modes, without wildcard packet-info handling.

The block cap defaults to 1468 bytes and the window cap to 16 blocks, configurable up to 65464 and 64. The client
requests 1468-byte blocks, eight-block windows and a three-second timeout. Peers declining options use 512-byte
lockstep transfers. Partial cumulative ACKs advance the window; missing DATA is resent on timeout. Duplicate ACKs
never trigger DATA retransmission.

The server permits 64 concurrent transfers globally and eight per source, and 20 new requests per source per second.
Unacknowledged peers share a 256 KiB/s output budget. Only two retransmissions are allowed before the first ACK,
then five per stalled transfer step. An option-bearing RRQ receives only OACK until ACK 0 arrives. Source accounting
is capped at 4096 entries. Single-port mode permits one active transfer per peer UDP endpoint.

## Checks

```sh
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
go test -race ./...
go test ./internal/wire -run '^$' -fuzz '^FuzzDecode$' -fuzztime 10s
go test ./internal/wire -run '^$' -fuzz '^FuzzOptions$' -fuzztime 10s
go test ./internal/store -run '^$' -fuzz '^FuzzName$' -fuzztime 10s
go test ./internal/xfer -run '^$' -fuzz '^FuzzNetascii$' -fuzztime 10s
go test ./internal/findings -run '^$' -fuzz '^FuzzFindings$' -fuzztime 10s
```

`.github/workflows/tftp.yml` runs those checks on GitHub-hosted Linux, installs curl, tftp-hpa and atftp for
interoperability, and cross-builds Windows and Linux for amd64 and arm64. Local tests skip external clients that are
not installed. Simulated transports cover loss, duplication, reordering, retransmission, final ACK recovery, window
boundaries, rollover, empty files and exact block multiples. A sparse file past 4 GiB checks 64-bit server `tsize`
negotiation; the default suite does not stream all 4 GiB. Real sockets cover IPv4, IPv6, both listening modes, legacy
client fallback, scope refusal, audit hashes and expiry during an active transfer.
Upload tests add concurrency, cleanup, stream and declared size caps, disk-space reservations, unchanged/changed
copies across restart, secret-free metadata and curl/tftp-hpa/atftp uploads. A GitHub-hosted Windows job executes
upload tests and checks the native owner-only DACLs, locking and atomic commits.

## Next slices

T3 adds the Windows desktop app. T4 adds service configuration and log forwarding. T5 adds packaging, signing, SBOM
and release provenance. These features are not present. Each slice is reviewed before the next starts.
