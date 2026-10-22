//go:build windows

package main

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
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
type wailsPlatform struct{ ctx context.Context }

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
		appName, err := winapi.UTF16PtrFromString(program)
		if err != nil {
			return "", err
		}
		argv := append([]string{program}, args...)
		for i := range argv {
			argv[i] = winapi.EscapeArg(argv[i])
		}
		commandLine, err := winapi.UTF16PtrFromString(strings.Join(argv, " "))
		if err != nil {
			return "", err
		}
		startup := winapi.StartupInfo{Cb: uint32(unsafe.Sizeof(winapi.StartupInfo{}))}
		var process winapi.ProcessInformation
		var flags uint32
		if action == "ssh" {
			flags = winapi.CREATE_NEW_CONSOLE
		}
		if err := winapi.CreateProcess(appName, commandLine, nil, nil, false, flags, nil, nil, &startup, &process); err != nil {
			return "", fmt.Errorf("could not launch the installed %s client: %w", action, err)
		}
		_ = winapi.CloseHandle(process.Thread)
		_ = winapi.CloseHandle(process.Process)
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
			app.setPlatform(ctx, wailsPlatform{ctx: ctx})
		},
		OnShutdown: func(context.Context) { app.StopScan() },
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
