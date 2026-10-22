// Package classify infers what kind of device a host is from the evidence
// the probes collected, and flags findings worth a technician's attention.
// Every result is an inference and carries the reason it was made.
package classify

import (
	"strings"
	"time"
)

// Evidence is the subset of scan results classification reads.
type Evidence struct {
	Vendor        string
	MACRandomized bool
	Hostname      string
	TTL           int
	OpenPorts     map[int]bool
	HTTPText      []string // servers, titles, realms
	MDNSServices  []string
	UPnPType      string
	UPnPMaker     string
	UPnPModel     string
	NetBIOSGroups []byte // suffixes of group names
	NetBIOS       bool
	Telnet        bool
	CertExpiries  []time.Time
}

// Result is the inferred device type and why.
type Result struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// Finding is something the technician should look at.
type Finding struct {
	Severity string `json:"severity"` // "high", "medium", "info"
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type rule struct {
	typ   string
	match func(e *Evidence) (string, bool)
}

func has(e *Evidence, ports ...int) bool {
	for _, p := range ports {
		if e.OpenPorts[p] {
			return true
		}
	}
	return false
}

func vendorIs(e *Evidence, names ...string) (string, bool) {
	v := strings.ToLower(e.Vendor + " " + e.UPnPMaker)
	for _, n := range names {
		if strings.Contains(v, n) {
			return n, true
		}
	}
	return "", false
}

func textHas(e *Evidence, words ...string) (string, bool) {
	all := strings.ToLower(strings.Join(e.HTTPText, " ") + " " + e.UPnPType + " " + e.UPnPModel + " " + e.Hostname)
	for _, w := range words {
		if strings.Contains(all, w) {
			return w, true
		}
	}
	return "", false
}

func mdnsHas(e *Evidence, svcs ...string) (string, bool) {
	for _, have := range e.MDNSServices {
		for _, s := range svcs {
			if have == s {
				return s, true
			}
		}
	}
	return "", false
}

var printerVendors = []string{"brother", "canon", "epson", "seiko epson", "kyocera", "lexmark", "ricoh", "sharp", "xerox", "konica", "zebra", "oki data", "toshiba tec"}
var cameraVendors = []string{"hikvision", "dahua", "axis communications", "hanwha", "uniview", "reolink", "amcrest", "vivotek", "mobotix", "avigilon", "ezviz", "lorex"}
var nasVendors = []string{"synology", "qnap", "asustor", "buffalo", "western digital", "netgear readynas", "drobo", "terramaster"}
var firewallVendors = []string{"fortinet", "palo alto", "sonicwall", "watchguard", "sophos", "check point", "barracuda", "juniper"}
var netVendors = []string{"cisco", "meraki", "ubiquiti", "mikrotik", "routerboard", "aruba", "hewlett packard enterprise", "netgear", "tp-link", "d-link", "zyxel", "ruckus", "extreme networks", "cambium", "edgecore", "draytek", "arista", "linksys", "eero", "huawei", "zte corporation"}
var voipVendors = []string{"polycom", "yealink", "grandstream", "snom", "mitel", "avaya", "fanvil", "obihai"}
var iotVendors = []string{"espressif", "tuya", "shelly", "allterco", "sonoff", "itead", "nest labs", "ecobee", "ring llc", "wyze", "philips lighting", "signify", "lutron", "honeywell", "particle industries", "raspberry pi"}
var mediaVendors = []string{"roku", "sonos", "amazon technologies", "vizio", "lg electronics", "samsung electronics", "nvidia", "denon", "bose"}

// rules run in order; the first match wins. Specific evidence (a protocol
// only one kind of device speaks) comes before vendor-only guesses.
var rules = []rule{
	{"Domain controller", func(e *Evidence) (string, bool) {
		if has(e, 88) && has(e, 389, 636) {
			return "Kerberos (88) and LDAP open", true
		}
		for _, g := range e.NetBIOSGroups {
			if g == 0x1c {
				return "registers the NetBIOS domain controllers group name (<1C>)", true
			}
		}
		return "", false
	}},
	{"Printer", func(e *Evidence) (string, bool) {
		if s, ok := mdnsHas(e, "_ipp._tcp", "_ipps._tcp", "_printer._tcp", "_pdl-datastream._tcp"); ok {
			return "advertises " + s + " over mDNS", true
		}
		if w, ok := textHas(e, "printer"); ok && e.UPnPType != "" {
			return "UPnP device type mentions " + w, true
		}
		if has(e, 9100, 515, 631) {
			if v, ok := vendorIs(e, printerVendors...); ok {
				return "print port open and vendor " + v, true
			}
			if v, ok := vendorIs(e, "hewlett packard", "hp inc"); ok {
				return "print port open and vendor " + v, true
			}
			if has(e, 9100) {
				return "raw print port 9100 open", true
			}
		}
		return "", false
	}},
	{"IP camera / NVR", func(e *Evidence) (string, bool) {
		if v, ok := vendorIs(e, cameraVendors...); ok {
			return "vendor " + v, true
		}
		if has(e, 554) {
			if w, ok := textHas(e, "camera", "nvr", "dvr", "ipcam", "webcam", "network video"); ok {
				return "RTSP open and web page mentions " + w, true
			}
		}
		return "", false
	}},
	{"Hypervisor", func(e *Evidence) (string, bool) {
		if has(e, 8006) {
			return "Proxmox VE web port 8006 open", true
		}
		if w, ok := textHas(e, "vmware esxi", "proxmox", "hyper-v", "xcp-ng", "xenserver", "nutanix"); ok {
			return "web page mentions " + w, true
		}
		if has(e, 902) && has(e, 443) {
			return "VMware authentication port 902 open", true
		}
		return "", false
	}},
	{"NAS / storage", func(e *Evidence) (string, bool) {
		if v, ok := vendorIs(e, nasVendors...); ok {
			return "vendor " + v, true
		}
		if w, ok := textHas(e, "synology", "diskstation", "qnap", "truenas", "freenas", "unraid", "readynas"); ok {
			return "web page mentions " + w, true
		}
		if s, ok := mdnsHas(e, "_afpovertcp._tcp"); ok && !has(e, 3389) {
			return "advertises " + s, true
		}
		return "", false
	}},
	{"Firewall", func(e *Evidence) (string, bool) {
		if v, ok := vendorIs(e, firewallVendors...); ok {
			return "vendor " + v, true
		}
		if w, ok := textHas(e, "fortigate", "pfsense", "opnsense", "sonicwall", "watchguard", "sophos"); ok {
			return "web page mentions " + w, true
		}
		return "", false
	}},
	{"Router / gateway", func(e *Evidence) (string, bool) {
		if w, ok := textHas(e, "internetgatewaydevice"); ok {
			return "UPnP " + w, true
		}
		if v, ok := vendorIs(e, netVendors...); ok && has(e, 53) {
			return "DNS open and vendor " + v, true
		}
		return "", false
	}},
	{"Network device", func(e *Evidence) (string, bool) {
		if v, ok := vendorIs(e, netVendors...); ok {
			return "vendor " + v, true
		}
		if w, ok := textHas(e, "unifi", "routeros", "edgeos", "switch", "access point"); ok {
			return "web page mentions " + w, true
		}
		return "", false
	}},
	{"VoIP phone", func(e *Evidence) (string, bool) {
		if v, ok := vendorIs(e, voipVendors...); ok {
			return "vendor " + v, true
		}
		return "", false
	}},
	{"Media device", func(e *Evidence) (string, bool) {
		if s, ok := mdnsHas(e, "_googlecast._tcp", "_airplay._tcp", "_raop._tcp", "_spotify-connect._tcp", "_sonos._tcp"); ok {
			return "advertises " + s, true
		}
		if w, ok := textHas(e, "mediarenderer", "mediaserver"); ok {
			return "UPnP " + w, true
		}
		if v, ok := vendorIs(e, mediaVendors...); ok && !has(e, 22, 445, 3389) {
			return "vendor " + v, true
		}
		return "", false
	}},
	{"IoT device", func(e *Evidence) (string, bool) {
		if v, ok := vendorIs(e, iotVendors...); ok {
			return "vendor " + v, true
		}
		if s, ok := mdnsHas(e, "_hap._tcp", "_matter._tcp", "_homekit._tcp"); ok {
			return "advertises " + s, true
		}
		return "", false
	}},
	{"Windows host", func(e *Evidence) (string, bool) {
		if has(e, 3389) && has(e, 135, 445) {
			return "RDP and Windows RPC/SMB open", true
		}
		if has(e, 135) && has(e, 445) {
			return "Windows RPC (135) and SMB (445) open", true
		}
		if e.NetBIOS && e.TTL > 64 && e.TTL <= 128 {
			return "answers NetBIOS with a Windows TTL", true
		}
		return "", false
	}},
	{"Apple device", func(e *Evidence) (string, bool) {
		if v, ok := vendorIs(e, "apple"); ok {
			return "vendor " + v, true
		}
		if s, ok := mdnsHas(e, "_companion-link._tcp", "_device-info._tcp", "_rdlink._tcp"); ok {
			return "advertises " + s, true
		}
		return "", false
	}},
	{"Linux / Unix host", func(e *Evidence) (string, bool) {
		if has(e, 22) && e.TTL > 0 && e.TTL <= 64 {
			return "SSH open with a Unix TTL", true
		}
		if has(e, 445) && e.TTL > 0 && e.TTL <= 64 {
			return "SMB with a Unix TTL (Samba)", true
		}
		return "", false
	}},
	{"Private-MAC device (phone, laptop or VM)", func(e *Evidence) (string, bool) {
		if e.MACRandomized && len(e.OpenPorts) == 0 {
			return "randomized MAC and no open ports", true
		}
		return "", false
	}},
}

// Classify returns the first rule that matches, or "Unknown".
func Classify(e *Evidence) Result {
	for _, r := range rules {
		if why, ok := r.match(e); ok {
			return Result{Type: r.typ, Reason: why}
		}
	}
	return Result{Type: "Unknown", Reason: "no identifying evidence"}
}

// ndaaVendors are the IEEE assignee names of the producers FY2019 NDAA
// section 889 and FAR 52.204-25 name (and their subsidiaries visible in
// the registry). OEM rebrands do not carry these names and are not caught.
var ndaaVendors = []string{"huawei", "zte corporation", "hytera", "hikvision", "dahua", "ezviz"}

// Findings flags conditions worth a technician's attention.
func Findings(e *Evidence, now time.Time) []Finding {
	var out []Finding
	v := strings.ToLower(e.Vendor + " " + e.UPnPMaker)
	for _, n := range ndaaVendors {
		if strings.Contains(v, n) {
			out = append(out, Finding{
				Severity: "high",
				Code:     "ndaa-889",
				Message:  "Hardware from a vendor named in NDAA section 889 / FAR 52.204-25 (" + strings.TrimSpace(n) + "). Federal contractors may not use it; review and document or replace.",
			})
			break
		}
	}
	if e.OpenPorts[23] {
		out = append(out, Finding{Severity: "high", Code: "telnet", Message: "Telnet is open: administration in cleartext."})
	}
	if e.OpenPorts[21] {
		out = append(out, Finding{Severity: "medium", Code: "ftp", Message: "FTP is open: credentials and files travel in cleartext."})
	}
	if e.OpenPorts[80] && !e.OpenPorts[443] && hasAdminHint(e) {
		out = append(out, Finding{Severity: "medium", Code: "http-admin", Message: "A device management page is served over plain HTTP only."})
	}
	for _, exp := range e.CertExpiries {
		if !exp.IsZero() && exp.Before(now) {
			out = append(out, Finding{Severity: "info", Code: "cert-expired", Message: "A TLS certificate on this host expired on " + exp.Format("2006-01-02") + "."})
			break
		}
	}
	return out
}

func hasAdminHint(e *Evidence) bool {
	_, ok := textHas(e, "login", "admin", "configuration", "setup", "router", "management")
	return ok
}
