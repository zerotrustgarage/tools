# CLAUDE.md

Standalone ZTG tools, one directory per tool. This repository is unrelated to the ZTG platform repositories
(`infrasec`, `jackpoint`, `outpost`, `ztg`): do not import from them or apply their rules here.

## Tools

- `scanner/`: ZTG Scanner, a network discovery and inventory tool (Go). Read `scanner/docs/design.md` before changing
  it: the safety principles (identify never intrude, scope guard, no privileges, clean room) are requirements.

## Rules for every tool

1. Clean room. Do not copy code, data or assets from commercial tools (Advanced IP Scanner, Radmin and others) or from
   projects whose licenses do not allow closed redistribution (for example Nmap's NPSL data files). Write from
   public specifications and record data sources.
2. Safe by default. Anything that authenticates, enumerates shares or users, or changes a remote system is opt-in per
   run and documented as such. Never store credentials on disk or in exports.
3. Minimal dependencies. Prefer the standard library; justify each new dependency in the commit message.
4. Tests with every change; CI must pass. Go code is `gofmt`-clean and passes `go vet` for linux and windows.
5. Writing style: no em dashes; plain, specific language.
6. Conventional commits.

## Scanner checks

```
cd scanner
gofmt -l .            # must print nothing
go vet ./...
GOOS=windows go vet ./...
go test ./...
```
