# ZTG AD Admin: market research

Status (October 22, 2026): research for the design in [design.md](design.md) and the ideas in
[runbook.md](runbook.md). Facts are cited; statements marked "assessment" are ZTG's reading and should be
rechecked against the product before they are repeated to a client. Vendor pages are marketing and are
marked as such.

## 1. Why technicians still need a better RSAT

Active Directory Users and Computers (ADUC), Active Directory Administrative Center and the PowerShell
`ActiveDirectory` module remain the way most MSP technicians create, change and disable accounts. They are
free and complete, but every recurring job is still manual (assessment):

| Job | What RSAT gives | What the technician still does by hand |
| --- | --- | --- |
| New starter | A blank new-user dialog or "Copy" of another user | Pick the OU, invent the UPN and sAMAccountName, set a temporary password, add the right groups, fill attributes Exchange and Entra rely on, then wait for or force a sync |
| Change of role | Attribute and Member Of tabs | Work out which groups belong to the old role and which to the new one; nothing records why |
| Leaver | Disable, Move, Reset Password | Remember every step (groups, manager, GAL, mailbox, licenses, sessions, devices), in the right order, on two systems (AD and Microsoft 365), and keep evidence |
| Lockout | Account tab shows "locked" | Find which DC, which machine and which stale credential; LockoutStatus.exe is a separate download |
| Hybrid sync problem | Nothing in RSAT | Log on to the sync server, run `Start-ADSyncSyncCycle`, read errors in the Entra admin center, compare anchors by hand |

For an MSP the cost is consistency: the same leaver handled by two technicians ends in two different states,
and neither leaves a record an assessor can sample (see section 7).

## 2. Native and free tools

| Tool | Platform | Strengths | Gaps (assessment unless cited) |
| --- | --- | --- | --- |
| ADUC / ADAC (RSAT) | Windows | Complete object editing; ADAC adds the AD Recycle Bin UI and PowerShell history | No workflows, templates beyond "Copy", bulk operations, approval or change record; no hybrid view |
| PowerShell `ActiveDirectory` module | Windows | Scriptable everything; talks to Active Directory Web Services | Every MSP writes its own offboarding script; scripts drift between technicians and clients |
| Windows Admin Center, AD extension | Browser via gateway | Basic user, group and OU tasks from a web UI | Light feature set; needs a gateway server (assessment) |
| [AD Explorer](https://learn.microsoft.com/sysinternals/downloads/adexplorer) (Sysinternals) | Windows | Fast attribute viewer and editor; snapshots for offline inspection; snapshot compare of objects, attributes and permissions | No tasks or workflows; last updated v1.52 (2022) |
| LockoutStatus.exe (Microsoft) | Windows | Lockout state, bad password count and time per DC ([Conetrix](https://conetrix.com/blog/a-helpful-account-lockout-tool-from-microsoft---lockoutstatusexe)) | Old download; does not find the source machine |
| [Netwrix Account Lockout Examiner](https://netwrix.com/en/resources/freeware/account-lockout-examiner/) | Windows | Free lockout alerts and source tracing (vendor page) | Separate product; depends on audit policy being enabled |
| repadmin, dcdiag, nltest | Windows | Replication and DC health | Text output; no summary for a technician |
| Microsoft Entra Connect `ADSync` module | Sync server | `Get-ADSyncScheduler`, `Start-ADSyncSyncCycle -PolicyType Delta` ([Learn](https://learn.microsoft.com/azure/active-directory/hybrid/connect/how-to-connect-sync-feature-scheduler)) | Runs only on the sync server; default cycle every 30 minutes |
| ADSyncTools module | Sync server or admin PC | Reads and clears on-premises attributes on cloud users (`Get-ADSyncToolsOnPremisesAttribute`, `Clear-ADSyncToolsOnPremisesAttribute`) ([Learn](https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/how-to-connect-install-existing-tenant)) | PowerShell only |

## 3. Commercial AD management suites

| Product | Positioning | Notable ideas | Caveats |
| --- | --- | --- | --- |
| ManageEngine ADManager Plus | Web console for AD, Exchange and Microsoft 365 | Drag-and-drop provisioning and offboarding workflows, over 200 prepackaged reports ([vendor](https://www.manageengine.com/products/ad-manager/comparison/softerra-adaxes-alternative.html)) | Vendor claims; server install; licensed per domain and per help desk technician (assessment) |
| Softerra Adaxes | Policy-driven AD, Entra and Exchange automation | Business rules that fire on create, modify and delete; approval workflows; web interface for delegated staff (assessment) | Server install; script-heavy for advanced rules |
| One Identity (Quest) Active Roles | Policy gateway in front of AD | Granular delegation, policy enforcement, workflow automation; configuration-heavy, advanced automation needs PowerShell or REST ([aimultiple](https://research.aimultiple.com/active-directory-management-tools)) | Enterprise scale and price |
| [Cayosoft Administrator](https://expertinsights.com/identity-and-access-management/cayosoft-administrator) | Hybrid Microsoft identity: AD, Entra ID, Microsoft 365, Exchange, Teams, Intune | Task-based delegation, onboarding and offboarding automation, group lifecycle, license optimization | Microsoft targets only |
| Netwrix Directory Manager (formerly Imanami GroupID, assessment) | Identity lifecycle and group management portal | Role-based delegation with approvals; dynamic groups maintained from directory data ([vendor](https://netwrix.com/directory-management-software.html)) | Vendor claims |

Common thread (assessment): every suite puts a server and a service account with broad rights between the
technician and AD, then delegates through its own permission model. That service account becomes a Tier 0
asset and a new attack surface. A technician tool that acts as the signed-in user, through AD's own
delegation, avoids that.

## 4. MSP cloud tooling: CIPP

CIPP (CyberDrain Improved Partner Portal, [GitHub](https://github.com/KelvinTegelaar/CIPP/)) is the reference
for multi-tenant Microsoft 365 work. Its offboarding wizard ([docs](https://docs.cipp.app/user-documentation/identity/administration/offboarding-wizard.md))
offers, in order: convert to shared mailbox, hide from the GAL, cancel calendar invites, remove mailbox and
calendar permissions, remove inbox rules, wipe or remove mobile devices, remove from all groups, remove group
ownership (with a new owner), remove licenses, revoke all sessions, disable sign-in, clear immutable ID,
reset password, remove MFA devices, remove the Teams phone number, disable OneDrive sharing links and delete
the user. Separately: grant full access, Send As, Send on Behalf, OneDrive access, forwarding and an
out-of-office message. Jobs can be scheduled and send webhook, email or PSA notifications.

Its own documentation names the hybrid gap: for synced users "Clear Immutable ID" only works once the account
is no longer synchronised, and groups synced from AD cannot be changed in the cloud. CIPP does not touch
on-premises AD. That gap (the on-premises half of a hybrid leaver, done in the right order with the cloud
half) is where this tool fits.

## 5. Assessment tools (read-only posture)

| Tool | Owner | Focus |
| --- | --- | --- |
| PingCastle | Netwrix (acquired August 1, 2024) | AD health report and maturity-style risk score; Community Edition free, commercial terms to confirm ([Netwrix](https://netwrix.com/en/resources/blog/pingcastle-vs-purple-knight/)) |
| Purple Knight | Semperis | Indicators of exposure and compromise across AD, Entra ID and Okta ([Semperis](https://www.semperis.com/blog/pingcastle-or-purple-knight/)) |
| Cayosoft Guardian Protector | Cayosoft | Free continuous change monitoring and alerting ([Petri](https://petri.com/purple-knight-vs-pingcastle/)) |

These are snapshot scanners. The tool should not become one; it should surface the handful of hygiene facts a
technician needs while doing administration (section 5 of the runbook) and leave full assessments to these.

## 6. Facts that shape the design

### 6.1 Hybrid synchronization in October 2026

- Entra Connect Sync 2.x versions retire 12 months after a newer version is released. On the current table
  2.5.79.0 reaches end of support on October 23, 2026 and 2.6.92.0 (September 23, 2026) is the latest
  ([version history](https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/reference-connect-version-history)).
- Versions earlier than 2.5.79.0 stopped synchronizing on September 30, 2026
  ([MC1237727](https://mc.merill.net/message/MC1237727)).
- Mandatory next step: "Upgrade Microsoft Entra Connect Sync to version 2.6.84.0 or later and configure
  application-based authentication by April 7, 2027. ... synchronization services will stop working after this
  date if these requirements aren't met" (version history page). Since 2.6.84.0, existing servers are no
  longer switched to application-based authentication automatically; the wizard must be run.
- Cloud Sync is Microsoft's strategic direction. It alone offers group provisioning to AD, disconnected
  forests, multiple active agents and on-demand provisioning; Connect Sync alone offers advanced sync rules,
  cross-forest references, attribute merging and reconciliation. Cloud Sync is limited to 150,000 objects per
  domain and 50,000-member groups. Neither provisions users from the cloud to AD
  ([decision guide](https://learn.microsoft.com/en-us/entra/identity/hybrid/cloud-sync/connect-to-cloud-sync-decision-guide)).
- Matching: soft match on userPrincipalName or primary SMTP address, hard match on sourceAnchor (Base64 of
  `mS-DS-ConsistencyGuid` or `objectGUID`) against `onPremisesImmutableId`. Since July 1, 2026 a hard match is
  blocked when the cloud account already has `onPremisesObjectIdentifier`, or holds or is eligible for a
  privileged role. Tenants can block hard and soft match with `BlockCloudObjectTakeoverThroughHardMatchEnabled`
  and `BlockSoftMatchEnabled`, which Microsoft advises keeping on except during a planned match
  ([existing tenant](https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/how-to-connect-install-existing-tenant)).
- Source of authority (SOA) can be moved from AD to the cloud per object through Graph
  (`onPremisesSyncBehavior.isCloudManaged`, [Graph reference](https://learn.microsoft.com/en-us/graph/api/resources/onpremisessyncbehavior)).
  Group SOA was previewed in 2025; user SOA is reported generally available in January 2026 with no write-back
  to AD ([dirteam](https://dirteam.com/sander/2026/01/09/whats-new-in-entra-id-in-january-2026/)). Status to
  confirm on Learn before relying on it; parts of the API are still `/beta`.

### 6.2 Cloud actions

- `POST /users/{id}/revokeSignInSessions` invalidates refresh tokens and browser session cookies, with "a small
  delay of a few minutes"; least privilege is `User.RevokeSessions.All`; available in US Government L4 and L5
  ([Graph](https://learn.microsoft.com/en-us/graph/api/user-revokesigninsessions?view=graph-rest-1.0)).
- For synced users, sign-in state and most attributes are mastered in AD: disabling must happen on-premises and
  reach the cloud through a sync cycle (assessment, consistent with CIPP's note in section 4).
- Converting a mailbox to shared and removing mailbox permissions are Exchange Online operations
  (`Set-Mailbox -Type Shared`, `Remove-MailboxPermission`), not Graph, according to community reports
  ([ServiceNow community](https://www.servicenow.com/community/itsm-forum/how-to-remove-the-mailbox-access-for-the-offboarding-user-via/td-p/3576540)).
  To verify before design.

### 6.3 Talking to domain controllers safely

- New AD deployments require LDAP signing by default; on Windows Server 2025 channel binding defaults to "When
  supported"; upgraded DCs keep their previous settings
  ([Learn](https://learn.microsoft.com/en-us/windows-server/identity/ad-ds/ldap-signing)).
- The Go library `go-ldap` gained a Windows SSPI GSSAPI client in 2022, but it "does not support SASL security
  layers", so it cannot sign or seal a plain LDAP connection ([PR 402](https://github.com/go-ldap/ldap/pull/402)).
  Against a DC that requires signing it can only work over LDAPS.
- The Windows LDAP client (`wldap32.dll`) signs and seals a Negotiate (Kerberos) bind when `LDAP_OPT_SIGN`
  (0x95) or `LDAP_OPT_ENCRYPT` (0x96) is set before `ldap_bind_s`
  ([ldap_set_option](https://learn.microsoft.com/en-us/windows/win32/api/winldap/nf-winldap-ldap_set_option)).
  Whether it supplies channel binding tokens over LDAPS is to verify against a DC set to "Always".

### 6.4 Security facts worth surfacing

- Windows LAPS stores `msLAPS-Password` (clear JSON when encryption is off), `msLAPS-EncryptedPassword`,
  history and DSRM variants; legacy LAPS uses `ms-Mcs-AdmPwd`. Event 10018 is a successful update, 10017 a
  failure ([LAPS reference](https://github.com/MicrosoftDocs/windowsserverdocs/blob/main/WindowsServerDocs/identity/laps/laps-technical-reference.md),
  [AWS](https://docs.amazonaws.cn/en_us/directoryservice/latest/admin-guide/ms_using_windows_laps.html)).
  Reading them is a privileged act that should be recorded.
- BadSuccessor (dMSA, Windows Server 2025) was fixed as CVE-2025-53779 in August 2025, but the dMSA migration
  link attribute itself was not protected; only Tier 0 should be able to create or change dMSAs
  ([Akamai](https://www.akamai.com/blog/security-research/badsuccessor-is-dead-analyzing-badsuccessor-patch)).

## 7. What is missing across the field

1. The on-premises and cloud halves of a hybrid leaver done as one ordered, recorded job (section 4).
2. A change record a CMMC assessor can sample: who requested, who did what, before and after, when
   (PS.L2-3.9.2, AU.L2-3.3.1, AU.L2-3.3.2). RSAT keeps none; suites keep their own logs on their own server.
3. Sync awareness where the work happens: is this object synced, by which engine, when did it last sync, is it
   in error, is the sync server on a supported version (section 6.1)?
4. Safe defaults: plan before apply, refusing Tier 0 objects, bulk caps, and never a broadly privileged service
   account.
5. Lookups that today need four tools: lockout source, effective group membership, last logon across DCs,
   "who can reset this account".

## 8. Compliance context

Active Directory is a Security Protection Asset in a CMMC assessment scope, and so is any tool that administers
it. Control references below are CMMC 2.0 with NIST SP 800-171 Rev 2 numbering, which is what 32 CFR Part 170
assesses; Rev 3 renumbers and restructures several of them (for example 3.9.2 becomes 03.09.02), so confirm the
revision before quoting a client.

| Control | Level | How the tool helps (assessment) |
| --- | --- | --- |
| AC.L1-b.1.i / AC.L2-3.1.1 Authorized access | L1, L2 | Creation from approved role templates; every account traced to a request |
| AC.L2-3.1.5 Least privilege | L2 | Role templates instead of copying a user; privileged group review |
| AC.L2-3.1.6 Non-privileged accounts | L2 | Separate admin accounts are visible and checked |
| AC.L2-3.1.7 Privileged functions | L2 | Privileged actions recorded with the acting user |
| AC.L2-3.1.8 Unsuccessful logon attempts | L2 | Lockout investigation |
| IA.L2-3.5.3 Multifactor authentication | L2 | Cloud sign-in for the tool itself through Entra, so the tenant's MFA and Conditional Access apply |
| IA.L2-3.5.6 Disable identifiers after inactivity | L2 | Stale account report with the defined inactivity period |
| IA.L2-3.5.9 Temporary passwords | L2 | New and reset passwords require change at next logon |
| AU.L2-3.3.1, AU.L2-3.3.2 Audit records, user accountability | L2 | Local change record with requester, actor, before and after |
| CM.L2-3.4.3 Track and review changes | L2 | Plans and change records are reviewable and exportable |
| PS.L2-3.9.2 Personnel actions | L2 | The offboarding job: a defined process [a], consistent execution [b], protection after transfer [c] ([NDISAC](https://ndisac.org/dibscc/cyberassist/cybersecurity-maturity-model-certification/level-2/ps-l2-3-9-2/)); no fixed time limit is set by the control, the organization defines it |
| SC.L2-3.13.8 Transmission confidentiality | L2 | Signed and sealed LDAP, or LDAPS; never a simple bind on 389 |
