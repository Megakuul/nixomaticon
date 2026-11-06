package main

import (
	"embed"
	"io/fs"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

const userAgent = "Dell SonicWALL NetExtender for Linux 8.1.789"

// pppdPath is the pppd binary used by the helper. pkexec resets PATH, so the
// nix build pins it with -ldflags "-X main.pppdPath=...".
var pppdPath = "pppd"

//go:embed all:frontend
var frontend embed.FS

func main() {
	// The GUI re-executes itself as root via pkexec in helper mode; the helper
	// owns pppd, routes and DNS and speaks JSON lines over stdin/stdout.
	if len(os.Args) > 1 && os.Args[1] == "helper" {
		os.Exit(runHelper(len(os.Args) > 2 && os.Args[2] == "--fake"))
	}

	assets, err := fs.Sub(frontend, "frontend")
	if err != nil {
		log.Fatal(err)
	}

	app := NewApp()
	err = wails.Run(&options.App{
		Title:            "Vibebender",
		Width:            440,
		Height:           720,
		MinWidth:         400,
		MinHeight:        560,
		BackgroundColour: &options.RGBA{R: 11, G: 13, B: 20, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		Bind:             []interface{}{app},
		Linux: &linux.Options{
			ProgramName: "vibebender",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
