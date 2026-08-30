package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/xywf221/steam-workshop-downloader/desktop/services"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
	"github.com/xywf221/steam-workshop-downloader/internal/version"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	logUI, _ := ui.New(ui.Options{})

	// Default output dir: <exe>/downloads (overridable in Settings).
	exe, _ := os.Executable()
	base := "."
	if exe != "" {
		base = filepath.Dir(exe)
	}
	outputDir := filepath.Join(base, "downloads")
	_ = os.MkdirAll(outputDir, 0o755)

	// Shared session + services. Proxy "" = direct (stealth TLS).
	sessMgr := services.NewSessionManager("", outputDir, logUI)
	dlSvc := services.NewDownloadService(sessMgr, nil)
	subsSvc := services.NewSubscriptionService(sessMgr, dlSvc)
	settingsSvc, err := services.NewSettingsService(sessMgr, subsSvc, base)
	if err != nil {
		log.Printf("settings: %v", err)
	}
	workshopSvc := services.NewWorkshopService(sessMgr)

	app := application.New(application.Options{
		Name:        version.AppName,
		Description: "Steam Workshop Downloader — desktop",
		Services: []application.Service{
			application.NewService(workshopSvc),
			application.NewService(dlSvc),
			application.NewService(subsSvc),
			application.NewService(settingsSvc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// Wire download progress events to the frontend.
	dlSvc.SetEmitter(func(event string, data any) {
		app.Event.Emit(event, data)
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  version.AppName,
		Width:  1180,
		Height: 760,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(11, 14, 20),
		URL:              "/",
	})

	// Warm up the Steam session in the background so the first action is fast.
	go sessMgr.Warmup()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
	sessMgr.Close()
}
