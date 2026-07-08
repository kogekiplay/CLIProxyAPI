package usageledger

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	return store
}

func findModelPrice(prices []ModelPrice, model string) (ModelPrice, bool) {
	for _, price := range prices {
		if price.Model == model {
			return price, true
		}
	}
	return ModelPrice{}, false
}

func TestSQLiteStoreInsertEventUpdatesRollupsOnce(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	event := Event{
		RequestID: "req-1",
		Timestamp: time.Date(2026, 6, 26, 10, 15, 0, 0, time.UTC),
		Provider:  "codex",
		Model:     "gpt-5.5",
		AuthIndex: "auth-1",
		Tokens:    TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}
	if inserted, err := store.InsertEvent(context.Background(), event); err != nil || !inserted {
		t.Fatalf("first insert inserted=%v err=%v", inserted, err)
	}
	if inserted, err := store.InsertEvent(context.Background(), event); err != nil || inserted {
		t.Fatalf("second insert inserted=%v err=%v", inserted, err)
	}

	summary, err := store.Summary(context.Background(), SummaryFilter{
		Provider:  "codex",
		AuthIndex: "auth-1",
		Window: Window{
			Start: event.Timestamp.Add(-time.Hour),
			End:   event.Timestamp.Add(time.Hour),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Tokens.TotalTokens != 15 || summary.RequestCount != 1 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestSQLiteStoreSummaryScopesByAuthIndexAndAPIKeyHash(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	events := []Event{
		{RequestID: "codex-a", Timestamp: now, Provider: "codex", Model: "gpt-5.5", AuthIndex: "codex-auth-1", Tokens: TokenUsage{TotalTokens: 10}},
		{RequestID: "codex-b", Timestamp: now, Provider: "codex", Model: "gpt-5.5", AuthIndex: "codex-auth-2", Tokens: TokenUsage{TotalTokens: 90}},
		{RequestID: "opencode-a", Timestamp: now, Provider: "opencode-go", Model: "claude-sonnet-4", APIKeyHash: "key-a", AccountRef: "opencode-go:acc-a", Tokens: TokenUsage{TotalTokens: 20}},
		{RequestID: "opencode-b", Timestamp: now, Provider: "opencode-go", Model: "claude-sonnet-4", APIKeyHash: "key-b", AccountRef: "opencode-go:acc-b", Tokens: TokenUsage{TotalTokens: 80}},
	}
	for _, event := range events {
		if _, err := store.InsertEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}

	window := Window{Start: now.Add(-time.Minute), End: now.Add(time.Minute)}
	codex, err := store.Summary(context.Background(), SummaryFilter{
		Provider:  "codex",
		AuthIndex: "codex-auth-1",
		Window:    window,
	})
	if err != nil {
		t.Fatal(err)
	}
	if codex.Tokens.TotalTokens != 10 {
		t.Fatalf("codex total = %d", codex.Tokens.TotalTokens)
	}

	opencode, err := store.Summary(context.Background(), SummaryFilter{
		Provider:   "opencode-go",
		APIKeyHash: "key-a",
		AccountRef: "opencode-go:acc-a",
		Window:     window,
	})
	if err != nil {
		t.Fatal(err)
	}
	if opencode.Tokens.TotalTokens != 20 {
		t.Fatalf("opencode total = %d", opencode.Tokens.TotalTokens)
	}
}

func TestSQLiteStoreSummaryIncludesKnownCostWhenSomeModelsMissPrices(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	ctx := context.Background()
	if err := store.UpsertModelPrice(ctx, ModelPrice{
		Model:       "priced-model-for-partial-cost",
		InputPer1M:  2,
		OutputPer1M: 6,
		Source:      "test",
	}); err != nil {
		t.Fatalf("upsert price: %v", err)
	}

	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	events := []Event{
		{
			RequestID: "priced",
			Timestamp: now,
			Provider:  "codex",
			Model:     "priced-model-for-partial-cost",
			AuthIndex: "auth-1",
			Tokens:    TokenUsage{InputTokens: 1_000_000, OutputTokens: 500_000, TotalTokens: 1_500_000},
		},
		{
			RequestID: "unpriced",
			Timestamp: now,
			Provider:  "codex",
			Model:     "unpriced-model-for-partial-cost",
			AuthIndex: "auth-1",
			Tokens:    TokenUsage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
	}
	for _, event := range events {
		if _, err := store.InsertEvent(ctx, event); err != nil {
			t.Fatalf("insert %s: %v", event.RequestID, err)
		}
	}

	summary, err := store.Summary(ctx, SummaryFilter{
		Provider:  "codex",
		AuthIndex: "auth-1",
		Window:    Window{Start: now.Add(-time.Minute), End: now.Add(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.EstimatedCostUSD == nil {
		t.Fatal("estimated cost is nil, want known priced rows to be included")
	}
	if *summary.EstimatedCostUSD != 5 {
		t.Fatalf("estimated cost = %v, want 5", *summary.EstimatedCostUSD)
	}
	if len(summary.MissingPriceModels) != 1 || summary.MissingPriceModels[0] != "unpriced-model-for-partial-cost" {
		t.Fatalf("missing prices = %#v", summary.MissingPriceModels)
	}
}

func TestSQLiteStoreModelPriceCRUD(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	price := ModelPrice{
		Model:              "gpt-5.5",
		InputPer1M:         10,
		OutputPer1M:        20,
		CacheReadPer1M:     1,
		CacheCreationPer1M: 5,
		Source:             "manual",
		UpdatedAt:          "2026-06-26T00:00:00Z",
	}
	if err := store.UpsertModelPrice(context.Background(), price); err != nil {
		t.Fatalf("upsert price: %v", err)
	}
	prices, err := store.ListModelPrices(context.Background())
	if err != nil {
		t.Fatalf("list prices: %v", err)
	}
	stored, ok := findModelPrice(prices, "gpt-5.5")
	if !ok || stored.InputPer1M != 10 {
		t.Fatalf("prices = %#v", prices)
	}

	price.InputPer1M = 11
	if err := store.ReplaceModelPrices(context.Background(), []ModelPrice{price}); err != nil {
		t.Fatalf("replace prices: %v", err)
	}
	prices, err = store.ListModelPrices(context.Background())
	if err != nil {
		t.Fatalf("list replaced prices: %v", err)
	}
	stored, ok = findModelPrice(prices, "gpt-5.5")
	if !ok || stored.InputPer1M != 11 {
		t.Fatalf("replaced prices = %#v", prices)
	}

	if err := store.DeleteModelPrice(context.Background(), "gpt-5.5"); err != nil {
		t.Fatalf("delete price: %v", err)
	}
	prices, err = store.ListModelPrices(context.Background())
	if err != nil {
		t.Fatalf("list deleted prices: %v", err)
	}
	if _, ok := findModelPrice(prices, "gpt-5.5"); ok {
		t.Fatalf("prices after delete = %#v", prices)
	}
}

func TestSQLiteStoreSeedsDefaultModelPricesWithoutOverridingManualPrices(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	prices, err := store.ListModelPrices(context.Background())
	if err != nil {
		t.Fatalf("list default prices: %v", err)
	}
	priceByModel := make(map[string]ModelPrice, len(prices))
	for _, price := range prices {
		priceByModel[price.Model] = price
	}
	kimi, ok := priceByModel["kimi-k2.6"]
	if !ok {
		t.Fatalf("expected kimi-k2.6 default price, got %#v", prices)
	}
	if kimi.InputPer1M != 0.95 || kimi.OutputPer1M != 4 || kimi.CacheReadPer1M != 0.16 {
		t.Fatalf("kimi-k2.6 default price = %#v", kimi)
	}
	if kimi.Source != "opencode-zen-default" {
		t.Fatalf("kimi-k2.6 source = %q", kimi.Source)
	}
	prefixedKimi, ok := priceByModel["opencode-go/kimi-k2.6"]
	if !ok {
		t.Fatalf("expected opencode-go/kimi-k2.6 default price, got %#v", prices)
	}
	if prefixedKimi.InputPer1M != kimi.InputPer1M || prefixedKimi.OutputPer1M != kimi.OutputPer1M {
		t.Fatalf("prefixed kimi-k2.6 default price = %#v, raw = %#v", prefixedKimi, kimi)
	}
	gptImage2, ok := priceByModel["gpt-image-2"]
	if !ok {
		t.Fatalf("expected gpt-image-2 default price, got %#v", prices)
	}
	if gptImage2.InputPer1M != 8 || gptImage2.OutputPer1M != 30 || gptImage2.CacheReadPer1M != 2 {
		t.Fatalf("gpt-image-2 default price = %#v", gptImage2)
	}
	gptImage15, ok := priceByModel["gpt-image-1.5"]
	if !ok {
		t.Fatalf("expected gpt-image-1.5 default price, got %#v", prices)
	}
	if gptImage15.InputPer1M != 8 || gptImage15.OutputPer1M != 32 || gptImage15.CacheReadPer1M != 2 {
		t.Fatalf("gpt-image-1.5 default price = %#v", gptImage15)
	}

	manual := ModelPrice{
		Model:       "kimi-k2.6",
		InputPer1M:  9,
		OutputPer1M: 8,
		Source:      "manual",
	}
	if err := store.UpsertModelPrice(context.Background(), manual); err != nil {
		t.Fatalf("upsert manual override: %v", err)
	}
	if err := store.ensureDefaultModelPrices(context.Background()); err != nil {
		t.Fatalf("ensure default prices: %v", err)
	}
	prices, err = store.ListModelPrices(context.Background())
	if err != nil {
		t.Fatalf("list default prices after ensure: %v", err)
	}
	for _, price := range prices {
		if price.Model == "kimi-k2.6" {
			if price.InputPer1M != 9 || price.OutputPer1M != 8 || price.Source != "manual" {
				t.Fatalf("manual price was overwritten: %#v", price)
			}
			return
		}
	}
	t.Fatalf("manual price not found after ensure")
}

func TestSQLiteStoreCleanupBeforeRemovesEventsKeepsRollups(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	event := Event{
		RequestID: "old",
		Timestamp: now.AddDate(0, 0, -61),
		Provider:  "codex",
		Model:     "gpt-5.5",
		AuthIndex: "auth-1",
		Tokens:    TokenUsage{TotalTokens: 7},
	}
	if _, err := store.InsertEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.CleanupBefore(context.Background(), now.AddDate(0, 0, -60))
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	summary, err := store.Summary(context.Background(), SummaryFilter{
		Provider:  "codex",
		AuthIndex: "auth-1",
		Window:    Window{Start: event.Timestamp.Add(-time.Hour), End: event.Timestamp.Add(time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Tokens.TotalTokens != 7 {
		t.Fatalf("rollup total after cleanup = %d", summary.Tokens.TotalTokens)
	}
}
