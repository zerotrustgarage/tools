package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	winapi "golang.org/x/sys/windows"
)

//go:embed all:frontend
var assets embed.FS

type wailsPlatform struct{ ctx context.Context }

func (p wailsPlatform) Directory(title string) (string, error) {
	return runtime.OpenDirectoryDialog(p.ctx, runtime.OpenDialogOptions{Title: title})
}
func (p wailsPlatform) OpenFile(title string) (string, error) {
	return runtime.OpenFileDialog(p.ctx, runtime.OpenDialogOptions{Title: title, Filters: []runtime.FileFilter{{DisplayName: "Copy JSON", Pattern: "*.json"}}})
}
func (p wailsPlatform) SaveFile(name string) (string, error) {
	return runtime.SaveFileDialog(p.ctx, runtime.SaveDialogOptions{Title: "Export to a new JSON file", DefaultFilename: name, Filters: []runtime.FileFilter{{DisplayName: "Copy JSON", Pattern: "*.json"}}})
}
func (p wailsPlatform) RevealDirectory(dir string) error {
	windowsDir, err := winapi.GetWindowsDirectory()
	if err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(windowsDir, "explorer.exe"), dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func main() {
	app := NewApp()
	frontend, err := fs.Sub(assets, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	err = wails.Run(&options.App{
		Title: "ZTG Copy", Width: 1360, Height: 880, MinWidth: 1050, MinHeight: 680,
		Frameless: true, CSSDragProperty: "--wails-draggable", CSSDragValue: "drag",
		BackgroundColour: &options.RGBA{R: 2, G: 12, B: 27, A: 255},
		AssetServer:      &assetserver.Options{Assets: frontend},
		OnStartup:        func(ctx context.Context) { app.setPlatform(ctx, wailsPlatform{ctx}) },
		OnShutdown:       func(context.Context) { app.Shutdown() },
		OnBeforeClose: func(ctx context.Context) bool {
			if !app.Status().Running {
				return false
			}
			answer, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
				Type: runtime.WarningDialog, Title: "Copy in progress",
				Message: "Stop the current operation and close ZTG Copy?",
				Buttons: []string{"Cancel", "Stop and close"}, DefaultButton: "Cancel", CancelButton: "Cancel",
			})
			if err != nil || answer != "Stop and close" {
				return true
			}
			app.Stop()
			return false
		},
		Bind:    []interface{}{app},
		Windows: &windows.Options{Theme: windows.SystemDefault, DisableFramelessWindowDecorations: false},
	})
	if err != nil {
		log.Fatal(err)
	}
}
