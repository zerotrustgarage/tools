package mirror

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/m365"
)

// How two values are compared.
type compareKind int

const (
	exact   compareKind = iota // trimmed, case-sensitive
	fold                       // addresses and codes: case-insensitive
	proxies                    // proxyAddresses as a set
	country                    // co or c against country
	cloudOK                    // case-insensitive; an empty AD value lets Microsoft 365 keep its own
)

type mapping struct {
	attr, prop, label string
	kind              compareKind
	ad                func(ad.Object) []string
	cloud             func(m365.User) []string
}

func one(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return []string{v}
}

func adAttr(name string) func(ad.Object) []string {
	return func(o ad.Object) []string { return o.Values(name) }
}

// mappings follow Entra Connect's default user attribute flow (assessment;
// confirmed per tenant in D3).
var mappings = []mapping{
	{"displayName", "displayName", "Display name", exact, adAttr("displayname"), func(u m365.User) []string { return one(u.DisplayName) }},
	{"givenName", "givenName", "First name", exact, adAttr("givenname"), func(u m365.User) []string { return one(u.GivenName) }},
	{"sn", "surname", "Last name", exact, adAttr("sn"), func(u m365.User) []string { return one(u.Surname) }},
	{"userPrincipalName", "userPrincipalName", "Sign-in name", fold, adAttr("userprincipalname"), func(u m365.User) []string { return one(u.UserPrincipalName) }},
	{"mail", "mail", "Email", fold, adAttr("mail"), func(u m365.User) []string { return one(u.Mail) }},
	{"proxyAddresses", "proxyAddresses", "Email addresses", proxies, adAttr("proxyaddresses"), func(u m365.User) []string { return u.ProxyAddresses }},
	{"telephoneNumber", "businessPhones", "Phone", exact, adAttr("telephonenumber"), func(u m365.User) []string {
		if len(u.BusinessPhones) == 0 {
			return nil
		}
		return one(u.BusinessPhones[0])
	}},
	{"mobile", "mobilePhone", "Mobile", exact, adAttr("mobile"), func(u m365.User) []string { return one(u.MobilePhone) }},
	{"title", "jobTitle", "Job title", exact, adAttr("title"), func(u m365.User) []string { return one(u.JobTitle) }},
	{"department", "department", "Department", exact, adAttr("department"), func(u m365.User) []string { return one(u.Department) }},
	{"company", "companyName", "Company", exact, adAttr("company"), func(u m365.User) []string { return one(u.CompanyName) }},
	{"physicalDeliveryOfficeName", "officeLocation", "Office", exact, adAttr("physicaldeliveryofficename"), func(u m365.User) []string { return one(u.OfficeLocation) }},
	{"streetAddress", "streetAddress", "Street", exact, adAttr("streetaddress"), func(u m365.User) []string { return one(u.StreetAddress) }},
	{"l", "city", "City", exact, adAttr("l"), func(u m365.User) []string { return one(u.City) }},
	{"st", "state", "State or province", exact, adAttr("st"), func(u m365.User) []string { return one(u.State) }},
	{"postalCode", "postalCode", "Postal code", exact, adAttr("postalcode"), func(u m365.User) []string { return one(u.PostalCode) }},
	{"co", "country", "Country", country, func(o ad.Object) []string {
		if v := o.Values("co"); len(v) > 0 {
			return v
		}
		return o.Values("c")
	}, func(u m365.User) []string { return one(u.Country) }},
	{"employeeID", "employeeId", "Employee ID", exact, adAttr("employeeid"), func(u m365.User) []string { return one(u.EmployeeID) }},
	{"msExchUsageLocation", "usageLocation", "Usage location", cloudOK, adAttr("msexchusagelocation"), func(u m365.User) []string { return one(u.UsageLocation) }},
	{"userAccountControl", "accountEnabled", "Sign-in enabled", exact, enabledText, func(u m365.User) []string {
		if u.AccountEnabled == nil {
			return nil
		}
		return []string{strconv.FormatBool(*u.AccountEnabled)}
	}},
}

// compare builds the comparison rows. managerUPN is nil when the manager's
// UPN could not be resolved.
func compare(o ad.Object, u m365.User, managerUPN *string) []Row {
	changed := ad.GeneralizedTime(o.First("whenchanged"))
	var rows []Row
	for _, m := range mappings {
		r := row(m.attr, m.prop, m.label, m.kind, m.ad(o), m.cloud(u), changed, u)
		// Country: the cloud value may come from co (the name) or c (the
		// two-letter code), depending on the sync rules.
		if m.kind == country && r.Status != StatusMatch && len(r.Cloud) == 1 && strings.EqualFold(strings.TrimSpace(r.Cloud[0]), o.First("c")) {
			r.Status, r.Note = StatusMatch, "Matches c (the country code)."
		}
		rows = append(rows, r)
	}
	for i := 1; i <= 15; i++ {
		name := fmt.Sprintf("extensionAttribute%d", i)
		adv, cv := o.Values(strings.ToLower(name)), one(u.OnPremisesExtensionAttrs[name])
		if cv == nil {
			for k, val := range u.OnPremisesExtensionAttrs {
				if strings.EqualFold(k, name) {
					cv = one(val)
				}
			}
		}
		if len(adv) == 0 && len(cv) == 0 {
			continue
		}
		rows = append(rows, row(name, "onPremisesExtensionAttributes."+name, fmt.Sprintf("Extension attribute %d", i), exact, adv, cv, changed, u))
	}
	mgr := Row{Attribute: "manager", Property: "managerUpn", Label: "Manager", Cloud: one(u.ManagerUPN)}
	if managerUPN == nil {
		mgr.AD = o.Values("manager")
		mgr.Status = StatusNotCompared
		mgr.Note = "The manager's UPN could not be read from AD, so the manager is not compared."
	} else {
		mgr = row("manager", "managerUpn", "Manager", fold, one(*managerUPN), one(u.ManagerUPN), changed, u)
	}
	return append(rows, mgr)
}

func row(attr, prop, label string, kind compareKind, adv, cloud []string, changed time.Time, u m365.User) Row {
	r := Row{Attribute: attr, Property: prop, Label: label, AD: adv, Cloud: cloud}
	equal, detail := same(kind, adv, cloud)
	if equal {
		r.Status, r.Note = StatusMatch, detail
		return r
	}
	r.Status, r.Note = status(changed, u.OnPremisesLastSyncDateTime)
	if detail != "" {
		r.Note = detail + " " + r.Note
	}
	if pe := provisioningError(u, prop); pe != "" {
		r.Note += " " + pe
	}
	return r
}

// status labels a difference: waiting for sync when AD changed at or after
// the last sync (whenChanged has one-second resolution), otherwise out of
// step.
func status(changed time.Time, lastSync *time.Time) (string, string) {
	if lastSync == nil || lastSync.IsZero() {
		return StatusOutOfStep, "Microsoft 365 has no last sync time for this user, so the difference is not explained by a pending sync."
	}
	ls := lastSync.UTC()
	if !changed.IsZero() && !changed.Before(ls.Truncate(time.Second)) {
		return StatusWaiting, "Changed in AD at " + changed.UTC().Format(time.RFC3339) + ", after the last sync at " + ls.Format(time.RFC3339) + "; the next sync cycle should carry it."
	}
	when := "an unknown time"
	if !changed.IsZero() {
		when = changed.UTC().Format(time.RFC3339)
	}
	return StatusOutOfStep, "The last sync (" + ls.Format(time.RFC3339) + ") came after the last AD change (" + when +
		") and the values still differ: check the sync rules and errors, or a change made in Microsoft 365. Correct it in AD, which masters this attribute."
}

func provisioningError(u m365.User, prop string) string {
	for _, e := range u.ProvisioningErrors {
		if strings.EqualFold(e.PropertyName, prop) {
			return "Microsoft 365 reports a provisioning error on this property (" + e.Category + ": " + e.Value + ")."
		}
	}
	return ""
}

func trimmed(v []string) []string {
	var out []string
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// same reports whether the values are equal under kind, with a detail line
// for proxyAddresses.
func same(kind compareKind, adv, cloud []string) (bool, string) {
	a, c := trimmed(adv), trimmed(cloud)
	switch kind {
	case proxies:
		return sameProxies(a, c)
	case cloudOK:
		if len(a) == 0 {
			if len(c) > 0 {
				return true, "Not set in AD; Microsoft 365 keeps its own value."
			}
			return true, ""
		}
		return equalFold(a, c), ""
	case fold, country:
		return equalFold(a, c), ""
	}
	if len(a) != len(c) {
		return false, ""
	}
	for i := range a {
		if a[i] != c[i] {
			return false, ""
		}
	}
	return true, ""
}

func equalFold(a, c []string) bool {
	if len(a) != len(c) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], c[i]) {
			return false
		}
	}
	return true
}

// normalProxy lower-cases the address and the prefix, except that SMTP in
// capitals (the primary address) stays distinct from smtp.
func normalProxy(p string) string {
	i := strings.IndexByte(p, ':')
	if i < 0 {
		return strings.ToLower(p)
	}
	prefix := p[:i]
	if prefix != "SMTP" {
		prefix = strings.ToLower(prefix)
	}
	return prefix + ":" + strings.ToLower(p[i+1:])
}

// serviceAddress reports an address Microsoft 365 adds by itself (the
// tenant's onmicrosoft routing address), which AD does not hold.
func serviceAddress(p string) bool {
	n := strings.ToLower(p)
	return strings.HasSuffix(n, ".onmicrosoft.com") || strings.HasSuffix(n, ".onmicrosoft.us")
}

func sameProxies(a, c []string) (bool, string) {
	inA, inC := map[string]string{}, map[string]string{}
	for _, p := range a {
		inA[normalProxy(p)] = p
	}
	for _, p := range c {
		inC[normalProxy(p)] = p
	}
	var onlyAD, onlyCloud, ignored []string
	for k, p := range inA {
		if _, ok := inC[k]; !ok {
			onlyAD = append(onlyAD, p)
		}
	}
	for k, p := range inC {
		if _, ok := inA[k]; !ok {
			if serviceAddress(p) {
				ignored = append(ignored, p)
				continue
			}
			onlyCloud = append(onlyCloud, p)
		}
	}
	sort.Strings(onlyAD)
	sort.Strings(onlyCloud)
	sort.Strings(ignored)
	var parts []string
	if len(onlyAD) > 0 {
		parts = append(parts, "Only in AD: "+strings.Join(onlyAD, ", ")+".")
	}
	if len(onlyCloud) > 0 {
		parts = append(parts, "Only in Microsoft 365: "+strings.Join(onlyCloud, ", ")+".")
	}
	if len(ignored) > 0 {
		parts = append(parts, "Ignored the routing addresses Microsoft 365 adds itself: "+strings.Join(ignored, ", ")+".")
	}
	return len(onlyAD) == 0 && len(onlyCloud) == 0, strings.Join(parts, " ")
}
