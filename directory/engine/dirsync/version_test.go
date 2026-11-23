package dirsync

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyConnectVersion(t *testing.T) {
	asOf := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		v      string
		now    time.Time
		status string
		note   string
	}{
		{"2.6.92.0", asOf, VersionCurrent, "latest version"},
		{"2.6.91.0", asOf, VersionCurrent, "Supported until September 23, 2027"},
		{"2.6.91.0", time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC), VersionRetiring, "September 23, 2027"},
		{"2.6.91.0", time.Date(2027, 9, 23, 0, 0, 0, 0, time.UTC), VersionUnsupported, "Support ended"},
		{"2.6.84.0", asOf, VersionCurrent, "September 16, 2027"},
		{"2.6.3.0", asOf, VersionRetiring, "April 7, 2027"},
		{"2.5.190.0", asOf, VersionRetiring, "application-based authentication"},
		{"2.6.3.0", time.Date(2027, 4, 7, 0, 0, 0, 0, time.UTC), VersionUnsupported, "stopped synchronizing on April 7, 2027"},
		{"2.5.79.0", asOf, VersionUnsupported, "Support ended on October 23, 2026"},
		{"2.5.79.0", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), VersionRetiring, "April 7, 2027"},
		{"2.5.76.0", asOf, VersionUnsupported, "stopped synchronizing on September 30, 2026"},
		{"2.5.76.0", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), VersionRetiring, "stop synchronizing on September 30, 2026"},
		{"1.6.16.0", asOf, VersionUnsupported, "1.x"},
		{"2.6.50.0", asOf, VersionUnsupported, "Not in Microsoft's list"},
		{"2.7.1.0", asOf, VersionCurrent, "Newer than 2.6.92.0"},
		{"", asOf, VersionUnknown, ""},
		{"2.x", asOf, VersionUnknown, ""},
		{"2.6.92.0.1", asOf, VersionUnknown, ""},
	}
	for _, c := range cases {
		status, note := ClassifyConnectVersion(c.v, c.now)
		if status != c.status || !strings.Contains(note, c.note) {
			t.Errorf("%q at %s: %s %q, want %s %q", c.v, c.now.Format("2006-01-02"), status, note, c.status, c.note)
		}
	}
}

func TestConnectTable(t *testing.T) {
	prev := version{}
	for i, r := range ConnectReleases {
		v := mustVersion(r.Version)
		if compareVersion(v, prev) <= 0 {
			t.Fatalf("row %d (%s) out of order", i, r.Version)
		}
		prev = v
		if last := i == len(ConnectReleases)-1; last != r.EndOfSupport.IsZero() {
			t.Fatalf("row %s: only the latest release has no end of support date", r.Version)
		}
	}
	if connectTableWarning(ConnectVersionsAsOf.AddDate(0, 1, 0)) != "" {
		t.Fatal("fresh table reported as stale")
	}
	if connectTableWarning(ConnectVersionsAsOf.AddDate(1, 0, 0)) == "" {
		t.Fatal("old table not reported")
	}
}
