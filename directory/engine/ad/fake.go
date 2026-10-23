package ad

import (
	"context"
	"strings"
	"sync"
)

// FakeClient is a deterministic in-memory Client for callers and tests. If
// SearchFunc is supplied it controls filtering; otherwise Search returns all
// objects for an empty query or the conventional (objectClass=*) filter.
type FakeClient struct {
	mu         sync.Mutex
	Objects    []Object
	Groups     map[string][]Group
	Logons     map[string]LastLogonResult
	SearchFunc func(SearchRequest, []Object) ([]Object, error)
	closed     bool
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
	} else if strings.TrimSpace(req.Query) == "" {
		found = all
	} else {
		found = matchingQuery(req.Query, all)
	}
	if err != nil {
		return SearchResult{}, err
	}
	truncated := req.Limit > 0 && len(found) > req.Limit
	if truncated {
		found = found[:req.Limit]
	}
	return SearchResult{Objects: cloneObjects(found), Truncated: truncated}, nil
}

func (f *FakeClient) Get(ctx context.Context, dn string) (Object, error) {
	res, err := f.Search(ctx, SearchRequest{})
	if err != nil {
		return Object{}, err
	}
	for _, o := range res.Objects {
		if strings.EqualFold(o.DN, dn) {
			return o, nil
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
	return append([]Group(nil), f.Groups[strings.ToLower(dn)]...), nil
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

func matchingQuery(query string, objects []Object) []Object {
	query = strings.TrimSpace(query)
	var out []Object
	fields := []string{"anr", "samaccountname", "userprincipalname", "mail", "telephonenumber", "employeeid", "cn", "displayname"}
	for _, o := range objects {
		matched := false
		for _, attr := range fields {
			for _, v := range o.Attributes[attr] {
				if strings.Contains(strings.ToLower(v), strings.ToLower(query)) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if matched {
			out = append(out, o)
		}
	}
	return out
}
