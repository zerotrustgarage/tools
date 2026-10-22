# ZTG Scanner

Network discovery and device inventory for technicians. Point it at the networks you choose and it reports every
device it finds: IP address, name, MAC address, manufacturer, device type, open services, and findings such as
Telnet exposure or hardware from NDAA section 889 covered vendors.

ZTG Scanner is ZTG's replacement for Advanced IP Scanner: source, build chain and signing owned by ZTG, no bundled
remote access software, and nothing installed on the hosts it scans. The design, safety model and roadmap are in
[docs/design.md](docs/design.md).

Status: engine and CLI (S1), Windows desktop app (S2) and SNMPv3 inventory (S3) are built.

## Usage

```
ztg-scanner scan 192.168.1.0/24
ztg-scanner scan 10.0.0.1-50 10.0.5.0/24 printer.lan
ztg-scanner scan --csv -o site.csv 10.10.0.0/22
ztg-scanner scan --json -o site.json --ports 22,80,443,8000-8100 192.168.10.0/24
ztg-scanner vendor 00:1b:21:12:34:56
```

```
STATUS  IP            NAME              TYPE              MAC                VENDOR                 PORTS              FLAGS
up      192.168.1.1   gw.office.lan     Firewall          00:09:0f:aa:bb:01  Fortinet, Inc.         53 80 443
up      192.168.1.20  FRONTDESK-PC      Windows host      00:1b:21:aa:bb:cc  Intel Corporate        135 139 445 3389
up      192.168.1.31  Office-Printer    Printer           30:05:5c:11:22:33  Brother Industries...  80 443 631 9100
up      192.168.1.40  NVR               IP camera / NVR   44:19:b6:01:02:03  Hangzhou Hikvision...  80 554 8000         H:ndaa-889
```

(Illustrative output.)

Scan flags (`ztg-scanner scan -h`):

| Flag                 | Default         | Meaning                                                        |
| -------------------- | --------------- | -------------------------------------------------------------- |
| `--json`, `--csv`    | table           | Output format                                                  |
| `-o FILE`            | stdout          | Write results to a file                                        |
| `--all`              | off             | Include addresses that did not answer                          |
| `--ports LIST`       | 40 common ports | TCP ports and ranges, up to 1024                               |
| `--no-ports`         | off             | Discovery only                                                 |
| `--no-banners`       | off             | Open ports without service identification                      |
| `--no-names`         | off             | Skip reverse DNS, NetBIOS and mDNS                             |
| `--no-ssdp`          | off             | Skip the UPnP multicast search                                 |
| `--concurrency N`    | 128             | Hosts scanned in parallel                                      |
| `--timeout D`        | 800ms           | TCP connect timeout                                            |
| `--max-hosts N`      | 65536           | Refuse larger target lists                                     |
| `--allow-public`     | off             | Permit public address space (you must be authorized)           |
| `--snmp-user NAME`   | off             | Add SNMPv3 inventory (authPriv, read-only) as this user        |
| `--snmp-auth P`      | SHA-256         | SHA, SHA-224, SHA-256, SHA-384 or SHA-512                      |
| `--snmp-priv P`      | AES             | AES, AES-192 or AES-256                                        |

SNMPv3 passphrases are prompted for with echo off, or read from `ZTG_SNMP_AUTH_PASS` and `ZTG_SNMP_PRIV_PASS` for
unattended runs. They are never accepted as flags and never written to output.

## What it learns

Liveness from ICMP, ARP (on-link) and TCP (an open port or a reset); MAC vendor from the IEEE registry with
randomized-MAC detection; names from reverse DNS, NetBIOS, mDNS and UPnP; services from banners, HTTP titles and
realms, and TLS certificates; device type with the reason it was inferred; findings. See the probe catalogue in
[docs/design.md](docs/design.md#4-probe-catalogue-s1).

No administrator rights are needed on Windows. On Linux, ICMP needs `net.ipv4.ping_group_range` to include your group,
or root / `CAP_NET_RAW`; without it, liveness falls back to ARP and TCP and the scan says so.

## Desktop app

`app/` is the Windows desktop app: the same engine with a live results grid, filters, a detail pane for every device,
SNMPv3 settings and CSV or JSON export. Build it from any platform:

```
cd app
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags desktop,production -ldflags "-H windowsgui" -o ZTGScanner.exe .
go test ./...                                   # bound app logic
NODE_PATH=$(npm root -g) node uitest/run.mjs    # UI in headless Chromium (needs playwright)
```

It needs the Microsoft Edge WebView2 runtime, which Windows 10 and 11 include.

## Building

Go 1.26 or later.

```
go test ./...
go build -o ztg-scanner ./cmd/ztg-scanner
GOOS=windows GOARCH=amd64 go build -o ztg-scanner.exe ./cmd/ztg-scanner
go generate ./internal/oui     # refresh the IEEE MAC vendor table
```

Set the version with `-ldflags "-X main.version=1.0.0"`.

## Data

The MAC vendor table is generated from the IEEE Registration Authority's public MA-L, MA-M, MA-S and IAB listings
(`internal/oui/data/oui.txt.gz`). Review IEEE's terms before redistributing the generated table outside ZTG.
