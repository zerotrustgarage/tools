package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

type fakeClient struct {
	searchRequest ad.SearchRequest
	searchResult  ad.SearchResult
	object        ad.Object
	groups        []ad.Group
	lastLogon     ad.LastLogonResult
	err           error
	closed        bool
}

func noUpdates(context.Context, string, []string, io.Writer, io.Writer) error {
	return errors.New("update command was not expected")
}

func (f *fakeClient) Search(_ context.Context, req ad.SearchRequest) (ad.SearchResult, error) {
	f.searchRequest = req
	return f.searchResult, f.err
}
func (f *fakeClient) Get(context.Context, string) (ad.Object, error) {
	return f.object, f.err
}
func (f *fakeClient) EffectiveGroups(context.Context, string) ([]ad.Group, error) {
	return f.groups, f.err
}
func (f *fakeClient) LastLogon(context.Context, string) (ad.LastLogonResult, error) {
	return f.lastLogon, f.err
}
func (f *fakeClient) Close() error { f.closed = true; return nil }

func TestSearchPassesPlainQueryAndProducesJSON(t *testing.T) {
	client := &fakeClient{searchResult: ad.SearchResult{Objects: []ad.Object{{DN: "CN=Alice,DC=example,DC=com"}}, Truncated: true}}
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"search", "--server", "dc.example.com", "--base-dn", "DC=example,DC=com", "--query", "a*)(cn=*)", "--json"}, &stdout, &stderr,
		func(_ context.Context, cfg ad.Config) (ad.Client, error) {
			if cfg.Timeout != defaultTimeout || cfg.Server != "dc.example.com" || cfg.BaseDN != "DC=example,DC=com" {
				t.Fatalf("unexpected config: %+v", cfg)
			}
			return client, nil
		}, noUpdates)
	if err != nil {
		t.Fatal(err)
	}
	if !client.closed {
		t.Fatal("client was not closed")
	}
	if client.searchRequest.Query != "a*)(cn=*)" {
		t.Fatalf("search query changed unexpectedly: %q", client.searchRequest.Query)
	}
	var decoded ad.SearchResult
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if !decoded.Truncated || len(decoded.Objects) != 1 {
		t.Fatalf("unexpected result: %+v", decoded)
	}
}

func TestInvalidArgumentsFailBeforeConnecting(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing server", []string{"object", "--base-dn", "DC=example,DC=com", "CN=Alice"}, "--server is required"},
		{"missing base dn", []string{"object", "--server", "dc", "CN=Alice"}, "--base-dn is required"},
		{"empty query", []string{"search", "--server", "dc", "--base-dn", "DC=example", "--query", " "}, "non-empty --query"},
		{"query control", []string{"search", "--server", "dc", "--base-dn", "DC=example", "--query", "alice\x1b[2J"}, "control characters"},
		{"bad limit", []string{"search", "--server", "dc", "--base-dn", "DC=example", "--query", "alice", "--limit", "1001"}, "--limit must be between"},
		{"zero timeout", []string{"groups", "--server", "dc", "--base-dn", "DC=example", "--timeout", "0s", "CN=Alice"}, "--timeout must be greater than 0"},
		{"no dn", []string{"groups", "--server", "dc", "--base-dn", "DC=example"}, "requires exactly one distinguished name"},
		{"dn control", []string{"groups", "--server", "dc", "--base-dn", "DC=example", "CN=Alice\x1b[2J"}, "control characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), tc.args, &stdout, &stderr,
				func(context.Context, ad.Config) (ad.Client, error) { called = true; return &fakeClient{}, nil }, noUpdates)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got error %v, want containing %q", err, tc.want)
			}
			if called {
				t.Fatal("connected despite invalid arguments")
			}
		})
	}
}

func TestDirectoryErrorsAreReturnedAndClientClosed(t *testing.T) {
	client := &fakeClient{err: errors.New("backend failure")}
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"object", "--server", "dc", "--base-dn", "DC=example", "CN=Alice"}, &stdout, &stderr,
		func(context.Context, ad.Config) (ad.Client, error) { return client, nil }, noUpdates)
	if err == nil || err.Error() != "backend failure" {
		t.Fatalf("got error %v", err)
	}
	if !client.closed {
		t.Fatal("client was not closed after backend error")
	}
}

func TestHumanOutputQuotesUntrustedDirectoryText(t *testing.T) {
	client := &fakeClient{object: ad.Object{
		DN:         "CN=Alice\x1b[31m\nInjected",
		Attributes: map[string][]string{"displayname": {"Alice\x1b[2J"}},
	}}
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"object", "--server", "dc", "--base-dn", "DC=example", "CN=Alice"}, &stdout, &stderr,
		func(context.Context, ad.Config) (ad.Client, error) { return client, nil }, noUpdates)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "\x1b") || strings.Contains(stdout.String(), "\nInjected") {
		t.Fatalf("terminal control data escaped into output: %q", stdout.String())
	}
}

func TestFlagDiagnosticsEscapeControlCharacters(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"object", "--bad\x1b[2J"}, &stdout, &stderr, nil, noUpdates)
	if err == nil {
		t.Fatal("expected an unknown flag error")
	}
	if strings.Contains(stderr.String(), "\x1b") {
		t.Fatalf("flag diagnostic contains terminal control data: %q", stderr.String())
	}
}

func TestTimeoutPassedToBackendAndContext(t *testing.T) {
	client := &fakeClient{}
	var stdout, stderr bytes.Buffer
	called := false
	err := run(context.Background(), []string{"last-logon", "--server", "dc", "--base-dn", "DC=example", "--timeout", "3s", "CN=Alice"}, &stdout, &stderr,
		func(ctx context.Context, cfg ad.Config) (ad.Client, error) {
			called = true
			if cfg.Timeout != 3*time.Second {
				t.Fatalf("timeout not passed to backend: %s", cfg.Timeout)
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("backend context has no deadline")
			}
			return client, nil
		}, noUpdates)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("backend was not called")
	}
}

func TestUpdateCommandDelegatesExplicitCheck(t *testing.T) {
	called := false
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"check-updates", "--json"}, &stdout, &stderr, nil,
		func(_ context.Context, current string, args []string, out, _ io.Writer) error {
			called = true
			if current != "dev" || len(args) != 1 || args[0] != "--json" {
				t.Fatalf("unexpected update invocation: current=%q args=%v", current, args)
			}
			_, err := fmt.Fprintln(out, `{"status":"development"}`)
			return err
		})
	if err != nil {
		t.Fatal(err)
	}
	if !called || !strings.Contains(stdout.String(), `"status":"development"`) {
		t.Fatalf("update command did not delegate: %q", stdout.String())
	}
}
