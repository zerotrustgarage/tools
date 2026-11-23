// Package plan computes changes before anything is written (design principle
// 1): each change names the object, the attribute, the value now and the value
// after, and is checked against the signed-in account's effective rights
// (runbook R118), the Tier 0 fence (principle 4) and the bulk cap (R39).
package plan

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

// Kind is the type of one change.
type Kind string

const (
	KindCreate        Kind = "create"
	KindSet           Kind = "set"
	KindAddMember     Kind = "add-member"
	KindRemoveMember  Kind = "remove-member"
	KindMove          Kind = "move"
	KindRename        Kind = "rename"
	KindEnable        Kind = "enable"
	KindDisable       Kind = "disable"
	KindUnlock        Kind = "unlock"
	KindResetPassword Kind = "reset-password"
	KindDelete        Kind = "delete"
	KindUndelete      Kind = "undelete"
)

// Change is one step of a plan. For memberships DN is the group and Target the
// member. For moves Target is the new parent; for renames Target is the new
// RDN. For create, Attributes holds the new object's attributes.
type Change struct {
	Kind       Kind           `json:"kind"`
	DN         string         `json:"dn"`
	Class      string         `json:"class,omitempty"`
	Attribute  string         `json:"attribute,omitempty"`
	Before     []string       `json:"before,omitempty"`
	After      []string       `json:"after,omitempty"`
	Target     string         `json:"target,omitempty"`
	Attributes []ad.Attribute `json:"attributes,omitempty"`
	// MustChange sets "change password at next logon" with a password reset
	// or a new user (IA.L2-3.5.9). Enable enables a new user after its
	// password is set.
	MustChange bool   `json:"mustChange,omitempty"`
	Enable     bool   `json:"enable,omitempty"`
	Summary    string `json:"summary"`
}

// Warning is shown with the plan; Blocking warnings stop Apply.
type Warning struct {
	DN       string `json:"dn,omitempty"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}

// Plan is a reviewed set of changes.
type Plan struct {
	ID         string    `json:"id"`
	Created    time.Time `json:"created"`
	Title      string    `json:"title"`
	Technician string    `json:"technician"`
	Ticket     string    `json:"ticket,omitempty"`
	DC         string    `json:"dc"`
	Changes    []Change  `json:"changes"`
	Warnings   []Warning `json:"warnings,omitempty"`
	Objects    int       `json:"objects"`
	// TypedCount is true when the plan touches more objects than the bulk
	// cap, so the technician must type the object count back (R39).
	TypedCount bool `json:"typedCount"`
	// Tier0 is true when any object is in Tier 0 (principle 4); such a plan
	// needs a second, explicit confirmation.
	Tier0 bool `json:"tier0"`
}

// Hash identifies the plan's content (not its ID or creation time), so the
// record can show that what ran is what was reviewed.
func (p Plan) Hash() string {
	c := struct {
		Changes []Change  `json:"changes"`
		Ticket  string    `json:"ticket"`
		Warn    []Warning `json:"warnings"`
	}{p.Changes, p.Ticket, p.Warnings}
	b, _ := json.Marshal(c)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Blocked reports whether a blocking warning prevents Apply.
func (p Plan) Blocked() bool {
	for _, w := range p.Warnings {
		if w.Blocking {
			return true
		}
	}
	return false
}

// Options configure a Builder.
type Options struct {
	// BulkCap is the number of objects above which the count must be typed
	// back. Zero means 25.
	BulkCap int
	// Tier0Groups are the names (cn) of privileged groups. Members, and the
	// groups themselves, are Tier 0. Nil uses DefaultTier0Groups.
	Tier0Groups []string
	Now         func() time.Time
}

// DefaultTier0Groups are AD's built-in privileged groups (runbook R35).
var DefaultTier0Groups = []string{"Domain Admins", "Enterprise Admins", "Schema Admins", "Administrators",
	"Account Operators", "Backup Operators", "Server Operators", "Print Operators", "DnsAdmins",
	"Group Policy Creator Owners", "Domain Controllers", "Read-only Domain Controllers",
	"Enterprise Key Admins", "Key Admins", "Cert Publishers"}

// Builder collects changes against one Writer.
type Builder struct {
	w       ad.Writer
	opt     Options
	p       Plan
	objects map[string]bool
	rights  map[string]ad.Rights
	cache   map[string]ad.Object
	created map[string]bool
	err     error
}

// NewBuilder starts a plan run as the writer's account on the writer's DC.
func NewBuilder(w ad.Writer, title, ticket string, opt Options) *Builder {
	if opt.BulkCap <= 0 {
		opt.BulkCap = 25
	}
	if opt.Tier0Groups == nil {
		opt.Tier0Groups = DefaultTier0Groups
	}
	if opt.Now == nil {
		opt.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Builder{w: w, opt: opt, objects: map[string]bool{}, rights: map[string]ad.Rights{}, cache: map[string]ad.Object{}, created: map[string]bool{},
		p: Plan{ID: newID(), Created: opt.Now(), Title: title, Ticket: strings.TrimSpace(ticket), DC: w.DC(), Technician: w.Identity()}}
}

// Err returns the first error a builder step hit (lookups, not refusals).
func (b *Builder) Err() error { return b.err }

func (b *Builder) get(ctx context.Context, dn string) (ad.Object, bool) {
	key := strings.ToLower(dn)
	if o, ok := b.cache[key]; ok {
		return o, true
	}
	o, err := b.w.Get(ctx, dn)
	if err != nil {
		if errors.Is(err, ad.ErrNotFound) {
			b.warn(dn, "The object was not found on "+b.w.DC()+".", true)
		} else if b.err == nil {
			b.err = err
		}
		return ad.Object{}, false
	}
	b.cache[key] = o
	return o, true
}

func (b *Builder) rightsFor(ctx context.Context, dn string) (ad.Rights, bool) {
	key := strings.ToLower(dn)
	if r, ok := b.rights[key]; ok {
		return r, true
	}
	r, err := b.w.Rights(ctx, dn)
	if err != nil {
		if b.err == nil {
			b.err = fmt.Errorf("read effective rights on %s: %w", dn, err)
		}
		return ad.Rights{}, false
	}
	b.rights[key] = r
	return r, true
}

func (b *Builder) warn(dn, msg string, blocking bool) {
	for _, w := range b.p.Warnings {
		if w.DN == dn && w.Message == msg {
			return
		}
	}
	b.p.Warnings = append(b.p.Warnings, Warning{DN: dn, Message: msg, Blocking: blocking})
}

func (b *Builder) needWrite(ctx context.Context, dn, attr string) bool {
	r, ok := b.rightsFor(ctx, dn)
	if !ok {
		return false
	}
	if !r.CanWrite(attr) {
		b.warn(dn, fmt.Sprintf("%s cannot change %s on this object; AD does not delegate that right to the account.", b.w.Identity(), attr), true)
		return false
	}
	return true
}

func (b *Builder) touch(ctx context.Context, o ad.Object) {
	b.objects[strings.ToLower(o.DN)] = true
	if reason := b.tier0(ctx, o); reason != "" {
		b.p.Tier0 = true
		b.warn(o.DN, "Tier 0 object: "+reason+". This change needs a second confirmation and should be made from a privileged access workstation or a DC.", false)
	}
}

// tier0 reports why o is Tier 0, or "".
func (b *Builder) tier0(ctx context.Context, o ad.Object) string {
	uac, _ := strconv.ParseInt(o.First("useraccountcontrol"), 10, 64)
	classes := strings.ToLower(strings.Join(o.Values("objectclass"), ","))
	name := o.First("samaccountname")
	switch {
	case uac&(ad.UACServerTrustAccount|ad.UACPartialSecretsServer) != 0:
		return "domain controller"
	case strings.EqualFold(name, "krbtgt") || strings.HasPrefix(strings.ToLower(name), "krbtgt_"):
		return "krbtgt account"
	case strings.Contains(classes, "msds-groupmanagedserviceaccount"):
		return "group managed service account"
	case strings.Contains(classes, "msds-delegatedmanagedserviceaccount"):
		return "delegated managed service account"
	case o.First("admincount") == "1":
		return "protected by AdminSDHolder (adminCount = 1)"
	}
	for _, g := range b.opt.Tier0Groups {
		if strings.EqualFold(o.First("cn"), g) && strings.Contains(classes, "group") {
			return "privileged group " + g
		}
	}
	for _, m := range o.Values("memberof") {
		cn := cnOf(m)
		for _, g := range b.opt.Tier0Groups {
			if strings.EqualFold(cn, g) {
				return "member of " + g
			}
		}
	}
	return ""
}

func (b *Builder) add(c Change) { b.p.Changes = append(b.p.Changes, c) }

// Set replaces attr's values on dn (nil or empty clears it). Unchanged values
// produce no change.
func (b *Builder) Set(ctx context.Context, dn, attr string, values []string) {
	attr = strings.TrimSpace(attr)
	if blockedAttribute(attr) {
		b.warn(dn, attr+" cannot be edited here.", true)
		return
	}
	o, ok := b.get(ctx, dn)
	if !ok {
		return
	}
	clean := cleanValues(values)
	before := o.Values(attr)
	if sameSet(before, clean) {
		return
	}
	b.touch(ctx, o)
	if !b.needWrite(ctx, dn, attr) {
		return
	}
	summary := fmt.Sprintf("Set %s on %s", attr, display(o))
	if len(clean) == 0 {
		summary = fmt.Sprintf("Clear %s on %s", attr, display(o))
	}
	b.add(Change{Kind: KindSet, DN: o.DN, Attribute: attr, Before: before, After: clean, Summary: summary})
	if strings.EqualFold(attr, "userPrincipalName") || strings.EqualFold(attr, "mail") || strings.EqualFold(attr, "proxyAddresses") {
		b.warn(o.DN, "This attribute synchronizes to Microsoft 365 for synced users; the cloud object changes at the next sync.", false)
	}
}

// AddMember adds member to group.
func (b *Builder) AddMember(ctx context.Context, group, member string) {
	b.member(ctx, group, member, true)
}

// RemoveMember removes member from group.
func (b *Builder) RemoveMember(ctx context.Context, group, member string) {
	b.member(ctx, group, member, false)
}

func (b *Builder) member(ctx context.Context, group, member string, add bool) {
	g, ok := b.get(ctx, group)
	if !ok {
		return
	}
	var m ad.Object
	if b.created[strings.ToLower(member)] {
		// The member is created earlier in this plan.
		m = ad.Object{DN: member, Attributes: map[string][]string{"cn": {rdnValue(member)}}}
	} else {
		var ok bool
		if m, ok = b.get(ctx, member); !ok {
			return
		}
		has := containsFold(g.Values("member"), m.DN) || containsFold(m.Values("memberof"), g.DN)
		if has == add {
			return
		}
		b.touch(ctx, m)
	}
	b.touch(ctx, g)
	if !b.needWrite(ctx, g.DN, "member") {
		return
	}
	if add {
		b.add(Change{Kind: KindAddMember, DN: g.DN, Target: m.DN, Attribute: "member", Summary: fmt.Sprintf("Add %s to %s", display(m), display(g))})
	} else {
		b.add(Change{Kind: KindRemoveMember, DN: g.DN, Target: m.DN, Attribute: "member", Summary: fmt.Sprintf("Remove %s from %s", display(m), display(g))})
	}
}

// Move moves dn under newParent.
func (b *Builder) Move(ctx context.Context, dn, newParent string) {
	o, ok := b.get(ctx, dn)
	if !ok {
		return
	}
	if strings.EqualFold(ad.ParentDN(o.DN), newParent) {
		return
	}
	if _, ok := b.get(ctx, newParent); !ok {
		return
	}
	b.touch(ctx, o)
	if r, ok := b.rightsFor(ctx, newParent); ok && !r.CanCreate(lastClass(o)) {
		b.warn(o.DN, fmt.Sprintf("%s cannot create %s objects in %s, so it cannot move this object there.", b.w.Identity(), lastClass(o), newParent), true)
		return
	}
	b.add(Change{Kind: KindMove, DN: o.DN, Before: []string{ad.ParentDN(o.DN)}, After: []string{newParent}, Target: newParent,
		Summary: fmt.Sprintf("Move %s to %s", display(o), newParent)})
	b.warn(o.DN, "Moving an object can take it out of the Microsoft 365 sync scope, which deletes the cloud object at the next sync. Check the target OU is in scope.", false)
}

// Rename changes dn's common name (and its RDN).
func (b *Builder) Rename(ctx context.Context, dn, newName string) {
	newName = strings.TrimSpace(newName)
	o, ok := b.get(ctx, dn)
	if !ok {
		return
	}
	if newName == "" {
		b.warn(dn, "A new name is required.", true)
		return
	}
	rdnType := "CN"
	if i := strings.IndexByte(ad.RDN(o.DN), '='); i > 0 {
		rdnType = ad.RDN(o.DN)[:i]
	}
	newRDN := rdnType + "=" + ad.EscapeRDNValue(newName)
	if strings.EqualFold(newRDN, ad.RDN(o.DN)) {
		return
	}
	b.touch(ctx, o)
	if !b.needWrite(ctx, o.DN, "name") {
		return
	}
	b.add(Change{Kind: KindRename, DN: o.DN, Target: newRDN, Before: []string{o.First("cn")}, After: []string{newName},
		Summary: fmt.Sprintf("Rename %s to %s", display(o), newName)})
}

// SetEnabled enables or disables an account.
func (b *Builder) SetEnabled(ctx context.Context, dn string, enabled bool) {
	o, ok := b.get(ctx, dn)
	if !ok {
		return
	}
	uac, err := strconv.ParseInt(o.First("useraccountcontrol"), 10, 64)
	if err != nil {
		b.warn(o.DN, "This object has no account to enable or disable.", true)
		return
	}
	disabled := uac&ad.UACAccountDisable != 0
	if disabled != enabled {
		return
	}
	b.touch(ctx, o)
	if !b.needWrite(ctx, o.DN, "userAccountControl") {
		return
	}
	next := uac | ad.UACAccountDisable
	kind, verb := KindDisable, "Disable"
	if enabled {
		next, kind, verb = uac&^ad.UACAccountDisable, KindEnable, "Enable"
	}
	b.add(Change{Kind: kind, DN: o.DN, Attribute: "userAccountControl", Before: []string{strconv.FormatInt(uac, 10)}, After: []string{strconv.FormatInt(next, 10)},
		Summary: fmt.Sprintf("%s %s", verb, display(o))})
}

// Unlock clears a lockout.
func (b *Builder) Unlock(ctx context.Context, dn string) {
	o, ok := b.get(ctx, dn)
	if !ok {
		return
	}
	if n, _ := strconv.ParseInt(o.First("lockouttime"), 10, 64); n <= 0 {
		b.warn(o.DN, "The account is not locked out on "+b.w.DC()+".", false)
		return
	}
	b.touch(ctx, o)
	if !b.needWrite(ctx, o.DN, "lockoutTime") {
		return
	}
	b.add(Change{Kind: KindUnlock, DN: o.DN, Attribute: "lockoutTime", Before: o.Values("lockouttime"), After: []string{"0"}, Summary: "Unlock " + display(o)})
}

// ResetPassword plans a password reset to a generated temporary password,
// shown once after Apply. mustChange sets change at next logon.
func (b *Builder) ResetPassword(ctx context.Context, dn string, mustChange bool) {
	o, ok := b.get(ctx, dn)
	if !ok {
		return
	}
	b.touch(ctx, o)
	if mustChange && !b.needWrite(ctx, o.DN, "pwdLastSet") {
		return
	}
	s := "Reset the password of " + display(o) + " to a generated temporary password"
	if mustChange {
		s += " that must be changed at next logon"
	}
	b.add(Change{Kind: KindResetPassword, DN: o.DN, MustChange: mustChange, Summary: s})
	b.warn(o.DN, "Reset Password is an extended right; AD confirms it only when the change runs.", false)
}

// SetAccountExpires sets or clears (zero time) the account's end date.
func (b *Builder) SetAccountExpires(ctx context.Context, dn string, end time.Time) {
	v := "0"
	if !end.IsZero() {
		v = ad.ToFileTime(end)
	}
	o, ok := b.get(ctx, dn)
	if !ok {
		return
	}
	cur := o.First("accountexpires")
	if cur == v || (v == "0" && (cur == "" || cur == "9223372036854775807")) {
		return
	}
	b.touch(ctx, o)
	if !b.needWrite(ctx, o.DN, "accountExpires") {
		return
	}
	s := "Remove the end date of " + display(o)
	if v != "0" {
		s = fmt.Sprintf("Set the account of %s to expire on %s", display(o), end.UTC().Format("2006-01-02"))
	}
	b.add(Change{Kind: KindSet, DN: o.DN, Attribute: "accountExpires", Before: o.Values("accountexpires"), After: []string{v}, Summary: s})
}

// Delete deletes dn (to the Recycle Bin when it is enabled).
func (b *Builder) Delete(ctx context.Context, dn string) {
	o, ok := b.get(ctx, dn)
	if !ok {
		return
	}
	b.touch(ctx, o)
	b.add(Change{Kind: KindDelete, DN: o.DN, Before: o.Values("memberof"), Summary: "Delete " + display(o)})
	b.warn(o.DN, "Deleting is permanent unless the AD Recycle Bin is enabled. Disabling is usually the safer first step.", false)
}

// Undelete restores a deleted object to newDN.
func (b *Builder) Undelete(ctx context.Context, deletedDN, newDN string) {
	b.objects[strings.ToLower(newDN)] = true
	b.add(Change{Kind: KindUndelete, DN: deletedDN, Target: newDN, Summary: "Restore " + newDN + " from the AD Recycle Bin"})
}

// Create plans a new object. For users, password is set to a generated
// temporary value and the account is enabled after it when enable is true.
func (b *Builder) Create(ctx context.Context, dn, class string, attrs []ad.Attribute, enable, mustChange bool) {
	class = strings.ToLower(strings.TrimSpace(class))
	parent := ad.ParentDN(dn)
	if _, err := b.w.Get(ctx, dn); err == nil {
		b.warn(dn, "An object with this name already exists.", true)
		return
	} else if !errors.Is(err, ad.ErrNotFound) && b.err == nil {
		b.err = err
		return
	}
	if _, ok := b.get(ctx, parent); !ok {
		return
	}
	b.objects[strings.ToLower(dn)] = true
	b.created[strings.ToLower(dn)] = true
	if r, ok := b.rightsFor(ctx, parent); ok && !r.CanCreate(class) {
		b.warn(dn, fmt.Sprintf("%s cannot create %s objects in %s.", b.w.Identity(), class, parent), true)
		return
	}
	attrs = append([]ad.Attribute{{Name: "objectClass", Values: objectClasses(class)}}, attrs...)
	sort.SliceStable(attrs[1:], func(i, j int) bool { return strings.ToLower(attrs[i+1].Name) < strings.ToLower(attrs[j+1].Name) })
	isUser := class == "user" || class == "inetorgperson"
	b.add(Change{Kind: KindCreate, DN: dn, Class: class, Attributes: attrs, Enable: isUser && enable, MustChange: isUser && mustChange,
		Summary: fmt.Sprintf("Create %s %s in %s", class, rdnValue(dn), parent)})
}

// Build finishes the plan.
func (b *Builder) Build() (Plan, error) {
	if b.err != nil {
		return Plan{}, b.err
	}
	p := b.p
	p.Objects = len(b.objects)
	p.TypedCount = p.Objects > b.opt.BulkCap
	if len(p.Changes) == 0 && !p.Blocked() {
		p.Warnings = append(p.Warnings, Warning{Message: "Nothing to change: the directory already matches."})
	}
	return p, nil
}

func objectClasses(class string) []string {
	switch class {
	case "user":
		return []string{"top", "person", "organizationalPerson", "user"}
	case "inetorgperson":
		return []string{"top", "person", "organizationalPerson", "user", "inetOrgPerson"}
	case "contact":
		return []string{"top", "person", "organizationalPerson", "contact"}
	case "group":
		return []string{"top", "group"}
	case "organizationalunit":
		return []string{"top", "organizationalUnit"}
	case "computer":
		return []string{"top", "person", "organizationalPerson", "user", "computer"}
	}
	return []string{"top", class}
}

func blockedAttribute(a string) bool {
	switch strings.ToLower(a) {
	case "unicodepwd", "userpassword", "ms-mcs-admpwd", "mslaps-password", "mslaps-encryptedpassword", "objectclass",
		"objectsid", "objectguid", "memberof", "distinguishedname", "cn", "name", "member", "useraccountcontrol", "whencreated", "whenchanged":
		return true
	}
	return strings.HasPrefix(strings.ToLower(a), "mslaps-") || strings.HasPrefix(strings.ToLower(a), "msfve-")
}

func display(o ad.Object) string {
	for _, a := range []string{"displayname", "cn", "samaccountname"} {
		if v := o.First(a); v != "" {
			return v
		}
	}
	return o.DN
}

func cnOf(dn string) string { return rdnValue(dn) }

func rdnValue(dn string) string {
	r := ad.RDN(dn)
	if i := strings.IndexByte(r, '='); i >= 0 {
		r = r[i+1:]
	}
	return strings.ReplaceAll(r, `\`, "")
}

func lastClass(o ad.Object) string {
	c := o.Values("objectclass")
	if len(c) == 0 {
		return ""
	}
	return strings.ToLower(c[len(c)-1])
}

func cleanValues(v []string) []string {
	var out []string
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" && !containsExact(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func containsExact(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !containsExact(b, x) {
			return false
		}
	}
	return true
}

func newID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
