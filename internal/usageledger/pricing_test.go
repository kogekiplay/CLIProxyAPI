package usageledger_test

import (
	"math"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/usageledger"
)

func TestCostForUsageSeparatesCacheBuckets(t *testing.T) {
	prices := []usageledger.ModelPrice{{
		Model:              "gpt-5.5",
		InputPer1M:         10,
		OutputPer1M:        20,
		CacheReadPer1M:     1,
		CacheCreationPer1M: 5,
	}}
	tokens := usageledger.TokenUsage{
		InputTokens:         1000,
		OutputTokens:        2000,
		CacheReadTokens:     300,
		CacheCreationTokens: 100,
	}

	cost, ok, missing := usageledger.CostForUsage("gpt-5.5", tokens, prices)
	if !ok || len(missing) != 0 {
		t.Fatalf("cost missing: ok=%v missing=%v", ok, missing)
	}

	want := float64(600)/1_000_000*10 +
		float64(2000)/1_000_000*20 +
		float64(300)/1_000_000*1 +
		float64(100)/1_000_000*5
	if math.Abs(cost-want) > 0.0000001 {
		t.Fatalf("cost = %v, want %v", cost, want)
	}
}

func TestCostForUsageFallsBackToCachedPerMillion(t *testing.T) {
	prices := []usageledger.ModelPrice{{
		Model:       "legacy-cache-model",
		InputPer1M:  2,
		OutputPer1M: 4,
		CachedPer1M: 0.5,
	}}
	tokens := usageledger.TokenUsage{
		InputTokens:  1000,
		OutputTokens: 1000,
		CachedTokens: 400,
	}

	cost, ok, missing := usageledger.CostForUsage("legacy-cache-model", tokens, prices)
	if !ok || len(missing) != 0 {
		t.Fatalf("cost missing: ok=%v missing=%v", ok, missing)
	}

	want := float64(600)/1_000_000*2 +
		float64(1000)/1_000_000*4 +
		float64(400)/1_000_000*0.5
	if math.Abs(cost-want) > 0.0000001 {
		t.Fatalf("cost = %v, want %v", cost, want)
	}
}

func TestCostForUsageMatchesWildcardAfterExact(t *testing.T) {
	prices := []usageledger.ModelPrice{
		{Model: "gpt-5*", InputPer1M: 1, OutputPer1M: 1},
		{Model: "gpt-5.5", InputPer1M: 10, OutputPer1M: 20},
	}
	tokens := usageledger.TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	exactCost, ok, missing := usageledger.CostForUsage("gpt-5.5", tokens, prices)
	if !ok || len(missing) != 0 {
		t.Fatalf("exact cost missing: ok=%v missing=%v", ok, missing)
	}
	if exactCost != 50 {
		t.Fatalf("exact cost = %v, want 50", exactCost)
	}

	wildcardCost, ok, missing := usageledger.CostForUsage("gpt-5.3-codex-spark", tokens, prices)
	if !ok || len(missing) != 0 {
		t.Fatalf("wildcard cost missing: ok=%v missing=%v", ok, missing)
	}
	if wildcardCost != 2 {
		t.Fatalf("wildcard cost = %v, want 2", wildcardCost)
	}
}

func TestCostForUsageMatchesReasoningSuffixToBaseModel(t *testing.T) {
	prices := []usageledger.ModelPrice{{
		Model:              "gpt-5.6-sol",
		InputPer1M:         5,
		OutputPer1M:        30,
		CacheReadPer1M:     0.5,
		CacheCreationPer1M: 6.25,
	}}
	tokens := usageledger.TokenUsage{
		InputTokens:         3_000_000,
		OutputTokens:        1_000_000,
		CacheReadTokens:     1_000_000,
		CacheCreationTokens: 1_000_000,
	}

	cost, ok, missing := usageledger.CostForUsage("gpt-5.6-sol(xhigh)", tokens, prices)
	if !ok || len(missing) != 0 {
		t.Fatalf("reasoning-suffix cost missing: ok=%v missing=%v", ok, missing)
	}
	if cost != 68.5 {
		t.Fatalf("reasoning-suffix cost = %v, want 68.5", cost)
	}
}

func TestCostForUsageLongContextStartsAbove272K(t *testing.T) {
	prices := []usageledger.ModelPrice{{
		Model:       "gpt-5.6-sol",
		InputPer1M:  5,
		OutputPer1M: 30,
	}}

	atBoundary, ok, _ := usageledger.CostForUsage("gpt-5.6-sol", usageledger.TokenUsage{
		InputTokens:  272_000,
		OutputTokens: 100,
	}, prices)
	aboveBoundary, okAbove, _ := usageledger.CostForUsage("gpt-5.6-sol", usageledger.TokenUsage{
		InputTokens:  272_001,
		OutputTokens: 100,
	}, prices)
	if !ok || !okAbove {
		t.Fatal("expected both requests to be priced")
	}
	wantBoundary := float64(272_000)/1_000_000*5 + float64(100)/1_000_000*30
	wantAbove := float64(272_001)/1_000_000*10 + float64(100)/1_000_000*45
	if math.Abs(atBoundary-wantBoundary) > 0.0000001 {
		t.Fatalf("272K cost = %v, want %v", atBoundary, wantBoundary)
	}
	if math.Abs(aboveBoundary-wantAbove) > 0.0000001 {
		t.Fatalf("272K+1 cost = %v, want %v", aboveBoundary, wantAbove)
	}
}

func TestCostForUsageServiceTierMultipliers(t *testing.T) {
	prices := []usageledger.ModelPrice{{
		Model:       "gpt-5.6-sol",
		InputPer1M:  5,
		OutputPer1M: 30,
	}}
	short := usageledger.TokenUsage{InputTokens: 100_000, OutputTokens: 10_000}
	long := usageledger.TokenUsage{InputTokens: 300_000, OutputTokens: 10_000}

	priorityShort, ok, _ := usageledger.CostForUsageWithServiceTier("gpt-5.6-sol", "priority", short, prices)
	if !ok || math.Abs(priorityShort-1.6) > 0.0000001 {
		t.Fatalf("short priority cost = %v, want 1.6", priorityShort)
	}
	priorityLong, ok, _ := usageledger.CostForUsageWithServiceTier("gpt-5.6-sol", "priority", long, prices)
	if !ok || math.Abs(priorityLong-3.45) > 0.0000001 {
		t.Fatalf("long priority cost = %v, want 3.45 without priority stacking", priorityLong)
	}
	flexLong, ok, _ := usageledger.CostForUsageWithServiceTier("gpt-5.6-sol", "flex", long, prices)
	if !ok || math.Abs(flexLong-1.725) > 0.0000001 {
		t.Fatalf("long flex cost = %v, want 1.725", flexLong)
	}
	batchShort, ok, _ := usageledger.CostForUsageWithServiceTier("gpt-5.6-sol", "batch", short, prices)
	if !ok || math.Abs(batchShort-0.4) > 0.0000001 {
		t.Fatalf("short batch cost = %v, want 0.4", batchShort)
	}
}

func TestCostForUsageDoesNotDoubleBillReasoningTokens(t *testing.T) {
	prices := []usageledger.ModelPrice{{Model: "test-model", OutputPer1M: 10}}
	tokens := usageledger.TokenUsage{OutputTokens: 1_000_000, ReasoningTokens: 400_000}

	cost, ok, _ := usageledger.CostForUsage("test-model", tokens, prices)
	if !ok || cost != 10 {
		t.Fatalf("cost = %v, want 10", cost)
	}
}

func TestCostForUsageAppliesCacheWriteFallbackOnlyToManagedPrices(t *testing.T) {
	tokens := usageledger.TokenUsage{
		InputTokens:         100,
		TotalInputTokens:    100,
		UncachedInputTokens: 0,
		CacheCreationTokens: 100,
	}
	managed := []usageledger.ModelPrice{{
		Model:       "gpt-5.6-sol",
		InputPer1M:  8,
		OutputPer1M: 1,
		Source:      "built-in",
	}}
	manual := []usageledger.ModelPrice{{
		Model:       "gpt-5.6-sol",
		InputPer1M:  8,
		OutputPer1M: 1,
		Source:      "manual",
	}}

	managedCost, ok, _ := usageledger.CostForUsage("gpt-5.6-sol", tokens, managed)
	if !ok || math.Abs(managedCost-0.001) > 0.0000001 {
		t.Fatalf("managed cache-write cost = %v, want 0.001", managedCost)
	}
	manualCost, ok, _ := usageledger.CostForUsage("gpt-5.6-sol", tokens, manual)
	if !ok || manualCost != 0 {
		t.Fatalf("manual cache-write cost = %v, want 0", manualCost)
	}
}

func TestCostForUsageUsesSourceModelForAliasBillingRules(t *testing.T) {
	prices := []usageledger.ModelPrice{{
		Model:         "cf-worker",
		SourceModelID: "gpt-5.6-sol",
		InputPer1M:    5,
		OutputPer1M:   30,
		Source:        "manual",
	}}

	cost, ok, _ := usageledger.CostForUsageWithServiceTier(
		"cf-worker",
		"priority",
		usageledger.TokenUsage{InputTokens: 300_000},
		prices,
	)
	if !ok || math.Abs(cost-3) > 0.0000001 {
		t.Fatalf("alias long-context cost = %v, want 3", cost)
	}
}

func TestCostForUsageMissingPrice(t *testing.T) {
	cost, ok, missing := usageledger.CostForUsage(
		"missing-model",
		usageledger.TokenUsage{InputTokens: 10, OutputTokens: 20},
		[]usageledger.ModelPrice{{Model: "gpt-5*", InputPer1M: 1}},
	)
	if ok {
		t.Fatalf("ok = true, cost = %v", cost)
	}
	if len(missing) != 1 || missing[0] != "missing-model" {
		t.Fatalf("missing = %#v", missing)
	}
}
