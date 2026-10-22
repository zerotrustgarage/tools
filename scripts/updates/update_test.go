package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSharedCases(t *testing.T) {
	data, err := os.ReadFile("fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Tool, Current, Status, Latest string
		Releases                            []release
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("per_page") != "100" {
					t.Error("unexpected request")
				}
				json.NewEncoder(w).Encode(tc.Releases)
			}))
			defer srv.Close()
			got, err := check(context.Background(), srv.Client(), srv.URL, tc.Tool, tc.Current)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.Status || got.Latest != tc.Latest {
				t.Fatalf("got %+v; expected %s/%s", got, tc.Status, tc.Latest)
			}
			if !strings.HasPrefix(got.ReleaseURL, "https://github.com/zerotrustgarage/tools/releases") {
				t.Fatal("unsafe release URL")
			}
		})
	}
}

func TestFailuresAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"rate limit", `{}`, "rate limit", 429},
		{"offline service", `{}`, "HTTP 503", 503},
		{"redirect", `[]`, "HTTP 302", 302},
		{"object", `{}`, "invalid update", 200},
		{"null", " null \n", "invalid update", 200},
		{"broken JSON", `[{`, "invalid update", 200},
		{"size cap", strings.Repeat(" ", maxBytes+1), "too large", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := check(context.Background(), srv.Client(), srv.URL, "scanner", "1.0.0")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %s, got %v", tc.want, err)
			}
		})
	}
}

func TestPaging(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("page") == "1" {
			json.NewEncoder(w).Encode(make([]release, 100))
		} else {
			w.Write([]byte(`[{"tag_name":"scanner-v1.2.0","published_at":"2026-10-22T12:00:00Z","assets":[{"name":"SHA256SUMS.txt","state":"uploaded","size":1},{"name":"ZTG-Scanner-1.2.0-x64.msi","state":"uploaded","size":1}]}]`))
		}
	}))
	defer srv.Close()
	r, err := check(context.Background(), srv.Client(), srv.URL, "scanner", "1.0.0")
	if err != nil || requests != 2 || r.Latest != "1.2.0" {
		t.Fatalf("pagination: %+v, %v, %d requests", r, err, requests)
	}
}

func TestInvalidLocalInputsAndCancellation(t *testing.T) {
	for _, tool := range []string{"../scanner", "https://evil.example"} {
		if _, err := Check(context.Background(), tool, "1.0.0"); err == nil {
			t.Fatal("unknown tool accepted")
		}
	}
	if _, err := Check(context.Background(), "scanner", "bad\nversion"); err == nil {
		t.Fatal("unsafe version accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Check(ctx, "tftp", "1.0.0"); err == nil {
		t.Fatal("canceled check succeeded")
	}
}
