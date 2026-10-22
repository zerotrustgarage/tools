# ZTG TFTP

A TFTP server and client for technicians. T1 is implemented and ready for review: a read-only, time-boxed server,
CLI `get` and `put`, an owned protocol engine and JSON Lines audit records. The plan is in
[docs/design.md](docs/design.md) and the research behind it in [docs/research.md](docs/research.md).

TFTP has no authentication or encryption. Use it only on networks you control, never for CUI, and prefer SCP,
SFTP or HTTPS wherever the device supports them. A source allowlist restricts network access; it does not identify
a user.

## Build

Go 1.26 or later, with no third-party dependencies. CI uses the checksum-verified Go 1.26.10 toolchain in
`../scripts/ci/install-go.sh`.

```sh
cd tftp
go build -trimpath -o dist/ztg-tftp ./cmd/ztg-tftp
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o dist/ztg-tftp.exe ./cmd/ztg-tftp
```

Set a version with `-ldflags '-X main.version=1.0.0'`. These are console binaries; Windows builds are unsigned.
The desktop app, service mode, signed installers and release workflow belong to later slices.

## Use

Flags go before positional arguments. The server refuses every upload request in T1; `put` sends a local file to
another server that permits uploads.

```sh
ztg-tftp serve ./images
ztg-tftp serve --for 15m --bind 192.168.1.10 --allow 192.168.1.0/24 ./images
ztg-tftp serve --single-port --blksize-max 8192 ./pxe
ztg-tftp serve --bind :: --port 6969 ./images
ztg-tftp get 192.168.1.10 firmware.bin ./download.bin
ztg-tftp get --port 6969 --timeout 1 127.0.0.1 firmware.bin ./download.bin
ztg-tftp put --windowsize 8 192.168.1.50 firmware.bin
ztg-tftp version
```

Defaults:

- Read-only sessions last 60 minutes. `--for` must be positive; Ctrl+C stops active transfers too.
- The IPv4 wildcard expands to private, CGNAT, link-local and loopback interface addresses. Use `--bind ::` for
  eligible IPv6 addresses, or choose a single local IP. Public local IPs require `--allow-public`.
- Sources must match the bound interface subnets, or the explicit comma-separated `--allow` CIDRs. Other sources
  get no response. Interface scope and source subnets are a snapshot taken at session startup.
- Port 69 normally needs `CAP_NET_BIND_SERVICE` on Linux. Use a high `--port` for unprivileged testing. Windows
  Firewall must permit inbound UDP for the executable on the intended private network; T1 changes no firewall rules.
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

By default the audit file is `<root parent>/.ztg-tftp-sessions/<session directory id>/audit.jsonl`. `--audit-dir`
chooses a directory outside the served root; an existing `audit.jsonl` is never replaced. New Unix directories and
files use modes 0700 and 0600. On Windows they inherit the selected directory's access controls.

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
```

`.github/workflows/tftp.yml` runs those checks on GitHub-hosted Linux, installs curl, tftp-hpa and atftp for
interoperability, and cross-builds Windows and Linux for amd64 and arm64. Local tests skip external clients that are
not installed. Simulated transports cover loss, duplication, reordering, retransmission, final ACK recovery, window
boundaries, rollover, empty files and exact block multiples. A sparse file past 4 GiB checks 64-bit server `tsize`
negotiation; the default suite does not stream all 4 GiB. Real sockets cover IPv4, IPv6, both listening modes, legacy
client fallback, scope refusal, audit hashes and expiry during an active transfer.

## Next slices

T2 adds opt-in uploads into a separate drop box, atomic commits, caps and findings on collected configs. T3 adds the
Windows desktop app. T4 adds service configuration and log forwarding. T5 adds packaging, signing, SBOM and release
provenance. Each slice is reviewed before the next starts; none of these later features is present in T1.
