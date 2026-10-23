// Package importer consumes exported inventory only; it never contacts scanned hosts.
package importer

import (
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"sort"
	"strings"
)

func Scanner(name string) ([]netip.Prefix, error) {
	f, e := os.Open(name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return Decode(f)
}
func Decode(r io.Reader) ([]netip.Prefix, error) {
	b, e := io.ReadAll(io.LimitReader(r, (16<<20)+1))
	if e != nil {
		return nil, e
	}
	if len(b) > 16<<20 {
		return nil, errors.New("Scanner export exceeds 16 MiB")
	}
	var doc struct {
		Tool  string `json:"tool"`
		Hosts []struct {
			IP     string `json:"ip"`
			Status string `json:"status"`
		} `json:"hosts"`
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	if e = d.Decode(&doc); e != nil {
		return nil, e
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing Scanner data")
	}
	if doc.Tool != "ZTG Scanner" && doc.Tool != "ztg-scanner" && doc.Tool != "ztg-scanner-desktop" {
		return nil, errors.New("expected a ZTG Scanner JSON export")
	}
	seen := map[netip.Addr]bool{}
	out := []netip.Prefix{}
	for _, h := range doc.Hosts {
		if h.Status != "up" {
			continue
		}
		a, e := netip.ParseAddr(h.IP)
		if e != nil || a.Zone() != "" || a.IsUnspecified() || a.IsMulticast() {
			return nil, errors.New("invalid Scanner host address")
		}
		a = a.Unmap()
		if !seen[a] {
			seen[a] = true
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
		if len(out) > 4096 {
			return nil, errors.New("Scanner import exceeds 4096 hosts")
		}
	}
	if len(out) == 0 {
		return nil, errors.New("Scanner export has no reachable hosts; refusing an empty allowlist")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr().Less(out[j].Addr()) })
	return out, nil
}
