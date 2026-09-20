package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	aiprovider "lumeterm/internal/ai/provider"
)

// TestCompatibleChatDegenerationRepro 用与 Lumin 完全相同的流程复现"流内退化重复":
// 读取指定会话的历史消息,按 Compatible 协议构造请求(复用同一套 prompt / messages /
// 采样参数 / 请求头构造代码),追加一条用户消息(默认"继续"),然后把流原样读出来,
// 统计两个通道里的重复片段与非宿主协议标签(含 DSML 标记形式)。
//
// 默认跳过,必须显式开启才会发出真实请求(会消耗该供应商的额度):
//
//	LUMIN_AI_REPRO=1 go test ./internal/ai/ -run TestCompatibleChatDegenerationRepro -v -timeout 900s
//
// 可用环境变量:
//
//	LUMIN_CONFIG_DIR       LumeTerm 配置目录,默认 <UserConfigDir>/LumeTerm/config
//	LUMIN_CONV_ID          会话 ID,默认 conv-da02222ff7825401
//	LUMIN_PROVIDER_ID      供应商 ID,默认取 ai_global_settings.json 的 currentProviderId
//	LUMIN_REPRO_PROMPT     追加的用户消息,默认 "继续"
//	LUMIN_REPRO_ITERATIONS 最多发几次请求直到复现,默认 1
func TestCompatibleChatDegenerationRepro(t *testing.T) {
	if strings.TrimSpace(os.Getenv("LUMIN_AI_REPRO")) != "1" {
		t.Skip("未设置 LUMIN_AI_REPRO=1,跳过(此测试会发出真实 API 请求)")
	}

	configDir := strings.TrimSpace(os.Getenv("LUMIN_CONFIG_DIR"))
	if configDir == "" {
		userConfigDir, err := os.UserConfigDir()
		if err != nil {
			t.Fatalf("无法定位用户配置目录: %v", err)
		}
		configDir = filepath.Join(userConfigDir, "LumeTerm", "config")
	}
	conversationID := strings.TrimSpace(os.Getenv("LUMIN_CONV_ID"))
	if conversationID == "" {
		conversationID = "conv-da02222ff7825401"
	}
	prompt := strings.TrimSpace(os.Getenv("LUMIN_REPRO_PROMPT"))
	if prompt == "" {
		prompt = "继续"
	}
	iterations := 1
	if raw := strings.TrimSpace(os.Getenv("LUMIN_REPRO_ITERATIONS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			iterations = parsed
		}
	}

	profile := loadReproProviderProfile(t, configDir)
	t.Logf("供应商: %s | 协议=%s | 模型=%s | 端点=%s", profile.Name, profile.Provider, profile.Model, profile.BaseURL)
	if profile.Provider != "" && profile.Provider != "Compatible" {
		t.Logf("注意: 该供应商协议为 %s,本测试只构造 Compatible(/chat/completions)请求", profile.Provider)
	}

	history := loadReproConversationHistory(t, configDir, conversationID)
	sessionID := extractReproSessionID(history)
	requestMessages := append(append([]AIChatRequestMessage{}, history...),
		AIChatRequestMessage{Role: "user", Content: buildReproUserMessage(history, prompt)})
	t.Logf("历史消息: %d 条 | 会话 ID: %s | 追加消息: %q | 计划请求次数: %d",
		len(history), sessionID, prompt, iterations)

	payload := AIChatRequestPayload{ConversationID: conversationID, SessionID: sessionID}
	payload.ToolScope = normalizeAIToolScope(payload.ToolScope)
	systemPrompt := resolveAISystemPromptForPayload(context.Background(), payload, profile)
	t.Logf("system prompt: %d 字符", len(systemPrompt))

	// 与 App 一致:复现过程同样落盘一份完整 AI 日志,便于事后比对。
	initAIDebugLog(configDir)
	if logger := currentAIDebugLogger(); logger != nil {
		t.Logf("AI 日志: %s", logger.path)
	} else {
		t.Logf("AI 日志: 未启用(目录不可写)")
	}

	for iteration := 1; iteration <= iterations; iteration++ {
		t.Logf("———— 第 %d/%d 次 ————", iteration, iterations)
		if runReproIteration(t, profile, conversationID, systemPrompt, requestMessages) {
			t.Logf("结论: 第 %d 次复现到退化重复", iteration)
			return
		}
		if iteration == iterations {
			t.Logf("结论: %d 次均未出现退化重复(可加大 LUMIN_REPRO_ITERATIONS)", iterations)
		}
	}
}

// runReproIteration 发一次与 Lumin 相同形态的请求,返回是否命中退化重复。
func runReproIteration(t *testing.T, profile AIProviderProfile, conversationID string, systemPrompt string, requestMessages []AIChatRequestMessage) bool {
	t.Helper()

	// 与 provider_compatible.go 的 requestCompatibleAIChatRound 一致地构造请求体。
	modelCapability := aiprovider.ResolveModelCapability(profile.Provider, profile.Model)
	runtimeProfile := toAIProviderRuntimeProfile(profile)
	requestBody := map[string]any{
		"model":    profile.Model,
		"stream":   true,
		"messages": aiprovider.BuildOpenAIChatMessages(systemPrompt, toAIProviderRuntimeMessages(requestMessages), aiprovider.ResolvePromptCacheStrategy(runtimeProfile, modelCapability)),
	}
	aiprovider.ApplySamplingParameters(requestBody, runtimeProfile)
	if reasoningEffort := aiprovider.GetEffectiveReasoningEffort(runtimeProfile, modelCapability); reasoningEffort != "" {
		requestBody["reasoning_effort"] = reasoningEffort
	} else if aiprovider.ShouldUseBinaryReasoning(runtimeProfile, modelCapability) {
		requestBody["thinking"] = map[string]any{"type": "enabled"}
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatalf("序列化请求体失败: %v", err)
	}
	endpoint := strings.TrimRight(profile.BaseURL, "/") + "/chat/completions"
	requestID := fmt.Sprintf("repro-%d", time.Now().UnixMilli())
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", aiprovider.GetUserAgent(requestID))
	req.Header.Set("Accept", "text/event-stream")
	if apiKey := strings.TrimSpace(profile.APIKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	aiprovider.ApplyCustomHeaders(req.Header, runtimeProfile.CustomHeaders)

	startedAt := time.Now()
	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := traceAIHTTPRound(client, req, aiDebugRoundMeta{
		RequestID:         requestID,
		ConversationID:    conversationID,
		Protocol:          profile.Provider,
		Model:             profile.Model,
		Endpoint:          endpoint,
		MessageCount:      len(requestMessages),
		SystemPromptChars: len(systemPrompt),
		APIKey:            profile.APIKey,
	}, body)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("HTTP %d: %s", resp.StatusCode, readReproPrefix(resp.Body, 500))
	}

	var contentBuilder, reasoningBuilder strings.Builder
	chunkCount := 0
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		chunkPayload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if chunkPayload == "" || chunkPayload == "[DONE]" {
			continue
		}
		var chunk aiChatCompatibleChunk
		if err := json.Unmarshal([]byte(chunkPayload), &chunk); err != nil {
			continue
		}
		chunkCount++
		for _, choice := range chunk.Choices {
			reasoningBuilder.WriteString(choice.Delta.ReasoningContent)
			reasoningBuilder.WriteString(choice.Delta.Reasoning)
			contentBuilder.WriteString(choice.Delta.Content)
		}
		// 退化时流可能极长,设个上限防止测试卡死(留下足够样本即可)。
		if reasoningBuilder.Len()+contentBuilder.Len() > 400_000 {
			t.Logf("已达 400k 字符上限,提前停止读取")
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Logf("读取流出错(可能被截断): %v", err)
	}

	reasoning := reasoningBuilder.String()
	content := contentBuilder.String()
	t.Logf("用时 %s | data 块 %d | 推理 %d 字符 | 正文 %d 字符",
		time.Since(startedAt).Round(time.Millisecond), chunkCount, len(reasoning), len(content))

	degenerated := reportReproDegeneration(t, "推理", reasoning)
	if reportReproDegeneration(t, "正文", content) {
		degenerated = true
	}

	// 用 Lumin 自己的清洗 / 解析逻辑,看它会怎么处理这次输出。
	sanitized := sanitizeAIAssistantToolProtocolText(content)
	t.Logf("Lumin sanitize: %d -> %d 字符", len(content), len(sanitized))
	if tools, duplicated, parseErr := parseAssistantToolUsesWithDuplicateCount(content); parseErr != nil {
		t.Logf("Lumin 工具解析: 失败(%v)", parseErr)
	} else {
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, tool.Name)
		}
		t.Logf("Lumin 工具解析: 成功 %v (重复 %d)", names, duplicated)
	}
	// 与 App 的 chat 循环一致地补上结果摘要,使 ai.log 里"请求 → 原始流 → 结果判定"完整。
	aiDebugLogRoundResult(conversationID, requestID, 0, 1, profile, time.Since(startedAt),
		aiChatRoundResult{Text: content, ElapsedMs: time.Since(startedAt).Milliseconds()}, nil)
	return degenerated
}

const (
	reproRepeatWindowSize = 20 // 重复检测窗口(字符)
	reproRepeatThreshold  = 8  // 同一窗口出现次数阈值
	reproForeignTagLimit  = 20 // 非宿主协议标签累计数量阈值
	reproClosingRunLimit  = 6  // 连续闭合标签达到该数量即认为异常
)

var (
	reproForeignTagPattern  = regexp.MustCompile(`<\s*/?\s*(?:invoke|parameter|tool_calls|function_calls|tool_reply|tools)\b[^>]*>`)
	reproClosingTagPattern  = regexp.MustCompile(`<\s*/\s*[A-Za-z_][^>]*>`)
	reproClosingRunPattern  = regexp.MustCompile(`(?:<\s*/\s*[A-Za-z_][^>]*>[\s]*){` + strconv.Itoa(reproClosingRunLimit) + `,}`)
	reproHostToolTagPattern = regexp.MustCompile(`<\s*/?\s*execute_command\b[^>]*>`)
)

// reportReproDegeneration 统计单个通道里的重复片段与非宿主协议标签(含 DSML 标记形式),
// 返回是否命中退化。
func reportReproDegeneration(t *testing.T, channel string, text string) bool {
	if strings.TrimSpace(text) == "" {
		t.Logf("[%s] 空", channel)
		return false
	}

	// DSML 标记(<全角竖线全角竖线 DSML 全角竖线全角竖线)会插在 '<' 与标签名之间,
	// 去掉它以后非宿主标签才能被正常匹配与统计。
	countable := strings.ReplaceAll(text, aiToolProtocolDSMLMarker, "")

	windows := map[string]int{}
	for start := 0; start+reproRepeatWindowSize <= len(countable); start += 4 {
		windows[countable[start:start+reproRepeatWindowSize]]++
	}
	type windowHit struct {
		text  string
		count int
	}
	hits := make([]windowHit, 0, 4)
	for window, count := range windows {
		if count > reproRepeatThreshold {
			hits = append(hits, windowHit{window, count})
		}
	}
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].count > hits[i].count {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
	if len(hits) > 3 {
		hits = hits[:3]
	}

	dsmlMarkers := strings.Count(text, aiToolProtocolDSMLMarker)
	foreignTags := reproForeignTagPattern.FindAllString(countable, -1)
	closingTags := reproClosingTagPattern.FindAllString(countable, -1)
	hostTags := reproHostToolTagPattern.FindAllString(countable, -1)
	longestClosingRun := 0
	for _, run := range reproClosingRunPattern.FindAllString(countable, -1) {
		if count := len(reproClosingTagPattern.FindAllString(run, -1)); count > longestClosingRun {
			longestClosingRun = count
		}
	}

	t.Logf("[%s] %d 字符 | DSML 标记=%d | execute_command 标签=%d | 非宿主标签=%d | 闭合标签=%d | 最长连续闭合=%d",
		channel, len(text), dsmlMarkers, len(hostTags), len(foreignTags), len(closingTags), longestClosingRun)

	degenerated := false
	if len(hits) > 0 {
		degenerated = true
		t.Logf("[%s] 退化: 重复片段 %d 组", channel, len(hits))
		for _, hit := range hits {
			t.Logf("    x%-4d %s", hit.count, escapeReproText(hit.text, 60))
		}
	}
	if len(foreignTags)+dsmlMarkers > reproForeignTagLimit {
		degenerated = true
		t.Logf("[%s] 退化: 非宿主协议标签刷屏(前 3 个: %s)", channel,
			escapeReproText(strings.Join(firstReproStrings(foreignTags, 3), " "), 120))
	}
	if longestClosingRun >= reproClosingRunLimit {
		degenerated = true
		t.Logf("[%s] 退化: 连续闭合标签 %d 个", channel, longestClosingRun)
	}
	t.Logf("[%s] 尾部 200 字符: %s", channel, escapeReproText(text, 200))
	return degenerated
}

func firstReproStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func escapeReproText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		text = string(runes[len(runes)-limit:])
	}
	replacer := strings.NewReplacer("<", "[LT]", ">", "[GT]", "\n", "\\n", "\r", "")
	return replacer.Replace(text)
}

func loadReproProviderProfile(t *testing.T, configDir string) AIProviderProfile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(configDir, "ai_providers.json"))
	if err != nil {
		t.Fatalf("读取 ai_providers.json 失败: %v", err)
	}
	var registry struct {
		Providers []AIProviderProfile `json:"providers"`
	}
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatalf("解析 ai_providers.json 失败: %v", err)
	}
	providerID := strings.TrimSpace(os.Getenv("LUMIN_PROVIDER_ID"))
	if providerID == "" {
		if settingsRaw, readErr := os.ReadFile(filepath.Join(configDir, "ai_global_settings.json")); readErr == nil {
			var settings struct {
				CurrentProviderID string `json:"currentProviderId"`
			}
			if json.Unmarshal(settingsRaw, &settings) == nil {
				providerID = strings.TrimSpace(settings.CurrentProviderID)
			}
		}
	}
	for _, candidate := range registry.Providers {
		if candidate.ID == providerID {
			return candidate
		}
	}
	if len(registry.Providers) > 0 {
		t.Logf("未找到供应商 %q,改用第一个: %s", providerID, registry.Providers[0].Name)
		return registry.Providers[0]
	}
	t.Fatalf("ai_providers.json 里没有任何供应商")
	return AIProviderProfile{}
}

func loadReproConversationHistory(t *testing.T, configDir string, conversationID string) []AIChatRequestMessage {
	t.Helper()
	path := filepath.Join(configDir, "tasks", conversationID, "api_conversation_history.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取会话历史失败(%s): %v", path, err)
	}
	var history []AIChatRequestMessage
	if err := json.Unmarshal(raw, &history); err != nil {
		t.Fatalf("解析会话历史失败: %v", err)
	}
	if len(history) == 0 {
		t.Fatalf("会话历史为空")
	}
	return history
}

var (
	reproUserMessagePattern = regexp.MustCompile(`(?s)<user_message>.*?</user_message>`)
	reproEnvironmentDetails = regexp.MustCompile(`(?s)<environment_details>.*?</environment_details>`)
	reproUTCTimePattern     = regexp.MustCompile(`UTC时间:[^\n]*`)
	reproSessionIDPattern   = regexp.MustCompile(`session_[0-9]+`)
)

// buildReproUserMessage 按历史里现有用户消息的形状重建一条新用户消息:
// 保留同样的 <environment_details> 块(只刷新时间),替换 <user_message> 内容。
func buildReproUserMessage(history []AIChatRequestMessage, prompt string) string {
	environmentDetails := ""
	for index := len(history) - 1; index >= 0; index-- {
		if !strings.EqualFold(strings.TrimSpace(history[index].Role), "user") {
			continue
		}
		if match := reproEnvironmentDetails.FindString(history[index].Content); match != "" {
			environmentDetails = reproUTCTimePattern.ReplaceAllString(match, "UTC时间:"+time.Now().UTC().Format(time.RFC3339))
			break
		}
	}
	message := "<user_message>\n" + prompt + "\n</user_message>"
	if environmentDetails != "" {
		message += "\n\n" + environmentDetails
	}
	return message
}

func extractReproSessionID(history []AIChatRequestMessage) string {
	for index := len(history) - 1; index >= 0; index-- {
		if match := reproSessionIDPattern.FindString(history[index].Content); match != "" {
			return match
		}
	}
	return ""
}

func readReproPrefix(body interface{ Read([]byte) (int, error) }, limit int) string {
	buffer := make([]byte, limit)
	n, _ := body.Read(buffer)
	return escapeReproText(string(buffer[:n]), limit)
}
