//go:build windows

package ad

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// SSPI values from sspi.h, documented with SEC_WINNT_AUTH_IDENTITY_EXW and
// QueryContextAttributes (Negotiate).
const (
	secWinntAuthIdentityUnicode = 0x2
	secWinntAuthIdentityVersion = 0x200
	secpkgAttrNegotiationInfo   = 12
	nameSamCompatible           = 2
	errorMoreData               = 234
)

// secWinntAuthIdentityEx mirrors SEC_WINNT_AUTH_IDENTITY_EXW. ldap_bind_sW
// documents this structure as the cred argument of LDAP_AUTH_NEGOTIATE.
// https://learn.microsoft.com/windows/win32/api/winldap/nf-winldap-ldap_bind_sw
// https://learn.microsoft.com/windows/win32/api/sspi/ns-sspi-sec_winnt_auth_identity_exw
type secWinntAuthIdentityEx struct {
	Version           uint32
	Length            uint32
	User              *uint16
	UserLength        uint32
	Domain            *uint16
	DomainLength      uint32
	Password          *uint16
	PasswordLength    uint32
	Flags             uint32
	PackageList       *uint16
	PackageListLength uint32
}

// secHandle mirrors SecHandle / CtxtHandle (two ULONG_PTR values).
type secHandle struct {
	Lower uintptr
	Upper uintptr
}

// secPkgContextNegotiationInfo mirrors SecPkgContext_NegotiationInfoW. The
// package information is allocated by SSPI and freed with FreeContextBuffer.
type secPkgContextNegotiationInfo struct {
	PackageInfo      uintptr
	NegotiationState uint32
}

// secPkgInfo mirrors SecPkgInfoW.
type secPkgInfo struct {
	Capabilities uint32
	Version      uint16
	RPCID        uint16
	MaxToken     uint32
	Name         *uint16
	Comment      *uint16
}

var (
	secur32                                   *syscall.LazyDLL
	queryContextAttributes, freeContextBuffer *syscall.LazyProc
	getUserNameEx                             *syscall.LazyProc
	secur32Once                               sync.Once
	secur32Err                                error
	errKerberosUnverified                     = errors.New("Kerberos-only sign-in: the Negotiate package used for the bind could not be confirmed, so the connection was closed")
)

func loadSecur32() error {
	secur32Once.Do(func() {
		dir, err := systemDirectory()
		if err != nil {
			secur32Err = err
			return
		}
		secur32 = syscall.NewLazyDLL(dir + `\secur32.dll`)
		queryContextAttributes = secur32.NewProc("QueryContextAttributesW")
		freeContextBuffer = secur32.NewProc("FreeContextBuffer")
		getUserNameEx = secur32.NewProc("GetUserNameExW")
		secur32Err = secur32.Load()
	})
	return secur32Err
}

// bind performs the Negotiate bind: as the current Windows identity when cred
// is nil, otherwise with an explicit SEC_WINNT_AUTH_IDENTITY_EXW (design 6.5,
// step 3). The UTF-16 password copy is erased before bind returns.
func (c *windowsClient) bind(cred *Credential, kerberosOnly bool) error {
	if cred == nil {
		code, _, _ := ldapBind.Call(c.conn, 0, 0, ldapAuthNegotiate)
		if code != ldapSuccess {
			return ldapResultError("Negotiate LDAP bind", uint32(code), c.serverError())
		}
		return nil
	}
	user, err := syscall.UTF16FromString(cred.User)
	if err != nil {
		return errors.New("the sign-in user name contains a NUL character")
	}
	var domain []uint16
	if cred.Domain != "" {
		if domain, err = syscall.UTF16FromString(cred.Domain); err != nil {
			return errors.New("the sign-in domain contains a NUL character")
		}
	}
	password, err := utf16Secret(cred.Password)
	if err != nil {
		return fmt.Errorf("sign-in: %w", err)
	}
	defer wipeUint16(password)
	identity := &secWinntAuthIdentityEx{
		Version:        secWinntAuthIdentityVersion,
		Length:         uint32(unsafe.Sizeof(secWinntAuthIdentityEx{})),
		Flags:          secWinntAuthIdentityUnicode,
		User:           &user[0],
		UserLength:     uint32(len(user) - 1),
		Password:       &password[0],
		PasswordLength: uint32(len(password) - 1),
	}
	if len(domain) > 0 {
		identity.Domain = &domain[0]
		identity.DomainLength = uint32(len(domain) - 1)
	}
	var packages []uint16
	if kerberosOnly {
		// SEC_WINNT_AUTH_IDENTITY_EXW documents "!ntlm" as the package list
		// that keeps Negotiate from using NTLM. requireKerberos then checks
		// the package actually used, so the bind fails closed either way.
		packages, _ = syscall.UTF16FromString("!ntlm")
		identity.PackageList = &packages[0]
		identity.PackageListLength = uint32(len(packages) - 1)
	}
	code, _, _ := ldapBind.Call(c.conn, 0, uintptr(unsafe.Pointer(identity)), ldapAuthNegotiate)
	identity.Password, identity.PasswordLength = nil, 0
	runtime.KeepAlive(user)
	runtime.KeepAlive(domain)
	runtime.KeepAlive(password)
	runtime.KeepAlive(packages)
	if code != ldapSuccess {
		return ldapResultError("sign-in as "+cred.Account(), uint32(code), c.serverError())
	}
	return nil
}

// requireKerberos confirms that Negotiate selected Kerberos for this
// connection: it reads the connection's security context
// (LDAP_OPT_SECURITY_CONTEXT) and asks SSPI which package it uses
// (SECPKG_ATTR_NEGOTIATION_INFO). Anything else, including a failure to find
// out, closes the connection (runbook R121, IA.L2-3.5.4). Over LDAPS the
// context may be the TLS channel's; SSPI then names no Kerberos package and
// the bind fails closed in the same way.
func (c *windowsClient) requireKerberos() error {
	if err := loadSecur32(); err != nil {
		return fmt.Errorf("%w: %v", errKerberosUnverified, err)
	}
	var handle secHandle
	if r, _, _ := ldapGetOption.Call(c.conn, ldapOptSecurityContext, uintptr(unsafe.Pointer(&handle))); r != ldapSuccess {
		return fmt.Errorf("%w: LDAP_OPT_SECURITY_CONTEXT returned %d", errKerberosUnverified, r)
	}
	if handle.Lower == 0 && handle.Upper == 0 {
		return fmt.Errorf("%w: the connection has no security context", errKerberosUnverified)
	}
	var info secPkgContextNegotiationInfo
	status, _, _ := queryContextAttributes.Call(uintptr(unsafe.Pointer(&handle)), secpkgAttrNegotiationInfo, uintptr(unsafe.Pointer(&info)))
	if status != 0 {
		return fmt.Errorf("%w: QueryContextAttributesW returned 0x%08x", errKerberosUnverified, uint32(status))
	}
	if info.PackageInfo == 0 {
		return fmt.Errorf("%w: SSPI returned no package information", errKerberosUnverified)
	}
	defer freeContextBuffer.Call(info.PackageInfo)
	pkg := (*secPkgInfo)(nativePointer(info.PackageInfo))
	name, err := utf16FromPtr(pkg.Name)
	if err != nil {
		return fmt.Errorf("%w: %v", errKerberosUnverified, err)
	}
	if !kerberosPackage(name) {
		return fmt.Errorf("Kerberos-only sign-in: the domain controller authenticated the bind with %q instead of Kerberos, so the connection was closed", name)
	}
	return nil
}

// flagOn reads an LDAP_OPT_ON / LDAP_OPT_OFF session option. Any failure
// reads as off.
func (c *windowsClient) flagOn(option uintptr) bool {
	var value uintptr
	r, _, _ := ldapGetOption.Call(c.conn, option, uintptr(unsafe.Pointer(&value)))
	return r == ldapSuccess && value != 0
}

// hostName is LDAP_OPT_HOST_NAME, the server the session is connected to.
// WinLDAP owns the string; it is not freed here.
func (c *windowsClient) hostName() string {
	var p uintptr
	if r, _, _ := ldapGetOption.Call(c.conn, ldapOptHostName, uintptr(unsafe.Pointer(&p))); r != ldapSuccess || p == 0 {
		return ""
	}
	name, err := utf16FromPtr((*uint16)(nativePointer(p)))
	if err != nil {
		return ""
	}
	return name
}

// serverError is LDAP_OPT_SERVER_ERROR, AD's diagnostic text for the last
// operation on this session. Callers hold mu (or own the session alone).
func (c *windowsClient) serverError() string {
	var p uintptr
	if r, _, _ := ldapGetOption.Call(c.conn, ldapOptServerError, uintptr(unsafe.Pointer(&p))); r != ldapSuccess || p == 0 {
		return ""
	}
	defer ldapMemFree.Call(p)
	text, err := utf16FromPtr((*uint16)(nativePointer(p)))
	if err != nil {
		return ""
	}
	return text
}

// currentUserName names the current Windows identity as DOMAIN\user
// (GetUserNameExW, NameSamCompatible).
func currentUserName() string {
	if loadSecur32() != nil {
		return ""
	}
	size := uint32(256)
	for attempt := 0; attempt < 2; attempt++ {
		buf := make([]uint16, size)
		n := size
		r, _, e := getUserNameEx.Call(nameSamCompatible, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
		if r&0xff != 0 {
			if n > uint32(len(buf)) {
				return ""
			}
			return syscall.UTF16ToString(buf[:n])
		}
		if errno, ok := e.(syscall.Errno); !ok || errno != errorMoreData || n <= size || n > 32768 {
			return ""
		}
		size = n
	}
	return ""
}

// defaultNamingContext reads the domain's naming context from the rootDSE
// and remembers it.
func (c *windowsClient) defaultNamingContext(ctx context.Context) (string, error) {
	c.mu.Lock()
	nc := c.namingContext
	c.mu.Unlock()
	if nc != "" {
		return nc, nil
	}
	res, err := c.searchWith(ctx, searchSpec{scope: ldapScopeBase, filter: "(objectClass=*)", limit: 1,
		attrs: []string{"defaultNamingContext"}, rootDSE: true})
	if err != nil {
		return "", err
	}
	if len(res.Objects) == 0 {
		return "", errors.New("the rootDSE returned no entry")
	}
	nc = strings.TrimSpace(res.Objects[0].First("defaultNamingContext"))
	if nc == "" {
		return "", errors.New("the rootDSE has no defaultNamingContext")
	}
	c.mu.Lock()
	c.namingContext = nc
	c.mu.Unlock()
	return nc, nil
}
