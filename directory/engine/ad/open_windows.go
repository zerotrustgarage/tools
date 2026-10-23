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
	searchTimeout, err := c.searchTimeout(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	code, _, callErr := ldapSearch.Call(c.conn, uintptr(unsafe.Pointer(basep)), scope, uintptr(unsafe.Pointer(filterp)), uintptr(unsafe.Pointer(&attrPtrs[0])), 0, 0, 0, uintptr(unsafe.Pointer(&searchTimeout)), uintptr(limit+1), uintptr(unsafe.Pointer(&msg)))
	if msg != 0 {
		defer ldapMsgFree.Call(msg)
	}
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
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
		obj, err := c.readEntry(ctx, entry)
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

func (c *windowsClient) readEntry(ctx context.Context, entry uintptr) (Object, error) {
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
		if descriptor.placeholder != "" && !strings.EqualFold(descriptor.placeholder, name) {
			return Object{}, fmt.Errorf("%w: AD returned an unexpected empty attribute placeholder", ErrIncomplete)
		}
		values, err := c.stringAttributeValues(ctx, dn, name, entry, descriptor)
		if err != nil {
			return Object{}, err
		}
		obj.Attributes[lowerASCII(name)] = values
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

// Inspect descriptors before reading values. A partial AD range is retained
// for the bounded follow-up reader rather than mistaken for a complete value.
func (c *windowsClient) responseAttributes(entry uintptr) (map[string]attributeDescription, error) {
	var cursor uintptr
	nameptr, _, _ := ldapFirstAttribute.Call(c.conn, entry, uintptr(unsafe.Pointer(&cursor)))
	if cursor != 0 {
		defer berFree.Call(cursor, 0)
	}
	attributes := map[string]attributeDescription{}
	for count := 0; nameptr != 0; count++ {
		descriptor, err := utf16FromPtr((*uint16)(nativePointer(nameptr)))
		ldapMemFree.Call(nameptr)
		if err != nil {
			return nil, err
		}
		if count >= 256 {
			return nil, fmt.Errorf("%w: too many LDAP attribute descriptors", ErrIncomplete)
		}
		parsed, err := parseAttributeDescription(descriptor)
		if err != nil {
			return nil, err
		}
		if previous, duplicate := attributes[parsed.name]; duplicate {
			if previous.placeholder != "" {
				return nil, fmt.Errorf("%w: multiple empty LDAP placeholders", ErrIncomplete)
			}
			previousHasValues, err := c.attributeHasValues(entry, previous.descriptor)
			if err != nil {
				return nil, err
			}
			currentHasValues, err := c.attributeHasValues(entry, parsed.descriptor)
			if err != nil {
				return nil, err
			}
			// AD documents an empty descriptor for the requested name alongside
			// the one descriptor that carries the returned range. Accept only
			// this placeholder pattern; two data-bearing descriptors or two
			// empty descriptors are ambiguous and fail closed.
			if previousHasValues == currentHasValues {
				return nil, fmt.Errorf("%w: duplicate LDAP attribute descriptors", ErrIncomplete)
			}
			populated, placeholder := parsed, previous
			if previousHasValues {
				populated, placeholder = previous, parsed
			}
			if !validRangePlaceholder(populated, placeholder) {
				return nil, fmt.Errorf("%w: conflicting LDAP range descriptors", ErrIncomplete)
			}
			if currentHasValues {
				parsed.placeholder = previous.descriptor
				attributes[parsed.name] = parsed
			} else {
				previous.placeholder = parsed.descriptor
				attributes[parsed.name] = previous
			}
		} else {
			attributes[parsed.name] = parsed
		}
		nameptr, _, _ = ldapNextAttribute.Call(c.conn, entry, cursor)
	}
	return attributes, nil
}

func validRangePlaceholder(populated, placeholder attributeDescription) bool {
	if !populated.ranged {
		return false
	}
	if !placeholder.ranged {
		return populated.start == 0
	}
	return placeholder.terminal && placeholder.start == populated.start && !populated.terminal
}

func (c *windowsClient) attributeHasValues(entry uintptr, descriptor string) (bool, error) {
	p, err := syscall.UTF16PtrFromString(descriptor)
	if err != nil {
		return false, err
	}
	values, _, _ := ldapGetValuesLen.Call(c.conn, entry, uintptr(unsafe.Pointer(p)))
	if values == 0 {
		return false, nil
	}
	defer ldapValuesFreeLen.Call(values)
	first := *(*uintptr)(nativePointer(values))
	return first != 0, nil
}

func (c *windowsClient) stringAttributeValues(ctx context.Context, dn, attr string, entry uintptr, d attributeDescription) ([]string, error) {
	values, err := c.readStringValues(entry, d.descriptor)
	if err != nil {
		return nil, err
	}
	if !d.ranged {
		if len(values) > 4096 {
			return nil, fmt.Errorf("%w: LDAP attribute exceeds the 4096 value limit", ErrIncomplete)
		}
		return values, nil
	}
	start, done, err := validateRangePage(d, 0, len(values), 4096)
	if err != nil {
		return nil, err
	}
	all := append([]string(nil), values...)
	pages := 1
	for !done {
		if pages >= 16 {
			return nil, fmt.Errorf("%w: AD string range exceeded the 16 page limit", ErrIncomplete)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pageDN, pageDesc, pageValues, err := c.fetchStringRangeLocked(ctx, dn, attr, start)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(pageDN, dn) || pageDesc.name != lowerASCII(attr) {
			return nil, fmt.Errorf("%w: AD range response did not match the requested object and attribute", ErrIncomplete)
		}
		start, done, err = validateRangePage(pageDesc, start, len(pageValues), 4096)
		if err != nil {
			return nil, err
		}
		if len(all)+len(pageValues) > 4096 {
			return nil, fmt.Errorf("%w: AD ranged attribute exceeds the 4096 value limit", ErrIncomplete)
		}
		all = append(all, pageValues...)
		pages++
	}
	return all, nil
}

func (c *windowsClient) readStringValues(entry uintptr, descriptor string) ([]string, error) {
	p, err := syscall.UTF16PtrFromString(descriptor)
	if err != nil {
		return nil, err
	}
	values, _, _ := ldapGetValues.Call(c.conn, entry, uintptr(unsafe.Pointer(p)))
	if values == 0 {
		return nil, nil
	}
	defer ldapValuesFree.Call(values)
	out := []string{}
	for i := 0; i <= 4096; i++ {
		v := *(*uintptr)(nativePointer(values + uintptr(i)*unsafe.Sizeof(uintptr(0))))
		if v == 0 {
			return out, nil
		}
		if i == 4096 {
			return nil, fmt.Errorf("%w: LDAP attribute has too many values", ErrIncomplete)
		}
		text, err := utf16FromPtr((*uint16)(nativePointer(v)))
		if err != nil {
			return nil, err
		}
		out = append(out, text)
	}
	return nil, fmt.Errorf("%w: unterminated LDAP values", ErrIncomplete)
}

// fetchStringRangeLocked runs one exact-DN BASE query. Callers already own mu.
func (c *windowsClient) fetchStringRangeLocked(ctx context.Context, dn, attr string, start int) (string, attributeDescription, []string, error) {
	requestAttr := fmt.Sprintf("%s;range=%d-*", attr, start)
	basep, err := syscall.UTF16PtrFromString(dn)
	if err != nil {
		return "", attributeDescription{}, nil, err
	}
	filterp, _ := syscall.UTF16PtrFromString("(objectClass=*)")
	attrp, _ := syscall.UTF16PtrFromString(requestAttr)
	attrs := []*uint16{attrp, nil}
	tm, err := c.searchTimeout(ctx)
	if err != nil {
		return "", attributeDescription{}, nil, err
	}
	var msg uintptr
	code, _, callErr := ldapSearch.Call(c.conn, uintptr(unsafe.Pointer(basep)), ldapScopeBase, uintptr(unsafe.Pointer(filterp)), uintptr(unsafe.Pointer(&attrs[0])), 0, 0, 0, uintptr(unsafe.Pointer(&tm)), 1, uintptr(unsafe.Pointer(&msg)))
	if msg != 0 {
		defer ldapMsgFree.Call(msg)
	}
	if err := ctx.Err(); err != nil {
		return "", attributeDescription{}, nil, err
	}
	if code != ldapSuccess {
		if code == ldapNoSuchObject {
			return "", attributeDescription{}, nil, ErrIncomplete
		}
		return "", attributeDescription{}, nil, fmt.Errorf("LDAP range search failed: %d (%v)", code, callErr)
	}
	if err := ctx.Err(); err != nil {
		return "", attributeDescription{}, nil, err
	}
	entry, _, _ := ldapFirstEntry.Call(c.conn, msg)
	if entry == 0 {
		return "", attributeDescription{}, nil, fmt.Errorf("%w: AD omitted the requested range attribute", ErrIncomplete)
	}
	dnptr, _, _ := ldapGetDN.Call(c.conn, entry)
	if dnptr == 0 {
		return "", attributeDescription{}, nil, fmt.Errorf("%w: range response has no distinguished name", ErrIncomplete)
	}
	pageDN, dnErr := utf16FromPtr((*uint16)(nativePointer(dnptr)))
	ldapMemFree.Call(dnptr)
	if dnErr != nil {
		return "", attributeDescription{}, nil, dnErr
	}
	attributes, err := c.responseAttributes(entry)
	if err != nil {
		return "", attributeDescription{}, nil, err
	}
	d, ok := attributes[lowerASCII(attr)]
	if !ok {
		return "", attributeDescription{}, nil, fmt.Errorf("%w: AD omitted the requested range attribute", ErrIncomplete)
	}
	if d.placeholder != "" && !strings.EqualFold(d.placeholder, requestAttr) {
		return "", attributeDescription{}, nil, fmt.Errorf("%w: AD returned an unexpected empty range placeholder", ErrIncomplete)
	}
	if !d.ranged {
		return "", attributeDescription{}, nil, fmt.Errorf("%w: AD returned an un-ranged follow-up attribute", ErrIncomplete)
	}
	values, err := c.readStringValues(entry, d.descriptor)
	return pageDN, d, values, err
}

func (c *windowsClient) searchTimeout(ctx context.Context) (ldapTimeval, error) {
	if err := ctx.Err(); err != nil {
		return ldapTimeval{}, err
	}
	duration := c.timeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return ldapTimeval{}, context.DeadlineExceeded
		}
		if remaining < duration {
			duration = remaining
		}
	}
	return ldapTimeval{Seconds: int32(duration / time.Second), Microseconds: int32(duration % time.Second / time.Microsecond)}, nil
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
	tm, err := c.searchTimeout(ctx)
	if err != nil {
		return nil, err
	}
	code, _, callErr := ldapSearch.Call(c.conn, uintptr(unsafe.Pointer(basep)), ldapScopeBase, uintptr(unsafe.Pointer(filterp)), uintptr(unsafe.Pointer(&attrs[0])), 0, 0, 0, uintptr(unsafe.Pointer(&tm)), 1, uintptr(unsafe.Pointer(&msg)))
	if msg != 0 {
		defer ldapMsgFree.Call(msg)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if code != ldapSuccess {
		if code == 32 {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("LDAP binary attribute search failed: %d (%v)", code, callErr)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
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
	if descriptor.placeholder != "" && !strings.EqualFold(descriptor.placeholder, attr) {
		return nil, fmt.Errorf("%w: AD returned an unexpected empty binary attribute placeholder", ErrIncomplete)
	}
	dnptr, _, _ := ldapGetDN.Call(c.conn, entry)
	if dnptr == 0 {
		return nil, fmt.Errorf("%w: binary attribute response has no distinguished name", ErrIncomplete)
	}
	canonicalDN, err := utf16FromPtr((*uint16)(nativePointer(dnptr)))
	ldapMemFree.Call(dnptr)
	if err != nil {
		return nil, err
	}
	page, err := c.readBinaryValues(entry, descriptor.descriptor)
	if err != nil {
		return nil, err
	}
	if !descriptor.ranged {
		return page, nil
	}
	start, done, err := validateRangePage(descriptor, 0, len(page), 8192)
	if err != nil {
		return nil, err
	}
	all := append([][]byte(nil), page...)
	pages := 1
	for !done {
		if pages >= 16 {
			return nil, fmt.Errorf("%w: AD binary range exceeded the 16 page limit", ErrIncomplete)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pageDN, pageDesc, pageValues, err := c.fetchBinaryRangeLocked(ctx, canonicalDN, attr, start)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(pageDN, canonicalDN) || pageDesc.name != lowerASCII(attr) {
			return nil, fmt.Errorf("%w: AD binary range response did not match the requested object and attribute", ErrIncomplete)
		}
		start, done, err = validateRangePage(pageDesc, start, len(pageValues), 8192)
		if err != nil {
			return nil, err
		}
		if len(all)+len(pageValues) > 8192 {
			return nil, fmt.Errorf("%w: AD binary range exceeds the 8192 value limit", ErrIncomplete)
		}
		all = append(all, pageValues...)
		pages++
	}
	return all, nil
}

func (c *windowsClient) readBinaryValues(entry uintptr, descriptor string) ([][]byte, error) {
	valueName, err := syscall.UTF16PtrFromString(descriptor)
	if err != nil {
		return nil, err
	}
	values, _, _ := ldapGetValuesLen.Call(c.conn, entry, uintptr(unsafe.Pointer(valueName)))
	if values == 0 {
		return nil, nil
	}
	defer ldapValuesFreeLen.Call(values)
	out := [][]byte{}
	for i := uintptr(0); i <= 8192; i++ {
		vp := *(*uintptr)(nativePointer(values + i*unsafe.Sizeof(uintptr(0))))
		if vp == 0 {
			return out, nil
		}
		if i == 8192 {
			break
		}
		bv := (*ldapBerval)(nativePointer(vp))
		if bv.Length < 12 || bv.Length > 68 || bv.Value == nil {
			return nil, errors.New("LDAP returned an invalid SID length or pointer")
		}
		value := unsafe.Slice(bv.Value, int(bv.Length))
		if !validSID(value) {
			return nil, errors.New("LDAP returned a malformed SID")
		}
		out = append(out, append([]byte(nil), value...))
	}
	return nil, fmt.Errorf("%w: LDAP binary attribute has too many values", ErrIncomplete)
}

func (c *windowsClient) fetchBinaryRangeLocked(ctx context.Context, dn, attr string, start int) (string, attributeDescription, [][]byte, error) {
	requestAttr := fmt.Sprintf("%s;range=%d-*", attr, start)
	basep, err := syscall.UTF16PtrFromString(dn)
	if err != nil {
		return "", attributeDescription{}, nil, err
	}
	filterp, _ := syscall.UTF16PtrFromString("(objectClass=*)")
	attrp, _ := syscall.UTF16PtrFromString(requestAttr)
	attrs := []*uint16{attrp, nil}
	tm, err := c.searchTimeout(ctx)
	if err != nil {
		return "", attributeDescription{}, nil, err
	}
	var msg uintptr
	code, _, callErr := ldapSearch.Call(c.conn, uintptr(unsafe.Pointer(basep)), ldapScopeBase, uintptr(unsafe.Pointer(filterp)), uintptr(unsafe.Pointer(&attrs[0])), 0, 0, 0, uintptr(unsafe.Pointer(&tm)), 1, uintptr(unsafe.Pointer(&msg)))
	if msg != 0 {
		defer ldapMsgFree.Call(msg)
	}
	if err := ctx.Err(); err != nil {
		return "", attributeDescription{}, nil, err
	}
	if code != ldapSuccess {
		if code == ldapNoSuchObject {
			return "", attributeDescription{}, nil, fmt.Errorf("%w: object disappeared during range retrieval", ErrIncomplete)
		}
		return "", attributeDescription{}, nil, fmt.Errorf("LDAP binary range search failed: %d (%v)", code, callErr)
	}
	if err := ctx.Err(); err != nil {
		return "", attributeDescription{}, nil, err
	}
	entry, _, _ := ldapFirstEntry.Call(c.conn, msg)
	if entry == 0 {
		return "", attributeDescription{}, nil, fmt.Errorf("%w: AD omitted the requested binary range", ErrIncomplete)
	}
	dnptr, _, _ := ldapGetDN.Call(c.conn, entry)
	if dnptr == 0 {
		return "", attributeDescription{}, nil, fmt.Errorf("%w: binary range response has no distinguished name", ErrIncomplete)
	}
	pageDN, dnErr := utf16FromPtr((*uint16)(nativePointer(dnptr)))
	ldapMemFree.Call(dnptr)
	if dnErr != nil {
		return "", attributeDescription{}, nil, dnErr
	}
	attributes, err := c.responseAttributes(entry)
	if err != nil {
		return "", attributeDescription{}, nil, err
	}
	d, ok := attributes[lowerASCII(attr)]
	if !ok || !d.ranged {
		return "", attributeDescription{}, nil, fmt.Errorf("%w: AD omitted or misdescribed the requested binary range", ErrIncomplete)
	}
	if d.placeholder != "" && !strings.EqualFold(d.placeholder, requestAttr) {
		return "", attributeDescription{}, nil, fmt.Errorf("%w: AD returned an unexpected empty binary range placeholder", ErrIncomplete)
	}
	values, err := c.readBinaryValues(entry, d.descriptor)
	return pageDN, d, values, err
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
