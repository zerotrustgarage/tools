package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/scan"
	"github.com/zerotrustgarage/tools/scanner/internal/targets"
)

// DeviceAction only accepts numeric addresses from current results. Device names
// and banners never become program arguments. The Windows adapter uses fixed
// executables in the system directory, with no shell or arbitrary commands.
func (a *App) DeviceAction(action, ip string, allowPublic bool) (string, error) {
	if a.platform == nil {
		return "", errors.New("not available")
	}
	if _, _, err := actionCommand(action, ip); err != nil {
		return "", err
	}
	a.mu.Lock()
	var host *scan.Host
	for _, h := range a.hosts {
		if h.IP == ip {
			host = &h
			break
		}
	}
	publicOK := a.allowPub || allowPublic
	a.mu.Unlock()
	if host == nil {
		return "", errors.New("that address is not in the results")
	}
	if !targets.IsInternal(netip.MustParseAddr(ip)) && !publicOK {
		return "", errors.New("public address space needs authorization for this action")
	}
	required := 0
	if action == "ssh" {
		required = 22
	}
	if action == "rdp" {
		required = 3389
	}
	if required != 0 {
		found := false
		for _, s := range host.Services {
			if s.Port == required {
				found = true
			}
		}
		if host.Status != "up" || !found {
			return "", errors.New("the required service was not found open on that device")
		}
	}
	return a.platform.Launch(action, ip)
}

func actionCommand(action, ip string) (string, []string, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() || addr.String() != ip {
		return "", nil, errors.New("a numeric IPv4 address is required")
	}
	switch action {
	case "ssh":
		return "ssh.exe", []string{"-p", "22", ip}, nil
	case "rdp":
		return "mstsc.exe", []string{"/v:" + ip}, nil
	case "ping":
		return "ping.exe", []string{"-n", "4", "-w", "1000", ip}, nil
	case "traceroute":
		return "tracert.exe", []string{"-d", "-h", "16", "-w", "500", ip}, nil
	default:
		return "", nil, errors.New("unknown device action")
	}
}

func (a *App) WakeOnLAN(ip string) error {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() || addr.String() != ip || !targets.IsInternal(addr) {
		return errors.New("Wake-on-LAN requires an internal IPv4 address")
	}
	a.mu.Lock()
	var mac string
	for _, h := range a.hosts {
		if h.IP == ip && !h.MACRandomized {
			mac = h.MAC
			if mac == "" {
				mac = h.LastKnownMAC
			}
			break
		}
	}
	a.mu.Unlock()
	if mac == "" {
		return errors.New("a known, non-randomized MAC address is required")
	}
	return a.wake(ip, mac)
}

func magicPacket(mac string) ([]byte, error) {
	m, err := net.ParseMAC(mac)
	if err != nil || len(m) != 6 || m[0]&1 != 0 || m.String() == "00:00:00:00:00:00" {
		return nil, errors.New("a unicast six-byte MAC address is required")
	}
	b := make([]byte, 6+16*6)
	for i := 0; i < 6; i++ {
		b[i] = 255
	}
	for i := 6; i < len(b); i += 6 {
		copy(b[i:], m)
	}
	return b, nil
}

func wakeDestination(ip netip.Addr, prefixes []netip.Prefix) (netip.Addr, netip.Addr, error) {
	best := netip.Prefix{}
	for _, p := range prefixes {
		if p.Addr().Is4() && p.Bits() <= 30 && p.Contains(ip) && (!best.IsValid() || p.Bits() > best.Bits()) {
			best = p
		}
	}
	if !best.IsValid() {
		return netip.Addr{}, netip.Addr{}, errors.New("Wake-on-LAN is limited to a directly connected IPv4 network")
	}
	b := best.Masked().Addr().As4()
	for bit := best.Bits(); bit < 32; bit++ {
		b[bit/8] |= 1 << (7 - bit%8)
	}
	return best.Addr(), netip.AddrFrom4(b), nil
}

func sendWake(ip, mac string) error {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() || !targets.IsInternal(addr) {
		return errors.New("Wake-on-LAN requires an internal IPv4 address")
	}
	packet, err := magicPacket(mac)
	if err != nil {
		return err
	}
	ifs, err := net.Interfaces()
	if err != nil {
		return err
	}
	var prefixes []netip.Prefix
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				prefixes = append(prefixes, p)
			}
		}
	}
	local, broadcast, err := wakeDestination(addr, prefixes)
	if err != nil {
		return err
	}
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IP(local.AsSlice())})
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	if _, err := c.WriteToUDP(packet, &net.UDPAddr{IP: net.IP(broadcast.AsSlice()), Port: 9}); err != nil {
		return fmt.Errorf("send Wake-on-LAN: %w", err)
	}
	return nil
}
