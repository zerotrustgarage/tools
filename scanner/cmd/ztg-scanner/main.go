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
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/oui"
	"github.com/zerotrustgarage/tools/scanner/internal/probe"
	"github.com/zerotrustgarage/tools/scanner/internal/report"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
	"github.com/zerotrustgarage/tools/scanner/internal/targets"
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
	case "scan":
		err = cmdScan(os.Args[2:])
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
  ztg-scanner vendor <mac>...            look up the manufacturer of a MAC address
  ztg-scanner version

Targets: 192.168.1.0/24  10.0.0.1-50  10.0.0.1-10.0.1.20  10.0.0.7  printer.lan
Only private, CGNAT, link-local and loopback space is scanned unless --allow-public is given.

Run "ztg-scanner scan -h" for scan flags.
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
		quiet       = fs.Bool("q", false, "no progress output")
	)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: ztg-scanner scan [flags] <target>...")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return errors.New("no targets given")
	}
	if *jsonOut && *csvOut {
		return errors.New("choose one of --json and --csv")
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

	cfg := scan.Config{
		Targets:        addrs,
		Probes:         scan.DefaultProbes,
		Concurrency:    *concurrency,
		ConnectTimeout: *timeout,
	}
	if *portsFlag != "" {
		if cfg.Ports, err = parsePorts(*portsFlag); err != nil {
			return err
		}
	}
	if *noPorts {
		cfg.Probes.Ports, cfg.Probes.Banners = false, false
	}
	if *noBanners {
		cfg.Probes.Banners = false
	}
	if *noNames {
		cfg.Probes.ReverseDNS, cfg.Probes.NetBIOS, cfg.Probes.MDNS = false, false, false
	}
	if *noSSDP {
		cfg.Probes.SSDP = false
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

	var w io.Writer = os.Stdout
	if *outFile != "" {
		f, err := os.Create(*outFile)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	switch {
	case *jsonOut:
		return report.JSON(w, report.Document{Tool: "ztg-scanner", Version: version, Targets: fs.Args(), Summary: sum, Hosts: col.hosts})
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

func upOnly(hs []scan.Host) []scan.Host {
	var out []scan.Host
	for _, h := range hs {
		if h.Status == "up" {
			out = append(out, h)
		}
	}
	return out
}

func parsePorts(s string) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			return nil, fmt.Errorf("bad port %q", part)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil {
				return nil, fmt.Errorf("bad port range %q", part)
			}
		}
		if a < 1 || b > 65535 || b < a {
			return nil, fmt.Errorf("bad port range %q", part)
		}
		for p := a; p <= b; p++ {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no ports given")
	}
	if len(out) > 1024 {
		return nil, fmt.Errorf("%d ports is too many for an inventory scan (max 1024)", len(out))
	}
	return out, nil
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
