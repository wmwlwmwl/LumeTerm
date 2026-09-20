package main

import (
	"embed"
	goruntime "runtime"

	"lumeterm/internal/wailsapp"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

//go:embed build/windows/icon.ico
var windowsIcon []byte

var icon = func() []byte {
	if goruntime.GOOS == "windows" {
		return windowsIcon
	}
	return appIcon
}()

func main() {
	wailsapp.Run(assets, icon)
}
