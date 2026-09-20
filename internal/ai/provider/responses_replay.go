package provider

import (
	"encoding/json"
	"strings"
)

const (
	// openAIResponsesReplayKind 标识本适配器私有的紧凑回放信封格式。
	openAIResponsesReplayKind = "lumeterm-openai-responses"
	// openAIResponsesReplayVersion 在信封结构发生不兼容变化时递增。
	// v2: 身份字段由协议标识 provider 改为端点 endpoint。
	openAIResponsesReplayVersion = 2
)

// normalizeOpenAIResponsesEndpoint 规范化端点, 消除尾斜杠与空白造成的假性不匹配。
func normalizeOpenAIResponsesEndpoint(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

// cloneOpenAIResponsesJSONValue 深拷贝任意 JSON 兼容值, 避免共享底层引用。
func cloneOpenAIResponsesJSONValue(value any) (any, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var cloned any
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, false
	}
	return cloned, true
}

// optionalOpenAIResponsesString 读取可选字符串字段。字段存在但类型不符时返回 false。
func optionalOpenAIResponsesString(item map[string]any, key string) (string, bool) {
	raw, exists := item[key]
	if !exists || raw == nil {
		return "", true
	}
	text, ok := raw.(string)
	if !ok {
		return "", false
	}
	return text, true
}

// openAIResponsesSummaryText 汇总 reasoning item 的 summary 文本。
// simple 为 true 表示 summary 恰好是单个 summary_text 元素, 可从内容块无损重建。
func openAIResponsesSummaryText(item map[string]any) (text string, simple bool, ok bool) {
	raw, exists := item["summary"]
	if !exists || raw == nil {
		return "", true, true
	}
	entries, isArray := raw.([]any)
	if !isArray {
		return "", false, false
	}
	if len(entries) == 0 {
		return "", true, true
	}
	var builder strings.Builder
	simpleCandidate := len(entries) == 1
	for _, entry := range entries {
		record, isRecord := entry.(map[string]any)
		if !isRecord {
			return "", false, false
		}
		entryType, _ := record["type"].(string)
		if entryType != "summary_text" {
			simpleCandidate = false
			continue
		}
		if entryText, isString := record["text"].(string); isString {
			builder.WriteString(entryText)
		} else if record["text"] != nil {
			simpleCandidate = false
		}
	}
	return builder.String(), simpleCandidate, true
}

// openAIResponsesMessageText 汇总 message item 的 output_text 文本。
// ok 为 false 表示该 message 含有无法用纯文本表达的内容部分。
func openAIResponsesMessageText(item map[string]any) (string, bool) {
	raw, exists := item["content"]
	if !exists || raw == nil {
		return "", true
	}
	parts, isArray := raw.([]any)
	if !isArray {
		return "", false
	}
	var builder strings.Builder
	for _, part := range parts {
		record, isRecord := part.(map[string]any)
		if !isRecord {
			return "", false
		}
		if partType, _ := record["type"].(string); partType != "output_text" {
			return "", false
		}
		if partText, isString := record["text"].(string); isString {
			builder.WriteString(partText)
		} else if record["text"] != nil {
			return "", false
		}
	}
	return builder.String(), true
}

// BuildOpenAIResponsesCompactReplay 把完成响应的原生 output items 拆成两段:
// contentBlocks 承载全部可重建文本, 是后续请求的权威文本源;
// replayState 只保存无法从文本重建的 provider 原生字段 (item id, 加密推理内容等)。
//
// baseURL 与 model 构成回放身份: 切换端点或模型时校验失败, 该条 assistant 降级为
// 普通文本, 即一次冷启动。
// 当 output 含有本版本无法建模的 item 类型时返回 ok=false, 调用方应退回保存完整
// output 以避免丢失能力。
func BuildOpenAIResponsesCompactReplay(
	output []map[string]any,
	baseURL string,
	model string,
	responseID string,
	include []string,
) (contentBlocks []map[string]any, replayState map[string]any, ok bool) {
	trimmedEndpoint := normalizeOpenAIResponsesEndpoint(baseURL)
	trimmedModel := strings.TrimSpace(model)
	if len(output) == 0 || trimmedEndpoint == "" || trimmedModel == "" {
		return nil, nil, false
	}

	blocks := make([]map[string]any, 0, len(output))
	metadata := make([]map[string]any, 0, len(output))

	for _, item := range output {
		if item == nil {
			return nil, nil, false
		}
		itemType, _ := item["type"].(string)
		switch itemType {
		case "reasoning":
			summaryText, simpleSummary, summaryOK := openAIResponsesSummaryText(item)
			if !summaryOK {
				return nil, nil, false
			}
			itemID, idOK := optionalOpenAIResponsesString(item, "id")
			if !idOK {
				return nil, nil, false
			}
			encryptedContent, encryptedOK := optionalOpenAIResponsesString(item, "encrypted_content")
			if !encryptedOK {
				return nil, nil, false
			}
			blocks = append(blocks, map[string]any{"type": "reasoning", "text": summaryText})
			descriptor := map[string]any{"type": "reasoning"}
			if itemID != "" {
				descriptor["id"] = itemID
			}
			if encryptedContent != "" {
				descriptor["encryptedContent"] = encryptedContent
			}
			if !simpleSummary {
				clonedSummary, cloneOK := cloneOpenAIResponsesJSONValue(item["summary"])
				if !cloneOK {
					return nil, nil, false
				}
				descriptor["summary"] = clonedSummary
			}
			metadata = append(metadata, descriptor)
		case "message":
			messageText, textOK := openAIResponsesMessageText(item)
			if !textOK {
				return nil, nil, false
			}
			itemID, idOK := optionalOpenAIResponsesString(item, "id")
			if !idOK {
				return nil, nil, false
			}
			role, roleOK := optionalOpenAIResponsesString(item, "role")
			if !roleOK {
				return nil, nil, false
			}
			hasText := messageText != ""
			if hasText {
				blocks = append(blocks, map[string]any{"type": "text", "text": messageText})
			}
			descriptor := map[string]any{"type": "message", "hasText": hasText}
			if itemID != "" {
				descriptor["id"] = itemID
			}
			if role != "" {
				descriptor["role"] = role
			}
			metadata = append(metadata, descriptor)
		case "function_call":
			callID, callOK := optionalOpenAIResponsesString(item, "call_id")
			itemID, idOK := optionalOpenAIResponsesString(item, "id")
			name, nameOK := optionalOpenAIResponsesString(item, "name")
			arguments, argumentsOK := optionalOpenAIResponsesString(item, "arguments")
			if !callOK || !idOK || !nameOK || !argumentsOK || callID == "" || name == "" {
				return nil, nil, false
			}
			blocks = append(blocks, map[string]any{
				"type":      "tool_use",
				"id":        callID,
				"name":      name,
				"arguments": arguments,
			})
			descriptor := map[string]any{"type": "function_call", "callId": callID, "name": name}
			if itemID != "" {
				descriptor["id"] = itemID
			}
			metadata = append(metadata, descriptor)
		default:
			// 内置搜索, computer-use 及未来的 provider 原生 item 无法从文本重建。
			return nil, nil, false
		}
	}

	if len(metadata) == 0 {
		return nil, nil, false
	}

	response := map[string]any{
		"endpoint": trimmedEndpoint,
		"model":    trimmedModel,
	}
	if trimmedResponseID := strings.TrimSpace(responseID); trimmedResponseID != "" {
		response["responseId"] = trimmedResponseID
	}
	if normalizedInclude := normalizeStringList(include); len(normalizedInclude) > 0 {
		includeValues := make([]any, 0, len(normalizedInclude))
		for _, value := range normalizedInclude {
			includeValues = append(includeValues, value)
		}
		response["include"] = includeValues
	}

	metadataValues := make([]any, 0, len(metadata))
	for _, descriptor := range metadata {
		metadataValues = append(metadataValues, descriptor)
	}

	return blocks, map[string]any{
		"kind":     openAIResponsesReplayKind,
		"version":  float64(openAIResponsesReplayVersion),
		"response": response,
		"blocks":   metadataValues,
	}, true
}

// findUnusedOpenAIResponsesBlock 按类型寻找尚未消耗的内容块。
func findUnusedOpenAIResponsesBlock(blocks []map[string]any, used map[int]struct{}, blockType string) (int, bool) {
	for index, block := range blocks {
		if _, consumed := used[index]; consumed {
			continue
		}
		if currentType, _ := block["type"].(string); currentType == blockType {
			return index, true
		}
	}
	return 0, false
}

// ReplayOpenAIResponsesCompact 用紧凑元数据与内容块重建 provider 原生 output items。
//
// 信封 kind, 版本, endpoint, model 任一不匹配, 或元数据与内容块无法一一对应时返回
// ok=false。调用方应把这一条 assistant 消息降级为普通文本节点, 而不是让整轮请求失败。
func ReplayOpenAIResponsesCompact(
	replayState map[string]any,
	contentBlocks []map[string]any,
	baseURL string,
	model string,
) ([]map[string]any, bool) {
	if len(replayState) == 0 {
		return nil, false
	}
	if kind, _ := replayState["kind"].(string); kind != openAIResponsesReplayKind {
		return nil, false
	}
	version, versionOK := replayState["version"].(float64)
	if !versionOK || int(version) != openAIResponsesReplayVersion {
		return nil, false
	}
	response, responseOK := replayState["response"].(map[string]any)
	if !responseOK {
		return nil, false
	}
	if stateEndpoint, _ := response["endpoint"].(string); stateEndpoint != normalizeOpenAIResponsesEndpoint(baseURL) {
		return nil, false
	}
	if stateModel, _ := response["model"].(string); stateModel != strings.TrimSpace(model) {
		return nil, false
	}
	descriptors, descriptorsOK := replayState["blocks"].([]any)
	if !descriptorsOK || len(descriptors) == 0 {
		return nil, false
	}

	used := make(map[int]struct{}, len(contentBlocks))
	items := make([]map[string]any, 0, len(descriptors))

	for _, rawDescriptor := range descriptors {
		descriptor, isRecord := rawDescriptor.(map[string]any)
		if !isRecord {
			return nil, false
		}
		switch descriptorType, _ := descriptor["type"].(string); descriptorType {
		case "reasoning":
			blockIndex, found := findUnusedOpenAIResponsesBlock(contentBlocks, used, "reasoning")
			if !found {
				return nil, false
			}
			used[blockIndex] = struct{}{}
			item := map[string]any{"type": "reasoning"}
			if itemID, _ := descriptor["id"].(string); itemID != "" {
				item["id"] = itemID
			}
			if encryptedContent, _ := descriptor["encryptedContent"].(string); encryptedContent != "" {
				item["encrypted_content"] = encryptedContent
			}
			if rawSummary, exists := descriptor["summary"]; exists && rawSummary != nil {
				clonedSummary, cloneOK := cloneOpenAIResponsesJSONValue(rawSummary)
				if !cloneOK {
					return nil, false
				}
				item["summary"] = clonedSummary
			} else if text, _ := contentBlocks[blockIndex]["text"].(string); text != "" {
				item["summary"] = []any{map[string]any{"type": "summary_text", "text": text}}
			} else {
				item["summary"] = []any{}
			}
			items = append(items, item)
		case "message":
			item := map[string]any{"type": "message", "role": "assistant", "content": []any{}}
			if role, _ := descriptor["role"].(string); role != "" {
				item["role"] = role
			}
			if itemID, _ := descriptor["id"].(string); itemID != "" {
				item["id"] = itemID
			}
			if hasText, _ := descriptor["hasText"].(bool); hasText {
				blockIndex, found := findUnusedOpenAIResponsesBlock(contentBlocks, used, "text")
				if !found {
					return nil, false
				}
				used[blockIndex] = struct{}{}
				text, isString := contentBlocks[blockIndex]["text"].(string)
				if !isString {
					return nil, false
				}
				item["content"] = []any{map[string]any{"type": "output_text", "text": text}}
			}
			items = append(items, item)
		case "function_call":
			callID, _ := descriptor["callId"].(string)
			name, _ := descriptor["name"].(string)
			if callID == "" || name == "" {
				return nil, false
			}
			blockIndex, found := findUnusedOpenAIResponsesBlock(contentBlocks, used, "tool_use")
			if !found {
				return nil, false
			}
			used[blockIndex] = struct{}{}
			arguments, _ := contentBlocks[blockIndex]["arguments"].(string)
			if arguments == "" {
				arguments = "{}"
			}
			item := map[string]any{
				"type":      "function_call",
				"call_id":   callID,
				"name":      name,
				"arguments": arguments,
			}
			if itemID, _ := descriptor["id"].(string); itemID != "" {
				item["id"] = itemID
			}
			items = append(items, item)
		default:
			return nil, false
		}
	}

	if len(used) != len(contentBlocks) {
		return nil, false
	}
	return items, true
}