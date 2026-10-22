package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/zerotrustgarage/tools/copy/internal/cmdline"
)

var kernel = windows.NewLazySystemDLL("kernel32.dll")

func systemEngine() (string, error) {
	buf := make([]uint16, 32768)
	n, _, callErr := kernel.NewProc("GetSystemDirectoryW").Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || n >= uintptr(len(buf)) {
		return "", fmt.Errorf("GetSystemDirectoryW: %w", callErr)
	}
	return filepath.Join(windows.UTF16ToString(buf[:n]), "Robocopy.exe"), nil
}

func launch(ctx context.Context, executable string, args []string, stdout, stderr string) (result Result, err error) {
	result.Arguments = append([]string(nil), args...)
	result.Started = time.Now().UTC()
	defer func() { result.ElapsedMS = time.Since(result.Started).Milliseconds() }()
	if err := ctx.Err(); err != nil {
		result.Canceled = true
		return result, err
	}
	line, err := cmdline.Render(append([]string{executable}, args...))
	if err != nil {
		return result, err
	}
	app, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return result, err
	}
	command, err := windows.UTF16PtrFromString(line)
	if err != nil {
		return result, err
	}
	out, err := os.OpenFile(stdout, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	defer out.Close()
	errFile, err := os.OpenFile(stderr, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	defer errFile.Close()
	in, err := os.Open(os.DevNull)
	if err != nil {
		return result, err
	}
	defer in.Close()
	handles := []windows.Handle{windows.Handle(in.Fd()), windows.Handle(out.Fd()), windows.Handle(errFile.Fd())}
	for _, h := range handles {
		if err := windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return result, err
		}
		defer windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, 0)
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return result, err
	}
	defer attrs.Delete()
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return result, err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return result, err
	}
	defer func() {
		if job != 0 {
			windows.CloseHandle(job)
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return result, err
	}
	si := windows.StartupInfoEx{}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput, si.StdOutput, si.StdErr = handles[0], handles[1], handles[2]
	si.ProcThreadAttributeList = attrs.List()
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(app, command, nil, nil, true, windows.CREATE_SUSPENDED|windows.CREATE_NO_WINDOW|windows.EXTENDED_STARTUPINFO_PRESENT, nil, nil, &si.StartupInfo, &pi); err != nil {
		return result, err
	}
	runtime.KeepAlive(handles)
	defer windows.CloseHandle(pi.Process)
	defer windows.CloseHandle(pi.Thread)
	// This also covers failures while the process is still suspended.
	defer windows.TerminateProcess(pi.Process, 1)
	if err := windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		return result, fmt.Errorf("contain Robocopy before resume: %w", err)
	}
	var member int32
	ok, _, callErr := kernel.NewProc("IsProcessInJob").Call(uintptr(pi.Process), uintptr(job), uintptr(unsafe.Pointer(&member)))
	if ok == 0 || member == 0 {
		return result, fmt.Errorf("verify Robocopy job membership: %v", callErr)
	}
	result.InJob = true
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		return result, err
	}
	for {
		state, waitErr := windows.WaitForSingleObject(pi.Process, 100)
		if waitErr != nil {
			return result, waitErr
		}
		if ctx.Err() != nil {
			result.Canceled = true
			// Closing the non-inherited job handle kills every contained process.
			if job != 0 {
				windows.CloseHandle(job)
				job = 0
			}
			if _, err := windows.WaitForSingleObject(pi.Process, 5000); err != nil {
				return result, err
			}
			_ = windows.GetExitCodeProcess(pi.Process, &result.NativeExit)
			return result, ctx.Err()
		}
		if state == windows.WAIT_OBJECT_0 {
			if err := windows.GetExitCodeProcess(pi.Process, &result.NativeExit); err != nil {
				return result, err
			}
			return result, nil
		}
		if state != uint32(windows.WAIT_TIMEOUT) {
			return result, errors.New("unexpected Robocopy wait status")
		}
	}
}
