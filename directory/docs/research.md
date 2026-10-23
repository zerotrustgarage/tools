# ZTG Directory: research

Status (October 22, 2026): research for the design in [design.md](design.md) and the workflows in
[runbook.md](runbook.md). Facts are cited; statements marked "assessment" are ZTG's reading and should be
rechecked against the product before they are repeated to a client. Vendor pages are marketing and are marked
as such.

## 1. The job

At a hybrid customer (on-premises Active Directory synchronized to Microsoft 365), every joiner, mover and
leaver touches two directories and two consoles:

| Job | Where the work happens today | What goes wrong (assessment) |
| --- | --- | --- |
| New starter | ADUC: new user, pick the OU, invent the UPN and sAMAccountName, set attributes, add groups; then wait for or force a sync; then the Microsoft 365 admin center for the license and mailbox | Wrong OU (outside the sync scope), missing `mail` or `proxyAddresses`, license assigned before the object syncs, groups copied from an old user |
| Change of role | ADUC Member Of tab, attributes, move | Old access kept; nothing records why |
| Leaver | Disable in ADUC, then in the cloud: sessions, mailbox to shared, access for the manager, licenses, devices | Steps forgotten or done in the wrong order; a mailbox lost because the user was moved out of the sync scope first; no record an assessor can sample |
| "Has it synced yet?" | Remote desktop to the Entra Connect server, `Start-ADSyncSyncCycle`, refresh the admin center | Slow; technicians compare the AD user and the cloud user by eye |

ZTG Directory is meant to be installed at one customer, inside that customer's network, and to do both halves
from one console: the AD user and the Microsoft 365 user that mirrors it side by side, a sync button, and
workflows that run the joiner and leaver steps in the right order.

## 2. Tools in use today

### 2.1 Native and free

| Tool | Strengths | Gaps (assessment unless cited) |
| --- | --- | --- |
| ADUC / ADAC (RSAT) | Complete object editing; ADAC adds the AD Recycle Bin and PowerShell history | No workflows, templates beyond "Copy", bulk operations or change record; no cloud view |
| PowerShell `ActiveDirectory` module | Scriptable everything | Every technician writes their own scripts; they drift |
| Windows Admin Center, AD extension | Basic user, group and OU tasks in a browser | Light feature set; needs a gateway |
| [AD Explorer](https://learn.microsoft.com/sysinternals/downloads/adexplorer) (Sysinternals) | Fast attribute viewer and editor; snapshots and snapshot compare | No tasks; last updated v1.52 (2022) |
| LockoutStatus.exe | Lockout state and bad password count per DC ([Conetrix](https://conetrix.com/blog/a-helpful-account-lockout-tool-from-microsoft---lockoutstatusexe)) | Does not find the source machine |
| Entra Connect `ADSync` module | `Start-ADSyncSyncCycle -PolicyType Delta`, `Get-ADSyncScheduler`; default cycle every 30 minutes ([Learn](https://learn.microsoft.com/azure/active-directory/hybrid/connect/how-to-connect-sync-feature-scheduler)) | Runs on the sync server only |
| Microsoft 365 admin center, Exchange admin center | The cloud half | A separate console with no view of the AD object |

### 2.2 Commercial on-premises suites

| Product | Notable ideas | Caveats |
| --- | --- | --- |
| ManageEngine ADManager Plus | Drag-and-drop provisioning and offboarding workflows; over 200 prepackaged reports ([vendor](https://www.manageengine.com/products/ad-manager/comparison/softerra-adaxes-alternative.html)) | Vendor claims; server install |
| Softerra Adaxes | Business rules that fire on create, modify and delete; approvals; web interface for delegated staff (assessment) | Script-heavy for advanced rules |
| One Identity (Quest) Active Roles | Policy gateway in front of AD; granular delegation; workflows; configuration-heavy ([aimultiple](https://research.aimultiple.com/active-directory-management-tools)) | Enterprise scale and price |
| [Cayosoft Administrator](https://expertinsights.com/identity-and-access-management/cayosoft-administrator) | One console for AD, Entra ID, Microsoft 365, Exchange, Teams and Intune; onboarding and offboarding automation; license optimization | Microsoft targets only |
| Netwrix Directory Manager (formerly Imanami GroupID, assessment) | Approvals; groups maintained from directory data ([vendor](https://netwrix.com/directory-management-software.html)) | Vendor claims |

What to take from them (assessment): templates per role, workflows with approvals, and one console for both
directories. What to avoid: a broadly privileged service account (often Domain Admin in practice) and a web
portal exposed on the network. ZTG Directory's service runs as a group managed service account with rights
delegated only on the OUs it manages (design 6).

## 3. Hybrid synchronization in October 2026

- Entra Connect Sync 2.x versions retire 12 months after a newer version is released. On the current table
  2.5.79.0 reaches end of support on October 23, 2026 and 2.6.92.0 (September 23, 2026) is the latest
  ([version history](https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/reference-connect-version-history)).
  Versions earlier than 2.5.79.0 stopped synchronizing on September 30, 2026
  ([MC1237727](https://mc.merill.net/message/MC1237727)).
- "Upgrade Microsoft Entra Connect Sync to version 2.6.84.0 or later and configure application-based
  authentication by April 7, 2027. ... synchronization services will stop working after this date if these
  requirements aren't met" (version history page). Since 2.6.84.0, existing servers are not switched to
  application-based authentication automatically; the wizard must be run.
- Cloud Sync is Microsoft's strategic direction: group provisioning to AD, disconnected forests, multiple
  active agents and on-demand provisioning are Cloud Sync only; advanced sync rules, cross-forest references
  and reconciliation are Connect Sync only. Cloud Sync supports up to 150,000 objects per domain and
  50,000-member groups. Neither provisions users from the cloud to AD
  ([decision guide](https://learn.microsoft.com/en-us/entra/identity/hybrid/cloud-sync/connect-to-cloud-sync-decision-guide)).
- Forcing a sync: Connect Sync with `Start-ADSyncSyncCycle -PolicyType Delta` on the sync server (delta
  import, delta sync and export on all connectors); "Sync is already running" if a cycle is in progress
  (scheduler page above). Cloud Sync with Graph `provisionOnDemand` for one object; least privileged
  application permission `Application.ReadWrite.OwnedBy`, higher `Synchronization.ReadWrite.All`; rate limit
  5 requests per 10 seconds ([Graph](https://learn.microsoft.com/en-us/graph/api/synchronization-synchronizationjob-provisionondemand?view=graph-rest-1.0)).
- Finding the cloud user that mirrors an AD user: the cloud `onPremisesImmutableId` is the Base64 of the
  sourceAnchor, which is `mS-DS-ConsistencyGuid` or `objectGUID`
  ([existing tenant](https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/how-to-connect-install-existing-tenant)).
  `onPremisesSecurityIdentifier` (the AD `objectSid`) is a second key. Filtering on these properties may need
  Graph advanced query headers (`ConsistencyLevel: eventual`, `$count`) ([advanced queries](https://learn.microsoft.com/en-us/graph/aad-advanced-queries));
  to confirm per property in D2.
- Matching rules: soft match on UPN or primary SMTP address, hard match on sourceAnchor. Since July 1, 2026 a
  hard match is blocked when the cloud account already has `onPremisesObjectIdentifier`, or holds or is
  eligible for a privileged role. `BlockSoftMatchEnabled` and `BlockCloudObjectTakeoverThroughHardMatchEnabled`
  should stay on except during a planned match (existing tenant page).

## 4. Microsoft 365 from an installed application

### 4.1 App registration and Graph

- An installed, unattended application authenticates as itself: a single-tenant app registration in the
  customer's tenant with a certificate credential and application permissions granted by admin consent.
  Microsoft Entra Connect itself moved to this model ("application-based authentication", section 3).
- Least privileged application permissions for the actions in the workflows:

| Action | Graph call | Least privileged application permission |
| --- | --- | --- |
| Revoke sessions | `POST /users/{id}/revokeSignInSessions` | `User.RevokeSessions.All` ([Graph](https://learn.microsoft.com/en-us/graph/api/user-revokesigninsessions?view=graph-rest-1.0)); takes "a few minutes" |
| Add or remove licenses | `POST /users/{id}/assignLicense` | `LicenseAssignment.ReadWrite.All` ([Graph](https://learn.microsoft.com/en-us/graph/api/user-assignlicense?view=graph-rest-1.0)) |
| Provision one object now (Cloud Sync) | `provisionOnDemand` | `Application.ReadWrite.OwnedBy` or `Synchronization.ReadWrite.All` (section 3) |
| Read users, licenses, sync state | `GET /users`, `/subscribedSkus`, `/organization` | `User.Read.All`, `Organization.Read.All` (assessment, to confirm per call in D2) |
| Remove authentication methods | `/users/{id}/authentication/...` | `UserAuthenticationMethod.ReadWrite.All` (assessment, to confirm) |

  Both `revokeSignInSessions` and `assignLicense` are available in US Government L4 (GCC High) and L5 (DoD).
- Group-based licensing (license an AD group that syncs) needs Microsoft Entra ID P1, or a suite that includes
  it such as Microsoft 365 Business Premium or Office 365 E3, and enough licenses for every member of the
  group ([Entra](https://docs.azure.cn/en-us/entra/fundamentals/concept-group-based-licensing)). Without it,
  licenses are assigned directly per user. A license cannot be assigned until the user has a usage location
  (assessment; for synced users it can come from AD `msExchUsageLocation`).

### 4.2 Exchange Online

- Mailbox type and permissions are Exchange operations (`Set-Mailbox -Type Shared`, `Add-MailboxPermission`,
  `Add-RecipientPermission`), not Graph. Unattended access uses the Exchange Online PowerShell module (V3) with
  app-only authentication: the `Exchange.ManageAsApp` application permission, admin consent, a certificate on
  the app registration, and a Microsoft Entra role on the app; Exchange Recipient Administrator is the
  narrowest built-in role, and a custom role group with a write scope can limit which recipients it may change
  ([app-only auth](https://learn.microsoft.com/en-us/powershell/exchange/app-only-auth-powershell-v2)).
- From the same page: Cryptography Next Generation (CNG) certificates "aren't supported for app-only
  authentication with Exchange"; the certificate must come from a CSP key provider. `-CertificateThumbprint`
  is Windows only and reads the certificate from the user certificate store of the account running it. GCC
  High and DoD connect with `-ExchangeEnvironmentName O365USGovGCCHigh` or `O365USGovDoD`.
- Hybrid Exchange changes where mailboxes are managed: "If your organization uses a hybrid Exchange
  environment, you need to manage your mailboxes using on-premises Exchange management tools." A mailbox
  provisioned in Exchange Online but never migrated is converted with `Set-RemoteMailbox <id> -Type Shared` in
  on-premises Exchange; a migrated mailbox also needs the Exchange Online object updated
  ([convert a mailbox](https://learn.microsoft.com/en-us/exchange/recipients-in-exchange-online/manage-user-mailboxes/convert-a-mailbox)).
  When the two sides disagree, Exchange Online can revert a shared mailbox to a regular one, and an unlicensed
  regular mailbox without a hold can be disconnected
  ([troubleshooting](https://learn.microsoft.com/en-us/exchange/troubleshoot/user-and-shared-mailboxes/shared-mailboxes-unexpectedly-converted-to-user-mailboxes)).
- Customers who removed their last Exchange server can still manage recipients with the Exchange Server 2019
  CU12 (or later) management tools, which include `Enable-RemoteMailbox` and `Set-RemoteMailbox`, under
  conditions (all mailboxes in Exchange Online, Entra Connect in use, no RBAC, no recipient auditing)
  ([Practical365](https://practical365.com/how-to-use-the-exchange-online-management-only-tools-if-youve-already-removed-your-last-exchange-servers/),
  [TechTarget](https://www.techtarget.com/it-infrastructure/tip/Follow-these-steps-to-remove-the-last-Exchange-Server)).
  The tool must therefore detect which of three mailbox models a customer has: cloud-only Exchange attributes,
  hybrid with an Exchange server, or hybrid with management tools only.

## 5. Talking to domain controllers safely

- New AD deployments require LDAP signing by default; Windows Server 2025 sets channel binding to "When
  supported"; upgraded DCs keep their previous settings
  ([Learn](https://learn.microsoft.com/en-us/windows-server/identity/ad-ds/ldap-signing)).
- `go-ldap`'s Windows SSPI client "does not support SASL security layers", so it cannot sign or seal a plain
  LDAP connection ([PR 402](https://github.com/go-ldap/ldap/pull/402)).
- The Windows LDAP client (`wldap32.dll`) signs and seals a Negotiate bind when `LDAP_OPT_SIGN` or
  `LDAP_OPT_ENCRYPT` is set before `ldap_bind_s`
  ([ldap_set_option](https://learn.microsoft.com/en-us/windows/win32/api/winldap/nf-winldap-ldap_set_option)).
  This works the same whether the tool runs on a DC (binding to itself) or on a member server.
- RADIUS (for example Windows NPS) answers one question, "may this user authenticate", against AD. It cannot
  read or change directory objects, so it is not a management channel (assessment, from the protocol's scope in
  RFC 2865). It could serve as an extra factor for technicians signing in to the console (design Q6).

## 6. Security facts worth surfacing

- Windows LAPS stores `msLAPS-Password` (clear JSON when encryption is off), `msLAPS-EncryptedPassword` and
  history; legacy LAPS uses `ms-Mcs-AdmPwd`
  ([LAPS reference](https://github.com/MicrosoftDocs/windowsserverdocs/blob/main/WindowsServerDocs/identity/laps/laps-technical-reference.md)).
  Reading them is a privileged act that should be recorded.
- BadSuccessor (dMSA, Windows Server 2025) was fixed as CVE-2025-53779, but the dMSA link attribute is not
  protected; only Tier 0 should be able to create or change dMSAs
  ([Akamai](https://www.akamai.com/blog/security-research/badsuccessor-is-dead-analyzing-badsuccessor-patch)).
- Assessment tools such as PingCastle (Netwrix) and Purple Knight (Semperis) cover AD posture in depth
  ([Netwrix](https://netwrix.com/en/resources/blog/pingcastle-vs-purple-knight/)); ZTG Directory surfaces only the
  hygiene facts a technician needs while working (runbook section 7).

## 7. Compliance context

Active Directory and the Microsoft 365 tenant are Security Protection Assets in a CMMC assessment scope, and so
is any tool that administers them; the app registration's certificate is a credential the customer's SSP should
name. References are CMMC 2.0 with NIST SP 800-171 Rev 2 numbering, which is what 32 CFR Part 170 assesses; Rev 3
renumbers and restructures several (for example 3.9.2 becomes 03.09.02), so confirm the revision before quoting
a customer.

| Control | Level | How the tool helps (assessment) |
| --- | --- | --- |
| AC.L1-b.1.i / AC.L2-3.1.1 Authorized access | L1, L2 | Accounts created only from approved role templates, each traced to a request |
| AC.L2-3.1.5 Least privilege | L2 | Templates instead of copying a user; the service holds only delegated OU rights and per-feature cloud permissions |
| AC.L2-3.1.7 Privileged functions | L2 | Privileged actions recorded with the acting and approving person |
| AC.L2-3.1.8 Unsuccessful logon attempts | L2 | Lockout investigation |
| IA.L2-3.5.6 Disable identifiers after inactivity | L2 | Stale account report |
| IA.L2-3.5.9 Temporary passwords | L2 | New and reset passwords require change at next logon |
| AU.L2-3.3.1, AU.L2-3.3.2 Audit records, user accountability | L2 | Change records name the technician even when the service or the app registration performed the write |
| CM.L2-3.4.3 Track and review changes | L2 | Plans and records are reviewable and exportable |
| PS.L2-3.9.2 Personnel actions | L2 | The leaver workflow: defined process [a], consistent execution [b], protection after transfer [c] ([NDISAC](https://ndisac.org/dibscc/cyberassist/cybersecurity-maturity-model-certification/level-2/ps-l2-3-9-2/)); the control sets no fixed time limit, the organization defines it |
| SC.L2-3.13.8 Transmission confidentiality | L2 | Signed and sealed LDAP or LDAPS; TLS to Microsoft 365 |
