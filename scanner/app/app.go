// Package main is the ZTG Scanner desktop app: a Wails shell around the
// same scan engine the CLI uses. This file holds the logic bound to the
// UI; it has no Wails imports so it can be tested on any platform.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/inventory"
	"github.com/zerotrustgarage/tools/scanner/internal/probe"
	"github.com/zerotrustgarage/tools/scanner/internal/report"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
	"github.com/zerotrustgarage/tools/scanner/internal/targets"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

// Event names the UI subscribes to.
const (
	EventHost     = "scan:host"
	EventProgress = "scan:progress"
	EventDone     = "scan:done"
)

// Platform is what the app needs from the desktop shell. The Wails
// implementation lives in shell_windows.go; tests use a fake.
type Platform interface {
	Emit(event string, data any)
	SaveFile(defaultName, filterName, pattern string) (string, error)
	OpenURL(url string)
	OpenFile(title, filterName, pattern string) (string, error)
	Launch(action, ip string) (string, error)
}

// App is bound to the UI: its exported methods are callable from JavaScript.
type App struct {
	ctx      context.Context
	platform Platform

	mu            sync.Mutex
	running       bool
	cancel        context.CancelFunc
	hosts         []scan.Host
	summary       *scan.Summary
	lastReq       []string
	allowPub      bool
	site          string
	annotations   map[string]scan.Annotation
	previous      []scan.Host
	wake          func(string, string) error
	revision      uint64
	savedRevision uint64
	filename      string
}

// NewApp returns an App; startup supplies the platform.
func NewApp() *App {
	return &App{ctx: context.Background(), annotations: map[string]scan.Annotation{}, wake: sendWake}
}

func (a *App) setPlatform(ctx context.Context, p Platform) {
	a.ctx = ctx
	a.platform = p
}

// Version reports the build version.
func (a *App) Version() string { return version }

// Suggestion is a network the user is attached to, offered as a target.
type Suggestion struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	Range     string `json:"range"`
}

// Suggestions lists the IPv4 networks of the machine's active interfaces.
// Networks wider than a /16 are offered as the /24 around the address, so
// the default scan stays reasonable.
func (a *App) Suggestions() []Suggestion {
	var out []Suggestion
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok || !ip.Unmap().Is4() {
				continue
			}
			ip = ip.Unmap()
			ones, _ := ipn.Mask.Size()
			if ones < 16 {
				ones = 24
			}
			out = append(out, Suggestion{
				Interface: ifc.Name,
				Address:   ip.String(),
				Range:     netip.PrefixFrom(ip, ones).Masked().String(),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Private ranges first: those are what a technician scans.
		pi := targets.IsInternal(netip.MustParseAddr(out[i].Address))
		pj := targets.IsInternal(netip.MustParseAddr(out[j].Address))
		return pi && !pj
	})
	return out
}

// Defaults is the initial state of the settings panel.
type Defaults struct {
	Ports       string      `json:"ports"`
	Probes      scan.Probes `json:"probes"`
	Concurrency int         `json:"concurrency"`
	TimeoutMs   int         `json:"timeout_ms"`
	MaxHosts    int         `json:"max_hosts"`
	SNMPAuth    []string    `json:"snmp_auth_protocols"`
	SNMPPriv    []string    `json:"snmp_priv_protocols"`
}

// Defaults returns the engine defaults.
func (a *App) Defaults() Defaults {
	p := make([]string, len(scan.DefaultPorts))
	for i, n := range scan.DefaultPorts {
		p[i] = strconv.Itoa(n)
	}
	return Defaults{
		Ports:       strings.Join(p, ","),
		Probes:      scan.DefaultProbes,
		Concurrency: 128,
		TimeoutMs:   800,
		MaxHosts:    targets.DefaultMaxHosts,
		SNMPAuth:    probe.AuthProtocols,
		SNMPPriv:    probe.PrivProtocols,
	}
}

// ScanRequest is what the UI sends to start a scan.
type ScanRequest struct {
	Targets     string      `json:"targets"`
	Ports       string      `json:"ports"`
	Probes      scan.Probes `json:"probes"`
	Concurrency int         `json:"concurrency"`
	TimeoutMs   int         `json:"timeout_ms"`
	// AllowPublic must be set by the UI only after the user has confirmed
	// they are authorized to scan public addresses.
	AllowPublic bool `json:"allow_public"`
	// SNMP carries SNMPv3 credentials when probes.snmp is set. They are
	// used for this scan only and never stored.
	SNMP *SNMPRequest `json:"snmp,omitempty"`
}

// SNMPRequest is the SNMPv3 part of a scan request from the UI.
type SNMPRequest struct {
	Username       string `json:"username"`
	AuthProtocol   string `json:"auth_protocol"`
	AuthPassphrase string `json:"auth_passphrase"`
	PrivProtocol   string `json:"priv_protocol"`
	PrivPassphrase string `json:"priv_passphrase"`
}

// Progress is the payload of EventProgress.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
	Up    int `json:"up"`
}

// StartScan validates the request and starts the scan in the background.
// It returns the number of addresses to scan.
func (a *App) StartScan(req ScanRequest) (int, error) {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return 0, errors.New("a scan is already running")
	}
	a.mu.Unlock()

	specs := strings.Fields(strings.NewReplacer(",", " ", ";", " ").Replace(req.Targets))
	if len(specs) == 0 {
		return 0, errors.New("enter at least one address, range or network")
	}
	addrs, err := targets.Expand(a.ctx, specs, targets.Options{AllowPublic: req.AllowPublic})
	if err != nil {
		return 0, err
	}
	if len(addrs) == 0 {
		return 0, errors.New("the target list is empty")
	}
	cfg := scan.Config{
		Targets:        addrs,
		Probes:         req.Probes,
		Concurrency:    clamp(req.Concurrency, 1, 512, 128),
		ConnectTimeout: time.Duration(clamp(req.TimeoutMs, 100, 10000, 800)) * time.Millisecond,
	}
	if req.Probes.SNMP {
		if req.SNMP == nil {
			return 0, errors.New("SNMPv3 is enabled but no credentials were entered")
		}
		cred := probe.SNMPCredentials{
			Username:       strings.TrimSpace(req.SNMP.Username),
			AuthProtocol:   req.SNMP.AuthProtocol,
			AuthPassphrase: req.SNMP.AuthPassphrase,
			PrivProtocol:   req.SNMP.PrivProtocol,
			PrivPassphrase: req.SNMP.PrivPassphrase,
		}
		if err := cred.Validate(); err != nil {
			return 0, err
		}
		cfg.SNMP = &cred
	}
	if strings.TrimSpace(req.Ports) != "" {
		if cfg.Ports, err = scan.ParsePorts(req.Ports); err != nil {
			return 0, err
		}
	}

	ctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	if a.running { // lost a race with another StartScan
		a.mu.Unlock()
		cancel()
		return 0, errors.New("a scan is already running")
	}
	a.running = true
	a.revision++
	a.cancel = cancel
	a.previous = a.hosts
	a.hosts = nil
	// Keep accurate scope and start time available while observations stream in.
	// scan.Run replaces this provisional summary with its final result.
	a.summary = &scan.Summary{Started: time.Now(), Total: len(addrs)}
	a.lastReq = specs
	a.allowPub = req.AllowPublic
	a.mu.Unlock()

	go func() {
		defer cancel()
		obs := &uiObserver{app: a, lastEmit: time.Now()}
		sum := scan.Run(ctx, cfg, obs)
		obs.flushProgress(true)
		a.mu.Lock()
		inventory.ApplyAnnotations(a.previous, a.hosts, a.annotations)
		a.previous = nil
		a.running = false
		a.cancel = nil
		a.summary = &sum
		a.mu.Unlock()
		a.emit(EventDone, sum)
	}()
	return len(addrs), nil
}

// StopScan cancels the running scan; results so far are kept.
func (a *App) StopScan() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
}

// Snapshot is the full current result set, for redrawing the grid.
type Snapshot struct {
	Running bool          `json:"running"`
	Hosts   []scan.Host   `json:"hosts"`
	Summary *scan.Summary `json:"summary,omitempty"`
	Site    string        `json:"site"`
	Targets []string      `json:"targets"`
	File    string        `json:"file"`
	Dirty   bool          `json:"dirty"`
}

// Results returns every host from the current or last scan. Unresponsive
// addresses are included only when all is true.
func (a *App) Results(all bool) Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	hosts := make([]scan.Host, 0, len(a.hosts))
	for _, h := range a.hosts {
		if all || h.Status == "up" {
			hosts = append(hosts, h)
		}
	}
	report.SortHosts(hosts)
	return Snapshot{Running: a.running, Hosts: hosts, Summary: a.summary, Site: a.site, Targets: append([]string(nil), a.lastReq...), File: a.filename, Dirty: a.revision != a.savedRevision}
}

// Export writes the results to a file the user picks. HTML always includes
// every recorded address; CSV and JSON retain their all argument semantics.
// It returns the path written, or "" if the user canceled.
func (a *App) Export(format string, all bool) (string, error) {
	if a.platform == nil {
		return "", errors.New("export is not available")
	}
	// Take the complete document while holding the inventory lock, before the
	// native dialog can yield to a scan finishing or another inventory loading.
	a.mu.Lock()
	includeAll := all || format == "html"
	doc := a.documentLocked(includeAll)
	running := a.running
	a.mu.Unlock()
	if len(doc.Hosts) == 0 {
		return "", errors.New("there are no results to export")
	}
	if format == "html" && running {
		// A snapshot taken during a scan is useful, but must describe itself as
		// partial even if the scan finishes while the chooser is open.
		doc.Summary.Canceled = false
		doc.Summary.Finished = time.Time{}
	}
	stamp := time.Now().Format("2006-01-02-1504")
	var name, filter, pattern string
	switch format {
	case "csv":
		name, filter, pattern = "ztg-scan-"+stamp+".csv", "CSV (*.csv)", "*.csv"
	case "json":
		name, filter, pattern = "ztg-scan-"+stamp+".json", "JSON (*.json)", "*.json"
	case "html":
		name, filter, pattern = "ztg-scan-"+stamp+".html", "HTML (*.html)", "*.html"
	default:
		return "", fmt.Errorf("unknown export format %q", format)
	}
	path, err := a.platform.SaveFile(name, filter, pattern)
	if err != nil || path == "" {
		return "", err
	}
	err = report.WriteFile(path, func(w io.Writer) error {
		if format == "csv" {
			return report.CSV(w, doc.Hosts)
		}
		if format == "json" {
			return report.JSONBounded(w, doc, report.MaxDocumentBytes)
		}
		return report.HTML(w, doc)
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// ExportTable writes the grid as the user sees it (visible columns,
// filtered and sorted rows) to a CSV file the user picks. The report
// package neutralizes formula injection in every cell.
func (a *App) ExportTable(headers []string, rows [][]string) (string, error) {
	if a.platform == nil {
		return "", errors.New("export is not available")
	}
	if len(rows) == 0 {
		return "", errors.New("there are no rows in this view to export")
	}
	// Validate before showing a dialog, so a bad request never creates a file.
	if err := report.CSVTable(io.Discard, headers, rows); err != nil {
		return "", err
	}
	name := "ztg-scan-view-" + time.Now().Format("2006-01-02-1504") + ".csv"
	path, err := a.platform.SaveFile(name, "CSV (*.csv)", "*.csv")
	if err != nil || path == "" {
		return "", err
	}
	if err := report.WriteFile(path, func(w io.Writer) error { return report.CSVTable(w, headers, rows) }); err != nil {
		return "", err
	}
	return path, nil
}

// OpenWeb opens http(s)://ip:port/ in the default browser. Only a port
// this scan found open on a host it found is accepted, so the UI cannot
// be used to open arbitrary URLs.
func (a *App) OpenWeb(ip string, port int) error {
	if a.platform == nil {
		return errors.New("not available")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, h := range a.hosts {
		if h.IP != ip {
			continue
		}
		for _, s := range h.Services {
			if s.Port != port {
				continue
			}
			scheme := "http"
			if s.TLS != nil {
				scheme = "https"
			}
			a.platform.OpenURL(fmt.Sprintf("%s://%s/", scheme, net.JoinHostPort(ip, strconv.Itoa(port))))
			return nil
		}
	}
	return errors.New("that host and port are not in the results")
}

func (a *App) emit(event string, data any) {
	if a.platform != nil {
		a.platform.Emit(event, data)
	}
}

// uiObserver forwards engine results to the UI. Hosts that answered are
// sent as they finish; progress is throttled to ten updates a second.
type uiObserver struct {
	app      *App
	done     int
	total    int
	up       int
	lastEmit time.Time
}

func (o *uiObserver) Host(h scan.Host) {
	o.app.mu.Lock()
	h.Annotation = o.app.annotations[inventory.AnnotationKey(h)]
	o.app.hosts = append(o.app.hosts, h)
	o.app.mu.Unlock()
	if h.Status == "up" {
		o.up++
		o.app.emit(EventHost, h)
	}
}

func (o *uiObserver) Progress(done, total int) {
	o.done, o.total = done, total
	o.flushProgress(done == total)
}

func (o *uiObserver) flushProgress(force bool) {
	if !force && time.Since(o.lastEmit) < 100*time.Millisecond {
		return
	}
	o.lastEmit = time.Now()
	o.app.emit(EventProgress, Progress{Done: o.done, Total: o.total, Up: o.up})
}

func clamp(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func init() { probe.UserAgent = "ZTG-Scanner-Desktop/" + version + " (+https://ztg.sh)" }
