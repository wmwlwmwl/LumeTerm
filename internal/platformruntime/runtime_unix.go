//go:build linux || darwin

package platformruntime

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const singletonSocketName = "lumeterm.sock"

var (
	singletonLock *os.File
	serverState   struct {
		sync.Mutex
		listener net.Listener
		path     string
	}
)

func singleInstanceSocketPath() string {
	if runDir := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(runDir) {
		return filepath.Join(runDir, singletonSocketName)
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("lumeterm-%d.sock", os.Getuid()))
}

func findAndShowWindow() {
	conn, err := net.DialTimeout("unix", singleInstanceSocketPath(), 2*time.Second)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.WriteString(conn, "show\n")
}

func safeRemoveSocket(path string) {
	info, err := os.Lstat(path)
	if err == nil && info.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(path)
	}
}

// StartSingletonServer 启动当前用户的单实例唤醒 socket。
func StartSingletonServer(showFn func()) {
	serverState.Lock()
	defer serverState.Unlock()
	if serverState.listener != nil {
		return
	}

	path := singleInstanceSocketPath()
	safeRemoveSocket(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		safeRemoveSocket(path)
		return
	}
	serverState.listener = listener
	serverState.path = path

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSingletonConn(conn, showFn)
		}
	}()
}

func handleSingletonConn(conn net.Conn, showFn func()) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	frame, err := bufio.NewReader(io.LimitReader(conn, 6)).ReadString('\n')
	if err != nil && err != io.EOF {
		return
	}
	if (frame == "show\n" || frame == "show") && showFn != nil {
		showFn()
	}
}

// StopSingletonServer 关闭监听并清理当前进程创建的 socket。
func StopSingletonServer() {
	serverState.Lock()
	defer serverState.Unlock()
	if serverState.listener != nil {
		_ = serverState.listener.Close()
		serverState.listener = nil
	}
	if serverState.path != "" {
		safeRemoveSocket(serverState.path)
		serverState.path = ""
	}
}

// EnsureSingleInstance 获取当前用户的实例锁；已有实例时通知其显示窗口并退出。
func EnsureSingleInstance() {
	lockPath := filepath.Join(os.TempDir(), fmt.Sprintf("lumeterm-%d.lock", os.Getuid()))
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		fmt.Println("LumeTerm 已在运行。")
		findAndShowWindow()
		os.Exit(0)
	}
	singletonLock = file
}

// AcquireMainLivenessLock 获取主进程存活锁。
func AcquireMainLivenessLock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Truncate(0); err == nil {
		_, _ = file.Write([]byte("1"))
		_, _ = file.Seek(0, 0)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			_ = file.Close()
		})
	}, nil
}

// ForceShowWindow 在 Unix 平台依赖 Wails 自身恢复窗口。
func ForceShowWindow() {}

// RemoveTrayIconSync 是 Windows 专用托盘清理的 Unix 空实现。
func RemoveTrayIconSync() {}

// PrepareTrayMenu 是 Windows 专用前台解锁的 Unix 空实现。
func PrepareTrayMenu() {}

// AfterTrayMenu 是 Windows 专用托盘菜单状态清理的 Unix 空实现。
func AfterTrayMenu() {}

// AttachFramelessWindowFix 是 Windows 专用窗口边缘光标修复的 Unix 空实现。
func AttachFramelessWindowFix() {}
