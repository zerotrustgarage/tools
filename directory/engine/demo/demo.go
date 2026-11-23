// Package demo builds a sample directory in memory (an ad.FakeClient) for
// tests and for the console's demo mode. It is fictional: the domain is
// lab.example and no name belongs to a real person.
package demo

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

// Base is the demo domain's naming context.
const Base = "DC=lab,DC=example"

// Now is the fixed time the demo directory is built around.
var Now = time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)

type person struct {
	given, sur, title, dept, office, ou, phone string
	flags                                      int
	lastLogonDays                              int // <0 never
	groups                                     []string
	manager                                    string
	locked, mustChange                         bool
	createdDaysAgo                             int
}

// NewDirectory returns a populated fake directory bound as LAB\ztg.tech.
func NewDirectory() *ad.FakeClient {
	f := ad.NewFake(nil)
	f.Account = `LAB\ztg.tech`
	f.DCName = "dc01.lab.example"
	f.Now = func() time.Time { return Now }
	add := func(dn string, attrs map[string][]string) {
		lower := map[string][]string{}
		for k, v := range attrs {
			lower[strings.ToLower(k)] = v
		}
		if _, ok := lower["whencreated"]; !ok {
			lower["whencreated"] = []string{Now.AddDate(-2, 0, 0).Format("20060102150405.0Z")}
		}
		lower["whenchanged"] = []string{Now.AddDate(0, 0, -3).Format("20060102150405.0Z")}
		lower["distinguishedname"] = []string{dn}
		f.Objects = append(f.Objects, ad.Object{DN: dn, Attributes: lower})
	}
	ou := func(name, parent, desc string) string {
		dn := "OU=" + name + "," + parent
		add(dn, map[string][]string{"objectClass": {"top", "organizationalUnit"}, "ou": {name}, "name": {name}, "description": {desc}})
		return dn
	}
	add(Base, map[string][]string{"objectClass": {"top", "domain", "domainDNS"}, "name": {"lab"}, "dc": {"lab"}})
	add("CN=Users,"+Base, map[string][]string{"objectClass": {"top", "container"}, "cn": {"Users"}, "description": {"Default container for upgraded user accounts"}})
	add("CN=Computers,"+Base, map[string][]string{"objectClass": {"top", "container"}, "cn": {"Computers"}})
	company := ou("Contoso Labs", Base, "Managed by ZTG Directory")
	staff := ou("Staff", company, "People")
	ous := map[string]string{}
	for _, d := range []string{"Finance", "Engineering", "Sales", "Operations"} {
		ous[d] = ou(d, staff, d+" department")
	}
	ous["Leavers"] = ou("Leavers", company, "Disabled accounts kept for the retention period")
	groupsOU := ou("Groups", company, "Security and distribution groups")
	devices := ou("Devices", company, "Workstations and laptops")
	servers := ou("Servers", company, "Member servers")
	contacts := ou("Contacts", company, "External contacts")
	admins := ou("Admins", company, "Tier 0 and Tier 1 administrative accounts")

	group := func(cn, parent, desc string, scope int) string {
		dn := "CN=" + cn + "," + parent
		add(dn, map[string][]string{"objectClass": {"top", "group"}, "cn": {cn}, "name": {cn}, "sAMAccountName": {strings.ReplaceAll(cn, " ", "")},
			"description": {desc}, "groupType": {strconv.Itoa(scope)}})
		return dn
	}
	const global, universal, distribution = -2147483646, -2147483640, 8
	g := map[string]string{
		"Finance Team":            group("Finance Team", groupsOU, "All finance staff", global),
		"Engineering Team":        group("Engineering Team", groupsOU, "All engineering staff", global),
		"Sales Team":              group("Sales Team", groupsOU, "All sales staff", global),
		"Operations Team":         group("Operations Team", groupsOU, "All operations staff", global),
		"VPN Users":               group("VPN Users", groupsOU, "Remote access", global),
		"M365 E3 Licensing":       group("M365 E3 Licensing", groupsOU, "Group-based licensing (synced)", global),
		"All Staff":               group("All Staff", groupsOU, "Company-wide distribution list", distribution),
		"Project Atlas":           group("Project Atlas", groupsOU, "Temporary project access", universal),
		"Helpdesk":                group("Helpdesk", groupsOU, "Help desk technicians", global),
		"ZTG Directory Operators": group("ZTG Directory Operators", groupsOU, "May use ZTG Directory", global),
		"ZTG Directory Approvers": group("ZTG Directory Approvers", groupsOU, "May approve ZTG Directory workflows", global),
		"Empty Legacy Group":      group("Empty Legacy Group", groupsOU, "No members", global),
	}
	g["Domain Admins"] = group("Domain Admins", "CN=Users,"+Base, "Designated administrators of the domain", global)

	people := []person{
		{"Avery", "Lindqvist", "Finance Director", "Finance", "Head Office", "Finance", "+1 555 0100", 0, 1, []string{"Finance Team", "VPN Users", "M365 E3 Licensing", "All Staff"}, "", false, false, 900},
		{"Bea", "Okafor", "Accountant", "Finance", "Head Office", "Finance", "+1 555 0101", 0, 2, []string{"Finance Team", "M365 E3 Licensing", "All Staff"}, "Avery Lindqvist", false, false, 400},
		{"Caleb", "Nakamura", "Payroll Specialist", "Finance", "Head Office", "Finance", "+1 555 0102", 0, 40, []string{"Finance Team", "M365 E3 Licensing", "All Staff"}, "Avery Lindqvist", true, false, 300},
		{"Dana", "Ferreira", "Engineering Manager", "Engineering", "Plant 2", "Engineering", "+1 555 0200", 0, 0, []string{"Engineering Team", "VPN Users", "M365 E3 Licensing", "All Staff", "Project Atlas"}, "", false, false, 1200},
		{"Elio", "Brandt", "Design Engineer", "Engineering", "Plant 2", "Engineering", "+1 555 0201", 0, 3, []string{"Engineering Team", "M365 E3 Licensing", "All Staff", "Project Atlas"}, "Dana Ferreira", false, false, 200},
		{"Farah", "Haddad", "Process Engineer", "Engineering", "Plant 2", "Engineering", "+1 555 0202", 0, -1, []string{"Engineering Team", "M365 E3 Licensing"}, "Dana Ferreira", false, true, 2},
		{"Gus", "Whitfield", "QA Technician", "Engineering", "Plant 2", "Engineering", "+1 555 0203", ad.UACDontExpirePassword, 120, []string{"Engineering Team", "All Staff"}, "Dana Ferreira", false, false, 1500},
		{"Hana", "Kowalski", "Sales Lead", "Sales", "Branch East", "Sales", "+1 555 0300", 0, 1, []string{"Sales Team", "VPN Users", "M365 E3 Licensing", "All Staff"}, "", false, false, 700},
		{"Ivan", "Petrov", "Account Executive", "Sales", "Branch East", "Sales", "+1 555 0301", 0, 5, []string{"Sales Team", "M365 E3 Licensing", "All Staff"}, "Hana Kowalski", false, false, 150},
		{"Jade", "Mbeki", "Account Executive", "Sales", "Branch East", "Sales", "+1 555 0302", 0, 0, []string{"Sales Team", "M365 E3 Licensing", "All Staff"}, "Hana Kowalski", false, false, 5},
		{"Kofi", "Asante", "Operations Coordinator", "Operations", "Head Office", "Operations", "+1 555 0400", 0, 1, []string{"Operations Team", "M365 E3 Licensing", "All Staff", "Helpdesk", "ZTG Directory Operators"}, "", false, false, 600},
		{"Lena", "Sorensen", "Facilities Lead", "Operations", "Head Office", "Operations", "+1 555 0401", 0, 200, []string{"Operations Team", "All Staff"}, "Kofi Asante", false, false, 1000},
		{"Marco", "Rossi", "Former Analyst", "Finance", "Head Office", "Leavers", "", ad.UACAccountDisable, 95, []string{"Finance Team"}, "", false, false, 800},
		{"Nia", "Johnson", "Former Sales Rep", "Sales", "Branch East", "Leavers", "", ad.UACAccountDisable, 60, nil, "", false, false, 500},
	}
	dnOf := map[string]string{}
	for _, p := range people {
		dnOf[p.given+" "+p.sur] = "CN=" + p.given + " " + p.sur + "," + ous[p.ou]
	}
	for i, p := range people {
		name := p.given + " " + p.sur
		sam := strings.ToLower(p.given + "." + p.sur)
		attrs := map[string][]string{
			"objectClass": {"top", "person", "organizationalPerson", "user"}, "objectCategory": {"CN=Person,CN=Schema,CN=Configuration," + Base},
			"cn": {name}, "name": {name}, "givenName": {p.given}, "sn": {p.sur}, "displayName": {name},
			"sAMAccountName": {sam}, "userPrincipalName": {sam + "@contoso-labs.example"}, "mail": {sam + "@contoso-labs.example"},
			"proxyAddresses": {"SMTP:" + sam + "@contoso-labs.example", "smtp:" + sam + "@contosolabs.mail.example"},
			"title":          {p.title}, "department": {p.dept}, "company": {"Contoso Labs"}, "physicalDeliveryOfficeName": {p.office},
			"employeeID": {fmt.Sprintf("E%04d", 1001+i)}, "userAccountControl": {strconv.Itoa(ad.UACNormalAccount | p.flags)},
			"accountExpires": {"9223372036854775807"}, "pwdLastSet": {ad.ToFileTime(Now.AddDate(0, 0, -30-i*7))},
			"whenCreated":         {Now.AddDate(0, 0, -p.createdDaysAgo).Format("20060102150405.0Z")},
			"msExchUsageLocation": {"US"}, "badPwdCount": {"0"}, "objectGUID": {fmt.Sprintf("6f1c2a%02d-4b7d-4e2a-9c1e-0a5b3c7d%04d", i, 1000+i)},
			"objectSid": {fmt.Sprintf("S-1-5-21-1004336348-1177238915-682003330-%d", 1104+i)},
		}
		if p.phone != "" {
			attrs["telephoneNumber"] = []string{p.phone}
		}
		if p.lastLogonDays >= 0 {
			t := ad.ToFileTime(Now.AddDate(0, 0, -p.lastLogonDays))
			attrs["lastLogonTimestamp"], attrs["lastLogon"] = []string{t}, []string{t}
		}
		if p.manager != "" {
			attrs["manager"] = []string{dnOf[p.manager]}
		}
		if p.locked {
			attrs["lockoutTime"] = []string{ad.ToFileTime(Now.Add(-20 * time.Minute))}
			attrs["badPwdCount"] = []string{"5"}
		}
		if p.mustChange {
			attrs["pwdLastSet"] = []string{"0"}
		}
		add(dnOf[name], attrs)
	}
	adminDN := "CN=Admin Riley Chen," + admins
	add(adminDN, map[string][]string{"objectClass": {"top", "person", "organizationalPerson", "user"}, "cn": {"Admin Riley Chen"},
		"displayName": {"Admin Riley Chen"}, "givenName": {"Riley"}, "sn": {"Chen"}, "sAMAccountName": {"adm.riley.chen"},
		"userPrincipalName": {"adm.riley.chen@lab.example"}, "userAccountControl": {strconv.Itoa(ad.UACNormalAccount)}, "adminCount": {"1"},
		"description": {"Tier 0 administrative account"}, "lastLogonTimestamp": {ad.ToFileTime(Now.AddDate(0, 0, -1))},
		"objectGUID": {"6f1c2a90-4b7d-4e2a-9c1e-0a5b3c7d1090"}, "objectSid": {"S-1-5-21-1004336348-1177238915-682003330-1190"}})
	techDN := "CN=ZTG Tech," + admins
	add(techDN, map[string][]string{"objectClass": {"top", "person", "organizationalPerson", "user"}, "cn": {"ZTG Tech"}, "displayName": {"ZTG Tech"},
		"sAMAccountName": {"ztg.tech"}, "userPrincipalName": {"ztg.tech@lab.example"}, "userAccountControl": {strconv.Itoa(ad.UACNormalAccount)},
		"description": {"Delegated help desk account used in demo mode"}, "lastLogonTimestamp": {ad.ToFileTime(Now)},
		"objectGUID": {"6f1c2a91-4b7d-4e2a-9c1e-0a5b3c7d1091"}, "objectSid": {"S-1-5-21-1004336348-1177238915-682003330-1191"}})
	add("CN=krbtgt,CN=Users,"+Base, map[string][]string{"objectClass": {"top", "person", "organizationalPerson", "user"}, "cn": {"krbtgt"},
		"sAMAccountName": {"krbtgt"}, "userAccountControl": {strconv.Itoa(ad.UACNormalAccount | ad.UACAccountDisable)}, "description": {"Key Distribution Center Service Account"}})

	computer := func(name, parent, osName string, days int, uac int) {
		dn := "CN=" + name + "," + parent
		add(dn, map[string][]string{"objectClass": {"top", "person", "organizationalPerson", "user", "computer"}, "cn": {name}, "name": {name},
			"sAMAccountName": {name + "$"}, "dNSHostName": {strings.ToLower(name) + ".lab.example"}, "operatingSystem": {osName},
			"userAccountControl": {strconv.Itoa(uac)}, "lastLogonTimestamp": {ad.ToFileTime(Now.AddDate(0, 0, -days))}})
	}
	for i, n := range []string{"WS-FIN-01", "WS-FIN-02", "WS-ENG-01", "WS-ENG-02", "LT-SAL-01", "LT-SAL-02", "WS-OPS-01"} {
		days := []int{1, 2, 0, 75, 3, 140, 1}[i]
		computer(n, devices, "Windows 11 Enterprise", days, 0x1000)
	}
	computer("SRV-FILE01", servers, "Windows Server 2025 Datacenter", 0, 0x1000)
	computer("SRV-ENTRA01", servers, "Windows Server 2022 Standard", 0, 0x1000)
	computer("DC01", "OU=Domain Controllers,"+Base, "Windows Server 2025 Datacenter", 0, ad.UACServerTrustAccount)
	add("OU=Domain Controllers,"+Base, map[string][]string{"objectClass": {"top", "organizationalUnit"}, "ou": {"Domain Controllers"}, "name": {"Domain Controllers"}})
	for _, c := range [][3]string{{"Priya Raman", "priya.raman@supplier.example", "Supplier account manager"}, {"Tom Becker", "tom.becker@auditor.example", "External auditor"}} {
		add("CN="+c[0]+","+contacts, map[string][]string{"objectClass": {"top", "person", "organizationalPerson", "contact"}, "cn": {c[0]},
			"displayName": {c[0]}, "mail": {c[1]}, "description": {c[2]}, "company": {strings.Split(strings.Split(c[1], "@")[1], ".")[0]}})
	}

	// Memberships, with memberOf and directReports maintained as AD would.
	link := func(groupDN, memberDN string) {
		for i := range f.Objects {
			if strings.EqualFold(f.Objects[i].DN, groupDN) {
				f.Objects[i].Attributes["member"] = append(f.Objects[i].Attributes["member"], memberDN)
			}
			if strings.EqualFold(f.Objects[i].DN, memberDN) {
				f.Objects[i].Attributes["memberof"] = append(f.Objects[i].Attributes["memberof"], groupDN)
			}
		}
	}
	for _, p := range people {
		for _, gr := range p.groups {
			link(g[gr], dnOf[p.given+" "+p.sur])
		}
	}
	link(g["Domain Admins"], adminDN)
	link(g["ZTG Directory Operators"], techDN)
	link(g["Helpdesk"], techDN)
	link(g["ZTG Directory Approvers"], dnOf["Kofi Asante"])
	for _, p := range people {
		if p.manager != "" {
			for i := range f.Objects {
				if strings.EqualFold(f.Objects[i].DN, dnOf[p.manager]) {
					f.Objects[i].Attributes["directreports"] = append(f.Objects[i].Attributes["directreports"], dnOf[p.given+" "+p.sur])
				}
			}
		}
	}
	// Ivan Petrov's title was changed in AD ten minutes ago, after the last
	// sync cycle, so the mirror shows it waiting for sync (tenant.go).
	for i := range f.Objects {
		if strings.EqualFold(f.Objects[i].DN, dnOf[WaitingUser]) {
			f.Objects[i].Attributes["whenchanged"] = []string{Now.Add(-10 * time.Minute).Format("20060102150405.0Z")}
		}
	}
	// The demo technician has delegated rights everywhere except Admins and
	// built-in containers, so refusals can be tried.
	f.RightsFunc = func(dn string) ad.Rights {
		if ad.UnderDN(dn, admins) || ad.UnderDN(dn, "CN=Users,"+Base) || ad.UnderDN(dn, "OU=Domain Controllers,"+Base) {
			return ad.Rights{}
		}
		return ad.FakeDefaultRights
	}
	f.PasswordCheck = func(p []byte) bool { return len(p) >= 12 }
	return f
}

// TechnicianDN is the demo technician's object.
const TechnicianDN = "CN=ZTG Tech,OU=Admins,OU=Contoso Labs," + Base
