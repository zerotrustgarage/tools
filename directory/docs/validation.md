# D1 validation

D1 is in development. The CLI and engine expose read operations only. Unit tests and cross builds do not
establish that LDAP signing, sealing, channel binding or certificate validation works against a domain controller.
Do not proceed to D2 until the D1 lab evidence has been reviewed.

## Automated checks

Use the repository's pinned Go toolchain (`scripts/ci/install-go.sh`, currently Go 1.26.10). From `directory/`:

```sh
gofmt -l .
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=windows GOARCH=arm64 go vet ./...
go test -race ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o ztg-directory.exe ./cmd/ztg-directory
cmp internal/update/update.go ../scripts/updates/update.go
```

The path-filtered Directory workflow also runs tests on a GitHub-hosted `windows-2025` runner. Its initial run
passed both Linux and native Windows checks ([run 39964322829](https://github.com/zerotrustgarage/tools/actions/runs/39964322829)).
The Windows suite also uses ephemeral loopback listeners to exercise the native LDAP response parser and
reject an untrusted LDAPS certificate. The protocol fixture uses anonymous authentication only inside the
test and checks that no non-anonymous bind is accepted; the application always uses Negotiate.
No fixture changes the trust store or connects to a real directory. These checks are not a domain controller
lab and do not meet the live D1 exit condition. They do not establish successful sign/seal negotiation,
channel binding, or correct matching against live AD.

Regression cases cover Unicode/multivalue responses, missing objects, malformed SIDs, string safety limits
and the portable fake's search caps. Ranged attribute reads collect contiguous pages on the same connection,
using the canonical DN from the first response for every continuation. Native fixtures cover string and SID
continuations, malformed or missing pages, changed response DNs, cancellation and request caps. Values are
bounded to 4,096 strings or 8,192 SIDs per attribute and 16 pages; exceeding a cap returns `ErrIncomplete`
without a partial success result. These are read operations, not an atomic snapshot across directory changes.

## Isolated AD lab

Use a disposable domain and an ordinary delegated technician account. Run from a member Windows server, a
domain-joined Windows 11 workstation and the domain controller, using the current logon identity. No credential is accepted as a CLI flag.
Use a Windows Server 2025 DC requiring LDAP signing and record its channel-binding policy. Repeat the semantic
lookups against Samba AD from a domain-joined Windows client. The client uses Windows LDAP on both paths.

The integration test makes no changes. In a Windows PowerShell session in the lab:

```powershell
$env:ZTG_DIRECTORY_INTEGRATION = '1'
$env:ZTG_DIRECTORY_TEST_SERVER = 'dc1.lab.example'
$env:ZTG_DIRECTORY_TEST_BASE = 'DC=lab,DC=example'
$env:ZTG_DIRECTORY_TEST_DN = 'CN=Test User,OU=Test,DC=lab,DC=example'
go test -v ./integration
# Repeat with LDAPS and a certificate trusted by this client.
$env:ZTG_DIRECTORY_TEST_LDAPS = '1'
go test -v ./integration
```

Record the server/client versions, enforced policies, test account's delegated rights, commands, exit codes and
sanitized output. Compare results to ADUC or documented AD PowerShell queries for the same object:

- Search for names, email addresses and literal LDAP metacharacters. Verify bounded results report truncation.
- Compare object attributes; ensure LAPS values, passwords and recovery keys are absent.
- Use a test user with multivalued attributes exceeding the DC's `MaxValRange`. Compare the complete values
  with native tools, confirm contiguous base-scope range requests stay on the same connection, and verify
  over-limit results fail explicitly. Repeat range semantics against Samba. No live range test has run yet.
- Check nested security groups and the primary group. Test an OU search base and a group outside it; its SID
  must remain visible with `Resolved: false`. Membership is not a complete effective-access calculation.
- Compare the selected DC's `lastLogon` and replicated `lastLogonTimestamp`. The result must remain labeled partial;
  the tool does not compute the maximum over every DC.
- Inspect the LDAP session or DC security evidence to confirm signing and sealing on port 389. A successful bind
  alone is not evidence of both. Confirm a server refusing the required protection causes failure.
- With LDAPS, confirm an untrusted, expired or hostname-mismatched certificate fails. Record channel-binding behavior
  against the enforced Windows policy. Never bypass certificate validation to make the test pass.
- Test an unreachable server, a slow server, cancellation and an out-of-scope referral. There must be no credential
  forwarding to a referred server and no unbounded wait.
- Inspect the account and directory after the run to confirm no object changed.

This lab matrix has not been executed as part of the initial implementation. Keep D1 open until evidence is
available; D2 (AD writes) also needs a delegated, non-admin technician and a domain-joined Windows 11
workstation in the lab, and Q8's tenant and hybrid Exchange estate is needed before D3.

## Working assumptions

The request to begin building is treated as authorization for D1. It does not settle deployment and workflow
policy for later slices. The implementation follows the documented Go/no-cgo recommendation (Q2); live reads
use the technician's existing Windows identity. No service, installer, app registration or tenant permission is
created. Q1 and Q3 through Q9 remain decisions to confirm for their relevant slices.

## D2 lab checks (AD changes as the signed-in account)

D2 code exists and passes its portable and Windows unit tests, but none of it has run against a domain
controller. In an isolated lab, with a delegated technician who is not a domain admin, on the DC and on a
domain-joined Windows 11 workstation, confirm and record:

1. Sign-in: a correct password signs in; a wrong password fails with LDAP 49 (not a client-side code) and counts
   toward lockout; an account outside ZTG Directory Operators and Approvers is refused.
2. Kerberos only: with `kerberosOnly` on, the bind's negotiated package reads as Kerberos; connecting by IP
   address (which forces NTLM) is refused rather than silently using NTLM. Check behaviour over LDAPS too.
3. Duo: with a Duo Authentication Proxy in `radius_server_duo_only` mode (6.5.1 or later,
   `force_message_authenticator=true`, `failmode=secure`), push, phone and passcode succeed, a denied push fails,
   and a response without Message-Authenticator is rejected.
4. Each change type from the console and the CLI: create from a template, set and clear attributes, membership,
   move, rename, enable, disable, unlock, password reset (sealed connection detected, change at next logon set),
   account expiry, delete, Recycle Bin restore. Each is read back, and each appears in the change record with the
   technician, DC and device.
5. Rights: a change outside the technician's delegation is refused at plan time with the attribute named
   (`allowedAttributesEffective`), and Reset Password without the extended right fails cleanly at run time.
6. Tier 0: a plan touching a privileged account or group needs the second confirmation; the demo delegation
   pattern (no rights on the Admins OU) is refused.
7. Records: `verify-records` reports the chain intact; editing or deleting a line is detected.
8. DC locator: with no server configured the console binds to a writable DC in the site and names it.

