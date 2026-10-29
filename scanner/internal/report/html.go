package report

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"strings"

	"github.com/zerotrustgarage/tools/scanner/internal/classify"
	"github.com/zerotrustgarage/tools/scanner/internal/inventory"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
	"github.com/zerotrustgarage/tools/scanner/internal/targets"
)

//go:embed report.html.tmpl
var reportHTML string

// HTML writes a self-contained, bounded inventory report. It is suitable for
// opening locally and for printing; all document strings are template-escaped.
func HTML(w io.Writer, doc Document) error {
	if len(doc.Hosts) > targets.DefaultMaxHosts {
		return fmt.Errorf("HTML report supports at most %d host rows", targets.DefaultMaxHosts)
	}
	v := makeHTMLView(doc)
	return htmlTemplate.Execute(&budgetWriter{w: w, remaining: MaxDocumentBytes}, v)
}

type htmlView struct {
	Document
	Status             string
	Recorded, Up, Down int
	FindingCount       int
	Findings           map[string]int
	Hosts              []htmlHost
}
type htmlHost struct {
	scan.Host
	Anchor                            string
	Ports, Discovery, Model, Firmware string
	// Extra is the device-reported text of the SC11 probes, one item per line.
	Extra    string
	Findings []classify.Finding
}

func makeHTMLView(d Document) htmlView {
	v := htmlView{Document: d, Findings: map[string]int{}, Hosts: make([]htmlHost, 0, len(d.Hosts))}
	v.Recorded = len(d.Hosts)
	if d.Summary.Canceled {
		v.Status = "Canceled / partial"
	} else if d.Summary.Finished.IsZero() {
		v.Status = "Incomplete / partial snapshot"
	} else {
		v.Status = "Complete"
	}
	for _, h := range d.Hosts {
		if h.Status == "up" {
			v.Up++
		} else {
			v.Down++
		}
		for _, f := range h.Findings {
			v.Findings[f.Severity]++
			v.FindingCount++
		}
	}
	hosts := append([]scan.Host(nil), d.Hosts...)
	for i := range hosts {
		if annotationEmpty(hosts[i].Annotation) {
			if a, ok := d.Annotations[inventory.AnnotationKey(hosts[i])]; ok {
				hosts[i].Annotation = a
			}
		}
	}
	SortHosts(hosts)
	for i, h := range hosts {
		x := htmlHost{Host: h, Anchor: fmt.Sprintf("host-%05d", i+1), Ports: Ports(h), Findings: append([]classify.Finding(nil), h.Findings...)}
		x.Discovery = joinNonempty(" · ", h.DNSName, netbiosName(h), h.LLMNR.Primary(), mdnsName(h), upnpName(h))
		x.Extra = extraIdentification(h)
		x.Model = joinNonempty(" · ", snmpModel(h), upnpModel(h))
		x.Firmware = joinNonempty(" · ", snmpFirmware(h))
		v.Hosts = append(v.Hosts, x)
	}
	return v
}

// extraIdentification lists what WS-Discovery, SMB2 and the favicon probe
// recorded. Everything is device-reported and is escaped by the template; the
// addresses a device announced are shown as text and never as links.
func extraIdentification(h scan.Host) string {
	var lines []string
	if w := h.WSD; w != nil {
		if len(w.Types) > 0 {
			lines = append(lines, "WS-Discovery types: "+strings.Join(w.Types, ", "))
		}
		if w.Name != "" || w.Hardware != "" {
			lines = append(lines, "ONVIF name / hardware: "+joinNonempty(" / ", w.Name, w.Hardware))
		}
		if len(w.XAddrs) > 0 {
			lines = append(lines, "WS-Discovery addresses (announced by the device, not contacted): "+strings.Join(w.XAddrs, ", "))
		}
	}
	if s := h.SMB; s != nil {
		line := "SMB2 negotiate: dialect " + s.Dialect + ", signing " + SigningPolicy(s)
		if len(s.Capabilities) > 0 {
			line += ", capabilities " + strings.Join(s.Capabilities, " ")
		}
		lines = append(lines, line)
		if s.SystemTime != "" {
			lines = append(lines, "SMB2 server clock (UTC): "+s.SystemTime)
		}
		if s.ServerGUID != "" {
			lines = append(lines, "SMB2 server GUID: "+s.ServerGUID)
		}
		if s.EraHint != "" {
			lines = append(lines, s.EraHint+" (an inference from the dialect, not a measured version)")
		}
	}
	for _, sv := range h.Services {
		if f := sv.Favicon; f != nil {
			lines = append(lines, fmt.Sprintf("Favicon on port %d: SHA-256 %s, MurmurHash3 %d, %d bytes", sv.Port, f.SHA256, f.MMH3, f.Size))
		}
	}
	return strings.Join(lines, "\n")
}

func annotationEmpty(a scan.Annotation) bool {
	return a.Notes == "" && len(a.Tags) == 0 && !a.Favorite
}

func joinNonempty(sep string, vals ...string) string {
	out := vals[:0:0]
	for _, s := range vals {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, sep)
}
func netbiosName(h scan.Host) string {
	if h.NetBIOS != nil {
		return h.NetBIOS.Computer
	}
	return ""
}
func mdnsName(h scan.Host) string {
	if h.MDNS != nil {
		return h.MDNS.Hostname
	}
	return ""
}
func upnpName(h scan.Host) string {
	if h.UPnP != nil {
		return joinNonempty(" · ", h.UPnP.FriendlyName, h.UPnP.Manufacturer, h.UPnP.DeviceType)
	}
	return ""
}
func upnpModel(h scan.Host) string {
	if h.UPnP != nil {
		return joinNonempty(" · ", h.UPnP.Manufacturer, joinNonempty(" ", h.UPnP.ModelName, h.UPnP.ModelNumber))
	}
	return ""
}
func snmpModel(h scan.Host) string {
	if h.SNMP != nil {
		return joinNonempty(" ", h.SNMP.Manufacturer, h.SNMP.Model)
	}
	return ""
}
func snmpFirmware(h scan.Host) string {
	if h.SNMP != nil {
		return joinNonempty(" · ", h.SNMP.Firmware, h.SNMP.Software)
	}
	return ""
}

var htmlTemplate = template.Must(template.New("report").Parse(reportHTML))
