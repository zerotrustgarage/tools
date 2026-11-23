//go:build windows

package auth

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

// Console input modes (SetConsoleMode).
const (
	enableProcessedInput = 0x0001
	enableLineInput      = 0x0002
	enableEchoInput      = 0x0004
)

// CredUI and CredUnPack values (wincred.h).
const (
	creduiwinAuthPackageOnly     = 0x10
	credPackProtectedCredentials = 0x1
	errorCancelled               = 1223
	errorInsufficientBuffer      = 122
)

// ReadPassword writes prompt to out and reads one line from the console in
// with echo turned off (GetConsoleMode and SetConsoleMode). The original mode
// is restored afterwards, also on error. in must be an interactive console;
// redirected input is refused with ErrNotConsole. The caller wipes the
// result.
func ReadPassword(prompt string, in *os.File, out io.Writer) ([]byte, error) {
	if in == nil {
		return nil, ErrNotConsole
	}
	a, err := loadAPI()
	if err != nil {
		return nil, err
	}
	if err := find(a.getConsoleMode, a.setConsoleMode); err != nil {
		return nil, err
	}
	h := in.Fd()
	var mode uint32
	if r, _, _ := a.getConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode))); r == 0 {
		return nil, ErrNotConsole
	}
	quiet := (mode &^ enableEchoInput) | enableLineInput | enableProcessedInput
	if r, _, callErr := a.setConsoleMode.Call(h, uintptr(quiet)); r == 0 {
		return nil, fmt.Errorf("turn off console echo: %v", callErr)
	}
	defer a.setConsoleMode.Call(h, uintptr(mode))
	if out != nil && prompt != "" {
		fmt.Fprint(out, prompt)
	}
	pw, err := readSecretLine(in)
	if out != nil {
		fmt.Fprintln(out)
	}
	return pw, err
}

// creduiInfo is CREDUI_INFOW.
type creduiInfo struct {
	size    uint32
	parent  uintptr
	message *uint16
	caption *uint16
	banner  uintptr
}

// lsaString is LSA_STRING.
type lsaString struct {
	length    uint16
	maxLength uint16
	buffer    *byte
}

// PromptWindowsCredential shows the Windows credential prompt
// (CredUIPromptForWindowsCredentialsW) limited to credential providers that
// serialize for the Negotiate package (CREDUIWIN_AUTHPACKAGE_ONLY), with no
// Save check box and no current user prefilled, and unpacks the result with
// CredUnPackAuthenticationBufferW. DOMAIN\user and UPN forms are split as by
// ParseAccount. A certificate (smart card) credential returns
// ErrSmartCardNotSupportedYet; cancelling returns ErrCancelled. The packed
// buffer is zeroed and freed with CoTaskMemFree. The caller owns and wipes
// the credential.
func PromptWindowsCredential(caption, message string) (*ad.Credential, error) {
	a, err := loadAPI()
	if err != nil {
		return nil, err
	}
	if err := find(a.credUIPrompt, a.credUnPack, a.coTaskMemFree, a.credIsMarshaled,
		a.lsaConnectUntrusted, a.lsaLookupPackage, a.lsaClose); err != nil {
		return nil, err
	}
	pkg, err := negotiatePackage(a)
	if err != nil {
		return nil, err
	}
	captionp, err := syscall.UTF16PtrFromString(caption)
	if err != nil {
		return nil, err
	}
	messagep, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return nil, err
	}
	info := creduiInfo{message: messagep, caption: captionp}
	info.size = uint32(unsafe.Sizeof(info))
	var (
		outBuf  unsafe.Pointer
		outSize uint32
		save    int32
	)
	r, _, _ := a.credUIPrompt.Call(uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&pkg)), 0, 0,
		uintptr(unsafe.Pointer(&outBuf)), uintptr(unsafe.Pointer(&outSize)), uintptr(unsafe.Pointer(&save)),
		creduiwinAuthPackageOnly)
	switch r {
	case 0:
	case errorCancelled:
		return nil, ErrCancelled
	default:
		return nil, fmt.Errorf("CredUIPromptForWindowsCredentialsW failed: %w", syscall.Errno(r))
	}
	if outBuf == nil {
		return nil, errors.New("the credential prompt returned no credential")
	}
	defer func() {
		clear(unsafe.Slice((*byte)(outBuf), outSize))
		a.coTaskMemFree.Call(uintptr(outBuf))
	}()
	return unpackCredential(a, outBuf, outSize)
}

func unpackCredential(a *winAPI, buf unsafe.Pointer, size uint32) (*ad.Credential, error) {
	userLen, domainLen, pwLen := uint32(514), uint32(256), uint32(256)
	for attempt := 0; ; attempt++ {
		user := make([]uint16, max(userLen, 1))
		domain := make([]uint16, max(domainLen, 1))
		pw := make([]uint16, max(pwLen, 1))
		userLen, domainLen, pwLen = uint32(len(user)), uint32(len(domain)), uint32(len(pw))
		r, _, callErr := a.credUnPack.Call(credPackProtectedCredentials, uintptr(buf), uintptr(size),
			uintptr(unsafe.Pointer(&user[0])), uintptr(unsafe.Pointer(&userLen)),
			uintptr(unsafe.Pointer(&domain[0])), uintptr(unsafe.Pointer(&domainLen)),
			uintptr(unsafe.Pointer(&pw[0])), uintptr(unsafe.Pointer(&pwLen)))
		if r == 0 {
			clear(pw)
			if errors.Is(callErr, syscall.Errno(errorInsufficientBuffer)) && attempt < 2 {
				continue
			}
			return nil, fmt.Errorf("CredUnPackAuthenticationBufferW failed: %v", callErr)
		}
		userName := syscall.UTF16ToString(user)
		marshaled, _, _ := a.credIsMarshaled.Call(uintptr(unsafe.Pointer(&user[0])))
		if marshaled != 0 || strings.HasPrefix(userName, "@@") {
			clear(pw)
			return nil, ErrSmartCardNotSupportedYet
		}
		password := utf16ToUTF8(pw)
		clear(pw)
		if userName == "" || len(password) == 0 {
			wipe(password)
			return nil, errors.New("the credential prompt returned no user name or password")
		}
		cred := &ad.Credential{User: userName, Domain: syscall.UTF16ToString(domain), Password: password}
		if cred.Domain == "" {
			cred.User, cred.Domain = ParseAccount(cred.User)
		}
		return cred, nil
	}
}

// negotiatePackage returns the Negotiate authentication package ID
// (LsaConnectUntrusted, LsaLookupAuthenticationPackage), as the documentation
// of CredUIPromptForWindowsCredentialsW directs.
func negotiatePackage(a *winAPI) (uint32, error) {
	var lsa syscall.Handle
	if st, _, _ := a.lsaConnectUntrusted.Call(uintptr(unsafe.Pointer(&lsa))); uint32(st) != 0 {
		return 0, fmt.Errorf("LsaConnectUntrusted failed: NTSTATUS 0x%08x", uint32(st))
	}
	defer a.lsaClose.Call(uintptr(lsa))
	name := []byte("Negotiate")
	s := lsaString{length: uint16(len(name)), maxLength: uint16(len(name)), buffer: &name[0]}
	var pkg uint32
	if st, _, _ := a.lsaLookupPackage.Call(uintptr(lsa), uintptr(unsafe.Pointer(&s)), uintptr(unsafe.Pointer(&pkg))); uint32(st) != 0 {
		return 0, fmt.Errorf("LsaLookupAuthenticationPackage(Negotiate) failed: NTSTATUS 0x%08x", uint32(st))
	}
	return pkg, nil
}
