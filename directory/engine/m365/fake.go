package m365

import (
	"context"
	"strings"
	"sync"
)

// FakeTenant is an in-memory Tenant for tests and demo mode.
type FakeTenant struct {
	mu      sync.Mutex
	Org     Organization
	Users   []User
	Groups  map[string][]Group  // user ID to groups
	Devices map[string][]Device // user ID to devices
	Methods map[string][]string // user ID to method kinds
	// Forbid makes the named calls ("UserDevices", ...) return ErrForbidden,
	// to try a technician without the role.
	Forbid map[string]bool
	Who    string
}

// NewFakeTenant returns an empty tenant.
func NewFakeTenant(who string) *FakeTenant {
	return &FakeTenant{Who: who, Groups: map[string][]Group{}, Devices: map[string][]Device{}, Methods: map[string][]string{}, Forbid: map[string]bool{}}
}

func (f *FakeTenant) check(ctx context.Context, call string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.Forbid[call] {
		return ErrForbidden
	}
	return nil
}

func (f *FakeTenant) Account() string { return f.Who }

func (f *FakeTenant) Organization(ctx context.Context) (Organization, error) {
	if err := f.check(ctx, "Organization"); err != nil {
		return Organization{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Org, nil
}

func (f *FakeTenant) find(ctx context.Context, call string, match func(User) bool) (*User, error) {
	if err := f.check(ctx, call); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.Users {
		if match(u) {
			c := u
			return &c, nil
		}
	}
	return nil, nil
}

func (f *FakeTenant) UserByImmutableID(ctx context.Context, id string) (*User, error) {
	return f.find(ctx, "UserByImmutableID", func(u User) bool { return id != "" && u.OnPremisesImmutableID == id })
}

func (f *FakeTenant) UserBySID(ctx context.Context, sid string) (*User, error) {
	return f.find(ctx, "UserBySID", func(u User) bool { return sid != "" && strings.EqualFold(u.OnPremisesSecurityIdentifier, sid) })
}

func (f *FakeTenant) UsersByName(ctx context.Context, v string) ([]User, error) {
	if err := f.check(ctx, "UsersByName"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []User
	for _, u := range f.Users {
		if v != "" && (strings.EqualFold(u.UserPrincipalName, v) || strings.EqualFold(u.Mail, v)) {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *FakeTenant) UserGroups(ctx context.Context, id string) ([]Group, error) {
	if err := f.check(ctx, "UserGroups"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Group(nil), f.Groups[id]...), nil
}

func (f *FakeTenant) UserDevices(ctx context.Context, id string) ([]Device, error) {
	if err := f.check(ctx, "UserDevices"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Device(nil), f.Devices[id]...), nil
}

func (f *FakeTenant) AuthMethods(ctx context.Context, id string) ([]string, error) {
	if err := f.check(ctx, "AuthMethods"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Methods[id]...), nil
}

// UpdateUser replaces the stored user with the same ID (used by demo Sync now).
func (f *FakeTenant) UpdateUser(u User) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.Users {
		if f.Users[i].ID == u.ID {
			f.Users[i] = u
			return
		}
	}
	f.Users = append(f.Users, u)
}

var _ Tenant = (*FakeTenant)(nil)
