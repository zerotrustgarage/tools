package guard

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/zerotrustgarage/tools/copy/internal/job"
)

func canonical(name string) (string, error) {
	final, _, err := identity(name)
	return final, err
}

func identity(name string) (string, string, error) {
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", "", err
	}
	h, err := windows.CreateFile(ptr, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", "", err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return "", "", err
	}
	if n == 0 || n >= uint32(len(buf)) {
		return "", "", errors.New("canonical path exceeds supported length")
	}
	final := windows.UTF16ToString(buf[:n])
	if !strings.HasPrefix(final, `\\?\`) || strings.HasPrefix(final, `\\?\UNC\`) {
		return "", "", errors.New("initial runner requires local fixed-drive endpoints")
	}
	// Identity spelling only. Invocation retains the original validated path.
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return "", "", err
	}
	id := fmt.Sprintf("%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
	return strings.TrimPrefix(final, `\\?\`), id, nil
}

func recordLocation(name string) (string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	current := absolute
	var suffix []string
	for {
		_, err := os.Stat(current)
		if err == nil {
			resolved, err := canonical(current)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("cannot resolve record location")
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func inspect(ctx context.Context, j job.Job, records string) (facts Facts, err error) {
	for i, endpoint := range []string{j.Source, j.Destination} {
		info, err := os.Stat(endpoint)
		if err != nil || !info.IsDir() {
			return facts, fmt.Errorf("endpoint %q must be an existing accessible directory", endpoint)
		}
		final, id, err := identity(endpoint)
		if err != nil {
			return facts, err
		}
		root := filepath.VolumeName(final) + "\\"
		p, err := windows.UTF16PtrFromString(root)
		if err != nil || windows.GetDriveType(p) != windows.DRIVE_FIXED {
			return facts, errors.New("initial runner requires local fixed drives")
		}
		if strings.EqualFold(strings.TrimRight(final, "\\"), strings.TrimRight(root, "\\")) {
			return facts, errors.New("volume roots are refused")
		}
		if i == 0 {
			facts.SourceCanonical, facts.SourceID = final, id
		} else {
			facts.DestinationCanonical, facts.DestinationID = final, id
		}
	}
	if facts.SourceID == facts.DestinationID || Overlap(facts.SourceCanonical, facts.DestinationCanonical) {
		return facts, errors.New("source and destination overlap after resolving aliases")
	}
	recordPath, err := recordLocation(records)
	if err != nil {
		return facts, err
	}
	if Overlap(recordPath, facts.SourceCanonical) || Overlap(recordPath, facts.DestinationCanonical) {
		return facts, errors.New("records must be outside both copy trees; select another --report directory")
	}
	for _, endpoint := range []string{j.Source, j.Destination} {
		err := filepath.WalkDir(endpoint, func(name string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			ptr, err := windows.UTF16PtrFromString(name)
			if err != nil {
				return err
			}
			attrs, err := windows.GetFileAttributes(ptr)
			if err != nil {
				return err
			}
			facts.EntriesChecked++
			if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
				return fmt.Errorf("initial runner refuses reparse points: %q", name)
			}
			return nil
		})
		if err != nil {
			return facts, err
		}
	}
	dest, err := windows.UTF16PtrFromString(j.Destination)
	if err != nil {
		return facts, err
	}
	if err := windows.GetDiskFreeSpaceEx(dest, &facts.FreeBytes, nil, nil); err != nil {
		return facts, err
	}
	return facts, nil
}
