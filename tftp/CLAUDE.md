# CLAUDE.md (tftp)

ZTG TFTP, a TFTP server and client for technicians, in Go. Status: T1 and T2 merged; desktop, service and release packaging implemented, under validation. Repository-wide
conventions (one folder per tool, GitHub-hosted runners, actions pinned by SHA, dependencies public for at least 14
days) are in the root README.

Read `docs/design.md` before changing anything here, and `docs/research.md` for why. Its principles are requirements:

1. Read-only by default. Writes are opt-in, land only in a drop box outside the served root, never overwrite, and are
   never served until a person promotes them.
2. Contained by construction: every file access through `os.Root`, after the name policy in design section 5. Never
   validate paths by filtering strings.
3. Scope guard: listen only on private, CGNAT, link-local, unique local and loopback addresses unless `--allow-public`;
   sources outside the allowlist get no reply.
4. Time-boxed sessions by default; running indefinitely is a service-mode configuration setting only.
5. Bounded: retransmission, concurrency, rate, upload size and unverified-peer limits on by default (design section 6).
6. Every request, served or refused, gets an audit record. Never log file contents or detected secret values.
7. TFTP has no authentication or encryption. Never imply otherwise in help text, UI or docs; it must not carry CUI.
8. Every decoder reachable by unauthenticated packets has a fuzz target.
9. Clean room: written from the RFCs; no code from other TFTP servers.
10. One job: no bundled DHCP, DNS, SNTP or syslog server.

Dependencies: standard library first; justify each new one in the commit message. Writing style: no em dashes.
Slices (T1 to T6) are built one at a time, each reviewed before the next starts.

## Checks

The engine uses the standard library; platform upload controls use the Scanner-pinned x/sys version.
`.github/workflows/tftp.yml` runs gofmt, vet for Linux and
Windows, race tests, decoder fuzz seeds and bounded fuzzing, curl/tftp-hpa/atftp interop and cross builds for
Linux and Windows (amd64 and arm64), plus native Windows upload and DACL tests. See README.md for commands and current
scope. T2 is merged. Owner authorized all remaining slices on October 22, 2026.
