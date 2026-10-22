# ZTG AD Admin: runbook of ideas

Status (October 22, 2026): proposal for owner review. Every idea below comes from the field survey in
[research.md](research.md) or from MSP practice; each says how it would work, where the idea comes from, which
control it supports and which slice of [design.md](design.md) would build it. Nothing here is built yet.

Priority: **High** is part of the first useful release; **Medium** is clearly worth doing; **Low** waits for
demand. Control references are CMMC 2.0 Level 2 with NIST SP 800-171 Rev 2 numbering unless marked L1.

Every change idea follows the same pattern, so it is stated once here instead of in every row:

1. **Plan**: the tool reads the current state and computes exactly what it will change (object, attribute,
   old value, new value). Nothing is written.
2. **Review**: the technician sees the plan, with warnings (synced object, Tier 0 object, out-of-scope move),
   and enters the ticket reference.
3. **Apply**: changes run in the order the plan states, each one checked after writing.
4. **Record**: one change record per job, with requester, ticket, acting user, machine, every step's before
   and after, and every failure. Exportable as JSON and as a readable PDF/HTML for the client file.

## 1. Onboarding (create)

| # | Idea | How | Source | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- | --- |
| I1 | Role templates per client: OU, groups, attribute defaults (company, department, office, manager), UPN suffix, home drive, `extensionAttribute`s Exchange or licensing rules rely on | A versioned template file per client domain, stored alongside the change records; reviewed like code | ADManager Plus, Adaxes, Cayosoft | AC.L1-b.1.i, AC.L2-3.1.1, AC.L2-3.1.5 | High | A3 |
| I2 | Naming rules with collision handling: UPN and `mail` from first and last name, `sAMAccountName` within 20 characters, diacritics folded, numbered on collision, checked against AD and (for hybrid) Entra proxyAddresses before create | LDAP search for `userPrincipalName`, `sAMAccountName`, `proxyAddresses`; Graph `GET /users?$filter=proxyAddresses/any(...)` | ADManager Plus, IdFix | AC.L2-3.1.1 | High | A3 |
| I3 | Temporary password: generated passphrase, "must change at next logon" always set, shown once in the app and never written to the record, log or clipboard history | `unicodePwd` over a sealed connection; `pwdLastSet = 0` | RSAT behaviour made the default | IA.L2-3.5.9, IA.L2-3.5.10 | High | A3 |
| I4 | Bulk create from CSV with column mapping, per-row validation and a full plan before any write | Same engine as I1 to I3, one plan per file | ADManager Plus, Adaxes | CM.L2-3.4.3 | Medium | A3 |
| I5 | Hybrid-ready check before create: domain suffix verified in the tenant, UPN routable, `mS-DS-ConsistencyGuid` empty (let sync stamp it), OU inside the sync scope, no cloud-only user already holding the UPN (would soft match) | Graph `GET /domains`, `GET /users`; sync scope read from the Connect Sync configuration (A5) | Entra Connect matching rules (research 6.1) | AC.L2-3.1.1 | High | A5 |
| I6 | "Sync now" after create, then wait for the cloud object and show it (licensing group membership, mailbox provisioning state) | WinRM to the sync server: `Start-ADSyncSyncCycle -PolicyType Delta`; Cloud Sync: Graph on-demand provisioning | Every MSP's manual step | | Medium | A6 |
| I7 | Copy from an existing user with a diff of what will be copied, so stale groups the old user collected are visible and unticked by default when they are not in the role template | Read the source user's `memberOf`, compare with the template | ADUC "Copy" made safe | AC.L2-3.1.5 | Medium | A3 |

## 2. Changes (modify, move, role change)

| # | Idea | How | Source | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- | --- |
| I8 | Role change as one job: remove groups of the old template, add groups of the new one, update title, department, manager, OU; groups outside both templates are listed for a decision instead of being kept silently | Template diff, then the plan pattern | Netwrix Directory Manager, Adaxes | PS.L2-3.9.2 [c] (transfers), AC.L2-3.1.5 | High | A3 |
| I9 | Attribute editor with schema awareness: syntax, single or multi-valued, constructed and system-only attributes read-only, "synced to Entra" badge on attributes the sync engine exports | Schema partition read at connect (`attributeSchema`) | AD Explorer, ADSI Edit | | Medium | A3 |
| I10 | Unlock and reset with the lockout context on the same screen (I24) so the technician sees the source before unlocking | Write `lockoutTime = 0` | LockoutStatus.exe | AC.L2-3.1.8 | High | A3 |
| I11 | Temporary group membership (ends on a date) for project or elevated access, using AD's own TTL links where the forest has the Privileged Access Management optional feature, and the change record's follow-up list elsewhere | `member` with TTL via the LDAP control `LDAP_SERVER_LINK_TTL_OID` (1.2.840.113556.1.4.2309) | Windows Server 2016 PAM feature | AC.L2-3.1.5, AC.L2-3.1.7 | Low | A7 |
| I12 | Undo for a recorded job: restore attributes and group memberships from the record's "before" values (passwords and deletions excluded) | The record is the undo source | Adaxes, Active Roles (assessment) | CM.L2-3.4.3 | Medium | A3 |

## 3. Offboarding (leaver)

### 3.1 Ideas

| # | Idea | How | Source | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- | --- |
| I13 | One ordered leaver job for AD and Microsoft 365 together (procedure in 3.2), run now or scheduled for the leaving time | On-premises through LDAP, cloud through Graph and Exchange Online; scheduling through a Windows scheduled task that runs the CLI with the approved plan file | CIPP covers only the cloud half (research 4) | PS.L2-3.9.2 [a][b] | High | A4, A6 |
| I14 | Snapshot before any change: attributes, direct group memberships, manager and direct reports, mailbox delegates, licenses, registered devices; stored in the change record | Read-only phase of the plan | ADManager Plus reports | PS.L2-3.9.2, AU.L2-3.3.1 | High | A4 |
| I15 | Leaver profiles per client (what to keep: mailbox as shared, OneDrive access for the manager, forward to whom and for how long) so the technician answers three questions, not twenty | Profile file beside the role templates | CIPP saved defaults per tenant | PS.L2-3.9.2 [a] | High | A4 |
| I16 | Sync-scope guard: warn and refuse by default when moving a synced user to an OU outside the sync scope before the mailbox is converted, because the next sync soft-deletes the cloud user and its mailbox | Compare target OU with the sync scope (A5) | Common MSP incident (assessment) | | High | A4 |
| I17 | Staged removal: day 0 disable and cut access, day N (client policy, often 30 or 90 days) remove licenses, convert or delete, delete the AD object; the tool lists due stage-2 jobs when opened, no background service | Pending stages kept in the change record store | Retention practice | PS.L2-3.9.2 | Medium | A4 |
| I18 | Leaver evidence pack: the change record rendered as a one-page PDF or HTML with requester, approver, timestamps and step results, ready for the client's personnel file and an assessor's sample | Export from the record | Gap in every tool surveyed | PS.L2-3.9.2, AU.L2-3.3.2 | High | A4 |

### 3.2 Leaver procedure (the order the job runs)

Order matters: access is cut first, then sessions, then data is preserved, and destructive steps come last.
Hybrid users are mastered in AD, so sign-in is blocked on-premises and reaches the cloud by sync.

| Step | Action | Where | Notes |
| --- | --- | --- | --- |
| 0 | Plan, ticket reference, effective time, leaver profile | Tool | Refuses Tier 0 accounts unless the job is explicitly marked privileged (design 4) |
| 1 | Snapshot (I14) | AD, Graph, Exchange Online | Read-only |
| 2 | Disable the AD account; set `accountExpires` to now; reset the password to a random unknown value | AD | Existing Kerberos tickets stay valid until they expire (10 hours by default); step 4 covers cloud sessions |
| 3 | Trigger a delta sync and wait for `accountEnabled = false` in Entra (cloud-only users: set it directly) | Sync server, Graph | Shows the sync engine and last sync time (I20) |
| 4 | Revoke sign-in sessions | Graph `revokeSignInSessions` | "A small delay of a few minutes" (research 6.2) |
| 5 | Remove MFA methods and registered authenticators | Graph authentication methods | Stops a re-enabled account from inheriting the leaver's factors |
| 6 | Mobile and Intune: wipe account data or retire company data, per profile | Graph Intune | Retire, not full wipe, on personal devices |
| 7 | Mailbox: convert to shared, set delegates, forwarding with an end date, auto-reply | Exchange Online | Must come before any OU move out of sync scope (I16) and before license removal |
| 8 | Hide from the address lists | AD `msExchHideFromAddressLists` when the Exchange schema is present (synced), else Exchange Online | Mastered on-premises for synced users |
| 9 | Remove group memberships (recorded in step 1), reassign group ownership and direct reports | AD, then Graph for cloud-only groups | Synced groups can only be changed in AD |
| 10 | OneDrive: grant the manager access for the retention period; disable sharing links | Graph, SharePoint | |
| 11 | Remove licenses (stage 2 if the profile retains them) | Graph | A shared mailbox under 50 GB needs no license unless it has an archive or hold (assessment, confirm per client plan) |
| 12 | Move to the client's leavers OU (inside sync scope unless the profile says the cloud object should go) | AD | |
| 13 | Record and evidence pack (I18) | Tool | |
| Stage 2 | Delete the AD object (Recycle Bin must be enabled, checked first); delete or keep the cloud object per profile | AD, Graph | Day N per client policy |

## 4. Hybrid sync and Entra lookups

| # | Idea | How | Source | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- | --- |
| I19 | Sync server check: Connect Sync version against Microsoft's retirement table, application-based authentication configured, staging mode, scheduler state and next run, last cycle result. Flags 2.5.79.0 end of support (October 23, 2026) and the April 7, 2027 deadline for 2.6.84.0 plus application-based authentication | Remote read on the sync server (WinRM, `Get-ADSyncScheduler`, product version); the retirement table is shipped with the tool and dated | Version history page (research 6.1) | CM.L2-3.4.1 (baseline), SI.L2-3.14.1 (flaw remediation) | High | A5 |
| I20 | Per-object sync card on every user, group and contact: synced or cloud-only, engine (Connect or Cloud Sync), sourceAnchor and its Base64 form next to `onPremisesImmutableId`, `onPremisesLastSyncDateTime`, provisioning errors, source of authority | LDAP plus Graph `onPremises*` properties and `onPremisesSyncBehavior` | Entra admin center, ADSyncTools | | High | A5 |
| I21 | Duplicate and conflict finder (IdFix-style): duplicate `proxyAddresses`, UPN and `mail` across AD and Entra; invalid characters; UPN suffixes not verified in the tenant | LDAP and Graph reads | IdFix, Entra Connect Health | | Medium | A5 |
| I22 | Matching assistant for "user exists in both": explains whether soft or hard match will happen, whether it is blocked (`BlockSoftMatchEnabled`, `BlockCloudObjectTakeoverThroughHardMatchEnabled`, July 2026 hard match protections) and what the safe fix is; never changes anchors without a plan and a warning about mailbox takeover | Graph `onPremisesSynchronization` features plus I20 | Existing tenant guide (research 6.1) | AC.L2-3.1.1 | Medium | A6 |
| I23 | Tenant sync posture: last tenant sync time (alert over 3 hours, assessment), hard and soft match blocks off outside a planned migration, Cloud Sync readiness against Microsoft's limits (150,000 objects per domain, 50,000-member groups) | Graph organization and `onPremisesSynchronization` | Decision guide (research 6.1) | | Medium | A5 |

## 5. Lookups and troubleshooting

| # | Idea | How | Source | Control | Priority | Slice |
| --- | --- | --- | --- | --- | --- | --- |
| I24 | Lockout investigation: state, `badPwdCount` and `badPasswordTime` per DC (not replicated), lockout time, the PDC emulator's event 4740 caller computer, then 4771 (Kerberos pre-authentication) and 4776 (NTLM) failures from that machine; common causes listed (mapped drives, services, scheduled tasks, phones) | LDAP per DC; Security log read on the PDC emulator (needs Event Log Readers) | LockoutStatus.exe, Netwrix and Lepide examiners | AC.L2-3.1.8, AU.L2-3.3.1 | High | A1 |
| I25 | Last logon truth: `lastLogon` from every DC (not replicated) and `lastLogonTimestamp` (replicated, up to about 14 days behind by design) shown together with which DC answered | LDAP per DC | Every MSP's script | IA.L2-3.5.6 | High | A1 |
| I26 | Effective group membership, nested, with the path that grants each group | LDAP `LDAP_MATCHING_RULE_IN_CHAIN` (1.2.840.113556.1.4.1941) and `tokenGroups` | ADUC lacks it | AC.L2-3.1.5 | High | A1 |
| I27 | "Who can do what to this object": effective permissions for reset password, write members, full control, from the security descriptor and AdminSDHolder state | Read `nTSecurityDescriptor` and resolve SIDs | AD Explorer, ADUC Effective Access | AC.L2-3.1.5, AC.L2-3.1.7 | Medium | A7 |
| I28 | Universal search box: name, UPN, email, phone, employee ID, SID, GUID, ImmutableId (Base64) across AD and Entra at once | LDAP ambiguous name resolution (`anr`) plus Graph search | Help desk need | | High | A1 (AD), A5 (Entra) |
| I29 | Password expiry lookups: when a user's password expires under the domain policy or a fine-grained policy (`msDS-ResultantPSO`), and a "expiring in N days" list | Constructed attribute `msDS-UserPasswordExpiryTimeComputed` | Every MSP's script | IA.L2-3.5.7 | Medium | A1 |
| I30 | Computer lookups: LAPS password retrieval (Windows LAPS and legacy), BitLocker recovery keys (`msFVE-RecoveryInformation`), OS and last logon; each retrieval recorded | LDAP reads, decryption of `msLAPS-EncryptedPassword` through Windows' own LAPS API where available | Windows LAPS tooling (research 6.4) | IA.L2-3.5.10, AU.L2-3.3.1 | Medium | A7 |
| I31 | DC health summary: replication status and failures, DCs reachable, FSMO holders, time source; a readable verdict instead of repadmin and dcdiag text | LDAP reads of replication metadata (verify which reads are possible without RPC) | repadmin, dcdiag | | Low | A7 |

## 6. Hygiene while you work

Not an assessment scanner (research 5); these are the facts a technician should see while already in the
directory, each one linking to the object.

| # | Idea | Control | Priority | Slice |
| --- | --- | --- | --- | --- |
| I32 | Stale accounts: enabled users and computers with no logon in the client's defined period (I25), never logged on, disabled but still in groups | IA.L2-3.5.6 | High | A7 |
| I33 | Privileged group review: members of Domain Admins, Enterprise Admins, Schema Admins, Administrators, Account Operators, Backup Operators, Server Operators, DnsAdmins, Group Policy Creator Owners; `adminCount = 1` orphans; admin accounts not in Protected Users; admin accounts with mailboxes | AC.L2-3.1.5, AC.L2-3.1.6, AC.L2-3.1.7 | High | A7 |
| I34 | Risky flags: password never expires, password not required, reversible encryption, Kerberos pre-authentication not required, unconstrained delegation, service accounts with SPNs and old passwords | IA.L2-3.5.7, IA.L2-3.5.10 | Medium | A7 |
| I35 | LAPS coverage: computers without a current LAPS password, by OU | IA.L2-3.5.10 | Medium | A7 |
| I36 | Who can create or change dMSAs (BadSuccessor residual risk, research 6.4) | AC.L2-3.1.5 | Medium | A7 |
| I37 | Recycle Bin enabled, tombstone lifetime, domain and forest functional levels | | Low | A7 |

## 7. Safety and evidence (cross-cutting)

| # | Idea | Why |
| --- | --- | --- |
| I38 | Plan, review, apply, record for every change (top of this file) | Consistency and evidence (research 7) |
| I39 | Tier 0 guard: objects protected by AdminSDHolder, members of the groups in I33, DC computer accounts, `krbtgt`, gMSAs and dMSAs need an explicit privileged job and a second confirmation | Stops help-desk slips from touching the keys to the domain |
| I40 | Bulk caps: a job touching more than 25 objects (configurable per client) needs a typed confirmation of the count | ADManager-style bulk power without the bulk accident |
| I41 | Acts as the signed-in technician through AD's own delegation; no service account, no stored credentials; alternate credentials for a client domain are held in memory for the session only | No new Tier 0 asset (research 3) |
| I42 | Change records are append-only, hash-chained JSON lines per client, exportable; the tool never edits a past record | AU.L2-3.3.8 (protect audit information) |
| I43 | Delegation helper: generate least-privilege ACEs for a help desk group on a client's user OUs (reset password, unlock, write specific attributes), shown as a plan; replaces "add the help desk to Domain Admins" | AC.L2-3.1.5 |

## 8. Later and integrations

| # | Idea | Note |
| --- | --- | --- |
| I44 | Create jobs from PSA tickets (Autotask, HaloPSA) and write the evidence pack back to the ticket | Needs credentials for the PSA: only with an owner decision on how they are held |
| I45 | HR feed: a CSV or SFTP drop of starters and leavers turned into plans awaiting a technician's review | Never applied unattended |
| I46 | Multi-forest and trusts view for mergers and acquisitions | Rare at MSP clients |
| I47 | GCC High and DoD tenants: Graph `graph.microsoft.us` and `dod-graph.microsoft.us` endpoints selectable per client | Defense industrial base clients; needed early if those clients are in scope |

## 9. Ideas rejected

| Idea | Why not |
| --- | --- |
| A server or always-on service with its own privileged service account | Creates the Tier 0 asset this tool exists to avoid (I41) |
| Storing client domain credentials or Graph client secrets | Repository rule for Scanner (credentials never on disk) applies equally here; delegated sign-in and Kerberos instead |
| Emailing or printing temporary passwords | The password would outlive the session in a mailbox; shown once (I3) |
| Attack-path or exploitation features (BloodHound-style graphs, Kerberoasting) | Detection belongs to assessment tools (research 5); this tool reports the flags (I34) |
| Automatic remediation of hygiene findings | Every change goes through a plan and a person (I38) |
| Background update checks | Repository rule 7: explicit "Check for updates" only |
