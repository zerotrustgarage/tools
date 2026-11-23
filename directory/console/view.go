package console

import (
	"strconv"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

// Row is one search result.
type Row struct {
	DN         string `json:"dn"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // user, group, computer, contact, ou, container, other
	Sub        string `json:"sub"`  // sign-in name, description or host name
	Title      string `json:"title,omitempty"`
	Department string `json:"department,omitempty"`
	Location   string `json:"location"` // parent OU path, readable
	Enabled    *bool  `json:"enabled,omitempty"`
	Locked     bool   `json:"locked,omitempty"`
	Privileged bool   `json:"privileged,omitempty"`
	LastLogon  string `json:"lastLogon,omitempty"` // RFC 3339 or empty
	Created    string `json:"created,omitempty"`
	// RestoreTo is the DN a deleted object is restored to (Recycle Bin rows).
	RestoreTo string `json:"restoreTo,omitempty"`
}

// Field is one attribute on the review page.
type Field struct {
	Attribute string   `json:"attribute"`
	Label     string   `json:"label"`
	Values    []string `json:"values"`
	Display   []string `json:"display"` // formatted for reading (dates, names)
	Kind      string   `json:"kind"`    // text, multi, dn, date, filetime, readonly
	Editable  bool     `json:"editable"`
	Synced    bool     `json:"synced"` // flows to Microsoft 365 for synced objects
	Help      string   `json:"help,omitempty"`
}

// Section groups fields on the review page.
type Section struct {
	Title  string  `json:"title"`
	Fields []Field `json:"fields"`
}

// Ref is a linked object (group, member, manager, report).
type Ref struct {
	DN   string `json:"dn"`
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
	Via  string `json:"via,omitempty"` // "direct", "nested" or "primary"
}

// Event is one line of the lifecycle timeline (runbook R58).
type Event struct {
	Time  string `json:"time"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // directory or record
}

// Check is one Microsoft 365 readiness check from the AD side.
type Check struct {
	Label  string `json:"label"`
	Status string `json:"status"` // ok, warn, info
	Detail string `json:"detail"`
}

// Badge is a status label in the header.
type Badge struct {
	Label string `json:"label"`
	Tone  string `json:"tone"` // ok, warn, crit, info, special, dim
}

// ObjectView is the review page for one object.
type ObjectView struct {
	DN            string    `json:"dn"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	Subtitle      string    `json:"subtitle"`
	Location      string    `json:"location"`
	Badges        []Badge   `json:"badges"`
	Enabled       *bool     `json:"enabled,omitempty"`
	Locked        bool      `json:"locked"`
	Sections      []Section `json:"sections"`
	Manager       *Ref      `json:"manager,omitempty"`
	Reports       []Ref     `json:"reports,omitempty"`
	MemberOf      []Ref     `json:"memberOf,omitempty"`
	Members       []Ref     `json:"members,omitempty"`
	Timeline      []Event   `json:"timeline"`
	Readiness     []Check   `json:"readiness,omitempty"`
	Attributes    []Field   `json:"attributes"` // every returned attribute, decoded (Advanced)
	CanRename     bool      `json:"canRename"`
	CanMembers    bool      `json:"canMembers"`
	CanEnable     bool      `json:"canEnable"`
	CanUnlock     bool      `json:"canUnlock"`
	CanExpire     bool      `json:"canExpire"`
	Privileged    string    `json:"privileged,omitempty"`
	CloudNote     string    `json:"cloudNote"` // whether Microsoft 365 is connected; the pane itself comes from Mirror
	RightsWarning string    `json:"rightsWarning,omitempty"`
}

// syncedUserAttributes flow to Microsoft Entra ID with Entra Connect's default
// rules for users in the sync scope (assessment; confirmed per tenant in D3).
var syncedUserAttributes = map[string]bool{"displayname": true, "givenname": true, "sn": true, "initials": true, "mail": true,
	"proxyaddresses": true, "userprincipalname": true, "telephonenumber": true, "mobile": true, "facsimiletelephonenumber": true,
	"ipphone": true, "title": true, "department": true, "company": true, "physicaldeliveryofficename": true, "streetaddress": true,
	"l": true, "st": true, "postalcode": true, "c": true, "co": true, "manager": true, "employeeid": true, "description": true,
	"wwwhomepage": true, "msexchusagelocation": true, "extensionattribute1": true, "extensionattribute2": true, "extensionattribute3": true,
	"extensionattribute4": true, "extensionattribute5": true, "extensionattribute6": true, "extensionattribute7": true,
	"extensionattribute8": true, "extensionattribute9": true, "extensionattribute10": true, "extensionattribute11": true,
	"extensionattribute12": true, "extensionattribute13": true, "extensionattribute14": true, "extensionattribute15": true,
	"employeetype": true, "useraccountcontrol": true, "member": true}

type fieldDef struct{ attr, label, kind, help string }

var userSections = []struct {
	title  string
	fields []fieldDef
}{
	{"Identity", []fieldDef{{"displayName", "Display name", "text", ""}, {"givenName", "First name", "text", ""}, {"initials", "Initials", "text", ""},
		{"sn", "Last name", "text", ""}, {"description", "Description", "text", ""}, {"employeeID", "Employee ID", "text", ""},
		{"employeeNumber", "Employee number", "text", ""}, {"employeeType", "Employee type", "text", ""}}},
	{"Sign-in", []fieldDef{{"userPrincipalName", "Sign-in name (UPN)", "text", "Also the Microsoft 365 sign-in name for synced users"},
		{"sAMAccountName", "Account name (pre-Windows 2000)", "readonly", ""}}},
	{"Contact", []fieldDef{{"mail", "Email", "text", ""}, {"telephoneNumber", "Phone", "text", ""}, {"mobile", "Mobile", "text", ""},
		{"ipPhone", "IP phone", "text", ""}, {"facsimileTelephoneNumber", "Fax", "text", ""}, {"wWWHomePage", "Web page", "text", ""}}},
	{"Organization", []fieldDef{{"title", "Job title", "text", ""}, {"department", "Department", "text", ""}, {"company", "Company", "text", ""},
		{"physicalDeliveryOfficeName", "Office", "text", ""}, {"manager", "Manager", "dn", ""}}},
	{"Address", []fieldDef{{"streetAddress", "Street", "text", ""}, {"l", "City", "text", ""}, {"st", "State or province", "text", ""},
		{"postalCode", "Postal code", "text", ""}, {"co", "Country", "text", ""}}},
	{"Account", []fieldDef{{"accountExpires", "Account expires", "filetime", ""}, {"pwdLastSet", "Password last set", "filetime", ""},
		{"msDS-UserPasswordExpiryTimeComputed", "Password expires", "filetime", ""}, {"lastLogonTimestamp", "Last logon (replicated, up to 14 days behind)", "filetime", ""},
		{"lastLogon", "Last logon on this DC", "filetime", ""}, {"badPwdCount", "Bad password count on this DC", "readonly", ""},
		{"logonCount", "Logons on this DC", "readonly", ""}, {"whenCreated", "Created", "date", ""}, {"whenChanged", "Changed", "date", ""}}},
	{"Profile", []fieldDef{{"scriptPath", "Logon script", "text", ""}, {"profilePath", "Profile path", "text", ""},
		{"homeDrive", "Home drive", "text", ""}, {"homeDirectory", "Home folder", "text", ""}}},
	{"Microsoft 365", []fieldDef{{"proxyAddresses", "Email addresses (proxyAddresses)", "multi", "SMTP: in capitals is the primary address"},
		{"msExchUsageLocation", "Usage location", "text", "Needed before a Microsoft 365 license can be assigned"}}},
}

var groupSections = []struct {
	title  string
	fields []fieldDef
}{
	{"Group", []fieldDef{{"description", "Description", "text", ""}, {"mail", "Email", "text", ""}, {"managedBy", "Owner (managed by)", "dn", ""},
		{"info", "Notes", "text", ""}, {"sAMAccountName", "Group name (pre-Windows 2000)", "readonly", ""}, {"groupType", "Type and scope", "readonly", ""},
		{"whenCreated", "Created", "date", ""}, {"whenChanged", "Changed", "date", ""}}},
}

var computerSections = []struct {
	title  string
	fields []fieldDef
}{
	{"Computer", []fieldDef{{"description", "Description", "text", ""}, {"dNSHostName", "DNS name", "readonly", ""},
		{"operatingSystem", "Operating system", "readonly", ""}, {"operatingSystemVersion", "Version", "readonly", ""},
		{"managedBy", "Managed by", "dn", ""}, {"lastLogonTimestamp", "Last logon (replicated)", "filetime", ""},
		{"pwdLastSet", "Machine password set", "filetime", ""}, {"whenCreated", "Created", "date", ""}}},
}

var contactSections = []struct {
	title  string
	fields []fieldDef
}{
	{"Contact", []fieldDef{{"displayName", "Display name", "text", ""}, {"givenName", "First name", "text", ""}, {"sn", "Last name", "text", ""},
		{"mail", "Email", "text", ""}, {"company", "Company", "text", ""}, {"telephoneNumber", "Phone", "text", ""},
		{"description", "Description", "text", ""}, {"whenCreated", "Created", "date", ""}}},
}

var ouSections = []struct {
	title  string
	fields []fieldDef
}{
	{"Container", []fieldDef{{"description", "Description", "text", ""}, {"managedBy", "Managed by", "dn", ""},
		{"whenCreated", "Created", "date", ""}, {"whenChanged", "Changed", "date", ""}}},
}

// KindOf classifies an object from its objectClass values.
func KindOf(o ad.Object) string {
	cls := map[string]bool{}
	for _, c := range o.Values("objectclass") {
		cls[strings.ToLower(c)] = true
	}
	switch {
	case cls["computer"]:
		return "computer"
	case cls["user"] || cls["inetorgperson"]:
		return "user"
	case cls["group"]:
		return "group"
	case cls["contact"]:
		return "contact"
	case cls["organizationalunit"]:
		return "ou"
	case cls["container"] || cls["builtindomain"] || cls["domaindns"] || cls["domain"]:
		return "container"
	}
	return "other"
}

// Name returns the best display name for an object.
func Name(o ad.Object) string {
	for _, a := range []string{"displayname", "cn", "name", "ou"} {
		if v := o.First(a); v != "" {
			return v
		}
	}
	return rdnValue(o.DN)
}

func rdnValue(dn string) string {
	r := ad.RDN(dn)
	if i := strings.IndexByte(r, '='); i >= 0 {
		r = r[i+1:]
	}
	return strings.ReplaceAll(r, `\`, "")
}

// Location renders the parent path of dn as "Contoso Labs / Staff / Finance".
func Location(dn, base string) string {
	parent := ad.ParentDN(dn)
	var parts []string
	for parent != "" && !strings.EqualFold(parent, base) {
		parts = append([]string{rdnValue(parent)}, parts...)
		parent = ad.ParentDN(parent)
		if strings.HasPrefix(strings.ToUpper(ad.RDN(parent)), "DC=") {
			break
		}
	}
	if len(parts) == 0 {
		return "(domain root)"
	}
	return strings.Join(parts, " / ")
}

func uac(o ad.Object) (int64, bool) {
	v, err := strconv.ParseInt(o.First("useraccountcontrol"), 10, 64)
	return v, err == nil
}

func enabledPtr(o ad.Object) *bool {
	v, ok := uac(o)
	if !ok {
		return nil
	}
	e := v&ad.UACAccountDisable == 0
	return &e
}

func locked(o ad.Object) bool {
	if v, err := strconv.ParseInt(o.First("msds-user-account-control-computed"), 10, 64); err == nil {
		return v&ad.UACLockout != 0
	}
	n, _ := strconv.ParseInt(o.First("lockouttime"), 10, 64)
	return n > 0
}

func timeOf(attr string, o ad.Object) time.Time {
	v := o.First(attr)
	if v == "" {
		return time.Time{}
	}
	switch strings.ToLower(attr) {
	case "whencreated", "whenchanged":
		return ad.GeneralizedTime(v)
	}
	return ad.FileTime(v)
}

func iso(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ToRow converts a search result.
func ToRow(o ad.Object, base string) Row {
	r := Row{DN: o.DN, Name: Name(o), Kind: KindOf(o), Location: Location(o.DN, base), Title: o.First("title"), Department: o.First("department")}
	switch r.Kind {
	case "user":
		r.Sub = firstNonEmpty(o.First("userprincipalname"), o.First("samaccountname"))
		r.Enabled, r.Locked = enabledPtr(o), locked(o)
		r.Privileged = o.First("admincount") == "1"
	case "computer":
		r.Sub = firstNonEmpty(o.First("dnshostname"), o.First("operatingsystem"))
		r.Enabled = enabledPtr(o)
	case "contact":
		r.Sub = o.First("mail")
	default:
		r.Sub = o.First("description")
	}
	r.LastLogon = iso(timeOf("lastlogontimestamp", o))
	r.Created = iso(timeOf("whencreated", o))
	return r
}

func groupTypeText(v string) string {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return v
	}
	kind := "Distribution"
	if n&0x80000000 != 0 {
		kind = "Security"
	}
	scope := "Unknown scope"
	switch {
	case n&0x2 != 0:
		scope = "Global"
	case n&0x4 != 0:
		scope = "Domain local"
	case n&0x8 != 0:
		scope = "Universal"
	}
	if n&0x1 != 0 {
		scope = "Built-in"
	}
	return kind + ", " + scope
}

func formatValue(kind, attr string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		switch {
		case kind == "filetime":
			if v == "0" && strings.EqualFold(attr, "pwdlastset") {
				out = append(out, "Must change at next logon")
				continue
			}
			if v == "0" || v == "9223372036854775807" {
				out = append(out, "Never")
				continue
			}
			if t := ad.FileTime(v); !t.IsZero() {
				out = append(out, t.UTC().Format("2006-01-02 15:04 UTC"))
				continue
			}
		case kind == "date":
			if t := ad.GeneralizedTime(v); !t.IsZero() {
				out = append(out, t.UTC().Format("2006-01-02 15:04 UTC"))
				continue
			}
		case kind == "dn":
			out = append(out, rdnValue(v))
			continue
		case strings.EqualFold(attr, "grouptype"):
			out = append(out, groupTypeText(v))
			continue
		}
		out = append(out, v)
	}
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
