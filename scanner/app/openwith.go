package main

import (
	"errors"
	"net/netip"

	"github.com/zerotrustgarage/tools/scanner/internal/openwith"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
	"github.com/zerotrustgarage/tools/scanner/internal/targets"
)

// "Open with" commands (SC10). The technician defines them in the settings
// editor; the UI keeps them in the WebView's local storage, like saved target
// sets, and hands the whole list to SetOpenWithCommands, which validates it
// and keeps the copy used for launching. A launch names a command and an
// address; the hostname comes from the current results here in Go, never from
// the UI, and nothing a device reports reaches the program path or the
// argument structure (internal/openwith).

// SetOpenWithCommands validates and installs the technician's saved commands.
// An invalid list is refused whole and the previous list stays in place.
func (a *App) SetOpenWithCommands(cmds []openwith.Command) ([]openwith.Command, error) {
	list, err := openwith.ValidateList(cmds)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.openWith = list
	a.mu.Unlock()
	return list, nil
}

// OpenWithPreview is the exact result of rendering a command with test values.
type OpenWithPreview struct {
	Kind string   `json:"kind"`
	Argv []string `json:"argv,omitempty"`
	URL  string   `json:"url,omitempty"`
}

// PreviewOpenWith renders a command, saved or not, with a test address and
// hostname so the technician sees the exact argument vector or URL. It starts
// nothing. The test values go through the same validation as a device's.
func (a *App) PreviewOpenWith(cmd openwith.Command, ip, hostname string) (OpenWithPreview, error) {
	r, err := openwith.Render(cmd, ip, hostname)
	if err != nil {
		return OpenWithPreview{}, err
	}
	return OpenWithPreview(r), nil
}

// OpenWithOption is one saved command and whether it can be used for a device.
type OpenWithOption struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Available bool   `json:"available"`
	// Reason says why an unavailable command cannot be used for this device.
	Reason string `json:"reason,omitempty"`
}

// OpenWithOptions lists the saved commands for one result row. A command that
// uses {hostname} is unavailable when the device has no valid hostname.
func (a *App) OpenWithOptions(ip string) ([]OpenWithOption, error) {
	a.mu.Lock()
	cmds := append([]openwith.Command(nil), a.openWith...)
	host, ok := a.hostLocked(ip)
	a.mu.Unlock()
	if !ok {
		return nil, errors.New("that address is not in the results")
	}
	out := make([]OpenWithOption, 0, len(cmds))
	for _, c := range cmds {
		o := OpenWithOption{Name: c.Name, Kind: c.Kind, Available: true}
		if _, err := openwith.Render(c, host.IP, deviceHostname(host)); err != nil {
			o.Available, o.Reason = false, err.Error()
		}
		out = append(out, o)
	}
	return out, nil
}

// OpenWith runs one saved command for one result row. It is an explicit
// action: nothing calls it except the technician's menu choice.
func (a *App) OpenWith(name, ip string, allowPublic bool) (string, error) {
	if a.platform == nil {
		return "", errors.New("not available")
	}
	a.mu.Lock()
	var cmd openwith.Command
	found := false
	for _, c := range a.openWith {
		if c.Name == name {
			cmd, found = c, true
			break
		}
	}
	host, inResults := a.hostLocked(ip)
	publicOK := a.allowPub || allowPublic
	a.mu.Unlock()
	if !found {
		return "", errors.New("that command is not saved")
	}
	if !inResults {
		return "", errors.New("that address is not in the results")
	}
	addr, err := netip.ParseAddr(host.IP)
	if err != nil {
		return "", errors.New("a valid IP address is required")
	}
	if !targets.IsInternal(addr.Unmap()) && !publicOK {
		return "", errors.New("public address space needs authorization for this action")
	}
	r, err := openwith.Render(cmd, host.IP, deviceHostname(host))
	if err != nil {
		return "", err
	}
	if r.Kind == openwith.KindURL {
		a.platform.OpenURL(r.URL)
	} else if err := a.platform.LaunchProgram(r.Argv); err != nil {
		return "", err
	}
	return "Opened " + cmd.Name + " for " + host.IP, nil
}

// hostLocked finds a result by address. The caller holds a.mu.
func (a *App) hostLocked(ip string) (scan.Host, bool) {
	for _, h := range a.hosts {
		if h.IP == ip {
			return h, true
		}
	}
	return scan.Host{}, false
}

// deviceHostname is the one name offered as {hostname}: the reverse DNS name
// if there is one, else the best name. It is validated by openwith, never
// cleaned up.
func deviceHostname(h scan.Host) string {
	if h.DNSName != "" {
		return h.DNSName
	}
	return h.Name
}
