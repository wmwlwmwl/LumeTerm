package wailsapp

// ponytail: 应用入口逻辑（原 main.go 的 Wails 启动 + 托盘 + 生命周期回调）。
// main.go 仅保留 //go:embed（路径相对根目录）并把资源注入 Run。
// 回调与 App 同包，可直接访问未导出字段（ctx/quitting/closeAck/configManager），
// 故无需为迁移新增任何导出方法，绑定结构体方法集零变化。

import (
	"context"
	"embed"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"lumeterm/internal/apppaths"
	"lumeterm/internal/mcpbridge"
	"lumeterm/internal/platformruntime"

	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// forceShowWindow 唤醒隐藏到托盘/久置最小化的窗口。
// 不先 Hide 再 Show：久置后 Show 失败会把窗口永久卡在隐藏态。
// 先走平台原生激活抢前台（Windows 久置后 SetForeground 常被拒），再异步走 Wails 恢复。
// 原生激活放前后各一次：覆盖「仅最小化」和「托盘隐藏」两种状态。
// ponytail: 托盘消息线程只做调度，绝不在这里同步执行窗口调用。
// energye/systray 的 wndProc 在托盘线程上同步回调 onClick；若同步执行
// platformruntime.ForceShowWindow（内部含 EnumWindows/ShowWindow/
// SetWindowPos/SetForegroundWindow/RedrawWindow 等向主窗口线程的
// 消息发送，无超时），主线程一旦卡死（SSH 断线等）托盘线程会无限期阻塞，
// 单击/双击/右键全部无响应，只能重启软件恢复。全部异步化后即使窗口操作
// 阻塞，托盘消息泵也能立即返回继续响应。
func forceShowWindow(ctx context.Context) {
	defer func() { recover() }()
	go func() {
		defer func() { recover() }()
		platformruntime.ForceShowWindow()
	}()
	if ctx != nil {
		go func() {
			defer func() { recover() }()
			wailsruntime.WindowUnminimise(ctx)
			wailsruntime.WindowShow(ctx)
		}()
	}
	go func() {
		defer func() { recover() }()
		platformruntime.ForceShowWindow()
	}()
}

var systrayOnce sync.Once

func setupSystray(app *App) {
	systrayOnce.Do(func() {
		log.Println("[Systray] setupSystray initializing...")
		systray.SetIcon(app.icon)
		systray.SetTitle("LumeTerm")
		systray.SetTooltip("LumeTerm")

		mShow := systray.AddMenuItem("显示主窗口", "Show Main Window")
		mQuit := systray.AddMenuItem("完全退出", "Quit LumeTerm")

		showMain := func() {
			log.Println("[Systray] showMain invoked, awakening main window")
			forceShowWindow(app.ctx)
		}

		if runtime.GOOS == "darwin" {
			// macOS: CreateMenu 将菜单永久挂到 statusItem，
			// 左键点托盘图标自动弹菜单（macOS 惯例）。
			// 不调 SetOnClick/SetOnRClick：enableOnClick 会覆盖菜单行为，
			// 且库注释明确 ShowMenu() 在 macOS 只支持 OnRClick 回调内调用。
			systray.CreateMenu()
			log.Println("[Systray] macOS CreateMenu configured")
		} else {
			// Windows/Linux: 左键直接显示窗口
			systray.SetOnClick(func(menu systray.IMenu) {
				log.Println("[Systray] Left click received, showing main window")
				showMain()
			})
			// 右键弹菜单；Windows 久置后 TrackPopupMenu 常因前台锁不弹出，
			// 先解锁再 ShowMenu。
			systray.SetOnRClick(func(menu systray.IMenu) {
				log.Println("[Systray] Right click received, preparing tray menu")
				platformruntime.PrepareTrayMenu()
				if err := menu.ShowMenu(); err != nil {
					log.Printf("[Systray] ShowMenu returned: %v", err)
				} else {
					log.Println("[Systray] ShowMenu completed successfully")
				}
				platformruntime.AfterTrayMenu()
			})
			log.Println("[Systray] Windows/Linux SetOnClick and SetOnRClick configured")
		}

		mShow.Click(func() {
			log.Println("[Systray] Menu item 'Show Main Window' clicked")
			showMain()
		})

		mQuit.Click(func() {
			log.Println("[Systray] Menu item 'Quit LumeTerm' clicked")
			app.DoQuit()
		})

		log.Println("[Systray] setupSystray completed successfully")
	})
}

// maxLogFileSize 日志单文件上限：超过则轮转（lumeterm.log → lumeterm.log.1），
// 防止长期运行无限增长；保留一份历史便于回溯。
const maxLogFileSize = 5 << 20 // 5MB

// rotatingFileWriter 带大小轮转的文件 writer。按本进程写入量累计，
// 超过上限时把当前文件改名为 .1 并重开新文件，保证单次运行内也会轮转
// （仅启动时检查的话，长跑几天单文件会无限膨胀）。
type rotatingFileWriter struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	written int64
}

func newRotatingFileWriter(path string) (*rotatingFileWriter, error) {
	w := &rotatingFileWriter{path: path}
	if err := w.reopen(); err != nil {
		return nil, err
	}
	return w, nil
}

// reopen 以追加模式打开文件，并把当前大小作为已写基线（跨进程追加的历史计入）。
func (w *rotatingFileWriter) reopen() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if info, err := f.Stat(); err == nil {
		w.written = info.Size()
	}
	w.f = f
	return nil
}

func (w *rotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		// 上次轮转后重开失败（罕见），下次写入再试
		if err := w.reopen(); err != nil {
			return len(p), nil
		}
	}
	if w.written+int64(len(p)) > maxLogFileSize {
		// 先改名再重开：改名失败（如被其它进程占用）继续写旧文件，最多超限一次
		if os.Rename(w.path, w.path+".1") == nil {
			if err := w.reopen(); err != nil {
				w.f = nil
			}
		}
	}
	n, err := w.f.Write(p)
	w.written += int64(n)
	return n, err
}

func (w *rotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// teeLogWriter 把日志扇出到多个 sink，每个 sink 独立写入并忽略其错误。
// 不用 io.MultiWriter：MultiWriter 任一 sink 返回 error 会短路后续 sink
// （标准库 io.MultiWriter 源码如此），而 GUI 进程无控制台时 os.Stderr.Write
// 会失败（句柄无效 ERROR_INVALID_HANDLE），会导致排在 stderr 之后的文件 sink
// 全部不执行 → 日志落不了盘，恰好与「落盘便于远程排查」的目的相悖。
// 日志场景允许个别 sink 丢条，绝不能因一个 sink 失败而连累其它 sink。
type teeLogWriter struct {
	writers []io.Writer
}

func (t teeLogWriter) Write(p []byte) (int, error) {
	for _, w := range t.writers {
		_, _ = w.Write(p)
	}
	return len(p), nil
}

// logExeDirSeam 仅测试注入：覆盖 exe 同级日志目录，避免测试在构建缓存目录留下 lumeterm.log。
var logExeDirSeam = ""

// initLogFile 把标准 log 输出重定向（控制台 + 文件双写）：
//  1. os.Stderr —— 从终端/调试器启动时日志仍可见；窗口应用没有控制台时静默丢弃
//  2. %AppData%\LumeTerm\config\lumeterm.log —— 主日志，始终写入
//  3. exe 同级目录 lumeterm.log —— 便携版场景：对方解压运行后日志就在运行目录，
//     无需进入隐藏的 %AppData%，直接取回即可；安装版（Program Files）写失败自动忽略
//
// 追加模式，0600，单文件 5MB 运行期轮转。
// 返回清理函数（关闭文件句柄），应在 wails.Run 返回后调用。
func initLogFile() func() {
	var writers []io.Writer
	var closers []io.Closer

	writers = append(writers, os.Stderr)

	// 触发数据目录迁移（Lumin → LumeTerm）：必须在打开任何旧目录文件句柄之前
	dir := filepath.Join(apppaths.DataRoot(), "config")
	if err := os.MkdirAll(dir, 0700); err == nil {
		if w, err := newRotatingFileWriter(filepath.Join(dir, "lumeterm.log")); err == nil {
			writers = append(writers, w)
			closers = append(closers, w)
		}
	}
	if exePath, err := os.Executable(); err == nil {
		dir := filepath.Dir(exePath)
		if logExeDirSeam != "" {
			dir = logExeDirSeam
		}
		if w, err := newRotatingFileWriter(filepath.Join(dir, "lumeterm.log")); err == nil {
			writers = append(writers, w)
			closers = append(closers, w)
		}
	}
	if len(closers) == 0 {
		return func() {}
	}
	log.SetOutput(teeLogWriter{writers})
	log.Printf("[LumeTerm] Logger initialized. Log files: %d", len(closers))
	return func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}
}

// Run 启动 Wails 应用。embed 资源由 main 包注入（//go:embed 路径必须相对根目录的 main.go）。
func Run(assets embed.FS, icon []byte) {
	// 日志落盘：先于一切业务日志，保证 [channel-diag] 等诊断可追溯
	closeLogs := initLogFile()

	// 单实例检查（平台特定实现）
	platformruntime.EnsureSingleInstance()

	app := NewApp()
	app.icon = icon

	systrayEnd := platformruntime.PrepareSystray(func() { setupSystray(app) })

	// 退出时先同步删托盘图标，再 systray.Quit。
	// Windows 上纯异步 Quit 常在 NIM_DELETE 前进程已死，留下幽灵图标。
	var trayCleanupOnce sync.Once
	cleanupTray := func() {
		trayCleanupOnce.Do(func() {
			platformruntime.RemoveTrayIconSync()
			systrayEnd()
		})
	}
	app.onBeforeQuit = cleanupTray

	// Create application with options
	opts := &options.App{
		Title:     "LumeTerm",
		Width:     1440,
		Height:    900,
		Frameless: true,
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 8, G: 12, B: 20, A: 255}, // #080c14
		OnStartup: func(ctx context.Context) {
			// 先挂托盘：startup 里 MCP 等可能阻塞，托盘若排后面会出现「窗口已能关到托盘但图标很久才出」。
			app.ctx = ctx
			platformruntime.AttachFramelessWindowFix()
			go func() {
				time.Sleep(200 * time.Millisecond)
				platformruntime.AttachFramelessWindowFix()
				time.Sleep(1 * time.Second)
				platformruntime.AttachFramelessWindowFix()
			}()
			// macOS: 窗口隐藏到托盘后，点 Dock 图标恢复窗口。
			// Wails 的 AppDelegate 未实现 applicationShouldHandleReopen:hasVisibleWindows:。
			platformruntime.SetupDockReopenHandler(func() { forceShowWindow(app.ctx) })
			platformruntime.StartSystray(func() { setupSystray(app) })
			// 启动单实例 socket：二次启动会发 show 指令，经 forceShowWindow 走托盘同一路径唤起主窗口。
			platformruntime.StartSingletonServer(func() {
				forceShowWindow(app.ctx)
			})
			app.startup(ctx)
		},
		OnShutdown: func(ctx context.Context) {
			app.shutdown()
			platformruntime.StopSingletonServer()
			mcpbridge.StopServer(newMCPHost(app))
			cleanupTray()
		},
		// 拦截窗口关闭：弹出对话框让用户选择退出 / 系统托盘 / 取消
		OnBeforeClose: func(ctx context.Context) bool {
			if app.quitting.Load() {
				return false // 用户确认退出，放行
			}
			app.closeAck.Store(false) // 重置，等待本次前端响应
			wailsruntime.EventsEmit(ctx, "close-request")
			// 超时兜底：仅当前端 5 秒内无响应（崩溃/JS 异常）时强制退出；
			// 前端选 tray/cancel 会调 AckClose 置位 closeAck，跳过强制退出
			go func() {
				time.Sleep(5 * time.Second)
				if !app.quitting.Load() && !app.closeAck.Load() {
					// 前端无响应时也必须复用统一退出清理，先断开 SSH 再退出。
					app.DoQuit()
				}
			}()
			return true // 取消关闭，由前端弹窗决定后续操作
		},
		Bind: []interface{}{
			app,
			NewAIBindings(app),
			NewAIProviderBindings(app.configManager),
		},
	}

	if _, ok := os.LookupEnv("LUMIN_OPEN_DEVTOOLS"); ok {
		opts.Debug.OpenInspectorOnStartup = true
	}

	// 应用平台特定选项（平台特定实现）
	gpuDisabled := app.configManager != nil && app.configManager.GetWebviewGpuDisabled()
	platformruntime.ApplyOptions(opts, gpuDisabled)

	err := wails.Run(opts)
	// 退出后关闭日志文件句柄，避免残留
	closeLogs()

	if err != nil {
		println("Error:", err.Error())
	}
}
