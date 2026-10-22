package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// UPnPInfo is what a device announces over SSDP and in its UPnP device
// description: routers, printers, TVs, NAS units, cameras and media
// players usually name their manufacturer and model here.
type UPnPInfo struct {
	Server       string `json:"server,omitempty"`
	Location     string `json:"location,omitempty"`
	FriendlyName string `json:"friendly_name,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	ModelName    string `json:"model_name,omitempty"`
	ModelNumber  string `json:"model_number,omitempty"`
	DeviceType   string `json:"device_type,omitempty"`
}

var ssdpGroup = &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}

// SSDPDiscover sends one multicast M-SEARCH on the local segment and
// collects answers for wait. Devices on other subnets are not reached.
func SSDPDiscover(ctx context.Context, wait time.Duration) (map[netip.Addr]*UPnPInfo, error) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer c.Close()
	req := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: ssdp:all\r\nUSER-AGENT: " + UserAgent + "\r\n\r\n"
	for i := 0; i < 2; i++ { // UDP: send twice
		if _, err := c.WriteToUDP([]byte(req), ssdpGroup); err != nil {
			return nil, err
		}
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetReadDeadline(deadline)
	out := map[netip.Addr]*UPnPInfo{}
	buf := make([]byte, 4096)
	for {
		n, src, err := c.ReadFromUDP(buf)
		if err != nil {
			break
		}
		addr, ok := netip.AddrFromSlice(src.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		server, location := parseSSDP(buf[:n])
		info := out[addr]
		if info == nil {
			info = &UPnPInfo{}
			out[addr] = info
		}
		if info.Server == "" {
			info.Server = server
		}
		// Prefer a description hosted on the responder itself; never follow
		// a LOCATION that points at another host.
		if info.Location == "" && locationOnHost(location, addr) {
			info.Location = location
		}
	}
	return out, nil
}

func parseSSDP(b []byte) (server, location string) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(k)) {
		case "SERVER":
			server = clip(v, 160)
		case "LOCATION":
			location = clip(v, 300)
		}
	}
	return server, location
}

func locationOnHost(loc string, addr netip.Addr) bool {
	u, err := url.Parse(loc)
	if err != nil || u.Scheme != "http" {
		return false
	}
	h, err := netip.ParseAddr(u.Hostname())
	return err == nil && h.Unmap() == addr
}

type upnpRoot struct {
	Device struct {
		DeviceType   string `xml:"deviceType"`
		FriendlyName string `xml:"friendlyName"`
		Manufacturer string `xml:"manufacturer"`
		ModelName    string `xml:"modelName"`
		ModelNumber  string `xml:"modelNumber"`
	} `xml:"device"`
}

// FetchDescription reads the UPnP device description at info.Location
// (already checked to be on the device itself) and fills in its fields.
func FetchDescription(ctx context.Context, info *UPnPInfo, timeout time.Duration) {
	if info == nil || info.Location == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.Location, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", UserAgent)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	parseDescription(io.LimitReader(resp.Body, 256*1024), info)
}

func parseDescription(r io.Reader, info *UPnPInfo) {
	var root upnpRoot
	if err := xml.NewDecoder(r).Decode(&root); err != nil {
		return
	}
	d := root.Device
	info.FriendlyName = clip(d.FriendlyName, 120)
	info.Manufacturer = clip(d.Manufacturer, 120)
	info.ModelName = clip(d.ModelName, 120)
	info.ModelNumber = clip(d.ModelNumber, 60)
	info.DeviceType = clip(d.DeviceType, 120)
}
