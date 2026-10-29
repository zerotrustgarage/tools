// Package openwith holds technician-defined "Open with" commands (slice SC10)
// and renders them for one scanned device.
//
// Commands come only from the technician's own settings. What a device
// reports (names, banners, UPnP text, SNMP strings, MAC vendors) is never part
// of the executable path or of the argument structure: a template is split
// into an argument vector once, and a placeholder is only ever substituted
// inside one element of that vector, so a value cannot add, remove or reorder
// arguments. No shell is involved anywhere.
//
// Two placeholders exist. {ip} is a parsed net/netip address in canonical
// form. {hostname} is a device-reported name that must pass a strict DNS name
// check; a device whose name fails it simply has no such command available.
// There is no credential placeholder, and templates that look like they carry
// a password or token are refused.
package openwith

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// Bounds on what a technician can store.
const (
	MaxCommands   = 20
	MaxNameLen    = 60
	MaxProgramLen = 260
	MaxArgs       = 16
	MaxArgLen     = 512
	MaxURLLen     = 2048
	MaxHostname   = 253
)

// Kinds of command.
const (
	KindProgram = "program"
	KindURL     = "url"
)

// Command is one saved "Open with" entry. A program command is an absolute
// path to an installed .exe plus one template per argument; a URL command is
// an https URL template opened in the default browser.
type Command struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Program string   `json:"program,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// Rendered is a command with its placeholders substituted for one device.
// For a program command Argv is the exact vector (Argv[0] is the executable);
// for a URL command URL is the exact address to open.
type Rendered struct {
	Kind string   `json:"kind"`
	Argv []string `json:"argv,omitempty"`
	URL  string   `json:"url,omitempty"`
}

// UnavailableError means a valid command cannot be used for this device,
// for example because its reported hostname is not a valid DNS name. The
// command is not changed or sanitized to fit.
type UnavailableError struct{ Reason string }

func (e *UnavailableError) Error() string { return e.Reason }

const (
	phIP       = "{ip}"
	phHostname = "{hostname}"
)

// Uses reports which placeholders a command's templates contain.
func Uses(c Command) (ip, hostname bool) {
	for _, t := range templates(c) {
		ip = ip || strings.Contains(t, phIP)
		hostname = hostname || strings.Contains(t, phHostname)
	}
	return
}

func templates(c Command) []string {
	if c.Kind == KindURL {
		return []string{c.URL}
	}
	return c.Args
}

// shellsAndScriptHosts are refused as the program: they would turn the
// argument vector back into a command line that something else parses.
// This is a guard against a mistake, not a sandbox: any installed program the
// technician chooses runs with that technician's rights.
var shellsAndScriptHosts = map[string]bool{
	"cmd.exe": true, "powershell.exe": true, "powershell_ise.exe": true, "pwsh.exe": true,
	"wscript.exe": true, "cscript.exe": true, "mshta.exe": true, "wsl.exe": true,
	"bash.exe": true, "sh.exe": true, "zsh.exe": true, "fish.exe": true,
	"rundll32.exe": true, "regsvr32.exe": true, "forfiles.exe": true, "pcalua.exe": true,
	"conhost.exe": true, "cmdkey.exe": true, "runas.exe": true,
}

// ValidateProgram checks an executable path: absolute, on a drive, no
// traversal, no UNC or device paths, .exe only, and not a shell or script host.
func ValidateProgram(p string) error {
	if p == "" {
		return errors.New("enter the full path of an installed program")
	}
	if len(p) > MaxProgramLen {
		return fmt.Errorf("the program path is longer than %d characters", MaxProgramLen)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`"<>|?*%/`, r) {
			return errors.New("the program path contains a character that is not allowed (use backslashes, no quotes, wildcards or variables)")
		}
		if r > 0x7e {
			return errors.New("the program path must use ASCII characters")
		}
	}
	if len(p) < 4 || !isLetter(p[0]) || p[1] != ':' || p[2] != '\\' {
		return errors.New(`the program path must be absolute and start with a drive, for example C:\Program Files\PuTTY\putty.exe`)
	}
	segments := strings.Split(p[3:], "\\")
	for _, s := range segments {
		switch {
		case s == "" || s == "." || s == "..":
			return errors.New("the program path must not contain empty, . or .. parts")
		case strings.ContainsRune(s, ':'):
			return errors.New("the program path must not contain a colon after the drive")
		case strings.HasSuffix(s, " ") || strings.HasSuffix(s, "."):
			return errors.New("a part of the program path ends with a space or dot")
		}
	}
	base := strings.ToLower(segments[len(segments)-1])
	if !strings.HasSuffix(base, ".exe") || base == ".exe" {
		return errors.New("the program must be an .exe file")
	}
	if shellsAndScriptHosts[base] {
		return fmt.Errorf("%s is a shell or script host and cannot be used; choose the program itself", base)
	}
	return nil
}

func isLetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

// credentialArg matches option names that carry a secret, for example PuTTY's
// -pw or --password=..., and key=value pairs such as password= or token=.
var credentialArg = regexp.MustCompile(`(?i)(^|[\s?&;])(?:[-/]{1,2})?(pw|pass|passwd|password|passphrase|pwd|secret|token|apikey|api[-_]key|access[-_]token)(=|:|$)`)

func looksLikeCredential(t string) bool { return credentialArg.MatchString(t) }

// checkTemplate rejects control characters, unknown placeholders and stray
// braces. It returns an error that names the first problem.
func checkTemplate(t string, max int) error {
	if len(t) > max {
		return fmt.Errorf("a template is longer than %d characters", max)
	}
	for _, r := range t {
		if r < 0x20 || r == 0x7f {
			return errors.New("templates must not contain control characters or line breaks")
		}
	}
	rest := t
	for {
		i := strings.IndexAny(rest, "{}")
		if i < 0 {
			break
		}
		switch {
		case strings.HasPrefix(rest[i:], phIP):
			rest = rest[i+len(phIP):]
		case strings.HasPrefix(rest[i:], phHostname):
			rest = rest[i+len(phHostname):]
		default:
			return errors.New("only {ip} and {hostname} are supported; there are no password or other placeholders")
		}
	}
	if looksLikeCredential(t) {
		return errors.New("this looks like it carries a password or token; Scanner never stores credentials, so let the program or site ask for it")
	}
	return nil
}

// ValidateCommand checks one command and returns it trimmed.
func ValidateCommand(c Command) (Command, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return c, errors.New("enter a name")
	}
	if len([]rune(c.Name)) > MaxNameLen {
		return c, fmt.Errorf("the name is longer than %d characters", MaxNameLen)
	}
	for _, r := range c.Name {
		if r < 0x20 || r == 0x7f {
			return c, errors.New("the name must not contain control characters")
		}
	}
	switch c.Kind {
	case KindProgram:
		c.URL = ""
		c.Program = strings.TrimSpace(c.Program)
		if err := ValidateProgram(c.Program); err != nil {
			return c, err
		}
		if len(c.Args) > MaxArgs {
			return c, fmt.Errorf("a command can have at most %d arguments", MaxArgs)
		}
		args := make([]string, len(c.Args))
		for i, a := range c.Args {
			if a == "" {
				return c, fmt.Errorf("argument %d is empty; remove the line", i+1)
			}
			if err := checkTemplate(a, MaxArgLen); err != nil {
				return c, fmt.Errorf("argument %d: %w", i+1, err)
			}
			args[i] = a
		}
		c.Args = args
		if len(c.Args) == 0 {
			c.Args = nil
		}
	case KindURL:
		c.Program, c.Args = "", nil
		c.URL = strings.TrimSpace(c.URL)
		if _, err := splitURL(c.URL); err != nil {
			return c, err
		}
		if err := checkTemplate(c.URL, MaxURLLen); err != nil {
			return c, err
		}
	default:
		return c, errors.New("the type must be program or url")
	}
	return c, nil
}

// ValidateList checks the whole saved list: at most MaxCommands, each valid,
// names unique ignoring case.
func ValidateList(cmds []Command) ([]Command, error) {
	if len(cmds) > MaxCommands {
		return nil, fmt.Errorf("you can save up to %d commands", MaxCommands)
	}
	out := make([]Command, 0, len(cmds))
	seen := map[string]bool{}
	for _, c := range cmds {
		v, err := ValidateCommand(c)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", strings.TrimSpace(c.Name), err)
		}
		k := strings.ToLower(v.Name)
		if seen[k] {
			return nil, fmt.Errorf("two commands are named %q", v.Name)
		}
		seen[k] = true
		out = append(out, v)
	}
	return out, nil
}

// ValidHostname checks a device-reported name against a strict DNS name
// syntax: ASCII letters, digits, hyphens and dots only; labels of 1 to 63
// characters that do not start or end with a hyphen; at most 253 characters;
// no trailing dot; and a last label that is not all digits (so a name can
// never be read as a numeric address).
func ValidHostname(h string) error {
	if h == "" {
		return errors.New("the device has no hostname")
	}
	if len(h) > MaxHostname {
		return errors.New("the device hostname is too long")
	}
	labels := strings.Split(h, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 {
			return errors.New("the device hostname is not a valid DNS name")
		}
		if l[0] == '-' || l[len(l)-1] == '-' {
			return errors.New("the device hostname is not a valid DNS name")
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return errors.New("the device hostname is not a valid DNS name")
			}
		}
	}
	last := labels[len(labels)-1]
	if strings.Trim(last, "0123456789") == "" {
		return errors.New("the device hostname is not a valid DNS name")
	}
	return nil
}

// CanonicalIP parses an address and renders it canonically. Zones and the
// unspecified and multicast addresses are refused; an IPv4-mapped IPv6
// address is rendered as IPv4.
func CanonicalIP(s string) (string, error) {
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return "", errors.New("a valid IP address is required")
	}
	a = a.Unmap()
	if a.IsUnspecified() || a.IsMulticast() {
		return "", errors.New("a valid unicast IP address is required")
	}
	return a.String(), nil
}

// Render substitutes ip and hostname into a command. The command must already
// be valid (see ValidateCommand); Render validates again, so an invalid
// command is never rendered. The hostname is examined only when the command
// uses {hostname}; if it fails validation the command is unavailable
// (*UnavailableError) and nothing is rendered.
func Render(c Command, ip, hostname string) (Rendered, error) {
	c, err := ValidateCommand(c)
	if err != nil {
		return Rendered{}, err
	}
	usesIP, usesHost := Uses(c)
	canonical := ""
	if usesIP {
		if canonical, err = CanonicalIP(ip); err != nil {
			return Rendered{}, err
		}
	}
	if usesHost {
		if err := ValidHostname(hostname); err != nil {
			return Rendered{}, &UnavailableError{Reason: err.Error()}
		}
	}
	sub := func(t string, esc func(string) string) string {
		return strings.NewReplacer(phIP, esc(canonical), phHostname, esc(hostname)).Replace(t)
	}
	if c.Kind == KindProgram {
		argv := make([]string, 0, len(c.Args)+1)
		argv = append(argv, c.Program)
		for _, a := range c.Args {
			argv = append(argv, sub(a, func(s string) string { return s }))
		}
		return Rendered{Kind: KindProgram, Argv: argv}, nil
	}
	return renderURL(c.URL, sub)
}

// urlParts is a URL template cut into a literal scheme and authority and the
// remainder, which is where placeholders may appear.
type urlParts struct {
	authority string // literal, no placeholders
	path      string // starts with "/" or is empty
	query     string // without "?"; hasQuery says whether one was present
	fragment  string
	hasQuery  bool
	hasFrag   bool
}

// splitURL requires https and a literal authority: the scheme and host
// are fixed by the technician and a device value can never choose the
// destination. Credentials in the URL are refused.
func splitURL(t string) (urlParts, error) {
	var p urlParts
	const scheme = "https://"
	if len(t) > MaxURLLen {
		return p, fmt.Errorf("the URL is longer than %d characters", MaxURLLen)
	}
	if !strings.HasPrefix(t, scheme) {
		return p, errors.New("the URL must start with https://")
	}
	rest := t[len(scheme):]
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	p.authority = rest[:end]
	rest = rest[end:]
	if p.authority == "" {
		return p, errors.New("the URL needs a host")
	}
	if strings.ContainsAny(p.authority, "{}@ \\") {
		return p, errors.New("the host part of the URL must be fixed text: no placeholders, no user name or password")
	}
	for _, r := range t {
		if r <= 0x20 || r >= 0x7f || strings.ContainsRune("\"<>\\^`|", r) {
			return p, errors.New("the URL contains a character that must be percent-encoded or is not allowed")
		}
	}
	u, err := url.Parse(scheme + p.authority)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return p, errors.New("the URL host is not valid")
	}
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		p.fragment, p.hasFrag = rest[i+1:], true
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		p.query, p.hasQuery = rest[i+1:], true
		rest = rest[:i]
	}
	p.path = rest
	return p, nil
}

func renderURL(t string, sub func(string, func(string) string) string) (Rendered, error) {
	p, err := splitURL(t)
	if err != nil {
		return Rendered{}, err
	}
	var b strings.Builder
	b.WriteString("https://")
	b.WriteString(p.authority)
	b.WriteString(sub(p.path, url.PathEscape))
	if p.hasQuery {
		b.WriteByte('?')
		b.WriteString(sub(p.query, url.QueryEscape))
	}
	if p.hasFrag {
		b.WriteByte('#')
		b.WriteString(sub(p.fragment, url.QueryEscape))
	}
	out := b.String()
	if len(out) > MaxURLLen {
		return Rendered{}, fmt.Errorf("the rendered URL is longer than %d characters", MaxURLLen)
	}
	// Defence in depth: the result must still be an https URL for the same
	// literal host, with no user information.
	u, err := url.Parse(out)
	want, werr := url.Parse("https://" + p.authority)
	if err != nil || werr != nil || u.Scheme != "https" || u.Host != want.Host || u.User != nil {
		return Rendered{}, errors.New("the rendered URL is not valid")
	}
	return Rendered{Kind: KindURL, URL: out}, nil
}
