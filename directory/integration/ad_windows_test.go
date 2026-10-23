//go:build windows

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

// This test is deliberately opt-in. Hosted Windows unit tests are not a DC lab.
func TestReadOnlyDirectory(t *testing.T) {
	if os.Getenv("ZTG_DIRECTORY_INTEGRATION") != "1" {
		t.Skip("requires an isolated domain lab; see docs/validation.md")
	}
	server, base, dn := os.Getenv("ZTG_DIRECTORY_TEST_SERVER"), os.Getenv("ZTG_DIRECTORY_TEST_BASE"), os.Getenv("ZTG_DIRECTORY_TEST_DN")
	if server == "" || base == "" || dn == "" {
		t.Fatal("set ZTG_DIRECTORY_TEST_SERVER, ZTG_DIRECTORY_TEST_BASE and ZTG_DIRECTORY_TEST_DN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := ad.Open(ctx, ad.Config{Server: server, BaseDN: base, LDAPS: os.Getenv("ZTG_DIRECTORY_TEST_LDAPS") == "1", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	obj, err := client.Get(ctx, dn)
	if err != nil {
		t.Fatal(err)
	}
	if obj.DN == "" {
		t.Fatal("object has no DN")
	}
	result, err := client.Search(ctx, ad.SearchRequest{Query: obj.First("sAMAccountName"), Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Objects) == 0 {
		t.Fatal("search did not find a lab object")
	}
	if _, err := client.EffectiveGroups(ctx, dn); err != nil {
		t.Fatal(err)
	}
	logon, err := client.LastLogon(ctx, dn)
	if err != nil {
		t.Fatal(err)
	}
	if !logon.Partial || logon.Warning == "" {
		t.Fatal("per-DC lastLogon must be labeled partial")
	}
}
