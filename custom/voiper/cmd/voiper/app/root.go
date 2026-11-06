package app

import (
	goruntime "runtime"

	assets "github.com/megakuul/voiper/build/package"
	"github.com/megakuul/voiper/cmd/voiper/flags"
	"github.com/megakuul/voiper/internal/desktop"
	"github.com/megakuul/voiper/internal/version"
	"github.com/megakuul/voiper/web"
	"github.com/spf13/cobra"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func NewRootCmd() *cobra.Command {
	options := NewRootOptions(flags.NewGlobalFlags())

	var rootCmd = &cobra.Command{
		Use:          "voiper [sip:address|tel:number]",
		Args:         cobra.MaximumNArgs(1),
		Short:        "Voiper Softphone",
		Version:      version.Version(),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				target, err := dialTarget(args[0])
				if err != nil {
					return err
				}
				options.target = target
			}
			if err := options.Run(); err != nil {
				return err
			}
			return nil
		},
	}
	options.globalFlags.Attach(rootCmd.PersistentFlags())

	return rootCmd
}

type RootOptions struct {
	target      string
	globalFlags *flags.GlobalFlags
}

func NewRootOptions(gFlags *flags.GlobalFlags) *RootOptions {
	return &RootOptions{
		globalFlags: gFlags,
	}
}

func (r *RootOptions) Run() error {
	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()
	if err := desktop.PrepareGTK(); err != nil {
		return err
	}
	app := NewApp(
		WithBase(r.globalFlags.Base),
	)

	app.dialTarget = r.target
	return wails.Run(&options.App{
		Title:  "voiper",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: web.Asset,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose:    app.beforeClose,
		SingleInstanceLock: &options.SingleInstanceLock{UniqueId: "com.megakuul.voiper", OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
			for _, arg := range data.Args {
				if target, err := dialTarget(arg); err == nil {
					app.receiveURI(target)
					return
				}
			}
			app.mu.Lock()
			ctx := app.ctx
			app.mu.Unlock()
			if ctx != nil {
				runtime.WindowShow(ctx)
			}
		}},
		// Wails otherwise disables Linux compositing, making scrolling CPU-bound.
		// ProgramName is the Wayland app ID; it must match the desktop entry name.
		Linux:     &linux.Options{WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand, ProgramName: "voiper", Icon: assets.Icon},
		MinWidth:  480,
		MinHeight: 400,
		Bind: []interface{}{
			app,
		},
	})
}
