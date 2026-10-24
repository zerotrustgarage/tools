package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrUnsupported is returned by probe sources that cannot run on this platform.
var ErrUnsupported = errors.New("requires Windows")

// Signature decisions. "unknown" means the check could not run or could not
// reach a clear answer; it never means "probably fine".
const (
	SignatureEmbedded = "embedded"
	SignatureCatalog  = "catalog"
	SignatureUnsigned = "unsigned"
	SignatureUnknown  = "unknown"
)

// Elevation types, from the token elevation type of the current process.
const (
	ElevationDefault = "default" // no split token: UAC off, or a plain standard user
	ElevationFull    = "full"
	ElevationLimited = "limited"
	ElevationUnknown = "unknown"
)

// Privilege states for a token. Absent privileges cannot be enabled by the
// process; present-but-disabled ones can be enabled by the owner of the token.
const (
	PrivilegeAbsent   = "absent"
	PrivilegeDisabled = "present_disabled"
	PrivilegeEnabled  = "enabled"
)

// Windows HRESULTs returned by WinVerifyTrust that mean "no embedded signature".
const (
	trustProviderUnknown = 0x800B0001
	trustSubjectFormUnk  = 0x800B0003
	trustNoSignature     = 0x800B0100
)

// RobocopyPrivileges are the privileges that change what Robocopy can do
// (/B, /ZB, /COPY:S and /COPY:O, /SEC).
var RobocopyPrivileges = []string{"SeBackupPrivilege", "SeRestorePrivilege", "SeSecurityPrivilege"}

// Metadata is the probe result stored in the run record. Every field says how
// it was obtained (Source) or why it is not available (Unavailable). Absent
// fields in older records load as zero values.
type Metadata struct {
	FileVersion FileVersionFact `json:"file_version"`
	Elevation   ElevationFact   `json:"elevation"`
	Privileges  PrivilegeFact   `json:"privileges"`
	Signature   SignatureFact   `json:"signature"`
}

type FileVersionFact struct {
	Value       string `json:"value,omitempty"`
	Source      string `json:"source"`
	Unavailable string `json:"unavailable,omitempty"`
}

type ElevationFact struct {
	Type        string `json:"type"`
	Elevated    *bool  `json:"elevated,omitempty"`
	Source      string `json:"source"`
	Unavailable string `json:"unavailable,omitempty"`
}

type Privilege struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type PrivilegeFact struct {
	Items       []Privilege `json:"items,omitempty"`
	Source      string      `json:"source"`
	Unavailable string      `json:"unavailable,omitempty"`
}

type SignatureFact struct {
	Decision string `json:"decision"`
	// Detail names the catalog file or the failing trust status. It is evidence,
	// not a trust claim about the signer.
	Detail      string `json:"detail,omitempty"`
	Source      string `json:"source"`
	Unavailable string `json:"unavailable,omitempty"`
}

// ElevationInfo is the raw token answer.
type ElevationInfo struct {
	// Type is the TOKEN_ELEVATION_TYPE value: 1 default, 2 full, 3 limited.
	Type     uint32
	Elevated bool
}

// CatalogResult is the raw catalog lookup answer.
type CatalogResult struct {
	Found   bool
	Catalog string
	// Status is the WinVerifyTrust result for the catalog member, 0 for success.
	Status uint32
}

// Sys is the operating system seam. Tests use fakes; Windows uses real APIs.
type Sys interface {
	// FileVersion returns the dotted FileVersion from the PE version resource.
	FileVersion(path string) (string, error)
	Elevation() (ElevationInfo, error)
	// Privileges maps the held privilege names to whether each is enabled.
	Privileges() (map[string]bool, error)
	// EmbeddedTrust returns the WinVerifyTrust status for the file's own
	// signature: 0 on success, otherwise the HRESULT. The error is for API failures.
	EmbeddedTrust(path string) (uint32, error)
	CatalogTrust(path string) (CatalogResult, error)
}

// Inspect probes the engine file and the current process with the real system.
func Inspect(path string) Metadata { return InspectWith(path, newSys()) }

func unavailable(err error) string {
	if errors.Is(err, ErrUnsupported) {
		return "non-Windows platform: " + err.Error()
	}
	return "API error: " + err.Error()
}

// InspectWith never guesses: each failure is written into the field it affects.
func InspectWith(path string, s Sys) Metadata {
	var m Metadata

	m.FileVersion.Source = "raw RT_VERSION resource read from the file (LoadLibraryEx as data file, FindResource, VS_FIXEDFILEINFO FileVersion); not GetFileVersionInfo, which reports a shimmed 6.2"
	if v, err := s.FileVersion(path); err != nil {
		m.FileVersion.Unavailable = unavailable(err)
	} else if v == "" {
		m.FileVersion.Unavailable = "file has no version resource"
	} else {
		m.FileVersion.Value = v
	}

	m.Elevation.Source = "process token (GetTokenInformation TokenElevationType and TokenElevation)"
	m.Elevation.Type = ElevationUnknown
	if e, err := s.Elevation(); err != nil {
		m.Elevation.Unavailable = unavailable(err)
	} else {
		elevated := e.Elevated
		m.Elevation.Elevated = &elevated
		switch e.Type {
		case 1:
			m.Elevation.Type = ElevationDefault
		case 2:
			m.Elevation.Type = ElevationFull
		case 3:
			m.Elevation.Type = ElevationLimited
		default:
			m.Elevation.Unavailable = fmt.Sprintf("unrecognized token elevation type %d", e.Type)
		}
	}

	m.Privileges.Source = "process token (GetTokenInformation TokenPrivileges)"
	if held, err := s.Privileges(); err != nil {
		m.Privileges.Unavailable = unavailable(err)
	} else {
		for _, name := range RobocopyPrivileges {
			state := PrivilegeAbsent
			if enabled, ok := held[name]; ok {
				state = PrivilegeDisabled
				if enabled {
					state = PrivilegeEnabled
				}
			}
			m.Privileges.Items = append(m.Privileges.Items, Privilege{Name: name, State: state})
		}
	}

	m.Signature = signature(path, s)
	return m
}

func signature(path string, s Sys) SignatureFact {
	f := SignatureFact{
		Decision: SignatureUnknown,
		Source:   "WinVerifyTrust (WINTRUST_ACTION_GENERIC_VERIFY_V2, no UI, no revocation or network checks) on the file, then the catalog database (CryptCATAdmin) by file hash",
	}
	status, err := s.EmbeddedTrust(path)
	if err != nil {
		f.Unavailable = unavailable(err)
		return f
	}
	switch status {
	case 0:
		f.Decision = SignatureEmbedded
		return f
	case trustNoSignature, trustProviderUnknown, trustSubjectFormUnk:
		// No embedded signature: a catalog may still cover the file.
	default:
		f.Detail = fmt.Sprintf("embedded signature present but WinVerifyTrust returned 0x%08X", status)
		return f
	}
	cat, err := s.CatalogTrust(path)
	if err != nil {
		f.Unavailable = "catalog lookup: " + unavailable(err)
		return f
	}
	switch {
	case !cat.Found && status == trustNoSignature:
		f.Decision = SignatureUnsigned
		f.Detail = fmt.Sprintf("no embedded signature (0x%08X) and no catalog contains the file hash", status)
	case !cat.Found:
		f.Detail = fmt.Sprintf("unsupported subject format (WinVerifyTrust 0x%08X) and no catalog contains the file hash", status)
	case cat.Status == 0:
		f.Decision = SignatureCatalog
		f.Detail = cat.Catalog
	default:
		f.Detail = fmt.Sprintf("catalog %s found but WinVerifyTrust returned 0x%08X", cat.Catalog, cat.Status)
	}
	return f
}

// parseFixedFileInfo finds VS_FIXEDFILEINFO in a raw RT_VERSION resource and
// returns its dotted FileVersion. The structure follows the VS_VERSIONINFO
// header and the "VS_VERSION_INFO" key, 4-byte aligned.
func parseFixedFileInfo(data []byte) (string, error) {
	for i := 0; i+52 <= len(data) && i < 256; i += 4 {
		if binary.LittleEndian.Uint32(data[i:]) != 0xFEEF04BD {
			continue
		}
		ms := binary.LittleEndian.Uint32(data[i+8:])
		ls := binary.LittleEndian.Uint32(data[i+12:])
		return fmt.Sprintf("%d.%d.%d.%d", ms>>16, ms&0xFFFF, ls>>16, ls&0xFFFF), nil
	}
	return "", errors.New("version resource has no VS_FIXEDFILEINFO signature")
}
