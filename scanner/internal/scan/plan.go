package scan

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/probe"
)

// Scan modes. A mode is chosen deliberately by the user; nothing selects
// gentle mode automatically.
const (
	ModeDefault = "default"
	ModeGentle  = "gentle"
)

// Gentle mode settings. This is the single definition of the gentle plan:
// the CLI flag, the Desktop option, the help text and the UI summary all
// read these values, so they cannot drift apart.
const (
	GentleConcurrency     = 4                      // hosts in flight
	GentlePortConcurrency = 2                      // connections per host
	GentleProbeInterval   = 100 * time.Millisecond // one probe start per interval: 10 per second, all hosts together
	GentleConnectTimeout  = 1500 * time.Millisecond
	GentlePingAttempts    = 1
	GentleMaxPorts        = 16
)

// GentlePorts is the reduced port list: the services that identify
// printers, file servers, web interfaces and remote-admin endpoints.
var GentlePorts = []int{22, 80, 443, 445, 515, 631, 8080, 9100}

// GentleProbes is the reduced probe set: ICMP, ARP and a short list of TCP
// connects, plus reverse DNS (which asks the resolver, not the device).
// Banner grabs, NetBIOS, mDNS, SSDP, WS-Discovery, LLMNR, SMB2 negotiate, favicon
// reads and SNMP are off. The list is spelled out field by field so a probe added
// later is off in gentle mode until someone decides otherwise.
var GentleProbes = Probes{Ports: true, ReverseDNS: true}

// GentleTradeoff states what gentle mode does and does not promise. It is
// shown verbatim in the CLI help and the Desktop settings.
const GentleTradeoff = "Gentle mode lowers the load a scan puts on a network and skips the more intrusive probes. " +
	"It is slower and identifies less: no service banners, NetBIOS, mDNS, UPnP, WS-Discovery, LLMNR, SMB or favicon probes, no SNMP, and only a few ports. " +
	"It reduces risk but does not remove it: any scan can upset a very fragile device, so get approval and watch the network."

// Pacer spaces out probe starts. Wait blocks until the caller may start one
// probe, or returns the context's error if it is canceled first.
type Pacer interface {
	Wait(ctx context.Context) error
}

// RateLimiter lets one probe start per interval, across all goroutines
// that share it. The clock and sleep are injectable so tests need no real
// time.
type RateLimiter struct {
	interval time.Duration
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
	mu       sync.Mutex
	next     time.Time
}

// NewRateLimiter returns a limiter on the real clock.
func NewRateLimiter(interval time.Duration) *RateLimiter {
	return NewRateLimiterClock(interval, time.Now, sleepCtx)
}

// NewRateLimiterClock returns a limiter with an injected clock and sleep.
func NewRateLimiterClock(interval time.Duration, now func() time.Time, sleep func(context.Context, time.Duration) error) *RateLimiter {
	return &RateLimiter{interval: interval, now: now, sleep: sleep}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Wait reserves the next free slot and sleeps until it arrives.
func (r *RateLimiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	at := r.now()
	if r.next.After(at) {
		at = r.next
	}
	r.next = at.Add(r.interval)
	wait := at.Sub(r.now())
	r.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	return r.sleep(ctx, wait)
}

// PlanRequest is the mode-independent input both front ends build from
// their own flags or form fields. Zero values mean "not set".
type PlanRequest struct {
	Mode           string
	Targets        []netip.Addr
	Ports          []int
	Probes         Probes
	Concurrency    int
	ConnectTimeout time.Duration
	SNMP           *probe.SNMPCredentials
}

// Plan is the fully resolved scan: every number the engine will use. The
// CLI and the Desktop app both obtain it from BuildPlan, so the same
// request always yields the same plan.
type Plan struct {
	Mode            string
	Targets         []netip.Addr
	Ports           []int
	Probes          Probes
	Concurrency     int
	PortConcurrency int
	PingAttempts    int
	ConnectTimeout  time.Duration
	ProbeInterval   time.Duration // 0 means unpaced
	SNMP            *probe.SNMPCredentials
}

// ValidMode reports whether m names a mode ("" means default).
func ValidMode(m string) error {
	switch m {
	case "", ModeDefault, ModeGentle:
		return nil
	}
	return fmt.Errorf("unknown scan mode %q (use %q or %q)", m, ModeDefault, ModeGentle)
}

// BuildPlan resolves a request. In default mode the request passes through
// untouched, so the default plan is exactly what it was before modes
// existed. In gentle mode the engine settings are fixed. A request may ask
// for fewer hosts in parallel, a subset of GentlePorts, fewer probes, and
// any positive connect timeout; anything beyond that is rejected rather than
// silently clamped. The timeout is free because it does not raise the rate
// or concurrency: a longer one waits longer for slow devices, a shorter one
// gives up sooner.
func BuildPlan(r PlanRequest) (Plan, error) {
	if err := ValidMode(r.Mode); err != nil {
		return Plan{}, err
	}
	if r.Mode != ModeGentle {
		return Plan{
			Mode: ModeDefault, Targets: r.Targets, Ports: r.Ports, Probes: r.Probes,
			Concurrency: r.Concurrency, ConnectTimeout: r.ConnectTimeout, SNMP: r.SNMP,
		}, nil
	}
	p := Plan{
		Mode:            ModeGentle,
		Targets:         r.Targets,
		Ports:           GentlePorts,
		Probes:          Probes{Ports: r.Probes.Ports && GentleProbes.Ports, ReverseDNS: r.Probes.ReverseDNS && GentleProbes.ReverseDNS},
		Concurrency:     GentleConcurrency,
		PortConcurrency: GentlePortConcurrency,
		PingAttempts:    GentlePingAttempts,
		ConnectTimeout:  GentleConnectTimeout,
		ProbeInterval:   GentleProbeInterval,
	}
	if r.Probes.SNMP || r.SNMP != nil {
		return Plan{}, errors.New("gentle mode does not use SNMP; choose the default mode to query devices with SNMPv3")
	}
	if r.Concurrency < 0 {
		return Plan{}, fmt.Errorf("hosts in parallel must not be negative, got %d", r.Concurrency)
	}
	if r.Concurrency > GentleConcurrency {
		return Plan{}, fmt.Errorf("gentle mode scans at most %d hosts in parallel; %d was requested", GentleConcurrency, r.Concurrency)
	}
	if r.Concurrency > 0 {
		p.Concurrency = r.Concurrency
	}
	if r.ConnectTimeout < 0 {
		return Plan{}, fmt.Errorf("connect timeout must not be negative, got %s", r.ConnectTimeout)
	}
	if r.ConnectTimeout > 0 {
		p.ConnectTimeout = r.ConnectTimeout
	}
	if r.Ports != nil {
		if len(r.Ports) == 0 {
			return Plan{}, errors.New("no ports given")
		}
		if len(r.Ports) > GentleMaxPorts {
			return Plan{}, fmt.Errorf("gentle mode probes at most %d ports; %d were requested", GentleMaxPorts, len(r.Ports))
		}
		allowed := map[int]bool{}
		for _, g := range GentlePorts {
			allowed[g] = true
		}
		for _, port := range r.Ports {
			if !allowed[port] {
				return Plan{}, fmt.Errorf("gentle mode probes only ports %s; port %d is not one of them", portList(GentlePorts), port)
			}
		}
		p.Ports = r.Ports
	}
	return p, nil
}

// Config converts the plan into engine settings. Gentle plans get a real
// rate limiter; tests that need a fake clock set Config.Pacer afterwards.
func (p Plan) Config() Config {
	c := Config{
		Targets:         p.Targets,
		Ports:           p.Ports,
		Probes:          p.Probes,
		Concurrency:     p.Concurrency,
		PortConcurrency: p.PortConcurrency,
		PingAttempts:    p.PingAttempts,
		ConnectTimeout:  p.ConnectTimeout,
		SNMP:            p.SNMP,
	}
	if p.Mode == ModeGentle {
		c.Mode = ModeGentle
	}
	if p.ProbeInterval > 0 {
		c.Pacer = NewRateLimiter(p.ProbeInterval)
	}
	return c
}

// ModeInfo describes a mode for the UI and the CLI help.
type ModeInfo struct {
	Mode            string `json:"mode"`
	Label           string `json:"label"`
	Summary         string `json:"summary"`
	Tradeoff        string `json:"tradeoff,omitempty"`
	Concurrency     int    `json:"concurrency"`
	PortConcurrency int    `json:"port_concurrency"`
	ProbesPerSecond int    `json:"probes_per_second"` // 0 means unpaced
	ConnectTimeout  int    `json:"timeout_ms"`
	Ports           []int  `json:"ports"`
	Probes          Probes `json:"probes"`
}

// GentleInfo describes the gentle plan from the same constants BuildPlan uses.
func GentleInfo() ModeInfo {
	return ModeInfo{
		Mode:            ModeGentle,
		Label:           "Gentle",
		Summary:         GentleSummary(),
		Tradeoff:        GentleTradeoff,
		Concurrency:     GentleConcurrency,
		PortConcurrency: GentlePortConcurrency,
		ProbesPerSecond: int(time.Second / GentleProbeInterval),
		ConnectTimeout:  int(GentleConnectTimeout / time.Millisecond),
		Ports:           append([]int(nil), GentlePorts...),
		Probes:          GentleProbes,
	}
}

// GentleSummary is the one-paragraph list of the gentle settings.
func GentleSummary() string {
	ports := portList(GentlePorts)
	return fmt.Sprintf("%d hosts at a time, %d connections per host, at most %d probe starts per second in total, "+
		"%d ping per host, %s connect timeout, TCP ports %s. Skips service banners, NetBIOS, mDNS, UPnP, WS-Discovery, LLMNR, SMB, favicon and SNMP probes.",
		GentleConcurrency, GentlePortConcurrency, int(time.Second/GentleProbeInterval), GentlePingAttempts,
		GentleConnectTimeout, ports)
}

func portList(ports []int) string {
	s := make([]string, len(ports))
	for i, n := range ports {
		s[i] = fmt.Sprint(n)
	}
	return strings.Join(s, ",")
}
