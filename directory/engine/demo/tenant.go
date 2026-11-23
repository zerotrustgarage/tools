package demo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/dirsync"
	"github.com/zerotrustgarage/tools/directory/engine/m365"
)

// Demo Microsoft 365 scenarios (fictional), each visible in the mirror pane.
const (
	// WaitingUser's title changed in AD after the last sync: "waiting for sync".
	WaitingUser = "Ivan Petrov"
	// OutOfStepUser's cloud title differs although the last sync came after
	// the AD change: "out of step".
	OutOfStepUser = "Elio Brandt"
	// ConflictUser has a duplicate proxyAddresses provisioning error: one of
	// its addresses is held by a cloud-only mailbox.
	ConflictUser = "Jade Mbeki"
	// PossibleMatchUser is a new starter not yet synced; a cloud-only
	// account with the same UPN exists: "possible match".
	PossibleMatchUser = "Farah Haddad"
	// CloudAccount is the demo technician's Microsoft 365 account.
	CloudAccount = "ztg.tech@contoso-labs.example"
)

// LastSync is the demo tenant's last sync cycle.
var LastSync = Now.Add(-25 * time.Minute)

// e3 is Microsoft 365 E3's documented SKU (subscribedSkus skuPartNumber SPE_E3).
var e3 = m365.License{SkuID: "05e9a617-0261-4cee-bb44-138d3ef5d965", SkuPartNumber: "SPE_E3"}

func cloudID(n int) string { return fmt.Sprintf("c10d0000-0000-4000-8000-%012d", n) }

func ptr[T any](v T) *T { return &v }

// NewTenant returns a fictional Microsoft 365 tenant mirroring dir: every
// user in the Staff and Leavers OUs is synced (immutable ID from objectGUID)
// except PossibleMatchUser; the Admins OU (the Tier 0 admin and the demo
// technician) is outside the sync scope.
func NewTenant(dir *ad.FakeClient) *m365.FakeTenant {
	ctx := context.Background()
	t := m365.NewFakeTenant(CloudAccount)
	t.Org = m365.Organization{ID: "c10d0000-0000-4000-8000-000000000000", DisplayName: "Contoso Labs", OnPremisesSyncEnabled: true,
		OnPremisesLastSyncDateTime: ptr(LastSync), VerifiedDomains: []string{"contoso-labs.example"}}
	res, _ := dir.Search(ctx, ad.SearchRequest{Class: ad.ClassUser, Limit: 5000})
	upns := map[string]string{}
	for _, o := range res.Objects {
		upns[strings.ToLower(o.DN)] = o.First("userprincipalname")
	}
	staff := "OU=Staff,OU=Contoso Labs," + Base
	leavers := "OU=Leavers,OU=Contoso Labs," + Base
	n := 0
	for _, o := range res.Objects {
		if hasClass(o, "computer") || !(ad.UnderDN(o.DN, staff) || ad.UnderDN(o.DN, leavers)) {
			continue
		}
		name := o.First("displayname")
		if name == PossibleMatchUser {
			continue
		}
		n++
		id, err := ad.ImmutableID(o.First("objectguid"))
		if err != nil {
			continue
		}
		created := ad.GeneralizedTime(o.First("whencreated")).Add(40 * time.Minute)
		u := m365.User{ID: cloudID(n), OnPremisesImmutableID: id, OnPremisesSyncEnabled: ptr(true), CreatedDateTime: &created,
			OnPremisesLastSyncDateTime: ptr(LastSync)}
		copyAttributes(o, upns[strings.ToLower(o.First("manager"))], &u)
		if t := ad.FileTime(o.First("lastlogontimestamp")); !t.IsZero() {
			u.LastSuccessfulSignIn = ptr(t.Add(-2 * time.Hour))
		}
		licensed := false
		var groups []m365.Group
		for _, g := range o.Values("memberof") {
			cn := strings.TrimPrefix(strings.SplitN(g, ",", 2)[0], "CN=")
			if cn == "M365 E3 Licensing" {
				licensed = true
			}
			groups = append(groups, m365.Group{ID: "g-" + strings.ToLower(strings.ReplaceAll(cn, " ", "-")), DisplayName: cn,
				SecurityEnabled: cn != "All Staff", MailEnabled: cn == "All Staff", OnPremisesSyncEnabled: true, Kind: "synced"})
		}
		if licensed {
			l := e3
			l.ViaGroup = true
			u.Licenses = []m365.License{l}
		}
		enabled := u.AccountEnabled != nil && *u.AccountEnabled
		if enabled {
			groups = append(groups, m365.Group{ID: "g-all-company", DisplayName: "Contoso Labs (all company)", Mail: "allcompany@contoso-labs.example",
				SecurityEnabled: false, MailEnabled: true, Kind: "microsoft365"})
		}
		methods := []string{"microsoftAuthenticator", "phone"}
		switch name {
		case OutOfStepUser:
			u.JobTitle = "Junior Design Engineer"
		case WaitingUser:
			u.JobTitle = "Sales Associate"
		case ConflictUser:
			bad := "smtp:" + strings.ToLower(o.First("samaccountname")) + "@contosolabs.mail.example"
			u.ProvisioningErrors = []m365.ProvisioningError{{Category: "PropertyConflict", PropertyName: "ProxyAddresses", Value: bad, Occurred: LastSync}}
			u.ProxyAddresses = without(u.ProxyAddresses, bad)
			methods = []string{"microsoftAuthenticator"}
		case "Avery Lindqvist", "Dana Ferreira", "Kofi Asante":
			methods = []string{"fido2", "microsoftAuthenticator", "windowsHelloForBusiness"}
		case "Gus Whitfield":
			methods = nil // no MFA method registered
		}
		switch o.First("department") {
		case "Engineering":
			if enabled {
				groups = append(groups, m365.Group{ID: "g-plant2", DisplayName: "Plant 2 Engineering", Mail: "plant2@contoso-labs.example", MailEnabled: true, Kind: "microsoft365"})
			}
		case "Sales":
			if enabled {
				groups = append(groups, m365.Group{ID: "g-pipeline", DisplayName: "Sales Pipeline Readers", SecurityEnabled: true, Kind: "cloud"})
			}
		}
		if name == "Kofi Asante" {
			groups = append(groups, m365.Group{ID: "g-helpdesk-admins", DisplayName: "Helpdesk Administrators (role-assignable)", SecurityEnabled: true, Kind: "role"})
		}
		t.Users = append(t.Users, u)
		t.Groups[u.ID] = groups
		t.Methods[u.ID] = methods
		if ds := demoDevices(name); len(ds) > 0 {
			t.Devices[u.ID] = ds
		}
	}
	// A cloud-only account created in the admin center before the AD account.
	farah := m365.User{ID: cloudID(901), UserPrincipalName: "farah.haddad@contoso-labs.example", DisplayName: "Farah Haddad", GivenName: "Farah",
		Surname: "Haddad", Mail: "farah.haddad@contoso-labs.example", ProxyAddresses: []string{"SMTP:farah.haddad@contoso-labs.example"},
		JobTitle: "Process Engineer", UsageLocation: "US", AccountEnabled: ptr(true), CreatedDateTime: ptr(Now.AddDate(0, 0, -5)),
		Licenses: []m365.License{e3}}
	t.Users = append(t.Users, farah)
	t.Methods[farah.ID] = []string{"microsoftAuthenticator"}
	t.Groups[farah.ID] = []m365.Group{{ID: "g-all-company", DisplayName: "Contoso Labs (all company)", Mail: "allcompany@contoso-labs.example", MailEnabled: true, Kind: "microsoft365"}}
	// The cloud-only shared mailbox that holds ConflictUser's address.
	t.Users = append(t.Users, m365.User{ID: cloudID(902), UserPrincipalName: "sales.desk@contoso-labs.example", DisplayName: "Sales Desk",
		Mail: "sales.desk@contoso-labs.example", ProxyAddresses: []string{"SMTP:sales.desk@contoso-labs.example", "smtp:jade.mbeki@contosolabs.mail.example"},
		AccountEnabled: ptr(false), CreatedDateTime: ptr(Now.AddDate(-1, 0, 0))})
	return t
}

func demoDevices(name string) []m365.Device {
	seen := func(days int) *time.Time { return ptr(Now.AddDate(0, 0, -days)) }
	hybrid := func(n string, days int) m365.Device {
		return m365.Device{ID: "d-" + strings.ToLower(n), DisplayName: n, OperatingSystem: "Windows", TrustType: "ServerAd", IsManaged: true,
			IsCompliant: ptr(true), AccountEnabled: true, LastSignIn: seen(days)}
	}
	switch name {
	case "Avery Lindqvist":
		return []m365.Device{hybrid("WS-FIN-01", 1), {ID: "d-avery-phone", DisplayName: "Avery's iPhone", OperatingSystem: "iOS", TrustType: "Workplace",
			IsManaged: true, IsCompliant: ptr(true), AccountEnabled: true, LastSignIn: seen(0)}}
	case "Bea Okafor":
		return []m365.Device{hybrid("WS-FIN-02", 2)}
	case "Dana Ferreira":
		return []m365.Device{hybrid("WS-ENG-01", 0)}
	case "Elio Brandt":
		return []m365.Device{hybrid("WS-ENG-02", 75)}
	case "Hana Kowalski":
		return []m365.Device{{ID: "d-lt-sal-01", DisplayName: "LT-SAL-01", OperatingSystem: "Windows", TrustType: "AzureAd", IsManaged: true,
			IsCompliant: ptr(true), AccountEnabled: true, LastSignIn: seen(3)}}
	case "Ivan Petrov":
		return []m365.Device{{ID: "d-lt-sal-02", DisplayName: "LT-SAL-02", OperatingSystem: "Windows", TrustType: "AzureAd", IsManaged: true,
			IsCompliant: ptr(false), AccountEnabled: true, LastSignIn: seen(140)}}
	case "Kofi Asante":
		return []m365.Device{hybrid("WS-OPS-01", 1), {ID: "d-kofi-phone", DisplayName: "Pixel (Kofi)", OperatingSystem: "Android", TrustType: "Workplace",
			IsManaged: false, AccountEnabled: true, LastSignIn: seen(1)}}
	}
	return nil
}

// copyAttributes sets u's synced properties from o as Entra Connect's default
// rules would (the mirror's attribute mapping).
func copyAttributes(o ad.Object, managerUPN string, u *m365.User) {
	u.DisplayName, u.GivenName, u.Surname = o.First("displayname"), o.First("givenname"), o.First("sn")
	u.UserPrincipalName, u.Mail = o.First("userprincipalname"), o.First("mail")
	u.ProxyAddresses = append([]string(nil), o.Values("proxyaddresses")...)
	u.BusinessPhones = nil
	if p := o.First("telephonenumber"); p != "" {
		u.BusinessPhones = []string{p}
	}
	u.MobilePhone, u.JobTitle, u.Department, u.CompanyName = o.First("mobile"), o.First("title"), o.First("department"), o.First("company")
	u.OfficeLocation, u.StreetAddress, u.City, u.State = o.First("physicaldeliveryofficename"), o.First("streetaddress"), o.First("l"), o.First("st")
	u.PostalCode, u.EmployeeID = o.First("postalcode"), o.First("employeeid")
	u.Country = o.First("co")
	if u.Country == "" {
		u.Country = o.First("c")
	}
	if l := o.First("msexchusagelocation"); l != "" {
		u.UsageLocation = l
	}
	u.OnPremisesExtensionAttrs = map[string]string{}
	for i := 1; i <= 15; i++ {
		if v := o.First(fmt.Sprintf("extensionattribute%d", i)); v != "" {
			u.OnPremisesExtensionAttrs[fmt.Sprintf("extensionAttribute%d", i)] = v
		}
	}
	if n, err := strconv.ParseInt(o.First("useraccountcontrol"), 10, 64); err == nil {
		u.AccountEnabled = ptr(n&ad.UACAccountDisable == 0)
	}
	u.ManagerUPN = managerUPN
	u.OnPremisesSecurityIdentifier = o.First("objectsid")
	u.OnPremisesSamAccountName = o.First("samaccountname")
	u.OnPremisesUserPrincipalName = o.First("userprincipalname")
	u.OnPremisesDistinguishedName = o.DN
}

// NewSyncer returns a demo Connect Sync server. Sync now copies the AD
// object's synced attributes to its cloud user and moves the user's last sync
// time to the directory's clock, so differences clear; an address that is
// in a provisioning conflict stays out, as Entra does.
func NewSyncer(dir *ad.FakeClient, tenant *m365.FakeTenant) *dirsync.Fake {
	clock := func() time.Time {
		if dir.Now != nil {
			return dir.Now().UTC()
		}
		return time.Now().UTC()
	}
	status, note := dirsync.ClassifyConnectVersion("2.6.92.0", Now)
	f := &dirsync.Fake{K: dirsync.KindConnect, Now: clock, H: dirsync.Health{Server: "srv-entra01.lab.example", Version: "2.6.92.0",
		VersionStatus: status, VersionNote: note, SchedulerEnabled: true, IntervalMinutes: 30, LastCycle: ptr(LastSync),
		NextCycle: ptr(LastSync.Add(30 * time.Minute)), LastResult: "success", AppAuth: ptr(true)}}
	// A real scheduler runs every interval: report the next cycle after the
	// wall clock, so the demo never shows a next cycle in the past.
	f.Adjust = func(h *dirsync.Health) {
		if h.NextCycle == nil || h.IntervalMinutes <= 0 {
			return
		}
		next, now := *h.NextCycle, time.Now().UTC()
		for !next.After(now) {
			next = next.Add(time.Duration(h.IntervalMinutes) * time.Minute)
		}
		h.NextCycle = &next
	}
	f.OnSync = func(ctx context.Context, obj ad.Object) error {
		id, err := ad.ImmutableID(obj.First("objectguid"))
		if err != nil {
			return nil // no source anchor: nothing to export
		}
		u, err := tenant.UserByImmutableID(ctx, id)
		if err != nil || u == nil {
			return err // not in the sync scope
		}
		mgr := ""
		if dn := obj.First("manager"); dn != "" {
			if m, err := dir.Get(ctx, dn); err == nil {
				mgr = m.First("userprincipalname")
			}
		}
		copyAttributes(obj, mgr, u)
		for _, e := range u.ProvisioningErrors {
			if strings.EqualFold(e.PropertyName, "ProxyAddresses") {
				u.ProxyAddresses = without(u.ProxyAddresses, e.Value)
			}
		}
		u.OnPremisesLastSyncDateTime = ptr(clock())
		tenant.UpdateUser(*u)
		return nil
	}
	return f
}

func without(list []string, v string) []string {
	var out []string
	for _, s := range list {
		if !strings.EqualFold(s, v) {
			out = append(out, s)
		}
	}
	return out
}

func hasClass(o ad.Object, c string) bool {
	for _, v := range o.Values("objectclass") {
		if strings.EqualFold(v, c) {
			return true
		}
	}
	return false
}
