# ZTG AD Admin

Working name for ZTG's Active Directory and hybrid identity tool: a better RSAT for the jobs MSP technicians
repeat every week. Create users from role templates, change roles, run a leaver across AD and Microsoft 365 in
the right order, see whether an object is synced and whether the sync is healthy, and find a lockout's source,
a user's real last logon or effective group membership without four separate utilities. Every change is
planned, reviewed, applied and recorded, so a client's assessor can sample it.

Status: proposal. Nothing is built yet.

| Document | What it holds |
| --- | --- |
| [docs/research.md](docs/research.md) | The tools technicians use today, what is missing, and the facts that shape the design (Entra Connect retirement dates, LDAP signing, matching rules) |
| [docs/runbook.md](docs/runbook.md) | Ideas to build, by job (onboarding, changes, offboarding, hybrid sync, lookups, hygiene), with the leaver procedure step by step |
| [docs/design.md](docs/design.md) | Principles, architecture, credentials, compliance mapping, slices A1 to A8 and open questions for the owner |

Two dates matter for every hybrid client now: Entra Connect Sync 2.5.79.0 reaches end of support on
October 23, 2026, and every server must run 2.6.84.0 or later with application-based authentication by
April 7, 2027, or synchronization stops ([Microsoft](https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/reference-connect-version-history)).
