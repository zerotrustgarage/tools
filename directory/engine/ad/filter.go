package ad

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Class narrows a search to one kind of object.
type Class string

const (
	ClassAny       Class = ""
	ClassUser      Class = "user"
	ClassGroup     Class = "group"
	ClassComputer  Class = "computer"
	ClassContact   Class = "contact"
	ClassContainer Class = "container" // organizational units and containers
)

// QuickFilter is a named, engine-built filter (runbook R47). Callers never
// pass raw LDAP filter text; every filter is composed here from fixed parts
// and escaped values.
type QuickFilter string

const (
	FilterEnabled              QuickFilter = "enabled"
	FilterDisabled             QuickFilter = "disabled"
	FilterLocked               QuickFilter = "locked"
	FilterNeverLoggedOn        QuickFilter = "never-logged-on"
	FilterPasswordNeverExpires QuickFilter = "password-never-expires"
	FilterMustChangePassword   QuickFilter = "must-change-password"
	FilterNoManager            QuickFilter = "no-manager"
	FilterEmptyGroup           QuickFilter = "empty-group"
)

// QuickFilters lists every supported quick filter, in display order.
var QuickFilters = []QuickFilter{FilterEnabled, FilterDisabled, FilterLocked, FilterNeverLoggedOn,
	FilterPasswordNeverExpires, FilterMustChangePassword, FilterNoManager, FilterEmptyGroup}

const bitAnd = "1.2.840.113556.1.4.803"

// BuildFilter composes the LDAP filter for req. It is portable so it can be
// tested on any platform; the Windows client sends exactly this text.
func BuildFilter(req SearchRequest) (string, error) {
	var parts []string
	switch req.Class {
	case ClassAny:
	case ClassUser:
		parts = append(parts, "(objectCategory=person)(objectClass=user)")
	case ClassGroup:
		parts = append(parts, "(objectClass=group)")
	case ClassComputer:
		parts = append(parts, "(objectClass=computer)")
	case ClassContact:
		parts = append(parts, "(objectCategory=person)(objectClass=contact)")
	case ClassContainer:
		parts = append(parts, "(|(objectClass=organizationalUnit)(objectClass=container)(objectClass=builtinDomain))")
	default:
		return "", fmt.Errorf("unknown object class %q", req.Class)
	}
	if q := strings.TrimSpace(req.Query); q != "" {
		if len(q) > 256 {
			return "", fmt.Errorf("search text must be 256 characters or fewer")
		}
		e := EscapeFilterValue(q)
		parts = append(parts, "(|(anr="+e+")(cn=*"+e+"*)(displayName=*"+e+"*)(sAMAccountName=*"+e+"*)(userPrincipalName=*"+e+"*)(mail=*"+e+"*)(proxyAddresses=*"+e+"*)(telephoneNumber=*"+e+"*)(mobile=*"+e+"*)(employeeID=*"+e+"*)(title=*"+e+"*)(department=*"+e+"*))")
	}
	seen := map[QuickFilter]bool{}
	for _, f := range req.Filters {
		if seen[f] {
			continue
		}
		seen[f] = true
		switch f {
		case FilterEnabled:
			parts = append(parts, "(!(userAccountControl:"+bitAnd+":="+strconv.Itoa(UACAccountDisable)+"))")
		case FilterDisabled:
			parts = append(parts, "(userAccountControl:"+bitAnd+":="+strconv.Itoa(UACAccountDisable)+")")
		case FilterLocked:
			// lockoutTime stays set after the lockout duration ends until the
			// next logon; the object page shows the computed state.
			parts = append(parts, "(lockoutTime>=1)")
		case FilterNeverLoggedOn:
			parts = append(parts, "(!(lastLogonTimestamp=*))")
		case FilterPasswordNeverExpires:
			parts = append(parts, "(userAccountControl:"+bitAnd+":="+strconv.Itoa(UACDontExpirePassword)+")")
		case FilterMustChangePassword:
			parts = append(parts, "(pwdLastSet=0)")
		case FilterNoManager:
			parts = append(parts, "(!(manager=*))")
		case FilterEmptyGroup:
			parts = append(parts, "(objectClass=group)(!(member=*))")
		default:
			return "", fmt.Errorf("unknown quick filter %q", f)
		}
	}
	if !req.CreatedAfter.IsZero() {
		parts = append(parts, "(whenCreated>="+generalizedTime(req.CreatedAfter)+")")
	}
	if !req.InactiveSince.IsZero() {
		parts = append(parts, "(|(!(lastLogonTimestamp=*))(lastLogonTimestamp<="+strconv.FormatInt(toFileTime(req.InactiveSince), 10)+"))")
	}
	if len(parts) == 0 {
		return "(objectClass=*)", nil
	}
	if len(parts) == 1 && balancedSingle(parts[0]) {
		return parts[0], nil
	}
	return "(&" + strings.Join(parts, "") + ")", nil
}

// balancedSingle reports whether s is exactly one parenthesized filter.
func balancedSingle(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i += 2
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && i != len(s)-1 {
				return false
			}
		}
	}
	return depth == 0
}

func generalizedTime(t time.Time) string { return t.UTC().Format("20060102150405.0Z") }

func toFileTime(t time.Time) int64 {
	return (t.Unix()+11_644_473_600)*10_000_000 + int64(t.Nanosecond()/100)
}

// FileTime converts an AD FILETIME value (100 ns ticks since 1601) to time.
// Zero, negative and "never" (max int64) values return the zero time.
func FileTime(value string) time.Time {
	if value == "9223372036854775807" {
		return time.Time{}
	}
	return fileTime(value)
}

// ToFileTime converts t to an AD FILETIME string.
func ToFileTime(t time.Time) string { return strconv.FormatInt(toFileTime(t), 10) }

// GeneralizedTime parses AD's whenCreated / whenChanged syntax.
func GeneralizedTime(v string) time.Time {
	for _, layout := range []string{"20060102150405.0Z", "20060102150405Z", "20060102150405.0Z0700"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Matches is the portable equivalent of BuildFilter for the fake directory and
// for tests. It follows the same rules on lower-cased attribute maps.
func Matches(req SearchRequest, o Object) bool {
	classes := map[string]bool{}
	for _, c := range o.Values("objectclass") {
		classes[lowerASCII(c)] = true
	}
	switch req.Class {
	case ClassUser:
		if !classes["user"] || classes["computer"] {
			return false
		}
	case ClassGroup:
		if !classes["group"] {
			return false
		}
	case ClassComputer:
		if !classes["computer"] {
			return false
		}
	case ClassContact:
		if !classes["contact"] {
			return false
		}
	case ClassContainer:
		if !classes["organizationalunit"] && !classes["container"] && !classes["builtindomain"] {
			return false
		}
	}
	if q := lowerASCII(strings.TrimSpace(req.Query)); q != "" {
		found := false
		for _, a := range []string{"anr", "cn", "displayname", "samaccountname", "userprincipalname", "mail", "proxyaddresses", "telephonenumber", "mobile", "employeeid", "title", "department", "givenname", "sn"} {
			for _, v := range o.Values(a) {
				if strings.Contains(strings.ToLower(v), q) {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	uac, _ := strconv.ParseInt(o.First("useraccountcontrol"), 10, 64)
	for _, f := range req.Filters {
		switch f {
		case FilterEnabled:
			if uac&UACAccountDisable != 0 {
				return false
			}
		case FilterDisabled:
			if uac&UACAccountDisable == 0 {
				return false
			}
		case FilterLocked:
			if n, _ := strconv.ParseInt(o.First("lockouttime"), 10, 64); n < 1 {
				return false
			}
		case FilterNeverLoggedOn:
			if o.First("lastlogontimestamp") != "" {
				return false
			}
		case FilterPasswordNeverExpires:
			if uac&UACDontExpirePassword == 0 {
				return false
			}
		case FilterMustChangePassword:
			if o.First("pwdlastset") != "0" {
				return false
			}
		case FilterNoManager:
			if o.First("manager") != "" {
				return false
			}
		case FilterEmptyGroup:
			if !classes["group"] || len(o.Values("member")) > 0 {
				return false
			}
		}
	}
	if !req.CreatedAfter.IsZero() {
		if t := GeneralizedTime(o.First("whencreated")); t.IsZero() || t.Before(req.CreatedAfter) {
			return false
		}
	}
	if !req.InactiveSince.IsZero() {
		if v := o.First("lastlogontimestamp"); v != "" && FileTime(v).After(req.InactiveSince) {
			return false
		}
	}
	if req.Under != "" {
		if req.OneLevel {
			if !strings.EqualFold(ParentDN(o.DN), req.Under) {
				return false
			}
		} else if !UnderDN(o.DN, req.Under) {
			return false
		}
	}
	return true
}
