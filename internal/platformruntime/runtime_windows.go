//go:build windows

package platformruntime

import (
	"log"
	"lumeterm/internal/apppaths"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	shell32                      = syscall.NewLazyDLL("shell32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetWindow                = user32.NewProc("GetWindow")
	procIsIconic                 = user32.NewProc("IsIconic")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procShowWindow               = user32.NewProc("ShowWindow")
	procShowWindowAsync          = user32.NewProc("ShowWindowAsync")
	procPostMessage              = user32.NewProc("PostMessageW")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop         = user32.NewProc("BringWindowToTop")
	procSetFocus                 = user32.NewProc("SetFocus")
	procSetActiveWindow          = user32.NewProc("SetActiveWindow")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
	procAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")
	procLockSetForegroundWindow  = user32.NewProc("LockSetForegroundWindow")
	procRedrawWindow             = user32.NewProc("RedrawWindow")
	procUpdateWindow             = user32.NewProc("UpdateWindow")
	procSendMessageTimeoutW      = user32.NewProc("SendMessageTimeoutW")
	procGetCurrentThreadId       = kernel32.NewProc("GetCurrentThreadId")
	procGetCurrentProcessId      = kernel32.NewProc("GetCurrentProcessId")
	procShellNotifyIconW         = shell32.NewProc("Shell_NotifyIconW")
	procIsZoomed                 = user32.NewProc("IsZoomed")
	comctl32                     = syscall.NewLazyDLL("comctl32.dll")
	procSetWindowSubclass        = comctl32.NewProc("SetWindowSubclass")
	procDefSubclassProc          = comctl32.NewProc("DefSubclassProc")
)

const (
	swHide          = 0
	swShowNormal    = 1
	swShow          = 5
	swMinimize      = 6
	swRestore       = 9
	swShowDefault   = 10
	gwOwner         = 4
	hwndTopmost     = ^uintptr(0) // -1
	hwndNotopmost   = ^uintptr(1) // -2
	swpNosize       = 0x0001
	swpNomove       = 0x0002
	swpNozorder     = 0x0004
	swpNoactivate   = 0x0010
	swpShowWindow   = 0x0040
	swpFramechanged = 0x0020
	// RedrawWindow
	rdwInvalidate  = 0x0001
	rdwErase       = 0x0004
	rdwFrame       = 0x0400
	rdwAllChildren = 0x0080
	rdwUpdatenow   = 0x0100
	// LockSetForegroundWindow
	lsfwUnlock = 2
	// AllowSetForegroundWindow(-1) = ASFW_ANY
	asfwAny   = ^uint32(0)
	nimDelete = 0x00000002
	// energye/systray 固定 uID=100；NIM_DELETE 靠 hWnd+uID 定位图标
	systrayIconID   = 100
	mainWindowTitle = "LumeTerm"
	wailsFormClass  = "winc_Form"
	systrayClass    = "SystrayClass"
	// SendMessageTimeoutW
	wmGetText       = 13
	wmGetTextLength = 14
	smtoBlock       = 0x0001
	smtoAbortifHung = 0x0002
	// GetWindowTextW 无超时，可能无限期阻塞在卡死的目标窗口线程上；
	// 限时读取窗口标题用 SendMessageTimeoutW(WM_GETTEXT)，最长等 windowTextTimeout。
	windowTextTimeout = 200
)

// trayNotifyIconData 对齐官方 NOTIFYICONDATA（uTimeout/uVersion 为 union，占 4 字节）。
// 仅用于退出时同步 NIM_DELETE；定位图标只靠 hWnd + uID。
type trayNotifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Timeout         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        [16]byte
	BalloonIcon     uintptr
}

var (
	cachedSystrayHWND     syscall.Handle
	cachedSystrayHWNDLock sync.Mutex
)

// systraySearchCtx 携带 SystrayClass 窗口搜索状态，经 EnumWindows 的 lParam 传入回调。
type systraySearchCtx struct {
	matchPID uint32
	found    syscall.Handle
}

// systrayEnumCallback 必须是包级唯一实例：syscall.NewCallback 在 Windows 上有
// 进程级数量上限（约 2000），在函数体内每次调用都新建，长期运行会触发
// "too many callbacks" panic。回调无闭包状态，搜索数据经 lParam 传递。
var systrayEnumCallback = syscall.NewCallback(func(hwnd syscall.Handle, lParam uintptr) uintptr {
	// uintptr 不能直接还原 unsafe.Pointer（go vet 禁止）；经 lParam 自身地址
	// 间接转译。EnumWindows 同步执行，ctx 在调用方栈上存活，生命周期安全。
	ctx := (*systraySearchCtx)(*(*unsafe.Pointer)(unsafe.Pointer(&lParam)))
	if windowClass(hwnd) != systrayClass {
		return 1
	}
	if ctx.matchPID != 0 {
		var pid uint32
		procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pid)))
		if pid != ctx.matchPID {
			return 1
		}
	}
	ctx.found = hwnd
	return 0
})

// findSystrayHWND 找本进程 energye 托盘隐藏窗（class=SystrayClass）
func findSystrayHWND(matchPID uint32) syscall.Handle {
	cachedSystrayHWNDLock.Lock()
	defer cachedSystrayHWNDLock.Unlock()

	// 优先检查缓存的句柄。句柄销毁后数值可能被系统复用（可能落到本进程的
	// 其它窗口上），仅校验 PID 不够，必须 PID + 窗口类双重校验；
	// 任一不符说明缓存已失效，清掉回退全量枚举。
	if matchPID == 0 || matchPID == uint32(os.Getpid()) {
		if cachedSystrayHWND != 0 {
			var pid uint32
			procGetWindowThreadProcessId.Call(uintptr(cachedSystrayHWND), uintptr(unsafe.Pointer(&pid)))
			if pid == uint32(os.Getpid()) && windowClass(cachedSystrayHWND) == systrayClass {
				return cachedSystrayHWND
			}
			cachedSystrayHWND = 0
		}
	}

	ctx := systraySearchCtx{matchPID: matchPID}
	procEnumWindows.Call(systrayEnumCallback, uintptr(unsafe.Pointer(&ctx)))
	if (matchPID == 0 || matchPID == uint32(os.Getpid())) && ctx.found != 0 {
		cachedSystrayHWND = ctx.found
	}
	return ctx.found
}

// removeTrayIconSync 退出前同步删托盘图标。
// energye 的 systray.Quit 只 PostMessage(WM_CLOSE)，NIM_DELETE 异步；
// 进程若先退出，Shell 会留下幽灵图标，鼠标扫过托盘才消失。
func RemoveTrayIconSync() {
	pid, _, _ := procGetCurrentProcessId.Call()
	hwnd := findSystrayHWND(uint32(pid))
	if hwnd == 0 {
		return
	}
	nid := trayNotifyIconData{
		Wnd: uintptr(hwnd),
		ID:  systrayIconID,
	}
	nid.Size = uint32(unsafe.Sizeof(nid))
	_, _, _ = procShellNotifyIconW.Call(uintptr(nimDelete), uintptr(unsafe.Pointer(&nid)))
}

// windowText 限时读取窗口标题（跨进程路径：二次启动唤醒已有实例时使用）。
// GetWindowTextW 会向目标窗口线程同步发送 WM_GETTEXT 并等待回复，
// 目标线程卡死时无限期阻塞（这正是此前托盘被拖死的直接原因）；
// SendMessageTimeoutW + SMTO_ABORTIFHUNG 在目标线程 hung 时立即/限时返回。
func windowText(hwnd syscall.Handle) string {
	buf := make([]uint16, 256)
	_, _, _ = procSendMessageTimeoutW.Call(
		uintptr(hwnd), uintptr(wmGetText), uintptr(len(buf)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(smtoAbortifHung|smtoBlock), windowTextTimeout, 0,
	)
	return syscall.UTF16ToString(buf)
}

func windowClass(hwnd syscall.Handle) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetClassNameW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

func isTopLevelWindow(hwnd syscall.Handle) bool {
	owner, _, _ := procGetWindow.Call(uintptr(hwnd), uintptr(gwOwner))
	return owner == 0
}

// activateHWND 久置后强拉主窗到前台。
// Windows 最小化/托盘隐藏久了，纯 SetForegroundWindow 常被拒；
// 再叠加透明 WebView，可能出现「任务栏有项但点了空白/无响应」。
//
// 本函数由托盘消息线程（systray goroutine）调用。SetWindowPos /
// BringWindowToTop / SetActiveWindow / SetFocus / RedrawWindow(RDW_UPDATENOW) /
// UpdateWindow 均同步发送消息到主窗口线程——若主线程卡死（SSH 断开等），
// 这些同步发送会阻塞托盘消息泵，导致托盘单击/双击/右键全部无响应。
// 用 SendMessageTimeoutW(WM_GETTEXTLENGTH, SMTO_ABORTIFHUNG) 探测主线程存活：
// 存活则返回非零（"Lumin" 标题长度），hung/超时返回 0。
func activateHWND(hwnd syscall.Handle) {
	if hwnd == 0 {
		return
	}

	// 放宽前台锁，降低久置后 SetForeground 被拒概率
	_, _, _ = procLockSetForegroundWindow.Call(uintptr(lsfwUnlock))
	_, _, _ = procAllowSetForegroundWindow.Call(uintptr(asfwAny))

	// 主窗口线程存活检查：SMTO_ABORTIFHUNG 在主线程 hung 时立即返回 0；
	// SMTO_BLOCK 阻止调用方消息泵重入。最多等 500ms。
	aliveRet, _, _ := procSendMessageTimeoutW.Call(
		uintptr(hwnd), uintptr(wmGetTextLength), 0, 0,
		uintptr(smtoAbortifHung|smtoBlock), 500, 0, // lpdwResult=NULL
	)
	mainThreadAlive := aliveRet != 0

	iconic, _, _ := procIsIconic.Call(uintptr(hwnd))
	visible, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
	if iconic != 0 {
		// 任务栏最小化：先异步再同步 restore，覆盖部分 WebView 卡住场景
		procShowWindowAsync.Call(uintptr(hwnd), uintptr(swRestore))
		if mainThreadAlive {
			procShowWindow.Call(uintptr(hwnd), uintptr(swRestore))
		}
	} else if visible == 0 {
		// 托盘 SW_HIDE：restore + show
		procShowWindowAsync.Call(uintptr(hwnd), uintptr(swRestore))
		if mainThreadAlive {
			procShowWindow.Call(uintptr(hwnd), uintptr(swRestore))
			procShowWindow.Call(uintptr(hwnd), uintptr(swShow))
			procShowWindow.Call(uintptr(hwnd), uintptr(swShowNormal))
		}
	} else {
		// 已显示但无前台/内容空白：仍走 restore/show 刷新
		procShowWindowAsync.Call(uintptr(hwnd), uintptr(swRestore))
		if mainThreadAlive {
			procShowWindow.Call(uintptr(hwnd), uintptr(swRestore))
			procShowWindow.Call(uintptr(hwnd), uintptr(swShow))
		}
	}

	if !mainThreadAlive {
		// 主线程卡死：ShowWindowAsync 已投递恢复指令到主线程消息队列。
		// 跳过所有同步消息发送，避免阻塞托盘消息泵。
		// 主线程恢复后窗口会自行处理队列中的指令。
		return
	}

	flags := uintptr(swpNomove | swpNosize | swpShowWindow | swpFramechanged)
	procSetWindowPos.Call(uintptr(hwnd), hwndTopmost, 0, 0, 0, 0, flags)
	procSetWindowPos.Call(uintptr(hwnd), hwndNotopmost, 0, 0, 0, 0, flags)

	fg, _, _ := procGetForegroundWindow.Call()
	curThread, _, _ := procGetCurrentThreadId.Call()
	var fgPid uint32
	fgThread, _, _ := procGetWindowThreadProcessId.Call(fg, uintptr(unsafe.Pointer(&fgPid)))
	var targetPid uint32
	targetThread, _, _ := procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&targetPid)))

	attachedFG := false
	attachedTarget := false
	if fgThread != 0 && fgThread != curThread {
		r, _, _ := procAttachThreadInput.Call(curThread, fgThread, 1)
		attachedFG = r != 0
	}
	if targetThread != 0 && targetThread != curThread && targetThread != fgThread {
		r, _, _ := procAttachThreadInput.Call(curThread, targetThread, 1)
		attachedTarget = r != 0
	}

	procBringWindowToTop.Call(uintptr(hwnd))
	procSetForegroundWindow.Call(uintptr(hwnd))
	procSetActiveWindow.Call(uintptr(hwnd))
	procSetFocus.Call(uintptr(hwnd))
	// 强制重绘，缓解透明 WebView 久置后「窗在但内容不刷」
	rdwFlags := uintptr(rdwInvalidate | rdwErase | rdwFrame | rdwAllChildren | rdwUpdatenow)
	procRedrawWindow.Call(uintptr(hwnd), 0, 0, rdwFlags)
	procUpdateWindow.Call(uintptr(hwnd))

	if attachedTarget {
		procAttachThreadInput.Call(curThread, targetThread, 0)
	}
	if attachedFG {
		procAttachThreadInput.Call(curThread, fgThread, 0)
	}
}

// mainWindowSearchCtx 携带主窗候选搜索状态，经 EnumWindows 的 lParam 传入回调。
type mainWindowSearchCtx struct {
	matchPID uint32
	seen     map[syscall.Handle]struct{}
	found    []syscall.Handle
}

// mainWindowEnumCallback 与 systrayEnumCallback 同理必须是包级唯一实例：
// 每次托盘左键/菜单唤起窗口都会走到这里（forceShowWindow 每次触发两次
// ForceShowWindow），函数内每次新建 syscall.NewCallback 会在约千次唤起后
// 耗尽进程级上限（约 2000）并 panic。
var mainWindowEnumCallback = syscall.NewCallback(func(hwnd syscall.Handle, lParam uintptr) uintptr {
	ctx := (*mainWindowSearchCtx)(*(*unsafe.Pointer)(unsafe.Pointer(&lParam)))
	if !isTopLevelWindow(hwnd) {
		return 1
	}
	class := windowClass(hwnd)
	if class == systrayClass {
		return 1
	}
	if ctx.matchPID != 0 {
		var pid uint32
		procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pid)))
		if pid != ctx.matchPID {
			return 1
		}
	}
	// 当前进程已按 Wails 窗口类识别，无需读取标题；GetWindowTextW
	// 可能向卡死的目标窗口线程同步取值，反而阻塞托盘消息线程。
	hit := false
	if ctx.matchPID != 0 {
		hit = class == wailsFormClass
	} else {
		// 跨进程二次启动：只认标题，避免误激活其他 Wails 应用。
		hit = windowText(hwnd) == mainWindowTitle
	}
	if !hit {
		return 1
	}
	if _, ok := ctx.seen[hwnd]; ok {
		return 1
	}
	ctx.seen[hwnd] = struct{}{}
	ctx.found = append(ctx.found, hwnd)
	return 1
})

// findMainWindowCandidates 枚举本进程/跨进程可能的 Lumin 主窗。
// 久置+多次点击时任务栏可能出现多个条目，唤醒时优先可见/未最小化的 winc_Form。
func findMainWindowCandidates(matchPID uint32) []syscall.Handle {
	ctx := mainWindowSearchCtx{
		matchPID: matchPID,
		seen:     map[syscall.Handle]struct{}{},
		found:    make([]syscall.Handle, 0, 4),
	}
	procEnumWindows.Call(mainWindowEnumCallback, uintptr(unsafe.Pointer(&ctx)))
	return ctx.found
}

type windowCandidate struct {
	handle  syscall.Handle
	visible bool
	iconic  bool
}

func pickBestCandidate(candidates []windowCandidate) syscall.Handle {
	var bestVisible, bestIconic, bestAny syscall.Handle
	for _, candidate := range candidates {
		if candidate.handle == 0 {
			continue
		}
		if bestAny == 0 {
			bestAny = candidate.handle
		}
		if candidate.visible && !candidate.iconic && bestVisible == 0 {
			bestVisible = candidate.handle
		}
		if candidate.iconic && bestIconic == 0 {
			bestIconic = candidate.handle
		}
	}
	if bestVisible != 0 {
		return bestVisible
	}
	if bestIconic != 0 {
		return bestIconic
	}
	return bestAny
}

// pickBestMainWindow 多窗口时选最该激活的那个：可见且非最小化 > 仅最小化 > 其它。
func pickBestMainWindow(windows []syscall.Handle) syscall.Handle {
	candidates := make([]windowCandidate, 0, len(windows))
	for _, hwnd := range windows {
		iconic, _, _ := procIsIconic.Call(uintptr(hwnd))
		visible, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
		candidates = append(candidates, windowCandidate{
			handle:  hwnd,
			visible: visible != 0,
			iconic:  iconic != 0,
		})
	}
	return pickBestCandidate(candidates)
}

// findAndShowWindow 二次启动时唤醒已有实例窗口（按标题，跨进程）
func findAndShowWindow() {
	cands := findMainWindowCandidates(0)
	if hwnd := pickBestMainWindow(cands); hwnd != 0 {
		activateHWND(hwnd)
	}
}

// PrepareTrayMenu 托盘右键菜单弹出前调用。
// energye ShowMenu 内部会对托盘隐藏窗 SetForegroundWindow 再 TrackPopupMenu；
// 久置后前台被拒时菜单直接不显示。这里先解锁前台并激活托盘窗。
func PrepareTrayMenu() {
	_, _, _ = procLockSetForegroundWindow.Call(uintptr(lsfwUnlock))
	_, _, _ = procAllowSetForegroundWindow.Call(uintptr(asfwAny))
	pid, _, _ := procGetCurrentProcessId.Call()
	hwnd := findSystrayHWND(uint32(pid))
	if hwnd == 0 {
		log.Printf("[Systray] PrepareTrayMenu: SystrayClass HWND not found for PID %d", pid)
		return
	}
	fgRes, _, fgErr := procSetForegroundWindow.Call(uintptr(hwnd))
	topRes, _, topErr := procBringWindowToTop.Call(uintptr(hwnd))
	log.Printf("[Systray] PrepareTrayMenu: hwnd=0x%x, SetForegroundWindow=%v (err=%v), BringWindowToTop=%v (err=%v)",
		hwnd, fgRes != 0, fgErr, topRes != 0, topErr)
}

// AfterTrayMenu 托盘右键菜单关闭后调用（发送 WM_NULL 确保 Windows Shell 清理菜单状态）。
func AfterTrayMenu() {
	pid, _, _ := procGetCurrentProcessId.Call()
	hwnd := findSystrayHWND(uint32(pid))
	if hwnd != 0 {
		const wmNull = 0x0000
		procPostMessage.Call(uintptr(hwnd), uintptr(wmNull), 0, 0)
		log.Printf("[Systray] AfterTrayMenu: posted WM_NULL to hwnd 0x%x", hwnd)
	}
}

// platformForceShowWindow 托盘/任务栏久置后唤醒：激活本进程主窗，并尽量恢复其它最小化副本。
func ForceShowWindow() {
	pid, _, _ := procGetCurrentProcessId.Call()
	cands := findMainWindowCandidates(uint32(pid))
	if len(cands) == 0 {
		return
	}
	best := pickBestMainWindow(cands)
	activateHWND(best)
	// 多次点击可能留下多个最小化条目：一并 restore，减少「点了没反应/点到空白副本」
	// 只用 ShowWindowAsync（非阻塞），避免主线程卡死时 ShowWindow 同步发送阻塞托盘
	for _, hwnd := range cands {
		if hwnd == 0 || hwnd == best {
			continue
		}
		iconic, _, _ := procIsIconic.Call(uintptr(hwnd))
		visible, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
		if iconic != 0 || visible == 0 {
			procShowWindowAsync.Call(uintptr(hwnd), uintptr(swRestore))
		}
	}
}

// ensureSingleInstance 检查是否已有实例运行，如果是则唤醒已有窗口并退出
func EnsureSingleInstance() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	procCreateMutex := kernel32.NewProc("CreateMutexW")
	mutexName, _ := syscall.UTF16PtrFromString("LumeTerm_Global_Single_Instance_Mutex")
	_, _, errMutex := procCreateMutex.Call(0, 1, uintptr(unsafe.Pointer(mutexName)))
	if errMutex == syscall.ERROR_ALREADY_EXISTS {
		findAndShowWindow()
		os.Exit(0)
	}
}

// startSingletonServer Windows 上为空操作：Windows 用命名 mutex + HWND 直接激活，
// 不需要 socket IPC（findAndShowWindow 已在二进程内完成跨进程窗口唤起）。
func StartSingletonServer(_ func()) {}

// stopSingletonServer Windows 上无 socket 资源需要清理。
func StopSingletonServer() {}

func AcquireMainLivenessLock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Truncate(0); err == nil {
		_, _ = file.Write([]byte("1"))
		_, _ = file.Seek(0, 0)
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx := kernel32.NewProc("LockFileEx")
	procUnlockFileEx := kernel32.NewProc("UnlockFileEx")
	const lockfileExclusiveLock = 0x00000002
	var overlapped syscall.Overlapped
	locked, _, lockErr := procLockFileEx.Call(
		file.Fd(),
		uintptr(lockfileExclusiveLock),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if locked == 0 {
		_ = file.Close()
		if lockErr != syscall.Errno(0) {
			return nil, lockErr
		}
		return nil, syscall.EINVAL
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_, _, _ = procUnlockFileEx.Call(
				file.Fd(),
				0,
				1,
				0,
				uintptr(unsafe.Pointer(&overlapped)),
			)
			_ = file.Close()
		})
	}, nil
}

// getScreenSize 用 Windows API 获取主显示器可用逻辑像素（已扣除 DPI 缩放）
func getScreenSize() (int, int) {
	user32 := syscall.NewLazyDLL("user32.dll")
	// 获取系统 DPI（Per-Monitor DPI Aware 下 GetSystemMetrics 返回物理像素，需除以缩放比）
	getDpi := user32.NewProc("GetDpiForSystem")
	dpi, _, _ := getDpi.Call()
	if dpi == 0 {
		dpi = 96
	}
	scale := float64(dpi) / 96.0

	smCx := user32.NewProc("GetSystemMetrics")
	const SM_CXSCREEN = 0
	const SM_CYSCREEN = 1
	cx, _, _ := smCx.Call(SM_CXSCREEN)
	cy, _, _ := smCx.Call(SM_CYSCREEN)
	return int(float64(cx) / scale), int(float64(cy) / scale)
}

// applyPlatformOptions 设置 Windows 特定的 Wails 选项，并根据屏幕大小自适应窗口尺寸
func ApplyOptions(opts *options.App, webviewGpuDisabled bool) {
	// ponytail: 根据屏幕分辨率自适应窗口大小，上限 1440x900，留 10% 边距
	sw, sh := getScreenSize()
	targetW := int(float64(sw) * 0.9)
	targetH := int(float64(sh) * 0.9)
	if opts.Width > targetW {
		opts.Width = targetW
	}
	if opts.Height > targetH {
		opts.Height = targetH
	}

	// 固定 WebView2 用户数据根目录为 %AppData%\LumeTerm，避免便携包改名后按 exe 名多出
	// LumeTerm-x.y.z-portable.exe/EBWebView。引擎会在其下自建 EBWebView，与 config 同级。
	webviewUserDataPath := apppaths.DataRoot()
	_ = os.MkdirAll(webviewUserDataPath, 0700)

	opts.Windows = &windows.Options{
		WebviewIsTransparent:              true,
		WindowIsTranslucent:               true,
		DisableWindowIcon:                 false,
		DisableFramelessWindowDecorations: false,
		WebviewUserDataPath:               webviewUserDataPath,
		ZoomFactor:                        1.0,
		WebviewGpuIsDisabled:              webviewGpuDisabled,
		Theme:                             windows.Dark,
	}
}

const (
	wmNCHitTest   = 0x0084
	htClient      = 1
	htGrowBox     = 4
	htLeft        = 10
	htRight       = 11
	htTop         = 12
	htTopLeft     = 13
	htTopRight    = 14
	htBottom      = 15
	htBottomLeft  = 16
	htBottomRight = 17
	htBorder      = 18
)

func framelessWindowSubclassProc(hwnd syscall.Handle, msg uint32, wParam, lParam, uIdSubclass, dwRefData uintptr) uintptr {
	ret, _, _ := procDefSubclassProc.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	if msg == wmNCHitTest {
		isZoomed, _, _ := procIsZoomed.Call(uintptr(hwnd))
		if isZoomed != 0 {
			// 最大化时，鼠标靠近边缘不应出现调整窗口大小的光标
			if ret == htGrowBox || (ret >= htLeft && ret <= htBorder) {
				return htClient
			}
		}
	}
	return ret
}

var subclassCallback = syscall.NewCallback(framelessWindowSubclassProc)

var attachedSubclassHandles sync.Map

// AttachFramelessWindowFix 对当前进程的 Wails 主窗口安装子类化，修复最大化时边缘光标显示为调整窗口大小的问题。
func AttachFramelessWindowFix() {
	pid, _, _ := procGetCurrentProcessId.Call()
	hwnds := findMainWindowCandidates(uint32(pid))
	for _, hwnd := range hwnds {
		if _, loaded := attachedSubclassHandles.LoadOrStore(hwnd, struct{}{}); !loaded {
			const subclassID = 1001
			ret, _, _ := procSetWindowSubclass.Call(uintptr(hwnd), subclassCallback, subclassID, 0)
			if ret == 0 {
				attachedSubclassHandles.Delete(hwnd)
			}
		}
	}
}
