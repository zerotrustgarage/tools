# ZTG Scanner

Network discovery and device inventory for technicians. Point it at the networks you choose and it reports every
device it finds: IP address, name, MAC address, manufacturer, device type, open services, and findings such as
Telnet exposure or hardware from NDAA section 889 covered vendors.

ZTG Scanner is ZTG's replacement for Advanced IP Scanner: source, build chain and signing owned by ZTG, no bundled
remote access software, and nothing installed on the hosts it scans. The design, safety model and roadmap are in
[docs/design.md](docs/design.md).

Status: engine and CLI (S1), Windows desktop app (S2), SNMPv3 inventory (S3) and the packaging and release
pipeline (S4) are built. Owner signing setup is complete; the first signed Scanner release still needs a successful run of
the release workflow, using the shared identity in [../docs/signing.md](../docs/signing.md). Actions, site inventories and scan comparisons (S5) are built.

## Usage

```
ztg-scanner scan 192.168.1.0/24
ztg-scanner scan 10.0.0.1-50 10.0.5.0/24 printer.lan
ztg-scanner scan --csv -o site.csv 10.10.0.0/22
ztg-scanner scan --json -o site.json --ports 22,80,443,8000-8100 192.168.10.0/24
ztg-scanner scan --html --site "Example Clinic / Main office" -o clinic.html 10.20.0.0/24
ztg-scanner report -o clinic.html clinic.json
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
| `--html`             | off             | Write a self-contained, printable client inventory report      |
| `--site LABEL`       | empty           | Client/site label for JSON and HTML reports                    |
| `-o FILE`            | stdout          | Write results to a file                                        |
| `--all`              | off             | Include addresses that did not answer                          |
| `--ports LIST`       | 40 common ports | TCP ports and ranges, up to 1024                               |
| `--no-ports`         | off             | Discovery only                                                 |
| `--no-banners`       | off             | Open ports without service identification                      |
| `--no-names`         | off             | Skip reverse DNS, NetBIOS and mDNS                             |
| `--no-ssdp`          | off             | Skip the UPnP multicast search                                 |
| `--mode M`           | default         | `default` or `gentle` (lower rate, fewer probes; see below)    |
| `--concurrency N`    | 128             | Hosts scanned in parallel                                      |
| `--timeout D`        | 800ms           | TCP connect timeout                                            |
| `--max-hosts N`      | 65536           | Refuse larger target lists                                     |
| `--allow-public`     | off             | Permit public address space (you must be authorized)           |
| `--snmp-user NAME`   | off             | Add SNMPv3 inventory (authPriv, read-only) as this user        |
| `--snmp-auth P`      | SHA-256         | SHA, SHA-224, SHA-256, SHA-384 or SHA-512                      |
| `--snmp-priv P`      | AES             | AES, AES-192 or AES-256                                        |

SNMPv3 passphrases are prompted for with echo off, or read from `ZTG_SNMP_AUTH_PASS` and `ZTG_SNMP_PRIV_PASS` for
unattended runs. They are never accepted as flags and never written to output.

### Gentle mode

`--mode gentle` is a conservative profile for fragile OT, medical or old printer networks. You choose it; the
scanner never switches to it by itself. It is defined once in `internal/scan` and the Desktop app's **Scan mode**
setting uses the same definition, so both build the identical plan:

| Setting                 | Default                | Gentle                                                         |
| ----------------------- | ---------------------- | -------------------------------------------------------------- |
| Hosts in parallel       | 128                    | 4                                                              |
| Connections per host    | 16                     | 2                                                              |
| Probe start rate        | unlimited              | 10 per second in total (one every 100 ms)                      |
| Echo requests per host  | 2                      | 1                                                              |
| Connect timeout         | 800 ms                 | 1500 ms                                                        |
| TCP ports               | 40 common ports        | 22, 80, 443, 445, 515, 631, 8080, 9100                         |
| Other probes            | banners, NetBIOS, mDNS, SSDP | none; reverse DNS and ICMP/ARP liveness only. SNMP is refused |

`--concurrency` may only ask for fewer hosts (at most 4) and `--ports` only for a subset of the eight gentle ports;
anything else is rejected with an error rather than silently clamped. `--timeout` is free (any positive value),
because it changes neither the rate nor the concurrency. Cancel (Ctrl-C, or Stop in the app) works at any point,
including while a scan is waiting for its next probe slot.

The tradeoff: gentle scans are slower and identify less (no service banners, names from NetBIOS, mDNS or UPnP, or
SNMP inventory). Gentle mode reduces the load a scan creates; it does not make a scan harmless to every device.
Get approval for fragile networks and watch them while you scan.

## What it learns

Liveness from ICMP, ARP (on-link) and TCP (an open port or a reset); MAC vendor from the IEEE registry with
randomized-MAC detection; names from reverse DNS, NetBIOS, mDNS and UPnP; services from banners, HTTP titles and
realms, and TLS certificates; device type with the reason it was inferred; findings. See the probe catalogue in
[docs/design.md](docs/design.md#4-probe-catalogue-s1).

No administrator rights are needed on Windows. On Linux, ICMP needs `net.ipv4.ping_group_range` to include your group,
or root / `CAP_NET_RAW`; without it, liveness falls back to ARP and TCP and the scan says so.

## Desktop app

`app/` is the Windows desktop app: the same engine with a live results grid, filters, a detail pane for every device,
SNMPv3 settings, a Gentle scan mode (the settings and tradeoff are shown in the settings panel) and CSV or JSON
export. Build it from any platform:

```
cd app
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags desktop,production -ldflags "-H windowsgui" -o ZTGScanner.exe .
go test ./...                                   # bound app logic
NODE_PATH=$(npm root -g) node uitest/run.mjs    # UI in headless Chromium (needs playwright)
```

It needs the Microsoft Edge WebView2 runtime, which Windows 10 and 11 include.

The unified app/title bar contains the build number, Theme selector, Settings, update checks, exports and
window controls. Drag its empty space to move the window or double-click to maximize. Choose **Theme: Dark**
or **Light** explicitly; **System** follows Windows. The choice is remembered, and native window styling follows
it too. Version 0.7.0 upgrades earlier test installers so Windows replaces the versioned executables.

## Site inventories and device actions

1. Enter a site label (client and office), then scan. Choose **New site** when moving to a different client so its
   device notes cannot carry across. Changing the label renames the current inventory.
2. Select a device and apply notes, comma-separated tags and a favorite. Tags such as `owner:Alice`, `location:MDF`
   and `cui:inside` make the results a useful starting point for an asset inventory. Search includes notes and tags;
   the Favorites filter and the optional Favorite, Tags and Notes columns work with the normal grid controls.
3. **Save scan** writes a local JSON file containing the site, every visited address (even when filtered out), the
   observations and annotations. **Open scan** restores it without running probes. Keep separate files per site and
   date. Earlier Scanner JSON exports also open. Files are limited to 64 MiB and 65,536 addresses; invalid files
   leave the current inventory in place. Writes replace an existing file only after a complete, flushed save.
4. **Compare with…** selects an earlier JSON file as the baseline for the current scan or loaded inventory. Different
   nonempty site labels are refused. Results show new, missing, changed and unchanged devices, plus devices whose
   absence could not be assessed. The comparison shows old and new field values and detects address moves when a
   non-randomized MAC is unique in both scans. MAC replacements at the same IP appear as changed occupants.

Missing means a previously observed address did not answer in a completed scan, not proof a device was removed.
Unvisited addresses, canceled scans and ambiguous identities never produce a missing-device claim. Changes reflect
observations: different probe settings or reachability can change the evidence without a device being reconfigured.
The comparison panel displays up to 500 rows per category and reports the full counts.

Notes follow confirmed address moves between consecutive scans, but never transfer to a different MAC at the same
IP or to a duplicate MAC at a new address. For an unresponsive device, a previously learned MAC is labeled
**Last known MAC**, kept separate from current evidence, and retained with notes and favorites. Full CSV exports
append `notes`, `tags`, `favorite` and `last_known_mac`; JSON preserves the full annotation history. These files are
saved only when you choose to save or export. Browser settings contain no inventory or SNMP passphrases. Opening
an inventory or starting a new site clears the SNMP passphrase fields and disables SNMP. The native
close button and Alt+F4 offer Cancel when the current scan has unsaved changes.

The device pane offers browser access, IP/MAC copy, SSH, RDP, ping, traceroute and Wake-on-LAN. SSH and RDP launch
the installed Windows clients only when their standard port was observed open. Ping and traceroute show bounded
diagnostic output in the app. Programs use fixed system paths and numeric result addresses, never names or banners
as commands. A public address loaded from a file requires authorization before these connection actions.
Wake-on-LAN asks before sending one UDP magic packet and is limited to directly connected internal IPv4 networks;
it can use the last known MAC of an offline favorite. Nothing is installed on the scanned device, and no credentials
are collected by Scanner for these actions. Scheduled scans remain planned.

## Open with commands

Choose **Settings**, then **Open with commands** to define up to 20 commands of your own, for example PuTTY, mstsc
or an RMM console link. A program command is the full path to an installed `.exe` plus one argument per line; a web
command is an `https://` address. Use `{ip}` for the device address and `{hostname}` for its reported name, for
example `-ssh` and `admin@{hostname}` as two arguments. **Preview** shows exactly what would run for a test address
and name without starting anything. Choose a command from **Open with** in the device pane, or right-click a result.

Commands are stored only in the current Windows user's desktop preferences and are never written to scan files or
exports. Each argument stays one argument and no shell is involved. If a device has no valid DNS hostname, commands
that use `{hostname}` are unavailable for it rather than adjusted. `cmd.exe`, PowerShell and other shells or script
hosts are refused, there is no password placeholder, and obvious password options such as `-pw` are refused: let the
program or site ask for the credential. A web address needs a fixed `https://` host, and values placed in its path or
query are URL-escaped. A public address needs the same authorization as the other connection actions.

## Saved target sets

Choose **Target sets** beside **My networks** to keep named network lists for each client or site. Fill in the
client/site, set name and networks, then choose **Save set**. Sets stay in the current Windows user's desktop
preferences across launches and upgrades; portable packaging uses the same preferences. Up to 50 sets are kept.
Saving an existing client/name asks before replacing it. Deleting a saved set also asks for confirmation.

**Import file** reads a UTF-8 `.txt` list into the editable draft. Use IPv4 addresses, CIDR blocks, ranges or
hostnames separated by spaces, newlines, commas or semicolons. Blank lines and `#` comments are supported:

```text
# Clinic, main office
10.20.0.0/24
10.20.5.10-25  # Printers
printer.clinic.lan
```

Files are limited to 64 KiB and 4,096 target specifications. Importing does not save a set or replace the current
scan. Choose **Use targets** to apply a saved set or draft, then press **Scan** when ready. Using targets starts
a new site inventory, asks before discarding unsaved results, and clears SNMP credentials and the SNMP switch.
Sets contain only the site, name and target list. They never remember public-range authorization; the usual
authorization prompt and host limit still apply when scanning. No hostname is resolved while saving or importing.

## Client inventory reports

Choose **Export → HTML, client report** to save a branded report of the entire recorded inventory, including
unresponsive addresses, device notes and tags, findings, identification evidence and available model/serial/firmware
details. Grid filters and the **Show unresponsive** switch do not limit the report. The report includes the site,
requested targets, scan times, warnings and counts of the recorded observations. Canceled or unfinished scans are
labeled as partial or incomplete, and historical MAC addresses are kept separate from current observations.

The HTML file opens offline in a browser and uses the ZTG palette and Scanner logo. It contains no scripts, remote
images or external fonts, and device-supplied text is escaped. Print from the browser to paper or **Save as PDF**;
printing uses the light palette. Exporting saves the file without opening a browser or running more probes.

The CLI can create the same report during a scan with `scan --html --site "Client / office" -o report.html`.
To report on an existing saved scan without contacting any devices, use
`ztg-scanner report -o report.html inventory.json`. The offline command accepts `--site` to override the report's
client/site label. A report summarizes observed evidence; a clean findings list is not a compliance assessment.
XLSX export and RMM/PSA-specific CSV layouts remain planned.

## Windows packages and releases

`installer/` builds a Windows x64 MSI for managed deployment and a portable ZIP. Both contain the desktop app,
CLI, license texts, an IEEE data attribution, and a CycloneDX 1.6 SBOM generated from the modules actually linked
into the two executables. The MSI adds an all-users Start menu shortcut and supports repair and major upgrades;
it refuses downgrades and installs no service, driver, scan schedule, firewall exception or PATH entry.
The MSI needs administrator rights to install; scanning and using the portable tools do not.

The desktop app requires the Microsoft Edge WebView2 Runtime. The MSI checks for it and fails before installation
if it is absent. Deploy the machine-wide runtime first when using Intune, Group Policy or an RMM. No runtime is
downloaded or bundled by the MSI. The CLI works without WebView2. Portable packaging uses the current user's
normal desktop preferences; it does not isolate settings. Uninstalling preserves exported scans and preferences.

To build unsigned packages locally on Linux (Go, Node 22+, `wixl`, `msitools`, `zip`):

```bash
version=$(cat scanner/VERSION)       # from the repository root
scanner/app/build-windows.sh "$version" amd64 .release/scanner
node scanner/ci/metadata.mjs .release/scanner "$version"
cp scanner/installer/README.txt .release/scanner/
scanner/installer/build-msi.sh .release/scanner "$version" dist/ZTG-Scanner-x64.msi
scanner/installer/portable.sh .release/scanner dist/ZTG-Scanner-x64-portable.zip
node --test scanner/ci/metadata.test.mjs
SCANNER_PAYLOAD="$PWD/.release/scanner" node --test scanner/ci/packaging.test.mjs  # also needs osslsigncode, openssl, unzip
```

`scanner/VERSION` is the release version (numeric `X.Y.Z`, within Windows Installer limits). The release workflow
`.github/workflows/release-scanner.yml` runs manually from `main` and refuses an existing `scanner-v<version>` tag.
It checks that two builds produce identical unsigned executables. `-trimpath`, a fixed version and commit, disabled
VCS stamping and an empty Go build ID remove local paths and build IDs. This reproducibility claim covers the
unsigned executables with the same source, toolchain and module versions, not MSI container metadata or signatures
with live timestamps. Portable archives have fixed entry timestamps and permissions.

The workflow separates compilation, signing through `tools-signing`, Windows validation and publication. It signs
and verifies both EXEs before embedding them in the MSI, then signs and verifies the MSI. Windows must validate the
signatures and launch both installed and portable apps before publication. Downloads include `SHA256SUMS.txt`, the
SBOM and notices, with provenance attested for every asset. See [../docs/signing.md](../docs/signing.md) for setup
and verification. PR CI uses unsigned packages and also checks MSI upgrades and downgrade refusal on `windows-2025`.
The upgrade test builds different old/new executable versions and verifies replacement. Windows CI and the
release test use Windows UI Automation to operate the real app's Theme selector, check the unified controls,
and verify dark/light screen pixels. No remote debugging is enabled. To replace an installed test build,
increase the numeric `scanner/VERSION`; changing a filename or commit label does not create an MSI upgrade.

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

## Updates

The desktop toolbar offers **Check for updates**; the CLI supports `ztg-scanner check-updates [--json]`.
Checks contact GitHub only when requested and open official release downloads without installing
anything. Offline errors and unpublished releases are shown explicitly. See
[../docs/updates.md](../docs/updates.md).
