//go:build windows

package ad

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	wldap                                                              *syscall.LazyDLL
	ldapInit, ldapSetOption, ldapBind                                  *syscall.LazyProc
	ldapConnect, ldapSearch, ldapFirstEntry, ldapNextEntry, ldapGetDN  *syscall.LazyProc
	ldapGetValues, ldapGetValuesLen, ldapValuesFree, ldapValuesFreeLen *syscall.LazyProc
	ldapMemFree, ldapMsgFree, ldapUnbind                               *syscall.LazyProc
	ldapFirstAttribute, ldapNextAttribute, berFree                     *syscall.LazyProc
	dllOnce                                                            sync.Once
	dllErr                                                             error
)

const (
	ldapOptTimeLimit       = 0x04
	ldapOptSizeLimit       = 0x03
	ldapOptProtocolVersion = 0x11
	ldapOptSSL             = 0x0a
	ldapOptReferrals       = 0x08
	ldapOptSign            = 0x95
	ldapOptEncrypt         = 0x96
	ldapOptSendTimeout     = 0x42
	ldapAuthNegotiate      = 0x486
	ldapSuccess            = 0
	ldapSizeLimitExceeded  = 4
	ldapNoSuchObject       = 32
	ldapScopeBase          = 0
	ldapScopeSubtree       = 2
)

// Attributes returned by normal searches are deliberately allowlisted. In
// particular, password, LAPS, key and recovery attributes are never requested.
var safeAttrs = []string{
	"objectClass", "cn", "displayName", "givenName", "sn", "sAMAccountName",
	"userPrincipalName", "mail", "proxyAddresses", "telephoneNumber", "title",
	"department", "company", "employeeID", "manager", "memberOf", "primaryGroupID",
	"userAccountControl", "whenCreated", "whenChanged", "lastLogon", "lastLogonTimestamp",
}

type ldapTimeval struct {
	Seconds      int32
	Microseconds int32
}

type ldapBerval struct {
	Length uint32
	Value  *byte
}

type windowsClient struct {
	mu      sync.Mutex
	conn    uintptr
	base    string
	closed  bool
	timeout time.Duration
}

func Open(ctx context.Context, cfg Config) (Client, error) {
	if cfg.Server == "" || cfg.BaseDN == "" {
		return nil, errors.New("server and base DN are required")
	}
	if cfg.Timeout < 0 || cfg.Timeout > 10*time.Minute {
		return nil, errors.New("timeout must be between zero and ten minutes")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := loadSystemLDAP(); err != nil {
		return nil, fmt.Errorf("load Windows LDAP client from system directory: %w", err)
	}
	host, portText, err := net.SplitHostPort(cfg.Server)
	if err != nil {
		host, portText = cfg.Server, ""
	}
	port := 389
	if cfg.LDAPS {
		port = 636
	}
	if portText != "" {
		port, err = strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid LDAP port %q", portText)
		}
	}
	hostp, err := syscall.UTF16PtrFromString(host)
	if err != nil {
		return nil, err
	}
	r, _, callErr := ldapInit.Call(uintptr(unsafe.Pointer(hostp)), uintptr(port))
	if r == 0 {
		return nil, fmt.Errorf("ldap_init failed: %v", callErr)
	}
	c := &windowsClient{conn: r, base: cfg.BaseDN, timeout: cfg.Timeout}
	cleanup := func(e error) (Client, error) { ldapUnbind.Call(c.conn); return nil, e }
	if err = c.setInt(ldapOptProtocolVersion, 3); err != nil {
		return cleanup(err)
	}
	if err = c.setFlag(ldapOptReferrals, false); err != nil {
		return cleanup(err)
	}
	if cfg.LDAPS {
		// Windows LDAP validates the TLS certificate against the system trust
		// store. No certificate-ignore switch is exposed.
		if err = c.setFlag(ldapOptSSL, true); err != nil {
			return cleanup(err)
		}
	} else {
		if err = c.setFlag(ldapOptSign, true); err != nil {
			return cleanup(err)
		}
		if err = c.setFlag(ldapOptEncrypt, true); err != nil {
			return cleanup(err)
		}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	c.timeout = timeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return cleanup(context.DeadlineExceeded)
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	seconds := int(timeout.Round(time.Second) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	if err = c.setInt(ldapOptTimeLimit, int32(seconds)); err != nil {
		return cleanup(err)
	}
	// WinLDAP documents LDAP_OPT_SEND_TIMEOUT in seconds.
	sendTimeoutSeconds := int32((timeout + time.Second - 1) / time.Second)
	if sendTimeoutSeconds < 1 {
		sendTimeoutSeconds = 1
	}
	if err = c.setInt(ldapOptSendTimeout, sendTimeoutSeconds); err != nil {
		return cleanup(err)
	}
	connectTimeout := ldapTimeval{Seconds: int32(timeout / time.Second), Microseconds: int32(timeout % time.Second / time.Microsecond)}
	connectCode, _, connectErr := ldapConnect.Call(c.conn, uintptr(unsafe.Pointer(&connectTimeout)))
	if connectCode != ldapSuccess {
		return cleanup(fmt.Errorf("LDAP connect failed: %d (%v)", connectCode, connectErr))
	}
	// Microsoft documents that ldap_bindW only supports SIMPLE. Negotiate
	// requires ldap_bind_sW; LDAP_OPT_TIMELIMIT bounds each bind-response
	// roundtrip. Go cancellation is checked when the native call returns.
	// https://learn.microsoft.com/windows/win32/api/winldap/nf-winldap-ldap_bindw
	if err := ctx.Err(); err != nil {
		return cleanup(err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return cleanup(context.DeadlineExceeded)
		}
		bindSeconds := int32((remaining + time.Second - 1) / time.Second)
		if bindSeconds < int32(seconds) {
			if err := c.setInt(ldapOptTimeLimit, bindSeconds); err != nil {
				return cleanup(err)
			}
		}
	}
	code, _, bindErr := ldapBind.Call(c.conn, 0, 0, ldapAuthNegotiate)
	if code != ldapSuccess {
		return cleanup(fmt.Errorf("Negotiate LDAP bind failed: %d (%v)", code, bindErr))
	}
	if err := ctx.Err(); err != nil {
		return cleanup(err)
	}
	return c, nil
}

func loadSystemLDAP() error {
	dllOnce.Do(func() {
		// kernel32 is a Windows KnownDLL. Query its trusted system directory,
		// then load WinLDAP by absolute path to avoid the current directory.
		kernel32 := syscall.NewLazyDLL("kernel32.dll")
		getSystemDirectory := kernel32.NewProc("GetSystemDirectoryW")
		buf := make([]uint16, 32768)
		n, _, callErr := getSystemDirectory.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n == 0 || n >= uintptr(len(buf)) {
			dllErr = fmt.Errorf("GetSystemDirectoryW failed: %v", callErr)
			return
		}
		path := syscall.UTF16ToString(buf[:n]) + `\wldap32.dll`
		wldap = syscall.NewLazyDLL(path)
		ldapInit = wldap.NewProc("ldap_initW")
		ldapSetOption = wldap.NewProc("ldap_set_optionW")
		ldapBind = wldap.NewProc("ldap_bind_sW")
		ldapConnect = wldap.NewProc("ldap_connect")
		ldapSearch = wldap.NewProc("ldap_search_ext_sW")
		ldapFirstEntry = wldap.NewProc("ldap_first_entry")
		ldapNextEntry = wldap.NewProc("ldap_next_entry")
		ldapGetDN = wldap.NewProc("ldap_get_dnW")
		ldapGetValues = wldap.NewProc("ldap_get_valuesW")
		ldapGetValuesLen = wldap.NewProc("ldap_get_values_lenW")
		ldapValuesFree = wldap.NewProc("ldap_value_freeW")
		ldapValuesFreeLen = wldap.NewProc("ldap_value_free_len")
		ldapMemFree = wldap.NewProc("ldap_memfreeW")
		ldapMsgFree = wldap.NewProc("ldap_msgfree")
		ldapUnbind = wldap.NewProc("ldap_unbind_s")
		ldapFirstAttribute = wldap.NewProc("ldap_first_attributeW")
		ldapNextAttribute = wldap.NewProc("ldap_next_attributeW")
		berFree = wldap.NewProc("ber_free")
		dllErr = wldap.Load()
	})
	return dllErr
}

func (c *windowsClient) setInt(option uintptr, value int32) error {
	// Keep the conversion in Proc.Call so the compiler pins value across the
	// syscall, including a possible Go stack move.
	r, _, e := ldapSetOption.Call(c.conn, option, uintptr(unsafe.Pointer(&value)))
	if r != ldapSuccess {
		return fmt.Errorf("ldap_set_option %x failed: %d (%v)", option, r, e)
	}
	return nil
}

// These LDAP options use LDAP_OPT_ON / LDAP_OPT_OFF pointer constants rather
// than pointers to ULONG values.
func (c *windowsClient) setFlag(option uintptr, enabled bool) error {
	var value uintptr
	if enabled {
		value = 1
	}
	return c.setOption(option, value)
}

func (c *windowsClient) setOption(option, value uintptr) error {
	r, _, e := ldapSetOption.Call(c.conn, option, value)
	if r != ldapSuccess {
		return fmt.Errorf("ldap_set_option %x failed: %d (%v)", option, r, e)
	}
	return nil
}

func (c *windowsClient) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}
	query := strings.TrimSpace(req.Query)
	filter := "(objectClass=*)"
	if query != "" {
		q := EscapeFilterValue(query)
		filter = "(|(anr=*" + q + "*)(sAMAccountName=*" + q + "*)(userPrincipalName=*" + q + "*)(mail=*" + q + "*)(telephoneNumber=*" + q + "*)(employeeID=*" + q + "*))"
	}
	limit := searchLimit(req.Limit)
	return c.search(ctx, c.base, ldapScopeSubtree, filter, limit)
}

func (c *windowsClient) search(ctx context.Context, base string, scope uintptr, filter string, limit int) (SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return SearchResult{}, ErrClosed
	}
	if limit < 1 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	if err := c.setInt(ldapOptSizeLimit, int32(limit+1)); err != nil {
		return SearchResult{}, err
	}
	basep, err := syscall.UTF16PtrFromString(base)
	if err != nil {
		return SearchResult{}, err
	}
	filterp, err := syscall.UTF16PtrFromString(filter)
	if err != nil {
		return SearchResult{}, err
	}
	attrPtrs := make([]*uint16, 0, len(safeAttrs)+1)
	for _, a := range safeAttrs {
		p, _ := syscall.UTF16PtrFromString(a)
		attrPtrs = append(attrPtrs, p)
	}
	attrPtrs = append(attrPtrs, nil)
	var msg uintptr
	searchTimeout := ldapTimeval{Seconds: int32(c.timeout / time.Second), Microseconds: int32(c.timeout % time.Second / time.Microsecond)}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return SearchResult{}, context.DeadlineExceeded
		}
		if remaining < c.timeout {
			searchTimeout = ldapTimeval{Seconds: int32(remaining / time.Second), Microseconds: int32(remaining % time.Second / time.Microsecond)}
		}
	}
	code, _, callErr := ldapSearch.Call(c.conn, uintptr(unsafe.Pointer(basep)), scope, uintptr(unsafe.Pointer(filterp)), uintptr(unsafe.Pointer(&attrPtrs[0])), 0, 0, 0, uintptr(unsafe.Pointer(&searchTimeout)), uintptr(limit+1), uintptr(unsafe.Pointer(&msg)))
	if msg != 0 {
		defer ldapMsgFree.Call(msg)
	}
	if code != ldapSuccess && code != ldapSizeLimitExceeded {
		if code == ldapNoSuchObject {
			return SearchResult{}, ErrNotFound
		}
		return SearchResult{}, fmt.Errorf("LDAP search failed: %d (%v)", code, callErr)
	}
	result := SearchResult{Objects: []Object{}}
	entry, _, _ := ldapFirstEntry.Call(c.conn, msg)
	for entry != 0 {
		if err := ctx.Err(); err != nil {
			return SearchResult{}, err
		}
		obj, err := c.readEntry(entry)
		if err != nil {
			return SearchResult{}, err
		}
		result.Objects = append(result.Objects, obj)
		if len(result.Objects) > limit {
			result.Objects = result.Objects[:limit]
			result.Truncated = true
			break
		}
		entry, _, _ = ldapNextEntry.Call(c.conn, entry)
	}
	if code == ldapSizeLimitExceeded {
		result.Truncated = true
	}
	return result, nil
}

func (c *windowsClient) readEntry(entry uintptr) (Object, error) {
	dnptr, _, _ := ldapGetDN.Call(c.conn, entry)
	if dnptr == 0 {
		return Object{}, errors.New("LDAP entry has no distinguished name")
	}
	dn, err := utf16FromPtr((*uint16)(nativePointer(dnptr)))
	ldapMemFree.Call(dnptr)
	if err != nil {
		return Object{}, err
	}
	attributes, err := c.responseAttributes(entry)
	if err != nil {
		return Object{}, err
	}
	obj := Object{DN: dn, Attributes: map[string][]string{}}
	for _, name := range safeAttrs {
		descriptor, exists := attributes[lowerASCII(name)]
		if !exists {
			continue
		}
		p, err := syscall.UTF16PtrFromString(descriptor)
		if err != nil {
			continue
		}
		values, _, _ := ldapGetValues.Call(c.conn, entry, uintptr(unsafe.Pointer(p)))
		if values == 0 {
			continue
		}
		valuesBase := values
		terminated := false
		for i := 0; i < 4096; i++ {
			v := *(*uintptr)(nativePointer(valuesBase + uintptr(i)*unsafe.Sizeof(uintptr(0))))
			if v == 0 {
				terminated = true
				break
			}
			text, err := utf16FromPtr((*uint16)(nativePointer(v)))
			if err != nil {
				ldapValuesFree.Call(valuesBase)
				return Object{}, err
			}
			obj.Attributes[lowerASCII(name)] = append(obj.Attributes[lowerASCII(name)], text)
		}
		if !terminated && *(*uintptr)(nativePointer(valuesBase + 4096*unsafe.Sizeof(uintptr(0)))) != 0 {
			ldapValuesFree.Call(valuesBase)
			return Object{}, fmt.Errorf("%w: LDAP attribute has too many values", ErrIncomplete)
		}
		ldapValuesFree.Call(valuesBase)
	}
	return obj, nil
}

func utf16FromPtr(p *uint16) (string, error) {
	if p == nil {
		return "", nil
	}
	var units []uint16
	for i := uintptr(0); i < 32768; i++ {
		v := *(*uint16)(unsafe.Add(unsafe.Pointer(p), i*2))
		if v == 0 {
			return syscall.UTF16ToString(units), nil
		}
		units = append(units, v)
	}
	return "", fmt.Errorf("%w: LDAP string exceeds the safety limit", ErrIncomplete)
}

// Inspect returned attribute descriptors before reading values. AD may rename
// a large multivalued attribute with ;range=0-N. Reading the bare name would
// silently omit those values. D1 rejects partial ranges instead of guessing.
func (c *windowsClient) responseAttributes(entry uintptr) (map[string]string, error) {
	var cursor uintptr
	nameptr, _, _ := ldapFirstAttribute.Call(c.conn, entry, uintptr(unsafe.Pointer(&cursor)))
	if cursor != 0 {
		defer berFree.Call(cursor, 0)
	}
	attributes := map[string]string{}
	for count := 0; nameptr != 0; count++ {
		descriptor, err := utf16FromPtr((*uint16)(nativePointer(nameptr)))
		ldapMemFree.Call(nameptr)
		if err != nil {
			return nil, err
		}
		if count >= 256 {
			return nil, fmt.Errorf("%w: too many LDAP attribute descriptors", ErrIncomplete)
		}
		base, err := completeAttribute(descriptor)
		if err != nil {
			return nil, err
		}
		if _, duplicate := attributes[base]; duplicate {
			return nil, fmt.Errorf("%w: duplicate LDAP attribute descriptor", ErrIncomplete)
		}
		attributes[base] = descriptor
		nameptr, _, _ = ldapNextAttribute.Call(c.conn, entry, cursor)
	}
	return attributes, nil
}

// nativePointer reinterprets a pointer returned by WinLDAP. The address is
// owned by WinLDAP and remains valid until the matching ldap_memfree/value_free.
func nativePointer(address uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}

func (c *windowsClient) Get(ctx context.Context, dn string) (Object, error) {
	res, err := c.search(ctx, dn, ldapScopeBase, "(objectClass=*)", 1)
	if err != nil {
		return Object{}, err
	}
	if len(res.Objects) == 0 {
		return Object{}, ErrNotFound
	}
	return res.Objects[0], nil
}

func (c *windowsClient) binaryAttribute(ctx context.Context, dn, attr string) ([][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	basep, err := syscall.UTF16PtrFromString(dn)
	if err != nil {
		return nil, err
	}
	filterp, _ := syscall.UTF16PtrFromString("(objectClass=*)")
	attrp, _ := syscall.UTF16PtrFromString(attr)
	attrs := []*uint16{attrp, nil}
	var msg uintptr
	duration := c.timeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, context.DeadlineExceeded
		}
		if remaining < duration {
			duration = remaining
		}
	}
	tm := ldapTimeval{Seconds: int32(duration / time.Second), Microseconds: int32(duration % time.Second / time.Microsecond)}
	code, _, callErr := ldapSearch.Call(c.conn, uintptr(unsafe.Pointer(basep)), ldapScopeBase, uintptr(unsafe.Pointer(filterp)), uintptr(unsafe.Pointer(&attrs[0])), 0, 0, 0, uintptr(unsafe.Pointer(&tm)), 1, uintptr(unsafe.Pointer(&msg)))
	if msg != 0 {
		defer ldapMsgFree.Call(msg)
	}
	if code != ldapSuccess {
		if code == 32 {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("LDAP binary attribute search failed: %d (%v)", code, callErr)
	}
	entry, _, _ := ldapFirstEntry.Call(c.conn, msg)
	if entry == 0 {
		return nil, ErrNotFound
	}
	attributes, err := c.responseAttributes(entry)
	if err != nil {
		return nil, err
	}
	descriptor, exists := attributes[lowerASCII(attr)]
	if !exists {
		return nil, nil
	}
	valueName, _ := syscall.UTF16PtrFromString(descriptor)
	values, _, _ := ldapGetValuesLen.Call(c.conn, entry, uintptr(unsafe.Pointer(valueName)))
	if values == 0 {
		return nil, nil
	}
	defer ldapValuesFreeLen.Call(values)
	out := [][]byte{}
	terminated := false
	for i := uintptr(0); i < 8192; i++ {
		vp := *(*uintptr)(nativePointer(values + i*unsafe.Sizeof(uintptr(0))))
		if vp == 0 {
			terminated = true
			break
		}
		bv := (*ldapBerval)(nativePointer(vp))
		// This private reader only requests objectSid and tokenGroups.
		// A SID is at most 68 bytes, not an arbitrary binary attribute.
		if bv.Length < 12 || bv.Length > 68 || bv.Value == nil {
			return nil, errors.New("LDAP returned an invalid SID length or pointer")
		}
		value := unsafe.Slice(bv.Value, int(bv.Length))
		if !validSID(value) {
			return nil, errors.New("LDAP returned a malformed SID")
		}
		out = append(out, append([]byte(nil), value...))
	}
	if !terminated {
		return nil, fmt.Errorf("%w: LDAP binary attribute has too many values", ErrIncomplete)
	}
	return out, nil
}

func (c *windowsClient) groupBySID(ctx context.Context, sid []byte, primary bool) (Group, error) {
	if !validSID(sid) {
		return Group{}, errors.New("AD returned a malformed group SID")
	}
	filter := "(objectSid=" + sidFilterValue(sid) + ")"
	res, err := c.search(ctx, c.base, ldapScopeSubtree, filter, 2)
	if err != nil {
		return Group{}, err
	}
	if res.Truncated || len(res.Objects) > 1 {
		return Group{}, errors.New("SID did not resolve to one object in the configured search base")
	}
	if len(res.Objects) == 0 {
		return Group{SID: sidText(sid), Primary: primary}, nil
	}
	o := res.Objects[0]
	return Group{DN: o.DN, Name: o.First("cn"), SID: sidText(sid), Primary: primary, Resolved: true}, nil
}

func (c *windowsClient) EffectiveGroups(ctx context.Context, dn string) ([]Group, error) {
	user, err := c.Get(ctx, dn)
	if err != nil {
		return nil, err
	}
	objectSID, err := c.binaryAttribute(ctx, dn, "objectSid")
	if err != nil {
		return nil, err
	}
	if len(objectSID) == 0 {
		return nil, errors.New("user objectSid is unavailable")
	}
	primaryID, err := strconv.ParseUint(user.First("primarygroupid"), 10, 32)
	if err != nil {
		return nil, errors.New("user primaryGroupID is unavailable or invalid")
	}
	primarySID, err := primaryGroupSID(objectSID[0], uint32(primaryID))
	if err != nil {
		return nil, err
	}
	tokenSIDs, err := c.binaryAttribute(ctx, dn, "tokenGroups")
	if err != nil {
		return nil, err
	}
	if len(tokenSIDs) == 0 {
		return nil, errors.New("AD did not return tokenGroups for this user; a Global Catalog must be available and the technician must be able to read the attribute")
	}
	seen := map[string]bool{}
	out := make([]Group, 0, len(tokenSIDs)+1)
	for _, sid := range tokenSIDs {
		key := sidFilterValue(sid)
		if seen[key] {
			continue
		}
		seen[key] = true
		group, err := c.groupBySID(ctx, sid, bytes.Equal(sid, primarySID))
		if err != nil {
			return nil, fmt.Errorf("resolve effective group SID %s: %w", sidText(sid), err)
		}
		out = append(out, group)
	}
	key := sidFilterValue(primarySID)
	if !seen[key] {
		group, err := c.groupBySID(ctx, primarySID, true)
		if err != nil {
			return nil, fmt.Errorf("resolve primary group SID %s: %w", sidText(primarySID), err)
		}
		out = append(out, group)
	}
	return out, nil
}

func (c *windowsClient) LastLogon(ctx context.Context, dn string) (LastLogonResult, error) {
	obj, err := c.Get(ctx, dn)
	if err != nil {
		return LastLogonResult{}, err
	}
	return LastLogonResult{LastLogon: fileTime(obj.First("lastlogon")), LastLogonTimestamp: fileTime(obj.First("lastlogontimestamp")), Partial: true, Warning: "lastLogon is local to the selected domain controller; query every DC for the forest-wide maximum"}, nil
}

func (c *windowsClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	r, _, e := ldapUnbind.Call(c.conn)
	if r != ldapSuccess {
		return fmt.Errorf("LDAP unbind failed: %d (%v)", r, e)
	}
	return nil
}
