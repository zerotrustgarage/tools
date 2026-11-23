package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

// Role is an app role granted by AD group membership (design 6.5 step 6,
// runbook R126).
type Role string

const (
	RoleOperator Role = "operator"
	RoleApprover Role = "approver"
)

// ErrNotAuthorized reports that the account is in neither the Operators nor
// the Approvers group, or that its groups could not be read (fail closed).
var ErrNotAuthorized = errors.New("this account is not authorized to use ZTG Directory")

// ErrAccountNotFound reports that the signed-in account's own user object
// was not found under the configured base DN.
var ErrAccountNotFound = errors.New("the signed-in account was not found in the directory")

// account is the signed-in account's own user object.
type account struct {
	DN          string
	DisplayName string
	SAM         string
	UPN         string
}

// ParseAccount splits what a user typed into a user name and domain:
// DOMAIN\user gives ("user", "DOMAIN"); a UPN or a bare name is returned
// unchanged with an empty domain.
func ParseAccount(typed string) (user, domain string) {
	typed = strings.TrimSpace(typed)
	if i := strings.IndexByte(typed, '\\'); i >= 0 {
		return typed[i+1:], typed[:i]
	}
	return typed, ""
}

// findAccount locates the credential's own user object: a search by class
// user, then an exact, case-insensitive match on sAMAccountName (DOMAIN\user
// or a bare name) or userPrincipalName (a UPN). Exactly one match is
// required.
func findAccount(ctx context.Context, w ad.Client, cred *ad.Credential) (account, error) {
	name := cred.User
	byUPN := cred.Domain == "" && strings.Contains(name, "@")
	res, err := w.Search(ctx, ad.SearchRequest{Query: name, Class: ad.ClassUser, Limit: 5000})
	if err != nil {
		return account{}, fmt.Errorf("look up the signed-in account: %w", err)
	}
	var found []ad.Object
	for _, o := range res.Objects {
		v := o.First("sAMAccountName")
		if byUPN {
			v = o.First("userPrincipalName")
		}
		if v != "" && strings.EqualFold(v, name) {
			found = append(found, o)
		}
	}
	switch {
	case len(found) == 1:
		o := found[0]
		return account{DN: o.DN, DisplayName: o.First("displayName"), SAM: o.First("sAMAccountName"), UPN: o.First("userPrincipalName")}, nil
	case len(found) > 1:
		return account{}, fmt.Errorf("%w: the name matches more than one account", ErrAccountNotFound)
	case res.Truncated:
		return account{}, fmt.Errorf("%w: the search returned too many results to find it", ErrAccountNotFound)
	}
	return account{}, ErrAccountNotFound
}

// rolesFor reads the account's effective groups (tokenGroups, nesting
// included) and grants roles by group cn, case-insensitively. It fails
// closed: unreadable groups or no matching group return ErrNotAuthorized.
func rolesFor(ctx context.Context, w ad.Client, s Settings, dn string) ([]Role, error) {
	groups, err := w.EffectiveGroups(ctx, dn)
	if err != nil {
		return nil, fmt.Errorf("%w: its groups could not be read (%v)", ErrNotAuthorized, err)
	}
	var operator, approver bool
	for _, g := range groups {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			continue
		}
		if strings.EqualFold(name, strings.TrimSpace(s.OperatorsGroup)) {
			operator = true
		}
		if strings.EqualFold(name, strings.TrimSpace(s.ApproversGroup)) {
			approver = true
		}
	}
	var roles []Role
	if operator {
		roles = append(roles, RoleOperator)
	}
	if approver {
		roles = append(roles, RoleApprover)
	}
	if len(roles) == 0 {
		return nil, fmt.Errorf("%w: it is in neither %q nor %q", ErrNotAuthorized, s.OperatorsGroup, s.ApproversGroup)
	}
	return roles, nil
}

// authorize finds the account and its roles over an open writer.
func authorize(ctx context.Context, s Settings, cred *ad.Credential, w ad.Client) (account, []Role, error) {
	acct, err := findAccount(ctx, w, cred)
	if err != nil {
		return account{}, nil, err
	}
	roles, err := rolesFor(ctx, w, s, acct.DN)
	if err != nil {
		return account{}, nil, err
	}
	return acct, roles, nil
}
