# ZTG Directory: design

Status (October 23, 2026; updated November 23, 2026): D1 in development following the owner's request to begin
building. The initial read-only engine and CLI use the recommended Go/no-cgo stack; live D1 lab validation remains
open. Owner decisions of November 23, 2026: AD writes are the next slice (D2, section 12), and the tool runs either
installed on a domain controller or other server, or from a technician's workstation like RSAT tools (section 4).
The app requires a sign-in with an AD account checked by a DC, and acts with that account's rights (section 6.5).

Build status (November 23, 2026), slice D2 in development ahead of the D1 lab evidence at the owner's request: the
engine has AD writes as the signed-in account (Windows LDAP), the plan, apply, change record and template engines,
the sign-in package (Kerberos-only bind check, roles, Duo and RADIUS, credential prompt, DPAPI), the console
service with demo, setup and live modes, and CLI change commands. The desktop console is being built. Nothing has
yet run against a real domain controller; [validation.md](validation.md) lists what the D2 lab must prove. Open
items found while building:

- Duo Authentication Proxy's default `failmode` is "safe", which lets a sign-in through when Duo cannot be reached;
  customers using Duo for IA.L2-3.5.3 should set `failmode = secure` on the proxy section the app uses.
- With an explicit credential the Windows LDAP client's automatic reconnect is off, so a dropped connection needs a
  new sign-in.
Unresolved decisions in section 14 remain open for their relevant slices; each slice is reviewed before the next
begins, as for ZTG TFTP. See [validation.md](validation.md) for current limits. The ideas behind each feature
are in [runbook.md](runbook.md) and the facts in [research.md](research.md).

## 1. Purpose

The core of the service (owner, November 23, 2026): replace Active Directory Users and Computers with something
more powerful, more intuitive and faster, and richer than ADUC for hybrid environments. Search comes first, one
object page replaces ADUC's tabbed property dialogs, every change is planned and recorded, and the Microsoft 365
side of each object sits beside the AD side.

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
| Console (desktop app) | Requires a sign-in with an AD account (section 6.5) and acts as that account. Browse and search AD; object pages with the Microsoft 365 mirror pane (section 7); create, edit, move, rename and delete users, groups and contacts, and change group membership, through plans; start workflows; approve pending ones; read change records. Runs on a server installation or on a technician's workstation (section 4) |
| Service (Windows service) | Server installation only. Runs workflows after they are approved, now or at a scheduled time; runs later stages (for example license removal 30 days after a leaver); holds the Microsoft 365 connection; writes change records |
| CLI `ztg-directory` | The same operations for scripts and for troubleshooting: lookups, `plan`, `apply`, `sync`, `check-updates`; on a server or a workstation |
| Setup | Server installation only. Installs the service, creates the group managed service account, delegates its AD rights on the chosen OUs, creates the app registration in the customer's tenant and its certificate (section 6). Workstation mode needs no setup |

## 3. Principles

1. Plan before apply. Every change, interactive or from a workflow, is computed and shown as a plan (object,
   attribute, old value, new value, target system) and applied only after a person confirms or approves it.
   Hygiene findings are never remediated automatically.
2. Narrow identities, none shared with people. Every person signs in to the app with their own AD account, checked
   by a domain controller (section 6.5), and interactive edits run as that account (Kerberos), so AD's own
   delegation decides what each person may change. A password typed at sign-in stays in memory for the session
   only. Workflow steps run as the service's group managed service account (gMSA), which has rights delegated only on
   the OUs and groups the customer chose, never Domain Admin, and only within the requester's own AD rights, checked
   when the plan is built and again when each step runs: the gMSA lets approved and scheduled work run without the
   requester signed in; it never grants more (question Q20). Cloud steps run as the customer's app
   registration with only the application permissions of the features that are switched on, never Global
   Administrator. There are no client secrets and no stored passwords. The one stored secret is the optional
   RADIUS shared secret, protected with DPAPI on the machine that uses it (section 6.5).
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

The tool reads and writes AD in both ways of running it (owner decision, November 23, 2026): installed on a
domain controller or another server, or run from a technician's own device the way RSAT tools are, with that
technician's delegated permissions.

### 4.1 Server installation

The service, the console and the CLI are installed together. Workflows that run unattended, wait for another
person's approval, run at a scheduled time or run in stages need a server installation.

| Host | Fits | Notes |
| --- | --- | --- |
| A domain controller | Any site; the simplest install where there is one server | Supported. LDAP still binds signed and sealed, to the local DC. Adds software to a DC, so the customer's SSP should list it |
| The Microsoft Entra Connect server | Customers using Connect Sync | Forcing a sync is local (no remote PowerShell), and the server is already managed as Tier 0 |
| A member management server | Larger sites, or Cloud Sync customers | LDAP to the DCs; forcing a Connect Sync cycle uses PowerShell remoting to the sync server |

Requirements (assessment, to confirm in D1 and D3): Windows Server 2019 or later, domain joined; Windows
PowerShell 5.1 with the Exchange Online PowerShell module (V3) for mailbox steps; Exchange Server 2019 CU12 or
later management tools where the customer has hybrid Exchange; outbound HTTPS to the Microsoft 365 endpoints of
the customer's cloud (commercial, GCC High or DoD).

In a server installation the console on the host talks to the service over a local named pipe. The service
identifies the person by the Kerberos handshake from the app sign-in (section 6.5), not by the Windows session the
pipe comes from, and admits members of two AD groups the setup creates: **ZTG Directory Operators** (run lookups,
edit, start workflows) and **ZTG Directory Approvers** (approve workflows that require it).

### 4.2 Workstation mode (remote, like RSAT)

The console and the CLI run on any domain-joined Windows 11 or Windows Server 2019 (or later) machine, the way
ADUC and the other RSAT tools do: no service, no setup in AD and nothing installed on a domain controller. Every
read and write runs as the AD account that signed in to the app (section 6.5) over signed and sealed LDAP (or
LDAPS) to a domain controller, and AD's own delegation decides what that person may change.

- RSAT is not required. The tool uses the Windows LDAP client (`wldap32.dll`) that is part of every Windows
  edition; RSAT can be installed beside it and nothing depends on it.
- Domain controller: the DC locator picks a writable DC in the technician's site, or the technician pins one. A
  plan reads, writes and verifies through that one DC, so its checks see its own writes despite replication
  delay; the plan and the record name the DC (runbook R119).
- Rights: before a plan is shown, the tool reads what the technician may write on each target and refuses a
  change outside their delegation at plan time, naming the attribute (runbook R118).
- Passwords: a reset writes `unicodePwd`, which a DC accepts only over TLS or an encrypted SASL layer of at least
  128 bits; the sealed Negotiate bind meets that, so a reset never falls back to a weaker connection (research 5).
- A device that is not domain joined: the technician signs in to the app with their AD account as on any other
  device; the DC must be pinned or found through the customer's DNS, and Kerberos needs the device's clock
  within the domain's tolerance (assessment, to confirm in D2).
- Without a server installation, a workflow runs in the technician's session, as the technician, from start to
  finish. Approval by another person, scheduled and staged workflows and steps run as the gMSA are refused with
  the reason.
- Microsoft 365 from a workstation uses the technician's own delegated sign-in, never the server's app
  certificate, which does not leave the server (question Q17).
- Change records: each workstation writes its own hash-chained file to the customer's protected share (section
  9; question Q18).
- Supported systems: Windows 11, and Windows Server 2019 or later. Windows 10 reached end of support on
  October 14, 2025, so it is not supported except where the customer pays for Extended Security Updates
  (assessment).
- A workstation that administers AD is a Security Protection Asset in the customer's SSP. Microsoft's tiering
  model expects Tier 0 objects (principle 4) to be changed only from a privileged access workstation or a DC;
  the tool warns when a Tier 0 change is planned from any other device (SC.L2-3.13.3, AC.L2-3.1.7; assessment).

A workstation console that submits workflows to a server installation's service over the network stays a later
option (question Q5).

## 5. Architecture

```
engine/
  ad/          LDAP sessions through wldap32.dll (Negotiate, sign and seal; LDAPS option); DC locator; reads and
               writes (add, modify, rename, delete, undelete) on one session per plan; rights preview; schema
               cache; Tier 0 map
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
| Technician (the AD account signed in to the app, section 6.5, on a server or a workstation) | Interactive edits and lookups; every step of a workflow run in workstation mode | Whatever AD already delegates to that person; the rights preview (R118) shows a missing right before anything is written |
| Service gMSA `ztg-directory$` | Workflow steps | Delegated by setup on the managed user OUs and groups only: create and delete user objects, reset password, write the attributes the templates use, write `member` on the managed groups, move between managed OUs. Setup shows the delegation as a plan before writing it. It is also a member of the Windows Authorization Access Group (or is granted read access to `tokenGroupsGlobalAndUniversal`), so the service can evaluate a requester's rights (section 6.5) |

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
| Devices (retire or wipe company data) | Intune managed device permissions (to confirm in D6) | |
| Mailboxes | Office 365 Exchange Online `Exchange.ManageAsApp` | Exchange Recipient Administrator, or a custom role group with a write scope limited to the customer's user mailboxes (recommended) |
| Proposed: cloud sign-in activity, sign-in failures for lockouts, MFA registration (runbook R63, R103, R104) | `AuditLog.Read.All` (licensing to confirm in D3) | |
| Proposed: risk level (R103) | `IdentityRiskyUser.Read.All` (Entra ID P2) | |
| Proposed: Entra role check for leavers (R86) | `RoleManagement.Read.Directory` | |
| Proposed: app consents in the leaver snapshot (R87) | `DelegatedPermissionGrant.Read.All` and a read of app role assignments (least privileged permissions to confirm in D5) | |
| Proposed: urgent leaver (R89) | `GroupMember.ReadWrite.All` (as cloud-only groups), `Policy.Read.All` to confirm the customer's policy exists (question Q11) | |
| Proposed, off by default: OneDrive handover and sharing links (R84, R85) | `Sites.FullControl.All` (question Q12) | |

### 6.3 The certificate

The app registration's credential is a certificate generated on the host during setup. Exchange Online requires
a CSP key provider, not CNG (research 4.2), so the certificate is created with a CSP provider and placed in the
service account's certificate store; the tool never exports the private key. Lifetime one year; the console warns
30 days before expiry and rotates by adding a new certificate to the app registration, switching, then removing
the old one. Whether the gMSA's own store works for the Exchange module, and whether a non-exportable key is
accepted, is tested in D3 (question Q7). Anyone who holds the private key can act as the app, so the certificate
is listed in the customer's SSP as a credential.

### 6.4 The sync server

Forcing a Connect Sync cycle runs `Start-ADSyncSyncCycle -PolicyType Delta`: locally when the tool is installed on
the sync server, otherwise through PowerShell remoting with the service gMSA, which needs membership of the sync
server's ADSync operators group (to confirm which local group allows starting a cycle). Cloud Sync needs no server
access.

### 6.5 Signing in to the app

Owner decision (November 23, 2026): the console and the CLI require a sign-in with an AD account, checked by a
domain controller, every time they start, even when Windows is already signed in. The account that signs in is
the account that acts: AD applies that account's delegated rights to every read and write, and the app takes its
own roles from that account's groups. A technician can keep a daily Windows account and sign in to the app with a
separate administrative account (AC.L2-3.1.6). The sign-in method is the customer's choice, from those below.

1. Notice. The customer's system use notice, if configured, is shown and acknowledged first (AC.L2-3.1.9).
2. Prompt. The Windows credential prompt (`CredUIPromptForWindowsCredentialsW`; in the CLI the console prompt,
   echo off) collects the credential, so Windows offers every method the device supports: password, smart card
   or PIV, and other credential providers. Credentials are never accepted from flags, environment variables or
   files.
3. Check at the DC. The app binds to a DC with Negotiate, signed and sealed, using that credential
   (`ldap_bind_sW` with an explicit identity). The DC checks the password or certificate and the account state
   (disabled, expired, locked, logon hours); a wrong password counts toward AD's lockout policy (AC.L2-3.1.8). A
   simple bind is never used.
4. Kerberos only by default. The Negotiate package list excludes NTLM (`!ntlm`), so a sign-in that cannot obtain
   a Kerberos ticket fails instead of falling back to NTLM (IA.L2-3.5.4, assessment). A customer can allow NTLM
   explicitly, and records then say so.
5. Second factor, from what the customer already runs (owner decision on Q21, November 23, 2026: ZTG provides no
   MFA service of its own):
   - Duo, where the customer has it, through the customer's Duo Authentication Proxy in Duo-only RADIUS mode
     (`radius_server_duo_only`). The DC checks the AD password through the Kerberos bind (step 3), so the
     password never goes over RADIUS; the app then sends only the user name and the chosen factor (push, phone
     call or passcode) to the proxy and waits for Accept. The proxy runs version 6.5.1 or later with
     `force_message_authenticator` on, and the app rejects replies without Message-Authenticator (CVE-2024-3596,
     research 5). The proxy's user name format (sAMAccountName or UPN) is set to match Duo's enrollment. The app
     talks only to the customer's proxy, so nothing leaves the network from the app (principle 7); the proxy's own
     connection to Duo is the customer's.
   - Duo's LDAP proxy mode is not used: it needs a simple bind through the proxy, which principle 3 rejects.
   - Duo Authentication for Windows Logon, where installed on DCs, servers or workstations, prompts at console,
     RDP and credentialed UAC logons on that machine. It does not prompt for network logons such as the app's LDAP
     bind, so it is not the app's second factor; it does protect the way a technician reaches a server
     installation (RDP to the DC or management server), and the sign-in record notes when the console runs in a
     Duo-protected RDP session (assessment, to confirm in D2).
   - Other RADIUS MFA (for example NPS with an MFA extension): the same RADIUS path, second factor only where the
     server supports it, otherwise the server checks the password as well.
   - Smart card or PIV (Kerberos PKINIT): multifactor in one step.
   - Password only, for a customer with none of these: allowed, and shown on every sign-in record and in the
     evidence pack as an open IA.L2-3.5.3 item for the customer's POA&M (runbook R129). IA.L2-3.5.3 requires
     multifactor authentication for network access to privileged and non-privileged accounts, and signing in to
     the app is network access to the account (assessment).
6. Roles. After sign-in the app reads the account's groups (`tokenGroups`, nesting included) and admits it only
   if it belongs to ZTG Directory Operators or Approvers (names configurable). If the groups cannot be read, the
   app refuses; it fails closed. An approver never approves their own request (AC.L2-3.1.4).
7. Session. The credential stays in process memory, encrypted with `CryptProtectMemory`, only while the session
   lasts, because new connections (another DC, the PDC emulator, the sync server) need it; it never reaches disk,
   a log, a record or a command line. The app locks after the customer's idle time (default 15 minutes) and needs
   the credential again to unlock (AC.L2-3.1.10); signing out, closing the app or reaching the maximum session
   length (default 8 hours) erases it (AC.L2-3.1.11).
8. Record. Every sign-in, failure (never the password), lock, unlock and sign-out is recorded with the account,
   the method, the DC and the device (AU.L2-3.3.1).

Where the boundary is: in workstation mode the app's role check is a convenience, because the account's AD rights
are the real boundary and the same person could use ADUC with them. In a server installation the role check is a
boundary for everything the service runs as the gMSA. The console proves the signed-in account to the service
with a Kerberos handshake over the named pipe (`AcceptSecurityContext` in the service), a network logon, so a
delegated technician does not need the right to log on locally that DCs restrict to administrators (assessment,
to confirm in D6). Every workflow step is gated by the requester's own rights (owner decision on Q20): the service builds the
requester's authorization context (`AuthzInitializeContextFromSid`, which reads `tokenGroupsGlobalAndUniversal`)
and evaluates each target's security descriptor with `AuthzAccessCheck`, once when the plan is built and again just
before each step runs, so rights removed in between, or a requester who has left, stop the step. Templates make
plans quick to build; they never grant rights.

The D1 CLI still uses the current Windows identity; the sign-in arrives with the console in D2 and replaces it in
the CLI too.

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

One JSON Lines file per month for each writer: the server installation and each workstation (section 4.2) keep their
own chain, so two machines never append to one chain at once, and the export merges them by time. The location is
configurable, for example a protected share. Each line holds
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
| AC.L1-b.1.i, AC.L2-3.1.1 | Onboarding from approved templates, plan and record (D4) |
| AC.L2-3.1.5, AC.L2-3.1.7 | Narrow identities (section 6), rights preview, Tier 0 fence, recorded privileged actions (D1, D2) |
| AC.L2-3.1.8 | Lockout investigation (D7) |
| IA.L2-3.5.6, IA.L2-3.5.9 | Stale accounts (D7); temporary passwords on every reset (D2) |
| AU.L2-3.3.1, AU.L2-3.3.2, AU.L2-3.3.8 | Change records naming people (D2) |
| CM.L2-3.4.3 | Plans and records (D2) |
| AC.L2-3.1.4, AC.L2-3.1.6 | No self-approval; sign in to the app with a separate administrative account (section 6.5, D2) |
| AC.L2-3.1.9, AC.L2-3.1.10, AC.L2-3.1.11 | System use notice, idle lock and maximum session at app sign-in (D2) |
| IA.L2-3.5.3, IA.L2-3.5.4 | Duo through the customer's Authentication Proxy, other RADIUS MFA or smart card at app sign-in; password only recorded as an open item; Kerberos only by default (D2, assessment) |
| SC.L2-3.13.3 | Workstation mode warns on Tier 0 changes from a device that is not a privileged access workstation or DC (D2, assessment) |
| PS.L2-3.9.2 | Offboarding workflow and evidence export (D5) |
| SC.L2-3.13.8 | Signed and sealed LDAP or LDAPS (D1) |

## 11. Testing

- Engine tests on Linux in CI against a fake directory and recorded Graph and Exchange responses: plans,
  templates, naming, ordering, Tier 0 fence, record chaining.
- LDAP semantics against Samba AD DC in a container; signing, sealing and channel binding against a Windows
  Server 2025 DC (a GitHub-hosted `windows-2025` runner promoted to a throwaway DC, to confirm in D1).
- A ZTG test estate (question Q8): a domain with Connect Sync and one with Cloud Sync, a tenant with Exchange
  Online, and a hybrid Exchange variant, where every workflow runs end to end before its slice ships.

## 12. Slices

Order changed on November 23, 2026 at the owner's request: AD changes made as the technician come straight after
D1, ahead of Microsoft 365, and the later slices each moved up by one.

| Slice | Scope | Exit condition |
| --- | --- | --- |
| D0 | This plan | Approved by the owner |
| D1 | Engine and CLI, read-only AD: signed and sealed bind on a DC, a member server or a workstation, search, object view, effective groups, last logon, `check-updates` | Works against Samba and a Server 2025 DC that requires signing, run on the DC and from a domain-joined workstation; nothing can write |
| D2 | AD changes as the technician: sign-in to the console and CLI with an AD account (section 6.5: notice, credential prompt, Kerberos-only bind, smart card, RADIUS option, roles, idle lock); the desktop console for AD (search, object page, OU tree), plans and change records, rights preview (R118), DC selection (R119); create, edit, rename, move, group membership, enable and disable, unlock, password reset, delete and Recycle Bin restore; on a DC and in workstation mode (section 4.2) | Sign-in works by password plus Duo (Authentication Proxy, Duo-only mode), by password alone with the open item recorded, and by smart card where the lab has one; a sign-in that only NTLM could satisfy is refused; a wrong password counts toward lockout; an account outside the Operators group is refused. Each change type applied in the lab by a delegated technician who is not a domain admin, both on the DC and from a workstation; a change outside that account's rights is refused at plan time; the Tier 0 fence and bulk cap hold; every change and sign-in has a complete record |
| D3 | Microsoft 365 connection and mirror: setup of the app registration and certificate, delegated sign-in in workstation mode (Q17), mirror pane, sync status, **Sync now** (Connect and Cloud Sync), sync server health | The pane matches the Entra admin center for synced and cloud-only users on the test tenant |
| D4 | Onboarding: role templates, naming and hybrid checks, onboarding workflow (8.2) | A starter created from a template appears licensed with a mailbox in the test tenant, with a complete record |
| D5 | Offboarding workflow (8.3) with all three mailbox models, mailbox access, license removal and transfer; evidence export | A leaver run on the test estate leaves the expected state in AD, Entra and Exchange for each mailbox model |
| D6 | Service: Kerberos-authenticated console to service connection and requester-rights gating at plan and run time (section 6.5), approvals, scheduled leavers, staged steps, device retirement, role change workflow (8.4) | A scheduled leaver runs unattended at its time, approved beforehand, and stage 2 runs on its due date |
| D7 | Lookups and hygiene: lockout investigation, password expiry, stale accounts, privileged group review, LAPS lookup | Agrees with PingCastle on a test domain for the overlapping checks |
| D8 | Packaging and release: MSI with the service, a workstation package without it, signing, `release-directory.yml`, `directory-v` tag in the shared update checker | First signed release |

## 13. Not in scope

- Multi-tenant management of many customers from one installation, and any cloud-hosted component.
- A self-service portal for end users, Group Policy, DNS, DHCP or certificate services administration.
- Managing AD through RADIUS: RADIUS can only answer whether a user may authenticate (research 5).
- Non-Microsoft directories.
- Attack-path analysis and anything that exploits a finding.

## 14. Open questions for the owner

| # | Question | Recommendation |
| --- | --- | --- |
| Q1 | Default host: the Entra Connect server where one exists, else a DC or a member server (section 4)? | Answered by the owner (November 23, 2026): install on a DC, or run from another device with the technician's own permissions (section 4) |
| Q2 | Stack: Go with a Wails console (as Scanner)? | Yes: one toolchain with Scanner and TFTP, no cgo |
| Q3 | Exchange Online steps through the Exchange Online PowerShell module (a supported, documented path) rather than undocumented REST calls? | Yes; the installer checks for the module and its version |
| Q4 | Approvals: which workflows need an approver by default? | Offboarding scheduled in advance and any privileged change; onboarding and immediate offboarding without |
| Q5 | Console only on the host (remote desktop) for v1, network console later? | Answered in part by the owner (November 23, 2026): the console and CLI also run on a technician's workstation, acting directly as the technician (section 4.2). Still recommended for later: a workstation console that talks to a server's service over the network, a new attack surface |
| Q6 | RADIUS: was it meant as the way to reach AD (it cannot manage objects), or as an extra sign-in factor for technicians (for example NPS with MFA)? | Answered by the owner (November 23, 2026): the app requires an AD sign-in by whatever method the customer uses on its network; RADIUS is one second-factor option at that sign-in (section 6.5, R123), in D2 |
| Q7 | Certificate storage: accept a CSP certificate in the service account's store (required by Exchange Online), with yearly rotation from the console? | Yes; revisit if Exchange Online accepts CNG or TPM keys later |
| Q8 | Test estate: who provides the test domain, tenants and hybrid Exchange lab? | A test domain with a delegated, non-admin technician, a domain-joined Windows 11 workstation and a Duo account with an Authentication Proxy before D2; the tenants and hybrid Exchange lab before D3 |
| Q9 | Release workflow timing: repository rule 1 asks for `release-<tool>.yml`; there is nothing to release before D2 | Add `release-directory.yml` in D8 with the first package |
| Q10 | The November 2026 feature review (research 8) added runbook ideas R47 to R117 with proposed slices. Accept them into those slices? Several are read-only and could join D1's CLI (R52, R58's AD dates, R59, R60, R61, R99) | Accept the slices as proposed; add only R59 and R60 to D1, because they are two constructed attributes on reads D1 already makes, and keep D1's exit condition otherwise unchanged |
| Q11 | Urgent leaver (R89): may the tool rely on a customer-owned Conditional Access policy that blocks a cloud-only group, and add leavers to that group? The group cannot be role-assignable (the app would then need `RoleManagement.ReadWrite.Directory`), so anyone with group rights could also block users | Yes, as an opt-in feature: the risk is denial of service, not elevation; break-glass accounts are excluded from the policy |
| Q12 | OneDrive handover and sharing links (R84, R85) need `Sites.FullControl.All`, far wider than any other permission the app holds | Off by default; a separate feature the customer switches on, otherwise a recorded manual step |
| Q13 | Removing a leaver's app consents (R87) needs `DelegatedPermissionGrant.ReadWrite.All`, which can grant any delegated permission | List them in the snapshot; removal stays manual |
| Q14 | Rule-based groups (R92): may an approved rule apply its additions and removals unattended within the bulk cap, or does every run wait for a person? | Every run waits for a person until D6 has run at a customer; revisit then |
| Q15 | PSA tickets (R75, R117): direct integration would send data outside Microsoft 365 (principle 7) | Copy-ready note only (R75) in v1 |
| Q16 | Changes made outside the tool (R98) need the DC Security logs, directory service change auditing and SACLs on the managed OUs | Yes, with the same Event Log Readers access as R30; setup shows missing audit policy and SACLs as findings and never changes them unasked |
| Q17 | Microsoft 365 in workstation mode (section 4.2): the server's app certificate must stay on the server, so a workstation needs the technician's own sign-in | A public client app registration with no secret: authorization code with PKCE in the system browser, so the customer's Conditional Access and MFA apply; tokens kept in memory only; delegated Graph permissions consented once by an administrator, and the technician's own Entra and Exchange roles limit what it can do |
| Q18 | Workstation change records (section 9): a technician who can append to their own chain on the share could also delete it | Accept, with mitigations: an NTFS ACL on the share that allows creating and appending but not deleting or overwriting (assessment, to confirm in D2), and the comparison of directory changes with the records (R98) |
| Q19 | The CLI also requires a sign-in, so it cannot run unattended from a scheduled task | Answered by the owner (November 23, 2026): scheduled work runs in the service (D6), as the gMSA after approval and within the requester's rights; scheduled reports also run through the service. The CLI is interactive only |
| Q20 | Workflows run by the service as the gMSA: may a template do more than the requester could do with their own rights (the reason for delegating to a gMSA), or is every request limited to the requester's rights (`AuthzAccessCheck`)? | Answered by the owner (November 23, 2026): templates are a convenience for building plans and never grant rights; every step is gated by the requester's own rights, checked when the plan is built and again when each step runs (section 6.5) |
| Q21 | Default sign-in method and RADIUS placement. RADIUS from workstations makes each workstation a RADIUS client holding the shared secret | Answered by the owner (November 23, 2026): ZTG has no MFA service of its own; use what the customer runs, chiefly Duo through the customer's Authentication Proxy (section 6.5). Each machine that sends RADIUS requests is registered as a client on the proxy with its own shared secret; password only is allowed for customers without MFA and recorded as an open item |
| Q22 | Does requester-rights gating (Q20) extend to Microsoft 365 steps the app registration performs? | Yes: before a cloud step, the service checks the requester's Entra directory roles (and administrative unit scope) for that action, and for Exchange steps the requester's Exchange role assignments; the mapping from action to required role ships with the tool and is dated (to size in D3) |
