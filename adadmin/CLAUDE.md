# CLAUDE.md (adadmin)

ZTG AD Admin (working name), a technician's tool for Active Directory and hybrid Microsoft identity: create,
change and offboard accounts through reviewed plans, see sync state, and run the everyday lookups. Status:
proposal (slice A0); no code yet. Repository-wide conventions (one folder per tool, GitHub-hosted runners,
actions pinned by SHA, dependencies public for at least 14 days, explicit update checks) are in the root
`CLAUDE.md`.

Read `docs/design.md` before building anything here, `docs/runbook.md` for the ideas and their slices, and
`docs/research.md` for why. The design's principles are requirements:

1. Plan before apply: every change is computed, shown and confirmed before anything is written; findings are
   never remediated automatically.
2. Act as the technician: Kerberos or session-only alternate credentials, AD's own delegation, no service
   account, nothing secret stored on disk, in settings, in records or in exports, and never accepted as a
   command-line flag.
3. LDAP is signed and sealed (Negotiate through `wldap32.dll`) or carried over LDAPS; never a simple bind on 389.
4. Tier 0 objects need an explicit privileged job; bulk jobs are capped.
5. Every job writes an append-only, hash-chained change record; secrets never enter it, their retrieval does.
6. Synced objects are changed on the side that masters the attribute.
7. Cloud access is delegated sign-in with per-action Graph scopes; no client secrets.
8. Clean room: Microsoft's documented protocols and APIs only.

Directory data is untrusted input in the desktop UI: insert it with `textContent` only, as in Scanner.
Dependencies: standard library first; justify each new one in the commit message. Writing style: no em dashes.
Slices A1 to A8 are built one at a time, each reviewed before the next starts. `release-adadmin.yml` arrives with
the first package in A8 (design question Q7).

## Checks

None yet. A1 adds the Go module with `gofmt -l .`, `go vet ./...`, `GOOS=windows go vet ./...` and
`go test -race ./...`, and a path-filtered workflow `.github/workflows/adadmin.yml`.
