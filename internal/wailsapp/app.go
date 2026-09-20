package wailsapp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ai "lumeterm/internal/ai"
	"lumeterm/internal/config"
	"lumeterm/internal/externaledit"
	"lumeterm/internal/localopen"
	"lumeterm/internal/mcpbridge"
	"lumeterm/internal/ping"
	"lumeterm/internal/platformruntime"
	"lumeterm/internal/platformupdate"
	"lumeterm/internal/programfonts"
	"lumeterm/internal/sshmanager"
	"lumeterm/internal/transfer"
	"lumeterm/internal/updatedownload"
	"lumeterm/internal/wsbuffer"
	"lumeterm/internal/wslocal"
	runtimeenv "lumeterm/module/runtimeenv"
	runtimeinstaller "lumeterm/module/runtimeinstaller"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type externalEditRemoteFiles struct {
	manager *sshmanager.SSHManager
}

func (r externalEditRemoteFiles) Size(sessionID string, remotePath string) (int64, error) {
	client, err := r.manager.GetSFTPClient(sessionID)
	if err != nil {
		return 0, err
	}
	info, err := client.Stat(remotePath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (r externalEditRemoteFiles) Read(sessionID string, remotePath string) ([]byte, error) {
	return r.manager.ReadFileBytes(sessionID, remotePath)
}

func (r externalEditRemoteFiles) Write(sessionID string, remotePath string, data []byte) error {
	return r.manager.WriteFile(sessionID, remotePath, string(data))
}

type externalEditEventSink struct {
	app *App
}

func (s externalEditEventSink) Emit(event string, payload map[string]interface{}) {
	if s.app != nil && s.app.ctx != nil {
		runtime.EventsEmit(s.app.ctx, event, payload)
	}
}

type externalEditOpener struct{}

func (externalEditOpener) OpenDefault(localPath string) error {
	return localopen.OpenDocument(localPath)
}

func (externalEditOpener) OpenWith(editorPath string, localPath string) error {
	cleanedEditor := filepath.Clean(strings.TrimSpace(editorPath))
	cleanedFile := filepath.Clean(strings.TrimSpace(localPath))
	if cleanedEditor == "" {
		return fmt.Errorf("missing editor path")
	}
	if _, err := os.Stat(cleanedEditor); err != nil {
		return err
	}
	if _, err := os.Stat(cleanedFile); err != nil {
		return err
	}
	if goruntime.GOOS == "darwin" && strings.HasSuffix(strings.ToLower(cleanedEditor), ".app") {
		return exec.Command("open", "-a", cleanedEditor, cleanedFile).Start()
	}
	return exec.Command(cleanedEditor, cleanedFile).Start()
}

// App struct
type App struct {
	ctx                     context.Context
	sshManager              *sshmanager.SSHManager
	configManager           *config.ConfigManager
	wsManager               *wsbuffer.Manager
	wsServer                *wslocal.Server
	mainLivenessLockPath    string
	mainLivenessLockRelease func()
	quitting                atomic.Bool
	closeAck                atomic.Bool
	shutdownOnce            sync.Once
	quitOnce                sync.Once
	onBeforeQuit            func()
	liveWorkspaceStateMu    sync.RWMutex
	liveWorkspaceState      string
	externalEdit            *externaledit.Manager
	icon                    []byte
	mcpReporter             *mcpActivityReporter
}

type GitHubContributorAuthor struct {
	Login  string `json:"login"`
	Avatar string `json:"avatar"`
	Path   string `json:"path"`
}

type GitHubContributorWeek struct {
	W int `json:"w"`
	A int `json:"a"`
	D int `json:"d"`
	C int `json:"c"`
}

type GitHubContributor struct {
	Author GitHubContributorAuthor `json:"author"`
	Total  int                     `json:"total"`
	Weeks  []GitHubContributorWeek `json:"weeks"`
}

const githubRepoURL = "https://github.com/wmwlwmwl/LumeTerm"

const githubContributorsAPIURL = githubRepoURL + "/graphs/contributors-data"

const githubContributorsRefererURL = githubRepoURL + "/graphs/contributors"

const githubContributorsRequestTimeout = 10 * time.Second

const githubContributorsMaxRetries = 5

// NewApp creates a new App application struct
func NewApp() *App {
	app := &App{
		sshManager:    sshmanager.NewSSHManager(),
		configManager: config.NewConfigManager(),
		wsManager:     wsbuffer.NewManager(),
	}
	app.mcpReporter = newMCPActivityReporter(app)
	app.configManager.SetProgramDir(getProgramDirectory())
	app.configManager.SetOnDeleteConnection(ping.ClearPingHostState)
	app.externalEdit = externaledit.NewManager(
		externalEditRemoteFiles{manager: app.sshManager},
		externalEditEventSink{app: app},
		externalEditOpener{},
	)
	return app
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.sshManager.SetCtx(ctx) // Give SSH manager access to Wails events
	a.sshManager.SetApp(a)   // Give SSH manager access to WebSocket registry
	a.configManager.SetWailsCtx(ctx)
	a.sshManager.ApplyTransferTuning(a.configManager.GetTransferTuningSettings())
	if err := a.ensureMainLivenessLock(); err != nil {
		log.Printf("failed to acquire main liveness lock: %v", err)
	}

	// ── 启动本地 WebSocket 终端服务器 ─────────────────────────────
	// 不经过 Wails IPC，直接走 TCP loopback，延迟极低
	wsServer := wslocal.NewServer(a.wsManager, a.sshManager)
	if err := wsServer.Start(); err != nil {
		log.Printf("failed to start local WebSocket server: %v", err)
	} else {
		a.wsServer = wsServer
	}

	// Clean up old executable from a previous auto-update
	exePath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exePath)
		files, err := os.ReadDir(dir)
		if err == nil {
			for _, file := range files {
				if !file.IsDir() && strings.HasSuffix(file.Name(), ".old") {
					os.Remove(filepath.Join(dir, file.Name()))
				}
			}
		}
	}

	a.configManager.CleanupOrphanedHistory()
	go a.configManager.AutoSync()
	mcpbridge.InitOutputCompression(a.configManager.GetConfigDir())
	// MCP 客户端会连远端（内置 context7），同步握手会拖慢首屏；后台启动即可。
	go mcpbridge.StartServer(a.configManager.GetConfigDir(), newMCPHost(a))
}

// AckClose 前端响应了关闭弹窗（tray/cancel），取消 5s 兜底强制退出
func (a *App) AckClose() {
	a.closeAck.Store(true)
}

// shutdown 清理应用持有的资源，可由主动退出和 Wails OnShutdown 重复调用。
func (a *App) shutdown() {
	a.shutdownOnce.Do(func() {
		// 在 runtime.Quit 之前清理托盘，确保 Windows 消息循环仍在运行。
		if a.onBeforeQuit != nil {
			a.onBeforeQuit()
		}
		if a.externalEdit != nil {
			a.externalEdit.StopAll()
		}
		// 断开所有 SSH 会话，避免服务器侧遗留僵尸会话。
		if a.sshManager != nil {
			a.sshManager.DisconnectAll()
		}
		// 关闭本地 WebSocket 服务器，释放端口。
		if a.wsServer != nil {
			a.wsServer.Stop()
			a.wsServer = nil
		}
		a.releaseMainLivenessLock()
	})
}

// DoQuit 用户确认退出，设标记让 OnBeforeClose 放行并清理应用资源。
func (a *App) DoQuit() {
	a.quitting.Store(true)
	a.shutdown()
	a.quitOnce.Do(func() {
		if a.ctx != nil {
			runtime.Quit(a.ctx)
		}
	})
}

// GetWsPort 返回本地 WebSocket 服务器端口，前端用于连接终端
func (a *App) GetWsPort() int {
	if a.wsServer == nil {
		return 0
	}
	return a.wsServer.Port()
}

// GetWsToken 返回 WebSocket 鉴权 token，前端连接时通过 ?token=xxx 携带
func (a *App) GetWsToken() string {
	if a.wsServer == nil {
		return ""
	}
	return a.wsServer.Token()
}

// WriteWsOutput 将 WebSocket 输出写入给指定 session 的 WS 连接
func (a *App) WriteWsOutput(sessionId string, data []byte) {
	a.wsManager.WriteOutput(sessionId, data)
}

// CleanupSession 在会话彻底销毁时清理所有旁路资源。
func (a *App) CleanupSession(sessionId string) {
	if a.externalEdit != nil {
		a.externalEdit.StopSessionSilently(sessionId)
	}
	if a.wsManager != nil {
		a.wsManager.CloseSession(sessionId)
	}
}

// IsPortableVersion checks if the current executable is the portable version
// 开发构建默认视为便携版（exe 名不含 installer/setup）
func (a *App) IsPortableVersion() bool {
	exePath, err := os.Executable()
	if err != nil {
		return true // 无法确定时默认便携版
	}
	exeName := strings.ToLower(filepath.Base(exePath))
	if strings.Contains(exeName, "installer") || strings.Contains(exeName, "setup") {
		return false
	}
	// 安装在 Program Files 下的一定是安装版
	return !strings.Contains(strings.ToLower(filepath.Dir(exePath)), "program files")
}

// GetArch returns the current executable's CPU architecture (amd64, arm64, etc.)
func (a *App) GetArch() string {
	return goruntime.GOARCH
}

// ClipboardGetText 读取系统剪贴板文本。前端在 WKWebView（macOS）下通过它绕开
// navigator.clipboard.readText 触发的 WebKit "Paste" 提示气泡（issue #263）。
func (a *App) ClipboardGetText() (string, error) {
	return runtime.ClipboardGetText(a.ctx)
}

// GetConnections returns all saved SSH connections
func (a *App) GetConnections() []config.Connection {
	return a.configManager.GetConnections()
}

// GetConnectionsMasked 返回掩码后的连接列表，用于前端显示
func (a *App) GetConnectionsMasked() []config.Connection {
	return a.configManager.GetConnectionsMasked()
}

// GetConnectionByID 返回指定连接的真实数据（含解密密码），供克隆等场景使用
func (a *App) GetConnectionByID(id string) (config.Connection, error) {
	conn, ok := a.configManager.GetConnectionByID(id)
	if !ok {
		return config.Connection{}, fmt.Errorf("connection not found")
	}
	return conn, nil
}

func (a *App) GetConnectionPassword(id string) (string, error) {
	conn, ok := a.configManager.GetConnectionByID(id)
	if !ok {
		return "", fmt.Errorf("connection not found")
	}
	resolvedConn, err := a.configManager.ResolveConnectionRuntime(conn)
	if err != nil {
		return "", err
	}
	if resolvedConn.AuthMethod != "password" {
		return "", fmt.Errorf("current connection does not use password auth")
	}
	if strings.TrimSpace(resolvedConn.Password) == "" {
		return "", fmt.Errorf("password is empty")
	}
	return resolvedConn.Password, nil
}

// SaveConnection saves a new or existing connection
func (a *App) SaveConnection(conn config.Connection, noSync bool) config.Connection {
	return a.configManager.SaveConnection(conn, noSync)
}

// DeleteConnection removes a connection by ID
func (a *App) DeleteConnection(id string) bool {
	return a.configManager.DeleteConnection(id)
}

// BatchDeleteConnections removes multiple connections at once
func (a *App) BatchDeleteConnections(ids []string) {
	a.configManager.BatchDeleteConnections(ids)
}

// ExportConnections 导出全部节点到用户选择的文件。
// useEncryption=false 导出明文 .json（含真实密码/私钥）；true 导出密文 .lumeterm2。
// password 非空时用于 LUMETERM2 PBKDF2 派生；空则用本机恢复密码（与云端同步加密口径一致）。
// 弹出保存对话框；用户取消时返回 ("", nil)。返回写入的文件路径。
func (a *App) ExportConnections(useEncryption bool, password string) (string, error) {
	if useEncryption && strings.TrimSpace(password) == "" {
		password = a.configManager.GetRecoveryPassword()
		if password == "" {
			return "", fmt.Errorf("未设置恢复密码，请输入自定义密码或先在同步设置中设置恢复密码")
		}
	}

	// 保存对话框
	ext := ".json"
	title := "导出节点"
	if useEncryption {
		ext = ".lumeterm2"
	}
	timestamp := time.Now().Format("20060102_150405.000_-0700")
	defaultName := fmt.Sprintf("lumeterm-connections-%s%s", timestamp, ext)
	filters := []runtime.FileFilter{
		{DisplayName: fmt.Sprintf("LumeTerm (*%s)", ext), Pattern: "*" + ext},
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: defaultName,
		Filters:         filters,
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		// 用户取消，非错误
		return "", nil
	}

	conns := a.configManager.GetConnections()
	creds := a.configManager.GetCredentials()
	proxyNodes := a.configManager.GetAIProxyNodes()
	exp := config.BuildConnectionsExportWithProxyNodes(conns, creds, proxyNodes)

	if !useEncryption {
		// 明文：序列化后直接写文件
		data, err := json.MarshalIndent(exp, "", "  ")
		if err != nil {
			return "", fmt.Errorf("导出失败: %w", err)
		}
		if err := config.AtomicWriteFile(path, data, 0600); err != nil {
			return "", fmt.Errorf("导出失败: %w", err)
		}
		return path, nil
	}

	// 密文：序列化 → LUMETERM2 加密
	encrypted, err := a.configManager.EncryptExportData(exp, password)
	if err != nil {
		return "", fmt.Errorf("导出失败: %w", err)
	}
	if err := config.AtomicWriteFile(path, []byte(encrypted), 0600); err != nil {
		return "", fmt.Errorf("导出失败: %w", err)
	}
	return path, nil
}

// ExportConnectionsByIDs 按 ID 列表导出节点到用户选择的文件。
// ids 为空时等同导出全部（向后兼容 ExportConnections）。
// useEncryption=false 导出明文 .json；true 导出密文 .lumeterm2。
// password 非空时用于 LUMETERM2 PBKDF2 派生；空则用本机恢复密码。
func (a *App) ExportConnectionsByIDs(ids []string, useEncryption bool, password string) (string, error) {
	if useEncryption && strings.TrimSpace(password) == "" {
		password = a.configManager.GetRecoveryPassword()
		if password == "" {
			return "", fmt.Errorf("未设置恢复密码，请输入自定义密码或先在同步设置中设置恢复密码")
		}
	}

	// 保存对话框
	ext := ".json"
	title := "导出节点"
	if useEncryption {
		ext = ".lumeterm2"
	}
	timestamp := time.Now().Format("20060102_150405.000_-0700")
	defaultName := fmt.Sprintf("lumeterm-connections-%s%s", timestamp, ext)
	filters := []runtime.FileFilter{
		{DisplayName: fmt.Sprintf("LumeTerm (*%s)", ext), Pattern: "*" + ext},
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: defaultName,
		Filters:         filters,
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}

	allConns := a.configManager.GetConnections()

	// 按 ID 过滤（空列表 = 全部）
	var conns []config.Connection
	if len(ids) == 0 {
		conns = allConns
	} else {
		idSet := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			idSet[id] = struct{}{}
		}
		for _, c := range allConns {
			if _, ok := idSet[c.ID]; ok {
				conns = append(conns, c)
			}
		}
	}

	creds := a.configManager.GetCredentials()
	proxyNodes := a.configManager.GetAIProxyNodes()
	exp := config.BuildConnectionsExportWithProxyNodes(conns, creds, proxyNodes)

	if !useEncryption {
		data, err := json.MarshalIndent(exp, "", "  ")
		if err != nil {
			return "", fmt.Errorf("导出失败: %w", err)
		}
		if err := config.AtomicWriteFile(path, data, 0600); err != nil {
			return "", fmt.Errorf("导出失败: %w", err)
		}
		return path, nil
	}

	// 密文：序列化 → LUMETERM2 加密
	encrypted, err := a.configManager.EncryptExportData(exp, password)
	if err != nil {
		return "", fmt.Errorf("导出失败: %w", err)
	}
	if err := config.AtomicWriteFile(path, []byte(encrypted), 0600); err != nil {
		return "", fmt.Errorf("导出失败: %w", err)
	}
	return path, nil
}

// SelectImportFile 弹出打开对话框让用户选择导入文件，返回文件路径（用户取消返回空串）。
// 与 ImportConnections 分离，便于密文导入需要密码时无需重新选文件。
func (a *App) SelectImportFile() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "导入节点",
		Filters: []runtime.FileFilter{
			{DisplayName: "LumeTerm (*.json;*.lumeterm2;*.lumin2)", Pattern: "*.json;*.lumeterm2;*.lumin2"},
		},
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// ImportConnections 从指定文件导入节点（合并，跳过重复）。
// filePath 由前端通过 SelectImportFile 获取；password 为弹窗输入的自定义解密密码（可空）。
// 智能识别明文 JSON / LUMETERM2 密文：
//   - 明文直接解析
//   - 密文优先用本机恢复密码，失败返回 config.ErrNeedPassword，前端弹窗输入自定义密码后再试
func (a *App) ImportConnections(filePath string, password string) (config.ImportResult, error) {
	if filePath == "" {
		return config.ImportResult{}, nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return config.ImportResult{}, fmt.Errorf("导入失败: %w", err)
	}

	if strings.TrimSpace(password) == "" {
		password = a.configManager.GetRecoveryPassword()
	}

	exp, err := a.configManager.ParseImportData(data, password)
	if err != nil {
		if errors.Is(err, config.ErrNeedPassword) {
			// 原样返回，前端识别此 sentinel 并弹密码框
			return config.ImportResult{}, err
		}
		return config.ImportResult{}, fmt.Errorf("导入失败: %w", err)
	}
	return a.configManager.ImportConnections(exp.Connections, exp.Credentials, exp.ProxyNodes)
}

// DownloadImportTemplate 下载导入模板（含样例的明文 JSON），方便用户批量录入。
// 弹出保存对话框；用户取消时返回 ("", nil)。返回写入的文件路径。
func (a *App) DownloadImportTemplate(lang string) (string, error) {
	title := "保存导入模板"
	if lang == "en-US" {
		title = "Save Import Template"
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: "lumeterm-import-template.json",
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	tmpl := config.BuildImportTemplate(lang)
	data, err := json.MarshalIndent(tmpl, "", "  ")
	if err != nil {
		if lang == "en-US" {
			return "", fmt.Errorf("failed to generate template: %w", err)
		}
		return "", fmt.Errorf("生成模板失败: %w", err)
	}
	if err := config.AtomicWriteFile(path, data, 0600); err != nil {
		if lang == "en-US" {
			return "", fmt.Errorf("failed to save template: %w", err)
		}
		return "", fmt.Errorf("保存模板失败: %w", err)
	}
	return path, nil
}

// HasRecoveryPassword 返回是否设置了恢复密码（供前端导出 UI 决定是否允许"复用恢复密码"）。
func (a *App) HasRecoveryPassword() bool {
	return a.configManager.GetRecoveryPassword() != ""
}

// SetConnectionGroup 仅更新服务器分组
func (a *App) SetConnectionGroup(id string, group string) error {
	return a.configManager.SetConnectionGroup(id, group)
}

// BatchSetConnectionGroup 批量更新服务器分组
func (a *App) BatchSetConnectionGroup(ids []string, group string) error {
	return a.configManager.BatchSetConnectionGroup(ids, group)
}

// RenameConnectionGroup 重命名服务器分组（批量更新该组下所有连接的 group）
func (a *App) RenameConnectionGroup(oldName, newName string) error {
	return a.configManager.RenameConnectionGroup(oldName, newName)
}

// SetConnectionOS 仅更新服务器操作系统
func (a *App) SetConnectionOS(id string, os string) error {
	return a.configManager.SetConnectionOS(id, os)
}

// ConnectSSH establishes an SSH connection
func (a *App) ConnectSSH(sessionId string, connId string) error {
	conn, ok := a.configManager.GetConnectionByID(connId)
	if !ok {
		return fmt.Errorf("connection not found")
	}
	resolvedConn, err := a.configManager.ResolveConnectionRuntime(conn)
	if err != nil {
		return err
	}
	return a.sshManager.Connect(sessionId, resolvedConn)
}

func (a *App) StartLocalPortForward(sessionId string, localAddr string, remoteAddr string) (string, error) {
	connKey := a.sshManager.ConnKeyForSession(sessionId)
	if connKey == "" {
		return "", fmt.Errorf("session not found")
	}
	return a.sshManager.StartLocalPortForward(connKey, localAddr, remoteAddr)
}

func (a *App) StartRemotePortForward(sessionId string, remoteAddr string, localAddr string) (string, error) {
	connKey := a.sshManager.ConnKeyForSession(sessionId)
	if connKey == "" {
		return "", fmt.Errorf("session not found")
	}
	return a.sshManager.StartRemotePortForward(connKey, remoteAddr, localAddr)
}

func (a *App) ListPortForwards(sessionId string) ([]sshmanager.PortForwardInfo, error) {
	connKey := a.sshManager.ConnKeyForSession(sessionId)
	if connKey == "" {
		return nil, fmt.Errorf("session not found")
	}
	return a.sshManager.ListPortForwardsForSession(sessionId)
}

// portForwardBelongsToSession 校验某端口映射是否归属给定会话对应的连接, 防止仅凭全局 id 越权操作。
func (a *App) portForwardBelongsToSession(sessionId string, id string) bool {
	return a.sshManager.PortForwardBelongsToSession(sessionId, id)
}

func (a *App) StopPortForward(id string) error {
	return a.sshManager.StopPortForward(id)
}

func (a *App) StopPortForwardForSession(sessionId string, id string) error {
	if !a.portForwardBelongsToSession(sessionId, id) {
		return fmt.Errorf("port forward not found for session")
	}
	return a.sshManager.StopPortForward(id)
}

func (a *App) DeletePortForwardForSession(sessionId string, id string) error {
	if !a.portForwardBelongsToSession(sessionId, id) {
		return fmt.Errorf("port forward not found for session")
	}
	return a.sshManager.DeletePortForward(id)
}

func (a *App) RestartPortForwardForSession(sessionId string, id string) (string, error) {
	if !a.portForwardBelongsToSession(sessionId, id) {
		return "", fmt.Errorf("port forward not found for session")
	}
	return a.sshManager.RestartPortForward(id)
}

// ReconnectWithPassword 更新密码并重连（认证失败后使用）
// persist: true=保存密码, false=仅本次会话使用
func (a *App) ReconnectWithPassword(sessionId string, connId string, newPassword string, persist bool) error {
	conn, ok := a.configManager.GetConnectionByID(connId)
	if !ok {
		return fmt.Errorf("connection not found")
	}

	resolved, err := a.configManager.ResolveConnectionRuntime(conn)
	if err != nil {
		return err
	}
	resolved.Password = newPassword

	// 清理旧会话。Disconnect 会清掉「只接受本次」的临时密钥授权，
	// 但换密码重连仍是同一会话，须跨重连保留，否则主机密钥确认会二次弹出。
	tempKey, hadTempKey := a.sshManager.TempAcceptedKey(sessionId)
	a.sshManager.Disconnect(sessionId)
	if hadTempKey {
		a.sshManager.RestoreTempAcceptedKey(sessionId, tempKey)
	}

	// 重新连接
	err = a.sshManager.Connect(sessionId, resolved)
	// 重连失败且非认证失败（认证失败会再弹密码框继续重试）时清除临时密钥，
	// 与 AcceptHostKeyChange 的失败处理保持一致，避免残留导致静默绕过校验。
	if hadTempKey && err != nil && !errors.Is(err, sshmanager.ErrAuthFailed) {
		a.sshManager.ClearTempAcceptedKey(sessionId)
	}
	if err != nil || !persist {
		return err
	}

	// 仅在认证成功后持久化密码，避免将错误密码写入配置；连接成功收尾会统一触发同步。
	if conn.CredentialID != "" {
		cred, credOk := a.configManager.GetCredentialByID(conn.CredentialID)
		if credOk {
			cred.Password = newPassword
			a.configManager.SaveCredentialNoSync(cred)
		}
	} else {
		conn.Password = newPassword
		a.configManager.SaveConnection(conn, true)
	}
	return nil
}

// DisconnectSSH closes an SSH connection
func (a *App) DisconnectSSHConnection(sessionId string, terminalIds []string) {
	a.sshManager.DisconnectConnection(sessionId, terminalIds)
}

func (a *App) DisconnectSSH(sessionId string) {
	a.sshManager.Disconnect(sessionId)
}

// OpenRemoteFileInSystemEditor downloads a remote file to a local temp path and opens it
// with the OS default application. When readOnly is false, local changes are watched and
// synced back via SFTP; when true (e.g. media files opened for viewing), no watch/sync is set up.
// content may be empty to re-read from remote; non-empty content is used as the initial temp file.
// 编辑大小上限由用户配置（GetFileManagerMaxEditSizeBytes），每次打开实时读取以反映最新设置。
func (a *App) OpenRemoteFileInSystemEditor(sessionId string, remotePath string, content string, readOnly bool) (map[string]interface{}, error) {
	if a.externalEdit == nil {
		return nil, fmt.Errorf("external editor not ready")
	}
	maxBytes := a.configManager.GetFileManagerMaxEditSizeBytes()
	return a.externalEdit.Open(sessionId, remotePath, content, "", readOnly, maxBytes)
}

// OpenRemoteFileWithEditor is like OpenRemoteFileInSystemEditor but launches a specific editor binary/.app.
func (a *App) OpenRemoteFileWithEditor(sessionId string, remotePath string, content string, editorPath string, readOnly bool) (map[string]interface{}, error) {
	if a.externalEdit == nil {
		return nil, fmt.Errorf("external editor not ready")
	}
	maxBytes := a.configManager.GetFileManagerMaxEditSizeBytes()
	return a.externalEdit.Open(sessionId, remotePath, content, editorPath, readOnly, maxBytes)
}

// SetFileManagerMaxEditSize sets the max editable file size (MB), normalized to [1, 50].
func (a *App) SetFileManagerMaxEditSize(mb int) error {
	return a.configManager.SetFileManagerMaxEditSize(mb)
}

// SelectExternalEditor opens a native file dialog for choosing an editor executable.
func (a *App) SelectExternalEditor() (string, error) {
	filters := []runtime.FileFilter{
		{DisplayName: "Applications", Pattern: "*.*"},
	}
	if goruntime.GOOS == "windows" {
		filters = []runtime.FileFilter{
			{DisplayName: "Executables (*.exe)", Pattern: "*.exe"},
			{DisplayName: "All files", Pattern: "*.*"},
		}
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "选择外部编辑器",
		Filters: filters,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(path), nil
}

// StopExternalEdit stops watching and cleans temp files for one remote path.
func (a *App) StopExternalEdit(sessionId string, remotePath string) error {
	if a.externalEdit == nil {
		return nil
	}
	return a.externalEdit.Stop(sessionId, remotePath)
}

// ListExternalEditSessions returns active external-edit sessions.
func (a *App) ListExternalEditSessions() []map[string]interface{} {
	if a.externalEdit == nil {
		return nil
	}
	return a.externalEdit.List()
}

// AcceptHostKeyChange 用户确认主机密钥变更
// action: 0=取消, 1=仅本次接受, 2=接受并保存
func (a *App) AcceptHostKeyChange(sessionId string, action int) error {
	return a.sshManager.AcceptHostKeyChange(sessionId, action)
}

// OpenTerminal 在当前服务器连接上打开新的终端标签页
func (a *App) OpenTerminal(sessionId string) (string, error) {
	existing, ok := a.sshManager.GetSession(sessionId)
	if !ok {
		return "", fmt.Errorf("session not found")
	}

	if existing.IsLocal {
		randomId := make([]byte, 8)
		if _, err := rand.Read(randomId); err != nil {
			return "", fmt.Errorf("生成 session ID 失败: %w", err)
		}
		newId := fmt.Sprintf("term_%x", randomId)

		err := a.sshManager.ConnectLocal(newId, filepath.Base(existing.ShellPath), existing.ShellPath, "")
		if err != nil {
			return "", err
		}
		return newId, nil
	}

	return a.sshManager.OpenTerminal(sessionId)
}

// WriteTerminal sends input to the SSH PTY (fallback, primary path is WebSocket)
func (a *App) WriteTerminal(sessionId string, data string) {
	a.sshManager.WriteBytes(sessionId, []byte(data))
}

// ResizeTerminal resizes the SSH PTY
func (a *App) ResizeTerminal(sessionId string, cols, rows int) {
	a.sshManager.Resize(sessionId, cols, rows)
}

// GetLocalShells lists detected shells on the local system.
func (a *App) GetLocalShells() ([]string, error) {
	return a.sshManager.GetLocalShells()
}

// ListSerialPorts returns the list of available serial port names.
func (a *App) ListSerialPorts() ([]string, error) {
	return a.sshManager.ListSerialPorts()
}

// ConnectLocal spawns a local command process and pipes it to the WebSocket path.
func (a *App) ConnectLocal(sessionId string, name string, shellPath string, cwd string) error {
	return a.sshManager.ConnectLocal(sessionId, name, shellPath, cwd)
}

// ConnectSerial connects to a local serial port and pipes it to the WebSocket path.
func (a *App) ConnectSerial(sessionId string, name string, portName string, baudRate int, dataBits int, stopBits float64, parity string) error {
	return a.sshManager.ConnectSerial(sessionId, name, portName, baudRate, dataBits, stopBits, parity)
}

// SystemInfo retrieves basic system probe info
func (a *App) SystemInfo(sessionId string) (map[string]interface{}, error) {
	return a.sshManager.GetSystemInfo(sessionId)
}

// SystemInfoLite retrieves CPU, memory, disk, network, and uptime metrics for the big-screen view.
func (a *App) SystemInfoLite(sessionId string) (map[string]interface{}, error) {
	return a.sshManager.GetSystemInfoLite(sessionId)
}

// NetworkInfo retrieves full network probe info including connection details.
func (a *App) NetworkInfo(sessionId string) (map[string]interface{}, error) {
	return a.sshManager.GetNetworkInfo(sessionId)
}

// GetServerStaticInfo retrieves static server info (OS/timezone/hostname/CPU model)
func (a *App) GetServerStaticInfo(sessionId string) (map[string]interface{}, error) {
	return a.sshManager.GetServerStaticInfo(sessionId)
}

// GetFullProcessList retrieves all process list from the remote server
func (a *App) GetFullProcessList(sessionId string) ([]map[string]interface{}, error) {
	return a.sshManager.GetFullProcessList(sessionId)
}

// KillProcess kills a process by PID on the remote server
func (a *App) KillProcess(sessionId string, pid string) error {
	return a.sshManager.KillProcess(sessionId, pid)
}

// GetProcessEnv returns environment variables of a remote process
func (a *App) GetProcessEnv(sessionId string, pid string) ([]string, error) {
	return a.sshManager.GetProcessEnv(sessionId, pid)
}

// GetTerminalCwd retrieves current working directory of the shell
func (a *App) GetTerminalCwd(sessionId string) (string, error) {
	return a.sshManager.GetTerminalCwd(sessionId)
}

// GetSSHChannelUsage 返回当前会话占用的 SSH 通道数量明细
func (a *App) GetSSHChannelUsage(sessionId string) map[string]interface{} {
	return a.sshManager.GetSSHChannelUsage(sessionId)
}

// ListDir lists directory contents via SFTP
func (a *App) ListDir(sessionId string, path string) ([]map[string]interface{}, error) {
	return a.sshManager.ListDir(sessionId, path)
}

// ReadFile reads a file's content via SFTP
func (a *App) ReadFile(sessionId string, path string) (string, error) {
	return a.sshManager.ReadFile(sessionId, path)
}

// WriteFile writes content to a file via SFTP
func (a *App) WriteFile(sessionId string, path string, content string) error {
	return a.sshManager.WriteFile(sessionId, path, content)
}

// DeleteItem deletes a file or directory via SFTP
func (a *App) DeleteItem(sessionId string, path string, isDir bool) error {
	return a.sshManager.DeleteItem(sessionId, path, isDir)
}

// DeleteItemShell 用 rm -rf 删除
func (a *App) DeleteItemShell(sessionId string, path string) error {
	return a.sshManager.DeleteItemShell(sessionId, path)
}

// BatchDeleteItemShell 用一条rm -rf path1 path2 ... 批量删除
func (a *App) BatchDeleteItemShell(sessionId string, paths []string) error {
	return a.sshManager.BatchDeleteItemShell(sessionId, paths)
}

// Mkdir creates a directory via SFTP
func (a *App) Mkdir(sessionId string, path string) error {
	return a.sshManager.Mkdir(sessionId, path)
}

// RenameItem renames a file or directory via SFTP
func (a *App) RenameItem(sessionId string, oldPath string, newPath string) error {
	return a.sshManager.RenameItem(sessionId, oldPath, newPath)
}

// CopyItem copies a file or directory within the same server (server-local cp -a).
func (a *App) CopyItem(sessionId string, srcPath string, dstPath string) error {
	return a.sshManager.CopyItem(sessionId, srcPath, dstPath)
}

// MoveItem moves a file or directory within the same server (server-local mv).
func (a *App) MoveItem(sessionId string, srcPath string, dstPath string) error {
	return a.sshManager.MoveItem(sessionId, srcPath, dstPath)
}

// GetChmodDialogSettings returns remembered chmod dialog preferences
func (a *App) GetChmodDialogSettings() map[string]interface{} {
	return a.configManager.GetChmodDialogSettings()
}

func (a *App) GetFileManagerSettings() map[string]interface{} {
	return a.configManager.GetFileManagerSettings()
}

func (a *App) SaveTransferTuningSettings(maxPacketKiB int, maxRequestsPerFile int, concurrentWrites bool, applyToSharedClient bool) error {
	if err := a.configManager.SaveTransferTuningSettings(maxPacketKiB, maxRequestsPerFile, concurrentWrites, applyToSharedClient); err != nil {
		return err
	}
	a.sshManager.ApplyTransferTuning(a.configManager.GetTransferTuningSettings())
	return nil
}

// SaveChmodDialogSettings persists chmod dialog preferences
func (a *App) SaveChmodDialogSettings(mode string, includeSubdirectories bool) error {
	return a.configManager.SaveChmodDialogSettings(mode, includeSubdirectories)
}

func (a *App) SetChmodAutoApplyLastSettings(enabled bool) error {
	return a.configManager.SetChmodAutoApplyLastSettings(enabled)
}

func (a *App) SetFileManagerDoubleClickUncompressArchive(enabled bool) error {
	return a.configManager.SetFileManagerDoubleClickUncompressArchive(enabled)
}

func (a *App) SetFileManagerAutoRefreshDisabled(disabled bool) error {
	return a.configManager.SetFileManagerAutoRefreshDisabled(disabled)
}

func (a *App) SetFileManagerSmartUncompressConflictStrategy(strategy string) error {
	return a.configManager.SetFileManagerSmartUncompressConflictStrategy(strategy)
}

func (a *App) ListOwnershipCandidates(sessionId string) (sshmanager.OwnershipCandidates, error) {
	return a.sshManager.ListOwnershipCandidates(sessionId)
}

func (a *App) GetPathOwnership(sessionId string, path string) (sshmanager.PathOwnershipInfo, error) {
	return a.sshManager.GetPathOwnership(sessionId, path)
}

func (a *App) ResolveDirectoryPath(sessionId string, path string) (string, error) {
	return a.sshManager.ResolveDirectoryPath(sessionId, path)
}

func (a *App) ChownFile(sessionId string, path string, owner string, group string, recursive bool) error {
	return a.sshManager.ChownFile(sessionId, path, owner, group, recursive)
}

// ChmodFile changes file permissions via SFTP or recursively via chmod -R
func (a *App) ChmodFile(sessionId string, path string, mode string, recursive bool) error {
	return a.sshManager.ChmodFile(sessionId, path, mode, recursive)
}

// CompressItem archives a file or directory on the remote server
func (a *App) CompressItem(sessionId string, remotePath string) error {
	return a.sshManager.CompressItem(sessionId, remotePath)
}

// PreviewSmartUncompressItem previews smart extract destination without modifying files.
func (a *App) PreviewSmartUncompressItem(sessionId string, remotePath string) (map[string]interface{}, error) {
	return a.sshManager.PreviewSmartUncompressItem(sessionId, remotePath)
}

// UncompressItemWithStrategy extracts an archive on the remote server using a caller-provided conflict strategy.
func (a *App) UncompressItemWithStrategy(sessionId string, remotePath string, strategy string) error {
	return a.sshManager.UncompressItemWithStrategy(sessionId, remotePath, strategy)
}

// UncompressItem extracts an archive on the remote server using the saved smart extract strategy.
func (a *App) UncompressItem(sessionId string, remotePath string) error {
	strategy := "auto_rename"
	if a != nil && a.configManager != nil {
		settings := a.configManager.GetFileManagerSettings()
		if configuredStrategy, ok := settings["smartUncompressConflictStrategy"].(string); ok {
			strategy = config.NormalizeFileManagerSmartUncompressConflictStrategy(configuredStrategy)
		}
	}
	return a.sshManager.UncompressItemWithStrategy(sessionId, remotePath, strategy)
}

func (a *App) InstallUnzip(sessionId string) error {
	return a.sshManager.InstallUnzip(sessionId)
}

// UploadLocalFile uploads a local file to a remote directory (no dialog)
func (a *App) UploadLocalFile(sessionId string, localFile string, remoteDir string) error {
	return a.sshManager.UploadFile(sessionId, localFile, remoteDir)
}

// UploadLocalDir recursively uploads a local directory to a remote directory (no dialog)
func (a *App) UploadLocalDir(sessionId string, localDir string, remoteDir string) error {
	return a.sshManager.UploadDir(sessionId, localDir, remoteDir)
}

// UploadFileContent uploads file content from memory to a remote path
func (a *App) UploadFileContent(sessionId string, fileName string, remoteDir string, content []byte) error {
	return a.sshManager.UploadFileContent(sessionId, fileName, remoteDir, content)
}

// UploadFileContentBase64 通过 base64 编码上传文件内容，避免前端内存爆炸
func (a *App) UploadFileContentBase64(sessionId string, fileName string, remoteDir string, base64Content string) error {
	return a.sshManager.UploadFileContentBase64(sessionId, fileName, remoteDir, base64Content)
}

func (a *App) BeginChunkedUploadTask(sessionId string, remoteDir string, maxClients int) (string, error) {
	return a.sshManager.BeginChunkedUploadTask(sessionId, remoteDir, maxClients)
}

func (a *App) BeginChunkedUploadFile(taskID string, relativePath string, size int64, totalChunks int) (string, error) {
	return a.sshManager.BeginChunkedUploadFile(taskID, relativePath, size, totalChunks)
}

func (a *App) UploadChunkBase64(taskID string, fileID string, chunkIndex int, offset int64, base64Content string) error {
	return a.sshManager.UploadChunkBase64(taskID, fileID, chunkIndex, offset, base64Content)
}

func (a *App) CompleteChunkedUploadFile(taskID string, fileID string) error {
	return a.sshManager.CompleteChunkedUploadFile(taskID, fileID)
}

func (a *App) AbortChunkedUploadFile(taskID string, fileID string) error {
	return a.sshManager.AbortChunkedUploadFile(taskID, fileID)
}

func (a *App) FinishChunkedUploadTask(taskID string) error {
	return a.sshManager.FinishChunkedUploadTask(taskID)
}

func (a *App) AbortChunkedUploadTask(taskID string) error {
	return a.sshManager.AbortChunkedUploadTask(taskID)
}

// UploadFile opens a file dialog to select a local file and uploads it to the remote path
func (a *App) UploadFile(sessionId string, remotePath string) error {
	filepaths, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select File to Upload",
	})
	if err != nil || filepaths == "" {
		return err
	}
	return a.sshManager.UploadFile(sessionId, filepaths, remotePath)
}

func (a *App) SelectUploadFiles() ([]string, error) {
	return runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Files to Upload",
	})
}

func (a *App) SelectUploadDirectory() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Folder to Upload",
	})
}

func (a *App) SelectDownloadFilePath(remotePath string, defaultDir string) (string, error) {
	filename := filepath.Base(remotePath)
	options := runtime.SaveDialogOptions{
		Title:           "Save File",
		DefaultFilename: filename,
	}
	defaultDirectory := resolveDownloadDefaultDirectory(defaultDir)
	if defaultDirectory != "" {
		_ = os.MkdirAll(defaultDirectory, 0o755)
		options.DefaultDirectory = defaultDirectory
	}
	return runtime.SaveFileDialog(a.ctx, options)
}

func (a *App) SelectDownloadDirectory(defaultDir string) (string, error) {
	options := runtime.OpenDialogOptions{
		Title: "Select Download Directory",
	}
	defaultDirectory := resolveDownloadDefaultDirectory(defaultDir)
	if defaultDirectory != "" {
		_ = os.MkdirAll(defaultDirectory, 0o755)
		options.DefaultDirectory = defaultDirectory
	}
	return runtime.OpenDirectoryDialog(a.ctx, options)
}

func (a *App) UploadLocalPathsCompressed(sessionId string, uploadID string, maxConcurrent int, localPaths []string, remoteDir string) error {
	return a.sshManager.UploadLocalPathsCompressed(sessionId, uploadID, maxConcurrent, localPaths, remoteDir)
}

func (a *App) AutoRepairCompressedUploadTargets(sessionId string, localPaths []string, remoteDir string) error {
	return a.sshManager.AutoRepairCompressedUploadTargets(sessionId, localPaths, remoteDir)
}

func (a *App) AbortCompressedUpload(sessionId string) error {
	return a.sshManager.AbortCompressedUpload(sessionId)
}

func getProgramDirectory() string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exePath)
}

func (a *App) ensureMainLivenessLock() error {
	if a == nil || a.configManager == nil {
		return fmt.Errorf("config manager unavailable")
	}
	if a.mainLivenessLockRelease != nil && strings.TrimSpace(a.mainLivenessLockPath) != "" {
		return nil
	}
	lockPath := filepath.Join(a.configManager.GetConfigDir(), "lumeterm-main.lock")
	release, err := platformruntime.AcquireMainLivenessLock(lockPath)
	if err != nil {
		return err
	}
	a.mainLivenessLockPath = lockPath
	a.mainLivenessLockRelease = release
	return nil
}

func (a *App) releaseMainLivenessLock() {
	if a == nil || a.mainLivenessLockRelease == nil {
		return
	}
	a.mainLivenessLockRelease()
	a.mainLivenessLockRelease = nil
}

func resolveDownloadDefaultDirectory(template string) string {
	programDir := getProgramDirectory()
	trimmed := strings.TrimSpace(template)
	if trimmed == "" {
		trimmed = "${APP_DIR}\\download"
	}
	resolved := strings.ReplaceAll(trimmed, "${APP_DIR}", programDir)
	resolved = strings.ReplaceAll(resolved, "%APP_DIR%", programDir)
	resolved = strings.ReplaceAll(resolved, "\\", string(os.PathSeparator))
	resolved = strings.ReplaceAll(resolved, "/", string(os.PathSeparator))
	if !filepath.IsAbs(resolved) && programDir != "" {
		resolved = filepath.Join(programDir, resolved)
	}
	absolutePath, err := filepath.Abs(resolved)
	if err == nil {
		resolved = absolutePath
	}
	return filepath.Clean(resolved)
}

func resolveDownloadBasePath(remotePath string, defaultDir string, isDirectory bool) string {
	defaultDirectory := resolveDownloadDefaultDirectory(defaultDir)
	if defaultDirectory == "" {
		return ""
	}
	baseName := filepath.Base(strings.TrimSpace(remotePath))
	if isDirectory {
		baseName = transfer.RemoteDownloadBaseName(remotePath)
	}
	return filepath.Join(defaultDirectory, baseName)
}

func resolveDownloadLocalPath(localPath string, isDirectory bool, optionsJSON string) string {
	cleaned := filepath.Clean(strings.TrimSpace(localPath))
	if cleaned == "" {
		return ""
	}
	options := transfer.ParseDownloadConflictOptions(optionsJSON)
	if options.StrategyFor(".") != transfer.DownloadConflictStrategyAutoRename {
		return cleaned
	}
	if _, err := os.Stat(cleaned); os.IsNotExist(err) {
		return cleaned
	}
	renamedPath, err := transfer.BuildDownloadRenamedPath(cleaned, options.RenameSuffixMode, isDirectory)
	if err != nil {
		return cleaned
	}
	return renamedPath
}

func resolveDownloadTargetPath(remotePath string, defaultDir string, isDirectory bool, optionsJSON string) string {
	basePath := resolveDownloadBasePath(remotePath, defaultDir, isDirectory)
	if basePath == "" {
		return ""
	}
	return resolveDownloadLocalPath(basePath, isDirectory, optionsJSON)
}

func (a *App) GetProgramDirectory() string {
	return getProgramDirectory()
}

func (a *App) GetThemePackagesDirectory() (string, error) {
	return a.configManager.GetThemePackagesDirectory()
}

func (a *App) GetThemePackageSettings() map[string]interface{} {
	return config.ThemePackageSettingsToMap(a.configManager.GetThemePackageSettings())
}

func (a *App) SaveThemePackageSettings(payload map[string]string) error {
	return a.configManager.SaveThemePackageSettings(config.ThemePackageSettings{
		ThemeMode:           payload["themeMode"],
		LightThemePackageID: payload["lightThemePackageId"],
		DarkThemePackageID:  payload["darkThemePackageId"],
	})
}

func (a *App) ListThemePackages() ([]map[string]interface{}, error) {
	items, err := a.configManager.ListThemePackages()
	if err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		result = append(result, config.ThemePackageSummaryToMap(item))
	}
	return result, nil
}

func (a *App) SelectThemePackageFiles() ([]string, error) {
	return runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择主题包文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "主题包 (*.json)", Pattern: "*.json"},
		},
	})
}

func (a *App) ImportThemePackageFiles(paths []string) ([]map[string]interface{}, error) {
	items, err := a.configManager.ImportThemePackageFiles(paths)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		result = append(result, config.ThemePackageSummaryToMap(item))
	}
	return result, nil
}

func (a *App) DeleteThemePackage(themeID string) error {
	return a.configManager.DeleteThemePackage(themeID)
}

func (a *App) CopyThemePackageToMode(themeID string, targetMode string) (map[string]interface{}, error) {
	item, err := a.configManager.CopyThemePackageToMode(themeID, targetMode)
	if err != nil {
		return nil, err
	}
	return config.ThemePackageSummaryToMap(item), nil
}

func getGitHubContributorsOnce(client *http.Client) ([]GitHubContributor, error) {
	request, err := http.NewRequest(http.MethodGet, githubContributorsAPIURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "LumeTerm")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	request.Header.Set("Referer", githubContributorsRefererURL)

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github contributors request failed: %s", response.Status)
	}

	var contributors []GitHubContributor
	if err := json.NewDecoder(response.Body).Decode(&contributors); err != nil {
		return nil, err
	}
	if contributors == nil {
		return []GitHubContributor{}, nil
	}
	return contributors, nil
}

func (a *App) GetGitHubContributors() ([]GitHubContributor, error) {
	client := &http.Client{Timeout: githubContributorsRequestTimeout}
	var lastErr error
	for attempt := 0; attempt <= githubContributorsMaxRetries; attempt++ {
		contributors, err := getGitHubContributorsOnce(client)
		if err == nil {
			return contributors, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("github contributors request failed")
	}
	return nil, lastErr
}

func (a *App) ListProgramFonts() ([]programfonts.ProgramFontInfo, error) {
	return programfonts.ListFonts(getProgramDirectory())
}

func (a *App) SelectProgramFontFiles() ([]string, error) {
	return runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择字体文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "字体文件 (*.ttf;*.otf;*.ttc;*.woff;*.woff2)", Pattern: "*.ttf;*.otf;*.ttc;*.woff;*.woff2"},
		},
	})
}

func (a *App) ImportProgramFontFiles(paths []string) ([]programfonts.ProgramFontInfo, error) {
	fontsDirectory, err := programfonts.EnsureDir(getProgramDirectory())
	if err != nil {
		return nil, err
	}
	imported := make([]programfonts.ProgramFontInfo, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		fontInfo, copyErr := programfonts.CopyFile(path, fontsDirectory)
		if copyErr != nil {
			return nil, copyErr
		}
		if seen[fontInfo.FileName] {
			continue
		}
		seen[fontInfo.FileName] = true
		imported = append(imported, fontInfo)
	}
	return imported, nil
}

func (a *App) DeleteProgramFont(fileName string) error {
	return programfonts.DeleteFile(fileName, getProgramDirectory())
}

func (a *App) GetProgramFontDataURL(fileName string) (string, error) {
	return programfonts.DataURL(fileName, getProgramDirectory())
}

func (a *App) ResolveDownloadPath(remotePath string, defaultDir string, isDirectory bool, optionsJSON string) string {
	return resolveDownloadTargetPath(remotePath, defaultDir, isDirectory, optionsJSON)
}

func (a *App) ResolveDownloadLocalPath(localPath string, isDirectory bool, optionsJSON string) string {
	return resolveDownloadLocalPath(localPath, isDirectory, optionsJSON)
}

func (a *App) PreviewDownloadConflicts(sessionId string, remotePath string, localPath string, isDirectory bool) ([]map[string]interface{}, error) {
	return a.sshManager.PreviewDownloadConflicts(sessionId, remotePath, localPath, isDirectory)
}

func (a *App) DownloadFile(sessionId string, remotePath string, defaultDir string) error {
	localPath := resolveDownloadTargetPath(remotePath, defaultDir, false, "")
	if localPath == "" {
		return fmt.Errorf("failed to resolve download path")
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	return a.sshManager.DownloadFile(sessionId, remotePath, localPath)
}

func (a *App) DownloadFileToLocal(sessionId string, downloadID string, remotePath string, localPath string, optionsJSON string) error {
	return a.sshManager.DownloadFileToLocal(sessionId, downloadID, remotePath, localPath, optionsJSON)
}

func (a *App) DownloadDirectoryToLocal(sessionId string, downloadID string, remotePath string, localDir string, optionsJSON string) error {
	return a.sshManager.DownloadDirectoryToLocal(sessionId, downloadID, remotePath, localDir, optionsJSON)
}

func (a *App) DownloadDirectoryCompressed(sessionId string, downloadID string, remotePath string, localDir string, optionsJSON string) error {
	return a.sshManager.DownloadDirectoryCompressed(sessionId, downloadID, remotePath, localDir, optionsJSON)
}

func (a *App) AbortDownloadTransfer(identifier string) error {
	return a.sshManager.AbortDownloadTransfer(identifier)
}

func (a *App) OpenLocalPathInExplorer(localPath string, isDirectory bool) error {
	return localopen.Reveal(localPath, isDirectory)
}

// ReadPrivateKeyFile opens a file dialog to read a private key file
func (a *App) ReadPrivateKeyFile() (string, error) {
	keyPath, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择私钥文件",
	})
	if err != nil || keyPath == "" {
		return "", err
	}
	content, err := os.ReadFile(keyPath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// WebDAV Methods
func (a *App) GetWebdavConfig() map[string]string {
	return a.configManager.GetWebdavConfig()
}

func (a *App) SaveWebdavConfig(config map[string]string) error {
	return a.configManager.SaveWebdavConfig(config)
}

func (a *App) TestWebdavConnection(url, username, password string) error {
	return a.configManager.TestWebdavConnection(url, username, password)
}

func (a *App) BackupToWebdav() (map[string]interface{}, error) {
	return a.configManager.BackupToWebdav()
}

func (a *App) ListWebdavBackups() ([]map[string]interface{}, error) {
	return a.configManager.ListWebdavBackups()
}

func (a *App) RestoreFromWebdavFile(filename string) (map[string]interface{}, error) {
	return a.configManager.RestoreFromWebdavFile(filename)
}

func (a *App) RestoreFromWebdavFileWithPassword(filename string, password string) (map[string]interface{}, error) {
	return a.configManager.RestoreFromWebdavFileWithPassword(filename, password)
}

func (a *App) SyncFromWebdav() (map[string]interface{}, error) {
	return a.configManager.SyncFromWebdav()
}

// R2 Methods
func (a *App) GetR2Config() map[string]interface{} {
	conf := a.configManager.GetR2Config()
	if conf == nil {
		return nil
	}
	return map[string]interface{}{
		"accessKeyId":     conf.AccessKeyID,
		"secretAccessKey": conf.SecretAccessKey,
		"bucket":          conf.Bucket,
		"endpoint":        conf.Endpoint,
		"region":          conf.Region,
		"prefix":          conf.Prefix,
		"maxBackups":      conf.MaxBackups,
	}
}

func (a *App) SaveR2Config(config map[string]string) error {
	return a.configManager.SaveR2Config(config)
}

func (a *App) TestR2Connection(accessKeyId, secretAccessKey, bucket, endpoint string) error {
	return a.configManager.TestR2Connection(accessKeyId, secretAccessKey, bucket, endpoint)
}

func (a *App) BackupToR2() (map[string]interface{}, error) {
	return a.configManager.BackupToR2()
}

func (a *App) ListR2Backups() ([]map[string]interface{}, error) {
	return a.configManager.ListR2Backups()
}

func (a *App) RestoreFromR2File(objectKey string) (map[string]interface{}, error) {
	return a.configManager.RestoreFromR2File(objectKey)
}

func (a *App) RestoreFromR2FileWithPassword(objectKey string, password string) (map[string]interface{}, error) {
	return a.configManager.RestoreFromR2FileWithPassword(objectKey, password)
}

func (a *App) SyncFromR2() (map[string]interface{}, error) {
	return a.configManager.SyncFromR2()
}

// SyncMode methods
func (a *App) GetSyncMode() string {
	return a.configManager.GetSyncMode()
}

func (a *App) SetSyncMode(mode string) error {
	return a.configManager.SetSyncMode(mode)
}

func (a *App) GetAutoSyncEnabled() bool {
	return a.configManager.GetAutoSyncEnabled()
}

func (a *App) GetLastSyncTime() int64 {
	if a == nil || a.configManager == nil {
		return 0
	}
	return a.configManager.LoadLastSyncTimeMax()
}

// GetSyncTombstoneStats 返回本地同步删除记录条数。
func (a *App) GetSyncTombstoneStats() config.SyncTombstoneStats {
	if a == nil || a.configManager == nil {
		return config.SyncTombstoneStats{}
	}
	return a.configManager.GetSyncTombstoneStats()
}

// PruneSyncTombstones 按天数清理删除记录并上传到已配置云端。
// days <= 0 表示清理全部。
func (a *App) PruneSyncTombstones(days int) (config.SyncTombstonePruneResult, error) {
	if a == nil || a.configManager == nil {
		return config.SyncTombstonePruneResult{}, fmt.Errorf("配置未初始化")
	}
	return a.configManager.PruneSyncTombstones(days)
}

// PreviewTombstoneConflicts 合并同步前：先读目标云，列出本地墓碑将删掉的远端项。
func (a *App) PreviewTombstoneConflicts() (config.TombstoneConflictPreview, error) {
	if a == nil || a.configManager == nil {
		return config.TombstoneConflictPreview{}, fmt.Errorf("配置未初始化")
	}
	return a.configManager.PreviewTombstoneConflicts()
}

// ClearTombstoneConflicts 放弃删除意图（保留云端项）后应再执行合并同步。
func (a *App) ClearTombstoneConflicts(connectionIDs []string, credentialIDs []string) {
	if a == nil || a.configManager == nil {
		return
	}
	a.configManager.ClearTombstoneConflicts(connectionIDs, credentialIDs)
}

func (a *App) SetAutoSyncEnabled(enabled bool) error {
	return a.configManager.SetAutoSyncEnabled(enabled)
}

// RecoveryPassword methods
func (a *App) GetRecoveryPassword() string {
	return a.configManager.GetRecoveryPassword()
}

func (a *App) SetRecoveryPassword(password string) error {
	return a.configManager.SetRecoveryPassword(password)
}

func (a *App) ChangeRecoveryPassword(newPassword string) error {
	return a.configManager.ChangeRecoveryPassword(newPassword)
}

func (a *App) ResetRecoveryPassword(newPassword string) error {
	return a.configManager.ResetRecoveryPassword(newPassword)
}

func (a *App) SyncWithRecoveryPassword(password string) (map[string]interface{}, error) {
	return a.configManager.SyncWithRecoveryPassword(password)
}

func (a *App) GetProxyNodes() []ai.AIProxyNode {
	if a == nil || a.configManager == nil {
		return []ai.AIProxyNode{}
	}
	return a.configManager.GetAIProxyNodes()
}

func (a *App) SaveProxyNodes(jsonStr string) error {
	if a == nil || a.configManager == nil {
		return nil
	}
	var nodes []ai.AIProxyNode
	if strings.TrimSpace(jsonStr) != "" {
		if err := json.Unmarshal([]byte(jsonStr), &nodes); err != nil {
			return err
		}
	}
	if err := a.configManager.SaveAIProxyNodes(nodes); err != nil {
		return err
	}
	a.configManager.BumpSnapshotTime()
	go a.configManager.AutoSync()
	return nil
}

func (a *App) SyncAllProviders() (map[string]interface{}, error) {
	return a.configManager.SyncAllProviders()
}

// FTP Methods
func (a *App) GetFTPConfig() map[string]interface{} {
	conf := a.configManager.GetFTPConfig()
	if conf == nil {
		return nil
	}
	return map[string]interface{}{
		"mode":       conf.Mode,
		"host":       conf.Host,
		"port":       conf.Port,
		"username":   conf.Username,
		"password":   conf.Password,
		"remoteDir":  conf.RemoteDir,
		"maxBackups": conf.MaxBackups,
	}
}

func (a *App) SaveFTPConfig(config map[string]string) error {
	return a.configManager.SaveFTPConfig(config)
}

func (a *App) TestFTPConnection(host string, port int, username, password, mode string) (*config.FTPConnectionTestResult, error) {
	return a.configManager.TestFTPConnection(host, port, username, password, mode)
}

func (a *App) TestFTPConnectionWithCertificateApproval(host string, port int, username, password, mode, approvedFingerprint, expectedPinnedFingerprint string) (*config.FTPConnectionTestResult, error) {
	return a.configManager.TestFTPConnectionWithCertificateApproval(host, port, username, password, mode, approvedFingerprint, expectedPinnedFingerprint)
}

func (a *App) BackupToFTP() (map[string]interface{}, error) {
	return a.configManager.BackupToFTP()
}

func (a *App) ListFTPBackups() ([]map[string]interface{}, error) {
	return a.configManager.ListFTPBackups()
}

func (a *App) RestoreFromFTPFile(filename string) (map[string]interface{}, error) {
	return a.configManager.RestoreFromFTPFile(filename)
}

func (a *App) RestoreFromFTPFileWithPassword(filename string, password string) (map[string]interface{}, error) {
	return a.configManager.RestoreFromFTPFileWithPassword(filename, password)
}

func (a *App) SyncFromFTP() (map[string]interface{}, error) {
	return a.configManager.SyncFromFTP()
}

// SFTP Methods
func (a *App) GetSFTPConfig() map[string]interface{} {
	conf := a.configManager.GetSFTPConfig()
	if conf == nil {
		return nil
	}
	return map[string]interface{}{
		"host":       conf.Host,
		"port":       conf.Port,
		"username":   conf.Username,
		"authMethod": conf.AuthMethod,
		"password":   conf.Password,
		"privateKey": conf.PrivateKey,
		"remoteDir":  conf.RemoteDir,
		"maxBackups": conf.MaxBackups,
	}
}

func (a *App) SaveSFTPConfig(config map[string]string) error {
	return a.configManager.SaveSFTPConfig(config)
}

func (a *App) TestSFTPConnection(host string, port int, username, password, authMethod, privateKey, passphrase string) (*config.SFTPConnectionTestResult, error) {
	return a.configManager.TestSFTPConnection(host, port, username, password, authMethod, privateKey, passphrase)
}

func (a *App) TestSFTPConnectionWithHostKeyApproval(host string, port int, username, password, authMethod, privateKey, passphrase, approvedFingerprint string) (*config.SFTPConnectionTestResult, error) {
	return a.configManager.TestSFTPConnectionWithHostKeyApproval(host, port, username, password, authMethod, privateKey, passphrase, approvedFingerprint)
}

func (a *App) BackupToSFTP() (map[string]interface{}, error) {
	return a.configManager.BackupToSFTP()
}

func (a *App) ListSFTPBackups() ([]map[string]interface{}, error) {
	return a.configManager.ListSFTPBackups()
}

func (a *App) RestoreFromSFTPFile(filename string) (map[string]interface{}, error) {
	return a.configManager.RestoreFromSFTPFile(filename)
}

func (a *App) RestoreFromSFTPFileWithPassword(filename string, password string) (map[string]interface{}, error) {
	return a.configManager.RestoreFromSFTPFileWithPassword(filename, password)
}

func (a *App) SyncFromSFTP() (map[string]interface{}, error) {
	return a.configManager.SyncFromSFTP()
}

// RetrySync 手动重试云端同步，返回空字符串表示成功，非空为错误信息
func (a *App) RetrySync() string {
	return a.configManager.RetrySync()
}

// EnsureRemoteDirAndRetrySync 先重建远端同步目录再重试同步（目录 404/被删时使用）
func (a *App) EnsureRemoteDirAndRetrySync() string {
	return a.configManager.EnsureRemoteDirAndRetrySync()
}

// GetQuickCommands 获取快捷命令列表
func (a *App) GetQuickCommands() string {
	return a.configManager.GetQuickCommands()
}

// SaveQuickCommands 保存快捷命令列表
func (a *App) SaveQuickCommands(jsonStr string) error {
	return a.configManager.SaveQuickCommands(jsonStr)
}

// SaveQuickCommandsLocal 保存快捷命令列表到本地，不触发云端同步
func (a *App) SaveQuickCommandsLocal(jsonStr string) error {
	return a.configManager.SaveQuickCommandsLocal(jsonStr)
}

func (a *App) GetRememberWorkspace() bool {
	return a.configManager.GetRememberWorkspace()
}

func (a *App) SetRememberWorkspace(enabled bool) error {
	return a.configManager.SetRememberWorkspace(enabled)
}

func (a *App) GetWorkspacePersistenceLevel() string {
	return a.configManager.GetWorkspacePersistenceLevel()
}

func (a *App) SetWorkspacePersistenceLevel(level string) error {
	return a.configManager.SetWorkspacePersistenceLevel(level)
}

func (a *App) SupportsWebviewGpuDisable() bool {
	return goruntime.GOOS == "windows"
}

func (a *App) GetWebviewGpuDisabled() bool {
	if !a.SupportsWebviewGpuDisable() || a.configManager == nil {
		return false
	}
	return a.configManager.GetWebviewGpuDisabled()
}

func (a *App) SetWebviewGpuDisabled(enabled bool) error {
	if !a.SupportsWebviewGpuDisable() || a.configManager == nil {
		return fmt.Errorf("current platform does not support disabling webview gpu acceleration")
	}
	return a.configManager.SetWebviewGpuDisabled(enabled)
}

// GetTasksDir 返回当前 AI 对话存储目录（自定义路径或默认 configDir/tasks）
func (a *App) GetTasksDir() string {
	if a == nil || a.configManager == nil {
		return ""
	}
	tasksDir := a.configManager.GetTasksDir()
	if tasksDir != "" {
		return tasksDir
	}
	return filepath.Join(a.configManager.GetConfigDir(), "tasks")
}

// IsCustomTasksDir 返回是否使用了自定义 AI 对话存储目录
func (a *App) IsCustomTasksDir() bool {
	if a == nil || a.configManager == nil {
		return false
	}
	tasksDir := a.configManager.GetTasksDir()
	if tasksDir == "" {
		return false
	}
	// 存储路径与默认路径相同时不算自定义
	defaultDir := filepath.Join(a.configManager.GetConfigDir(), "tasks")
	absTasks, err1 := filepath.Abs(tasksDir)
	absDefault, err2 := filepath.Abs(defaultDir)
	if err1 != nil || err2 != nil {
		return true // 无法比较时按自定义处理
	}
	return absTasks != absDefault
}

// SelectTasksDirectory 弹出目录选择对话框，返回用户选择的目录路径
func (a *App) SelectTasksDirectory() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择 AI 对话存储目录",
	})
}

// MigrateAITasksDir 将 AI 对话数据迁移到目标目录
func (a *App) MigrateAITasksDir(targetDir string) error {
	if a == nil || a.configManager == nil {
		return fmt.Errorf("config manager unavailable")
	}
	return a.configManager.MigrateAITasksDir(targetDir)
}

// ResetTasksDir 重置为默认目录（迁移数据后清除自定义路径设置）
func (a *App) ResetTasksDir() error {
	if a == nil || a.configManager == nil {
		return fmt.Errorf("config manager unavailable")
	}
	defaultDir := filepath.Join(a.configManager.GetConfigDir(), "tasks")
	if err := a.configManager.MigrateAITasksDir(defaultDir); err != nil {
		return err
	}
	// 迁移成功后清除自定义路径，使其回到默认
	return a.configManager.SetTasksDir("")
}

func (a *App) GetRuntimeEnvironmentSettings() runtimeenv.Settings {
	if a == nil || a.configManager == nil {
		return runtimeenv.DefaultSettings()
	}
	return a.configManager.GetRuntimeEnvironmentSettings()
}

func (a *App) SaveRuntimeEnvironmentSettings(jsonStr string) error {
	if a == nil || a.configManager == nil {
		return nil
	}
	settings := runtimeenv.DefaultSettings()
	if strings.TrimSpace(jsonStr) != "" {
		if err := json.Unmarshal([]byte(jsonStr), &settings); err != nil {
			return err
		}
	}
	return a.configManager.SaveRuntimeEnvironmentSettings(settings)
}

func (a *App) GetRuntimeEnvironmentStatus() runtimeenv.Status {
	settings := a.GetRuntimeEnvironmentSettings()
	return runtimeenv.DetectStatus(getProgramDirectory(), settings)
}

func (a *App) InstallRuntimeEnvironment(language string) (runtimeenv.Status, error) {
	settings := a.GetRuntimeEnvironmentSettings()
	return runtimeinstaller.InstallRuntimeEnvironment(getProgramDirectory(), settings, language)
}

func (a *App) GetLiveWorkspaceState() string {
	if a == nil {
		return ""
	}
	a.liveWorkspaceStateMu.RLock()
	defer a.liveWorkspaceStateMu.RUnlock()
	return a.liveWorkspaceState
}

func (a *App) SetLiveWorkspaceState(jsonStr string) error {
	if a == nil {
		return nil
	}
	trimmed := strings.TrimSpace(jsonStr)
	if trimmed != "" && !json.Valid([]byte(trimmed)) {
		return fmt.Errorf("invalid live workspace state")
	}
	a.liveWorkspaceStateMu.Lock()
	a.liveWorkspaceState = trimmed
	a.liveWorkspaceStateMu.Unlock()
	return nil
}

func (a *App) GetWorkspaceState() string {
	if liveState := a.GetLiveWorkspaceState(); liveState != "" {
		return liveState
	}
	if a == nil || a.configManager == nil {
		return ""
	}
	return a.configManager.GetWorkspaceState()
}

func (a *App) SaveWorkspaceState(jsonStr string) error {
	err := a.configManager.SaveWorkspaceState(jsonStr)
	return err
}

func (a *App) GetWorkspaceSessionState(serverId string) string {
	return a.configManager.GetWorkspaceSessionState(serverId)
}

func (a *App) SaveWorkspaceSessionState(serverId string, jsonStr string) error {
	err := a.configManager.SaveWorkspaceSessionState(serverId, jsonStr)
	return err
}

func (a *App) ClearWorkspaceState() error {
	err := a.configManager.ClearWorkspaceState()
	return err
}

// GetParamHistory 获取参数历史
func (a *App) GetParamHistory() string {
	return a.configManager.GetParamHistory()
}

// SaveParamHistory 保存参数历史
func (a *App) SaveParamHistory(jsonStr string) error {
	return a.configManager.SaveParamHistory(jsonStr)
}

// GetCommandHistory 获取指定会话的命令历史
func (a *App) GetCommandHistory(sessionId string) string {
	return a.configManager.GetCommandHistory(sessionId)
}

// SaveCommandHistory 保存指定会话的命令历史
func (a *App) SaveCommandHistory(sessionId, jsonStr string) error {
	return a.configManager.SaveCommandHistory(sessionId, jsonStr)
}

// GetGlobalCommandHistory 获取全局命令历史
func (a *App) GetGlobalCommandHistory() string {
	return a.configManager.GetGlobalCommandHistory()
}

// SaveGlobalCommandHistory 保存全局命令历史
func (a *App) SaveGlobalCommandHistory(jsonStr string) error {
	return a.configManager.SaveGlobalCommandHistory(jsonStr)
}

// PingServer pings a server. mode 为延迟检测方式：auto / banner / tcp（见 ping.go normalizePingMode）。
func (a *App) PingServer(connId string, mode string) map[string]interface{} {
	conn, ok := a.configManager.GetConnectionByID(connId)
	if !ok {
		return map[string]interface{}{
			"online":  false,
			"latency": 0,
		}
	}
	resolvedConn, err := a.configManager.ResolveConnectionProxy(conn)
	if err != nil {
		return map[string]interface{}{
			"online":  false,
			"latency": 0,
		}
	}
	return ping.PingServer(resolvedConn, mode)
}

// UpdateApp downloads a platform update package, verifies it, and starts the
// platform-specific installation or executable replacement flow.
func (a *App) UpdateApp(downloadUrl string, filename string, proxyFirst bool) error {
	// 1. 强制 HTTPS + 仅允许 GitHub Release 资产下载地址
	if !strings.HasPrefix(downloadUrl, "https://") {
		return fmt.Errorf("更新地址必须使用 HTTPS")
	}
	if !platformupdate.IsAllowedDownloadURL(downloadUrl) {
		return fmt.Errorf("更新地址无效：仅允许 GitHub Release 安装包下载链接")
	}
	// Release asset names must not escape the temporary/download directory.
	filename = filepath.Base(strings.TrimSpace(filename))
	if !platformupdate.IsAllowedFilename(filename) {
		return fmt.Errorf("更新文件名无效或不受支持: %s", filename)
	}
	// 2. 下载新文件（带超时，防止慢网络永久阻塞）
	// ponytail: 对每个 URL 尝试完整的 下载→写入磁盘 流程，失败再试下一个。
	// 旧实现只在 Get() 阶段切换 URL，io.Copy 阶段超时直接放弃（"failed to save update file"），
	// 不会重试代理 URL。大文件 + 慢网络下直连极易在 body 读取阶段超时。
	client := updatedownload.NewHTTPClient()
	ghProxies := []string{"https://ghproxy.net/", "https://gh-proxy.com/", "https://proxy.gitwarp.top/"}
	var tryUrls []string
	if strings.Contains(downloadUrl, "github.com") {
		if proxyFirst {
			tryUrls = []string{ghProxies[0] + downloadUrl, ghProxies[1] + downloadUrl, ghProxies[2] + downloadUrl, downloadUrl}
		} else {
			tryUrls = []string{downloadUrl, ghProxies[0] + downloadUrl, ghProxies[1] + downloadUrl, ghProxies[2] + downloadUrl}
		}
	} else {
		tryUrls = []string{downloadUrl}
	}

	isDeb := strings.HasSuffix(strings.ToLower(filename), ".deb")
	isRpm := strings.HasSuffix(strings.ToLower(filename), ".rpm")
	isDmg := strings.HasSuffix(strings.ToLower(filename), ".dmg")
	isSetup := strings.Contains(strings.ToLower(filename), "installer") || strings.Contains(strings.ToLower(filename), "setup")
	var targetPath string
	var exePath string
	var needsElevated bool

	// 获取可执行文件路径，后续 deb 安装和便携版替换都可能需要
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine executable path: %w", err)
	}
	exePath = exe

	if isSetup || isDeb || isRpm || isDmg {
		// 安装包、.deb、.rpm 和 .dmg 都下载到临时目录
		targetPath = filepath.Join(os.TempDir(), filename)
	} else {
		// 便携版：下载到 exe 同级目录，或权限不足时降级到临时目录
		targetPath = exePath + ".update"

		// Test write access to exe directory upfront.
		// On Linux the app may be installed in /usr/bin/ where regular users
		// lack write permission, so we fall back to the temp directory.
		testFile, err := os.Create(targetPath)
		if err != nil {
			if os.IsPermission(err) {
				targetPath = filepath.Join(os.TempDir(), filepath.Base(exePath)+".update")
				needsElevated = true
			} else {
				return fmt.Errorf("could not create temporary update file: %w", err)
			}
		} else {
			testFile.Close()
			os.Remove(targetPath)
		}
	}

	downloader := updatedownload.Downloader{
		Client: client,
		Progress: func(progress float64) {
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "app-update-progress", progress)
			}
		},
	}
	var lastErr error
	var failedSources []string
	for i, u := range tryUrls {
		src := updatedownload.SourceLabel(u)
		err := downloader.Download(a.ctx, u, targetPath)
		if err != nil {
			_ = os.Remove(targetPath)
			failedSources = append(failedSources, src)
			lastErr = err
			if i+1 < len(tryUrls) {
				next := updatedownload.SourceLabel(tryUrls[i+1])
				fmt.Printf("[UpdateApp] %s 整源失败，换源 → %s: %v\n", src, next, err)
				// 换源时进度归零，避免接在上一源半成品后面
				downloader.Progress(0)
			} else {
				fmt.Printf("[UpdateApp] %s 整源失败，无更多源: %v\n", src, err)
			}
			continue
		}

		downloadUrl = u
		lastErr = nil
		break
	}
	if lastErr != nil {
		if len(failedSources) == 0 {
			return fmt.Errorf("更新下载失败: %w", lastErr)
		}
		return fmt.Errorf("所有下载源均失败（%s）: %w", strings.Join(failedSources, " → "), lastErr)
	}

	// 2.5 强制校验下载文件的 SHA256，校验失败不得继续安装/替换。
	shaResp, shaErr := client.Get(downloadUrl + ".sha256")
	if shaErr != nil {
		os.Remove(targetPath)
		return fmt.Errorf("failed to fetch .sha256 file: %w", shaErr)
	}
	defer shaResp.Body.Close()
	if shaResp.StatusCode != http.StatusOK {
		os.Remove(targetPath)
		return fmt.Errorf(".sha256 file returned %d", shaResp.StatusCode)
	}

	shaBody, shaReadErr := io.ReadAll(shaResp.Body)
	if shaReadErr != nil {
		os.Remove(targetPath)
		return fmt.Errorf("failed to read .sha256 file: %w", shaReadErr)
	}

	// .sha256 文件内容通常是 "<hash>  <filename>" 格式，取第一个字段。
	expectedHashFields := strings.Fields(strings.TrimSpace(string(shaBody)))
	if len(expectedHashFields) == 0 {
		os.Remove(targetPath)
		return fmt.Errorf("empty .sha256 file")
	}
	expectedHash := expectedHashFields[0]
	if len(expectedHash) != sha256.Size*2 {
		os.Remove(targetPath)
		return fmt.Errorf("invalid SHA256 length: %d", len(expectedHash))
	}
	if _, decodeErr := hex.DecodeString(expectedHash); decodeErr != nil {
		os.Remove(targetPath)
		return fmt.Errorf("invalid SHA256 hex: %w", decodeErr)
	}

	// 计算下载文件的 SHA256（流式读取，避免将整个安装包读入内存）
	f, openErr := os.Open(targetPath)
	if openErr != nil {
		os.Remove(targetPath)
		return fmt.Errorf("failed to open downloaded file for verification: %w", openErr)
	}
	h := sha256.New()
	if _, copyErr := io.Copy(h, f); copyErr != nil {
		f.Close()
		os.Remove(targetPath)
		return fmt.Errorf("failed to hash downloaded file: %w", copyErr)
	}
	f.Close()
	actualHashHex := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actualHashHex, expectedHash) {
		os.Remove(targetPath)
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expectedHash, actualHashHex)
	}

	// 3. 处理 .deb 包安装（Linux）
	if isDeb {
		if err := platformupdate.InstallDeb(targetPath); err != nil {
			return err
		}
		// dpkg -i 已替换 /usr/bin/lumeterm，重启为新版本
		if err := platformupdate.Restart(exePath); err != nil {
			return err
		}
		os.Exit(0)
		return nil
	}

	// 3.5 处理 .rpm 包安装（Linux）
	if isRpm {
		if err := platformupdate.InstallRPM(targetPath); err != nil {
			return err
		}
		if err := platformupdate.Restart(exePath); err != nil {
			return err
		}
		os.Exit(0)
		return nil
	}

	// 4. macOS DMG 由独立更新进程替换 .app，并在旧进程退出后重启。
	if isDmg {
		if err := platformupdate.InstallDMG(targetPath, exePath); err != nil {
			return err
		}
		os.Exit(0)
		return nil
	}

	// Windows 安装包交给系统安装器处理；其他文件走 Portable 替换。
	if isSetup {
		if err := platformupdate.LaunchInstaller(targetPath); err != nil {
			return err
		}
		// 退出当前应用以解除目录锁定
		os.Exit(0)
		return nil
	}

	// Portable 热更替换逻辑
	if needsElevated {
		if err := platformupdate.ApplyElevated(targetPath, exePath); err != nil {
			return err
		}
		os.Exit(0)
		return nil
	}

	oldPath := exePath + ".old"
	// 清理上次更新残留的 .old 文件
	os.Remove(oldPath)
	if err := os.Rename(exePath, oldPath); err != nil {
		os.Remove(targetPath)
		return fmt.Errorf("failed to rename current executable: %w", err)
	}

	if err := os.Rename(targetPath, exePath); err != nil {
		os.Rename(oldPath, exePath)
		return fmt.Errorf("failed to apply update file: %w", err)
	}

	if err := platformupdate.Restart(exePath); err != nil {
		return err
	}

	os.Exit(0)
	return nil
}

// ── config.Credential 凭据管理 ──────────────────────────────────────────

func (a *App) GetCredentials() []config.Credential {
	return a.configManager.GetCredentialsMasked()
}

func (a *App) SaveCredential(cred config.Credential) config.Credential {
	return a.configManager.SaveCredential(cred)
}

func (a *App) DeleteCredential(id string) error {
	return a.configManager.DeleteCredential(id)
}
