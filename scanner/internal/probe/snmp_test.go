package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

type fakeSNMP struct {
	values map[string]gosnmp.SnmpPDU
	walk   []gosnmp.SnmpPDU
	gets   [][]string
}

func (f *fakeSNMP) Get(oids []string) (*gosnmp.SnmpPacket, error) {
	f.gets = append(f.gets, oids)
	p := &gosnmp.SnmpPacket{}
	for _, o := range oids {
		if v, ok := f.values[o]; ok {
			p.Variables = append(p.Variables, v)
		} else {
			p.Variables = append(p.Variables, gosnmp.SnmpPDU{Name: o, Type: gosnmp.NoSuchObject})
		}
	}
	return p, nil
}

func (f *fakeSNMP) Walk(root string, fn gosnmp.WalkFunc) error {
	for _, v := range f.walk {
		if strings.HasPrefix(v.Name, root+".") {
			if err := fn(v); err != nil {
				return err
			}
		}
	}
	return nil
}

func str(oid, s string) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Name: oid, Type: gosnmp.OctetString, Value: []byte(s)}
}

func TestQuerySNMPSwitch(t *testing.T) {
	f := &fakeSNMP{
		values: map[string]gosnmp.SnmpPDU{
			oidSysDescr:    str(oidSysDescr, "Cisco IOS Software, C2960X Software\r\nVersion 15.2(7)E8\r\n\x00"),
			oidSysObjectID: {Name: oidSysObjectID, Type: gosnmp.ObjectIdentifier, Value: ".1.3.6.1.4.1.9.1.1208"},
			oidSysUpTime:   {Name: oidSysUpTime, Type: gosnmp.TimeTicks, Value: uint32(8640000)},
			oidSysName:     str(oidSysName, "core-sw-01"),
			oidSysLocation: str(oidSysLocation, "MDF"),
			oidIfNumber:    {Name: oidIfNumber, Type: gosnmp.Integer, Value: 30},
			// chassis is entity 1001
			oidEntPhysical + ".11.1001": str(oidEntPhysical+".11.1001", "FOC1234X0YZ"),
			oidEntPhysical + ".12.1001": str(oidEntPhysical+".12.1001", "Cisco Systems, Inc."),
			oidEntPhysical + ".13.1001": str(oidEntPhysical+".13.1001", "WS-C2960X-24PS-L"),
			oidEntPhysical + ".10.1001": str(oidEntPhysical+".10.1001", "15.2(7)E8"),
		},
		walk: []gosnmp.SnmpPDU{
			{Name: oidEntClass + ".1", Type: gosnmp.Integer, Value: 10}, // port
			{Name: oidEntClass + ".1001", Type: gosnmp.Integer, Value: 3},
			{Name: oidEntClass + ".1002", Type: gosnmp.Integer, Value: 3}, // second chassis ignored
		},
	}
	info, err := querySNMP(f)
	if err != nil {
		t.Fatal(err)
	}
	want := SNMPInfo{
		SysName: "core-sw-01", SysDescr: "Cisco IOS Software, C2960X Software Version 15.2(7)E8",
		SysObjectID: ".1.3.6.1.4.1.9.1.1208", Enterprise: "Cisco", SysLocation: "MDF",
		UptimeSecs: 86400, Interfaces: 30, Manufacturer: "Cisco Systems, Inc.", Model: "WS-C2960X-24PS-L",
		Serial: "FOC1234X0YZ", Software: "15.2(7)E8",
	}
	if *info != want {
		t.Fatalf("got  %+v\nwant %+v", *info, want)
	}
	for _, g := range f.gets {
		for _, o := range g {
			if o == oidPrinterSerial {
				t.Fatal("printer serial queried although ENTITY-MIB supplied one")
			}
		}
	}
}

func TestQuerySNMPPrinterFallback(t *testing.T) {
	f := &fakeSNMP{values: map[string]gosnmp.SnmpPDU{
		oidSysDescr:      str(oidSysDescr, "Brother NC-8300w"),
		oidSysObjectID:   {Name: oidSysObjectID, Type: gosnmp.ObjectIdentifier, Value: ".1.3.6.1.4.1.2435.2.3.9.1"},
		oidPrinterSerial: str(oidPrinterSerial, "U63881A1N123456"),
	}}
	info, err := querySNMP(f)
	if err != nil {
		t.Fatal(err)
	}
	if info.Serial != "U63881A1N123456" || info.Enterprise != "Brother" {
		t.Fatalf("got %+v", info)
	}
}

func TestQuerySNMPEmpty(t *testing.T) {
	if _, err := querySNMP(&fakeSNMP{values: map[string]gosnmp.SnmpPDU{}}); err == nil {
		t.Fatal("want error for an agent that returns nothing")
	}
}

func TestSNMPCredentialsValidate(t *testing.T) {
	good := SNMPCredentials{Username: "noc", AuthProtocol: "SHA-256", AuthPassphrase: "authpass1", PrivProtocol: "AES", PrivPassphrase: "privpass1"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*SNMPCredentials){
		"no user":     func(c *SNMPCredentials) { c.Username = " " },
		"md5":         func(c *SNMPCredentials) { c.AuthProtocol = "MD5" },
		"des":         func(c *SNMPCredentials) { c.PrivProtocol = "DES" },
		"short auth":  func(c *SNMPCredentials) { c.AuthPassphrase = "short" },
		"short priv":  func(c *SNMPCredentials) { c.PrivPassphrase = "short" },
		"no protocol": func(c *SNMPCredentials) { c.AuthProtocol = "" },
	}
	for name, mutate := range bad {
		c := good
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestSNMPCredentialsNeverLeak(t *testing.T) {
	c := SNMPCredentials{Username: "noc", AuthProtocol: "SHA", AuthPassphrase: "s3cret-auth", PrivProtocol: "AES", PrivPassphrase: "s3cret-priv"}
	b, _ := json.Marshal(c)
	wrapped, _ := json.Marshal(struct{ C SNMPCredentials }{c})
	for _, out := range []string{string(b), string(wrapped), c.String(), fmt.Sprint(c), fmt.Sprintf("%v %+v", c, c)} {
		if strings.Contains(out, "s3cret") {
			t.Fatalf("secret leaked: %s", out)
		}
	}
}

func TestEnterpriseName(t *testing.T) {
	for oid, want := range map[string]string{
		".1.3.6.1.4.1.9.1.1208": "Cisco", "1.3.6.1.4.1.12356.101.1.1": "Fortinet",
		".1.3.6.1.4.1.999999.1": "", ".1.3.6.1.2.1.1": "", "": "",
	} {
		if got := EnterpriseName(oid); got != want {
			t.Errorf("%q: %q, want %q", oid, got, want)
		}
	}
}

// TestSNMPQueryNetSNMP runs against a real net-snmp agent when snmpd is
// installed, proving SNMPv3 authPriv interoperability end to end.
func TestSNMPQueryNetSNMP(t *testing.T) {
	snmpd, err := exec.LookPath("snmpd")
	if err != nil {
		t.Skip("snmpd not installed")
	}
	ln, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.LocalAddr().(*net.UDPAddr).Port
	ln.Close()

	dir := t.TempDir()
	conf := filepath.Join(dir, "snmpd.conf")
	os.WriteFile(conf, []byte(fmt.Sprintf(`agentAddress udp:127.0.0.1:%d
createUser ztgtest SHA-256 "authpass-123" AES "privpass-456"
rouser ztgtest priv
sysName lab-switch-01
sysLocation Rack 3
sysContact noc@example.com
`, port)), 0o600)
	cmd := exec.Command(snmpd, "-f", "-C", "-c", conf, "-Lf", filepath.Join(dir, "log"))
	cmd.Env = append(os.Environ(), "SNMP_PERSISTENT_DIR="+dir)
	if err := cmd.Start(); err != nil {
		t.Skip("snmpd did not start:", err)
	}
	defer cmd.Process.Kill()

	cred := SNMPCredentials{Username: "ztgtest", AuthProtocol: "SHA-256", AuthPassphrase: "authpass-123", PrivProtocol: "AES", PrivPassphrase: "privpass-456", Port: port}
	var info *SNMPInfo
	for i := 0; i < 20; i++ { // wait for the agent to come up
		if info, err = SNMPQuery(context.Background(), loopback, cred, 500*time.Millisecond); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if info.SysName != "lab-switch-01" || info.SysLocation != "Rack 3" || info.Enterprise != "Net-SNMP (Linux or Unix)" || info.Interfaces < 1 {
		t.Fatalf("got %+v", info)
	}

	// Wrong passphrase: authentication fails, nothing is returned.
	cred.AuthPassphrase = "wrong-pass-1"
	if _, err := SNMPQuery(context.Background(), loopback, cred, 500*time.Millisecond); err == nil {
		t.Fatal("query with a wrong passphrase succeeded")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("unexpected cancel")
	}
}
