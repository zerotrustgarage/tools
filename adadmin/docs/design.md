# ZTG AD Admin: design

Status (October 22, 2026): proposal. No slice is built. The owner decides the open questions in section 11
before A1 starts; each slice is reviewed before the next begins, as for ZTG TFTP. The working name and the
folder name (`adadmin/`) can still change (question Q1).

## 1. Purpose

ZTG AD Admin is a technician's tool for Active Directory and hybrid Microsoft identity at MSP clients: create,
change and offboard accounts the same way every time, see sync state where the work happens, and answer the
everyday lookups (lockouts, last logon, effective membership) without four separate utilities. It is a better
RSAT for repeatable work, not a directory suite: it runs on the technician's machine as the technician, with no
server, agent or service account.

The ideas it draws on, and why each is in or out, are in [runbook.md](runbook.md); the field survey is in
[research.md](research.md).

## 2. Principles

1. Plan before apply. Every change is computed and shown before anything is written, and applied only after a
   person confirms it (runbook I38). Hygiene findings are never remediated automatically.
2. Act as the technician. The tool binds as the signed-in user (Kerberos) or with alternate credentials held in
   memory for the session, and relies on AD's own delegation. It has no service account and stores no
   credentials, secrets or tokens on disk (runbook I41).
3. Protected by default on the wire. LDAP is always signed and sealed (Negotiate) or carried over LDAPS. A
   simple bind on port 389 is never offered.
4. Tier 0 is fenced. Privileged accounts, privileged groups, domain controllers, `krbtgt`, gMSAs and dMSAs need
   an explicit privileged job (runbook I39). Bulk jobs are capped (I40).
5. Every job leaves a record. Append-only, hash-chained, per client, with requester, ticket, actor, machine,
   before and after (I42). Secrets (passwords, LAPS values, recovery keys) never enter a record; their
   retrieval does.
6. Hybrid-aware. A synced object is labelled as synced everywhere, and changes go to the side that masters the
   attribute; the tool never edits a cloud attribute that sync will overwrite.
7. Least privilege for cloud access. Microsoft Graph and Exchange Online are reached with delegated sign-in
   (the tenant's MFA and Conditional Access apply) and only the scopes the current action needs, requested
   when first needed.
8. Explicit updates only: a **Check for updates** action and `check-updates` command through
   `scripts/updates/`, no background checks (repository rule 7).
9. Clean room: no code or data from commercial products; Microsoft's documented protocols and APIs only.

## 3. Interface

- CLI `ztg-adadmin` for every operation, with `--plan` (write a plan file and stop) and `--apply <plan>`
  (apply a reviewed plan), so a scheduled leaver (runbook I13) is a Windows scheduled task applying an approved
  plan, not a background service.
- Desktop app (Wails, as ZTG Scanner) over the same engine: search, object pages with the sync card (I20),
  job wizards for create, change and leaver, the change record browser and evidence export.
- Windows x64 first. The engine compiles for Linux so tests and plan review run in CI and on any machine, but
  directory access in v1 is Windows-only (section 4).

## 4. Architecture

```
engine/
  directory/   LDAP session (Windows: wldap32.dll, Negotiate, sign and seal; LDAPS option), schema cache
  graph/       Microsoft Graph client: delegated sign-in, per-action scopes, cloud endpoints (commercial, GCC High, DoD)
  exo/         Exchange Online operations needed by the leaver job (method to decide, Q4)
  syncsrv/     Read-only checks and delta sync trigger on the sync server (WinRM), retirement table
  plan/        Plans: compute, render, apply in order, verify each step
  record/      Append-only hash-chained change records; evidence pack export
  templates/   Role templates and leaver profiles per client (versioned files)
cmd/ztg-adadmin/   CLI
app/               Wails desktop app (separate module, as in scanner/app)
```

Directory access. `go-ldap`'s Windows SSPI bind cannot sign or seal (research 6.3), and DCs increasingly
require signing. The engine therefore calls the Windows LDAP client (`wldap32.dll`) through
`golang.org/x/sys/windows` (already pinned by Scanner): `ldap_set_option(LDAP_OPT_SIGN, LDAP_OPT_ENCRYPT)`, then
`ldap_bind_s` with `LDAP_AUTH_NEGOTIATE`. Paged results, the `LDAP_MATCHING_RULE_IN_CHAIN` filter and the link
TTL control are standard LDAP controls through the same API. No cgo. Whether channel binding is honoured over
LDAPS through this API is tested against a DC set to "Always" in A1 (open item, research 6.3).

Hybrid. Entra state comes from Graph (`onPremises*` properties, `onPremisesSynchronization`, provisioning
errors). The sync server is touched only through WinRM with the technician's credentials, for read-only checks
and `Start-ADSyncSyncCycle -PolicyType Delta`; Cloud Sync uses Graph on-demand provisioning instead.

## 5. Change records

One JSON Lines file per client per month under the user's profile (location configurable to a client share).
Each line holds the job ID, the plan hash, the step, the before and after values, the result, the acting
account, the machine and a hash of the previous line. Records are never rewritten; a correction is a new job.
Export renders a job as PDF or HTML (runbook I18) and as JSON. Secrets are never recorded; the record states
that a password was set or a LAPS value was read, by whom and when.

## 6. Credentials and permissions

| Target | How the tool authenticates | What it needs |
| --- | --- | --- |
| AD (own domain) | Kerberos as the signed-in user | Whatever the technician is delegated; the tool never asks for Domain Admin |
| AD (client domain from a ZTG machine) | Alternate credentials typed per session, held in memory, never written | Same |
| Microsoft Graph | Delegated sign-in through the Windows account manager or system browser with PKCE; per-action scopes such as `User.RevokeSessions.All` | A ZTG multi-tenant app registration with no secret, or a per-client registration (Q3) |
| Exchange Online | Delegated, method per Q4 | Exchange recipient administration role |
| Sync server | WinRM as the technician | Local administrator on the sync server for ADSync cmdlets |

## 7. Compliance mapping

Controls are CMMC 2.0 with NIST SP 800-171 Rev 2 numbering; see [research.md](research.md) section 8 for what
each one means here and the Rev 3 caveat. The tool is itself a Security Protection Asset wherever it
administers an in-scope domain, so a client's SSP should name it, its version and where its change records are
kept.

| Control | Provided by |
| --- | --- |
| AC.L1-b.1.i, AC.L2-3.1.1 | Role templates, plan and record (A3) |
| AC.L2-3.1.5, AC.L2-3.1.6, AC.L2-3.1.7 | Templates, privileged group review, Tier 0 guard, recorded privileged actions (A3, A7) |
| AC.L2-3.1.8 | Lockout investigation (A1) |
| IA.L2-3.5.3 | Delegated cloud sign-in under the tenant's MFA (A5) |
| IA.L2-3.5.6, IA.L2-3.5.9, IA.L2-3.5.10 | Stale accounts, temporary passwords, LAPS coverage (A1, A3, A7) |
| AU.L2-3.3.1, AU.L2-3.3.2, AU.L2-3.3.8 | Change records (A3) |
| CM.L2-3.4.3 | Plans and records (A3) |
| PS.L2-3.9.2 | Leaver job and evidence pack (A4, A6) |
| SC.L2-3.13.8 | Signed and sealed LDAP or LDAPS (A1) |

## 8. Testing

- Engine unit tests run on Linux in CI against a fake directory (plans, templates, naming, record chaining,
  Tier 0 guard).
- Directory integration tests run against Samba AD DC in a container on Linux for LDAP semantics, and against
  a real Windows Server 2025 DC for signing, sealing and channel binding (a GitHub-hosted `windows-2025` runner
  can promote itself to a throwaway DC; to confirm in A1).
- Graph and Exchange calls are tested against recorded fixtures; a ZTG test tenant runs the leaver job end to
  end before A6 ships.

## 9. Slices

| Slice | Scope | Exit condition |
| --- | --- | --- |
| A0 | Owner decisions (section 11); folder, `CLAUDE.md`, research, runbook, design | This document approved |
| A1 | Read-only engine and CLI: signed and sealed bind, search (I28 AD part), object view, lockout (I24), last logon (I25), effective membership (I26), password expiry (I29), `check-updates` | Works against Samba and a Server 2025 DC requiring signing; nothing can write |
| A2 | Desktop app, read-only: search, object pages, lookups, exports | Windows install and launch test in CI, as Scanner |
| A3 | Changes on-premises with plan, apply and record: create from templates (I1 to I4, I7), modify and role change (I8, I9), unlock and reset (I10), undo (I12), Tier 0 guard and bulk caps (I39, I40) | Every change path covered by plan tests; records verified by the hash chain |
| A4 | Leaver job, on-premises half: snapshot, profiles, sync-scope guard, staged removal, evidence pack (I13 to I18) | Procedure 3.2 steps 0 to 2, 8, 9, 12, 13 and stage 2 run from one plan |
| A5 | Hybrid read: Graph sign-in, sync card (I20), sync server check (I19), conflicts (I21), tenant posture (I23), hybrid-ready create check (I5) | Matches the Entra admin center on a test tenant for synced and cloud-only users |
| A6 | Hybrid actions: delta sync trigger (I6), cloud steps of the leaver (procedure 3.2 steps 3 to 7, 10, 11), matching assistant (I22) | End-to-end leaver on the ZTG test tenant, synced and cloud-only users |
| A7 | Hygiene and advanced lookups: I27, I30 to I37, delegation helper (I43), temporary membership (I11) | Findings agree with PingCastle on a test domain for the overlapping checks |
| A8 | Packaging and release: MSI, signing through `scripts/signing/`, `release-adadmin.yml`, `adadmin-v` tag prefix in the shared update checker | First signed release |

## 10. Not in scope

- Being a directory suite: no web portal, self-service password reset, approval server or always-on service.
- Group Policy editing, DNS, DHCP and certificate services administration.
- Attack-path analysis and anything that exploits a finding.
- Non-Microsoft directories (OpenLDAP, Okta, Google) in v1.
- Storing any credential, token or secret.

## 11. Open questions for the owner

| # | Question | Recommendation |
| --- | --- | --- |
| Q1 | Name and folder: "ZTG AD Admin" in `adadmin/`, or another name (for example "ZTG Directory")? | Keep `adadmin/` until A8; the folder name is cheap to change before the first release |
| Q2 | Go and Wails (as Scanner) or another stack? | Go and Wails: one toolchain with Scanner and TFTP, `x/sys/windows` already pinned, no cgo |
| Q3 | Graph app registration: one ZTG multi-tenant public client (consent per client tenant) or one registration per client? | One multi-tenant public client with no secret, admin consent per tenant, scopes requested per action |
| Q4 | Exchange Online for the leaver (convert to shared, delegates, forwarding): call the Exchange Online PowerShell module if installed, use its REST endpoint directly, or leave mailbox steps to CIPP for now? | Leave to CIPP in A4, decide before A6 after testing the REST path on the test tenant |
| Q5 | Where change records live by default: technician's profile, or a client share chosen per client? | Per client, chosen when the client is first opened; profile as the fallback |
| Q6 | Which clients are GCC High or DoD tenants (runbook I47)? | If any are in scope, A5 supports the US Government endpoints from the start |
| Q7 | Release workflow timing: repository rule 1 asks every tool for `release-<tool>.yml`; there is nothing to release until A2 | Add `release-adadmin.yml` in A8 with the first package, and say so in `CLAUDE.md` |
| Q8 | Test estate: a ZTG test tenant (hybrid, with Connect Sync and Cloud Sync) and a test domain for A5 and A6 | Needed before A5; owner provides or approves the subscription |
