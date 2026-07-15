package usageledger

import (
	"fmt"
	"reflect"
	"testing"
)

func TestCompiledModelPriceIndexMatchesLinearResolver(t *testing.T) {
	prices := []ModelPrice{
		{Model: "gpt-5.6", InputPer1M: 1},
		{Model: "GPT-5.6", InputPer1M: 2},
		{Model: "gpt-5*", InputPer1M: 3},
		{Model: "gpt-*", InputPer1M: 4},
		{Model: "claude-*", InputPer1M: 5},
	}
	index := compileModelPriceIndex(prices)
	models := []string{
		"gpt-5.6",
		" GPT-5.6(XHIGH) ",
		"gpt-5.7-codex",
		"gpt-4.9",
		"claude-sonnet-5",
		"missing-model",
		"",
	}

	for _, model := range models {
		model := model
		t.Run(model, func(t *testing.T) {
			wantPrice, wantOK := MatchModelPrice(model, prices)
			gotPrice, gotOK := index.match(model)
			if gotOK != wantOK || !reflect.DeepEqual(gotPrice, wantPrice) {
				t.Fatalf("indexed match for %q = (%+v, %v), want (%+v, %v)", model, gotPrice, gotOK, wantPrice, wantOK)
			}

			tokens := TokenUsage{InputTokens: 2_000_000, OutputTokens: 1_000_000}
			wantCost, wantCostOK, wantMissing := CostForUsage(model, tokens, prices)
			gotCost, gotCostOK, gotMissing := costForUsageWithPriceIndex(model, tokens, index)
			if gotCost != wantCost || gotCostOK != wantCostOK || !reflect.DeepEqual(gotMissing, wantMissing) {
				t.Fatalf("indexed cost for %q = (%v, %v, %v), want (%v, %v, %v)", model, gotCost, gotCostOK, gotMissing, wantCost, wantCostOK, wantMissing)
			}
		})
	}
}

func BenchmarkModelPriceLookup(b *testing.B) {
	prices := make([]ModelPrice, 0, 202)
	for i := 0; i < 200; i++ {
		prices = append(prices, ModelPrice{
			Model:       fmt.Sprintf("benchmark-model-%03d", i),
			InputPer1M:  float64(i + 1),
			OutputPer1M: float64(i + 2),
		})
	}
	prices = append(prices,
		ModelPrice{Model: "gpt-5.6-sol", InputPer1M: 5, OutputPer1M: 30},
		ModelPrice{Model: "gpt-5*", InputPer1M: 2, OutputPer1M: 10},
	)
	tokens := TokenUsage{InputTokens: 1_000, OutputTokens: 500}
	index := compileModelPriceIndex(prices)

	b.Run("linear", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkCost, benchmarkCostOK, _ = CostForUsage("gpt-5.6-sol(xhigh)", tokens, prices)
		}
	})
	b.Run("compiled", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkCost, benchmarkCostOK, _ = costForUsageWithPriceIndex("gpt-5.6-sol(xhigh)", tokens, index)
		}
	})
}

var (
	benchmarkCost   float64
	benchmarkCostOK bool
)
