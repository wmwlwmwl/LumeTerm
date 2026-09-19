package ai

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// AI 对话完整日志:常开,把每轮请求(含完整 messages)与原始响应流逐字节写入
// <configDir>/ai.log,超限按大小轮转。目的是让 AI 面板出问题时,用户直接把该文件
// 发出来即可定位,而不必从会话存档反推模型到底吐了什么。
//
// 设计约束:
//   - 绝不影响聊天:未初始化、目录不可写、写入失败时全部退化为 no-op;
//   - 绝不泄漏密钥:不记录 Authorization 头,自定义头只记名字,写入前遮蔽 API Key 与 sk- 令牌;
//   - 体积可控:单文件 20 MiB、保留 3 份轮转;同一会话重复的请求体只记一次(按 sha256 去重)。
const (
	aiDebugLogFileName   = "ai.log"
	aiDebugLogBufferSize = 64 << 10
)

// 轮转限额用变量而非常量,便于单元测试缩小规模验证轮转行为。
var (
	aiDebugLogMaxSize    = int64(20 << 20) // 20 MiB
	aiDebugLogMaxBackups = 3
)

// aiDebugLogger 是带大小轮转的追加写入器,自带缓冲。所有写入都在互斥锁内完成。
type aiDebugLogger struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	writer  *bufio.Writer
	written int64
	// enabled 为 false 时所有写入退化为 no-op(用户在设置里关闭了 AI 对话日志)。
	enabled bool
	// bodyHashes 记录每个会话上一次已写入的请求体摘要,用于跳过完全相同的重发(如重试)。
	bodyHashes map[string]string
	// secrets 需要在写入前遮蔽的字符串(当前供应商的 API Key)。
	secrets []string
}

var aiDebugLogState struct {
	mu     sync.Mutex
	logger *aiDebugLogger
}

// initAIDebugLog 由 NewService 调用,解析并打开日志文件;失败时保持 no-op。
func initAIDebugLog(configDir string) {
	logger := newAIDebugLogger(configDir)
	aiDebugLogState.mu.Lock()
	previous := aiDebugLogState.logger
	aiDebugLogState.logger = logger
	aiDebugLogState.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
}

func currentAIDebugLogger() *aiDebugLogger {
	aiDebugLogState.mu.Lock()
	defer aiDebugLogState.mu.Unlock()
	return aiDebugLogState.logger
}

func newAIDebugLogger(configDir string) *aiDebugLogger {
	candidates := make([]string, 0, 2)
	if trimmed := strings.TrimSpace(configDir); trimmed != "" {
		candidates = append(candidates, filepath.Join(trimmed, aiDebugLogFileName))
	}
	if exePath, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exePath), aiDebugLogFileName))
	}
	for _, path := range candidates {
		if dir := filepath.Dir(path); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				continue
			}
		}
		logger := &aiDebugLogger{path: path, enabled: true, bodyHashes: map[string]string{}}
		if err := logger.reopen(); err != nil {
			continue
		}
		return logger
	}
	return nil
}

// setAIDebugLogEnabled 由设置变更时调用:关闭后日志写入整体退化为 no-op。
func setAIDebugLogEnabled(enabled bool) {
	logger := currentAIDebugLogger()
	if logger == nil {
		return
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.enabled = enabled
	if !enabled {
		// 关掉之前把已缓冲内容落盘,避免留下半截轮次。
		if logger.writer != nil {
			_ = logger.writer.Flush()
		}
	}
}

func (l *aiDebugLogger) isEnabled() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enabled
}

// SetSecrets 更新需要遮蔽的敏感串(供应商 API Key 变化时调用)。
func (l *aiDebugLogger) SetSecrets(secrets ...string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	cleaned := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if trimmed := strings.TrimSpace(secret); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	l.secrets = cleaned
}

// reopen 以追加模式打开文件,并把当前大小作为已写基线。
func (l *aiDebugLogger) reopen() error {
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if info, statErr := file.Stat(); statErr == nil {
		l.written = info.Size()
	}
	l.file = file
	l.writer = bufio.NewWriterSize(file, aiDebugLogBufferSize)
	return nil
}

// rotateLocked 依次后移历史文件(ai.log -> ai.log.1 -> ...),失败不致命。
func (l *aiDebugLogger) rotateLocked() {
	if l.writer != nil {
		_ = l.writer.Flush()
	}
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
		l.writer = nil
	}
	for index := aiDebugLogMaxBackups - 1; index >= 1; index-- {
		from := fmt.Sprintf("%s.%d", l.path, index)
		to := fmt.Sprintf("%s.%d", l.path, index+1)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		if err := os.Rename(from, to); err != nil {
			slog.Warn("AI调试日志轮转失败", "from", from, "to", to, "error", err)
		}
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil {
		slog.Warn("AI调试日志轮转主文件失败", "path", l.path, "error", err)
	}
	if err := l.reopen(); err != nil {
		l.file = nil
		l.writer = nil
	}
}

func (l *aiDebugLogger) writeLocked(p []byte) {
	if !l.enabled {
		return
	}
	if l.writer == nil {
		if err := l.reopen(); err != nil {
			return
		}
	}
	if l.written+int64(len(p)) > aiDebugLogMaxSize {
		l.rotateLocked()
		if l.writer == nil {
			return
		}
	}
	n, err := l.writer.Write(p)
	l.written += int64(n)
	if err != nil {
		// 写入失败(磁盘满 / 文件被删)不致命,下次写入再尝试。
		l.writer = nil
	}
}

func (l *aiDebugLogger) Write(p []byte) (int, error) {
	if l == nil {
		return len(p), nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writeLocked(p)
	return len(p), nil
}

// Flush 把缓冲区落盘。每轮结束时调用。
func (l *aiDebugLogger) Flush() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writer != nil {
		_ = l.writer.Flush()
	}
}

func (l *aiDebugLogger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var err error
	if l.writer != nil {
		err = l.writer.Flush()
	}
	if l.file != nil {
		if closeErr := l.file.Close(); err == nil {
			err = closeErr
		}
	}
	l.writer = nil
	l.file = nil
	return err
}

var (
	aiDebugTokenPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{12,}\b`)
	// 请求体是逐字记录的会话内容,里面可能包含用户脚本/命令输出中的令牌,故一并遮蔽。
	aiDebugBearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._\-]{8,}`)
)

// redactLocked 遮蔽敏感内容:已知密钥原值 + sk- 令牌 + Bearer 令牌。
func (l *aiDebugLogger) redactLocked(text string) string {
	for _, secret := range l.secrets {
		if secret == "" {
			continue
		}
		text = strings.ReplaceAll(text, secret, "***REDACTED***")
	}
	text = aiDebugTokenPattern.ReplaceAllString(text, "sk-***REDACTED***")
	return aiDebugBearerPattern.ReplaceAllString(text, "Bearer ***REDACTED***")
}

// aiDebugRoundMeta 描述一次 provider 请求轮次的上下文。
type aiDebugRoundMeta struct {
	RequestID         string
	ConversationID    string
	Protocol          string
	Model             string
	Endpoint          string
	MessageCount      int
	SystemPromptChars int
	// RoundIndex / AttemptIndex 可选,仅用于日志可读性。
	RoundIndex   int
	AttemptIndex int
	// APIKey 只用于构造脱敏列表,绝不写入日志。
	APIKey string
}

// traceAIHTTPRound 用一个统一入口记录"请求 + 原始响应流":
// 写轮次头与请求体(相同则省略)→ 发请求 → 失败记错误 → 成功则把响应体包成
// TeeReader,后续由解析循环正常读取的同时逐字节落盘,轮次结束时写尾行。
func traceAIHTTPRound(client *http.Client, req *http.Request, meta aiDebugRoundMeta, body []byte) (*http.Response, error) {
	logger := currentAIDebugLogger()
	if logger == nil || !logger.isEnabled() {
		return client.Do(req)
	}
	logger.SetSecrets(meta.APIKey)

	startedAt := time.Now()
	summary := aiDebugRequestSummary(req, meta, body)

	resp, err := client.Do(req)
	if err != nil {
		logger.mu.Lock()
		{
			logger.writeLocked([]byte(logger.redactLocked(summary)))
			logger.writeLocked([]byte(fmt.Sprintf("-------------- request failed: %v (%.1fs) --------------\n\n",
				err, time.Since(startedAt).Seconds())))
		}
		logger.mu.Unlock()
		logger.Flush()
		return resp, err
	}

	header := logger.prepareRoundHeader(meta, summary, body)
	header += fmt.Sprintf("-------------- response stream (raw, HTTP %d) --------------\n", resp.StatusCode)

	trailer := fmt.Sprintf("-------------- stream end: HTTP %d, %.1fs --------------\n\n",
		resp.StatusCode, time.Since(startedAt).Seconds())

	if resp.Body == nil {
		logger.Write([]byte(header))
		logger.Write([]byte(trailer))
		logger.Flush()
		return resp, nil
	}
	resp.Body = &aiDebugTracedBody{
		ReadCloser: resp.Body,
		logger:     logger,
		header:     header,
		trailer:    trailer,
	}
	return resp, nil
}

// prepareRoundHeader 组装轮次头:元信息 + 请求头白名单 + 请求体(按会话 sha256 去重)。
func (l *aiDebugLogger) prepareRoundHeader(meta aiDebugRoundMeta, summary string, body []byte) string {
	hash := sha256.Sum256(body)
	hashText := hex.EncodeToString(hash[:])[:16]

	l.mu.Lock()
	defer l.mu.Unlock()

	var builder strings.Builder
	builder.WriteString("\n===== round start ")
	builder.WriteString(time.Now().Format("2006-01-02 15:04:05.000"))
	builder.WriteString(" =====\n")
	builder.WriteString(summary)

	if l.bodyHashes == nil {
		l.bodyHashes = map[string]string{}
	}
	key := strings.TrimSpace(meta.ConversationID)
	previousHash, seen := l.bodyHashes[key]
	if seen && previousHash == hashText {
		builder.WriteString(fmt.Sprintf("request-body sha256=%s (与上一轮相同,已省略)\n", hashText))
	} else {
		l.bodyHashes[key] = hashText
		builder.WriteString(fmt.Sprintf("request-body sha256=%s bytes=%d\n", hashText, len(body)))
		builder.WriteString(l.redactLocked(string(body)))
		builder.WriteString("\n")
	}
	return builder.String()
}

// aiDebugRequestSummary 生成不含密钥的请求元信息与请求头白名单。
func aiDebugRequestSummary(req *http.Request, meta aiDebugRoundMeta, body []byte) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("request  id=%s conv=%s round=%d attempt=%d\n",
		meta.RequestID, meta.ConversationID, meta.RoundIndex, meta.AttemptIndex))
	builder.WriteString(fmt.Sprintf("endpoint %s\n", meta.Endpoint))
	builder.WriteString(fmt.Sprintf("protocol=%s model=%s messages=%d body=%d bytes systemPrompt=%d chars\n",
		meta.Protocol, meta.Model, meta.MessageCount, len(body), meta.SystemPromptChars))

	if req != nil {
		names := make([]string, 0, 4)
		for _, header := range []string{"Content-Type", "Accept", "User-Agent"} {
			if value := strings.TrimSpace(req.Header.Get(header)); value != "" {
				names = append(names, fmt.Sprintf("%s=%s", header, value))
			}
		}
		customNames := make([]string, 0, 4)
		for name := range req.Header {
			switch strings.ToLower(name) {
			case "authorization", "content-type", "accept", "user-agent", "content-length", "host":
				continue
			}
			customNames = append(customNames, name)
		}
		sort.Strings(customNames)
		builder.WriteString(fmt.Sprintf("headers  %s custom=[%s] (Authorization 不记录)\n",
			strings.Join(names, " "), strings.Join(customNames, ",")))
	}
	return builder.String()
}

// aiDebugTracedBody 边读边把原始字节写进日志,结束时写尾行并落盘。
type aiDebugTracedBody struct {
	io.ReadCloser
	logger  *aiDebugLogger
	header  string
	trailer string
	once    sync.Once
}

func (b *aiDebugTracedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { b.logger.Write([]byte(b.header)) })
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.logger.Write(p[:n])
	}
	return n, err
}

func (b *aiDebugTracedBody) Close() error {
	err := b.ReadCloser.Close()
	b.logger.Write([]byte(b.trailer))
	b.logger.Flush()
	return err
}

// aiDebugLogRoundResult 记录单轮结果摘要,把"请求 → 原始流 → 结果判定"在同一时间线上串起来。
func aiDebugLogRoundResult(conversationID string, requestID string, roundIndex int, attemptIndex int,
	profile AIProviderProfile, duration time.Duration, result aiChatRoundResult, roundErr error) {
	logger := currentAIDebugLogger()
	if logger == nil {
		return
	}
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("result   conv=%s id=%s round=%d attempt=%d protocol=%s model=%s %.1fs\n",
		conversationID, requestID, roundIndex, attemptIndex, profile.Provider, profile.Model, duration.Seconds()))
	builder.WriteString(fmt.Sprintf("         text=%d chars tokens=in:%d/out:%d cache:%d elapsed=%dms\n",
		len(result.Text), result.InputTokens, result.OutputTokens, result.CacheReadTokens, result.ElapsedMs))

	tools, duplicated, parseErr := parseAssistantToolUsesWithDuplicateCount(result.Text)
	switch {
	case parseErr == nil:
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, tool.Name)
		}
		builder.WriteString(fmt.Sprintf("         tools=[%s] duplicated=%d parse=ok\n",
			strings.Join(names, ","), duplicated))
	case strings.TrimSpace(result.Text) == aiChatEmptyResponseText || strings.TrimSpace(result.Text) == "":
		builder.WriteString("         tools=[] parse=skip(空正文)\n")
	default:
		builder.WriteString(fmt.Sprintf("         parse=failed: %v\n", parseErr))
	}
	if roundErr != nil {
		builder.WriteString(fmt.Sprintf("         roundErr: %v\n", roundErr))
	}
	builder.WriteString("===== round end =====\n")

	logger.Write([]byte(builder.String()))
	logger.Flush()
}

// AIDebugLogPath 返回当前 AI 日志文件的绝对路径;未初始化时返回空串。
// 供 Wails 绑定在文件管理器中定位该文件用。
func AIDebugLogPath() string {
	logger := currentAIDebugLogger()
	if logger == nil {
		return ""
	}
	return logger.path
}

// aiDebugLogMessage 供其它路径(如模型校验、联网搜索)复用的通用写入入口。
func aiDebugLogMessage(format string, args ...any) {
	logger := currentAIDebugLogger()
	if logger == nil {
		return
	}
	logger.Write([]byte(fmt.Sprintf(format, args...)))
	logger.Flush()
}
