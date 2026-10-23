// Package ad provides read-only Active Directory lookups.
package ad

import (
	"context"
	"errors"
	"strconv"
	"time"
)

var ErrUnsupportedPlatform = errors.New("Active Directory LDAP is supported only on Windows")
var ErrClosed = errors.New("directory client is closed")
var ErrNotFound = errors.New("directory object not found")
var ErrIncomplete = errors.New("directory result is incomplete")

const (
	defaultSearchLimit = 500
	maxSearchLimit     = 5000
)

func searchLimit(limit int) int {
	if limit <= 0 {
		return defaultSearchLimit
	}
	if limit > maxSearchLimit {
		return maxSearchLimit
	}
	return limit
}

// Config selects one domain controller and the search root. Server may be a
// host name or host:port. Credentials are always the current Windows identity.
// Timeout limits native network waits. Windows Negotiate binding is synchronous
// with a limit per response roundtrip; cancellation is observed after it returns.
type Config struct {
	Server  string
	BaseDN  string
	LDAPS   bool
	Timeout time.Duration
}

// Client deliberately exposes only read operations.
type Client interface {
	Search(context.Context, SearchRequest) (SearchResult, error)
	Get(context.Context, string) (Object, error)
	EffectiveGroups(context.Context, string) ([]Group, error)
	LastLogon(context.Context, string) (LastLogonResult, error)
	Close() error
}

type SearchRequest struct {
	// Query is plain text. The engine escapes it and searches common AD
	// identity fields using bounded substring matching.
	Query string
	Limit int
}

type SearchResult struct {
	Objects   []Object
	Truncated bool
}

// Object contains only the explicitly requested, non-secret directory
// attributes. Attribute names are normalized to lower case.
type Object struct {
	DN         string
	Attributes map[string][]string
}

func (o Object) Values(name string) []string { return o.Attributes[lowerASCII(name)] }
func (o Object) First(name string) string {
	v := o.Values(name)
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

type Group struct {
	DN      string
	Name    string
	SID     string
	Primary bool
	// Resolved is false when the group SID is valid but its object is outside
	// Config.BaseDN. SID and membership are still returned in that case.
	Resolved bool
}

// LastLogonResult reports the selected DC's replicated lastLogonTimestamp
// and its local lastLogon value. AD's lastLogon is not replicated; Partial is
// always true unless a caller separately queries every domain controller.
type LastLogonResult struct {
	LastLogon          time.Time
	LastLogonTimestamp time.Time
	Partial            bool
	Warning            string
}

// EscapeFilterValue applies RFC 4515 escaping to an untrusted filter value.
func EscapeFilterValue(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case 0:
			out = append(out, `\00`...)
		case '(':
			out = append(out, `\28`...)
		case ')':
			out = append(out, `\29`...)
		case '*':
			out = append(out, `\2a`...)
		case '\\':
			out = append(out, `\5c`...)
		default:
			if s[i] >= 0x80 {
				out = append(out, '\\', "0123456789abcdef"[s[i]>>4], "0123456789abcdef"[s[i]&15])
			} else {
				out = append(out, s[i])
			}
		}
	}
	return string(out)
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func fileTime(value string) time.Time {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.Unix(n/10_000_000-11_644_473_600, (n%10_000_000)*100)
}
