package wailsapp

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
)

// initLogFile 必须真正落盘：设置临时配置目录后，log.Printf 应写入 lumeterm.log。
// exe 同级目录通过 logExeDirSeam 指到临时目录，避免测试在构建缓存目录留下日志。
func TestInitLogFileWritesToDisk(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp) // Windows
	t.Setenv("HOME", tmp)        // Unix
	t.Setenv("APPDATA", filepath.Join(tmp, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config")) // Linux 隔离，避免污染真实用户配置
	logExeDirSeam = filepath.Join(tmp, "exe")
	if err := os.MkdirAll(logExeDirSeam, 0700); err != nil {
		t.Fatalf("创建 exe 缝目录失败: %v", err)
	}
	t.Cleanup(func() { logExeDirSeam = "" })

	cleanup := initLogFile()
	if cleanup == nil {
		t.Fatal("initLogFile 返回 nil，日志未初始化")
	}
	defer func() {
		cleanup()
		log.SetOutput(os.Stderr)
	}()
	log.Printf("[channel-diag] TEST-MARKER connect session=probe")

	// 预期路径与 initLogFile 保持一致（apppaths 首次调用于 initLogFile 内，
	// 此时 Setenv 已生效）：os.UserConfigDir() 平台自适配，
	// Windows=APPDATA、Linux=$XDG_CONFIG_HOME 或 ~/.config，避免测试写死 Windows 路径。
	ucd, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("获取用户配置目录失败: %v", err)
	}
	logPath := filepath.Join(ucd, "LumeTerm", "config", "lumeterm.log")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("lumeterm.log 未生成: %v", err)
	}
	if !contains(string(raw), "TEST-MARKER") {
		t.Fatalf("lumeterm.log 内容缺少测试标记: %q", string(raw))
	}
	// exe 同级目录也应落盘（便携版场景），且不能污染真实运行目录
	exeLog, err := os.ReadFile(filepath.Join(logExeDirSeam, "lumeterm.log"))
	if err != nil {
		t.Fatalf("exe 同级 lumeterm.log 未生成: %v", err)
	}
	if !contains(string(exeLog), "TEST-MARKER") {
		t.Fatalf("exe 同级 lumeterm.log 内容缺少测试标记: %q", string(exeLog))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// failingSink 模拟写必失败的 sink（近似 GUI 进程无控制台时 os.Stderr 写失败）。
type failingSink struct{}

func (failingSink) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }

// teeLogWriter 必须容忍单个 sink 失败：排在失败 sink 之后的文件 sink 仍要写入。
// 这正是改用 teeLogWriter 取代 io.MultiWriter 的原因——io.MultiWriter 任一 sink
// 返回 error 即放弃后续 sink，会导致 GUI 无控制台时 os.Stderr 失败 → 文件完全不落盘。
func TestTeeLogWriterToleratesFailingSink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lumin.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("打开临时日志失败: %v", err)
	}
	// 失败 sink 排在文件 sink 之前，复现 stderr 在 MultiWriter 首位的场景
	tee := teeLogWriter{writers: []io.Writer{failingSink{}, f}}
	if _, err := tee.Write([]byte("AFTER-FAIL-MARKER\n")); err != nil {
		t.Fatalf("teeLogWriter.Write 不应向上抛 sink 错误: %v", err)
	}
	f.Close()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	if !contains(string(raw), "AFTER-FAIL-MARKER") {
		t.Fatalf("失败 sink 短路了文件 sink，日志未落盘: %q", string(raw))
	}
}
