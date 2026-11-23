// Package template holds role templates (runbook R9, R10, R76 to R78): JSON
// files that say where a new object goes, how it is named, which attributes
// and groups it gets, and which extra fields the form asks for. Templates make
// plans quick to build; they never grant rights (design Q20): every change is
// still checked against the signed-in account's own rights.
package template

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/plan"
)

// Field is an extra form field.
type Field struct {
	Attribute string   `json:"attribute"`
	Label     string   `json:"label"`
	Required  bool     `json:"required,omitempty"`
	Options   []string `json:"options,omitempty"`
	// Type is "text" (default), "user" (a DN picked from the directory, for
	// manager), "date" or "choice".
	Type    string `json:"type,omitempty"`
	Pattern string `json:"pattern,omitempty"` // optional regular expression the value must match
	Help    string `json:"help,omitempty"`
}

// Template is one role or object template.
type Template struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     int    `json:"version"`
	// Class is "user" (default), "group", "contact" or "organizationalUnit".
	Class string `json:"class,omitempty"`
	OU    string `json:"ou"`
	// Patterns use {attribute} tokens from the form, plus {first1} (first
	// letter of givenName) and {n} (collision number, empty for the first).
	CN                string   `json:"cn,omitempty"`
	DisplayName       string   `json:"displayName,omitempty"`
	SAMAccountName    string   `json:"sAMAccountName,omitempty"`
	UserPrincipalName string   `json:"userPrincipalName,omitempty"`
	Mail              string   `json:"mail,omitempty"`
	ProxyAddresses    []string `json:"proxyAddresses,omitempty"`
	// Attributes are fixed values or patterns set on every new object.
	Attributes map[string]string `json:"attributes,omitempty"`
	Groups     []string          `json:"groups,omitempty"`
	Fields     []Field           `json:"fields,omitempty"`
	Enable     bool              `json:"enable"`
	// MustChangePassword defaults to true when omitted.
	MustChangePassword *bool `json:"mustChangePassword,omitempty"`
	// File is the path the template was loaded from.
	File string `json:"-"`
}

// MustChange reports whether a new user must change the temporary password.
func (t Template) MustChange() bool { return t.MustChangePassword == nil || *t.MustChangePassword }

// Load reads one template file. Unknown fields are rejected.
func Load(path string) (Template, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Template{}, err
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var t Template
	if err := dec.Decode(&t); err != nil {
		return Template{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	t.File = path
	if err := t.Validate(); err != nil {
		return Template{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return t, nil
}

// LoadDir loads every *.json template in dir, sorted by name. Invalid files are
// returned as errors beside the valid templates.
func LoadDir(dir string) ([]Template, []error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, []error{err}
	}
	var out []Template
	var errs []error
	for _, f := range files {
		t, err := Load(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, errs
}

// Save writes t to dir as a new version and returns the saved template. The
// file name comes from the template name.
func Save(dir string, t Template) (Template, error) {
	if err := t.Validate(); err != nil {
		return Template{}, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Template{}, err
	}
	t.Version++
	path := filepath.Join(dir, Slug(t.Name)+".json")
	b, _ := json.MarshalIndent(t, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o640); err != nil {
		return Template{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return Template{}, err
	}
	t.File = path
	return t, nil
}

var tokenRE = regexp.MustCompile(`\{([A-Za-z0-9-]+)\}`)

// Validate checks the template's structure.
func (t Template) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("name is required")
	}
	switch t.class() {
	case "user", "group", "contact", "organizationalunit":
	default:
		return fmt.Errorf("class %q is not supported", t.Class)
	}
	if strings.TrimSpace(t.OU) == "" {
		return errors.New("ou is required")
	}
	if t.class() == "user" && (t.SAMAccountName == "" || t.UserPrincipalName == "") {
		return errors.New("user templates need sAMAccountName and userPrincipalName patterns")
	}
	if t.class() == "user" && !strings.Contains(t.UserPrincipalName, "@") {
		return errors.New("userPrincipalName pattern needs an @suffix")
	}
	seen := map[string]bool{}
	for _, f := range t.Fields {
		a := strings.ToLower(f.Attribute)
		if a == "" || seen[a] {
			return fmt.Errorf("field %q is empty or repeated", f.Attribute)
		}
		seen[a] = true
		if f.Pattern != "" {
			if _, err := regexp.Compile(f.Pattern); err != nil {
				return fmt.Errorf("field %s pattern: %w", f.Attribute, err)
			}
		}
		switch a {
		case "unicodepwd", "userpassword", "objectclass", "memberof", "member", "useraccountcontrol":
			return fmt.Errorf("field %s is not allowed", f.Attribute)
		}
	}
	for k := range t.Attributes {
		switch strings.ToLower(k) {
		case "unicodepwd", "userpassword", "objectclass", "memberof", "member", "useraccountcontrol", "cn", "distinguishedname":
			return fmt.Errorf("attribute %s cannot be set by a template", k)
		}
	}
	return nil
}

func (t Template) class() string {
	if t.Class == "" {
		return "user"
	}
	return strings.ToLower(t.Class)
}

// Form is what the technician enters: givenName, sn, initials and the
// template's fields, keyed by attribute name (case-insensitive).
type Form map[string]string

func (f Form) get(k string) string {
	for key, v := range f {
		if strings.EqualFold(key, k) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Preview is the computed new object, shown live while the form is filled
// (runbook R76).
type Preview struct {
	Template          string         `json:"template"`
	DN                string         `json:"dn"`
	CN                string         `json:"cn"`
	DisplayName       string         `json:"displayName,omitempty"`
	SAMAccountName    string         `json:"sAMAccountName,omitempty"`
	UserPrincipalName string         `json:"userPrincipalName,omitempty"`
	Mail              string         `json:"mail,omitempty"`
	Attributes        []ad.Attribute `json:"attributes"`
	Groups            []string       `json:"groups,omitempty"`
	// Problems are missing or invalid fields; a plan is refused while any
	// remain. Notes explain adjustments such as collision numbering.
	Problems []string `json:"problems,omitempty"`
	Notes    []string `json:"notes,omitempty"`
}

// Lookup finds existing objects for collision checks.
type Lookup interface {
	Search(context.Context, ad.SearchRequest) (ad.SearchResult, error)
	Get(context.Context, string) (ad.Object, error)
}

// Build computes the preview: names from the patterns, collision numbering
// checked in AD, required fields, and the attribute list.
func Build(ctx context.Context, l Lookup, t Template, f Form) (Preview, error) {
	p := Preview{Template: t.Name, Groups: append([]string(nil), t.Groups...)}
	for _, field := range t.Fields {
		v := f.get(field.Attribute)
		if v == "" && field.Required {
			p.Problems = append(p.Problems, field.labelOrAttr()+" is required")
			continue
		}
		if v != "" && field.Pattern != "" {
			if !regexp.MustCompile(field.Pattern).MatchString(v) {
				p.Problems = append(p.Problems, field.labelOrAttr()+" is not in the expected format")
			}
		}
		if v != "" && len(field.Options) > 0 && !containsFold(field.Options, v) {
			p.Problems = append(p.Problems, field.labelOrAttr()+" must be one of: "+strings.Join(field.Options, ", "))
		}
		if v != "" && field.Type == "user" {
			if _, err := l.Get(ctx, v); err != nil {
				p.Problems = append(p.Problems, field.labelOrAttr()+" was not found in the directory")
			}
		}
	}
	if t.class() == "user" || t.class() == "contact" {
		if f.get("givenName") == "" {
			p.Problems = append(p.Problems, "First name is required")
		}
		if f.get("sn") == "" {
			p.Problems = append(p.Problems, "Last name is required")
		}
	} else if f.get("name") == "" && f.get("cn") == "" {
		p.Problems = append(p.Problems, "Name is required")
	}
	if len(p.Problems) > 0 {
		return p, nil
	}
	cnPattern := firstNonEmpty(t.CN, "{givenName} {sn}")
	if t.class() == "group" || t.class() == "organizationalunit" {
		cnPattern = firstNonEmpty(t.CN, "{name}")
	}
	for n := 1; n <= 99; n++ {
		num := ""
		if n > 1 {
			num = strconv.Itoa(n)
		}
		cn := strings.TrimSpace(expand(cnPattern, f, num, false))
		if n > 1 && !strings.Contains(cnPattern, "{n}") {
			cn += " " + num
		}
		rdnType := "CN"
		if t.class() == "organizationalunit" {
			rdnType = "OU"
		}
		dn := rdnType + "=" + ad.EscapeRDNValue(cn) + "," + t.OU
		var sam, upn, mail string
		if t.class() == "user" {
			sam = samName(expand(t.SAMAccountName, f, "", true), num)
			upn = localWithNumber(expand(t.UserPrincipalName, f, "", true), num)
			if t.Mail != "" {
				mail = localWithNumber(expand(t.Mail, f, "", true), num)
			}
		}
		taken, why, err := collides(ctx, l, dn, sam, upn, mail)
		if err != nil {
			return p, err
		}
		if taken {
			if n == 1 {
				p.Notes = append(p.Notes, why+"; a number was added")
			}
			continue
		}
		p.DN, p.CN, p.SAMAccountName, p.UserPrincipalName, p.Mail = dn, cn, sam, upn, mail
		break
	}
	if p.DN == "" {
		p.Problems = append(p.Problems, "No free name was found after 99 attempts")
		return p, nil
	}
	attrs := map[string][]string{}
	set := func(k string, v ...string) {
		var clean []string
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				clean = append(clean, s)
			}
		}
		if len(clean) > 0 {
			attrs[k] = clean
		}
	}
	switch t.class() {
	case "user", "contact":
		set("givenName", f.get("givenName"))
		set("sn", f.get("sn"))
		set("initials", f.get("initials"))
		p.DisplayName = strings.TrimSpace(expand(firstNonEmpty(t.DisplayName, "{givenName} {sn}"), f, "", false))
		set("displayName", p.DisplayName)
	case "group":
		set("sAMAccountName", samName(strings.ReplaceAll(p.CN, " ", ""), ""))
	}
	if t.class() == "user" {
		set("sAMAccountName", p.SAMAccountName)
		set("userPrincipalName", p.UserPrincipalName)
		set("mail", p.Mail)
		var proxies []string
		for _, pp := range t.ProxyAddresses {
			v := strings.ReplaceAll(pp, "{mail}", p.Mail)
			v = expand(v, f, "", true)
			if strings.HasPrefix(v, "smtp:") && strings.HasPrefix(pp, "SMTP:") {
				v = "SMTP:" + v[5:]
			}
			proxies = append(proxies, v)
		}
		set("proxyAddresses", proxies...)
	}
	for k, v := range t.Attributes {
		set(k, expand(v, f, "", false))
	}
	for _, field := range t.Fields {
		if v := f.get(field.Attribute); v != "" {
			set(field.Attribute, v)
		}
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.ToLower(keys[i]) < strings.ToLower(keys[j]) })
	for _, k := range keys {
		p.Attributes = append(p.Attributes, ad.Attribute{Name: k, Values: attrs[k]})
	}
	return p, nil
}

// Plan builds the preview and turns it into a plan: create, then the template's
// groups. Problems in the preview are returned as an error.
func Plan(ctx context.Context, w ad.Writer, t Template, f Form, ticket string, opt plan.Options) (plan.Plan, Preview, error) {
	p, err := Build(ctx, w, t, f)
	if err != nil {
		return plan.Plan{}, p, err
	}
	if len(p.Problems) > 0 {
		return plan.Plan{}, p, errors.New(strings.Join(p.Problems, "; "))
	}
	b := plan.NewBuilder(w, fmt.Sprintf("New %s from template %s (version %d): %s", t.class(), t.Name, t.Version, p.CN), ticket, opt)
	b.Create(ctx, p.DN, t.class(), p.Attributes, t.Enable, t.MustChange())
	for _, g := range t.Groups {
		b.AddMember(ctx, g, p.DN)
	}
	pl, err := b.Build()
	return pl, p, err
}

func collides(ctx context.Context, l Lookup, dn, sam, upn, mail string) (bool, string, error) {
	if _, err := l.Get(ctx, dn); err == nil {
		return true, "An object named " + ad.RDN(dn) + " already exists in this OU", nil
	} else if !errors.Is(err, ad.ErrNotFound) {
		return false, "", err
	}
	for _, c := range []struct{ attr, v, why string }{
		{"samaccountname", sam, "The account name " + sam + " is taken"},
		{"userprincipalname", upn, "The sign-in name " + upn + " is taken"},
		{"mail", mail, "The email address " + mail + " is taken"},
		{"proxyaddresses", mail, "The email address " + mail + " is already an alias"},
	} {
		if c.v == "" {
			continue
		}
		res, err := l.Search(ctx, ad.SearchRequest{Query: c.v, Limit: 50})
		if err != nil {
			return false, "", err
		}
		for _, o := range res.Objects {
			for _, v := range o.Values(c.attr) {
				v = strings.TrimPrefix(strings.TrimPrefix(v, "SMTP:"), "smtp:")
				if strings.EqualFold(v, c.v) {
					return true, c.why, nil
				}
			}
		}
	}
	return false, "", nil
}

func (f Field) labelOrAttr() string {
	if f.Label != "" {
		return f.Label
	}
	return f.Attribute
}

// expand replaces {tokens}. When ident is true the result is for an account
// name: letters are folded to ASCII, lower-cased, and characters other than
// letters, digits, dot, hyphen, underscore and @ are dropped.
func expand(pattern string, f Form, num string, ident bool) string {
	out := tokenRE.ReplaceAllStringFunc(pattern, func(tok string) string {
		key := tok[1 : len(tok)-1]
		switch strings.ToLower(key) {
		case "n":
			return num
		case "first1":
			g := []rune(f.get("givenName"))
			if len(g) == 0 {
				return ""
			}
			return string(g[0])
		}
		return f.get(key)
	})
	if !ident {
		return strings.Join(strings.Fields(out), " ")
	}
	var b strings.Builder
	for _, r := range Fold(out) {
		r = unicode.ToLower(r)
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' || r == '@' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), ".")
}

func samName(base, num string) string {
	limit := 20 - len(num)
	if len(base) > limit {
		base = strings.TrimRight(base[:limit], ".")
	}
	return base + num
}

func localWithNumber(addr, num string) string {
	if num == "" {
		return addr
	}
	at := strings.LastIndexByte(addr, '@')
	if at < 0 {
		return addr + num
	}
	return addr[:at] + num + addr[at:]
}

var foldMap = map[rune]string{
	'ß': "ss", 'æ': "ae", 'Æ': "AE", 'ø': "o", 'Ø': "O", 'œ': "oe", 'Œ': "OE", 'đ': "d", 'Đ': "D", 'ł': "l", 'Ł': "L",
	'þ': "th", 'Þ': "Th", 'ð': "d", 'Ð': "D", 'ı': "i", '\'': "", '’': "",
}

// Fold removes common Latin diacritics (é to e, ß to ss) without external
// tables. Unknown non-ASCII letters are dropped by expand.
func Fold(s string) string {
	var b strings.Builder
	for _, r := range s {
		if v, ok := foldMap[r]; ok {
			b.WriteString(v)
			continue
		}
		b.WriteRune(baseLetter(r))
	}
	return b.String()
}

func baseLetter(r rune) rune {
	const src = "ÀÁÂÃÄÅàáâãäåÇçÈÉÊËèéêëÌÍÎÏìíîïÑñÒÓÔÕÖòóôõöÙÚÛÜùúûüÝýÿĀāĂăĄąĆćČčĎďĒēĖėĘęĚěĞğĪīĮįĶķĹĺĻļĽľŃńŅņŇňŌōŐőŔŕŘřŚśŞşŠšŢţŤťŪūŮůŰűŲųŹźŻżŽž"
	const dst = "AAAAAAaaaaaaCcEEEEeeeeIIIIiiiiNnOOOOOoooooUUUUuuuuYyyAaAaAaCcCcDdEeEeEeEeGgIiIiKkLlLlLlNnNnNnOoOoRrRrSsSsSsTtTtUuUuUuUuZzZzZz"
	srcR, dstR := []rune(src), []rune(dst)
	for i, c := range srcR {
		if c == r {
			return dstR[i]
		}
	}
	return r
}

// Slug turns a template name into a file name.
func Slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(Fold(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "template"
	}
	return s
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}
