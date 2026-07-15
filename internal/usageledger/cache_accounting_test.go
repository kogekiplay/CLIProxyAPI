package usageledger

import "testing"

func TestNormalizeCacheAccountingIncludedInput(t *testing.T) {
	got := NormalizeCacheAccounting(CacheInputContext{Provider: "codex"}, TokenUsage{
		InputTokens:         1000,
		CachedTokens:        300,
		CacheReadTokens:     300,
		CacheCreationTokens: 100,
	})
	if got.Mode != CacheInputModeIncluded || got.TotalInputTokens != 1000 || got.UncachedInputTokens != 600 || got.CachedTokens != 0 || got.CacheReadTokens != 300 || got.CacheCreationTokens != 100 {
		t.Fatalf("included accounting = %#v", got)
	}
}

func TestNormalizeCacheAccountingSeparateInput(t *testing.T) {
	got := NormalizeCacheAccounting(CacheInputContext{Provider: "claude"}, TokenUsage{
		InputTokens:         100,
		CachedTokens:        300,
		CacheReadTokens:     300,
		CacheCreationTokens: 50,
	})
	if got.Mode != CacheInputModeSeparate || got.TotalInputTokens != 450 || got.UncachedInputTokens != 100 || got.CachedTokens != 0 || got.CacheReadTokens != 300 || got.CacheCreationTokens != 50 {
		t.Fatalf("separate accounting = %#v", got)
	}
}

func TestNormalizeCacheAccountingExplicitModeWins(t *testing.T) {
	got := NormalizeCacheAccounting(CacheInputContext{
		ExplicitMode: CacheInputModeSeparate,
		Provider:     "codex",
		ExecutorType: "OpenAIExecutor",
	}, TokenUsage{InputTokens: 100, CacheReadTokens: 25})
	if got.Mode != CacheInputModeSeparate || got.TotalInputTokens != 125 || got.UncachedInputTokens != 100 {
		t.Fatalf("explicit accounting = %#v", got)
	}
}

func TestCacheHitRateUsesCanonicalBuckets(t *testing.T) {
	got := CacheHitRate(TokenUsage{
		InputTokens:         999,
		TotalInputTokens:    1000,
		CachedTokens:        100,
		CacheReadTokens:     300,
		CacheCreationTokens: 200,
	})
	if got != 0.4 {
		t.Fatalf("cache hit rate = %v, want 0.4", got)
	}
	if got := CacheHitRate(TokenUsage{InputTokens: 100, CacheReadTokens: 150}); got != 1 {
		t.Fatalf("clamped cache hit rate = %v, want 1", got)
	}
}
