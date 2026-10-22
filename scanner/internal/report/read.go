package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/zerotrustgarage/tools/scanner/internal/inventory"
	"github.com/zerotrustgarage/tools/scanner/internal/targets"
)

const MaxDocumentBytes = 64 << 20

// JSONBounded keeps desktop saves within the reader's size limit. Callers use
// WriteFile so exceeding the budget cannot damage an existing inventory.
func JSONBounded(w io.Writer, d Document, maxBytes int64) error {
	return JSON(&budgetWriter{w: w, remaining: maxBytes}, d)
}

type budgetWriter struct {
	w         io.Writer
	remaining int64
}

func (w *budgetWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.remaining {
		return 0, fmt.Errorf("scan file would exceed the size limit; scan a smaller target set")
	}
	n, err := w.w.Write(b)
	w.remaining -= int64(n)
	return n, err
}

// Read accepts a bounded Scanner JSON export, including earlier exports with no
// annotations. Unknown fields are refused so imported credentials cannot persist.
func Read(r io.Reader) (Document, error) {
	var d Document
	limited := io.LimitReader(r, MaxDocumentBytes+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return d, err
	}
	if len(b) > MaxDocumentBytes {
		return d, fmt.Errorf("scan file exceeds 64 MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, fmt.Errorf("invalid scan file: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return d, fmt.Errorf("scan file must contain exactly one JSON document")
	}
	if d.Tool != "ztg-scanner" && d.Tool != "ztg-scanner-desktop" {
		return d, fmt.Errorf("this is not a ZTG Scanner export")
	}
	if len(d.Site) > 200 || strings.ContainsAny(d.Site, "\r\n\x00") || len(d.Hosts) > targets.DefaultMaxHosts || len(d.Annotations) > targets.DefaultMaxHosts || len(d.Targets) > targets.DefaultMaxHosts {
		return d, fmt.Errorf("scan file exceeds inventory limits")
	}
	seen := map[string]bool{}
	for i := range d.Hosts {
		h := &d.Hosts[i]
		ip, err := netip.ParseAddr(h.IP)
		if err != nil || !ip.Is4() || ip.String() != h.IP || seen[h.IP] {
			return d, fmt.Errorf("invalid or duplicate IPv4 address %q", h.IP)
		}
		seen[h.IP] = true
		if h.Status != "up" && h.Status != "down" {
			return d, fmt.Errorf("invalid host status")
		}
		if h.MAC != "" {
			mac, err := net.ParseMAC(h.MAC)
			if err != nil || len(mac) != 6 {
				return d, fmt.Errorf("invalid MAC address")
			}
			h.MAC = mac.String()
		}
		if h.LastKnownMAC != "" {
			mac, err := net.ParseMAC(h.LastKnownMAC)
			if err != nil || len(mac) != 6 || h.Status != "down" {
				return d, fmt.Errorf("invalid historical MAC address")
			}
			h.LastKnownMAC = mac.String()
		}
		if len(h.Services) > 1024 {
			return d, fmt.Errorf("too many services")
		}
		ports := map[int]bool{}
		for _, s := range h.Services {
			if s.Port < 1 || s.Port > 65535 || ports[s.Port] {
				return d, fmt.Errorf("invalid or duplicate service port")
			}
			ports[s.Port] = true
		}
		if err := inventory.ValidateAnnotation(h.Annotation); err != nil {
			return d, err
		}
	}
	for key, a := range d.Annotations {
		if !inventory.ValidKey(key) {
			return d, fmt.Errorf("invalid annotation identity")
		}
		if err := inventory.ValidateAnnotation(a); err != nil {
			return d, err
		}
	}
	return d, nil
}

// WriteFile replaces a file only after the complete export was flushed and
// closed. A failed writer never truncates an existing inventory.
func WriteFile(path string, write func(io.Writer) error) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".ztg-scan-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := write(f); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
