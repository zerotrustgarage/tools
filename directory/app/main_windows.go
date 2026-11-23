//go:build windows

package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/zerotrustgarage/tools/directory/console"
	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/auth"
	"github.com/zerotrustgarage/tools/directory/engine/demo"
)

//go:embed all:frontend
var assets embed.FS

// version is set at build time (build-windows.sh).
var version = "dev"

// dataDir is %LOCALAPPDATA%\ZTG Directory: change records, templates and the
// sign-in settings of this computer.
func dataDir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		if base, err = os.UserCacheDir(); err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "ZTG Directory"), nil
}

// build chooses the backend: the demo directory, the live domain when the
// sign-in settings exist, or the first-run setup.
func build(demoMode bool, settingsPath string) (*console.API, string, error) {
	dir, err := dataDir()
	if err != nil {
		return nil, "", err
	}
	if demoMode {
		// Demo records and templates stay apart from the live evidence.
		svc, err := console.New(console.NewDemoBackend(), console.Config{DataDir: filepath.Join(dir, "demo"), Version: version})
		if err != nil {
			return nil, "", err
		}
		if err := svc.SeedTemplates(demo.Templates()); err != nil {
			return nil, "", err
		}
		return console.NewAPI(svc, nil), "ZTG Directory (demo)", nil
	}
	if settingsPath == "" {
		settingsPath = filepath.Join(dir, "settings.json")
	}
	var backend console.Backend = console.SetupBackend{}
	if _, err := os.Stat(settingsPath); err == nil {
		s, err := auth.LoadSettings(settingsPath)
		if err != nil {
			return nil, "", err
		}
		lb, err := console.NewLiveBackend(s, ad.OpenWriter)
		if err != nil {
			return nil, "", err
		}
		backend = lb
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	svc, err := console.New(backend, console.Config{DataDir: dir, Version: version})
	if err != nil {
		return nil, "", err
	}
	// A first live run gets the demo templates as examples to copy; an
	// existing template folder is never touched.
	if err := svc.SeedTemplates(demo.Templates()); err != nil {
		return nil, "", err
	}
	wiring := &console.LiveWiring{SettingsPath: settingsPath, Open: ad.OpenWriter, Protect: auth.ProtectSecret}
	return console.NewAPI(svc, wiring), "ZTG Directory", nil
}

func main() {
	demoMode := flag.Bool("demo", false, "open the fictional demo directory instead of a domain")
	settings := flag.String("settings", "", `sign-in settings file (default %LOCALAPPDATA%\ZTG Directory\settings.json)`)
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments: "+strings.Join(flag.Args(), " "))
		os.Exit(2)
	}
	api, title, err := build(*demoMode, *settings)
	if err != nil {
		log.Fatal(err)
	}
	frontend, err := fs.Sub(assets, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	err = wails.Run(&options.App{
		Title:            title,
		Width:            1440,
		Height:           900,
		MinWidth:         1024,
		MinHeight:        640,
		AssetServer:      &assetserver.Options{Assets: frontend},
		BackgroundColour: &options.RGBA{R: 2, G: 12, B: 27, A: 255}, // ZTG bg (#020C1B)
		Bind:             []interface{}{api},
		Windows: &windows.Options{
			Theme:               windows.SystemDefault,
			WebviewUserDataPath: "", // default per-user location
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
