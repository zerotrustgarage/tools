# ZTG Directory: design

Status (October 22, 2026): plan (slice D0). No code is built. The owner answers the open questions in section 14
before D1 starts; each slice is reviewed before the next begins, as for ZTG TFTP. The ideas behind each feature
are in [runbook.md](runbook.md) and the facts in [research.md](research.md).

## 1. Purpose

ZTG Directory manages a customer's on-premises Active Directory and the Microsoft 365 side of the same hybrid
identity from one application installed inside that customer's network. A technician opens an AD user and sees
the Microsoft 365 user that mirrors it beside it, can force a sync and watch the change arrive, and runs
onboarding and offboarding as workflows: the right OU, groups, email addresses, manager and attributes for a
starter; disable, password reset, shared mailbox, mailbox access for colleagues and license changes for a
leaver, in the right order, with a record of every step.

It is installed per customer. One installation knows one AD forest and one Microsoft 365 tenant. It is not a
multi-tenant console and not a cloud service.

## 2. What it consists of

| Part | What it does |
| --- | --- |
| Console (desktop app) | Browse and search AD; object pages with the Microsoft 365 mirror pane (section 7); edit attributes, groups and OU through plans; start workflows; approve pending ones; read change records |
| Service (Windows service) | Runs workflows after they are approved, now or at a scheduled time; runs later stages (for example license removal 30 days after a leaver); holds the Microsoft 365 connection; writes change records |
| CLI `ztg-directory` | The same operations for scripts and for troubleshooting: lookups, `plan`, `apply`, `sync`, `check-updates` |
| Setup | Installs the service, creates the group managed service account, delegates its AD rights on the chosen OUs, creates the app registration in the customer's tenant and its certificate (section 6) |

## 3. Principles

1. Plan before apply. Every change, interactive or from a workflow, is computed and shown as a plan (object,
   attribute, old value, new value, target system) and applied only after a person confirms or approves it.
   Hygiene findings are never remediated automatically.
2. Narrow identities, none shared with people. Interactive edits run as the signed-in technician (Kerberos).
   Workflow steps run as the service's group managed service account (gMSA), which has rights delegated only on
   the OUs and groups the customer chose, never Domain Admin. Cloud steps run as the customer's app
   registration with only the application permissions of the features that are switched on, never Global
   Administrator. There are no client secrets and no stored passwords.
3. Protected on the wire. LDAP is signed and sealed (Negotiate through the Windows LDAP client) or carried over
   LDAPS, also when the tool runs on a domain controller. A simple bind is never offered.
4. Tier 0 is fenced. Privileged accounts and groups, domain controllers, `krbtgt`, gMSAs and dMSAs are outside
   every workflow and need an explicit privileged change from a technician with a second confirmation. Bulk
   plans over a set size (default 25 objects) need the count typed back.
5. Every change names a person. The change record holds the requester, the approver and the technician even when
   the service or the app registration performed the write, so Microsoft's own logs (which show the gMSA or the
   app) can be tied to a person.
6. Change the side that masters the data. Synced attributes are written in AD and synced; cloud-only settings
   are written in Microsoft 365; in a customer with hybrid Exchange, mailbox type changes go through the
   on-premises Exchange tools (research 4.2).
7. Stays inside the customer. Nothing leaves the customer's network except calls to Microsoft 365 endpoints and
   the explicit update check. No telemetry, no ZTG back end.
8. Explicit updates only: **Check for updates** in the console and `check-updates` in the CLI through
   `scripts/updates/` (repository rule 7).
9. Clean room: Microsoft's documented protocols and APIs only.

## 4. Deployment

| Host | Fits | Notes |
| --- | --- | --- |
| The Microsoft Entra Connect server | Customers using Connect Sync | Recommended where it exists: forcing a sync is local (no remote PowerShell), and the server is already managed as Tier 0 |
| A domain controller | Small sites with one server | Supported. LDAP still binds signed and sealed, to the local DC. Adds software to a DC, so the customer's SSP should list it |
| A member management server | Larger sites, or Cloud Sync customers | LDAP to the DCs; forcing a Connect Sync cycle uses PowerShell remoting to the sync server |

Requirements (assessment, to confirm in D1 and D2): Windows Server 2019 or later, domain joined; Windows
PowerShell 5.1 with the Exchange Online PowerShell module (V3) for mailbox steps; Exchange Server 2019 CU12 or
later management tools where the customer has hybrid Exchange; outbound HTTPS to the Microsoft 365 endpoints of
the customer's cloud (commercial, GCC High or DoD).

The console runs on the same host (by remote desktop) and talks to the service over a local named pipe whose
ACL admits two AD groups the setup creates: **ZTG Directory Operators** (run lookups, edit, start workflows) and
**ZTG Directory Approvers** (approve workflows that require it). A console on a technician's workstation talking
to the service over the network is a later option (question Q5).

## 5. Architecture

```
engine/
  ad/          LDAP sessions through wldap32.dll (Negotiate, sign and seal; LDAPS option); schema cache; Tier 0 map
  m365/        Microsoft Graph client: certificate token, per-feature permissions, cloud endpoints
  exo/         Exchange Online steps: runs a fixed script in Windows PowerShell with the ExchangeOnlineManagement
               module and app-only authentication; parameters passed as JSON on standard input, never spliced
  exonprem/    Hybrid Exchange steps (Enable-RemoteMailbox, Set-RemoteMailbox) through the Exchange management tools
  sync/        Connect Sync (local or remote PowerShell) and Cloud Sync (Graph provisionOnDemand); sync server checks
  mirror/      Matching an AD object to its Microsoft 365 object; attribute comparison
  workflow/    Templates, forms, steps, approvals, schedules, stages
  plan/        Compute, render, apply in order, verify each step
  record/      Append-only, hash-chained change records; evidence export
cmd/ztg-directory/   CLI
service/             Windows service host, named pipe API
app/                 Wails console (separate module, as in scanner/app)
```

Language and stack: Go, with the console in Wails as ZTG Scanner, `golang.org/x/sys/windows` for the LDAP client
and service APIs, no cgo (question Q2). `go-ldap` is not used because its Windows bind cannot sign or seal
(research 5).

## 6. Identities and permissions

### 6.1 Active Directory

| Identity | Used for | Rights |
| --- | --- | --- |
| Technician (signed in to the console) | Interactive edits and lookups | Whatever AD already delegates to that person |
| Service gMSA `ztg-directory$` | Workflow steps | Delegated by setup on the managed user OUs and groups only: create and delete user objects, reset password, write the attributes the templates use, write `member` on the managed groups, move between managed OUs. Setup shows the delegation as a plan before writing it |

A gMSA has no password anyone knows; AD rotates it. The service never asks for, stores or accepts a password.

### 6.2 Microsoft 365 app registration

Setup creates one single-tenant app registration in the customer's tenant, named "ZTG Directory
(<server name>)". A customer administrator signs in once during setup to create it and grant admin consent;
that sign-in is not kept. Permissions are added only for the features the customer switches on:

| Feature | Application permissions (least privileged, research 4) | Entra or Exchange role |
| --- | --- | --- |
| Mirror pane, sync status | `User.Read.All`, `Organization.Read.All`, `OnPremDirectorySynchronization.Read.All` (to confirm) | |
| Cloud Sync "sync now" | `Synchronization.ReadWrite.All` (or `Application.ReadWrite.OwnedBy` if it proves sufficient) | |
| Licenses | `LicenseAssignment.ReadWrite.All` | |
| Sessions and sign-in methods | `User.RevokeSessions.All`, `UserAuthenticationMethod.ReadWrite.All` | |
| Cloud-only groups | `GroupMember.ReadWrite.All` | |
| Devices (retire or wipe company data) | Intune managed device permissions (to confirm in D5) | |
| Mailboxes | Office 365 Exchange Online `Exchange.ManageAsApp` | Exchange Recipient Administrator, or a custom role group with a write scope limited to the customer's user mailboxes (recommended) |

### 6.3 The certificate

The app registration's credential is a certificate generated on the host during setup. Exchange Online requires
a CSP key provider, not CNG (research 4.2), so the certificate is created with a CSP provider and placed in the
service account's certificate store; the tool never exports the private key. Lifetime one year; the console warns
30 days before expiry and rotates by adding a new certificate to the app registration, switching, then removing
the old one. Whether the gMSA's own store works for the Exchange module, and whether a non-exportable key is
accepted, is tested in D2 (question Q7). Anyone who holds the private key can act as the app, so the certificate
is listed in the customer's SSP as a credential.

### 6.4 The sync server

Forcing a Connect Sync cycle runs `Start-ADSyncSyncCycle -PolicyType Delta`: locally when the tool is installed on
the sync server, otherwise through PowerShell remoting with the service gMSA, which needs membership of the sync
server's ADSync operators group (to confirm which local group allows starting a cycle). Cloud Sync needs no server
access.

## 7. The mirror pane

Every AD user, group and contact page has a Microsoft 365 pane beside it.

- Matching, in order: `onPremisesImmutableId` against the Base64 of the customer's source anchor
  (`mS-DS-ConsistencyGuid`, or `objectGUID` where the tenant uses it), then `onPremisesSecurityIdentifier`
  against `objectSid`. A UPN or primary SMTP match without either is shown as "possible match, not linked" with
  the reason, never as the same object.
- Shown: sign-in enabled, last sync time, sync errors, source of authority, licenses, mailbox type and size,
  mailbox delegates, cloud group memberships, registered devices, number of sign-in methods.
- Comparison: for each synced attribute, the AD value beside the cloud value; differences are labelled "waiting
  for sync" (AD changed after the last sync) or "out of step" (needs attention).
- **Sync now**: Connect Sync delta cycle (6.4) or Cloud Sync on-demand provisioning for this object; the pane
  then watches the cloud object until its last sync time moves and the differences clear, or reports the sync
  error.
- **Sync server health**: version against Microsoft's retirement table (shipped with the tool and dated),
  application-based authentication configured, staging mode, scheduler state, last cycle result (research 3).

## 8. Workflows

### 8.1 Model

A workflow is a customer template plus a form. The technician (or, later, an HR request) fills the form; the
tool builds the full plan; the plan is approved where the template requires it; the service runs it now or at
the scheduled time; each step is verified after it runs; later stages wait in the service until due; the record
is written as each step completes. A failed step stops the steps that depend on it and leaves the rest of the
plan visible with what is still to do.

Templates are versioned files per customer, edited in the console and recorded like any other change.

### 8.2 Onboarding

Form: name, role (selects the template), start date, manager, plus the template's extra fields (job title,
department, office, phone, employee ID, company, custom attributes). The template supplies the OU, groups, UPN
and email patterns, license and mailbox settings. Steps (runbook 3.2 has the detail):

1. Check: name collisions in AD and Microsoft 365, UPN suffix verified in the tenant, OU inside the sync scope.
2. Create the AD user in the template's OU with attributes, manager and a temporary password (change at next
   logon); set `mail`, `proxyAddresses` and usage location.
3. Mailbox: hybrid Exchange customers `Enable-RemoteMailbox`; others rely on the license to create the mailbox.
4. Add the template's groups (including the license group where group-based licensing is used).
5. Sync now, then wait for the cloud user.
6. Licenses by direct assignment where group-based licensing is not used.
7. Cloud-only steps from the template (cloud groups, Teams, shared mailbox access).
8. Record, and a starter sheet for the manager without the password.

### 8.3 Offboarding

Form: the user, leaving time (now or scheduled), who receives mailbox access, forwarding, out-of-office text,
who receives the license (if anyone), and the template's retention period. Steps (runbook 4.2 has the detail):

1. Snapshot the user in AD and Microsoft 365 into the record.
2. Disable the AD account, reset the password to an unknown random value, set `accountExpires`.
3. Sync now; confirm sign-in is blocked in the cloud.
4. Revoke sessions; remove sign-in methods.
5. Convert the mailbox to shared using the customer's mailbox model (research 4.2), then grant Full Access (with
   or without automapping), Send As or Send on Behalf to the named colleagues; forwarding and out-of-office.
6. Remove group memberships (recorded in step 1); hide from address lists.
7. Licenses: remove, and assign to the named colleague where the form says so; or keep until stage 2.
8. Devices: retire company data.
9. Move to the leavers OU (kept inside the sync scope so the shared mailbox survives).
10. Stage 2 after the retention period: remove remaining licenses, delete or keep the account per template.

### 8.4 Role change

Form: the user and the new role. The plan removes the old template's groups and attributes, adds the new ones,
moves the OU, and lists groups outside both templates for a decision. Licenses follow the templates.

## 9. Change records

One JSON Lines file per month on the host (location configurable, for example a protected share). Each line holds
the job, the plan hash, the step, before and after values, the result, the requester, the approver, the
technician, the identity that performed the write (technician, gMSA or app) and a hash of the previous line.
Records are never rewritten. Passwords, LAPS values and recovery keys never enter a record; that they were set
or read, by whom and when, does. Export renders a job as a one-page PDF or HTML for the customer's personnel
file, and as JSON.

## 10. Compliance mapping

CMMC 2.0 with NIST SP 800-171 Rev 2 numbering; see [research.md](research.md) section 7 for each control and the
Rev 3 caveat. The tool, its service account and its app registration certificate are Security Protection Assets
and belong in the customer's SSP.

| Control | Provided by |
| --- | --- |
| AC.L1-b.1.i, AC.L2-3.1.1 | Onboarding from approved templates, plan and record (D3) |
| AC.L2-3.1.5, AC.L2-3.1.7 | Narrow identities (section 6), Tier 0 fence, recorded privileged actions (D1 to D3) |
| AC.L2-3.1.8 | Lockout investigation (D6) |
| IA.L2-3.5.6, IA.L2-3.5.9 | Stale accounts (D6); temporary passwords (D3) |
| AU.L2-3.3.1, AU.L2-3.3.2, AU.L2-3.3.8 | Change records naming people (D3) |
| CM.L2-3.4.3 | Plans and records (D3) |
| PS.L2-3.9.2 | Offboarding workflow and evidence export (D4) |
| SC.L2-3.13.8 | Signed and sealed LDAP or LDAPS (D1) |

## 11. Testing

- Engine tests on Linux in CI against a fake directory and recorded Graph and Exchange responses: plans,
  templates, naming, ordering, Tier 0 fence, record chaining.
- LDAP semantics against Samba AD DC in a container; signing, sealing and channel binding against a Windows
  Server 2025 DC (a GitHub-hosted `windows-2025` runner promoted to a throwaway DC, to confirm in D1).
- A ZTG test estate (question Q8): a domain with Connect Sync and one with Cloud Sync, a tenant with Exchange
  Online, and a hybrid Exchange variant, where every workflow runs end to end before its slice ships.

## 12. Slices

| Slice | Scope | Exit condition |
| --- | --- | --- |
| D0 | This plan | Approved by the owner |
| D1 | Engine and CLI, read-only AD: signed and sealed bind on a DC or a member server, search, object view, effective groups, last logon, `check-updates` | Works against Samba and a Server 2025 DC that requires signing; nothing can write |
| D2 | Microsoft 365 connection and mirror: setup of the app registration and certificate, mirror pane, sync status, **Sync now** (Connect and Cloud Sync), sync server health; console read-only | The pane matches the Entra admin center for synced and cloud-only users on the test tenant |
| D3 | Changes and onboarding: plans for edits, moves and group changes; onboarding workflow (8.2) with templates; change records | A starter created from a template appears licensed with a mailbox in the test tenant, with a complete record |
| D4 | Offboarding workflow (8.3) with all three mailbox models, mailbox access, license removal and transfer; evidence export | A leaver run on the test estate leaves the expected state in AD, Entra and Exchange for each mailbox model |
| D5 | Service: approvals, scheduled leavers, staged steps, device retirement, role change workflow (8.4) | A scheduled leaver runs unattended at its time, approved beforehand, and stage 2 runs on its due date |
| D6 | Lookups and hygiene: lockout investigation, password expiry, stale accounts, privileged group review, LAPS lookup | Agrees with PingCastle on a test domain for the overlapping checks |
| D7 | Packaging and release: MSI with the service, signing, `release-directory.yml`, `directory-v` tag in the shared update checker | First signed release |

## 13. Not in scope

- Multi-tenant management of many customers from one installation, and any cloud-hosted component.
- A self-service portal for end users, Group Policy, DNS, DHCP or certificate services administration.
- Managing AD through RADIUS: RADIUS can only answer whether a user may authenticate (research 5).
- Non-Microsoft directories.
- Attack-path analysis and anything that exploits a finding.

## 14. Open questions for the owner

| # | Question | Recommendation |
| --- | --- | --- |
| Q1 | Default host: the Entra Connect server where one exists, else a DC or a member server (section 4)? | Yes, in that order |
| Q2 | Stack: Go with a Wails console (as Scanner)? | Yes: one toolchain with Scanner and TFTP, no cgo |
| Q3 | Exchange Online steps through the Exchange Online PowerShell module (a supported, documented path) rather than undocumented REST calls? | Yes; the installer checks for the module and its version |
| Q4 | Approvals: which workflows need an approver by default? | Offboarding scheduled in advance and any privileged change; onboarding and immediate offboarding without |
| Q5 | Console only on the host (remote desktop) for v1, network console later? | Yes; a network API is a new attack surface and can wait |
| Q6 | RADIUS: was it meant as the way to reach AD (it cannot manage objects), or as an extra sign-in factor for technicians (for example NPS with MFA)? | Treat as a possible extra factor for console sign-in, after D5 |
| Q7 | Certificate storage: accept a CSP certificate in the service account's store (required by Exchange Online), with yearly rotation from the console? | Yes; revisit if Exchange Online accepts CNG or TPM keys later |
| Q8 | Test estate: who provides the test domain, tenants and hybrid Exchange lab? | Needed before D2 |
| Q9 | Release workflow timing: repository rule 1 asks for `release-<tool>.yml`; there is nothing to release before D2 | Add `release-directory.yml` in D7 with the first package |
