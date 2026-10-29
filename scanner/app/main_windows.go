//go:build windows

package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	winapi "golang.org/x/sys/windows"
)

//go:embed all:frontend
var assets embed.FS

// wailsPlatform adapts the Wails runtime to Platform.
type wailsPlatform struct {
	ctx   context.Context
	toast *toastState
}

func (p wailsPlatform) Emit(event string, data any) { runtime.EventsEmit(p.ctx, event, data) }

func (p wailsPlatform) SaveFile(defaultName, filterName, pattern string) (string, error) {
	return runtime.SaveFileDialog(p.ctx, runtime.SaveDialogOptions{
		DefaultFilename: defaultName,
		Title:           "Export scan results",
		Filters:         []runtime.FileFilter{{DisplayName: filterName, Pattern: pattern}},
	})
}

func (p wailsPlatform) OpenURL(url string) { runtime.BrowserOpenURL(p.ctx, url) }

func (p wailsPlatform) OpenFile(title, filterName, pattern string) (string, error) {
	return runtime.OpenFileDialog(p.ctx, runtime.OpenDialogOptions{Title: title, Filters: []runtime.FileFilter{{DisplayName: filterName, Pattern: pattern}}})
}

func (p wailsPlatform) Launch(action, ip string) (string, error) {
	program, args, err := actionCommand(action, ip)
	if err != nil {
		return "", err
	}
	dir, err := winapi.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	if action == "ssh" {
		program = filepath.Join(dir, "OpenSSH", program)
	} else {
		program = filepath.Join(dir, program)
	}
	if action == "ssh" || action == "rdp" {
		// os/exec supplies NUL for unspecified standard handles. CreateProcess
		// without STARTF_USESTDHANDLES lets Windows attach SSH to a real console
		// with keyboard input and visible output, while still avoiding a shell.
		if err := startProcess(program, args, action == "ssh"); err != nil {
			return "", fmt.Errorf("could not launch the installed %s client: %w", action, err)
		}
		return "Opened the installed " + action + " client for " + ip, nil
	}
	ctx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%s: %w\n%s", action, err, output.String())
	}
	return output.String(), nil
}

// startProcess runs program with an exact argument vector through CreateProcess,
// never through a shell. The program is passed as the application name, so
// Windows does not search for it, and each argument is escaped as one element.
func startProcess(program string, args []string, newConsole bool) error {
	appName, err := winapi.UTF16PtrFromString(program)
	if err != nil {
		return err
	}
	argv := append([]string{program}, args...)
	for i := range argv {
		argv[i] = winapi.EscapeArg(argv[i])
	}
	commandLine, err := winapi.UTF16PtrFromString(strings.Join(argv, " "))
	if err != nil {
		return err
	}
	startup := winapi.StartupInfo{Cb: uint32(unsafe.Sizeof(winapi.StartupInfo{}))}
	var process winapi.ProcessInformation
	var flags uint32
	if newConsole {
		flags = winapi.CREATE_NEW_CONSOLE
	}
	if err := winapi.CreateProcess(appName, commandLine, nil, nil, false, flags, nil, nil, &startup, &process); err != nil {
		return err
	}
	_ = winapi.CloseHandle(process.Thread)
	_ = winapi.CloseHandle(process.Process)
	return nil
}

// LaunchProgram starts a technician-defined "Open with" program (SC10). The
// path was validated as an absolute .exe path; it must also exist as a regular
// file now. The program runs detached, in its own console if it is a console
// program, and its output is not captured.
func (p wailsPlatform) LaunchProgram(argv []string) error {
	if len(argv) == 0 {
		return errors.New("no program to start")
	}
	info, err := os.Stat(argv[0])
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("the program was not found at the saved path; check the command in Open with settings")
	}
	if err := startProcess(argv[0], argv[1:], true); err != nil {
		return fmt.Errorf("could not start the program: %w", err)
	}
	return nil
}

// toastState makes sure the Wails notification service is set up once, and only
// when a scheduled rescan first has something to announce with a Windows
// notification. Setting it up registers the app for notifications in the
// current user's registry (HKCU), so it is not done at startup.
type toastState struct {
	once sync.Once
	err  error
}

// Alert flashes the taskbar button until the window is brought forward and, if
// toast is set, sends a Windows notification through Wails. Both are best
// effort: the in-app banner is the notice of record. The text is built in
// internal/schedule from counts and addresses only.
func (p wailsPlatform) Alert(title, body string, toast bool) {
	flashMainWindow()
	if !toast || p.toast == nil {
		return
	}
	p.toast.once.Do(func() { p.toast.err = runtime.InitializeNotifications(p.ctx) })
	if p.toast.err != nil {
		return
	}
	_ = runtime.SendNotification(p.ctx, runtime.NotificationOptions{ID: "ztg-scanner-new-devices", Title: title, Body: body})
}

// systemPowerStatus is SYSTEM_POWER_STATUS (winbase.h).
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte // 1: battery saver is on
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

var (
	kernel32              = winapi.NewLazySystemDLL("kernel32.dll")
	procGetSystemPower    = kernel32.NewProc("GetSystemPowerStatus")
	user32                = winapi.NewLazySystemDLL("user32.dll")
	procFlashWindowEx     = user32.NewProc("FlashWindowEx")
	procGetClassName      = user32.NewProc("GetClassNameW")
	procEnumWindows       = user32.NewProc("EnumWindows")
	procIsWindowVisible   = user32.NewProc("IsWindowVisible")
	procGetWindowThreadID = user32.NewProc("GetWindowThreadProcessId")
)

// PowerSaving reports whether battery saver is on. Metered connections are not
// checked: that needs the WinRT networking API, which is not worth a
// dependency for this.
func (wailsPlatform) PowerSaving() bool {
	var st systemPowerStatus
	if r, _, _ := procGetSystemPower.Call(uintptr(unsafe.Pointer(&st))); r == 0 {
		return false
	}
	return st.SystemStatusFlag&1 != 0
}

type flashInfo struct {
	Size    uint32
	Hwnd    uintptr
	Flags   uint32
	Count   uint32
	Timeout uint32
}

const (
	flashwTray       = 0x2
	flashwTimerNoFg  = 0xC
	wailsWindowClass = "wailsWindow"
)

// flashMainWindow flashes this process's Wails window on the taskbar until it
// comes to the foreground.
func flashMainWindow() {
	pid := winapi.GetCurrentProcessId()
	var target uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var owner uint32
		procGetWindowThreadID.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if owner != pid {
			return 1
		}
		if vis, _, _ := procIsWindowVisible.Call(hwnd); vis == 0 {
			return 1
		}
		buf := make([]uint16, 64)
		n, _, _ := procGetClassName.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n > 0 && syscall.UTF16ToString(buf[:n]) == wailsWindowClass {
			target = hwnd
			return 0
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	if target == 0 {
		return
	}
	fi := flashInfo{Hwnd: target, Flags: flashwTray | flashwTimerNoFg}
	fi.Size = uint32(unsafe.Sizeof(fi))
	procFlashWindowEx.Call(uintptr(unsafe.Pointer(&fi)))
}

func main() {
	app := NewApp()
	frontend, err := fs.Sub(assets, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	err = wails.Run(&options.App{
		Title:     "ZTG Scanner",
		Width:     1320,
		Height:    820,
		MinWidth:  960,
		MinHeight: 560,
		AssetServer: &assetserver.Options{
			Assets: frontend,
		},
		// The app draws its own title bar (frontend/index.html, #titlebar) so the whole window
		// follows the app's theme. Frameless windows keep resizing; on Windows 11 they also keep
		// the shadow and rounded corners because DisableFramelessWindowDecorations stays false.
		Frameless:        true,
		CSSDragProperty:  "--wails-draggable",
		CSSDragValue:     "drag",
		BackgroundColour: &options.RGBA{R: 2, G: 12, B: 27, A: 255}, // ZTG bg (#020C1B); the frontend switches it with the theme
		OnStartup: func(ctx context.Context) {
			app.setPlatform(ctx, wailsPlatform{ctx: ctx, toast: &toastState{}})
		},
		OnShutdown: func(context.Context) { app.Shutdown() },
		OnBeforeClose: func(ctx context.Context) bool {
			if !app.UnsavedChanges() {
				return false
			}
			choice, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
				Type: runtime.WarningDialog, Title: "Unsaved scan", Message: "This scan has unsaved changes. Close without saving?",
				Buttons: []string{"Cancel", "Close without saving"}, DefaultButton: "Cancel", CancelButton: "Cancel",
			})
			return err != nil || choice != "Close without saving"
		},
		Bind: []interface{}{app},
		Windows: &windows.Options{
			Theme:               windows.SystemDefault,
			DisableWindowIcon:   false,
			WebviewUserDataPath: "", // default per-user location
			// Keep the Windows 11 shadow and rounded corners on the frameless window.
			DisableFramelessWindowDecorations: false,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
