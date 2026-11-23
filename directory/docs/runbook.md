# ZTG Directory: runbook

Status (October 22, 2026): plan for owner review; nothing is built yet. This file describes what ZTG Directory
does, step by step, for the work a technician does at a customer: everyday console work, onboarding, offboarding,
role changes, sync and lookups. Each idea names the slice of [design.md](design.md) that builds it; the facts
behind it are in [research.md](research.md).

Ideas R47 to R117 were added on November 23, 2026 from a review of comparable tools (research section 8). They
focus on everyday administration (search, the object page, editing, sync status, creation data and reports)
and on gaps in the workflows. Their slices are proposals until the owner answers design question Q10.

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

### 2.1 Search and navigation

| # | Idea | How | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- |
| R47 | Quick filters on every list: disabled, locked, password expired, never logged on, created in the last N days, unlicensed, sync error, out of step | Fixed LDAP filters; cloud filters from the mirror data | | High | D2 |
| R48 | Query builder: attribute, operator and value rows compiled to an escaped LDAP filter, with the filter text shown so it can be copied into PowerShell | The same escaping as the D1 search | | Medium | D2 |
| R49 | Searches across both directories, for example "enabled in AD and unlicensed in Microsoft 365" or "in the sync scope and missing in the cloud" | LDAP results joined to Graph through the mirror match (design 7) | | Medium | D2 |
| R50 | Saved searches, private to a technician or shared for the customer (sharing one is recorded) | Stored beside the templates | | Medium | D2 |
| R51 | Results grid: columns chosen from AD and cloud fields, sorting, CSV and JSON export, rows selected into a bulk plan (R66). Search and every report (section 7.2) use the same grid and click through to the object | One grid component | | High | D2 (view), D3 (bulk) |
| R52 | Effective membership search: everyone who belongs to a group directly or through nesting, with the path | `LDAP_MATCHING_RULE_IN_CHAIN` on `memberOf` | AC.L2-3.1.5 | High | D1 (CLI), D2 |
| R53 | OU tree with the number of users, groups and computers in each OU; OUs outside the sync scope shaded | One-level LDAP counts; sync scope as in R6 | | High | D2 |
| R54 | Organization navigation: the manager chain upwards and direct reports downwards from any user | `manager`, `directReports` | | Medium | D2 |
| R55 | Deleted objects search: the AD Recycle Bin and Entra deleted users, with deletion time and last known OU | LDAP Show Deleted Objects control (1.2.840.113556.1.4.417), `lastKnownParent`; Graph `directory/deletedItems` | | Medium | D2 |
| R56 | Recent and pinned objects for each technician | Kept locally by the console | | Low | D2 |

### 2.2 The object page

| # | Idea | How | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- |
| R57 | Status header: enabled, locked, password expired, account expiry, synced, licensed and mailbox type as badges, each with its underlying value | `userAccountControl`, `lockoutTime`, `msDS-UserPasswordExpiryTimeComputed`, `accountExpires`, mirror data | | High | D2 |
| R58 | Lifecycle timeline: created in AD (`whenCreated`) and by whom (the change record, or event 4720 while the DC's log still holds it; AD keeps no creator attribute for users), first seen in the cloud (`createdDateTime`), mailbox created (`WhenMailboxCreated`), each license's assigned date (`assignedPlans`), password last set, last logon, last change, account expiry | LDAP; Security log (Event Log Readers); Graph; Exchange Online | AU.L2-3.3.1 | High | D1 (AD dates, CLI), D2 |
| R59 | Attribute history: for each attribute, when it last changed, how many times (its version) and on which DC the change originated | `msDS-ReplAttributeMetaData` (research 8.2); it names the DC, not the person | CM.L2-3.4.3 | High | D1 (CLI), D2 |
| R60 | Group membership history: the date the user was added to each group, and recent removals still held as absent values | `msDS-ReplValueMetaData` on each group, read in bounded ranges; memberships older than the forest's linked value replication have no date and show "date unknown" | AC.L2-3.1.5 | High | D1 (CLI), D2 |
| R61 | Decoded attribute view: `userAccountControl` flags named, Windows times as dates, SIDs, GUIDs, the ImmutableId as Base64 and as a GUID, binary values in hex | Syntax from the schema cache | | Medium | D1 (CLI), D2 |
| R62 | Password panel: last set, expiry date, the policy that applies (the domain policy or the fine-grained policy and its precedence), "must change at next logon" | `pwdLastSet`, `msDS-UserPasswordExpiryTimeComputed`, `msDS-ResultantPSO` | IA.L2-3.5.7 | High | D2 (the expiry list stays R32 in D6) |
| R63 | Cloud sign-in beside AD logon: the last successful cloud sign-in next to `lastLogon` from every DC (R31) | Graph `signInActivity.lastSuccessfulSignInDateTime` (`AuditLog.Read.All`; licensing to confirm in D2) | IA.L2-3.5.6 | High | D2 |
| R64 | Computers the user manages (`managedBy`) and the user's Entra registered devices | LDAP; Graph `registeredDevices` | | Low | D2 |

### 2.3 Editing and bulk changes

| # | Idea | How | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- |
| R65 | Pending changes tray: edits on an object page collect into one plan that is reviewed once, instead of one plan per field | Plan engine | CM.L2-3.4.3 | High | D3 |
| R66 | Bulk edit from a selection: set or clear an attribute, add or remove a group, move to an OU | One plan under the bulk cap (R39) | CM.L2-3.4.3 | High | D3 |
| R67 | Bulk changes from CSV: any template attribute, rows matched by employee ID, UPN or `objectGUID`, checks per row, one plan for the file | The R16 engine | CM.L2-3.4.3 | Medium | D5 |
| R68 | Find and replace across users, for example an office or department rename, with every affected object listed in the plan | LDAP search on the old value | CM.L2-3.4.3 | Medium | D3 |
| R69 | Reassign all direct reports of one manager to another | `manager` on each report | | Medium | D3 |
| R70 | Proxy address editor: primary `SMTP:` versus `smtp:` aliases, X500 addresses kept, uniqueness checked in AD and Microsoft 365 before the plan | LDAP and Graph lookups, as R10 | | High | D3 |
| R71 | Add group members from a pasted list of names, UPNs or addresses; each line resolved, and unresolved lines shown before the plan | ANR for each line | | Medium | D3 |
| R72 | Compare two users (groups and attributes) or two groups (members) side by side; "make the same" builds a plan for the differences the technician ticks | Diff view | AC.L2-3.1.5 | Medium | D2 (view), D3 (apply) |
| R73 | Contractor and temporary accounts: an end date (`accountExpires`) on the form, and a list of accounts expiring in N days | LDAP | AC.L2-3.1.1 | Medium | D3 |
| R74 | Restore a deleted user or group from the AD Recycle Bin as a plan: last known OU, the memberships that come back, and the cloud effect (assessment: a restore inside Entra's soft-delete period brings the cloud user back through sync; to confirm in D3). Interactive only, because it needs the Reanimate Tombstones right | Undelete through LDAP with the Show Deleted Objects control: remove `isDeleted`, set the new DN; needs the Recycle Bin (R101) | CM.L2-3.4.3 | Medium | D3 |
| R75 | Ticket note: every completed plan offers a copy-ready note (what changed, by whom, when, the result) for the customer's PSA ticket; nothing is sent anywhere | Rendered from the record | AU.L2-3.3.2 | Medium | D3 |

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
| R76 | Live preview while the form is filled in: UPN, `sAMAccountName`, primary SMTP and display name generated from the template, each with its collision status shown beside it | The R10 naming engine with lookups as the technician types | AC.L2-3.1.1 | High | D3 |
| R77 | Picklists for department, office, title, company and city built from the values already in AD (with counts), so new users match the existing spelling | Distinct values read at connect and refreshed | | High | D3 |
| R78 | Attribute rules per customer: required fields, formats (phone, employee ID), allowed values and uniqueness, applied to every plan including interactive edits, not only to templates; a failed rule blocks the plan and says why | Rule file beside the templates | CM.L2-3.4.3 | Medium | D3 |
| R79 | Draft starters: a starter saved ahead of time is created disabled and enabled by a stage on the start date; the console lists upcoming starters | Service stage | AC.L2-3.1.1 | Medium | D5 |
| R80 | Templates for groups and contacts: naming, OU, owner (`managedBy`), mail settings | The template engine | | Low | D5 |

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
| R81 | Inbox rules: the snapshot lists the leaver's rules and flags any that forward or redirect outside the organization; the plan removes them | `Get-InboxRule`, `Remove-InboxRule`, app-only | AU.L2-3.3.1 | High | D4 |
| R82 | The leaver's own access elsewhere: Full Access, Send As and calendar permissions the leaver holds on other mailboxes, listed and removed | Exchange Online permission cmdlets; reading every mailbox is slow, so the scope and cost are sized in D4 (assessment) | AC.L2-3.1.1 | Medium | D4 |
| R83 | Cancel future meetings the leaver organized, with an optional note to attendees | `Remove-CalendarEvents -CancelOrganizedMeetings` | | Medium | D4 |
| R84 | OneDrive: give the manager access, with the date by which files must be moved and the date OneDrive retention deletes them | SharePoint Online; app-only needs `Sites.FullControl.All` (design Q12), otherwise a recorded manual step | PS.L2-3.9.2 [c] | Medium | D4 |
| R85 | Sharing links the leaver created: listed and removed | SharePoint Online, as R84 | | Low | D4 |
| R86 | Cloud admin roles: the snapshot lists the leaver's Entra role assignments, active and eligible; any assignment stops the workflow as a Tier 0 case | `RoleManagement.Read.Directory` | AC.L2-3.1.7 | High | D4 |
| R87 | App consents: the snapshot lists the delegated permission grants and app role assignments the leaver holds; removal is a manual step by default (design Q13) | Graph `oauth2PermissionGrants`, `appRoleAssignments` | AC.L2-3.1.1 | Medium | D4 |
| R88 | Personal phones: wipe only the company account from devices that sync mail but are not managed by Intune | `Clear-MobileDevice -AccountOnly` (assessment, to confirm in D5) | | Medium | D5 |
| R89 | Urgent leaver: block the cloud account at once by adding it to a cloud-only group that a customer-owned Conditional Access policy blocks, revoke sessions, then disable in AD and sync; take the user out of the group once the cloud shows sign-in blocked | Entra ID P1; `GroupMember.ReadWrite.All`, `Policy.Read.All` to confirm the policy exists (design Q11) | PS.L2-3.9.2 [b] | High | D4 |
| R90 | Leaver timeliness: the record holds the HR leaving time, the AD disable time and the time the cloud showed sign-in blocked; reports (R106, R115) show the gaps against the customer's defined time | Record fields | PS.L2-3.9.2 [b] | High | D4 |

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

### 4.4 Proposed changes to the procedure

If the owner accepts R81 to R90, the procedure in 4.2 changes as follows:

| Step | Addition |
| --- | --- |
| 0 | Urgent mode (R89): the cloud block group and session revocation run before step 2 |
| 1 | The snapshot adds inbox rules (R81), the leaver's access to other mailboxes (R82), Entra role assignments (R86) and app consents (R87); any role assignment stops the workflow |
| 6 | Remove flagged inbox rules (R81) and the leaver's access to other mailboxes (R82); cancel organized meetings (R83) |
| 8 | OneDrive access for the manager and sharing links (R84, R85), where the customer switched them on |
| 10 | Account-only wipe for unmanaged phones (R88) |
| 12 | The record holds the three times of R90 |

## 5. Role change and access reviews

| # | Idea | Control | Priority | Slice |
| --- | --- | --- | --- | --- |
| R26 | One workflow for a transfer: remove the old template's groups, add the new template's groups, update title, department, manager and OU, adjust licenses; groups outside both templates are listed for a decision instead of kept silently | PS.L2-3.9.2 [c], AC.L2-3.1.5 | High | D5 |
| R27 | Temporary access that ends on a date (project groups, elevated groups), as a stage in the service by default; AD's link TTL only where the customer already enabled the Privileged Access Management feature, which needs the Windows Server 2016 forest functional level and cannot be turned off. Raised from Low in November 2026: comparable tools lead with it (research 8.1) | AC.L2-3.1.5, AC.L2-3.1.7 | Medium | D5 |
| R91 | Access reviews: on a schedule the customer sets, each group's owner (`managedBy`) or each user's manager confirms or rejects every member, seeing last logon (R111) and mirror state. There is no owner portal (design 13): a technician enters the decisions, recorded with the reviewer's name and the ticket. Rejections become one plan; the finished review is evidence. Rev 3 03.01.05 makes the frequency of privilege review an organization-defined parameter (research 8.3) | AC.L2-3.1.1, AC.L2-3.1.5, AC.L2-3.1.7 | High | D5 |
| R92 | Rule-based groups: a group defined by a rule (for example department is Finance and the account is enabled); each evaluation produces a plan of additions and removals. Whether an approved rule may apply unattended within the bulk cap is design Q14 | AC.L2-3.1.5 | Medium | D5 |

## 6. Sync and the Microsoft 365 side

| # | Idea | How | Priority | Slice |
| --- | --- | --- | --- | --- |
| R28 | Sync server health: Connect Sync version against Microsoft's retirement table (2.5.79.0 ends October 23, 2026; 2.6.84.0 with application-based authentication required by April 7, 2027), staging mode, scheduler, last cycle, export errors | Local or remote `ADSync` cmdlets; the table ships with the tool and is dated | High | D2 |
| R29 | Conflict finder: duplicate `proxyAddresses`, UPN and `mail` across AD and Microsoft 365; UPN suffixes not verified in the tenant; objects in sync error; tenant hard and soft match blocks left off after a migration | LDAP and Graph | Medium | D2 |
| R93 | Sync badge in every list and search result: synced, waiting for sync, out of step, sync error, outside the sync scope, cloud only, possible match | Mirror match for each row, Graph reads batched | High | D2 |
| R94 | "Why is this not syncing?": the scope check (OU, group or attribute filtering), the object's provisioning errors, and for a duplicate value the other object that holds it | Graph `onPremisesProvisioningErrors`; R29 data; the sync configuration | High | D2 |
| R95 | Sync clock on each object: AD last change, cloud last sync, the last cycle's result and when the next cycle is due | `whenChanged`, `onPremisesLastSyncDateTime`; Connect Sync `Get-ADSyncScheduler` (`NextSyncCycleStartTimeInUTC`); Cloud Sync job status through Graph | High | D2 |
| R96 | Pending-sync queue: objects in the sync scope changed in AD since the last cycle started | `whenChanged` after the cycle's start time (assessment: approximate, because DCs replicate at different times) | Medium | D2 |
| R97 | Tenant sync summary: counts of synced, cloud-only, in error, out of step and possible matches, each count opening its list | Aggregate of R93 | Medium | D2 |

## 7. Lookups, hygiene and reports

### 7.1 Lookups and hygiene

| # | Idea | How | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- |
| R30 | Lockout investigation: state, `badPwdCount` per DC, lockout time, the PDC emulator's event 4740 caller computer, then 4771 and 4776 failures from that machine | LDAP per DC; Security log on the PDC emulator (Event Log Readers) | AC.L2-3.1.8, AU.L2-3.3.1 | High | D6 |
| R31 | Last logon truth: `lastLogon` from every DC and `lastLogonTimestamp` (replicated, up to about 14 days behind) | LDAP per DC | IA.L2-3.5.6 | High | D1 |
| R32 | Password expiry: when a password expires under the domain or fine-grained policy; list of passwords expiring in N days | `msDS-UserPasswordExpiryTimeComputed`, `msDS-ResultantPSO` | IA.L2-3.5.7 | Medium | D6 |
| R33 | Computer lookups: LAPS password (Windows LAPS and legacy) and BitLocker recovery keys, each read recorded | LDAP; Windows LAPS decryption API | IA.L2-3.5.10, AU.L2-3.3.1 | Medium | D6 |
| R34 | Stale accounts: enabled users and computers without logon in the customer's period; disabled accounts still in groups | R31 | IA.L2-3.5.6 | High | D6 |
| R35 | Privileged group review: Domain, Enterprise and Schema Admins, Administrators, Account, Backup and Server Operators, DnsAdmins, Group Policy Creator Owners; `adminCount = 1` orphans; admins outside Protected Users; who can create dMSAs | LDAP and security descriptors | AC.L2-3.1.5, AC.L2-3.1.7 | High | D6 |
| R36 | Risky account flags: password never expires, not required, reversible encryption, no Kerberos pre-authentication, unconstrained delegation | `userAccountControl` | IA.L2-3.5.7 | Medium | D6 |
| R98 | Changes made outside the tool: watermarks on `uSNChanged` for each DC find objects in the managed OUs and Tier 0 that changed since the last check; replication metadata (R59) names the attribute and the originating DC; event 5136 on that DC names the person and the old and new values. Shown beside the change records, so a technician sees every change, not only the tool's. Reverting is a plan (R8), never automatic. A DC's watermark resets when its `invocationId` changes after a restore | Ordinary LDAP reads, not the DirSync control (it needs replication rights); Security log (Event Log Readers); needs directory service change auditing and SACLs on the audited OUs (design Q16) | CM.L2-3.4.3, AU.L2-3.3.1 | High | D6 |
| R99 | Kerberos RC4 readiness: accounts with SPNs and no explicit `msDS-SupportedEncryptionTypes`, accounts that allow only RC4, and accounts whose password predates the domain's AES support, which have no AES keys until the password is reset (assessment: the date is estimated from the creation of the Read-only Domain Controllers group). Events 4768 and 4769 with ticket encryption type 0x17 show what still uses RC4. Microsoft's 2026 enforcement phases (research 8.3) make this a current check | LDAP; Security log | SC.L2-3.13.11 (assessment) | High | D6 (candidate for the D1 CLI, design Q10) |
| R100 | Entra Password Protection status: the DC agent on every writable DC, the proxy registered, the tenant in Audit or Enforce mode, the custom banned list on or off | Service connection points under each DC's computer object (assessment, to confirm in D6); Graph directory settings (permission to confirm) | IA.L2-3.5.7 | High | D6 |
| R101 | AD Recycle Bin: whether it is enabled, with the warning that enabling it is forest-wide and permanent; R74 depends on it | `msDS-EnabledFeature` on the Partitions container | | Medium | D6 |
| R102 | Cloud account hygiene: guests without a successful sign-in in N days, cloud-only users whose UPN or address would soft match an AD user, licensed users whose sign-in is blocked, leavers past their retention date who are still licensed | Graph `signInActivity`, mirror data, records | IA.L2-3.5.6 | High | D6 |
| R103 | MFA and risk on the mirror pane: the number and kinds of registered sign-in methods, "no MFA method registered", and the Entra ID Protection risk level where the customer has it | Graph `userRegistrationDetails` (`AuditLog.Read.All`, licensing to confirm), `riskyUsers` (`IdentityRiskyUser.Read.All`, Entra ID P2) | IA.L2-3.5.3 | Medium | D2 |
| R104 | Lockout cause beyond the caller computer (R30): cloud sign-in failures for the same user (password spray versus a stale device), Exchange mobile devices with recent failed syncs, and a note that event 4740 names the proxy (Exchange, AD FS, NPS) rather than the client. Services, scheduled tasks and mapped drives on the caller computer are read by the technician with their own rights, never by the service | Graph sign-in logs (`AuditLog.Read.All`, Entra ID P1); `Get-MobileDeviceStatistics`; remote reads as the technician | AC.L2-3.1.8 | High | D6 |

### 7.2 Reports and analytics

Every report uses the R51 grid: it opens the object, exports, and turns selected rows into a plan.

| # | Report | Source | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- |
| R105 | Home dashboard: users by state (enabled, disabled, locked now, password expired), starters and leavers this month, the sync summary (R97), license use (R112) | LDAP counts, Graph, records | | High | D2 |
| R106 | Joiner and leaver trends by month and department, and the time from request to completion | `whenCreated`, records | PS.L2-3.9.2 | Medium | D4 |
| R107 | Attribute completeness: users missing a manager, department, title, phone, employee ID or usage location, by department and OU | LDAP | | High | D2 |
| R108 | Distribution: users per OU, department, office and title; direct reports per manager | LDAP | | Medium | D2 |
| R109 | Groups: empty groups, groups without an owner, the largest groups, nesting depth, groups with disabled members, groups whose membership has not changed in N days | LDAP, R60 | AC.L2-3.1.5 | Medium | D6 |
| R110 | Passwords: expiring in N days (R32), age distribution, never expires, "must change at next logon" left set for more than N days | LDAP | IA.L2-3.5.7 | Medium | D6 |
| R111 | Activity: last logon in buckets (0 to 30 days, 31 to 90, over 90, never) combining AD (R31) and the cloud (R63), so an account used only in the cloud is not reported as stale | LDAP for each DC, Graph | IA.L2-3.5.6 | High | D6 |
| R112 | Licenses: purchased, assigned and available per SKU; by department; disabled but licensed users and leavers past retention (R102), to reclaim | Graph `subscribedSkus`, users | | High | D2 |
| R113 | Changes in a period from the records and from R98, filtered by technician, object, OU and attribute | Records, R98 | CM.L2-3.4.3, AU.L2-3.3.2 | High | D3 (records), D6 (R98) |
| R114 | Scheduled reports: chosen reports written as CSV, HTML and JSON to a protected share | Service | | Medium | D5 |
| R115 | Evidence pack: a dated bundle for a period with privileged group membership and its changes (R35, R60), stale accounts (R111), leaver timeliness (R90), access review results (R91), MFA registration (R103) and verification of the record chain (R42) | Service, records | AC.L2-3.1.1, AC.L2-3.1.7, AU.L2-3.3.1, PS.L2-3.9.2 | High | D6 |

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
| R116 | Entra Lifecycle Workflows: where the customer licenses Entra ID Governance, set `employeeHireDate` and `employeeLeaveDateTime` so the customer's cloud workflows fire on the same dates as ZTG Directory's, and the two never both run a leaver | Which of these can flow from AD is to confirm |
| R117 | Direct PSA integration, for example writing R75's note to the ticket | Sends data outside Microsoft 365 (design 3, principle 7); question Q15 |

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
| An AI assistant that turns typed requests into actions | Directory data would leave the customer's network (design 3, principle 7), and plans must come from templates and rules a person can review |
| Automatic undo of changes made outside the tool | Detection and a revert plan instead (R98); every change goes through a person |
| Breached or shared password audits that compare password hashes | Reading hashes needs directory replication rights, which are Tier 0; R100 checks Entra Password Protection instead |
| A portal where group owners or managers do access reviews themselves | Self-service portals are out of scope (design 13); a technician records the owners' decisions (R91) |
