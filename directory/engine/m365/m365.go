// Package m365 is ZTG Directory's Microsoft 365 side (design 6.2a): Microsoft
// Graph called as the signed-in technician through delegated sign-in, so Entra
// enforces that person's own roles. This file is the contract the mirror,
// sync and console packages code against; graph.go and auth.go implement it
// against a real tenant and fake.go in memory.
package m365

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotSignedIn reports that no Microsoft 365 sign-in is active.
var ErrNotSignedIn = errors.New("not signed in to Microsoft 365")

// ErrForbidden reports that Graph refused the call: the delegated permission
// was not consented, or the technician's Entra role does not allow it.
var ErrForbidden = errors.New("Microsoft 365 refused this for the signed-in account (missing permission or role)")

// Cloud is the customer's Microsoft cloud.
type Cloud string

const (
	CloudCommercial Cloud = "commercial"
	CloudGCCHigh    Cloud = "gcchigh"
	CloudDoD        Cloud = "dod"
)

// Endpoints are the sign-in and Graph hosts of a cloud.
type Endpoints struct {
	Login string // for example https://login.microsoftonline.com
	Graph string // for example https://graph.microsoft.com
}

// EndpointsFor returns the documented endpoints of a cloud (Microsoft Graph
// national cloud deployments). GCC (moderate) uses the commercial endpoints.
func EndpointsFor(c Cloud) (Endpoints, error) {
	switch c {
	case CloudCommercial, "":
		return Endpoints{Login: "https://login.microsoftonline.com", Graph: "https://graph.microsoft.com"}, nil
	case CloudGCCHigh:
		return Endpoints{Login: "https://login.microsoftonline.us", Graph: "https://graph.microsoft.us"}, nil
	case CloudDoD:
		return Endpoints{Login: "https://login.microsoftonline.us", Graph: "https://dod-graph.microsoft.us"}, nil
	}
	return Endpoints{}, fmt.Errorf("unknown Microsoft cloud %q", c)
}

// Settings name the tenant and the public client app registration.
type Settings struct {
	TenantID string `json:"tenantId"` // GUID or verified domain
	ClientID string `json:"clientId"` // public client (no secret)
	Cloud    Cloud  `json:"cloud"`
	// CloudSync enables Cloud Sync provisioning on demand (needs
	// Synchronization.ReadWrite.All and the Hybrid Identity Administrator role).
	CloudSync bool `json:"cloudSync,omitempty"`
}

// Validate checks the settings' shape.
func (s Settings) Validate() error {
	if strings.TrimSpace(s.TenantID) == "" || strings.TrimSpace(s.ClientID) == "" {
		return errors.New("Microsoft 365 needs the tenant ID and the app registration's client ID")
	}
	_, err := EndpointsFor(s.Cloud)
	return err
}

// License is one assigned license.
type License struct {
	SkuID         string   `json:"skuId"`
	SkuPartNumber string   `json:"skuPartNumber"` // for example SPE_E3; resolved from subscribedSkus
	DisabledPlans []string `json:"disabledPlans,omitempty"`
	// ViaGroup is true when the license comes from group-based licensing.
	ViaGroup bool `json:"viaGroup,omitempty"`
}

// ProvisioningError is one onPremisesProvisioningErrors entry.
type ProvisioningError struct {
	Category     string    `json:"category"`
	PropertyName string    `json:"propertyCausingError"`
	Value        string    `json:"value"`
	Occurred     time.Time `json:"occurredDateTime"`
}

// User is the cloud user that mirrors an AD user (Graph user resource,
// selected properties).
type User struct {
	ID                           string              `json:"id"`
	UserPrincipalName            string              `json:"userPrincipalName"`
	DisplayName                  string              `json:"displayName"`
	GivenName                    string              `json:"givenName"`
	Surname                      string              `json:"surname"`
	Mail                         string              `json:"mail"`
	ProxyAddresses               []string            `json:"proxyAddresses"`
	BusinessPhones               []string            `json:"businessPhones"`
	MobilePhone                  string              `json:"mobilePhone"`
	JobTitle                     string              `json:"jobTitle"`
	Department                   string              `json:"department"`
	CompanyName                  string              `json:"companyName"`
	OfficeLocation               string              `json:"officeLocation"`
	StreetAddress                string              `json:"streetAddress"`
	City                         string              `json:"city"`
	State                        string              `json:"state"`
	PostalCode                   string              `json:"postalCode"`
	Country                      string              `json:"country"`
	EmployeeID                   string              `json:"employeeId"`
	UsageLocation                string              `json:"usageLocation"`
	AccountEnabled               *bool               `json:"accountEnabled"`
	CreatedDateTime              *time.Time          `json:"createdDateTime"`
	OnPremisesSyncEnabled        *bool               `json:"onPremisesSyncEnabled"`
	OnPremisesImmutableID        string              `json:"onPremisesImmutableId"`
	OnPremisesSecurityIdentifier string              `json:"onPremisesSecurityIdentifier"`
	OnPremisesSamAccountName     string              `json:"onPremisesSamAccountName"`
	OnPremisesUserPrincipalName  string              `json:"onPremisesUserPrincipalName"`
	OnPremisesDistinguishedName  string              `json:"onPremisesDistinguishedName"`
	OnPremisesLastSyncDateTime   *time.Time          `json:"onPremisesLastSyncDateTime"`
	OnPremisesExtensionAttrs     map[string]string   `json:"onPremisesExtensionAttributes"` // extensionAttribute1..15
	ProvisioningErrors           []ProvisioningError `json:"onPremisesProvisioningErrors"`
	Licenses                     []License           `json:"licenses"`
	LastSuccessfulSignIn         *time.Time          `json:"lastSuccessfulSignIn"`
	ManagerUPN                   string              `json:"managerUpn"`
}

// Group is a cloud group the user belongs to.
type Group struct {
	ID                    string `json:"id"`
	DisplayName           string `json:"displayName"`
	Mail                  string `json:"mail"`
	SecurityEnabled       bool   `json:"securityEnabled"`
	MailEnabled           bool   `json:"mailEnabled"`
	OnPremisesSyncEnabled bool   `json:"onPremisesSyncEnabled"`
	// Kind is "synced", "cloud", "microsoft365" (Unified) or "role".
	Kind string `json:"kind"`
}

// Device is a device registered to or owned by the user.
type Device struct {
	ID              string     `json:"id"`
	DisplayName     string     `json:"displayName"`
	OperatingSystem string     `json:"operatingSystem"`
	TrustType       string     `json:"trustType"` // AzureAd, ServerAd (hybrid joined), Workplace
	IsManaged       bool       `json:"isManaged"`
	IsCompliant     *bool      `json:"isCompliant"`
	AccountEnabled  bool       `json:"accountEnabled"`
	LastSignIn      *time.Time `json:"approximateLastSignInDateTime"`
}

// Organization is the tenant and its directory synchronization state.
type Organization struct {
	ID                         string     `json:"id"`
	DisplayName                string     `json:"displayName"`
	OnPremisesSyncEnabled      bool       `json:"onPremisesSyncEnabled"`
	OnPremisesLastSyncDateTime *time.Time `json:"onPremisesLastSyncDateTime"`
	VerifiedDomains            []string   `json:"verifiedDomains"`
}

// Tenant is a signed-in Microsoft 365 connection. Every call runs as the
// signed-in technician; a refusal by Graph returns ErrForbidden.
type Tenant interface {
	// Account names the signed-in cloud account (UPN).
	Account() string
	Organization(ctx context.Context) (Organization, error)
	// UserByImmutableID finds the user whose onPremisesImmutableId equals id
	// (the Base64 of the AD source anchor); nil when none.
	UserByImmutableID(ctx context.Context, id string) (*User, error)
	// UserBySID finds the user whose onPremisesSecurityIdentifier equals sid.
	UserBySID(ctx context.Context, sid string) (*User, error)
	// UsersByName finds users whose userPrincipalName or mail equals v, for
	// "possible match, not linked".
	UsersByName(ctx context.Context, v string) ([]User, error)
	// UserGroups lists the user's direct group memberships.
	UserGroups(ctx context.Context, userID string) ([]Group, error)
	// UserDevices lists the user's registered and owned devices.
	UserDevices(ctx context.Context, userID string) ([]Device, error)
	// AuthMethodCount counts the user's registered sign-in methods (password
	// excluded) and names their kinds.
	AuthMethods(ctx context.Context, userID string) ([]string, error)
}
