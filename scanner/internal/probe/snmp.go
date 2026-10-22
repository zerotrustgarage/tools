package probe

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// SNMPCredentials are SNMPv3 user credentials for authenticated,
// encrypted (authPriv) read-only queries. They are held in memory for one
// scan only: every field is excluded from JSON and String redacts them,
// so they cannot reach an export or a log by accident.
type SNMPCredentials struct {
	Username       string `json:"-"`
	AuthProtocol   string `json:"-"` // SHA, SHA-224, SHA-256, SHA-384, SHA-512
	AuthPassphrase string `json:"-"`
	PrivProtocol   string `json:"-"` // AES, AES-192, AES-256
	PrivPassphrase string `json:"-"`
	Port           int    `json:"-"` // default 161
}

// String never prints secrets.
func (c SNMPCredentials) String() string {
	return fmt.Sprintf("SNMPv3 user %q (%s/%s, passphrases redacted)", c.Username, c.AuthProtocol, c.PrivProtocol)
}

var authProtocols = map[string]gosnmp.SnmpV3AuthProtocol{
	"SHA": gosnmp.SHA, "SHA-224": gosnmp.SHA224, "SHA-256": gosnmp.SHA256,
	"SHA-384": gosnmp.SHA384, "SHA-512": gosnmp.SHA512,
}

var privProtocols = map[string]gosnmp.SnmpV3PrivProtocol{
	"AES": gosnmp.AES, "AES-192": gosnmp.AES192, "AES-256": gosnmp.AES256,
}

// AuthProtocols and PrivProtocols list the accepted names, for the UI.
var (
	AuthProtocols = []string{"SHA", "SHA-224", "SHA-256", "SHA-384", "SHA-512"}
	PrivProtocols = []string{"AES", "AES-192", "AES-256"}
)

// Validate checks the credentials before a scan starts. Only authPriv with
// SHA-family authentication and AES privacy is accepted: SNMP v1 and v2c
// send community strings in cleartext, and MD5 and DES are broken.
func (c SNMPCredentials) Validate() error {
	if strings.TrimSpace(c.Username) == "" {
		return errors.New("SNMPv3 needs a username")
	}
	if _, ok := authProtocols[c.AuthProtocol]; !ok {
		return fmt.Errorf("SNMPv3 authentication must be one of %s (MD5 is not accepted)", strings.Join(AuthProtocols, ", "))
	}
	if _, ok := privProtocols[c.PrivProtocol]; !ok {
		return fmt.Errorf("SNMPv3 privacy must be one of %s (DES is not accepted)", strings.Join(PrivProtocols, ", "))
	}
	// RFC 3414 section 11.2 requires at least 8 characters.
	if len(c.AuthPassphrase) < 8 || len(c.PrivPassphrase) < 8 {
		return errors.New("SNMPv3 passphrases must be at least 8 characters")
	}
	return nil
}

// SNMPInfo is what a device reports about itself over SNMP.
type SNMPInfo struct {
	SysName      string `json:"sys_name,omitempty"`
	SysDescr     string `json:"sys_descr,omitempty"`
	SysObjectID  string `json:"sys_object_id,omitempty"`
	Enterprise   string `json:"enterprise,omitempty"` // organization owning the sysObjectID arc, when known
	SysLocation  string `json:"sys_location,omitempty"`
	SysContact   string `json:"sys_contact,omitempty"`
	UptimeSecs   int64  `json:"uptime_seconds,omitempty"`
	Interfaces   int    `json:"interfaces,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	Serial       string `json:"serial,omitempty"`
	Hardware     string `json:"hardware_rev,omitempty"`
	Firmware     string `json:"firmware_rev,omitempty"`
	Software     string `json:"software_rev,omitempty"`
}

// OIDs read. All are standard MIB-II, ENTITY-MIB (RFC 6933) or Printer-MIB
// (RFC 3805) objects; nothing vendor-specific and nothing writable is touched.
const (
	oidSysDescr      = ".1.3.6.1.2.1.1.1.0"
	oidSysObjectID   = ".1.3.6.1.2.1.1.2.0"
	oidSysUpTime     = ".1.3.6.1.2.1.1.3.0"
	oidSysContact    = ".1.3.6.1.2.1.1.4.0"
	oidSysName       = ".1.3.6.1.2.1.1.5.0"
	oidSysLocation   = ".1.3.6.1.2.1.1.6.0"
	oidIfNumber      = ".1.3.6.1.2.1.2.1.0"
	oidPrinterSerial = ".1.3.6.1.2.1.43.5.1.1.17.1"
	oidEntClass      = ".1.3.6.1.2.1.47.1.1.1.1.5"
	oidEntPhysical   = ".1.3.6.1.2.1.47.1.1.1.1"
)

// entPhysicalClass chassis(3) is the box itself; stack(11) is a stack of them.
const entClassChassis = 3

// entPhysical columns read for the chassis entity.
var entColumns = map[int]func(*SNMPInfo, string){
	8:  func(i *SNMPInfo, v string) { i.Hardware = v },
	9:  func(i *SNMPInfo, v string) { i.Firmware = v },
	10: func(i *SNMPInfo, v string) { i.Software = v },
	11: func(i *SNMPInfo, v string) { i.Serial = v },
	12: func(i *SNMPInfo, v string) { i.Manufacturer = v },
	13: func(i *SNMPInfo, v string) { i.Model = v },
}

// snmpClient is the part of gosnmp the probe uses, so the query logic can
// be tested without a network.
type snmpClient interface {
	Get(oids []string) (*gosnmp.SnmpPacket, error)
	Walk(root string, fn gosnmp.WalkFunc) error
}

// SNMPQuery reads identity and inventory objects from addr with SNMPv3
// authPriv. It only issues GET and GETNEXT requests.
func SNMPQuery(ctx context.Context, addr netip.Addr, cred SNMPCredentials, timeout time.Duration) (*SNMPInfo, error) {
	if err := cred.Validate(); err != nil {
		return nil, err
	}
	port := cred.Port
	if port == 0 {
		port = 161
	}
	g := &gosnmp.GoSNMP{
		Target:        addr.String(),
		Port:          uint16(port),
		Version:       gosnmp.Version3,
		Transport:     "udp",
		Timeout:       timeout,
		Retries:       1,
		Context:       ctx,
		MaxOids:       gosnmp.MaxOids,
		SecurityModel: gosnmp.UserSecurityModel,
		MsgFlags:      gosnmp.AuthPriv,
		SecurityParameters: &gosnmp.UsmSecurityParameters{
			UserName:                 cred.Username,
			AuthenticationProtocol:   authProtocols[cred.AuthProtocol],
			AuthenticationPassphrase: cred.AuthPassphrase,
			PrivacyProtocol:          privProtocols[cred.PrivProtocol],
			PrivacyPassphrase:        cred.PrivPassphrase,
		},
	}
	if err := g.Connect(); err != nil {
		return nil, err
	}
	defer g.Conn.Close()
	return querySNMP(g)
}

func querySNMP(c snmpClient) (*SNMPInfo, error) {
	pkt, err := c.Get([]string{oidSysDescr, oidSysObjectID, oidSysUpTime, oidSysContact, oidSysName, oidSysLocation, oidIfNumber})
	if err != nil {
		return nil, err
	}
	info := &SNMPInfo{}
	for _, v := range pkt.Variables {
		switch v.Name {
		case oidSysDescr:
			info.SysDescr = snmpString(v, 300)
		case oidSysObjectID:
			info.SysObjectID = snmpString(v, 120)
			info.Enterprise = EnterpriseName(info.SysObjectID)
		case oidSysUpTime:
			if t, ok := snmpInt(v); ok {
				info.UptimeSecs = t / 100 // TimeTicks are hundredths of a second
			}
		case oidSysContact:
			info.SysContact = snmpString(v, 120)
		case oidSysName:
			info.SysName = snmpString(v, 120)
		case oidSysLocation:
			info.SysLocation = snmpString(v, 120)
		case oidIfNumber:
			if n, ok := snmpInt(v); ok {
				info.Interfaces = int(n)
			}
		}
	}
	if info.SysDescr == "" && info.SysObjectID == "" && info.SysName == "" {
		return nil, errors.New("no system information returned")
	}

	// ENTITY-MIB: find the first chassis entity, then read its columns.
	// Optional; many devices do not implement it.
	chassis := ""
	stop := errors.New("found")
	_ = c.Walk(oidEntClass, func(v gosnmp.SnmpPDU) error {
		if n, ok := snmpInt(v); ok && n == entClassChassis {
			chassis = strings.TrimPrefix(v.Name, oidEntClass+".")
			return stop
		}
		return nil
	})
	if chassis != "" && !strings.Contains(chassis, ".") {
		oids := make([]string, 0, len(entColumns))
		cols := map[string]func(*SNMPInfo, string){}
		for col, set := range entColumns {
			oid := fmt.Sprintf("%s.%d.%s", oidEntPhysical, col, chassis)
			oids = append(oids, oid)
			cols[oid] = set
		}
		if pkt, err := c.Get(oids); err == nil {
			for _, v := range pkt.Variables {
				if set := cols[v.Name]; set != nil {
					if s := snmpString(v, 120); s != "" {
						set(info, s)
					}
				}
			}
		}
	}

	// Printer-MIB serial number, for printers without ENTITY-MIB.
	if info.Serial == "" {
		if pkt, err := c.Get([]string{oidPrinterSerial}); err == nil && len(pkt.Variables) == 1 {
			info.Serial = snmpString(pkt.Variables[0], 120)
		}
	}
	return info, nil
}

func snmpString(v gosnmp.SnmpPDU, max int) string {
	var s string
	switch v.Type {
	case gosnmp.OctetString:
		b, _ := v.Value.([]byte)
		s = snmpText(b)
	case gosnmp.ObjectIdentifier:
		s, _ = v.Value.(string)
	default:
		return ""
	}
	return clip(s, max)
}

// snmpText keeps every line of a value (multi-line sysDescr is common),
// joined with spaces, without control characters.
func snmpText(b []byte) string {
	var sb strings.Builder
	for _, r := range string(b) {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			sb.WriteByte(' ')
		case r >= 0x20 && r != 0x7f:
			sb.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}

func snmpInt(v gosnmp.SnmpPDU) (int64, bool) {
	switch v.Type {
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Counter64, gosnmp.Uinteger32:
		return gosnmp.ToBigInt(v.Value).Int64(), true
	}
	return 0, false
}

// enterprises maps IANA private enterprise numbers to organizations, for
// the vendors technicians meet most often on client networks.
var enterprises = map[string]string{
	"9": "Cisco", "11": "Hewlett-Packard", "43": "3Com", "171": "D-Link", "253": "Xerox", "311": "Microsoft",
	"367": "Ricoh", "641": "Lexmark", "674": "Dell", "1347": "Kyocera", "1602": "Canon", "2011": "Huawei",
	"2435": "Brother", "2636": "Juniper Networks", "3902": "ZTE", "4526": "Netgear", "6574": "Synology",
	"6876": "VMware", "8072": "Net-SNMP (Linux or Unix)", "8741": "SonicWall", "11863": "TP-Link",
	"12356": "Fortinet", "14988": "MikroTik", "24681": "QNAP", "25461": "Palo Alto Networks",
	"30065": "Arista Networks", "41112": "Ubiquiti",
}

// EnterpriseName returns the organization that owns a sysObjectID's
// enterprise arc (1.3.6.1.4.1.N), or "".
func EnterpriseName(oid string) string {
	const arc = "1.3.6.1.4.1."
	oid = strings.TrimPrefix(oid, ".")
	if !strings.HasPrefix(oid, arc) {
		return ""
	}
	n, _, _ := strings.Cut(oid[len(arc):], ".")
	return enterprises[n]
}
