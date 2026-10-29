package schedule

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// clock is an injectable clock: tests move time with advance and never sleep.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)} }

func fixedJitter(d time.Duration) func(time.Duration) time.Duration {
	return func(max time.Duration) time.Duration {
		if d > max {
			return max
		}
		return d
	}
}

func item(key string, minutes int) Item {
	return Item{Key: key, Interval: time.Duration(minutes) * time.Minute}
}

func TestIntervalBounds(t *testing.T) {
	for _, m := range []int{15, 16, 60, 1440} {
		if _, err := ValidInterval(m); err != nil {
			t.Errorf("%d minutes refused: %v", m, err)
		}
	}
	for _, m := range []int{-1, 0, 1, 14, 1441, 100000} {
		if _, err := ValidInterval(m); err == nil {
			t.Errorf("%d minutes accepted", m)
		}
	}
	s := New(newClock().now, fixedJitter(0))
	if err := s.Set([]Item{{Key: "a", Interval: 14 * time.Minute}}); err == nil {
		t.Error("scheduler accepted a 14 minute interval")
	}
	if err := s.Set([]Item{{Key: "a", Interval: 25 * time.Hour}}); err == nil {
		t.Error("scheduler accepted a 25 hour interval")
	}
	if err := s.Set([]Item{{Key: "a", Interval: 90 * time.Second * 60}}); err != nil {
		t.Errorf("90 minutes refused: %v", err)
	}
	if err := s.Set([]Item{{Key: "a", Interval: 15*time.Minute + time.Second}}); err == nil {
		t.Error("a fractional minute interval was accepted")
	}
	if err := s.Set([]Item{item("a", 30), item("a", 30)}); err == nil {
		t.Error("duplicate keys accepted")
	}
	var many []Item
	for i := 0; i <= MaxSchedules; i++ {
		many = append(many, item(string(rune('a'+i)), 30))
	}
	if err := s.Set(many); err == nil {
		t.Error("more than the maximum number of schedules accepted")
	}
}

func TestJitterBounds(t *testing.T) {
	for interval, want := range map[time.Duration]time.Duration{
		15 * time.Minute: 90 * time.Second,
		30 * time.Minute: 3 * time.Minute,
		time.Hour:        5 * time.Minute,
		24 * time.Hour:   5 * time.Minute,
	} {
		if got := JitterMax(interval); got != want {
			t.Errorf("JitterMax(%s) = %s, want %s", interval, got, want)
		}
	}
	// The production source stays within [0, max].
	for i := 0; i < 2000; i++ {
		if j := RandomJitter(90 * time.Second); j < 0 || j > 90*time.Second {
			t.Fatalf("jitter %s out of bounds", j)
		}
	}
	if RandomJitter(0) != 0 || RandomJitter(-time.Second) != 0 {
		t.Fatal("non-positive max must give no jitter")
	}
	// A misbehaving source cannot push a cycle outside the bound or earlier than the interval.
	c := newClock()
	s := New(c.now, func(time.Duration) time.Duration { return 24 * time.Hour })
	if err := s.Set([]Item{item("a", 15)}); err != nil {
		t.Fatal(err)
	}
	info, _ := s.Info("a")
	if got := info.Next.Sub(c.t); got != 15*time.Minute+90*time.Second {
		t.Fatalf("next = +%s, want the interval plus the capped jitter", got)
	}
	s2 := New(c.now, func(time.Duration) time.Duration { return -time.Hour })
	_ = s2.Set([]Item{item("a", 15)})
	info, _ = s2.Info("a")
	if got := info.Next.Sub(c.t); got != 15*time.Minute {
		t.Fatalf("negative jitter gave +%s", got)
	}
}

func TestFirstRunIsOneIntervalAfterEnabling(t *testing.T) {
	c := newClock()
	s := New(c.now, fixedJitter(time.Minute))
	if err := s.Set([]Item{item("a", 30)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Tick(""); ok {
		t.Fatal("a scan started the moment the schedule was enabled")
	}
	c.advance(30 * time.Minute)
	if _, ok := s.Tick(""); ok {
		t.Fatal("started before the jitter elapsed")
	}
	c.advance(time.Minute)
	if key, ok := s.Tick(""); !ok || key != "a" {
		t.Fatalf("tick = %q %v, want a", key, ok)
	}
}

func TestNextRunIsAnIntervalAfterTheEndAndNeverOverlaps(t *testing.T) {
	c := newClock()
	s := New(c.now, fixedJitter(0))
	_ = s.Set([]Item{item("a", 15)})
	c.advance(15 * time.Minute)
	if key, ok := s.Tick(""); !ok || key != "a" {
		t.Fatal("not started when due")
	}
	// A slow scan: the same schedule never starts a second run, however late.
	c.advance(3 * time.Hour)
	if _, ok := s.Tick(""); ok {
		t.Fatal("a scheduled rescan overlapped itself")
	}
	if info, _ := s.Info("a"); !info.Running {
		t.Fatal("info does not show the run")
	}
	s.Finish("a")
	if _, ok := s.Tick(""); ok {
		t.Fatal("a missed cycle ran again at once; cycles are not queued")
	}
	info, _ := s.Info("a")
	if want := c.t.Add(15 * time.Minute); !info.Next.Equal(want) || info.Running {
		t.Fatalf("next %s running %v, want %s", info.Next, info.Running, want)
	}
	c.advance(15 * time.Minute)
	if _, ok := s.Tick(""); !ok {
		t.Fatal("did not run on the next cycle")
	}
}

func TestOnlyOneScheduledRescanAtATime(t *testing.T) {
	c := newClock()
	s := New(c.now, fixedJitter(0))
	_ = s.Set([]Item{item("a", 15), item("b", 15)})
	c.advance(15 * time.Minute)
	first, ok := s.Tick("")
	if !ok {
		t.Fatal("nothing started")
	}
	if second, ok := s.Tick(""); ok {
		t.Fatalf("%s started while %s was running", second, first)
	}
	s.Finish(first)
	other, ok := s.Tick("")
	if !ok || other == first {
		t.Fatalf("the waiting schedule did not run after the first ended: %q %v", other, ok)
	}
	// The waiting schedule was deferred, not skipped.
	if info, _ := s.Info(other); info.Skipped != 0 {
		t.Fatalf("a deferred schedule counted as skipped: %+v", info)
	}
}

func TestEarliestDueRunsFirst(t *testing.T) {
	c := newClock()
	s := New(c.now, fixedJitter(0))
	_ = s.Set([]Item{item("late", 60)})
	c.advance(10 * time.Minute)
	_ = s.Set([]Item{item("late", 60), item("early", 15)})
	c.advance(55 * time.Minute) // both due; "late" was due at +60 min of the start, "early" at +25
	if key, _ := s.Tick(""); key != "early" {
		t.Fatalf("started %q, want the one due first", key)
	}
}

func TestBlockedCycleIsSkippedNotQueued(t *testing.T) {
	c := newClock()
	s := New(c.now, fixedJitter(0))
	_ = s.Set([]Item{item("a", 15)})
	c.advance(14 * time.Minute)
	if key, ok := s.Tick("a manual scan was running"); ok || key != "" {
		t.Fatal("started while blocked")
	}
	if info, _ := s.Info("a"); info.Skipped != 0 {
		t.Fatal("a schedule that was not due counted a skip")
	}
	c.advance(time.Minute) // due, and the manual scan is still running
	if _, ok := s.Tick("a manual scan was running"); ok {
		t.Fatal("started while a manual scan was running")
	}
	info, _ := s.Info("a")
	if info.Skipped != 1 || info.SkipReason != "a manual scan was running" || !info.SkippedAt.Equal(c.t) {
		t.Fatalf("skip not recorded: %+v", info)
	}
	if !info.Next.Equal(c.t.Add(15 * time.Minute)) {
		t.Fatalf("a skipped cycle must wait a whole interval, next = %s", info.Next)
	}
	// The manual scan ends; the skipped cycle does not run on its own.
	c.advance(time.Minute)
	if _, ok := s.Tick(""); ok {
		t.Fatal("the skipped cycle ran afterwards")
	}
	c.advance(14 * time.Minute)
	if _, ok := s.Tick(""); !ok {
		t.Fatal("the next full cycle did not run")
	}
}

func TestSetKeepsTimersAndHandlesRemovalDuringARun(t *testing.T) {
	c := newClock()
	s := New(c.now, fixedJitter(0))
	_ = s.Set([]Item{item("a", 30), item("b", 30)})
	c.advance(20 * time.Minute)
	// Re-sending the same list (the UI does after every set change) must not push the timers back.
	_ = s.Set([]Item{item("a", 30), item("b", 30)})
	c.advance(10 * time.Minute)
	key, ok := s.Tick("")
	if !ok {
		t.Fatal("timer was reset by Set")
	}
	// Remove the running schedule: it finishes, then is gone.
	_ = s.Set([]Item{item("b", 30)})
	if _, ok := s.Info(key); ok {
		t.Fatal("a removed schedule is still reported")
	}
	s.Finish(key)
	if _, ok := s.Info(key); ok {
		t.Fatal("a removed schedule came back after it finished")
	}
	// Changing an interval restarts that schedule's timer.
	_ = s.Set([]Item{item("b", 60)})
	info, _ := s.Info("b")
	if !info.Next.Equal(c.t.Add(time.Hour)) {
		t.Fatalf("changed interval next = %s", info.Next)
	}
	// Disabling everything stops everything.
	_ = s.Set(nil)
	c.advance(48 * time.Hour)
	if _, ok := s.Tick(""); ok {
		t.Fatal("a removed schedule ran")
	}
}

func TestCheckTargetsRefusesPublicAndNames(t *testing.T) {
	good := map[string][]string{
		"10.0.0.0/24":                             {"10.0.0.0/24"},
		"192.168.1.1-50, 172.16.0.0/28":           {"192.168.1.1-50", "172.16.0.0/28"},
		"169.254.1.1, 100.64.0.1, 127.0.0.1":      {"169.254.1.1", "100.64.0.1", "127.0.0.1"},
		"10.0.0.1-10.0.0.9 # lab\n10.0.1.5":       {"10.0.0.1-10.0.0.9", "10.0.1.5"},
		"192.168.0.0/16":                          {"192.168.0.0/16"},
		"172.31.255.250-172.31.255.255, 10.1.1.1": {"172.31.255.250-172.31.255.255", "10.1.1.1"},
	}
	for in, want := range good {
		got, err := CheckTargets(in)
		if err != nil || strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("CheckTargets(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{
		"8.8.8.8",
		"8.8.8.0/24",
		"10.0.0.1, 1.1.1.1",
		"172.15.255.255",          // just below 172.16.0.0/12
		"172.32.0.0",              // just above it
		"10.255.255.250-11.0.0.5", // a range that leaves private space
		"192.169.0.1",
		"100.128.0.1",
	} {
		_, err := CheckTargets(in)
		if !errors.Is(err, ErrPublic) {
			t.Errorf("CheckTargets(%q) err = %v, want a public range refusal", in, err)
		} else if !strings.Contains(err.Error(), "fresh authorization") {
			t.Errorf("refusal for %q does not explain why: %v", in, err)
		}
	}
	for _, in := range []string{"printer.lan", "10.0.0.1, host-1", "", "# nothing", "not an address!", "10.0.0.1-5-7", "2001:db8::1"} {
		if _, err := CheckTargets(in); err == nil {
			t.Errorf("CheckTargets(%q) accepted", in)
		}
	}
	if _, err := CheckTargets("10.0.0.0/8"); err == nil {
		t.Error("a /8 exceeds the scan limit and must be refused")
	}
}

func TestNoticeTextHasOnlyCountsAndAddresses(t *testing.T) {
	title, body := NoticeText([]string{"10.0.0.5"}, 5)
	if title != "ZTG Scanner: 1 new device" || body != "10.0.0.5" {
		t.Fatalf("%q %q", title, body)
	}
	title, body = NoticeText([]string{"10.0.0.5", "10.0.0.6", "10.0.0.7", "10.0.0.8", "10.0.0.9", "10.0.0.10", "10.0.0.11"}, 5)
	if title != "ZTG Scanner: 7 new devices" || body != "10.0.0.5, 10.0.0.6, 10.0.0.7, 10.0.0.8, 10.0.0.9 and 2 more" {
		t.Fatalf("%q %q", title, body)
	}
	// Anything that is not a plain IPv4 address (a device-reported name, markup) is dropped, not escaped.
	title, body = NoticeText([]string{"10.0.0.5", "<b>evil</b>", "printer\nname", "::1", "10.0.0.6%eth0"}, 5)
	if title != "ZTG Scanner: 1 new device" || body != "10.0.0.5" {
		t.Fatalf("%q %q", title, body)
	}
	if strings.ContainsAny(body, "<>\n%") {
		t.Fatalf("unsafe text in notification: %q", body)
	}
}
