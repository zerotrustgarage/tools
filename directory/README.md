# ZTG Directory

Active Directory and Microsoft 365 management for one customer, installed inside that customer's network: on a
domain controller, on the Entra Connect server or on a management server.

- Open an AD user and see the Microsoft 365 user that mirrors it beside it: sign-in state, licenses, mailbox,
  last sync, sync errors and every synced attribute compared.
- **Sync now** on any object (Entra Connect Sync or Cloud Sync), then watch the change arrive.
- Onboarding workflows from role templates: the requested OU, groups, email addresses, manager and extra
  attributes, mailbox and licenses.
- Offboarding workflows: disable sign-in, reset the password, revoke sessions, convert the mailbox to shared
  the way the customer's Exchange setup requires, give colleagues access to it, and remove or reassign
  licenses; now or at a scheduled time, with approval where the customer wants it.
- Every change planned, reviewed and recorded, with an evidence page for the personnel file.

The Microsoft 365 side runs through an app registration in the customer's own tenant with a certificate and only
the permissions of the features in use; the on-premises side through a group managed service account with
rights on the chosen OUs only.

Status: D1 in development. The initial Go engine and CLI support read-only AD lookups using the current
Windows identity. Live Samba and Windows Server 2025 validation is still required. Microsoft 365, the desktop
console, writes, workflows, service and installer belong to later slices.

## Build and use the D1 CLI

Use the repository's pinned Go toolchain (Go 1.26.10). From `directory/`:

```sh
go test -race ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o ztg-directory.exe ./cmd/ztg-directory
```

Run the executable on a domain-joined Windows host as the technician. Flags must precede the distinguished
name. These PowerShell examples use an isolated example domain:

```powershell
.\ztg-directory.exe search --server dc1.lab.example --base-dn 'DC=lab,DC=example' --query 'Alex' --json
.\ztg-directory.exe object --server dc1.lab.example --base-dn 'DC=lab,DC=example' --json 'CN=Alex,OU=Users,DC=lab,DC=example'
.\ztg-directory.exe groups --server dc1.lab.example --base-dn 'DC=lab,DC=example' 'CN=Alex,OU=Users,DC=lab,DC=example'
.\ztg-directory.exe last-logon --server dc1.lab.example --base-dn 'DC=lab,DC=example' 'CN=Alex,OU=Users,DC=lab,DC=example'
.\ztg-directory.exe check-updates --json
```

LDAP uses Negotiate with signing and sealing; `--ldaps` selects TLS with Windows certificate validation.
There are no credential flags, write commands or background connections. Linux can run tests, help and
release status; live LDAP requires Windows. Search text is escaped as a value, never executed as an LDAP filter.
`--timeout` limits native network waits. Negotiate binding uses Microsoft's synchronous API with a timeout
per response roundtrip, so it is not a strict total bind deadline. Manual cancellation is observed after
the current native bind or search call returns; search wait times are capped by the command's deadline.
Use `--limit` to bound a search and check the truncation indicator. Group membership is not a complete access
calculation. Token group SIDs outside the search base remain in the result with `Resolved: false` and empty
name and DN fields. Use the domain naming context as the base to resolve more names. Last-logon results
describe one DC and explicitly report partial coverage across the domain.
`--base-dn` scopes searches and group-name resolution; direct DN lookups use the technician's existing AD
permissions. It is not an authorization boundary. Effective groups require a Global Catalog to be available.
Multivalued attributes are retrieved in ranges on the same connection and object. Reads are bounded to
4,096 text values or 8,192 SID values and 16 range pages per attribute. Invalid, inconsistent or over-limit
responses return an incomplete-result error instead of a partial list. These reads are not an atomic snapshot;
concurrent directory changes can affect the values returned.

`check-updates` currently reports `unreleased` locally. It makes no network request and does not establish
that a build is up to date. The shared release checker source is vendored for D7, when `directory-v` selection
and the signed installer contract are added. See [docs/validation.md](docs/validation.md) for the lab checks
and open decisions before continuing to D2.

| Document | What it holds |
| --- | --- |
| [docs/design.md](docs/design.md) | Parts, principles, deployment, identities and permissions, the mirror pane, workflows, slices D1 to D7 and open questions |
| [docs/runbook.md](docs/runbook.md) | What the tool does step by step: everyday work, onboarding, offboarding, the three mailbox models, role change, sync, lookups |
| [docs/research.md](docs/research.md) | Tools in use today and the Microsoft facts the design rests on |

Two dates matter for every hybrid customer now: Entra Connect Sync 2.5.79.0 reaches end of support on
October 23, 2026, and every server must run 2.6.84.0 or later with application-based authentication by
April 7, 2027, or synchronization stops ([Microsoft](https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/reference-connect-version-history)).
