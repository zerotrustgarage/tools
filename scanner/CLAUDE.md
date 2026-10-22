# CLAUDE.md (scanner)

ZTG Scanner, a network discovery and inventory tool in Go. Repository-wide conventions (one folder per tool,
GitHub-hosted runners, actions pinned by SHA, dependencies public for at least 14 days) are in the root README.

Read `docs/design.md` before changing anything here. Its safety principles are requirements:

1. Identify, never intrude. Default probes are single standard exchanges; nothing authenticates, writes or exploits.
   The only credentialed probe is SNMPv3: opt-in per scan, authPriv, GET and GETNEXT only.
2. Scope guard. Private, CGNAT, link-local and loopback space only, unless the user explicitly allows public ranges.
3. No privileges required on Windows.
4. Clean room. No code, data or assets from commercial tools (Advanced IP Scanner, Radmin) or from projects whose
   licenses do not allow closed redistribution (for example Nmap's NPSL data files). Record data sources.
5. Credentials are never stored on disk, in settings or in exports, and never accepted as command-line flags.
6. Device-supplied text is untrusted: the desktop UI inserts it with `textContent` only.

Dependencies: standard library first; justify each new one in the commit message. Writing style: no em dashes.

## Checks

```
gofmt -l .                       # must print nothing (here and in app/)
go vet ./... && GOOS=windows go vet ./...
go test -race ./...
cd app && go vet ./... && GOOS=windows CGO_ENABLED=0 go vet ./... && go test -race ./...
NODE_PATH=$(npm root -g) node uitest/run.mjs   # needs playwright
```

Packaging checks run from the repository root: `node --test scanner/ci/metadata.test.mjs`, then
`SCANNER_PAYLOAD=<Windows build directory with metadata> node --test scanner/ci/packaging.test.mjs`.
See `scanner/README.md` for preparing that payload. They require wixl, msitools, osslsigncode,
openssl, zip and unzip. `scanner/ci/test-windows.ps1` runs the portable/MSI lifecycle on Windows.
