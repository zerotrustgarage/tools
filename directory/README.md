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

Status: plan. Nothing is built yet.

| Document | What it holds |
| --- | --- |
| [docs/design.md](docs/design.md) | Parts, principles, deployment, identities and permissions, the mirror pane, workflows, slices D1 to D7 and open questions |
| [docs/runbook.md](docs/runbook.md) | What the tool does step by step: everyday work, onboarding, offboarding, the three mailbox models, role change, sync, lookups |
| [docs/research.md](docs/research.md) | Tools in use today and the Microsoft facts the design rests on |

Two dates matter for every hybrid customer now: Entra Connect Sync 2.5.79.0 reaches end of support on
October 23, 2026, and every server must run 2.6.84.0 or later with application-based authentication by
April 7, 2027, or synchronization stops ([Microsoft](https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/reference-connect-version-history)).
