package usageledger

import "strings"

const (
	CacheInputModeIncluded         = "included_in_input"
	CacheInputModeSeparate         = "separate_from_input"
	LongContextInputTokenThreshold = int64(272_000)
)

// CacheAccounting is the canonical input-token view used by analytics and pricing.
// Raw provider counters remain stored separately on the usage event.
type CacheAccounting struct {
	Mode                string
	UncachedInputTokens int64
	TotalInputTokens    int64
	CachedTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

type CacheInputContext struct {
	ExplicitMode string
	ExecutorType string
	Provider     string
	Model        string
}

func NormalizeCacheAccounting(context CacheInputContext, tokens TokenUsage) CacheAccounting {
	mode := InferCacheInputMode(context, tokens.CacheReadTokens, tokens.CacheCreationTokens)
	input := maxInt64(tokens.InputTokens, 0)
	cacheRead := maxInt64(tokens.CacheReadTokens, 0)
	cacheCreation := maxInt64(tokens.CacheCreationTokens, 0)
	compatibleCached := maxInt64(tokens.CachedTokens-cacheRead-cacheCreation, 0)

	accounting := CacheAccounting{
		Mode:                mode,
		CachedTokens:        compatibleCached,
		CacheReadTokens:     cacheRead,
		CacheCreationTokens: cacheCreation,
	}
	if mode == CacheInputModeSeparate {
		accounting.UncachedInputTokens = input
		accounting.TotalInputTokens = input + compatibleCached + cacheRead + cacheCreation
		return accounting
	}
	accounting.UncachedInputTokens = maxInt64(input-compatibleCached-cacheRead-cacheCreation, 0)
	accounting.TotalInputTokens = input
	return accounting
}

// CacheHitRate returns a normalized 0..1 ratio. Cache creation is excluded
// because a write is not a cache hit.
func CacheHitRate(tokens TokenUsage) float64 {
	totalInput := maxInt64(tokens.TotalInputTokens, 0)
	if totalInput == 0 {
		totalInput = maxInt64(tokens.InputTokens, 0)
	}
	if totalInput == 0 {
		return 0
	}
	hitTokens := maxInt64(tokens.CachedTokens, 0) + maxInt64(tokens.CacheReadTokens, 0)
	if hitTokens >= totalInput {
		return 1
	}
	return float64(hitTokens) / float64(totalInput)
}

func InferCacheInputMode(context CacheInputContext, cacheReadTokens, cacheCreationTokens int64) string {
	if mode := normalizedCacheInputMode(context.ExplicitMode); mode != "" {
		return mode
	}
	if mode, ok := classifyExecutorCacheInputMode(context.ExecutorType); ok {
		return mode
	}
	if mode, ok := classifyProviderCacheInputMode(context.Provider); ok {
		return mode
	}
	if mode, ok := classifyModelCacheInputMode(context.Model); ok {
		return mode
	}
	if cacheReadTokens > 0 || cacheCreationTokens > 0 {
		return CacheInputModeSeparate
	}
	return CacheInputModeIncluded
}

func normalizedCacheInputMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case CacheInputModeIncluded:
		return CacheInputModeIncluded
	case CacheInputModeSeparate:
		return CacheInputModeSeparate
	default:
		return ""
	}
}

func classifyExecutorCacheInputMode(executorType string) (string, bool) {
	executor := strings.ToLower(strings.TrimSpace(executorType))
	if executor == "" {
		return "", false
	}
	if strings.Contains(executor, "claude") {
		return CacheInputModeSeparate, true
	}
	for _, marker := range []string{
		"openaicompat", "openai_compat", "openai-compat", "openai", "codex",
		"gemini", "aistudio", "ai_studio", "ai-studio", "antigravity",
		"interaction", "xai", "kimi", "opencode",
	} {
		if strings.Contains(executor, marker) {
			return CacheInputModeIncluded, true
		}
	}
	return "", false
}

func classifyProviderCacheInputMode(provider string) (string, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return "", false
	}
	if strings.Contains(provider, "anthropic") || strings.Contains(provider, "claude") {
		return CacheInputModeSeparate, true
	}
	for _, marker := range []string{
		"openai", "codex", "gemini", "vertex", "aistudio", "ai_studio",
		"ai-studio", "interaction", "antigravity", "xai", "kimi", "moonshot",
		"opencode",
	} {
		if strings.Contains(provider, marker) {
			return CacheInputModeIncluded, true
		}
	}
	return "", false
}

func classifyModelCacheInputMode(model string) (string, bool) {
	model = normalizedModelSlug(model)
	if model == "" {
		return "", false
	}
	if strings.Contains(model, "anthropic") || strings.Contains(model, "claude") {
		return CacheInputModeSeparate, true
	}
	for _, marker := range []string{
		"gpt-", "openai", "codex", "gemini", "vertex", "aistudio",
		"antigravity", "grok", "xai", "kimi", "moonshot", "opencode",
	} {
		if strings.Contains(model, marker) {
			return CacheInputModeIncluded, true
		}
	}
	return "", false
}

func normalizedModelSlug(model string) string {
	model = strings.ToLower(strings.TrimSpace(modelWithoutReasoningSuffix(model)))
	if slash := strings.LastIndex(model, "/"); slash >= 0 {
		model = model[slash+1:]
	}
	return model
}

func supportsLongContextPremium(model string) bool {
	slug := normalizedModelSlug(model)
	return isModelFamily(slug, "gpt-5.6") ||
		slug == "gpt-5.5" || strings.HasPrefix(slug, "gpt-5.5-20") ||
		slug == "gpt-5.4" || strings.HasPrefix(slug, "gpt-5.4-20") ||
		slug == "gpt-5.4-pro" || strings.HasPrefix(slug, "gpt-5.4-pro-20")
}

func isModelFamily(model, family string) bool {
	model = normalizedModelSlug(model)
	return model == family || strings.HasPrefix(model, family+"-")
}
