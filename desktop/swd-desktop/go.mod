module github.com/xywf221/steam-workshop-downloader/desktop

go 1.25.6

require github.com/wailsapp/wails/v3 v3.0.0-beta.16

require (
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/ulikunitz/xz v0.5.16 // indirect
	golang.org/x/net v0.57.0 // indirect
)

require (
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/xywf221/steam-workshop-downloader v0.0.0
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/xywf221/steam-workshop-downloader => ../..
