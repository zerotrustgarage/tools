package probe

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// UserAgent identifies the scanner in the logs of every web server it
// touches, so a client's administrators can tell who was looking.
var UserAgent = "ZTG-Scanner/dev (+https://ztg.sh)"

// Service describes what answered on an open TCP port.
type Service struct {
	Port    int       `json:"port"`
	Name    string    `json:"name"`
	Product string    `json:"product,omitempty"`
	Banner  string    `json:"banner,omitempty"`
	HTTP    *HTTPInfo `json:"http,omitempty"`
	TLS     *TLSInfo  `json:"tls,omitempty"`
	// Favicon is set only on a web service whose /favicon.ico was fingerprinted.
	Favicon *FaviconInfo `json:"favicon,omitempty"`
}

// HTTPInfo is what the root page of a web server says about itself.
type HTTPInfo struct {
	Status    int    `json:"status"`
	Server    string `json:"server,omitempty"`
	Title     string `json:"title,omitempty"`
	PoweredBy string `json:"powered_by,omitempty"`
	// Realm is the Basic/Digest authentication realm, which embedded
	// devices often set to their model name.
	Realm    string `json:"realm,omitempty"`
	Location string `json:"location,omitempty"`
}

// TLSInfo describes the certificate a TLS service presented. The
// certificate is recorded, never trusted: verification is skipped because
// LAN devices almost always use self-signed certificates.
type TLSInfo struct {
	Version    string    `json:"version"`
	Subject    string    `json:"subject,omitempty"`
	Issuer     string    `json:"issuer,omitempty"`
	DNSNames   []string  `json:"dns_names,omitempty"`
	NotAfter   time.Time `json:"not_after"`
	SelfSigned bool      `json:"self_signed"`
	SHA256     string    `json:"sha256"`
}

// WellKnown names the service conventionally found on a port.
var WellKnown = map[int]string{
	21: "ftp", 22: "ssh", 23: "telnet", 25: "smtp", 53: "dns", 80: "http", 81: "http",
	88: "kerberos", 110: "pop3", 135: "msrpc", 139: "netbios-ssn", 143: "imap",
	389: "ldap", 443: "https", 445: "smb", 515: "lpd", 548: "afp", 554: "rtsp",
	587: "submission", 631: "ipp", 636: "ldaps", 902: "vmware-auth", 993: "imaps", 995: "pop3s",
	1433: "mssql", 1883: "mqtt", 3306: "mysql", 3389: "rdp", 5000: "http", 5001: "https",
	5357: "wsdapi", 5900: "vnc", 5985: "winrm", 5986: "winrm-https", 8000: "http",
	8006: "proxmox", 8008: "http", 8080: "http", 8081: "http", 8443: "https",
	8888: "http", 9100: "jetdirect", 9443: "https",
}

var (
	httpPorts   = map[int]bool{80: true, 81: true, 5000: true, 5357: true, 5985: true, 8000: true, 8008: true, 8080: true, 8081: true, 8888: true}
	tlsPorts    = map[int]bool{443: true, 636: true, 993: true, 995: true, 5001: true, 5986: true, 8006: true, 8443: true, 9443: true}
	bannerPorts = map[int]bool{21: true, 22: true, 23: true, 25: true, 110: true, 143: true, 587: true, 3306: true, 5900: true}
	webOverTLS  = map[int]bool{443: true, 5001: true, 5986: true, 8006: true, 8443: true, 9443: true}
)

// Grab identifies the service on an open port with at most one standard
// request: read the greeting, send one HTTP GET for "/", or complete one
// TLS handshake. It never sends credentials.
func Grab(ctx context.Context, addr netip.Addr, port int, timeout time.Duration) Service {
	s := Service{Port: port, Name: WellKnown[port]}
	if s.Name == "" {
		s.Name = "unknown"
	}
	switch {
	case tlsPorts[port]:
		grabTLS(ctx, addr, port, timeout, &s)
	case httpPorts[port]:
		grabHTTP(ctx, addr, port, timeout, &s)
	case bannerPorts[port]:
		grabBanner(ctx, addr, port, timeout, &s)
	case s.Name == "unknown":
		// Unrecognized port: services that greet first (SSH, SMTP, FTP on an
		// odd port) show themselves; a silent one may be a web interface.
		grabBanner(ctx, addr, port, timeout/2, &s)
		if s.Banner == "" {
			grabHTTP(ctx, addr, port, timeout, &s)
		} else if strings.HasPrefix(s.Banner, "SSH-") {
			s.Name = "ssh"
		}
	}
	return s
}

func grabBanner(ctx context.Context, addr netip.Addr, port int, timeout time.Duration, s *Service) {
	c, err := DialTCP(ctx, addr, port, timeout)
	if err != nil {
		return
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 512)
	n, _ := io.ReadAtLeast(c, buf, 1)
	s.Banner = printable(buf[:n])
	if strings.HasPrefix(s.Banner, "SSH-") {
		// "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13" -> product after the second dash.
		if parts := strings.SplitN(s.Banner, "-", 3); len(parts) == 3 {
			s.Product = parts[2]
		}
	}
	if strings.HasPrefix(s.Banner, "RFB ") {
		s.Product = s.Banner
	}
}

func grabHTTP(ctx context.Context, addr netip.Addr, port int, timeout time.Duration, s *Service) {
	c, err := DialTCP(ctx, addr, port, timeout)
	if err != nil {
		return
	}
	defer c.Close()
	info, err := httpGet(c, addr, timeout)
	if err != nil {
		return
	}
	s.Name = "http"
	s.HTTP = info
	s.Product = info.Server
}

func grabTLS(ctx context.Context, addr netip.Addr, port int, timeout time.Duration, s *Service) {
	raw, err := DialTCP(ctx, addr, port, timeout)
	if err != nil {
		return
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(timeout))
	c := tls.Client(raw, &tls.Config{
		InsecureSkipVerify: true, // why: inventory only; the certificate is recorded, not trusted
		MinVersion:         tls.VersionTLS10,
	})
	if err := c.HandshakeContext(ctx); err != nil {
		return
	}
	st := c.ConnectionState()
	s.TLS = describeTLS(st)
	if webOverTLS[port] {
		if info, err := httpGet(c, addr, timeout); err == nil {
			s.Name = "https"
			s.HTTP = info
			s.Product = info.Server
		}
	}
}

func describeTLS(st tls.ConnectionState) *TLSInfo {
	info := &TLSInfo{Version: tls.VersionName(st.Version)}
	if len(st.PeerCertificates) == 0 {
		return info
	}
	leaf := st.PeerCertificates[0]
	sum := sha256.Sum256(leaf.Raw)
	info.Subject = leaf.Subject.String()
	info.Issuer = leaf.Issuer.String()
	info.DNSNames = leaf.DNSNames
	info.NotAfter = leaf.NotAfter.UTC()
	info.SHA256 = hex.EncodeToString(sum[:])
	// Issuer equal to subject: the device signed its own certificate, the
	// norm for printers, NAS units, hypervisors and network gear.
	info.SelfSigned = bytes.Equal(leaf.RawIssuer, leaf.RawSubject)
	return info
}

var titleRE = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func httpGet(c net.Conn, addr netip.Addr, timeout time.Duration) (*HTTPInfo, error) {
	_ = c.SetDeadline(time.Now().Add(timeout))
	req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nUser-Agent: %s\r\nAccept: */*\r\nConnection: close\r\n\r\n", hostHeader(addr), UserAgent)
	if _, err := io.WriteString(c, req); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(io.LimitReader(c, 256*1024)), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	info := &HTTPInfo{
		Status:    resp.StatusCode,
		Server:    clip(resp.Header.Get("Server"), 120),
		PoweredBy: clip(resp.Header.Get("X-Powered-By"), 120),
		Location:  clip(resp.Header.Get("Location"), 200),
	}
	if m := titleRE.FindSubmatch(body); m != nil {
		info.Title = clip(strings.Join(strings.Fields(html.UnescapeString(string(m[1]))), " "), 120)
	}
	if auth := resp.Header.Get("WWW-Authenticate"); auth != "" {
		if i := strings.Index(strings.ToLower(auth), `realm="`); i >= 0 {
			rest := auth[i+7:]
			if j := strings.IndexByte(rest, '"'); j >= 0 {
				info.Realm = clip(rest[:j], 120)
			}
		}
	}
	return info, nil
}

// printable keeps the first line of a banner, without control characters
// or telnet negotiation bytes.
func printable(b []byte) string {
	var sb strings.Builder
	for _, r := range string(b) {
		if r == '\r' || r == '\n' {
			if sb.Len() > 0 {
				break
			}
			continue
		}
		if r >= 0x20 && r < 0x7f {
			sb.WriteRune(r)
		}
	}
	return clip(strings.TrimSpace(sb.String()), 200)
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
