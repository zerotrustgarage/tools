// Package report renders scan results as a terminal table, CSV or JSON.
package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

// Document is the JSON export: the scan summary and every host.
type Document struct {
	Tool    string       `json:"tool"`
	Version string       `json:"version"`
	Targets []string     `json:"targets"`
	Summary scan.Summary `json:"summary"`
	Hosts   []scan.Host  `json:"hosts"`
}

// SortHosts orders hosts by IP address.
func SortHosts(hosts []scan.Host) {
	sort.Slice(hosts, func(i, j int) bool {
		a, _ := netip.ParseAddr(hosts[i].IP)
		b, _ := netip.ParseAddr(hosts[j].IP)
		return a.Less(b)
	})
}

// JSON writes doc indented.
func JSON(w io.Writer, doc Document) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// CSVHeader is the column order of the CSV export.
var CSVHeader = []string{"ip", "status", "name", "type", "mac", "vendor", "mac_randomized", "dns_name", "netbios_name", "workgroup", "mdns_name", "manufacturer", "model", "open_ports", "services", "rtt_ms", "ttl", "os_hint", "findings", "evidence", "serial", "firmware", "location", "snmp_descr"}

// CSV writes one row per host.
func CSV(w io.Writer, hosts []scan.Host) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(CSVHeader); err != nil {
		return err
	}
	for _, h := range hosts {
		if err := cw.Write(csvRow(h)); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func csvRow(h scan.Host) []string {
	var nbName, wg, mdnsName, maker, model string
	if h.NetBIOS != nil {
		nbName, wg = h.NetBIOS.Computer, h.NetBIOS.Workgroup
	}
	if h.MDNS != nil {
		mdnsName = h.MDNS.Hostname
	}
	if h.UPnP != nil {
		maker = h.UPnP.Manufacturer
		model = strings.TrimSpace(h.UPnP.ModelName + " " + h.UPnP.ModelNumber)
	}
	var serial, firmware, location, descr string
	if h.SNMP != nil {
		if maker == "" {
			maker = h.SNMP.Manufacturer
		}
		if model == "" {
			model = h.SNMP.Model
		}
		serial, location, descr = h.SNMP.Serial, h.SNMP.SysLocation, h.SNMP.SysDescr
		firmware = h.SNMP.Firmware
		if firmware == "" {
			firmware = h.SNMP.Software
		}
	}
	var findings []string
	for _, f := range h.Findings {
		findings = append(findings, f.Severity+":"+f.Code)
	}
	rtt := ""
	if h.RTTMillis > 0 {
		rtt = strconv.FormatFloat(h.RTTMillis, 'f', 1, 64)
	}
	ttl := ""
	if h.TTL > 0 {
		ttl = strconv.Itoa(h.TTL)
	}
	return []string{
		h.IP, h.Status, safe(h.Name), h.Type, h.MAC, safe(h.Vendor), strconv.FormatBool(h.MACRandomized),
		safe(h.DNSName), safe(nbName), safe(wg), safe(mdnsName), safe(maker), safe(model),
		Ports(h), safe(ServiceSummary(h)), rtt, ttl, h.OSHint, strings.Join(findings, " "), strings.Join(h.Evidence, " "),
		safe(serial), safe(firmware), safe(location), safe(descr),
	}
}

// safe neutralizes spreadsheet formula injection: device-supplied text (a
// hostname, a web page title) must not run as a formula when a technician
// opens the CSV in Excel.
func safe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// Ports lists open ports, space separated.
func Ports(h scan.Host) string {
	p := make([]string, len(h.Services))
	for i, s := range h.Services {
		p[i] = strconv.Itoa(s.Port)
	}
	return strings.Join(p, " ")
}

// ServiceSummary is a one-line description of what answered.
func ServiceSummary(h scan.Host) string {
	var parts []string
	for _, s := range h.Services {
		d := fmt.Sprintf("%d/%s", s.Port, s.Name)
		switch {
		case s.HTTP != nil && s.HTTP.Title != "":
			d += " \"" + s.HTTP.Title + "\""
		case s.Product != "":
			d += " " + s.Product
		}
		parts = append(parts, d)
	}
	return strings.Join(parts, "; ")
}

// Table writes a human-readable table of live hosts (all hosts if all).
func Table(w io.Writer, hosts []scan.Host, all bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tIP\tNAME\tTYPE\tMAC\tVENDOR\tPORTS\tFLAGS")
	for _, h := range hosts {
		if !all && h.Status != "up" {
			continue
		}
		vendor := h.Vendor
		if h.MACRandomized {
			vendor = "(private MAC)"
		}
		var flags []string
		for _, f := range h.Findings {
			flags = append(flags, strings.ToUpper(f.Severity[:1])+":"+f.Code)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			h.Status, h.IP, trunc(h.Name, 32), h.Type, h.MAC, trunc(vendor, 28), trunc(Ports(h), 40), strings.Join(flags, " "))
	}
	return tw.Flush()
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
