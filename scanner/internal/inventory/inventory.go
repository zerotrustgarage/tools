// Package inventory compares observed devices and identifies local annotations.
package inventory

import (
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"sort"
	"strings"

	"github.com/zerotrustgarage/tools/scanner/internal/scan"
	"github.com/zerotrustgarage/tools/scanner/internal/targets"
)

// Key uses a stable MAC when available. An IP-only or randomized-MAC record is
// tied to its address and MAC together, so a replacement does not inherit notes.
func Key(h scan.Host) string {
	mac, _ := net.ParseMAC(h.MAC)
	if len(mac) == 6 && mac[0]&1 == 0 && mac.String() != "00:00:00:00:00:00" && !h.MACRandomized {
		return "mac:" + mac.String()
	}
	return AnnotationKey(h)
}

// AnnotationKey also includes the address: duplicate MACs must not share notes.
func AnnotationKey(h scan.Host) string {
	macText := h.MAC
	if macText == "" && h.Status == "down" {
		macText = h.LastKnownMAC
	}
	mac, _ := net.ParseMAC(macText)
	return "ip:" + h.IP + "|" + mac.String()
}

// ApplyAnnotations follows a DHCP move only when the MAC is unique in both
// completed observations. A changed occupant never inherits the previous notes.
func ApplyAnnotations(before, hosts []scan.Host, notes map[string]scan.Annotation) {
	old, current := live(before), live(hosts)
	oldMAC, curMAC := uniqueMAC(old), uniqueMAC(current)
	history := map[string]scan.Host{}
	for _, h := range before {
		history[h.IP] = h
	}
	for i := range hosts {
		h := &hosts[i]
		if prior, ok := history[h.IP]; ok && h.Status == "down" && !prior.MACRandomized {
			h.LastKnownMAC = prior.MAC
			if h.LastKnownMAC == "" {
				h.LastKnownMAC = prior.LastKnownMAC
			}
		}
		key := AnnotationKey(*h)
		note, exists := notes[key]
		stable := Key(*h)
		if strings.HasPrefix(stable, "mac:") && curMAC[stable] == h.IP && oldMAC[stable] != "" {
			prior, found := notes[AnnotationKey(old[oldMAC[stable]])]
			if found && (exists || len(notes) < targets.DefaultMaxHosts) {
				note, exists = prior, true
				notes[key] = prior
			}
		}
		if exists {
			h.Annotation = note
		} else {
			h.Annotation = scan.Annotation{}
		}
	}
}

func ValidKey(key string) bool {
	if strings.HasPrefix(key, "mac:") {
		mac, err := net.ParseMAC(strings.TrimPrefix(key, "mac:"))
		return err == nil && len(mac) == 6 && key == Key(scan.Host{MAC: mac.String()})
	}
	parts := strings.Split(strings.TrimPrefix(key, "ip:"), "|")
	if !strings.HasPrefix(key, "ip:") || len(parts) != 2 {
		return false
	}
	ip, err := netip.ParseAddr(parts[0])
	if err != nil || !ip.Is4() || ip.String() != parts[0] {
		return false
	}
	if parts[1] == "" {
		return true
	}
	mac, err := net.ParseMAC(parts[1])
	return err == nil && len(mac) == 6 && mac.String() == parts[1]
}

func ValidateAnnotation(a scan.Annotation) error {
	if len(a.Notes) > 8192 || len(a.Tags) > 32 {
		return fmt.Errorf("notes are limited to 8192 bytes and tags to 32")
	}
	for _, tag := range a.Tags {
		if len(tag) == 0 || len(tag) > 64 || strings.TrimSpace(tag) != tag || strings.ContainsAny(tag, "\x00\r\n\t,") {
			return fmt.Errorf("tags must be 1 to 64 bytes without commas or control characters")
		}
	}
	return nil
}

type Change struct {
	Kind   string     `json:"kind"` // new, missing, changed, unchanged, not_scanned
	Before *scan.Host `json:"before,omitempty"`
	After  *scan.Host `json:"after,omitempty"`
	Fields []string   `json:"fields,omitempty"`
}

// Compare matches unique stable MACs first (including DHCP moves), then IPs.
// Missing means no answer at an address that was actually visited. It does not
// mean decommissioned, and it never includes an unvisited address or canceled scan.
func Compare(before, after []scan.Host, canceled bool) []Change {
	old, cur := live(before), live(after)
	oldMAC, curMAC := uniqueMAC(old), uniqueMAC(cur)
	matched := map[string]bool{}
	paired := map[string]string{}
	for ip, h := range cur {
		key := Key(h)
		if strings.HasPrefix(key, "mac:") && curMAC[key] == ip && oldMAC[key] != "" {
			paired[ip] = oldMAC[key]
			matched[oldMAC[key]] = true
		}
	}
	for ip := range cur {
		if _, ok := paired[ip]; !ok && !matched[ip] {
			if _, exists := old[ip]; exists {
				paired[ip], matched[ip] = ip, true
			}
		}
	}
	var out []Change
	for ip, h := range cur {
		previous, ok := paired[ip]
		if !ok {
			out = append(out, Change{Kind: "new", After: &h})
			continue
		}
		prev := old[previous]
		fields := changes(prev, h)
		kind := "unchanged"
		if len(fields) > 0 {
			kind = "changed"
		}
		out = append(out, Change{Kind: kind, Before: &prev, After: &h, Fields: fields})
	}
	visited := map[string]string{}
	for _, h := range after {
		visited[h.IP] = h.Status
	}
	for ip, h := range old {
		if matched[ip] {
			continue
		}
		kind := "not_scanned"
		if visited[ip] == "down" && !canceled {
			kind = "missing"
		}
		out = append(out, Change{Kind: kind, Before: &h})
	}
	sort.Slice(out, func(i, j int) bool {
		ip := func(c Change) netip.Addr {
			if c.After != nil {
				return netip.MustParseAddr(c.After.IP)
			}
			return netip.MustParseAddr(c.Before.IP)
		}
		a, b := ip(out[i]), ip(out[j])
		if a == b {
			return out[i].Kind < out[j].Kind
		}
		return a.Less(b)
	})
	return out
}

func live(hosts []scan.Host) map[string]scan.Host {
	m := map[string]scan.Host{}
	for _, h := range hosts {
		if h.Status == "up" {
			m[h.IP] = h
		}
	}
	return m
}
func uniqueMAC(hosts map[string]scan.Host) map[string]string {
	m := map[string]string{}
	for ip, h := range hosts {
		key := Key(h)
		if _, exists := m[key]; exists {
			m[key] = ""
		} else {
			m[key] = ip
		}
	}
	return m
}
func changes(a, b scan.Host) []string {
	var out []string
	add := func(name string, x, y any) {
		if !reflect.DeepEqual(x, y) {
			out = append(out, name)
		}
	}
	add("ip", a.IP, b.IP)
	add("name", a.Name, b.Name)
	add("mac", strings.ToLower(a.MAC), strings.ToLower(b.MAC))
	add("type", a.Type, b.Type)
	add("vendor", a.Vendor, b.Vendor)
	services := func(h scan.Host) []string {
		out := []string{}
		for _, s := range h.Services {
			out = append(out, fmt.Sprintf("%d/%s/%s", s.Port, s.Name, s.Product))
		}
		sort.Strings(out)
		return out
	}
	add("services", services(a), services(b))
	// SC11 evidence is compared only where both scans have it, so turning a probe
	// on or off, or one lost UDP datagram, is not reported as a device change.
	// Clock readings, GUIDs and capability bits are never compared.
	if a.LLMNR != nil && b.LLMNR != nil {
		add("llmnr_name", a.LLMNR.Primary(), b.LLMNR.Primary())
	}
	if a.WSD != nil && b.WSD != nil {
		add("wsd_types", sortedCopy(a.WSD.Types), sortedCopy(b.WSD.Types))
	}
	if a.SMB != nil && b.SMB != nil {
		add("smb_dialect", a.SMB.Dialect, b.SMB.Dialect)
		add("smb_signing", [2]bool{a.SMB.SigningEnabled, a.SMB.SigningRequired}, [2]bool{b.SMB.SigningEnabled, b.SMB.SigningRequired})
	}
	if x, y := favicons(a), favicons(b); len(x) > 0 && len(y) > 0 {
		for port, sum := range x {
			if other, ok := y[port]; ok && other != sum {
				out = append(out, "favicon")
				break
			}
		}
	}
	if a.SNMP != nil && b.SNMP != nil {
		add("model", a.SNMP.Model, b.SNMP.Model)
		add("serial", a.SNMP.Serial, b.SNMP.Serial)
		add("firmware", a.SNMP.Firmware, b.SNMP.Firmware)
		add("software", a.SNMP.Software, b.SNMP.Software)
	}
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

// favicons maps a port to the SHA-256 of the icon its web service served.
func favicons(h scan.Host) map[int]string {
	m := map[int]string{}
	for _, s := range h.Services {
		if s.Favicon != nil {
			m[s.Port] = s.Favicon.SHA256
		}
	}
	return m
}
