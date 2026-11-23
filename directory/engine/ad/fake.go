package ad

import (
	"context"
	"strings"
	"sync"
	"time"
)

// FakeClient is a deterministic in-memory Client for callers and tests. If
// SearchFunc is supplied it controls filtering; otherwise Search returns all
// objects for an empty query, applying the native client's result limits.
type FakeClient struct {
	mu         sync.Mutex
	Objects    []Object
	Groups     map[string][]Group
	Logons     map[string]LastLogonResult
	SearchFunc func(SearchRequest, []Object) ([]Object, error)
	closed     bool

	// Write support (fake_writer.go).
	DeletedObjects []Object
	Passwords      map[string]int // lower-case DN to number of password sets
	// RightsFunc returns the effective rights for dn; nil grants
	// FakeDefaultRights everywhere. Writes are refused with
	// ErrInsufficientRights when the attribute or class is not granted.
	RightsFunc func(dn string) Rights
	// PasswordCheck rejects a password with ErrConstraint when it returns false.
	PasswordCheck func(password []byte) bool
	// FailOn injects an error for one operation ("add", "modify", "delete",
	// "rename", "password", "undelete") on one DN.
	FailOn  func(op, dn string) error
	Ops     []string
	Account string
	DCName  string
	Now     func() time.Time
}

func NewFake(objects []Object) *FakeClient {
	return &FakeClient{Objects: cloneObjects(objects), Groups: map[string][]Group{}, Logons: map[string]LastLogonResult{}}
}

func (f *FakeClient) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return SearchResult{}, ErrClosed
	}
	all := cloneObjects(f.Objects)
	var found []Object
	var err error
	if f.SearchFunc != nil {
		found, err = f.SearchFunc(req, all)
	} else {
		if _, ferr := BuildFilter(req); ferr != nil {
			return SearchResult{}, ferr
		}
		for _, o := range all {
			if Matches(req, o) {
				found = append(found, o)
			}
		}
	}
	if err != nil {
		return SearchResult{}, err
	}
	limit := searchLimit(req.Limit)
	truncated := len(found) > limit
	if truncated {
		found = found[:limit]
	}
	return SearchResult{Objects: cloneObjects(found), Truncated: truncated}, nil
}

func (f *FakeClient) Get(ctx context.Context, dn string) (Object, error) {
	if err := ctx.Err(); err != nil {
		return Object{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return Object{}, ErrClosed
	}
	for _, o := range f.Objects {
		if strings.EqualFold(o.DN, dn) {
			return cloneObjects([]Object{o})[0], nil
		}
	}
	return Object{}, ErrNotFound
}

func (f *FakeClient) EffectiveGroups(ctx context.Context, dn string) ([]Group, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, ErrClosed
	}
	if gs, ok := f.Groups[strings.ToLower(dn)]; ok {
		return append([]Group(nil), gs...), nil
	}
	// Without an explicit entry, derive direct groups from memberOf so writes
	// are reflected (the fake does not model nesting).
	for _, o := range f.Objects {
		if strings.EqualFold(o.DN, dn) {
			var out []Group
			for _, m := range o.Values("memberof") {
				out = append(out, Group{DN: m, Name: rdnValue(m), Resolved: true})
			}
			return out, nil
		}
	}
	return nil, nil
}

func (f *FakeClient) LastLogon(ctx context.Context, dn string) (LastLogonResult, error) {
	if err := ctx.Err(); err != nil {
		return LastLogonResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return LastLogonResult{}, ErrClosed
	}
	v, ok := f.Logons[strings.ToLower(dn)]
	if !ok {
		return LastLogonResult{}, ErrNotFound
	}
	return v, nil
}

func (f *FakeClient) Close() error { f.mu.Lock(); defer f.mu.Unlock(); f.closed = true; return nil }

func cloneObjects(in []Object) []Object {
	out := make([]Object, len(in))
	for i, o := range in {
		out[i] = Object{DN: o.DN, Attributes: make(map[string][]string, len(o.Attributes))}
		for k, v := range o.Attributes {
			out[i].Attributes[lowerASCII(k)] = append([]string(nil), v...)
		}
	}
	return out
}
