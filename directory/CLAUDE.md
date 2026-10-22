# CLAUDE.md (directory)

ZTG Directory: installed at one customer, inside that customer's network, it manages on-premises Active
Directory and the Microsoft 365 side of the same hybrid identity. A console with a Microsoft 365 mirror pane and
**Sync now** on every object, onboarding and offboarding workflows, and a Windows service that runs approved and
scheduled workflows. Status: plan (slice D0); no code yet. Repository-wide conventions (one folder per tool,
GitHub-hosted runners, actions pinned by SHA, dependencies public for at least 14 days, explicit update checks)
are in the root `CLAUDE.md`.

Read `docs/design.md` before building anything here, `docs/runbook.md` for the workflows step by step, and
`docs/research.md` for why. The design's principles are requirements:

1. Plan before apply: every change is computed, shown and confirmed or approved before anything is written;
   findings are never remediated automatically.
2. Narrow identities: interactive edits as the technician; workflow steps as the service's gMSA with rights
   delegated only on the managed OUs and groups; cloud steps as the customer's single-tenant app registration
   with only the permissions of enabled features. Never Domain Admin or Global Administrator, no client secrets,
   no stored passwords, nothing secret accepted as a command-line flag.
3. LDAP is signed and sealed (Negotiate through `wldap32.dll`) or carried over LDAPS, also on a DC.
4. Tier 0 objects are outside every workflow; bulk plans are capped.
5. Every change record names the requester, approver and technician, even when the gMSA or the app wrote.
6. Change the side that masters the data; hybrid Exchange mailbox types change through the Exchange tools.
7. One customer per installation; nothing leaves the network except Microsoft 365 calls and the update check.
8. Clean room: Microsoft's documented protocols and APIs only.

Directory and tenant data are untrusted input in the console: insert them with `textContent` only, as in Scanner.
Exchange cmdlets run from a fixed script with parameters passed as JSON, never spliced into command text.
Dependencies: standard library first; justify each new one in the commit message. Writing style: no em dashes.
Slices D1 to D7 are built one at a time, each reviewed before the next starts. `release-directory.yml` arrives
with the first package in D7 (design question Q9).

## Checks

None yet. D1 adds the Go module with `gofmt -l .`, `go vet ./...`, `GOOS=windows go vet ./...` and
`go test -race ./...`, and a path-filtered workflow `.github/workflows/directory.yml`.
