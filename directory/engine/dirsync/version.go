package dirsync

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Version status values for Health.VersionStatus.
const (
	VersionCurrent     = "current"
	VersionRetiring    = "retiring"
	VersionUnsupported = "unsupported"
	VersionUnknown     = "unknown"
)

// ConnectRelease is one row of Microsoft's Connect Sync 2.x retirement table.
// EndOfSupport is zero for the latest release, which has no date yet.
type ConnectRelease struct {
	Version      string
	Released     time.Time
	EndOfSupport time.Time
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// ConnectVersionsAsOf is the date ConnectReleases was checked against
// ConnectVersionSource (research 3). Update both together.
var ConnectVersionsAsOf = day(2026, time.November, 23)

// ConnectVersionSource is Microsoft's version history, which holds the table.
const ConnectVersionSource = "https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/reference-connect-version-history"

// ConnectReleases is the "Retiring Microsoft Entra Connect 2.x versions" table
// as of ConnectVersionsAsOf, oldest first. Microsoft: "Versions of Microsoft
// Entra Connect Sync 2.x retire 12 months from the date that a newer version is
// released" and "All other versions are not supported".
var ConnectReleases = []ConnectRelease{
	{"2.3.2.0", day(2023, time.December, 12), day(2025, time.April, 30)},
	{"2.3.6.0", day(2024, time.February, 21), day(2025, time.April, 30)},
	{"2.3.8.0", day(2024, time.April, 1), day(2025, time.April, 30)},
	{"2.3.20.0", day(2024, time.July, 15), day(2025, time.April, 30)},
	{"2.4.18.0", day(2024, time.October, 7), day(2025, time.October, 9)},
	{"2.4.21.0", day(2024, time.October, 9), day(2025, time.November, 15)},
	{"2.4.27.0", day(2024, time.November, 14), day(2026, time.January, 15)},
	{"2.4.129.0", day(2025, time.January, 15), day(2026, time.March, 27)},
	{"2.4.131.0", day(2025, time.March, 27), day(2026, time.May, 26)},
	{"2.5.3.0", day(2025, time.May, 27), day(2026, time.July, 31)},
	{"2.5.76.0", day(2025, time.July, 31), day(2026, time.September, 1)},
	{"2.5.79.0", day(2025, time.September, 1), day(2026, time.October, 23)},
	{"2.5.190.0", day(2025, time.November, 19), day(2027, time.February, 2)},
	{"2.6.1.0", day(2026, time.February, 2), day(2027, time.March, 10)},
	{"2.6.3.0", day(2026, time.March, 10), day(2027, time.July, 7)},
	{"2.6.84.0", day(2026, time.July, 7), day(2027, time.September, 16)},
	{"2.6.91.0", day(2026, time.September, 16), day(2027, time.September, 23)},
	{"2.6.92.0", day(2026, time.September, 23), time.Time{}},
}

// Deadlines from research 3.
var (
	// Versions before connectStopBelow stopped synchronizing on
	// connectStoppedOn (message center MC1237727).
	connectStopBelow = "2.5.79.0"
	connectStoppedOn = day(2026, time.September, 30)
	// "Upgrade Microsoft Entra Connect Sync to version 2.6.84.0 or later and
	// configure application-based authentication by April 7, 2027" (version
	// history page).
	connectAppAuthMinimum  = "2.6.84.0"
	connectAppAuthDeadline = day(2027, time.April, 7)
)

// retiringWindow is how close an end of support date must be for a supported
// version to be reported as retiring.
const retiringWindow = 180 * 24 * time.Hour

// staleTable is how old the shipped table may be before Health warns.
const staleTable = 180 * 24 * time.Hour

// ClassifyConnectVersion rates a Connect Sync version against the shipped
// table at now: current, retiring, unsupported or unknown, with a note.
func ClassifyConnectVersion(version string, now time.Time) (status, note string) {
	v, ok := parseVersion(version)
	if !ok {
		return VersionUnknown, "The Connect Sync version could not be read."
	}
	latest := ConnectReleases[len(ConnectReleases)-1]
	upgrade := "upgrade to " + latest.Version + " or later"
	if v[0] < 2 {
		return VersionUnsupported, "Connect Sync 1.x is retired and does not synchronize; " + upgrade + "."
	}
	if compareVersion(v, mustVersion(connectStopBelow)) < 0 {
		if !now.Before(connectStoppedOn) {
			return VersionUnsupported, fmt.Sprintf("Versions before %s stopped synchronizing on %s; %s.",
				connectStopBelow, longDate(connectStoppedOn), upgrade)
		}
		return VersionRetiring, fmt.Sprintf("Versions before %s stop synchronizing on %s; %s.",
			connectStopBelow, longDate(connectStoppedOn), upgrade)
	}
	if compareVersion(v, mustVersion(latest.Version)) > 0 {
		return VersionCurrent, fmt.Sprintf("Newer than %s, the latest version in the table dated %s; check %s.",
			latest.Version, longDate(ConnectVersionsAsOf), ConnectVersionSource)
	}
	var rel *ConnectRelease
	for i := range ConnectReleases {
		if compareVersion(v, mustVersion(ConnectReleases[i].Version)) == 0 {
			rel = &ConnectReleases[i]
			break
		}
	}
	if rel == nil {
		return VersionUnsupported, fmt.Sprintf("Not in Microsoft's list of supported versions as of %s; %s.",
			longDate(ConnectVersionsAsOf), upgrade)
	}
	eos := rel.EndOfSupport
	if !eos.IsZero() && !now.Before(eos) {
		return VersionUnsupported, fmt.Sprintf("Support ended on %s; %s.", longDate(eos), upgrade)
	}
	if compareVersion(v, mustVersion(connectAppAuthMinimum)) < 0 {
		if !now.Before(connectAppAuthDeadline) {
			return VersionUnsupported, fmt.Sprintf("Versions before %s stopped synchronizing on %s; %s.",
				connectAppAuthMinimum, longDate(connectAppAuthDeadline), upgrade)
		}
		return VersionRetiring, fmt.Sprintf("Upgrade to %s or later and configure application-based authentication by %s, or synchronization stops; support for %s ends on %s.",
			connectAppAuthMinimum, longDate(connectAppAuthDeadline), rel.Version, longDate(eos))
	}
	if !eos.IsZero() && eos.Sub(now) <= retiringWindow {
		return VersionRetiring, fmt.Sprintf("Support ends on %s; %s.", longDate(eos), upgrade)
	}
	if !eos.IsZero() {
		return VersionCurrent, fmt.Sprintf("Supported until %s.", longDate(eos))
	}
	return VersionCurrent, fmt.Sprintf("The latest version as of %s.", longDate(ConnectVersionsAsOf))
}

// connectTableWarning returns a warning when the shipped table is old.
func connectTableWarning(now time.Time) string {
	if now.Sub(ConnectVersionsAsOf) <= staleTable {
		return ""
	}
	return fmt.Sprintf("The Connect Sync version table shipped with ZTG Directory is dated %s; check for an update and %s.",
		longDate(ConnectVersionsAsOf), ConnectVersionSource)
}

type version [4]int

// parseVersion reads a dotted version of one to four numeric parts.
func parseVersion(s string) (version, bool) {
	var v version
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) == 0 || len(parts) > 4 || parts[0] == "" {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p[0] == '+' {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func mustVersion(s string) version {
	v, ok := parseVersion(s)
	if !ok {
		panic("dirsync: bad version in table: " + s)
	}
	return v
}

func compareVersion(a, b version) int {
	for i := range a {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

func longDate(t time.Time) string { return t.Format("January 2, 2006") }
