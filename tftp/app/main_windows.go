//go:build windows

package main

import (
	"context"
	"embed"
	"io/fs"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend
var assets embed.FS

type wailsPlatform struct{ ctx context.Context }

func (p wailsPlatform) Directory() (string, error) {
	return runtime.OpenDirectoryDialog(p.ctx, runtime.OpenDialogOptions{Title: "Choose a folder"})
}
func (p wailsPlatform) OpenFile() (string, error) {
	return runtime.OpenFileDialog(p.ctx, runtime.OpenDialogOptions{Title: "Choose a file"})
}
func (p wailsPlatform) SaveFile(name string) (string, error) {
	return runtime.SaveFileDialog(p.ctx, runtime.SaveDialogOptions{DefaultFilename: name, Title: "Export audit log"})
}

func main() {
	app := NewApp()
	frontend, err := fs.Sub(assets, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	err = wails.Run(&options.App{
		Title:     "ZTG TFTP",
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
		Frameless:        false,
		CSSDragProperty:  "--wails-draggable",
		CSSDragValue:     "drag",
		BackgroundColour: &options.RGBA{R: 2, G: 12, B: 27, A: 255}, // ZTG bg (#020C1B); the frontend switches it with the theme
		OnStartup: func(ctx context.Context) {
			app.ctx, app.cancel = context.WithCancel(ctx)
			app.platform = wailsPlatform{ctx: ctx}
		},
		OnShutdown: func(context.Context) { app.cancel(); app.Stop() },
		Bind:       []interface{}{app},
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
