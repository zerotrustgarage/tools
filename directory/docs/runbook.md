# ZTG Directory: runbook

Status (October 22, 2026): plan for owner review; nothing is built yet. This file describes what ZTG Directory
does, step by step, for the work a technician does at a customer: everyday console work, onboarding, offboarding,
role changes, sync and lookups. Each idea names the slice of [design.md](design.md) that builds it; the facts
behind it are in [research.md](research.md).

Priority: **High** is part of the first useful release; **Medium** is clearly worth doing; **Low** waits for
demand. Controls are CMMC 2.0 Level 2 with NIST SP 800-171 Rev 2 numbering unless marked L1.

## 1. How every change runs

1. **Plan**: the tool reads the current state in AD and Microsoft 365 and lists every change it will make
   (system, object, attribute, old value, new value), in order. Nothing is written.
2. **Review**: the technician sees the plan with warnings (synced object, Tier 0 object, move out of the sync
   scope, license shortage) and enters the ticket reference. Workflows that need approval wait for a member of
   ZTG Directory Approvers.
3. **Run**: steps run in the plan's order, each checked after it runs. Interactive edits run as the technician;
   workflow steps run in the service (gMSA for AD, the app registration for Microsoft 365).
4. **Record**: every step's before and after, result, requester, approver and technician, in the hash-chained
   change record, exportable as PDF or HTML and JSON.

## 2. Everyday console work

| # | Idea | How | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- |
| R1 | Search box across AD and Microsoft 365: name, UPN, email, phone, employee ID, SID, GUID, ImmutableId | LDAP ambiguous name resolution (`anr`) plus Graph | | High | D1, D2 |
| R2 | Mirror pane on every user, group and contact: the Microsoft 365 object beside the AD object, attribute comparison, licenses, mailbox, sign-in state, last sync, errors (design 7) | Match on `onPremisesImmutableId`, then `onPremisesSecurityIdentifier` | | High | D2 |
| R3 | **Sync now** from any object: Connect Sync delta cycle or Cloud Sync on-demand provisioning, then watch the cloud object update | Design 6.4 | | High | D2 |
| R4 | Attribute editor with schema awareness: syntax, single or multi-valued, read-only system attributes, a "synced" badge on attributes that flow to Microsoft 365 | Schema read at connect | | Medium | D3 |
| R5 | Group membership edits with nested (effective) membership shown and the path that grants each group | `LDAP_MATCHING_RULE_IN_CHAIN` (1.2.840.113556.1.4.1941) | AC.L2-3.1.5 | High | D1 (read), D3 (edit) |
| R6 | Move between OUs with a sync-scope warning when the target is outside it | Compare with the sync configuration | | High | D3 |
| R7 | Unlock and password reset with the lockout context on the same screen (R30); reset always sets "change at next logon" | `lockoutTime = 0`; `unicodePwd`, `pwdLastSet = 0` | AC.L2-3.1.8, IA.L2-3.5.9 | High | D3 |
| R8 | Undo a recorded change: restore attributes and group memberships from the record's before values (passwords and deletions excluded) | The record is the undo source | CM.L2-3.4.3 | Medium | D3 |

## 3. Onboarding

### 3.1 Ideas

| # | Idea | How | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- |
| R9 | Role templates per customer: OU, groups, UPN and email patterns, attribute defaults (company, department, office), license, mailbox settings, cloud groups, extra form fields | Versioned template files, edited in the console and recorded | AC.L1-b.1.i, AC.L2-3.1.1, AC.L2-3.1.5 | High | D3 |
| R10 | Naming with collision handling: UPN and primary SMTP from the template pattern, `sAMAccountName` within 20 characters, diacritics folded, numbered on collision, checked in AD and Microsoft 365 before creating | LDAP search on `userPrincipalName`, `sAMAccountName`, `proxyAddresses`; Graph lookup | AC.L2-3.1.1 | High | D3 |
| R11 | Temporary password: generated passphrase, change at next logon, shown once to the technician, never in the record, a log or the starter sheet | Sealed LDAP write | IA.L2-3.5.9 | High | D3 |
| R12 | Hybrid-ready check: UPN suffix verified in the tenant, OU inside the sync scope, no cloud-only user already holding the UPN or address (it would soft match), license available | Graph `/domains`, `/users`, `/subscribedSkus` | AC.L2-3.1.1 | High | D3 |
| R13 | Manager picker with the manager's own mirror state, so the cloud manager field is right after sync | `manager` DN | | High | D3 |
| R14 | Starter sheet for the manager: account name, email, groups, licenses, first-logon instructions; no password | Export from the record | | Medium | D3 |
| R15 | Copy from an existing user with a diff against the role template, so groups the old user collected over the years are visible and unticked | Read the source user's `memberOf` | AC.L2-3.1.5 | Medium | D3 |
| R16 | Bulk starters from CSV with column mapping, per-row checks and one plan for the file | Same engine, one plan | CM.L2-3.4.3 | Medium | D5 |

### 3.2 Onboarding procedure

| Step | Action | System and identity | Notes |
| --- | --- | --- | --- |
| 0 | Form, template, ticket; checks R10 and R12 | Console | Refuses a template OU outside the sync scope unless the template is marked "on-premises only" |
| 1 | Create the user in the template OU: name attributes, UPN, `sAMAccountName`, title, department, office, phone, employee ID, company, custom attributes, `manager`, `mail`, `proxyAddresses` (primary SMTP in capitals), `msExchUsageLocation` where the Exchange schema exists | AD, gMSA (workflow) or technician (interactive) | Account created disabled until the password is set |
| 2 | Temporary password, change at next logon; enable the account (or leave disabled until the start date) | AD | R11 |
| 3 | Mailbox, by mailbox model (section 4.3): hybrid Exchange `Enable-RemoteMailbox`; cloud-only Exchange nothing here (the license creates the mailbox) | Exchange management tools on the host | |
| 4 | Add the template's AD groups, including the license group where group-based licensing is used | AD | |
| 5 | Sync now and wait for the cloud user (design 7) | Sync server or Graph | Stops with the sync error if the object does not appear |
| 6 | Direct license assignment where the template uses it | Graph, app registration | Needs a usage location |
| 7 | Cloud-only groups, Teams, access to shared mailboxes from the template | Graph, Exchange Online | |
| 8 | Record and starter sheet (R14) | Service | |

## 4. Offboarding

### 4.1 Ideas

| # | Idea | How | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- |
| R17 | Leaver templates per customer: what happens to the mailbox (shared, kept, deleted), who gets access by default (manager), forwarding and how long, out-of-office text, license handling, retention period | Template files beside the role templates | PS.L2-3.9.2 [a] | High | D4 |
| R18 | Snapshot before any change: attributes, groups, manager and reports, mailbox delegates, licenses, devices | Read-only first step | PS.L2-3.9.2, AU.L2-3.3.1 | High | D4 |
| R19 | Shared mailbox conversion that follows the customer's mailbox model (4.3), so Exchange Online does not later revert it to a regular mailbox and disconnect it | Research 4.2 | | High | D4 |
| R20 | Mailbox access for named colleagues: Full Access with or without automapping, Send As, Send on Behalf; forwarding with an end date; out-of-office | Exchange Online cmdlets, app-only | | High | D4 |
| R21 | License removal and transfer: remove the leaver's licenses, optionally assign them to a named colleague (for example the replacement), or keep until stage 2 | Graph `assignLicense` | | High | D4 |
| R22 | Sync-scope guard: refuse to move a synced user out of the sync scope before the mailbox is converted, because the next sync deletes the cloud user and its mailbox | Compare target OU with the sync configuration | | High | D4 |
| R23 | Scheduled leavers: approve now, run at the leaving time (for example 17:00 on the last day) | Service schedule | PS.L2-3.9.2 [b] | High | D5 |
| R24 | Staged removal: day 0 cut access, day N (template) remove remaining licenses, delete or keep the account; the console lists what is due | Service stages | PS.L2-3.9.2 | Medium | D5 |
| R25 | Evidence export: one page with requester, approver, times and every step's result for the customer's personnel file and an assessor's sample | Record export | PS.L2-3.9.2, AU.L2-3.3.2 | High | D4 |

### 4.2 Offboarding procedure

Order matters: access is cut first, then sessions, then data is preserved, and destructive steps come last. For
synced users, sign-in is blocked in AD and reaches Microsoft 365 by sync.

| Step | Action | System and identity | Notes |
| --- | --- | --- | --- |
| 0 | Form, leaver template, ticket, approval where required, run now or at a time | Console, service | Refuses Tier 0 accounts (design 3, principle 4) |
| 1 | Snapshot (R18) | AD, Graph, Exchange Online | Read-only |
| 2 | Disable the AD account; reset the password to a random unknown value; set `accountExpires` to now | AD, gMSA | Existing Kerberos tickets stay valid until they expire (10 hours by default) |
| 3 | Sync now; confirm `accountEnabled` is false in Entra (cloud-only users: set it directly) | Sync server or Graph | |
| 4 | Revoke sessions; remove sign-in methods | Graph, app registration | Revocation takes a few minutes |
| 5 | Convert the mailbox to shared by mailbox model (4.3) | Exchange tools on the host and Exchange Online | Before any license removal or move |
| 6 | Grant mailbox access to the named colleagues; forwarding with an end date; out-of-office (R20) | Exchange Online, app-only | |
| 7 | Hide from address lists: `msExchHideFromAddressLists` in AD where the Exchange schema exists, else in Exchange Online | AD or Exchange Online | |
| 8 | Remove group memberships (recorded in step 1); reassign owned groups and direct reports | AD, then Graph for cloud-only groups | Synced groups can only be changed in AD |
| 9 | Licenses: remove; assign to the named colleague where requested (R21) | Graph | A shared mailbox under 50 GB without archive or hold needs no license (assessment; check the customer's plan) |
| 10 | Devices: retire company data from mobile devices | Intune through Graph | D5 |
| 11 | Move to the leavers OU, inside the sync scope (R22) | AD | |
| 12 | Record and evidence (R25) | Service | |
| Stage 2 | After the retention period: remove remaining licenses; delete or keep the AD account per template (AD Recycle Bin checked first) | Service | D5 |

### 4.3 Mailbox models

The tool detects which model a customer has during setup and shows it on the settings page; a technician can
override it.

| Model | How it is detected (assessment, to confirm in D3) | Create mailbox | Convert to shared |
| --- | --- | --- | --- |
| A. Exchange Online only, no Exchange organization in AD | No Microsoft Exchange object in the configuration partition | License the user | Exchange Online `Set-Mailbox -Type Shared` |
| B. Hybrid with an Exchange server | Exchange organization and servers present | `Enable-RemoteMailbox` | `Set-RemoteMailbox -Type Shared` on-premises; if the mailbox was migrated from on-premises, also `Set-Mailbox -Type Shared` in Exchange Online |
| C. Hybrid with Exchange management tools only (last server removed) | Exchange organization present, no servers, management tools installed on the host | `Enable-RemoteMailbox` | As B |

Mixing models (changing the type only in the cloud for a model B or C customer) is what makes Exchange Online
revert a shared mailbox (research 4.2), so the tool refuses it.

## 5. Role change

| # | Idea | Control | Priority | Slice |
| --- | --- | --- | --- | --- |
| R26 | One workflow for a transfer: remove the old template's groups, add the new template's groups, update title, department, manager and OU, adjust licenses; groups outside both templates are listed for a decision instead of kept silently | PS.L2-3.9.2 [c], AC.L2-3.1.5 | High | D5 |
| R27 | Temporary access that ends on a date (project groups, elevated groups), using AD's link TTL where the Privileged Access Management feature is enabled, else a stage in the service | AC.L2-3.1.5, AC.L2-3.1.7 | Low | D5 |

## 6. Sync and the Microsoft 365 side

| # | Idea | How | Priority | Slice |
| --- | --- | --- | --- | --- |
| R28 | Sync server health: Connect Sync version against Microsoft's retirement table (2.5.79.0 ends October 23, 2026; 2.6.84.0 with application-based authentication required by April 7, 2027), staging mode, scheduler, last cycle, export errors | Local or remote `ADSync` cmdlets; the table ships with the tool and is dated | High | D2 |
| R29 | Conflict finder: duplicate `proxyAddresses`, UPN and `mail` across AD and Microsoft 365; UPN suffixes not verified in the tenant; objects in sync error; tenant hard and soft match blocks left off after a migration | LDAP and Graph | Medium | D2 |

## 7. Lookups and hygiene

| # | Idea | How | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- |
| R30 | Lockout investigation: state, `badPwdCount` per DC, lockout time, the PDC emulator's event 4740 caller computer, then 4771 and 4776 failures from that machine | LDAP per DC; Security log on the PDC emulator (Event Log Readers) | AC.L2-3.1.8, AU.L2-3.3.1 | High | D6 |
| R31 | Last logon truth: `lastLogon` from every DC and `lastLogonTimestamp` (replicated, up to about 14 days behind) | LDAP per DC | IA.L2-3.5.6 | High | D1 |
| R32 | Password expiry: when a password expires under the domain or fine-grained policy; list of passwords expiring in N days | `msDS-UserPasswordExpiryTimeComputed`, `msDS-ResultantPSO` | IA.L2-3.5.7 | Medium | D6 |
| R33 | Computer lookups: LAPS password (Windows LAPS and legacy) and BitLocker recovery keys, each read recorded | LDAP; Windows LAPS decryption API | IA.L2-3.5.10, AU.L2-3.3.1 | Medium | D6 |
| R34 | Stale accounts: enabled users and computers without logon in the customer's period; disabled accounts still in groups | R31 | IA.L2-3.5.6 | High | D6 |
| R35 | Privileged group review: Domain, Enterprise and Schema Admins, Administrators, Account, Backup and Server Operators, DnsAdmins, Group Policy Creator Owners; `adminCount = 1` orphans; admins outside Protected Users; who can create dMSAs | LDAP and security descriptors | AC.L2-3.1.5, AC.L2-3.1.7 | High | D6 |
| R36 | Risky account flags: password never expires, not required, reversible encryption, no Kerberos pre-authentication, unconstrained delegation | `userAccountControl` | IA.L2-3.5.7 | Medium | D6 |

## 8. Safety

| # | Idea | Why |
| --- | --- | --- |
| R37 | Plan, review, run, record for every change (section 1) | Consistency and evidence |
| R38 | Tier 0 fence (design 3, principle 4) | Keeps help desk work away from the keys to the domain |
| R39 | Bulk caps: a plan over 25 objects (configurable) needs the count typed back | Bulk power without the bulk accident |
| R40 | Setup delegates the gMSA's rights as a reviewed plan and shows them later on the settings page; an "effective rights" check warns when someone has widened them | AC.L2-3.1.5 |
| R41 | App registration review on the settings page: permissions granted versus features switched on, certificate expiry, last use | AC.L2-3.1.5 |
| R42 | Change records append-only and hash-chained; the tool never edits a past record | AU.L2-3.3.8 |

## 9. Later

| # | Idea | Note |
| --- | --- | --- |
| R43 | HR request form or CSV drop that creates starter and leaver requests waiting for a technician | Never applied without a person |
| R44 | Email the manager the starter sheet or the leaver summary | Needs `Mail.Send` limited to one mailbox through Exchange RBAC for Applications |
| R45 | RADIUS (for example NPS with MFA) as an extra factor for console sign-in | Question Q6 |
| R46 | Network console on technicians' workstations | Question Q5 |

## 10. Rejected

| Idea | Why not |
| --- | --- |
| One installation managing many customers, or a cloud-hosted portal | The tool lives inside one customer's network by design (design 1) |
| A Domain Admin service account, or Global Administrator for the app | Narrow identities (design 6) |
| Client secrets, stored passwords, emailed or printed passwords | Certificate and gMSA only; passwords shown once |
| Writing Exchange recipient attributes directly instead of using the Exchange tools | Unsupported by Microsoft and the cause of mismatched mailboxes (section 4.3) |
| Attack-path or exploitation features | Assessment tools do this (research 6) |
| Automatic remediation of hygiene findings | Every change goes through a plan and a person |
| Background update checks | Repository rule 7 |
