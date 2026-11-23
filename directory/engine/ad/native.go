package ad

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Portable parts of the Windows client, kept here so they are unit tested on
// every platform.

// safeAttrs are the attributes Get returns for the object review page. They
// are deliberately allowlisted: password, LAPS, BitLocker recovery and key
// attributes are never requested (see secretAttribute). member and
// directReports use AD's ranged retrieval, like every multivalued string.
var safeAttrs = []string{
	"objectClass", "cn", "name", "distinguishedName", "objectCategory", "displayName", "givenName", "initials",
	"sn", "description", "sAMAccountName", "userPrincipalName", "mail", "proxyAddresses",
	"telephoneNumber", "mobile", "ipPhone", "facsimileTelephoneNumber", "wWWHomePage", "info",
	"title", "department", "company", "physicalDeliveryOfficeName", "streetAddress", "l", "st",
	"postalCode", "c", "co", "employeeID", "employeeNumber", "employeeType", "manager", "directReports",
	"memberOf", "member", "managedBy", "primaryGroupID", "groupType", "sAMAccountType", "adminCount",
	"userAccountControl", "accountExpires", "pwdLastSet", "lockoutTime", "badPwdCount", "badPasswordTime",
	"logonCount", "whenCreated", "whenChanged", "lastLogon", "lastLogonTimestamp",
	"homeDirectory", "homeDrive", "scriptPath", "profilePath", "msExchUsageLocation",
	"extensionAttribute1", "extensionAttribute2", "extensionAttribute3", "extensionAttribute4",
	"extensionAttribute5", "extensionAttribute6", "extensionAttribute7", "extensionAttribute8",
	"extensionAttribute9", "extensionAttribute10", "extensionAttribute11", "extensionAttribute12",
	"extensionAttribute13", "extensionAttribute14", "extensionAttribute15",
	"operatingSystem", "operatingSystemVersion", "dNSHostName", "lastKnownParent",
}

// computedAttrs are constructed attributes AD computes only for a base-scope
// read, so Get requests them and Search does not.
var computedAttrs = []string{"msDS-User-Account-Control-Computed", "msDS-UserPasswordExpiryTimeComputed"}

// searchAttrs are safeAttrs without the forward and back links that can hold
// thousands of values (member, directReports). A subtree search reads them
// for every result, and one large group would fail the whole search at the
// value limit; Get reads them for one object.
var searchAttrs = withoutAttrs(safeAttrs, "member", "directReports")

// deletedAttrs are read from the Deleted Objects container.
var deletedAttrs = append(append([]string(nil), searchAttrs...), "isDeleted", "msDS-LastKnownRDN")

// rightsAttrs are AD's constructed effective-rights attributes (research 5).
var rightsAttrs = []string{"allowedAttributesEffective", "allowedChildClassesEffective"}

func withoutAttrs(list []string, drop ...string) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		keep := true
		for _, d := range drop {
			if strings.EqualFold(a, d) {
				keep = false
			}
		}
		if keep {
			out = append(out, a)
		}
	}
	return out
}

// secretAttribute reports attributes that must never be requested: password
// hashes and history, LAPS passwords and BitLocker recovery data.
func secretAttribute(name string) bool {
	n := lowerASCII(name)
	switch n {
	case "unicodepwd", "userpassword", "ms-mcs-admpwd", "supplementalcredentials", "dbcspwd",
		"ntpwdhistory", "lmpwdhistory", "unixuserpassword", "msds-managedpassword":
		return true
	}
	return strings.HasPrefix(n, "mslaps-") || strings.HasPrefix(n, "msfve-")
}

// LDAP search scopes (RFC 4511 section 4.5.1.2).
const (
	scopeBaseObject   = 0
	scopeSingleLevel  = 1
	scopeWholeSubtree = 2
)

// searchTarget resolves the search base and scope for req against the
// configured base: Under must lie within it. The base is the DN as the
// caller gave it.
func searchTarget(req SearchRequest, base string) (string, int, error) {
	under := strings.TrimSpace(req.Under)
	if under == "" {
		// As in Matches, OneLevel applies only with Under.
		return base, scopeWholeSubtree, nil
	}
	if !UnderDN(under, base) {
		return "", 0, fmt.Errorf("%q is outside the configured search base %q", under, base)
	}
	if req.OneLevel {
		return under, scopeSingleLevel, nil
	}
	return under, scopeWholeSubtree, nil
}

// deletedFilter is BuildFilter's filter for req combined with isDeleted=TRUE.
// Under and OneLevel do not apply to the Deleted Objects container, as in the
// fake.
func deletedFilter(req SearchRequest) (string, error) {
	req.Under, req.OneLevel = "", false
	f, err := BuildFilter(req)
	if err != nil {
		return "", err
	}
	if f == "(objectClass=*)" {
		return "(isDeleted=TRUE)", nil
	}
	return "(&(isDeleted=TRUE)" + f + ")", nil
}

// deletedObjectsDN names the Deleted Objects container of a naming context
// (MS-ADTS 6.1.1.4.2).
func deletedObjectsDN(namingContext string) string {
	return "CN=Deleted Objects," + namingContext
}

// trimDCName removes the leading backslashes DsGetDcNameW puts before a DC
// name ("\\dc1.example.com").
func trimDCName(name string) string { return strings.TrimLeft(strings.TrimSpace(name), `\`) }

// guidText formats an objectGUID value as the standard GUID string, whose
// first three fields are stored little-endian (MS-DTYP 2.3.4).
func guidText(g []byte) string {
	if len(g) != 16 {
		return "invalid"
	}
	return fmt.Sprintf("%08x-%04x-%04x-%x-%x", binary.LittleEndian.Uint32(g[0:4]),
		binary.LittleEndian.Uint16(g[4:6]), binary.LittleEndian.Uint16(g[6:8]), g[8:10], g[10:16])
}

func lowerAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = lowerASCII(v)
	}
	return out
}

// kerberosPackage reports whether the package that Negotiate selected is
// Kerberos (QueryContextAttributes SECPKG_ATTR_NEGOTIATION_INFO).
func kerberosPackage(name string) bool { return strings.EqualFold(strings.TrimSpace(name), "Kerberos") }
