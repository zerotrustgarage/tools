package ad

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// FakeDefaultRights is what RightsFunc grants when it is nil: the attributes
// and classes the planner and templates use.
var FakeDefaultRights = Rights{
	Attributes: []string{"cn", "name", "displayname", "givenname", "sn", "initials", "description", "samaccountname",
		"userprincipalname", "mail", "proxyaddresses", "telephonenumber", "mobile", "ipphone", "facsimiletelephonenumber",
		"title", "department", "company", "physicaldeliveryofficename", "streetaddress", "l", "st", "postalcode", "c", "co",
		"employeeid", "employeenumber", "employeetype", "manager", "info", "member", "useraccountcontrol", "accountexpires",
		"lockouttime", "pwdlastset", "managedby", "grouptype", "homedirectory", "homedrive", "scriptpath", "profilepath",
		"extensionattribute1", "extensionattribute2", "extensionattribute3", "extensionattribute4", "extensionattribute5",
		"extensionattribute6", "extensionattribute7", "extensionattribute8", "extensionattribute9", "extensionattribute10",
		"extensionattribute11", "extensionattribute12", "extensionattribute13", "extensionattribute14", "extensionattribute15",
		"msexchusagelocation", "wwwhomepage", "objectclass"},
	ChildClasses: []string{"user", "group", "contact", "organizationalunit", "inetorgperson"},
}

// fakeComputed are attributes AD maintains itself; writing them is refused.
var fakeComputed = map[string]bool{"memberof": true, "directreports": true, "whencreated": true, "whenchanged": true,
	"objectguid": true, "objectsid": true, "distinguishedname": true, "lastlogon": true, "lastlogontimestamp": true}

func (f *FakeClient) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now().UTC()
}

func (f *FakeClient) rights(dn string) Rights {
	if f.RightsFunc != nil {
		return f.RightsFunc(dn)
	}
	return FakeDefaultRights
}

func (f *FakeClient) prepare(ctx context.Context, op, dn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.closed {
		return ErrClosed
	}
	f.Ops = append(f.Ops, op+" "+dn)
	if f.FailOn != nil {
		if err := f.FailOn(op, dn); err != nil {
			return err
		}
	}
	return nil
}

func (f *FakeClient) index(dn string) int {
	for i, o := range f.Objects {
		if strings.EqualFold(o.DN, dn) {
			return i
		}
	}
	return -1
}

// Add implements Writer.
func (f *FakeClient) Add(ctx context.Context, dn string, attrs []Attribute) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.prepare(ctx, "add", dn); err != nil {
		return err
	}
	if f.index(dn) >= 0 {
		return ErrAlreadyExists
	}
	parent := ParentDN(dn)
	obj := Object{DN: dn, Attributes: map[string][]string{}}
	var class string
	for _, a := range attrs {
		name := lowerASCII(a.Name)
		if fakeComputed[name] {
			return fmt.Errorf("%w: %s is maintained by Active Directory", ErrConstraint, a.Name)
		}
		obj.Attributes[name] = append([]string(nil), a.Values...)
		if name == "objectclass" && len(a.Values) > 0 {
			class = lowerASCII(a.Values[len(a.Values)-1])
		}
	}
	if class == "" {
		return fmt.Errorf("%w: objectClass is required", ErrConstraint)
	}
	if !f.rights(parent).CanCreate(class) {
		return ErrInsufficientRights
	}
	if _, ok := obj.Attributes["cn"]; !ok {
		obj.Attributes["cn"] = []string{rdnValue(dn)}
	}
	now := f.now()
	obj.Attributes["whencreated"] = []string{generalizedTime(now)}
	obj.Attributes["whenchanged"] = []string{generalizedTime(now)}
	if class == "user" || class == "inetorgperson" {
		if obj.First("useraccountcontrol") == "" {
			obj.Attributes["useraccountcontrol"] = []string{fmt.Sprint(UACNormalAccount | UACAccountDisable)}
		}
		if obj.First("pwdlastset") == "" {
			obj.Attributes["pwdlastset"] = []string{"0"}
		}
	}
	f.Objects = append(f.Objects, obj)
	for _, m := range obj.Values("member") {
		f.addBacklink(m, "memberof", dn)
	}
	if m := obj.First("manager"); m != "" {
		f.addBacklink(m, "directreports", dn)
	}
	return nil
}

// Modify implements Writer.
func (f *FakeClient) Modify(ctx context.Context, dn string, mods []Mod) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.prepare(ctx, "modify", dn); err != nil {
		return err
	}
	i := f.index(dn)
	if i < 0 {
		return ErrNotFound
	}
	r := f.rights(dn)
	// Validate everything first so the modify is atomic, as in AD.
	work := cloneObjects([]Object{f.Objects[i]})[0]
	for _, m := range mods {
		name := lowerASCII(m.Name)
		if fakeComputed[name] {
			return fmt.Errorf("%w: %s is maintained by Active Directory", ErrConstraint, m.Name)
		}
		if !r.CanWrite(name) {
			return ErrInsufficientRights
		}
		cur := work.Attributes[name]
		switch m.Op {
		case ModReplace:
			if len(m.Values) == 0 {
				delete(work.Attributes, name)
			} else {
				work.Attributes[name] = append([]string(nil), m.Values...)
			}
		case ModAdd:
			for _, v := range m.Values {
				if containsFold(cur, v) {
					return fmt.Errorf("%w: %s already has the value %q", ErrConstraint, m.Name, v)
				}
				cur = append(cur, v)
			}
			work.Attributes[name] = cur
		case ModDelete:
			if len(m.Values) == 0 {
				delete(work.Attributes, name)
				continue
			}
			for _, v := range m.Values {
				if !containsFold(cur, v) {
					return fmt.Errorf("%w: %s does not have the value %q", ErrConstraint, m.Name, v)
				}
				cur = removeFold(cur, v)
			}
			if len(cur) == 0 {
				delete(work.Attributes, name)
			} else {
				work.Attributes[name] = cur
			}
		default:
			return fmt.Errorf("unknown modify operation %d", m.Op)
		}
	}
	old := f.Objects[i]
	work.Attributes["whenchanged"] = []string{generalizedTime(f.now())}
	f.Objects[i] = work
	f.syncBacklinks(dn, "member", "memberof", old.Values("member"), work.Values("member"))
	f.syncBacklinks(dn, "manager", "directreports", old.Values("manager"), work.Values("manager"))
	return nil
}

// Delete implements Writer.
func (f *FakeClient) Delete(ctx context.Context, dn string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.prepare(ctx, "delete", dn); err != nil {
		return err
	}
	i := f.index(dn)
	if i < 0 {
		return ErrNotFound
	}
	for _, o := range f.Objects {
		if strings.EqualFold(ParentDN(o.DN), dn) {
			return fmt.Errorf("%w: the object has children", ErrConstraint)
		}
	}
	obj := f.Objects[i]
	f.Objects = append(f.Objects[:i], f.Objects[i+1:]...)
	f.syncBacklinks(dn, "member", "memberof", obj.Values("member"), nil)
	f.syncBacklinks(dn, "manager", "directreports", obj.Values("manager"), nil)
	for j := range f.Objects {
		if containsFold(f.Objects[j].Values("member"), dn) {
			f.Objects[j].Attributes["member"] = removeFold(f.Objects[j].Values("member"), dn)
		}
		if strings.EqualFold(f.Objects[j].First("manager"), dn) {
			delete(f.Objects[j].Attributes, "manager")
		}
	}
	deleted := cloneObjects([]Object{obj})[0]
	deleted.Attributes["isdeleted"] = []string{"TRUE"}
	deleted.Attributes["lastknownparent"] = []string{ParentDN(dn)}
	deleted.Attributes["lastknownmemberof"] = obj.Values("memberof")
	deleted.DN = RDN(dn) + `\0ADEL:fake,CN=Deleted Objects`
	f.DeletedObjects = append(f.DeletedObjects, deleted)
	return nil
}

// Rename implements Writer.
func (f *FakeClient) Rename(ctx context.Context, dn, newRDN, newParent string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.prepare(ctx, "rename", dn); err != nil {
		return err
	}
	i := f.index(dn)
	if i < 0 {
		return ErrNotFound
	}
	if newRDN == "" {
		newRDN = RDN(dn)
	}
	parent := newParent
	if parent == "" {
		parent = ParentDN(dn)
	}
	newDN := newRDN + "," + parent
	if !strings.EqualFold(newDN, dn) && f.index(newDN) >= 0 {
		return ErrAlreadyExists
	}
	if newParent != "" && !f.rights(newParent).CanCreate(lastClass(f.Objects[i])) {
		return ErrInsufficientRights
	}
	f.Objects[i].DN = newDN
	f.Objects[i].Attributes["cn"] = []string{rdnValue(newDN)}
	for j := range f.Objects {
		for _, a := range []string{"member", "memberof", "manager", "directreports", "managedby"} {
			vals := f.Objects[j].Attributes[a]
			for k, v := range vals {
				if strings.EqualFold(v, dn) {
					vals[k] = newDN
				}
			}
		}
	}
	return nil
}

// SetPassword implements Writer.
func (f *FakeClient) SetPassword(ctx context.Context, dn string, password []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.prepare(ctx, "password", dn); err != nil {
		return err
	}
	i := f.index(dn)
	if i < 0 {
		return ErrNotFound
	}
	if f.PasswordCheck != nil && !f.PasswordCheck(password) {
		return fmt.Errorf("%w: the password does not meet the domain's password policy", ErrConstraint)
	}
	if f.Passwords == nil {
		f.Passwords = map[string]int{}
	}
	f.Passwords[lowerASCII(dn)]++
	f.Objects[i].Attributes["pwdlastset"] = []string{ToFileTime(f.now())}
	return nil
}

// Undelete implements Writer.
func (f *FakeClient) Undelete(ctx context.Context, deletedDN, newDN string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.prepare(ctx, "undelete", deletedDN); err != nil {
		return err
	}
	for i, o := range f.DeletedObjects {
		if strings.EqualFold(o.DN, deletedDN) {
			if f.index(newDN) >= 0 {
				return ErrAlreadyExists
			}
			restored := cloneObjects([]Object{o})[0]
			restored.DN = newDN
			delete(restored.Attributes, "isdeleted")
			delete(restored.Attributes, "lastknownparent")
			delete(restored.Attributes, "lastknownmemberof")
			delete(restored.Attributes, "memberof")
			f.DeletedObjects = append(f.DeletedObjects[:i], f.DeletedObjects[i+1:]...)
			f.Objects = append(f.Objects, restored)
			for _, g := range o.Values("lastknownmemberof") {
				if j := f.index(g); j >= 0 {
					f.Objects[j].Attributes["member"] = append(f.Objects[j].Attributes["member"], newDN)
					f.addBacklink(newDN, "memberof", g)
				}
			}
			return nil
		}
	}
	return ErrNotFound
}

// Deleted implements Writer.
func (f *FakeClient) Deleted(ctx context.Context, req SearchRequest) (SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Object
	inner := req
	inner.Under, inner.OneLevel = "", false
	for _, o := range f.DeletedObjects {
		if Matches(inner, o) {
			out = append(out, o)
		}
	}
	limit := searchLimit(req.Limit)
	truncated := len(out) > limit
	if truncated {
		out = out[:limit]
	}
	return SearchResult{Objects: cloneObjects(out), Truncated: truncated}, nil
}

// Rights implements Writer.
func (f *FakeClient) Rights(ctx context.Context, dn string) (Rights, error) {
	if err := ctx.Err(); err != nil {
		return Rights{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.rights(dn)
	return Rights{Attributes: append([]string(nil), r.Attributes...), ChildClasses: append([]string(nil), r.ChildClasses...)}, nil
}

// DC implements Writer.
func (f *FakeClient) DC() string {
	if f.DCName == "" {
		return "fake-dc"
	}
	return f.DCName
}

// Identity implements Writer.
func (f *FakeClient) Identity() string {
	if f.Account == "" {
		return `FAKE\technician`
	}
	return f.Account
}

func (f *FakeClient) addBacklink(target, attr, value string) {
	if j := f.index(target); j >= 0 && !containsFold(f.Objects[j].Values(attr), value) {
		f.Objects[j].Attributes[attr] = append(f.Objects[j].Attributes[attr], value)
		sort.Strings(f.Objects[j].Attributes[attr])
	}
}

func (f *FakeClient) syncBacklinks(dn, _ string, back string, before, after []string) {
	for _, v := range before {
		if !containsFold(after, v) {
			if j := f.index(v); j >= 0 {
				f.Objects[j].Attributes[back] = removeFold(f.Objects[j].Values(back), dn)
				if len(f.Objects[j].Attributes[back]) == 0 {
					delete(f.Objects[j].Attributes, back)
				}
			}
		}
	}
	for _, v := range after {
		if !containsFold(before, v) {
			f.addBacklink(v, back, dn)
		}
	}
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

func removeFold(list []string, v string) []string {
	out := list[:0:0]
	for _, x := range list {
		if !strings.EqualFold(x, v) {
			out = append(out, x)
		}
	}
	return out
}

func rdnValue(dn string) string {
	r := RDN(dn)
	if i := strings.IndexByte(r, '='); i >= 0 {
		return unescapeRDN(r[i+1:])
	}
	return r
}

func unescapeRDN(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) {
			i++
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

func lastClass(o Object) string {
	c := o.Values("objectclass")
	if len(c) == 0 {
		return ""
	}
	return lowerASCII(c[len(c)-1])
}

var _ Writer = (*FakeClient)(nil)
