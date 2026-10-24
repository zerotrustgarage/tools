// Command ztg-scanner scans networks you specify and reports every device
// it finds: address, name, MAC, manufacturer, type, open services and
// findings worth a technician's attention.
//
//	ztg-scanner scan 192.168.1.0/24
//	ztg-scanner scan --json -o site.json 10.0.0.1-50 10.0.5.0/24
//	ztg-scanner vendor 00:1b:21:12:34:56
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/oui"
	"github.com/zerotrustgarage/tools/scanner/internal/probe"
	"github.com/zerotrustgarage/tools/scanner/internal/report"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
	"github.com/zerotrustgarage/tools/scanner/internal/targets"
	"github.com/zerotrustgarage/tools/scanner/internal/update"
	"golang.org/x/term"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	probe.UserAgent = "ZTG-Scanner/" + version + " (+https://ztg.sh)"
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "check-updates":
		err = update.Command(context.Background(), "scanner", version, os.Args[2:], os.Stdout, os.Stderr)
	case "scan":
		err = cmdScan(os.Args[2:])
	case "report":
		err = cmdReport(os.Args[2:], os.Stdout)
	case "vendor":
		err = cmdVendor(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("ztg-scanner", version)
	case "help", "--help", "-h":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ztg-scanner:", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `ZTG Scanner: network discovery and device inventory.

Usage:
  ztg-scanner scan [flags] <target>...   scan addresses, ranges, CIDR blocks or hostnames
  ztg-scanner report [flags] <scan.json> render a saved scan as HTML, offline
  ztg-scanner vendor <mac>...            look up the manufacturer of a MAC address
  ztg-scanner version
  ztg-scanner check-updates [--json]    check published releases on request

Targets: 192.168.1.0/24  10.0.0.1-50  10.0.0.1-10.0.1.20  10.0.0.7  printer.lan
Only private, CGNAT, link-local and loopback space is scanned unless --allow-public is given.

Run "ztg-scanner scan -h" or "ztg-scanner report -h" for command flags.
`)
}

type collector struct {
	hosts    []scan.Host
	progress bool
	start    time.Time
}

func (c *collector) Host(h scan.Host) { c.hosts = append(c.hosts, h) }

func (c *collector) Progress(done, total int) {
	if c.progress {
		fmt.Fprintf(os.Stderr, "\r  scanned %d/%d  (%s)   ", done, total, time.Since(c.start).Round(time.Second))
	}
}

func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	var (
		jsonOut     = fs.Bool("json", false, "write JSON instead of a table")
		csvOut      = fs.Bool("csv", false, "write CSV instead of a table")
		htmlOut     = fs.Bool("html", false, "write a self-contained HTML report")
		site        = fs.String("site", "", "site or client label (included in JSON and HTML)")
		outFile     = fs.String("o", "", "write results to this file instead of standard output")
		all         = fs.Bool("all", false, "include hosts that did not answer (table and CSV)")
		portsFlag   = fs.String("ports", "", "comma-separated TCP ports and ranges to probe (default: built-in list of 40)")
		noPorts     = fs.Bool("no-ports", false, "discovery only: skip port and service probes")
		noBanners   = fs.Bool("no-banners", false, "list open ports without identifying services")
		noNames     = fs.Bool("no-names", false, "skip reverse DNS, NetBIOS and mDNS name queries")
		noSSDP      = fs.Bool("no-ssdp", false, "skip the UPnP/SSDP multicast discovery")
		concurrency = fs.Int("concurrency", 128, "hosts scanned in parallel")
		timeout     = fs.Duration("timeout", 800*time.Millisecond, "TCP connect timeout")
		maxHosts    = fs.Int("max-hosts", targets.DefaultMaxHosts, "refuse target lists larger than this")
		allowPublic = fs.Bool("allow-public", false, "permit public addresses; you must be authorized to scan them")
		mode        = fs.String("mode", scan.ModeDefault, "scan mode: default, or gentle (lower rate, fewer probes; see below)")
		quiet       = fs.Bool("q", false, "no progress output")
		snmpUser    = fs.String("snmp-user", "", "enable SNMPv3 (authPriv, read-only) with this username; passphrases are prompted for or read from "+envAuth+" and "+envPriv)
		snmpAuth    = fs.String("snmp-auth", "SHA-256", "SNMPv3 authentication: "+strings.Join(probe.AuthProtocols, ", "))
		snmpPriv    = fs.String("snmp-priv", "AES", "SNMPv3 privacy: "+strings.Join(probe.PrivProtocols, ", "))
	)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: ztg-scanner scan [flags] <target>...")
		fs.PrintDefaults()
		fmt.Fprint(fs.Output(), gentleHelp())
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if err := scan.ValidMode(*mode); err != nil {
		return err
	}
	formats := 0
	for _, selected := range []bool{*jsonOut, *csvOut, *htmlOut} {
		if selected {
			formats++
		}
	}
	if formats > 1 {
		return errors.New("choose only one of --json, --csv and --html")
	}
	validatedSite, siteErr := validateSiteLabel(*site)
	if siteErr != nil {
		return siteErr
	}
	*site = validatedSite
	if fs.NArg() == 0 {
		fs.Usage()
		return errors.New("no targets given")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	addrs, err := targets.Expand(ctx, fs.Args(), targets.Options{MaxHosts: *maxHosts, AllowPublic: *allowPublic})
	if err != nil {
		return err
	}
	if len(addrs) == 0 {
		return errors.New("target list is empty")
	}
	if *allowPublic {
		fmt.Fprintln(os.Stderr, "warning: --allow-public set; scan only networks you are authorized to test.")
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	req, err := planRequest(planFlags{
		mode: *mode, concurrency: *concurrency, concurrencySet: set["concurrency"],
		timeout: *timeout, timeoutSet: set["timeout"], ports: *portsFlag,
		noPorts: *noPorts, noBanners: *noBanners, noNames: *noNames, noSSDP: *noSSDP,
	}, addrs)
	if err != nil {
		return err
	}
	if *snmpUser != "" {
		if *mode == scan.ModeGentle {
			return errors.New("gentle mode does not use SNMP; choose the default mode to query devices with SNMPv3")
		}
		cred, err := snmpCredentials(*snmpUser, *snmpAuth, *snmpPriv)
		if err != nil {
			return err
		}
		req.Probes.SNMP = true
		req.SNMP = &cred
	}
	plan, err := scan.BuildPlan(req)
	if err != nil {
		return err
	}
	cfg := plan.Config()
	if plan.Mode == scan.ModeGentle && !*quiet {
		fmt.Fprintln(os.Stderr, "Gentle mode: "+scan.GentleSummary())
		fmt.Fprintln(os.Stderr, scan.GentleTradeoff)
	}

	col := &collector{progress: !*quiet, start: time.Now()}
	if !*quiet {
		fmt.Fprintf(os.Stderr, "ZTG Scanner %s: scanning %d address(es)\n", version, len(addrs))
	}
	sum := scan.Run(ctx, cfg, col)
	if !*quiet {
		fmt.Fprintf(os.Stderr, "\r  done: %d of %d up in %s via %s\n", sum.Up, sum.Total, sum.Finished.Sub(sum.Started).Round(100*time.Millisecond), strings.Join(sum.Methods, ", "))
		for _, w := range sum.Warnings {
			fmt.Fprintln(os.Stderr, "  warning:", w)
		}
		if sum.Canceled {
			fmt.Fprintln(os.Stderr, "  canceled: results are partial")
		}
	}
	report.SortHosts(col.hosts)

	doc := report.Document{Tool: "ztg-scanner", Version: version, Targets: fs.Args(), Summary: sum, Hosts: col.hosts, Site: *site}
	write := func(w io.Writer) error {
		switch {
		case *jsonOut:
			return report.JSON(w, doc)
		case *htmlOut:
			return report.HTML(w, doc)
		case *csvOut:
			hosts := col.hosts
			if !*all {
				hosts = upOnly(hosts)
			}
			return report.CSV(w, hosts)
		default:
			return report.Table(w, col.hosts, *all)
		}
	}
	if *outFile != "" {
		return report.WriteFile(*outFile, write)
	}
	return write(os.Stdout)
}

// planFlags are the scan flags that shape the plan.
type planFlags struct {
	mode                                string
	concurrency                         int
	concurrencySet                      bool
	timeout                             time.Duration
	timeoutSet                          bool
	ports                               string
	noPorts, noBanners, noNames, noSSDP bool
}

// planRequest turns flags into the shared scan.PlanRequest. In gentle mode
// only values the user actually set are passed on, so the mode's own
// settings apply otherwise and a request for more than gentle allows is
// rejected by scan.BuildPlan.
func planRequest(f planFlags, addrs []netip.Addr) (scan.PlanRequest, error) {
	req := scan.PlanRequest{Mode: f.mode, Targets: addrs, Probes: scan.DefaultProbes}
	if f.mode == scan.ModeGentle {
		if f.concurrencySet {
			req.Concurrency = f.concurrency
		}
		if f.timeoutSet {
			req.ConnectTimeout = f.timeout
		}
	} else {
		req.Concurrency, req.ConnectTimeout = f.concurrency, f.timeout
	}
	if f.ports != "" {
		var err error
		if req.Ports, err = scan.ParsePorts(f.ports); err != nil {
			return req, err
		}
	}
	if f.noPorts {
		req.Probes.Ports, req.Probes.Banners = false, false
	}
	if f.noBanners {
		req.Probes.Banners = false
	}
	if f.noNames {
		req.Probes.ReverseDNS, req.Probes.NetBIOS, req.Probes.MDNS = false, false, false
	}
	if f.noSSDP {
		req.Probes.SSDP = false
	}
	return req, nil
}

func gentleHelp() string {
	return "\nGentle mode (--mode gentle) is for fragile networks such as OT, medical or old printer segments.\n" +
		"  Settings: " + scan.GentleSummary() + "\n" +
		"  " + scan.GentleTradeoff + "\n" +
		"  --concurrency, --timeout and --ports may only ask for less than gentle mode allows (a larger value is rejected).\n"
}

func cmdReport(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	outFile := fs.String("o", "", "write HTML to this file instead of standard output")
	site := fs.String("site", "", "override the saved client/site label")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: ztg-scanner report [flags] <scan.json>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("provide one saved scan JSON file")
	}
	source := fs.Arg(0)
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	doc, readErr := report.Read(f)
	closeErr := f.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	siteSupplied := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "site" {
			siteSupplied = true
		}
	})
	if siteSupplied {
		if doc.Site, err = validateSiteLabel(*site); err != nil {
			return err
		}
	}
	if *outFile == "" {
		return report.HTML(stdout, doc)
	}
	if samePath(source, *outFile) {
		return errors.New("output file must differ from the source scan file")
	}
	return report.WriteFile(*outFile, func(w io.Writer) error { return report.HTML(w, doc) })
}

func validateSiteLabel(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > 200 || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("site label must be at most 200 UTF-8 bytes and contain no line breaks or NUL")
	}
	return value, nil
}

func samePath(source, destination string) bool {
	a, errA := filepath.Abs(source)
	b, errB := filepath.Abs(destination)
	if errA == nil && errB == nil && filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	fiA, errA := os.Stat(source)
	fiB, errB := os.Stat(destination)
	return errA == nil && errB == nil && os.SameFile(fiA, fiB)
}

func upOnly(hs []scan.Host) []scan.Host {
	var out []scan.Host
	for _, h := range hs {
		if h.Status == "up" {
			out = append(out, h)
		}
	}
	return out
}

// Environment variables for SNMPv3 passphrases in unattended runs.
// Passphrases are never accepted as flags: those end up in shell history
// and in the process list.
const (
	envAuth = "ZTG_SNMP_AUTH_PASS"
	envPriv = "ZTG_SNMP_PRIV_PASS"
)

func snmpCredentials(user, auth, priv string) (probe.SNMPCredentials, error) {
	c := probe.SNMPCredentials{Username: user, AuthProtocol: strings.ToUpper(auth), PrivProtocol: strings.ToUpper(priv)}
	var err error
	if c.AuthPassphrase, err = secret(envAuth, "SNMPv3 authentication passphrase for "+user+": "); err != nil {
		return c, err
	}
	if c.PrivPassphrase, err = secret(envPriv, "SNMPv3 privacy passphrase for "+user+": "); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func secret(env, prompt string) (string, error) {
	if v := os.Getenv(env); v != "" {
		return v, nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("set %s, or run in a terminal to be prompted", env)
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

func cmdVendor(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ztg-scanner vendor <mac>...")
	}
	db, err := oui.Default()
	if err != nil {
		return err
	}
	for _, mac := range args {
		info, err := db.Lookup(mac)
		if err != nil {
			return err
		}
		switch {
		case info.Multicast:
			fmt.Printf("%s\tmulticast group address\n", mac)
		case info.Randomized:
			fmt.Printf("%s\tlocally administered (randomized or virtual): no manufacturer\n", mac)
		case info.Vendor == "":
			fmt.Printf("%s\tunknown\n", mac)
		default:
			fmt.Printf("%s\t%s\n", mac, info.Vendor)
		}
	}
	return nil
}
