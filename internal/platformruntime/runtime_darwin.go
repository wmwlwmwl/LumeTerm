//go:build darwin

package platformruntime

import (
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

// ApplyOptions 设置 macOS 窗口选项。
func ApplyOptions(opts *options.App, _ bool) {
	if opts == nil {
		return
	}
	opts.Mac = &mac.Options{
		TitleBar:             mac.TitleBarHiddenInset(),
		Appearance:           mac.DefaultAppearance,
		WebviewIsTransparent: false,
		WindowIsTranslucent:  false,
		About: &mac.AboutInfo{
			Title:   "LumeTerm",
			Message: "Lightweight Terminal Client",
		},
	}
}
