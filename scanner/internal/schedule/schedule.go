// Package schedule decides when a scheduled rescan (SC7) may start. It holds no
// scan logic and starts nothing itself: the desktop app asks Tick whether a
// rescan is due, runs it, and reports back with Finish. The clock and the
// jitter source are injected, so tests drive time without sleeping.
//
// Rules, all enforced here and tested in schedule_test.go:
//
//   - The interval is bounded (MinInterval to MaxInterval) and each cycle gets
//     random jitter, so several machines set to the same interval do not all
//     probe a client network at the same second.
//   - At most one scheduled rescan runs at a time, and a cycle never starts
//     while the app says a scan is blocked (a manual scan is running).
//   - A cycle that falls due while something blocks it is skipped, not queued:
//     the next one is a full interval later.
//   - Nothing runs unless the owning app is running and calls Tick.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/targets"
)

const (
	// MinInterval and MaxInterval bound the time between two rescans of one set.
	MinInterval = 15 * time.Minute
	MaxInterval = 24 * time.Hour
	// MaxSchedules bounds how many sets can be scheduled at once.
	MaxSchedules = 20
	// MaxJitter caps the random delay added to every cycle. The delay is also
	// limited to a tenth of the interval, so a 15 minute schedule stays close
	// to 15 minutes.
	MaxJitter = 5 * time.Minute
)

// ValidInterval converts minutes to a duration within the allowed bounds.
func ValidInterval(minutes int) (time.Duration, error) {
	d := time.Duration(minutes) * time.Minute
	if minutes < int(MinInterval/time.Minute) || minutes > int(MaxInterval/time.Minute) {
		return 0, fmt.Errorf("the interval must be between %d minutes and %d hours", int(MinInterval/time.Minute), int(MaxInterval/time.Hour))
	}
	return d, nil
}

// JitterMax is the longest random delay added to a cycle of the given length:
// a tenth of the interval, capped at MaxJitter.
func JitterMax(interval time.Duration) time.Duration {
	j := interval / 10
	if j > MaxJitter {
		j = MaxJitter
	}
	return j
}

// RandomJitter returns a random delay in [0, max]. It is the production jitter
// source; tests inject their own.
func RandomJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(max) + 1))
}

// Key identifies a schedule by its client/site label and set name, ignoring
// case, exactly as saved target sets are identified in the desktop UI.
func Key(site, name string) string {
	return strings.ToLower(site) + "\x00" + strings.ToLower(name)
}

// Specs splits target text the way a scan request does.
func Specs(text string) []string {
	return strings.Fields(strings.NewReplacer(",", " ", ";", " ").Replace(text))
}

// ErrPublic is returned (wrapped) when a schedule's targets include public
// address space.
var ErrPublic = errors.New("scheduled rescans cover private and link-local addresses only")

// CheckTargets validates the targets of a schedule and returns the normalized
// specifications. A schedule is refused when:
//
//   - the text is not a valid target list;
//   - a spec is a host name, because a name can resolve to a different address
//     (public space) later and nothing can be classified without DNS;
//   - any address is outside the scope guard's private, carrier-grade NAT,
//     link-local and loopback space. Scanning public space needs a fresh
//     authorization every time, which a schedule cannot carry, so there is no
//     way to allow it here.
//
// It resolves nothing and sends nothing.
func CheckTargets(text string) ([]string, error) {
	normalized, err := targets.NormalizeList(text)
	if err != nil {
		return nil, err
	}
	specs := Specs(normalized)
	for _, spec := range specs {
		if targets.IsHostnameSpec(spec) {
			return nil, fmt.Errorf("%s is a host name. Scheduled rescans take addresses, ranges and CIDR blocks only, because a name can point somewhere else later", spec)
		}
	}
	if _, err := targets.Expand(context.Background(), specs, targets.Options{AllowPublic: false}); err != nil {
		var scope *targets.ScopeError
		if errors.As(err, &scope) {
			return nil, fmt.Errorf("%w: %s includes public address %s. A scan of public addresses needs fresh authorization each time, so use Scan for it", ErrPublic, scope.Spec, scope.Addr)
		}
		return nil, err
	}
	return specs, nil
}

// Item is one schedule as the Scheduler sees it.
type Item struct {
	Key      string
	Interval time.Duration
}

// Info is the Scheduler's view of one schedule.
type Info struct {
	Next       time.Time
	Running    bool
	Skipped    int
	SkipReason string
	SkippedAt  time.Time
}

type job struct {
	interval   time.Duration
	next       time.Time
	skipped    int
	skipReason string
	skippedAt  time.Time
	removed    bool
}

// Scheduler tracks when each schedule is next due. It is safe for concurrent
// use.
type Scheduler struct {
	mu     sync.Mutex
	now    func() time.Time
	jitter func(max time.Duration) time.Duration
	jobs   map[string]*job
	active string // key of the running scheduled rescan, if any
}

// New returns a Scheduler. Nil arguments use the real clock and random jitter.
func New(now func() time.Time, jitter func(max time.Duration) time.Duration) *Scheduler {
	if now == nil {
		now = time.Now
	}
	if jitter == nil {
		jitter = RandomJitter
	}
	return &Scheduler{now: now, jitter: jitter, jobs: map[string]*job{}}
}

// nextAfter is one interval from t plus jitter, never earlier than the interval.
func (s *Scheduler) nextAfter(t time.Time, interval time.Duration) time.Time {
	j := s.jitter(JitterMax(interval))
	if j < 0 {
		j = 0
	}
	if m := JitterMax(interval); j > m {
		j = m
	}
	return t.Add(interval + j)
}

// Set replaces the schedules. A schedule that already exists with the same
// interval keeps its next run; a new or changed one first runs one interval
// (plus jitter) from now, so enabling a schedule or launching the app never
// starts a scan at once. A removed schedule that is mid-run is dropped when it
// finishes.
func (s *Scheduler) Set(items []Item) error {
	if len(items) > MaxSchedules {
		return fmt.Errorf("you can schedule up to %d target sets", MaxSchedules)
	}
	seen := map[string]bool{}
	for _, it := range items {
		if _, err := ValidInterval(int(it.Interval / time.Minute)); err != nil || it.Interval%time.Minute != 0 {
			return fmt.Errorf("%s: the interval must be a whole number of minutes between %d minutes and %d hours", it.Key, int(MinInterval/time.Minute), int(MaxInterval/time.Hour))
		}
		if it.Key == "" || seen[it.Key] {
			return errors.New("schedules must have distinct names")
		}
		seen[it.Key] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, j := range s.jobs {
		if !seen[key] {
			if key == s.active {
				j.removed = true
			} else {
				delete(s.jobs, key)
			}
		}
	}
	for _, it := range items {
		if j, ok := s.jobs[it.Key]; ok {
			j.removed = false
			if j.interval != it.Interval {
				j.interval = it.Interval
				j.next = s.nextAfter(now, it.Interval)
			}
			continue
		}
		s.jobs[it.Key] = &job{interval: it.Interval, next: s.nextAfter(now, it.Interval)}
	}
	return nil
}

// Tick asks what to start now. blocked is empty when scanning is allowed, or a
// short reason (a manual scan is running, battery saver is on). While blocked,
// every due schedule skips this cycle: its next run moves one interval (plus
// jitter) from now and the skip is recorded. Otherwise, if no scheduled rescan
// is running, Tick marks the earliest due schedule as running and returns its
// key; the caller must call Finish when the rescan ends. A schedule that falls
// due while another scheduled rescan runs waits for it; it is not skipped.
func (s *Scheduler) Tick(blocked string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if blocked != "" {
		for key, j := range s.jobs {
			if key == s.active || j.removed || j.next.After(now) {
				continue
			}
			j.skipped++
			j.skipReason = blocked
			j.skippedAt = now
			j.next = s.nextAfter(now, j.interval)
		}
		return "", false
	}
	if s.active != "" {
		return "", false
	}
	var due []string
	for key, j := range s.jobs {
		if !j.removed && !j.next.After(now) {
			due = append(due, key)
		}
	}
	if len(due) == 0 {
		return "", false
	}
	sort.Slice(due, func(a, b int) bool {
		ja, jb := s.jobs[due[a]], s.jobs[due[b]]
		if !ja.next.Equal(jb.next) {
			return ja.next.Before(jb.next)
		}
		return due[a] < due[b]
	})
	s.active = due[0]
	return s.active, true
}

// Finish records the end of a rescan started by Tick. The next run is one
// interval (plus jitter) after the end, so rescans never pile up behind a slow
// one.
func (s *Scheduler) Finish(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key != s.active {
		return
	}
	s.active = ""
	j := s.jobs[key]
	if j == nil {
		return
	}
	if j.removed {
		delete(s.jobs, key)
		return
	}
	j.next = s.nextAfter(s.now(), j.interval)
}

// Info reports one schedule's state.
func (s *Scheduler) Info(key string) (Info, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[key]
	if !ok || j.removed {
		return Info{}, false
	}
	return Info{Next: j.next, Running: key == s.active, Skipped: j.skipped, SkipReason: j.skipReason, SkippedAt: j.skippedAt}, true
}

// NoticeText builds the text of a new-device notification: counts and
// addresses only. Addresses are re-parsed and printed canonically, so nothing
// else (and nothing a device reported) can reach the text. At most
// maxListed addresses are named.
func NoticeText(addresses []string, maxListed int) (title, body string) {
	var clean []string
	for _, a := range addresses {
		if ip, err := netip.ParseAddr(a); err == nil && ip.Is4() {
			clean = append(clean, ip.String())
		}
	}
	n := len(clean)
	if n == 1 {
		title = "ZTG Scanner: 1 new device"
	} else {
		title = fmt.Sprintf("ZTG Scanner: %d new devices", n)
	}
	if maxListed < 1 {
		maxListed = 1
	}
	shown := clean
	if len(shown) > maxListed {
		shown = shown[:maxListed]
	}
	body = strings.Join(shown, ", ")
	if n > len(shown) {
		body += fmt.Sprintf(" and %d more", n-len(shown))
	}
	return title, body
}
