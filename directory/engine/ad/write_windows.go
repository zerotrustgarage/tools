//go:build windows

package ad

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// LDAPModW operations (winldap.h).
const (
	ldapModAdd     = 0x00
	ldapModDelete  = 0x01
	ldapModReplace = 0x02
	ldapModBValues = 0x80
)

// ldapServerShowDeletedOID returns deleted objects (MS-ADTS 3.1.1.3.4.1.14).
const ldapServerShowDeletedOID = "1.2.840.113556.1.4.417"

// ldapMod mirrors LDAPModW: mod_op, mod_type and the mod_vals union
// (PWCHAR* or struct berval**). Values points at Go memory, so it is an
// unsafe.Pointer the garbage collector keeps alive.
type ldapMod struct {
	Op     uint32
	Type   *uint16
	Values unsafe.Pointer
}

// ldapControl mirrors LDAPControlW.
type ldapControl struct {
	OID        *uint16
	Value      ldapBerval
	IsCritical byte
}

// modList builds a NULL-terminated LDAPModW* array whose memory stays
// reachable from the list until the call returns.
type modList struct {
	mods    []*ldapMod
	strings [][]*uint16
	bervals [][]*ldapBerval
}

func (l *modList) addStrings(op uint32, name string, values []string) error {
	if secretAttribute(name) {
		return fmt.Errorf("%s is written only through SetPassword", name)
	}
	typ, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return fmt.Errorf("attribute name %q: %w", name, err)
	}
	m := &ldapMod{Op: op, Type: typ}
	if len(values) > 0 {
		ptrs := make([]*uint16, 0, len(values)+1)
		for _, v := range values {
			p, err := syscall.UTF16PtrFromString(v)
			if err != nil {
				return fmt.Errorf("%w: a value of %s contains a NUL character", ErrConstraint, name)
			}
			ptrs = append(ptrs, p)
		}
		ptrs = append(ptrs, nil)
		l.strings = append(l.strings, ptrs)
		m.Values = unsafe.Pointer(&ptrs[0])
	}
	l.mods = append(l.mods, m)
	return nil
}

func (l *modList) addBinary(op uint32, name string, values [][]byte) error {
	typ, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	ptrs := make([]*ldapBerval, 0, len(values)+1)
	for _, v := range values {
		if len(v) == 0 {
			return fmt.Errorf("%w: empty binary value for %s", ErrConstraint, name)
		}
		ptrs = append(ptrs, &ldapBerval{Length: uint32(len(v)), Value: &v[0]})
	}
	ptrs = append(ptrs, nil)
	l.bervals = append(l.bervals, ptrs)
	l.mods = append(l.mods, &ldapMod{Op: op | ldapModBValues, Type: typ, Values: unsafe.Pointer(&ptrs[0])})
	return nil
}

// array returns the NULL-terminated LDAPModW* array.
func (l *modList) array() []*ldapMod { return append(append([]*ldapMod(nil), l.mods...), nil) }

func modOp(op ModOp) (uint32, error) {
	switch op {
	case ModAdd:
		return ldapModAdd, nil
	case ModDelete:
		return ldapModDelete, nil
	case ModReplace:
		return ldapModReplace, nil
	}
	return 0, fmt.Errorf("unknown modify operation %d", op)
}

// showDeletedControl is the critical LDAP_SERVER_SHOW_DELETED_OID control.
func showDeletedControl() (*ldapControl, error) {
	oid, err := syscall.UTF16PtrFromString(ldapServerShowDeletedOID)
	if err != nil {
		return nil, err
	}
	return &ldapControl{OID: oid, IsCritical: 1}, nil
}

// write runs one synchronous write under mu and maps its LDAP result. AD's
// diagnostic is read on the same session before mu is released. A write that
// AD accepted is reported as done even if ctx ended meanwhile.
func (c *windowsClient) write(ctx context.Context, op string, call func() uintptr) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	if code := call(); code != ldapSuccess {
		return ldapResultError(op, uint32(code), c.serverError())
	}
	return nil
}

func utf16Ptr(s string) (*uint16, error) {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil, fmt.Errorf("%q contains a NUL character", s)
	}
	return p, nil
}

// Add implements Writer (ldap_add_ext_sW).
func (c *windowsClient) Add(ctx context.Context, dn string, attrs []Attribute) error {
	dnp, err := utf16Ptr(dn)
	if err != nil {
		return err
	}
	var list modList
	hasClass := false
	for _, a := range attrs {
		if len(a.Values) == 0 {
			return fmt.Errorf("%w: %s has no values", ErrConstraint, a.Name)
		}
		if strings.EqualFold(a.Name, "objectClass") {
			hasClass = true
		}
		if err := list.addStrings(ldapModAdd, a.Name, a.Values); err != nil {
			return err
		}
	}
	if !hasClass {
		return fmt.Errorf("%w: objectClass is required", ErrConstraint)
	}
	mods := list.array()
	return c.write(ctx, "add "+dn, func() uintptr {
		code, _, _ := ldapAdd.Call(c.conn, uintptr(unsafe.Pointer(dnp)), uintptr(unsafe.Pointer(&mods[0])), 0, 0)
		runtime.KeepAlive(&list)
		return code
	})
}

// Modify implements Writer (ldap_modify_ext_sW). AD applies the mods
// atomically.
func (c *windowsClient) Modify(ctx context.Context, dn string, mods []Mod) error {
	if len(mods) == 0 {
		return errors.New("no modifications")
	}
	dnp, err := utf16Ptr(dn)
	if err != nil {
		return err
	}
	var list modList
	for _, m := range mods {
		op, err := modOp(m.Op)
		if err != nil {
			return err
		}
		if err := list.addStrings(op, m.Name, m.Values); err != nil {
			return err
		}
	}
	array := list.array()
	return c.write(ctx, "modify "+dn, func() uintptr {
		code, _, _ := ldapModify.Call(c.conn, uintptr(unsafe.Pointer(dnp)), uintptr(unsafe.Pointer(&array[0])), 0, 0)
		runtime.KeepAlive(&list)
		return code
	})
}

// Delete implements Writer (ldap_delete_ext_sW).
func (c *windowsClient) Delete(ctx context.Context, dn string) error {
	dnp, err := utf16Ptr(dn)
	if err != nil {
		return err
	}
	return c.write(ctx, "delete "+dn, func() uintptr {
		code, _, _ := ldapDelete.Call(c.conn, uintptr(unsafe.Pointer(dnp)), 0, 0)
		return code
	})
}

// Rename implements Writer (ldap_rename_ext_sW). The old RDN value is
// deleted; an empty newRDN keeps the current one.
func (c *windowsClient) Rename(ctx context.Context, dn, newRDN, newParent string) error {
	if newRDN == "" {
		newRDN = RDN(dn)
	}
	dnp, err := utf16Ptr(dn)
	if err != nil {
		return err
	}
	rdnp, err := utf16Ptr(newRDN)
	if err != nil {
		return err
	}
	var parentp *uint16
	if newParent != "" {
		if parentp, err = utf16Ptr(newParent); err != nil {
			return err
		}
	}
	return c.write(ctx, "rename "+dn, func() uintptr {
		code, _, _ := ldapRename.Call(c.conn, uintptr(unsafe.Pointer(dnp)), uintptr(unsafe.Pointer(rdnp)), uintptr(unsafe.Pointer(parentp)), 1, 0, 0)
		return code
	})
}

// SetPassword implements Writer: it replaces unicodePwd with the quoted
// UTF-16LE password. A DC accepts this only over TLS or a sealed SASL layer
// (MS-ADTS 3.1.1.3.1.5.1), and the client refuses anything else before the
// password leaves memory. Every copy is erased before returning.
func (c *windowsClient) SetPassword(ctx context.Context, dn string, password []byte) error {
	if !c.ldaps && !c.sealed {
		return errNotSealed
	}
	dnp, err := utf16Ptr(dn)
	if err != nil {
		return err
	}
	value, err := unicodePwdValue(password)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrConstraint, err)
	}
	defer wipeBytes(value)
	var list modList
	if err := list.addBinary(ldapModReplace, "unicodePwd", [][]byte{value}); err != nil {
		return err
	}
	array := list.array()
	return c.write(ctx, "set password on "+dn, func() uintptr {
		code, _, _ := ldapModify.Call(c.conn, uintptr(unsafe.Pointer(dnp)), uintptr(unsafe.Pointer(&array[0])), 0, 0)
		runtime.KeepAlive(&list)
		runtime.KeepAlive(value)
		return code
	})
}

// Undelete implements Writer. Per MS-ADTS 3.1.1.5.3.7.2 it is one modify,
// sent with the show-deleted control, that removes isDeleted and replaces
// distinguishedName with the object's new DN.
func (c *windowsClient) Undelete(ctx context.Context, deletedDN, newDN string) error {
	if strings.TrimSpace(newDN) == "" {
		return errors.New("a restored object needs a new DN")
	}
	dnp, err := utf16Ptr(deletedDN)
	if err != nil {
		return err
	}
	var list modList
	if err := list.addStrings(ldapModDelete, "isDeleted", nil); err != nil {
		return err
	}
	if err := list.addStrings(ldapModReplace, "distinguishedName", []string{newDN}); err != nil {
		return err
	}
	array := list.array()
	control, err := showDeletedControl()
	if err != nil {
		return err
	}
	controls := []*ldapControl{control, nil}
	return c.write(ctx, "undelete "+deletedDN, func() uintptr {
		code, _, _ := ldapModify.Call(c.conn, uintptr(unsafe.Pointer(dnp)), uintptr(unsafe.Pointer(&array[0])), uintptr(unsafe.Pointer(&controls[0])), 0)
		runtime.KeepAlive(&list)
		runtime.KeepAlive(controls)
		return code
	})
}

// Deleted implements Writer: it searches the domain's Deleted Objects
// container with the show-deleted control. Deleted objects are direct
// children of that container, so the search is one level, which also keeps
// the container itself (isDeleted=TRUE too) out of the results.
func (c *windowsClient) Deleted(ctx context.Context, req SearchRequest) (SearchResult, error) {
	filter, err := deletedFilter(req)
	if err != nil {
		return SearchResult{}, err
	}
	nc, err := c.defaultNamingContext(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	control, err := showDeletedControl()
	if err != nil {
		return SearchResult{}, err
	}
	return c.searchWith(ctx, searchSpec{base: deletedObjectsDN(nc), scope: ldapScopeOneLevel, filter: filter,
		limit: searchLimit(req.Limit), attrs: deletedAttrs, controls: []*ldapControl{control}})
}

// Rights implements Writer from the constructed allowedAttributesEffective
// and allowedChildClassesEffective attributes, read as the bound account.
func (c *windowsClient) Rights(ctx context.Context, dn string) (Rights, error) {
	res, err := c.searchWith(ctx, searchSpec{base: dn, scope: ldapScopeBase, filter: "(objectClass=*)", limit: 1, attrs: rightsAttrs})
	if err != nil {
		return Rights{}, err
	}
	if len(res.Objects) == 0 {
		return Rights{}, ErrNotFound
	}
	o := res.Objects[0]
	return Rights{Attributes: lowerAll(o.Values("allowedAttributesEffective")), ChildClasses: lowerAll(o.Values("allowedChildClassesEffective"))}, nil
}

// DC implements Writer.
func (c *windowsClient) DC() string { return c.dc }

// Identity implements Writer.
func (c *windowsClient) Identity() string { return c.identity }

var _ Writer = (*windowsClient)(nil)
