package classify

import (
	"testing"
	"time"
)

func ports(p ...int) map[int]bool {
	m := map[int]bool{}
	for _, x := range p {
		m[x] = true
	}
	return m
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		e    Evidence
		want string
	}{
		{"dc", Evidence{OpenPorts: ports(53, 88, 135, 389, 445)}, "Domain controller"},
		{"dc via netbios", Evidence{NetBIOS: true, NetBIOSGroups: []byte{0x00, 0x1c}, OpenPorts: ports()}, "Domain controller"},
		{"printer mdns", Evidence{MDNSServices: []string{"_ipp._tcp"}, OpenPorts: ports(80)}, "Printer"},
		{"printer 9100", Evidence{Vendor: "Brother Industries, LTD.", OpenPorts: ports(80, 9100)}, "Printer"},
		{"hp printer", Evidence{Vendor: "HP Inc.", OpenPorts: ports(631)}, "Printer"},
		{"camera vendor", Evidence{Vendor: "Hangzhou Hikvision Digital Technology Co.,Ltd.", OpenPorts: ports(80, 554)}, "IP camera / NVR"},
		{"proxmox", Evidence{OpenPorts: ports(22, 8006)}, "Hypervisor"},
		{"synology", Evidence{HTTPText: []string{"Synology DiskStation"}, OpenPorts: ports(5000, 5001)}, "NAS / storage"},
		{"fortigate", Evidence{Vendor: "Fortinet, Inc.", OpenPorts: ports(443)}, "Firewall"},
		{"igd", Evidence{UPnPType: "urn:schemas-upnp-org:device:InternetGatewayDevice:1", OpenPorts: ports(80)}, "Router / gateway"},
		{"unifi switch", Evidence{Vendor: "Ubiquiti Inc", OpenPorts: ports(22)}, "Network device"},
		{"yealink", Evidence{Vendor: "Yealink(Xiamen) Network Technology Co.,Ltd.", OpenPorts: ports(80)}, "VoIP phone"},
		{"chromecast", Evidence{MDNSServices: []string{"_googlecast._tcp"}, OpenPorts: ports(8008)}, "Media device"},
		{"esp32", Evidence{Vendor: "Espressif Inc.", OpenPorts: ports(80)}, "IoT device"},
		{"windows", Evidence{OpenPorts: ports(135, 139, 445, 3389), TTL: 128}, "Windows host"},
		{"mac", Evidence{Vendor: "Apple, Inc.", OpenPorts: ports()}, "Apple device"},
		{"linux", Evidence{OpenPorts: ports(22), TTL: 64}, "Linux / Unix host"},
		{"phone", Evidence{MACRandomized: true, OpenPorts: ports()}, "Private-MAC device (phone, laptop or VM)"},
		{"nothing", Evidence{OpenPorts: ports()}, "Unknown"},
	}
	for _, c := range cases {
		got := Classify(&c.e)
		if got.Type != c.want {
			t.Errorf("%s: got %q (%s), want %q", c.name, got.Type, got.Reason, c.want)
		}
		if got.Reason == "" {
			t.Errorf("%s: empty reason", c.name)
		}
	}
}

func TestFindings(t *testing.T) {
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	e := Evidence{
		Vendor:       "Zhejiang Dahua Technology Co., Ltd.",
		OpenPorts:    ports(21, 23, 80),
		HTTPText:     []string{"WEB SERVICE login"},
		CertExpiries: []time.Time{now.AddDate(0, -1, 0)},
	}
	got := map[string]string{}
	for _, f := range Findings(&e, now) {
		got[f.Code] = f.Severity
	}
	for code, sev := range map[string]string{"ndaa-889": "high", "telnet": "high", "ftp": "medium", "http-admin": "medium", "cert-expired": "info"} {
		if got[code] != sev {
			t.Errorf("%s: got severity %q, want %q (all: %v)", code, got[code], sev, got)
		}
	}
	if f := Findings(&Evidence{Vendor: "Intel Corporate", OpenPorts: ports(443)}, now); len(f) != 0 {
		t.Errorf("clean host flagged: %v", f)
	}
}

func TestSNMPEvidence(t *testing.T) {
	if got := Classify(&Evidence{SNMPMaker: " Fortinet", OpenPorts: ports()}); got.Type != "Firewall" {
		t.Fatalf("SNMP enterprise Fortinet: %+v", got)
	}
	if got := Classify(&Evidence{SNMPDescr: "Synology DiskStation DS920+", OpenPorts: ports()}); got.Type != "NAS / storage" {
		t.Fatalf("SNMP sysDescr Synology: %+v", got)
	}
	found := false
	for _, f := range Findings(&Evidence{SNMPMaker: "Huawei Technologies Co., Ltd. Huawei", OpenPorts: ports()}, time.Now()) {
		found = found || f.Code == "ndaa-889"
	}
	if !found {
		t.Fatal("NDAA vendor reported over SNMP not flagged")
	}
}
