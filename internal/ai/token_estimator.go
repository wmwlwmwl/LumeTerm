package ai

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"strings"
	"sync"
	"unicode"

	aiprovider "lumeterm/internal/ai/provider"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

type aiTokenEstimatorMultipliers struct {
	Word       float64
	Number     float64
	CJK        float64
	Symbol     float64
	MathSymbol float64
	URLDelim   float64
	AtSign     float64
	Emoji      float64
	Newline    float64
	Space      float64
	BasePad    int
}

var (
	aiTokenEstimatorEncodingMu       sync.RWMutex
	aiTokenEstimatorEncodingCache    = map[string]*tiktoken.Tiktoken{}
	aiTokenEstimatorEncodingErrCache = map[string]error{}
)

var aiTokenEstimatorMultipliersMap = map[string]aiTokenEstimatorMultipliers{
	"gemini": {
		Word: 1.15, Number: 2.8, CJK: 0.68, Symbol: 0.38, MathSymbol: 1.05, URLDelim: 1.2, AtSign: 2.5, Emoji: 1.08, Newline: 1.15, Space: 0.2, BasePad: 0,
	},
	"claude": {
		Word: 1.13, Number: 1.63, CJK: 1.21, Symbol: 0.4, MathSymbol: 4.52, URLDelim: 1.26, AtSign: 2.82, Emoji: 2.6, Newline: 0.89, Space: 0.39, BasePad: 0,
	},
	"openai": {
		Word: 1.02, Number: 1.55, CJK: 0.85, Symbol: 0.4, MathSymbol: 2.68, URLDelim: 1.0, AtSign: 2.0, Emoji: 2.12, Newline: 0.5, Space: 0.42, BasePad: 0,
	},
}

const (
	aiGenericImageTokenCost         = 520
	aiFallbackUnknownImageTokenCost = 300
)

func normalizeAITokenEstimatorModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func resolveAITokenEstimatorProviderFamily(model string) string {
	normalizedModel := normalizeAITokenEstimatorModel(model)
	switch {
	case strings.Contains(normalizedModel, "gemini"):
		return "gemini"
	case strings.Contains(normalizedModel, "claude"):
		return "claude"
	default:
		return "openai"
	}
}

func isAIOpenAICompatibleTokenizerModel(model string) bool {
	normalizedModel := normalizeAITokenEstimatorModel(model)
	if normalizedModel == "" {
		return false
	}
	if strings.Contains(normalizedModel, "gemini") || strings.Contains(normalizedModel, "claude") {
		return false
	}
	return strings.HasPrefix(normalizedModel, "gpt-") ||
		strings.HasPrefix(normalizedModel, "o1") ||
		strings.HasPrefix(normalizedModel, "o3") ||
		strings.HasPrefix(normalizedModel, "o4") ||
		strings.Contains(normalizedModel, "gpt-4o") ||
		strings.Contains(normalizedModel, "gpt-4.1") ||
		strings.Contains(normalizedModel, "gpt-4.5") ||
		strings.Contains(normalizedModel, "gpt-5") ||
		strings.Contains(normalizedModel, "text-embedding") ||
		strings.Contains(normalizedModel, "omni") ||
		strings.Contains(normalizedModel, "computer-use-preview") ||
		strings.Contains(normalizedModel, "codex")
}

func resolveAITokenEncodingNameForModel(model string) string {
	normalizedModel := normalizeAITokenEstimatorModel(model)
	switch {
	case strings.Contains(normalizedModel, "gpt-4o"),
		strings.Contains(normalizedModel, "gpt-4.1"),
		strings.Contains(normalizedModel, "gpt-4.5"),
		strings.Contains(normalizedModel, "gpt-5"),
		strings.HasPrefix(normalizedModel, "o1"),
		strings.HasPrefix(normalizedModel, "o3"),
		strings.HasPrefix(normalizedModel, "o4"),
		strings.Contains(normalizedModel, "omni"),
		strings.Contains(normalizedModel, "computer-use-preview"):
		return "o200k_base"
	default:
		return "cl100k_base"
	}
}

func getAITokenEncodingForModel(model string) (*tiktoken.Tiktoken, error) {
	encodingName := resolveAITokenEncodingNameForModel(model)
	aiTokenEstimatorEncodingMu.RLock()
	cachedEncoding := aiTokenEstimatorEncodingCache[encodingName]
	cachedErr := aiTokenEstimatorEncodingErrCache[encodingName]
	aiTokenEstimatorEncodingMu.RUnlock()
	if cachedEncoding != nil || cachedErr != nil {
		return cachedEncoding, cachedErr
	}
	encoding, err := tiktoken.GetEncoding(encodingName)
	aiTokenEstimatorEncodingMu.Lock()
	aiTokenEstimatorEncodingCache[encodingName] = encoding
	aiTokenEstimatorEncodingErrCache[encodingName] = err
	aiTokenEstimatorEncodingMu.Unlock()
	return encoding, err
}

func countAIExactTextTokensByModel(text string, model string) (int, bool) {
	if strings.TrimSpace(text) == "" {
		return 0, true
	}
	encoding, err := getAITokenEncodingForModel(model)
	if err != nil || encoding == nil {
		return 0, false
	}
	return len(encoding.Encode(text, nil, nil)), true
}

func estimateAIResultTextTokens(rawResultContent string, profile AIProviderProfile) int {
	if strings.TrimSpace(rawResultContent) == "" {
		return 0
	}
	return estimateAITextTokensForProfile(rawResultContent, profile)
}

func estimateAIFallbackImageTokensFromData(data string) int {
	_, base64Data, ok := aiprovider.ParseBase64DataURL(data)
	if !ok {
		return aiFallbackUnknownImageTokenCost
	}
	if base64Data == "" {
		return 0
	}
	return int(math.Ceil(math.Sqrt(float64(len(base64Data)))))
}

func getAITokenEstimatorMultipliers(model string) aiTokenEstimatorMultipliers {
	if multipliers, ok := aiTokenEstimatorMultipliersMap[resolveAITokenEstimatorProviderFamily(model)]; ok {
		return multipliers
	}
	return aiTokenEstimatorMultipliersMap["openai"]
}

func isAITokenEstimatorCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		(r >= 0x3040 && r <= 0x30FF) ||
		(r >= 0xAC00 && r <= 0xD7A3)
}

func isAITokenEstimatorLatinOrNumber(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

func isAITokenEstimatorEmoji(r rune) bool {
	return (r >= 0x1F300 && r <= 0x1F9FF) ||
		(r >= 0x2600 && r <= 0x26FF) ||
		(r >= 0x2700 && r <= 0x27BF) ||
		(r >= 0x1F600 && r <= 0x1F64F) ||
		(r >= 0x1F900 && r <= 0x1F9FF) ||
		(r >= 0x1FA00 && r <= 0x1FAFF)
}

func isAITokenEstimatorMathSymbol(r rune) bool {
	mathSymbols := "∑∫∂√∞≤≥≠≈±×÷∈∉∋∌⊂⊃⊆⊇∪∩∧∨¬∀∃∄∅∆∇∝∟∠∡∢°′″‴⁺⁻⁼⁽⁾ⁿ₀₁₂₃₄₅₆₇₈₉₊₋₌₍₎²³¹⁴⁵⁶⁷⁸⁹⁰"
	for _, symbol := range mathSymbols {
		if r == symbol {
			return true
		}
	}
	return (r >= 0x2200 && r <= 0x22FF) ||
		(r >= 0x2A00 && r <= 0x2AFF) ||
		(r >= 0x1D400 && r <= 0x1D7FF)
}

func isAITokenEstimatorURLDelim(r rune) bool {
	for _, delimiter := range "/:?&=;#%" {
		if r == delimiter {
			return true
		}
	}
	return false
}

func estimateAIHeuristicTextTokens(text string, model string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	multipliers := getAITokenEstimatorMultipliers(model)
	count := 0.0
	const (
		wordNone = iota
		wordLatin
		wordNumber
	)
	currentWordType := wordNone
	for _, r := range text {
		if unicode.IsSpace(r) {
			currentWordType = wordNone
			if r == '\n' || r == '\t' {
				count += multipliers.Newline
			} else {
				count += multipliers.Space
			}
			continue
		}
		if isAITokenEstimatorCJK(r) {
			currentWordType = wordNone
			count += multipliers.CJK
			continue
		}
		if isAITokenEstimatorEmoji(r) {
			currentWordType = wordNone
			count += multipliers.Emoji
			continue
		}
		if isAITokenEstimatorLatinOrNumber(r) {
			nextWordType := wordLatin
			if unicode.IsNumber(r) {
				nextWordType = wordNumber
			}
			if currentWordType == wordNone || currentWordType != nextWordType {
				if nextWordType == wordNumber {
					count += multipliers.Number
				} else {
					count += multipliers.Word
				}
				currentWordType = nextWordType
			}
			continue
		}
		currentWordType = wordNone
		switch {
		case isAITokenEstimatorMathSymbol(r):
			count += multipliers.MathSymbol
		case r == '@':
			count += multipliers.AtSign
		case isAITokenEstimatorURLDelim(r):
			count += multipliers.URLDelim
		default:
			count += multipliers.Symbol
		}
	}
	return int(math.Ceil(count)) + multipliers.BasePad
}

func estimateAITextTokensForProfile(text string, profile AIProviderProfile) int {
	normalizedModel := normalizeAITokenEstimatorModel(profile.Model)
	if isAIOpenAICompatibleTokenizerModel(normalizedModel) {
		if exactCount, ok := countAIExactTextTokensByModel(text, normalizedModel); ok {
			return exactCount
		}
	}
	return estimateAIHeuristicTextTokens(text, normalizedModel)
}

func decodeAIImageDimensions(data string) (int, int, bool) {
	_, base64Data, ok := aiprovider.ParseBase64DataURL(data)
	if !ok || strings.TrimSpace(base64Data) == "" {
		return 0, 0, false
	}
	decodedData, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil || len(decodedData) == 0 {
		return 0, 0, false
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(decodedData))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return 0, 0, false
	}
	return config.Width, config.Height, true
}

func resolveAIImageTileConfig(model string) (int, int) {
	normalizedModel := normalizeAITokenEstimatorModel(model)
	baseTokens := 85
	tileTokens := 170
	switch {
	case strings.HasPrefix(normalizedModel, "gpt-4o-mini"):
		baseTokens = 2833
		tileTokens = 5667
	case strings.HasPrefix(normalizedModel, "gpt-5-chat-latest") || (strings.HasPrefix(normalizedModel, "gpt-5") && !strings.Contains(normalizedModel, "mini") && !strings.Contains(normalizedModel, "nano")):
		baseTokens = 70
		tileTokens = 140
	case strings.HasPrefix(normalizedModel, "o1") || strings.HasPrefix(normalizedModel, "o3") || strings.HasPrefix(normalizedModel, "o1-pro"):
		baseTokens = 75
		tileTokens = 150
	case strings.Contains(normalizedModel, "computer-use-preview"):
		baseTokens = 65
		tileTokens = 129
	}
	return baseTokens, tileTokens
}

func resolveAIImagePatchMultiplier(model string) (float64, bool) {
	normalizedModel := normalizeAITokenEstimatorModel(model)
	switch {
	case strings.Contains(normalizedModel, "gpt-4.1-mini"):
		return 1.62, true
	case strings.Contains(normalizedModel, "gpt-4.1-nano"):
		return 2.46, true
	case strings.HasPrefix(normalizedModel, "o4-mini"):
		return 1.72, true
	case strings.HasPrefix(normalizedModel, "gpt-5-mini"):
		return 1.62, true
	case strings.HasPrefix(normalizedModel, "gpt-5-nano"):
		return 2.46, true
	default:
		return 0, false
	}
}

func estimateAIImagePatchTokens(width int, height int, multiplier float64) int {
	ceilDiv := func(value int, divisor int) int {
		return (value + divisor - 1) / divisor
	}
	rawPatchesWidth := ceilDiv(width, 32)
	rawPatchesHeight := ceilDiv(height, 32)
	rawPatches := rawPatchesWidth * rawPatchesHeight
	if rawPatches > 1536 {
		area := float64(width * height)
		scaleRatio := math.Sqrt(float64(32*32*1536) / area)
		scaledWidth := float64(width) * scaleRatio
		scaledHeight := float64(height) * scaleRatio
		adjustWidth := math.Floor(scaledWidth/32.0) / (scaledWidth / 32.0)
		adjustHeight := math.Floor(scaledHeight/32.0) / (scaledHeight / 32.0)
		adjust := math.Min(adjustWidth, adjustHeight)
		if !math.IsNaN(adjust) && adjust > 0 {
			scaleRatio *= adjust
		}
		scaledWidth = float64(width) * scaleRatio
		scaledHeight = float64(height) * scaleRatio
		patchesWidth := math.Ceil(scaledWidth / 32.0)
		patchesHeight := math.Ceil(scaledHeight / 32.0)
		imageTokens := int(patchesWidth * patchesHeight)
		if imageTokens > 1536 {
			imageTokens = 1536
		}
		return int(math.Round(float64(imageTokens) * multiplier))
	}
	return int(math.Round(float64(rawPatches) * multiplier))
}

func estimateAIImageTileTokens(width int, height int, baseTokens int, tileTokens int) int {
	maxSide := math.Max(float64(width), float64(height))
	fitScale := 1.0
	if maxSide > 2048 {
		fitScale = maxSide / 2048.0
	}
	fitWidth := int(math.Round(float64(width) / fitScale))
	fitHeight := int(math.Round(float64(height) / fitScale))
	minSide := math.Min(float64(fitWidth), float64(fitHeight))
	if minSide == 0 {
		return baseTokens
	}
	shortScale := 768.0 / minSide
	finalWidth := int(math.Round(float64(fitWidth) * shortScale))
	finalHeight := int(math.Round(float64(fitHeight) * shortScale))
	tilesWidth := (finalWidth + 512 - 1) / 512
	tilesHeight := (finalHeight + 512 - 1) / 512
	return tilesWidth*tilesHeight*tileTokens + baseTokens
}

func estimateAIImageTokensForProfile(data string, profile AIProviderProfile) int {
	normalizedModel := normalizeAITokenEstimatorModel(profile.Model)
	if strings.HasPrefix(normalizedModel, "glm-4") {
		return 1047
	}
	if !isAIOpenAICompatibleTokenizerModel(normalizedModel) {
		return aiGenericImageTokenCost
	}
	width, height, ok := decodeAIImageDimensions(data)
	if !ok || width <= 0 || height <= 0 {
		fallbackEstimate := estimateAIFallbackImageTokensFromData(data)
		if fallbackEstimate > 0 {
			return fallbackEstimate
		}
		return aiFallbackUnknownImageTokenCost
	}
	if multiplier, patchBased := resolveAIImagePatchMultiplier(normalizedModel); patchBased {
		return estimateAIImagePatchTokens(width, height, multiplier)
	}
	baseTokens, tileTokens := resolveAIImageTileConfig(normalizedModel)
	return estimateAIImageTileTokens(width, height, baseTokens, tileTokens)
}

func countAIConversationTokenBlocksWithProfile(blocks []TokenCountBlock, profile AIProviderProfile) int {
	totalTokens := 0
	for _, block := range blocks {
		switch block.Type {
		case "text":
			totalTokens += estimateAITextTokensForProfile(block.Text, profile)
		case "image":
			if strings.TrimSpace(block.Data) != "" {
				totalTokens += estimateAIImageTokensForProfile(block.Data, profile)
			}
		}
	}
	return totalTokens
}

func estimateAIConversationMessageOverhead(message AIConversationAPIMessage, profile AIProviderProfile) int {
	normalizedModel := normalizeAITokenEstimatorModel(profile.Model)
	if isAIOpenAICompatibleTokenizerModel(normalizedModel) || normalizeAIProviderProtocol(profile.Provider) == "Responses" {
		return 3
	}
	return 0
}

func estimateAIConversationMessageTokens(message AIConversationAPIMessage, profile AIProviderProfile) int {
	return estimateAIConversationMessageOverhead(message, profile) + countAIConversationTokenBlocksWithProfile(buildAIConversationAPIMessageTokenBlocks(message, profile), profile)
}