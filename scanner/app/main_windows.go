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
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 21, A: 255},
		OnStartup: func(ctx context.Context) {
			app.setPlatform(ctx, wailsPlatform{ctx: ctx})
		},
		OnShutdown: func(context.Context) { app.StopScan() },
		Bind:       []interface{}{app},
		Windows: &windows.Options{
			Theme:               windows.SystemDefault,
			DisableWindowIcon:   false,
			WebviewUserDataPath: "", // default per-user location
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
