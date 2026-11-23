package demo

import "github.com/zerotrustgarage/tools/directory/engine/template"

// Templates returns the demo role templates.
func Templates() []template.Template {
	staff := "OU=Staff,OU=Contoso Labs," + Base
	groups := "OU=Groups,OU=Contoso Labs," + Base
	user := func(name, dept, office string, extra ...string) template.Template {
		t := template.Template{
			Name: name, Description: "New " + dept + " starter", Version: 1, OU: "OU=" + dept + "," + staff,
			CN: "{givenName} {sn}", DisplayName: "{givenName} {sn}", SAMAccountName: "{givenName}.{sn}",
			UserPrincipalName: "{givenName}.{sn}@contoso-labs.example", Mail: "{givenName}.{sn}@contoso-labs.example",
			ProxyAddresses: []string{"SMTP:{mail}"},
			Attributes:     map[string]string{"company": "Contoso Labs", "department": dept, "physicalDeliveryOfficeName": office, "msExchUsageLocation": "US"},
			Groups:         []string{"CN=" + dept + " Team," + groups, "CN=All Staff," + groups, "CN=M365 E3 Licensing," + groups},
			Fields: []template.Field{
				{Attribute: "title", Label: "Job title", Required: true},
				{Attribute: "manager", Label: "Manager", Type: "user"},
				{Attribute: "employeeID", Label: "Employee ID", Required: true, Pattern: `^E[0-9]{4}$`, Help: "E followed by four digits"},
				{Attribute: "telephoneNumber", Label: "Phone"},
			},
			Enable: true,
		}
		for _, g := range extra {
			t.Groups = append(t.Groups, "CN="+g+","+groups)
		}
		return t
	}
	return []template.Template{
		user("Finance staff", "Finance", "Head Office"),
		user("Engineering staff", "Engineering", "Plant 2", "VPN Users"),
		user("Sales staff", "Sales", "Branch East", "VPN Users"),
		user("Operations staff", "Operations", "Head Office"),
		{Name: "Security group", Description: "A new global security group", Version: 1, Class: "group", OU: groups, CN: "{name}",
			Attributes: map[string]string{"groupType": "-2147483646", "description": "{description}"},
			Fields:     []template.Field{{Attribute: "description", Label: "Purpose", Required: true}, {Attribute: "managedBy", Label: "Owner", Type: "user"}}},
		{Name: "External contact", Description: "A mail contact", Version: 1, Class: "contact", OU: "OU=Contacts,OU=Contoso Labs," + Base,
			Fields: []template.Field{{Attribute: "mail", Label: "Email", Required: true}, {Attribute: "company", Label: "Company"}}},
	}
}
