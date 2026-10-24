package engine

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wintrust                          = windows.NewLazySystemDLL("wintrust.dll")
	procCatAdminAcquireContext2       = wintrust.NewProc("CryptCATAdminAcquireContext2")
	procCatAdminReleaseContext        = wintrust.NewProc("CryptCATAdminReleaseContext")
	procCatAdminCalcHash2             = wintrust.NewProc("CryptCATAdminCalcHashFromFileHandle2")
	procCatAdminEnumCatalogFromHash   = wintrust.NewProc("CryptCATAdminEnumCatalogFromHash")
	procCatAdminReleaseCatalogContext = wintrust.NewProc("CryptCATAdminReleaseCatalogContext")
	procCatCatalogInfoFromContext     = wintrust.NewProc("CryptCATCatalogInfoFromContext")
)

type winSys struct{}

func newSys() Sys { return winSys{} }

func (winSys) FileVersion(path string) (string, error) {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil {
		if errors.Is(err, windows.ERROR_RESOURCE_DATA_NOT_FOUND) || errors.Is(err, windows.ERROR_RESOURCE_TYPE_NOT_FOUND) {
			return "", nil
		}
		return "", fmt.Errorf("GetFileVersionInfoSize: %w", err)
	}
	buf := make([]byte, size)
	if err := windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buf[0])); err != nil {
		return "", fmt.Errorf("GetFileVersionInfo: %w", err)
	}
	var info *windows.VS_FIXEDFILEINFO
	var n uint32
	if err := windows.VerQueryValue(unsafe.Pointer(&buf[0]), `\`, unsafe.Pointer(&info), &n); err != nil {
		return "", fmt.Errorf("VerQueryValue: %w", err)
	}
	if info == nil || n < uint32(unsafe.Sizeof(*info)) || info.Signature != 0xFEEF04BD {
		return "", errors.New("VerQueryValue returned no fixed file info")
	}
	return fmt.Sprintf("%d.%d.%d.%d", info.FileVersionMS>>16, info.FileVersionMS&0xFFFF, info.FileVersionLS>>16, info.FileVersionLS&0xFFFF), nil
}

func tokenInfo(class uint32, size int) ([]byte, error) {
	token := windows.GetCurrentProcessToken()
	for {
		buf := make([]byte, size)
		var need uint32
		err := windows.GetTokenInformation(token, class, &buf[0], uint32(len(buf)), &need)
		if err == nil {
			return buf, nil
		}
		if errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) && int(need) > size {
			size = int(need)
			continue
		}
		return nil, err
	}
}

func (winSys) Elevation() (ElevationInfo, error) {
	var info ElevationInfo
	typ, err := tokenInfo(windows.TokenElevationType, 4)
	if err != nil {
		return info, fmt.Errorf("TokenElevationType: %w", err)
	}
	info.Type = *(*uint32)(unsafe.Pointer(&typ[0]))
	el, err := tokenInfo(windows.TokenElevation, 4)
	if err != nil {
		return info, fmt.Errorf("TokenElevation: %w", err)
	}
	info.Elevated = *(*uint32)(unsafe.Pointer(&el[0])) != 0
	return info, nil
}

func (winSys) Privileges() (map[string]bool, error) {
	buf, err := tokenInfo(windows.TokenPrivileges, 1024)
	if err != nil {
		return nil, fmt.Errorf("TokenPrivileges: %w", err)
	}
	tp := (*windows.Tokenprivileges)(unsafe.Pointer(&buf[0]))
	held := unsafe.Slice(&tp.Privileges[0], tp.PrivilegeCount)
	out := map[string]bool{}
	for _, name := range RobocopyPrivileges {
		var luid windows.LUID
		if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &luid); err != nil {
			return nil, fmt.Errorf("LookupPrivilegeValue %s: %w", name, err)
		}
		for _, p := range held {
			if p.Luid == luid {
				out[name] = p.Attributes&windows.SE_PRIVILEGE_ENABLED != 0
			}
		}
	}
	return out, nil
}

func (winSys) EmbeddedTrust(path string) (uint32, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	file := windows.WinTrustFileInfo{FilePath: p}
	file.Size = uint32(unsafe.Sizeof(file))
	return verify(windows.WTD_CHOICE_FILE, unsafe.Pointer(&file))
}

// verify runs WinVerifyTrust without UI or network. A trust failure is a
// status, not an error: only a failure to run the check is an error.
func verify(choice uint32, data unsafe.Pointer) (uint32, error) {
	wd := windows.WinTrustData{
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     choice,
		FileOrCatalogOrBlobOrSgnrOrCert: data,
		StateAction:                     windows.WTD_STATEACTION_IGNORE,
		ProvFlags:                       windows.WTD_REVOCATION_CHECK_NONE | windows.WTD_CACHE_ONLY_URL_RETRIEVAL,
	}
	wd.Size = uint32(unsafe.Sizeof(wd))
	err := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &wd)
	if err == nil {
		return 0, nil
	}
	var errno windows.Errno
	if errors.As(err, &errno) {
		return uint32(errno), nil
	}
	return 0, err
}

type catalogInfo struct {
	Size    uint32
	Catalog [260]uint16
}

type wintrustCatalogInfo struct {
	Size           uint32
	Version        uint32
	CatalogFile    *uint16
	MemberTag      *uint16
	MemberFile     *uint16
	MemberHandle   windows.Handle
	Hash           *byte
	HashSize       uint32
	CatalogContext uintptr
	CatAdmin       windows.Handle
}

func (winSys) CatalogTrust(path string) (CatalogResult, error) {
	var res CatalogResult
	sha256 := windows.StringToUTF16Ptr("SHA256")
	var admin windows.Handle
	if r, _, err := procCatAdminAcquireContext2.Call(uintptr(unsafe.Pointer(&admin)), 0, uintptr(unsafe.Pointer(sha256)), 0, 0); r == 0 {
		return res, fmt.Errorf("CryptCATAdminAcquireContext2: %w", err)
	}
	defer procCatAdminReleaseContext.Call(uintptr(admin), 0)

	file, err := os.Open(path)
	if err != nil {
		return res, err
	}
	defer file.Close()
	hash := make([]byte, 64)
	size := uint32(len(hash))
	if r, _, err := procCatAdminCalcHash2.Call(uintptr(admin), file.Fd(), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&hash[0])), 0); r == 0 {
		return res, fmt.Errorf("CryptCATAdminCalcHashFromFileHandle2: %w", err)
	}
	hash = hash[:size]
	cat, _, enumErr := procCatAdminEnumCatalogFromHash.Call(uintptr(admin), uintptr(unsafe.Pointer(&hash[0])), uintptr(size), 0, 0)
	if cat == 0 {
		// ERROR_NOT_FOUND (or no last error) means no catalog lists this hash.
		if en, ok := enumErr.(windows.Errno); ok && en != 0 && en != windows.ERROR_NOT_FOUND {
			return res, fmt.Errorf("CryptCATAdminEnumCatalogFromHash: %w", enumErr)
		}
		return res, nil
	}
	defer procCatAdminReleaseCatalogContext.Call(uintptr(admin), cat, 0)
	var ci catalogInfo
	ci.Size = uint32(unsafe.Sizeof(ci))
	if r, _, err := procCatCatalogInfoFromContext.Call(cat, uintptr(unsafe.Pointer(&ci)), 0); r == 0 {
		return res, fmt.Errorf("CryptCATCatalogInfoFromContext: %w", err)
	}
	res.Found = true
	res.Catalog = windows.UTF16ToString(ci.Catalog[:])
	member, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return res, err
	}
	tag, err := windows.UTF16PtrFromString(strings.ToUpper(hex.EncodeToString(hash)))
	if err != nil {
		return res, err
	}
	catFile := windows.StringToUTF16Ptr(res.Catalog)
	wci := wintrustCatalogInfo{Version: 0, CatalogFile: catFile, MemberTag: tag, MemberFile: member, MemberHandle: windows.Handle(file.Fd()), Hash: &hash[0], HashSize: size, CatAdmin: admin}
	wci.Size = uint32(unsafe.Sizeof(wci))
	status, err := verify(windows.WTD_CHOICE_CATALOG, unsafe.Pointer(&wci))
	if err != nil {
		return res, err
	}
	res.Status = status
	return res, nil
}
