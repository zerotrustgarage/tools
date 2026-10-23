//go:build windows

package ad

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const rangeTestDN = "CN=User,OU=People,DC=example,DC=test"

func TestWinLDAPGetContinuesRangedMemberOf(t *testing.T) {
	var requests atomic.Int32
	fixture := startLDAPRangeFixture(t, func(request ldapSearchRequest) ([][]byte, int) {
		requestCount := int(requests.Add(1))
		assertRangeSearch(t, request, rangeTestDN, "memberOf")
		switch requestCount {
		case 1:
			return [][]byte{ldapEntry(rangeTestDN,
				ldapAttribute("memberOf"),
				ldapAttribute("memberOf;range=0-1", "CN=Grüppe,DC=example,DC=test", "CN=Operators,DC=example,DC=test"),
			)}, 0
		case 2:
			assertRequested(t, request, "memberOf;range=2-*")
			return [][]byte{ldapEntry(rangeTestDN,
				ldapAttribute("memberOf;range=2-*"),
				ldapAttribute("memberOf;range=2-3", "CN=Auditors,DC=example,DC=test", "CN=Readers,DC=example,DC=test"),
			)}, 0
		case 3:
			assertRequested(t, request, "memberOf;range=4-*")
			return [][]byte{ldapEntry(rangeTestDN,
				ldapAttribute("memberOf;range=4-*", "CN=Support,DC=example,DC=test"),
			)}, 0
		default:
			t.Errorf("unexpected extra LDAP search %d", requestCount)
			return nil, 0
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entry, err := fixture.client.Get(ctx, rangeTestDN)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := []string{"CN=Grüppe,DC=example,DC=test", "CN=Operators,DC=example,DC=test", "CN=Auditors,DC=example,DC=test", "CN=Readers,DC=example,DC=test", "CN=Support,DC=example,DC=test"}
	if got := entry.Values("memberOf"); !equalStrings(got, want) {
		t.Fatalf("memberOf = %#v, want %#v", got, want)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("LDAP searches = %d, want 3", got)
	}
}

func TestWinLDAPRangeContinuationUsesCanonicalResponseDN(t *testing.T) {
	callerDN := `CN=Zo\C3\AB,OU=People,DC=example,DC=test`
	canonicalDN := "CN=Zoë,OU=People,DC=example,DC=test"
	var requests atomic.Int32
	fixture := startLDAPRangeFixture(t, func(request ldapSearchRequest) ([][]byte, int) {
		requestCount := int(requests.Add(1))
		if request.scope != 0 {
			t.Errorf("search scope = %d, want base scope 0", request.scope)
		}
		if requestCount == 1 {
			if request.base != callerDN {
				t.Errorf("initial search base = %q, want caller DN %q", request.base, callerDN)
			}
			return [][]byte{ldapEntry(canonicalDN, ldapAttribute("memberOf;range=0-0", "CN=First,DC=example,DC=test"))}, 0
		}
		if request.base != canonicalDN {
			t.Errorf("continuation search base = %q, want canonical response DN %q", request.base, canonicalDN)
		}
		assertRequested(t, request, "memberOf;range=1-*")
		return [][]byte{ldapEntry(canonicalDN, ldapAttribute("memberOf;range=1-*", "CN=Last,DC=example,DC=test"))}, 0
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entry, err := fixture.client.Get(ctx, callerDN)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := entry.Values("memberOf"); len(got) != 2 {
		t.Fatalf("memberOf values = %#v, want 2 values", got)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("LDAP searches = %d, want 2", got)
	}
}

func TestWinLDAPBinaryAttributeContinuesRangedTokenGroups(t *testing.T) {
	first := testSID(513)
	second := testSID(544)
	var requests atomic.Int32
	fixture := startLDAPRangeFixture(t, func(request ldapSearchRequest) ([][]byte, int) {
		requestCount := int(requests.Add(1))
		assertRangeSearch(t, request, rangeTestDN, "tokenGroups")
		switch requestCount {
		case 1:
			return [][]byte{ldapEntry(rangeTestDN,
				ldapBinaryAttribute("tokenGroups;range=0-0", first),
				ldapBinaryAttribute("tokenGroups"),
			)}, 0
		case 2:
			assertRequested(t, request, "tokenGroups;range=1-*")
			return [][]byte{ldapEntry(rangeTestDN, ldapBinaryAttribute("tokenGroups;range=1-*", second))}, 0
		default:
			t.Errorf("unexpected extra LDAP search %d", requestCount)
			return nil, 0
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := fixture.client.binaryAttribute(ctx, rangeTestDN, "tokenGroups")
	if err != nil {
		t.Fatalf("binaryAttribute: %v", err)
	}
	if len(got) != 2 || string(got[0]) != string(first) || string(got[1]) != string(second) {
		t.Fatalf("tokenGroups = %x, want [%x %x]", got, first, second)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("LDAP searches = %d, want 2", got)
	}
}

func TestWinLDAPGetRejectsInvalidRangeContinuations(t *testing.T) {
	cases := []struct {
		name       string
		firstName  string
		firstCount int
		nextName   string
		nextValues []string
	}{
		{name: "gap", firstName: "memberOf;range=0-0", firstCount: 1, nextName: "memberOf;range=2-*", nextValues: []string{"CN=Later,DC=example,DC=test"}},
		{name: "overlap", firstName: "memberOf;range=0-1", firstCount: 2, nextName: "memberOf;range=1-*", nextValues: []string{"CN=Later,DC=example,DC=test"}},
		{name: "nonprogress empty page", firstName: "memberOf;range=0-0", firstCount: 1, nextName: "memberOf;range=1-*"},
		{name: "count mismatch", firstName: "memberOf;range=0-1", firstCount: 1, nextName: "memberOf;range=2-*", nextValues: []string{"CN=Later,DC=example,DC=test"}},
		{name: "missing attribute", firstName: "memberOf;range=0-0", firstCount: 1, nextName: "description", nextValues: []string{"missing"}},
		{name: "different DN", firstName: "memberOf;range=0-0", firstCount: 1, nextName: "memberOf;range=1-*", nextValues: []string{"CN=Later,DC=example,DC=test"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			fixture := startLDAPRangeFixture(t, func(request ldapSearchRequest) ([][]byte, int) {
				requests++
				assertRangeSearch(t, request, rangeTestDN, "memberOf")
				if requests == 1 {
					values := make([]string, tc.firstCount)
					for i := range values {
						values[i] = fmt.Sprintf("CN=Group%d,DC=example,DC=test", i)
					}
					return [][]byte{ldapEntry(rangeTestDN, ldapAttribute(tc.firstName, values...))}, 0
				}
				dn := rangeTestDN
				if tc.name == "different DN" {
					dn = "CN=Other,DC=example,DC=test"
				}
				return [][]byte{ldapEntry(dn, ldapAttribute(tc.nextName, tc.nextValues...))}, 0
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := fixture.client.Get(ctx, rangeTestDN)
			if !errors.Is(err, ErrIncomplete) {
				t.Fatalf("Get error = %v, want errors.Is(ErrIncomplete)", err)
			}
		})
	}
}

func TestWinLDAPGetRejectsTwoPopulatedRangeDescriptors(t *testing.T) {
	fixture := startLDAPRangeFixture(t, func(request ldapSearchRequest) ([][]byte, int) {
		assertRangeSearch(t, request, rangeTestDN, "memberOf")
		return [][]byte{ldapEntry(rangeTestDN,
			ldapAttribute("memberOf;range=0-0", "CN=First,DC=example,DC=test"),
			ldapAttribute("memberOf;range=1-*", "CN=Second,DC=example,DC=test"),
		)}, 0
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := fixture.client.Get(ctx, rangeTestDN)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Get error = %v, want errors.Is(ErrIncomplete)", err)
	}
}

func TestWinLDAPGetCapsRangeRequests(t *testing.T) {
	var requests atomic.Int32
	fixture := startLDAPRangeFixture(t, func(request ldapSearchRequest) ([][]byte, int) {
		requestCount := int(requests.Add(1))
		assertRangeSearch(t, request, rangeTestDN, "memberOf")
		start := requestCount - 1
		if requestCount == 1 {
			return [][]byte{ldapEntry(rangeTestDN, ldapAttribute("memberOf;range=0-0", "CN=Group0,DC=example,DC=test"))}, 0
		}
		assertRequested(t, request, fmt.Sprintf("memberOf;range=%d-*", start))
		return [][]byte{ldapEntry(rangeTestDN, ldapAttribute(fmt.Sprintf("memberOf;range=%d-%d", start, start), fmt.Sprintf("CN=Group%d,DC=example,DC=test", start)))}, 0
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := fixture.client.Get(ctx, rangeTestDN)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Get error = %v, want errors.Is(ErrIncomplete)", err)
	}
	if got := requests.Load(); got > 16 {
		t.Fatalf("LDAP searches = %d, exceeds 16-request cap", got)
	}
}

func TestWinLDAPGetRejectsMultipleEmptyRangePlaceholders(t *testing.T) {
	fixture := startLDAPFixture(t, func() ([][]byte, int) {
		return [][]byte{ldapEntry(rangeTestDN,
			ldapAttribute("memberOf;range=0-0", "CN=First,DC=example,DC=test"),
			ldapAttribute("memberOf"),
			ldapAttribute("memberOf;range=0-*"),
		)}, 0
	})
	_, err := fixture.client.Get(context.Background(), rangeTestDN)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Get error = %v, want ErrIncomplete", err)
	}
}

func TestWinLDAPGetCapsCombinedRangeValues(t *testing.T) {
	var calls atomic.Int32
	fixture := startLDAPRangeFixture(t, func(request ldapSearchRequest) ([][]byte, int) {
		if calls.Add(1) == 1 {
			values := make([]string, 4095)
			for i := range values {
				values[i] = fmt.Sprintf("CN=Group%d,DC=example,DC=test", i)
			}
			return [][]byte{ldapEntry(rangeTestDN, ldapAttribute("memberOf;range=0-4094", values...))}, 0
		}
		assertRequested(t, request, "memberOf;range=4095-*")
		return [][]byte{ldapEntry(rangeTestDN, ldapAttribute("memberOf;range=4095-*", "CN=Next,DC=example,DC=test", "CN=OverLimit,DC=example,DC=test"))}, 0
	})
	entry, err := fixture.client.Get(context.Background(), rangeTestDN)
	if !errors.Is(err, ErrIncomplete) || entry.DN != "" {
		t.Fatalf("Get returned partial success: DN=%q err=%v", entry.DN, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("searches=%d, want 2", calls.Load())
	}
}

func TestWinLDAPRangeContinuationHonorsCancellation(t *testing.T) {
	continuationSeen := make(chan struct{})
	allowResponse := make(chan struct{})
	fixture := startLDAPRangeFixture(t, func(request ldapSearchRequest) ([][]byte, int) {
		assertRangeSearch(t, request, rangeTestDN, "memberOf")
		if strings.Contains(strings.Join(request.attributes, ","), "range=1-*") {
			close(continuationSeen)
			<-allowResponse
			return [][]byte{ldapEntry(rangeTestDN, ldapAttribute("memberOf;range=1-*", "CN=Later,DC=example,DC=test"))}, 0
		}
		return [][]byte{ldapEntry(rangeTestDN, ldapAttribute("memberOf;range=0-0", "CN=First,DC=example,DC=test"))}, 0
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := fixture.client.Get(ctx, rangeTestDN)
		result <- err
	}()
	select {
	case <-continuationSeen:
	case <-time.After(5 * time.Second):
		cancel()
		close(allowResponse)
		t.Fatal("client did not request a range continuation")
	}
	cancel()
	close(allowResponse)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Get error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Get did not stop after context cancellation")
	}
}

func assertRangeSearch(t *testing.T, request ldapSearchRequest, dn, attribute string) {
	t.Helper()
	if request.base != dn {
		t.Errorf("search base = %q, want %q", request.base, dn)
	}
	if request.scope != 0 {
		t.Errorf("search scope = %d, want base scope 0", request.scope)
	}
	if len(request.attributes) == 0 {
		t.Errorf("search requested no attributes")
		return
	}
	requested := hasRequested(request, attribute)
	for _, name := range request.attributes {
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(attribute)+";range=") {
			requested = true
		}
	}
	if !requested {
		t.Errorf("search attributes %#v omit %q", request.attributes, attribute)
	}
}

func assertRequested(t *testing.T, request ldapSearchRequest, want string) {
	t.Helper()
	if !hasRequested(request, want) {
		t.Errorf("search attributes %#v omit %q", request.attributes, want)
	}
}

func hasRequested(request ldapSearchRequest, want string) bool {
	for _, attribute := range request.attributes {
		if strings.EqualFold(attribute, want) {
			return true
		}
	}
	return false
}

func testSID(rid uint32) []byte {
	sid := []byte{1, 2, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0, byte(rid), byte(rid >> 8), byte(rid >> 16), byte(rid >> 24)}
	return sid
}
