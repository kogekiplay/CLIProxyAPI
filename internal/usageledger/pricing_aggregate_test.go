package usageledger

import (
	"math"
	"testing"
)

func TestCostForAggregatePricesMixedShortAndLongRequests(t *testing.T) {
	prices := compileModelPriceIndex([]ModelPrice{{
		Model:       "gpt-5.6-sol",
		InputPer1M:  5,
		OutputPer1M: 30,
	}})
	all := TokenUsage{
		InputTokens:         400_000,
		UncachedInputTokens: 400_000,
		TotalInputTokens:    400_000,
		OutputTokens:        30_000,
	}
	long := TokenUsage{
		InputTokens:         300_000,
		UncachedInputTokens: 300_000,
		TotalInputTokens:    300_000,
		OutputTokens:        20_000,
	}

	cost, ok, missing := costForAggregateWithPriceIndex("gpt-5.6-sol", "priority", all, long, prices)
	if !ok || len(missing) != 0 {
		t.Fatalf("aggregate cost missing: ok=%v missing=%v", ok, missing)
	}
	if math.Abs(cost-5.5) > 0.0000001 {
		t.Fatalf("aggregate cost = %v, want 5.5", cost)
	}
}
