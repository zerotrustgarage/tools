package openwith

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const putty = `C:\Program Files\PuTTY\putty.exe`

func TestRenderProgramPlaceholders(t *testing.T) {
	ssh := Command{Name: "PuTTY", Kind: KindProgram, Program: putty, Args: []string{"-ssh", "admin@{hostname}", "-P", "22"}}
	rdp := Command{Name: "RDP", Kind: KindProgram, Program: `C:\Windows\System32\mstsc.exe`, Args: []string{"/v:{ip}"}}
	both := Command{Name: "Both", Kind: KindProgram, Program: putty, Args: []string{"{ip}", "{hostname}", "{ip}:{hostname}"}}
	none := Command{Name: "Plain", Kind: KindProgram, Program: putty}
	tests := []struct {
		name     string
		cmd      Command
		ip, host string
		want     []string
	}{
		{"hostname", ssh, "10.0.0.5", "srv-01.lab.lan", []string{putty, "-ssh", "admin@srv-01.lab.lan", "-P", "22"}},
		{"ipv4", rdp, "192.168.1.20", "", []string{`C:\Windows\System32\mstsc.exe`, "/v:192.168.1.20"}},
		{"ipv6 canonical", rdp, "FE80:0:0:0:0:0:0:1", "", []string{`C:\Windows\System32\mstsc.exe`, "/v:fe80::1"}},
		{"ipv6 loopback", rdp, "0:0:0:0:0:0:0:1", "", []string{`C:\Windows\System32\mstsc.exe`, "/v:::1"}},
		{"4in6 unmapped", rdp, "::ffff:10.0.0.9", "", []string{`C:\Windows\System32\mstsc.exe`, "/v:10.0.0.9"}},
		{"both in one arg", both, "10.1.2.3", "Host1", []string{putty, "10.1.2.3", "Host1", "10.1.2.3:Host1"}},
		{"no placeholders", none, "10.0.0.5", "", []string{putty}},
		{"hostname unused is not examined", rdp, "10.0.0.5", "bad host; rm", []string{`C:\Windows\System32\mstsc.exe`, "/v:10.0.0.5"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Render(tc.cmd, tc.ip, tc.host)
			if err != nil {
				t.Fatal(err)
			}
			if r.Kind != KindProgram || !reflect.DeepEqual(r.Argv, tc.want) {
				t.Fatalf("got %q want %q", r.Argv, tc.want)
			}
		})
	}
}

func TestRenderRefusesHostileValues(t *testing.T) {
	ssh := Command{Name: "SSH", Kind: KindProgram, Program: putty, Args: []string{"{hostname}"}}
	web := Command{Name: "Web", Kind: KindURL, URL: "https://rmm.example.com/c?h={hostname}"}
	hostile := []string{
		"", "host name", "host\tname", "host\nname", `ho"st`, "ho'st", "a&b", "a;b", "a|b", "a$b", "a`b", "a(b)", "a<b", "a%20b",
		"a/b", `a\b`, "a:b", "a@b", "a=b", "-oProxyCommand=calc", "-host", "--help", "/c", "host-", "-", ".", "..", ".host", "host.", "ho..st",
		"hôst", "host\u202e", "xn--\u00e9", "ｈｏｓｔ", "10.0.0.5", "1", "host.123", strings.Repeat("a", 64), strings.Repeat("a.", 130) + "com",
		"host\x00", "host\u200b",
	}
	for _, h := range hostile {
		for _, c := range []Command{ssh, web} {
			_, err := Render(c, "10.0.0.5", h)
			var ue *UnavailableError
			if !errors.As(err, &ue) {
				t.Fatalf("hostname %q on %s: want unavailable, got %v", h, c.Name, err)
			}
		}
	}
	good := []string{"a", "host", "Host-1", "srv.lab.lan", "a-b.c-d.example", "FRONTDESK-PC", "printer.local", strings.Repeat("a", 63), "h.example1", "7up"}
	for _, h := range good {
		r, err := Render(ssh, "10.0.0.5", h)
		if err != nil || r.Argv[1] != h {
			t.Fatalf("hostname %q refused: %v", h, err)
		}
		if strings.HasPrefix(r.Argv[1], "-") {
			t.Fatalf("option-like argument rendered from %q", h)
		}
	}
	for _, ip := range []string{"", "300.1.1.1", "10.0.0.5 ", "10.0.0.5; calc", "fe80::1%eth0", "0.0.0.0", "::", "224.0.0.1", "ff02::1", "-1", "host"} {
		if _, err := Render(Command{Name: "RDP", Kind: KindProgram, Program: putty, Args: []string{"{ip}"}}, ip, "h"); err == nil {
			t.Fatalf("ip %q accepted", ip)
		}
	}
}

func TestPlaceholderNeverSplitsOrAddsArguments(t *testing.T) {
	c := Command{Name: "x", Kind: KindProgram, Program: putty, Args: []string{"-l", "{hostname}", "{ip}"}}
	// A value that would be two shell words, or contain a template, never gets this far.
	for _, h := range []string{"a b", "a{ip}", "{ip}", "{hostname}"} {
		if _, err := Render(c, "10.0.0.5", h); err == nil {
			t.Fatalf("%q accepted", h)
		}
	}
	r, err := Render(c, "10.0.0.5", "srv")
	if err != nil || len(r.Argv) != 4 {
		t.Fatalf("argv count changed: %q %v", r.Argv, err)
	}
}

func TestRenderURL(t *testing.T) {
	tests := []struct {
		name, tmpl, ip, host, want string
	}{
		{"query hostname", "https://rmm.example.com/console?host={hostname}", "10.0.0.5", "srv-01.lab.lan", "https://rmm.example.com/console?host=srv-01.lab.lan"},
		{"query ipv4 and host", "https://rmm.example.com/c?ip={ip}&name={hostname}", "10.0.0.5", "srv", "https://rmm.example.com/c?ip=10.0.0.5&name=srv"},
		{"ipv6 escaped in query", "https://rmm.example.com/c?ip={ip}", "fe80::1", "", "https://rmm.example.com/c?ip=fe80%3A%3A1"},
		{"path", "https://rmm.example.com/devices/{hostname}/open", "10.0.0.5", "Srv1", "https://rmm.example.com/devices/Srv1/open"},
		{"ipv6 in path escaped", "https://rmm.example.com/d/{ip}", "2001:db8::5", "", "https://rmm.example.com/d/2001:db8::5"},
		{"fragment", "https://rmm.example.com/#/dev/{ip}", "fe80::1", "", "https://rmm.example.com/#/dev/fe80%3A%3A1"},
		{"port in authority", "https://rmm.example.com:8443/c?h={hostname}", "10.0.0.5", "a", "https://rmm.example.com:8443/c?h=a"},
		{"no path", "https://rmm.example.com", "10.0.0.5", "", "https://rmm.example.com"},
		{"literal percent kept", "https://rmm.example.com/a%20b?h={hostname}", "10.0.0.5", "a", "https://rmm.example.com/a%20b?h=a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Render(Command{Name: "w", Kind: KindURL, URL: tc.tmpl}, tc.ip, tc.host)
			if err != nil {
				t.Fatal(err)
			}
			if r.Kind != KindURL || r.URL != tc.want || r.Argv != nil {
				t.Fatalf("got %+v want %s", r, tc.want)
			}
		})
	}
}

func TestURLTemplateRefusals(t *testing.T) {
	bad := []string{
		"", "http://rmm.example.com/c", "ftp://x/", "file:///c:/windows", "javascript:alert(1)", "//rmm.example.com/", "HTTPS://rmm.example.com/x",
		"https://", "https:///path", "https://{hostname}.example.com/", "https://rmm.example.com{hostname}/", "https://{ip}/", "https://user@rmm.example.com/", "https://user:pw@rmm.example.com/",
		"https://rmm.example.com@evil.example/", `https://rmm.example.com\@evil/`, "https://rmm example.com/", "https://rmm.example.com/a b", "https://rmm.example.com/a\nb",
		"https://rmm.example.com/<x>", "https://rmm.example.com/c?h={host}", "https://rmm.example.com/c?h={ip", "https://rmm.example.com/c?h={password}",
		"https://rmm.example.com/c?h={IP}", "https://rmm.example.com/c?password=abc", "https://rmm.example.com/c?x=1&token=abc", "https://rmm.example.com/c?api_key=abc&h={ip}",
		"https://rmm.example.com/é", "https://rmm.example.com/" + strings.Repeat("a", MaxURLLen),
	}
	for _, u := range bad {
		if _, err := ValidateCommand(Command{Name: "w", Kind: KindURL, URL: u}); err == nil {
			t.Fatalf("URL template %q accepted", u)
		}
	}
	// A hostname can never redirect a URL to another host: it cannot even contain a delimiter.
	c := Command{Name: "w", Kind: KindURL, URL: "https://rmm.example.com/c?h={hostname}"}
	for _, h := range []string{"evil.example/", "a@evil", "a#b", "a?b", "a.example:443"} {
		if _, err := Render(c, "10.0.0.5", h); err == nil {
			t.Fatalf("hostname %q accepted in URL", h)
		}
	}
}

func TestProgramPathAndShellGuard(t *testing.T) {
	good := []string{putty, `C:\Windows\System32\mstsc.exe`, `d:\tools\x.EXE`, `C:\a b\c.exe`, `C:\x.exe`}
	for _, p := range good {
		if err := ValidateProgram(p); err != nil {
			t.Fatalf("%q refused: %v", p, err)
		}
	}
	bad := []string{
		"", "putty.exe", `putty`, `.\putty.exe`, `..\putty.exe`, `\Windows\putty.exe`, `\\server\share\putty.exe`, `\\?\C:\putty.exe`, `\\.\pipe\x`,
		`C:putty.exe`, `C:/Tools/putty.exe`, `C:\Tools\..\putty.exe`, `C:\Tools\.\putty.exe`, `C:\\Tools\putty.exe`, `C:\Tools\`, `C:\Tools\putty.exe `,
		`C:\Tools\putty.exe.`, `C:\Tools\putty.bat`, `C:\Tools\putty.cmd`, `C:\Tools\putty.ps1`, `C:\Tools\putty.vbs`, `C:\Tools\putty.lnk`, `C:\Tools\putty.com`, `C:\Tools\putty`,
		`C:\Tools\.exe`, `C:\Tools\put"ty.exe`, `C:\Tools\put*.exe`, `C:\Tools\put?.exe`, `C:\%ProgramFiles%\putty.exe`, `C:\Tools\a:stream.exe`, "C:\\Tools\\a\x00.exe",
		`C:\Tools\pütty.exe`, `1:\putty.exe`, strings.Repeat("a", MaxProgramLen) + `.exe`,
		`C:\Windows\System32\cmd.exe`, `C:\Windows\System32\CMD.EXE`, `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, `C:\Program Files\PowerShell\7\pwsh.exe`,
		`C:\Windows\System32\wscript.exe`, `C:\Windows\System32\cscript.exe`, `C:\Windows\System32\mshta.exe`, `C:\Windows\System32\wsl.exe`, `C:\Git\bin\bash.exe`, `C:\Git\bin\sh.exe`,
		`C:\Windows\System32\rundll32.exe`, `C:\Windows\System32\cmdkey.exe`, `C:\Windows\System32\runas.exe`,
	}
	for _, p := range bad {
		if err := ValidateProgram(p); err == nil {
			t.Fatalf("program %q accepted", p)
		}
	}
	// The guard also applies through ValidateCommand and Render.
	if _, err := Render(Command{Name: "x", Kind: KindProgram, Program: `C:\Windows\System32\cmd.exe`, Args: []string{"/c", "ping", "{ip}"}}, "10.0.0.5", ""); err == nil {
		t.Fatal("cmd.exe rendered")
	}
}

func TestCredentialsRefused(t *testing.T) {
	bad := []string{
		"{password}", "-pw", "-pw=secret", "--password", "--password=x", "-password", "/pass:x", "/password:x", "password=x", "token=abc", "--token", "-secret",
		"--api-key=abc", "apikey=abc", "access_token=abc", "-passphrase", "Password:abc",
	}
	for _, a := range bad {
		if _, err := ValidateCommand(Command{Name: "x", Kind: KindProgram, Program: putty, Args: []string{"-ssh", a, "{ip}"}}); err == nil {
			t.Fatalf("argument %q accepted", a)
		}
	}
	for _, a := range []string{"-ssh", "-P", "22", "-l", "admin", "user@{hostname}", "--passive", "/v:{ip}", "-i", `C:\keys\id.ppk`, "{ip}"} {
		if _, err := ValidateCommand(Command{Name: "x", Kind: KindProgram, Program: putty, Args: []string{a}}); err != nil {
			t.Fatalf("argument %q refused: %v", a, err)
		}
	}
	for _, a := range []string{"{pass}", "{user}", "{IP}", "{ip", "ip}", "{}", "{ hostname }", "{hostname}}", "{{ip}}", "{mac}", "{vendor}", "{name}"} {
		if _, err := ValidateCommand(Command{Name: "x", Kind: KindProgram, Program: putty, Args: []string{a}}); err == nil {
			t.Fatalf("template %q accepted", a)
		}
	}
}

func TestCommandBoundsAndList(t *testing.T) {
	base := Command{Name: "x", Kind: KindProgram, Program: putty, Args: []string{"{ip}"}}
	with := func(f func(*Command)) Command {
		c := base
		c.Args = append([]string(nil), base.Args...)
		f(&c)
		return c
	}
	bad := map[string]Command{
		"empty name":   with(func(c *Command) { c.Name = "  " }),
		"long name":    with(func(c *Command) { c.Name = strings.Repeat("n", MaxNameLen+1) }),
		"control name": with(func(c *Command) { c.Name = "a\nb" }),
		"bad kind":     with(func(c *Command) { c.Kind = "shell" }),
		"empty kind":   with(func(c *Command) { c.Kind = "" }),
		"too many args": with(func(c *Command) {
			c.Args = make([]string, MaxArgs+1)
			for i := range c.Args {
				c.Args[i] = "a"
			}
		}),
		"long arg":       with(func(c *Command) { c.Args = []string{strings.Repeat("a", MaxArgLen+1)} }),
		"empty arg":      with(func(c *Command) { c.Args = []string{""} }),
		"newline in arg": with(func(c *Command) { c.Args = []string{"a\nb"} }),
		"nul in arg":     with(func(c *Command) { c.Args = []string{"a\x00"} }),
	}
	for name, c := range bad {
		if _, err := ValidateCommand(c); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	atLimit := with(func(c *Command) {
		c.Name = strings.Repeat("é", MaxNameLen)
		c.Args = make([]string, MaxArgs)
		for i := range c.Args {
			c.Args[i] = strings.Repeat("a", MaxArgLen)
		}
	})
	if _, err := ValidateCommand(atLimit); err != nil {
		t.Fatalf("limits are inclusive: %v", err)
	}
	// Fields of the other kind are dropped, so they cannot ride along.
	got, err := ValidateCommand(Command{Name: " Web ", Kind: KindURL, URL: " https://rmm.example.com/ ", Program: putty, Args: []string{"x"}})
	if err != nil || got.Name != "Web" || got.URL != "https://rmm.example.com/" || got.Program != "" || got.Args != nil {
		t.Fatalf("%+v %v", got, err)
	}

	var list []Command
	for i := 0; i < MaxCommands; i++ {
		c := base
		c.Name = "cmd " + strings.Repeat("x", i)
		list = append(list, c)
	}
	if _, err := ValidateList(list); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateList(append(list, Command{Name: "one more", Kind: KindURL, URL: "https://rmm.example.com/"})); err == nil {
		t.Fatal("more than the maximum accepted")
	}
	if _, err := ValidateList([]Command{base, with(func(c *Command) { c.Name = "X" })}); err == nil {
		t.Fatal("duplicate names (ignoring case) accepted")
	}
	if _, err := ValidateList([]Command{base, with(func(c *Command) { c.Name = "y"; c.Kind = "nope" })}); err == nil {
		t.Fatal("invalid member accepted")
	}
}

// TestSettingsRoundTrip covers the JSON the UI stores in local storage and
// sends to the app: it must survive a round trip unchanged, and unknown
// device-shaped fields cannot appear in a command.
func TestSettingsRoundTrip(t *testing.T) {
	in := []Command{
		{Name: "PuTTY", Kind: KindProgram, Program: putty, Args: []string{"-ssh", "{hostname}"}},
		{Name: "Console", Kind: KindURL, URL: "https://rmm.example.com/c?ip={ip}"},
	}
	valid, err := ValidateList(in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	var back []Command
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	again, err := ValidateList(back)
	if err != nil || !reflect.DeepEqual(again, in) {
		t.Fatalf("round trip changed the list: %+v %v", again, err)
	}
	for _, key := range []string{`"password"`, `"id"`, `"shell"`} {
		if strings.Contains(string(b), key) {
			t.Fatalf("unexpected field %s in %s", key, b)
		}
	}
}

func TestUses(t *testing.T) {
	ip, host := Uses(Command{Kind: KindProgram, Args: []string{"-x", "{ip}"}})
	if !ip || host {
		t.Fatal(ip, host)
	}
	ip, host = Uses(Command{Kind: KindURL, URL: "https://a.example/{hostname}"})
	if ip || !host {
		t.Fatal(ip, host)
	}
}
