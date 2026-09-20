package terminalstream

import (
	"bytes"
	"encoding/base64"
	"strings"
)

var commandMarkerStart = []byte("\x1fLUMETERM_CMD\x1f")
var cwdMarkerStart = []byte("\x1fLUMETERM_CWD\x1f")

const markerEnd byte = 0x1e
const markerPayloadMax = 64 * 1024

const (
	markerKindNone byte = iota
	markerKindCommand
	markerKindCWD
)

type TextDecoder func([]byte) string

type CommandHistoryParser struct {
	visibleCarry []byte
	payloadCarry []byte
	inMarker     bool
	markerKind   byte
	lastCommand  string
	decoder      TextDecoder
}

func NewCommandHistoryParser(decoder TextDecoder) *CommandHistoryParser {
	if decoder == nil {
		decoder = func(data []byte) string { return string(data) }
	}
	return &CommandHistoryParser{decoder: decoder}
}

func (p *CommandHistoryParser) resetMarkerState() {
	p.payloadCarry = p.payloadCarry[:0]
	p.inMarker = false
	p.markerKind = markerKindNone
}

func (p *CommandHistoryParser) Process(chunk []byte) ([]byte, []string, string, bool) {
	if len(chunk) == 0 {
		return nil, nil, "", false
	}

	if len(p.visibleCarry) == 0 && !p.inMarker && !bytes.Contains(chunk, commandMarkerStart) && !bytes.Contains(chunk, cwdMarkerStart) && trailingMarkerPrefixLen(chunk, commandMarkerStart, cwdMarkerStart) == 0 {
		return chunk, nil, "", false
	}

	data := append(append([]byte{}, p.visibleCarry...), chunk...)
	p.visibleCarry = p.visibleCarry[:0]

	out := make([]byte, 0, len(data))
	commands := make([]string, 0, 1)
	cwd := ""
	promptSeen := false

	for i := 0; i < len(data); {
		if p.inMarker {
			relEnd := bytes.IndexByte(data[i:], markerEnd)
			if relEnd == -1 {
				remaining := data[i:]
				if len(p.payloadCarry)+len(remaining) > markerPayloadMax {
					p.resetMarkerState()
					out = append(out, remaining...)
					return out, commands, cwd, promptSeen
				}
				p.payloadCarry = append(p.payloadCarry, remaining...)
				return out, commands, cwd, promptSeen
			}

			end := i + relEnd
			if len(p.payloadCarry)+relEnd > markerPayloadMax {
				p.resetMarkerState()
				i = end + 1
				continue
			}
			p.payloadCarry = append(p.payloadCarry, data[i:end]...)
			promptSeen = true
			if p.markerKind == markerKindCommand {
				if command := p.decodeCommand(p.payloadCarry); command != "" && command != p.lastCommand {
					commands = append(commands, command)
					p.lastCommand = command
				}
			} else if p.markerKind == markerKindCWD {
				if nextCWD := p.decodeCWD(p.payloadCarry); nextCWD != "" {
					cwd = nextCWD
				}
			}
			p.resetMarkerState()
			i = end + 1
			continue
		}

		relCommandStart := bytes.Index(data[i:], commandMarkerStart)
		relCWDStart := bytes.Index(data[i:], cwdMarkerStart)
		relStart := -1
		var marker []byte
		markerKind := markerKindNone

		if relCommandStart != -1 {
			relStart = relCommandStart
			marker = commandMarkerStart
			markerKind = markerKindCommand
		}
		if relCWDStart != -1 && (relStart == -1 || relCWDStart < relStart) {
			relStart = relCWDStart
			marker = cwdMarkerStart
			markerKind = markerKindCWD
		}

		if relStart == -1 {
			remaining := data[i:]
			overlap := trailingMarkerPrefixLen(remaining, commandMarkerStart, cwdMarkerStart)
			visibleEnd := len(remaining) - overlap
			if visibleEnd > 0 {
				out = append(out, remaining[:visibleEnd]...)
			}
			if overlap > 0 {
				p.visibleCarry = append(p.visibleCarry, remaining[visibleEnd:]...)
			}
			return out, commands, cwd, promptSeen
		}

		start := i + relStart
		if start > i {
			out = append(out, data[i:start]...)
		}
		i = start + len(marker)
		p.inMarker = true
		p.markerKind = markerKind
	}

	return out, commands, cwd, promptSeen
}

func (p *CommandHistoryParser) decodeCommand(payload []byte) string {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(payload)))
	if err != nil {
		return ""
	}
	command := strings.TrimSpace(p.decoder(decoded))
	if isInteractivePrompt(command) {
		return ""
	}
	return command
}

func (p *CommandHistoryParser) decodeCWD(payload []byte) string {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(payload)))
	if err != nil {
		return ""
	}
	cwd := strings.TrimSpace(p.decoder(decoded))
	if cwd == "" || !strings.HasPrefix(cwd, "/") {
		return ""
	}
	return cwd
}

func isInteractivePrompt(command string) bool {
	text := strings.TrimSpace(command)
	lower := strings.ToLower(text)
	if text == "" {
		return true
	}
	// 1) 英文前缀
	for _, prefix := range []string{"choose ", "select ", "enter ", "input ", "please enter ", "press enter ", "would you like ", "do you have ", "port to use "} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	// 2) 中文前缀：含"请"的 + 常见安装脚本裸动词（设置/配置/指定/填写/输入/选择/确认/继续）
	zhPrefixes := []string{"请设置", "请输入", "请选择", "请确认", "请指定", "请填写",
		"请提供", "是否", "是否需要", "是否继续", "是否确认", "按回车", "回车确认", "请按",
		"设置", "配置", "指定", "填写", "输入", "选择", "确认", "继续"}
	for _, prefix := range zhPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	// 3) 提问结构探测：行内含提问痕迹（冒号/问号、(默认)默认值括号、方括号选择格式、按回车继续类句式）
	//    这层不要求行尾是冒号，"回答后行尾是用户输入"的场景也能命中。
	hasColonOrQM := strings.ContainsAny(text, ":?：？")
	hasDefaultBracket := strings.Contains(text, "（默认") || strings.Contains(text, "(默认")
	hasBracketChoice := strings.Contains(text, "[") && strings.Contains(text, "]") &&
		(strings.ContainsAny(text, "ynYN/") || strings.ContainsAny(text, "0123456789-"))
	hasEnterPhrase := (strings.Contains(text, "回车") && (strings.Contains(text, "按") || strings.Contains(text, "继续"))) ||
		strings.Contains(text, "继续执行")
	hasPromptStructure := hasColonOrQM || hasDefaultBracket || hasBracketChoice || hasEnterPhrase
	if hasPromptStructure {
		// 3.1) 英文关键词：default / leave empty / skip / y/n / yes/no / option / selection
		//      + 安装脚本高频词：password / username / user / port / host / ip / path / dir / directory
		for _, marker := range []string{"default", "leave empty", "skip", "y/n", "yes/no", "option", "selection",
			"password", "passwd", "username", "user", "port", "host", "ip", "path", "dir", "directory"} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
		// 3.2) 中文关键词：默认 / 留空 / 跳过 / 可选 / 选项 / 是/否 / 回车 / 确认 / 取消
		//      + 安装脚本高频词：用户名 / 密码 / 口令 / 账号 / 端口 / SSH / 安全入口 / 面板 / 监听 / 版本号
		//      + 路径类：路径 / 目录 / 数据目录 / 安装路径 / 主机 / 地址
		zhMarkers := []string{"默认", "留空", "跳过", "可选", "选项", "是/否", "回车", "确认", "取消",
			"（默认", "(默认", "可直接回车", "按回车继续", "回车确认",
			"用户名", "密码", "口令", "账号", "端口", "SSH", "安全入口", "面板", "入口", "监听", "版本号",
			"路径", "目录", "数据目录", "安装路径", "主机", "地址", "是否", "继续执行", "是否确认"}
		for _, marker := range zhMarkers {
			if strings.Contains(text, marker) {
				return true
			}
		}
	}
	// 4) 兜底：行尾就是【冒号/问号 (+ 可选数字)】，典型"等待输入光标前最后一个字符"形式
	trimEnd := strings.TrimRight(text, " \t")
	if strings.HasSuffix(trimEnd, ":") || strings.HasSuffix(trimEnd, "?") ||
		strings.HasSuffix(trimEnd, "：") || strings.HasSuffix(trimEnd, "？") {
		return true
	}
	// 5) 兜底：方括号选择格式行尾 [y/n] / [0] / [1-23] ...
	if hasBracketChoice {
		suffix := trimEnd
		if idx := strings.LastIndex(suffix, "]"); idx >= 0 {
			// 结尾附近有 "]"，且后续只有空白或数字
			tail := strings.TrimSpace(suffix[idx+1:])
			if tail == "" || isAllDigits(tail) {
				return true
			}
		}
	}
	return false
}

// isAllDigits 判断字符串是否全部是十进制数字（ponytail: 仅 0-9，不含 Unicode 其它数字字符，够用就好）
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func trailingMarkerPrefixLen(data []byte, markers ...[]byte) int {
	best := 0
	for _, marker := range markers {
		limit := len(marker)
		if len(data) < limit {
			limit = len(data)
		}
		for size := limit; size > best; size-- {
			if bytes.Equal(data[len(data)-size:], marker[:size]) {
				best = size
				break
			}
		}
	}
	return best
}
