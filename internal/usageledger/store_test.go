package usageledger

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
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

func TestOpenSQLiteConfiguresConcurrentWALConnections(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	if got := store.db.Stats().MaxOpenConnections; got != sqliteFileMaxOpenConns {
		t.Fatalf("max open connections = %d, want %d", got, sqliteFileMaxOpenConns)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connections := make([]*sql.Conn, 0, sqliteFileMaxOpenConns)
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()

	for i := 0; i < sqliteFileMaxOpenConns; i++ {
		connection, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatalf("open pooled connection %d: %v", i, err)
		}
		connections = append(connections, connection)

		var busyTimeout int
		if err := connection.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatalf("read busy timeout from connection %d: %v", i, err)
		}
		if busyTimeout != sqliteBusyTimeoutMS {
			t.Fatalf("connection %d busy timeout = %d, want %d", i, busyTimeout, sqliteBusyTimeoutMS)
		}

		var journalMode string
		if err := connection.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
			t.Fatalf("read journal mode from connection %d: %v", i, err)
		}
		if journalMode != "wal" {
			t.Fatalf("connection %d journal mode = %q, want wal", i, journalMode)
		}
	}
}

func TestSQLiteStoreHandlesConcurrentAnalyticsAndWrites(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	base := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		if _, err := store.InsertEvent(context.Background(), Event{
			RequestID: fmt.Sprintf("seed-%d", i),
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Provider:  "codex",
			Model:     "gpt-5.6",
		}); err != nil {
			t.Fatalf("seed event %d: %v", i, err)
		}
	}

	request := AnalyticsRequest{
		FromMS: base.Add(-time.Minute).UnixMilli(),
		ToMS:   base.Add(time.Hour).UnixMilli(),
		Include: AnalyticsInclude{
			EventsPage: &AnalyticsEventsPage{Limit: 20},
		},
	}

	const workers = 12
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			if worker%3 == 0 {
				_, err := store.InsertEvent(context.Background(), Event{
					RequestID: fmt.Sprintf("concurrent-%d", worker),
					Timestamp: base.Add(time.Duration(100+worker) * time.Second),
					Provider:  "codex",
					Model:     "gpt-5.6",
				})
				if err != nil {
					errors <- fmt.Errorf("writer %d: %w", worker, err)
				}
				return
			}
			if _, err := store.Analytics(context.Background(), request); err != nil {
				errors <- fmt.Errorf("reader %d: %w", worker, err)
			}
		}(i)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestOpenSQLiteKeepsPrivateInMemoryDatabaseOnOneConnection(t *testing.T) {
	store, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite store: %v", err)
	}
	defer store.Close()

	if got := store.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("in-memory max open connections = %d, want 1", got)
	}
}

func findModelPrice(prices []ModelPrice, model string) (ModelPrice, bool) {
	for _, price := range prices {
		if price.Model == model {
			return price, true
		}
	}
	return ModelPrice{}, false
}

func TestOpenSQLiteAddsModelAliasColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy sqlite database: %v", err)
	}
	_, err = legacy.Exec(`CREATE TABLE usage_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		request_id TEXT NOT NULL DEFAULT '',
		ts_ns INTEGER NOT NULL,
		provider TEXT NOT NULL,
		model TEXT NOT NULL,
		endpoint TEXT NOT NULL DEFAULT '',
		auth_index TEXT NOT NULL DEFAULT '',
		auth_file_name TEXT NOT NULL DEFAULT '',
		api_key_hash TEXT NOT NULL DEFAULT '',
		credential_key_hash TEXT NOT NULL DEFAULT '',
		account_ref TEXT NOT NULL DEFAULT '',
		auth_type TEXT NOT NULL DEFAULT '',
		service_tier TEXT NOT NULL DEFAULT '',
		reasoning_effort TEXT NOT NULL DEFAULT '',
		status_code INTEGER NOT NULL DEFAULT 0,
		latency_ms INTEGER NOT NULL DEFAULT 0,
		ttft_ms INTEGER NOT NULL DEFAULT 0,
		fail_status_code INTEGER NOT NULL DEFAULT 0,
		fail_summary TEXT NOT NULL DEFAULT '',
		fail_body TEXT NOT NULL DEFAULT '',
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_tokens INTEGER NOT NULL DEFAULT 0,
		cached_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		failed INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		_ = legacy.Close()
		t.Fatalf("create legacy usage_events: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO usage_events (ts_ns, provider, model) VALUES (?, ?, ?)`, 1, "legacy", "legacy-model"); err != nil {
		_ = legacy.Close()
		t.Fatalf("insert legacy event: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy sqlite database: %v", err)
	}

	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("reopen migrated sqlite database: %v", err)
	}
	defer store.Close()

	columns, err := store.tableColumns(context.Background(), "usage_events")
	if err != nil {
		t.Fatalf("read usage_events columns: %v", err)
	}
	if !columns["model_alias"] {
		t.Fatal("usage_events is missing model_alias")
	}
	var model, modelAlias string
	if err := store.db.QueryRow(`SELECT model, model_alias FROM usage_events LIMIT 1`).Scan(&model, &modelAlias); err != nil {
		t.Fatal(err)
	}
	if model != "legacy-model" || modelAlias != "" {
		t.Fatalf("migrated model names = %q / %q", model, modelAlias)
	}
}

func TestOpenSQLiteBackfillsCanonicalCacheAccountingAndRebuildsRollups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("create sqlite store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial sqlite store: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite for legacy seed: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM usage_ledger_migrations WHERE name = ?`, cacheAccountingMigration); err != nil {
		t.Fatalf("clear migration marker: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM usage_rollups`); err != nil {
		t.Fatalf("clear rollups: %v", err)
	}
	ts := time.Date(2026, 7, 15, 8, 30, 0, 0, time.UTC).UnixNano()
	for _, seed := range []struct {
		requestID    string
		provider     string
		model        string
		executorType string
		input        int64
		cached       int64
		read         int64
		created      int64
	}{
		{requestID: "legacy-codex", provider: "codex", model: "gpt-5.6-sol", executorType: "CodexExecutor", input: 1000, cached: 300, read: 300, created: 100},
		{requestID: "legacy-claude", provider: "claude", model: "claude-opus", executorType: "ClaudeExecutor", input: 100, cached: 300, read: 300, created: 50},
	} {
		if _, err := db.Exec(`INSERT INTO usage_events (
			request_id, ts_ns, provider, model, executor_type,
			input_tokens, cached_tokens, cache_read_tokens, cache_creation_tokens
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			seed.requestID, ts, seed.provider, seed.model, seed.executorType,
			seed.input, seed.cached, seed.read, seed.created,
		); err != nil {
			t.Fatalf("insert legacy event %s: %v", seed.requestID, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy seed database: %v", err)
	}

	store, err = OpenSQLite(path)
	if err != nil {
		t.Fatalf("open migrated sqlite store: %v", err)
	}
	assertCacheAccounting := func(requestID, mode string, cached, read, created, uncached, total int64) {
		t.Helper()
		var gotMode string
		var gotCached, gotRead, gotCreated, gotUncached, gotTotal int64
		if err := store.db.QueryRow(`SELECT
			cache_input_mode,
			normalized_cached_tokens,
			normalized_cache_read_tokens,
			normalized_cache_creation_tokens,
			uncached_input_tokens,
			total_input_tokens
			FROM usage_events WHERE request_id = ?`, requestID).Scan(
			&gotMode, &gotCached, &gotRead, &gotCreated, &gotUncached, &gotTotal,
		); err != nil {
			t.Fatalf("read migrated event %s: %v", requestID, err)
		}
		if gotMode != mode || gotCached != cached || gotRead != read || gotCreated != created || gotUncached != uncached || gotTotal != total {
			t.Fatalf("migrated event %s = mode=%q C=%d CR=%d CW=%d uncached=%d total=%d", requestID, gotMode, gotCached, gotRead, gotCreated, gotUncached, gotTotal)
		}
	}
	assertCacheAccounting("legacy-codex", CacheInputModeIncluded, 0, 300, 100, 600, 1000)
	assertCacheAccounting("legacy-claude", CacheInputModeSeparate, 0, 300, 50, 100, 450)

	var rollupCount, markerCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_rollups`).Scan(&rollupCount); err != nil {
		t.Fatalf("count rebuilt rollups: %v", err)
	}
	if rollupCount != 4 {
		t.Fatalf("rollup count = %d, want 4", rollupCount)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_ledger_migrations WHERE name = ?`, cacheAccountingMigration).Scan(&markerCount); err != nil {
		t.Fatalf("count migration marker: %v", err)
	}
	if markerCount != 1 {
		t.Fatalf("migration marker count = %d, want 1", markerCount)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}

	store, err = OpenSQLite(path)
	if err != nil {
		t.Fatalf("reopen migrated sqlite store: %v", err)
	}
	defer store.Close()
	var reopenedRollupCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_rollups`).Scan(&reopenedRollupCount); err != nil {
		t.Fatalf("count rollups after reopen: %v", err)
	}
	if reopenedRollupCount != rollupCount {
		t.Fatalf("rollup count after reopen = %d, want %d", reopenedRollupCount, rollupCount)
	}
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
	gpt56Cases := []struct {
		model         string
		input         float64
		output        float64
		cacheRead     float64
		cacheCreation float64
	}{
		{model: "gpt-5.6-sol", input: 5, output: 30, cacheRead: 0.5, cacheCreation: 6.25},
		{model: "gpt-5.6-terra", input: 2.5, output: 15, cacheRead: 0.25, cacheCreation: 3.125},
		{model: "gpt-5.6-luna", input: 1, output: 6, cacheRead: 0.1, cacheCreation: 1.25},
	}
	for _, testCase := range gpt56Cases {
		price, ok := priceByModel[testCase.model]
		if !ok {
			t.Fatalf("expected %s default price, got %#v", testCase.model, prices)
		}
		if price.InputPer1M != testCase.input || price.OutputPer1M != testCase.output ||
			price.CacheReadPer1M != testCase.cacheRead || price.CacheCreationPer1M != testCase.cacheCreation {
			t.Fatalf("%s default price = %#v", testCase.model, price)
		}
		if price.Source != gpt56PricingSource {
			t.Fatalf("%s source = %q", testCase.model, price.Source)
		}
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

	staleDefault := ModelPrice{
		Model:              "gpt-5.6-sol",
		InputPer1M:         5,
		OutputPer1M:        30,
		CacheReadPer1M:     0.5,
		CacheCreationPer1M: 0,
		Source:             defaultModelPriceSource,
		SourceModelID:      "gpt-5.6-sol",
		UpdatedAt:          "2026-06-25T00:00:00Z",
	}
	if err := store.UpsertModelPrice(context.Background(), staleDefault); err != nil {
		t.Fatalf("upsert stale default price: %v", err)
	}
	if err := store.ensureDefaultModelPrices(context.Background()); err != nil {
		t.Fatalf("refresh default prices: %v", err)
	}
	prices, err = store.ListModelPrices(context.Background())
	if err != nil {
		t.Fatalf("list refreshed default prices: %v", err)
	}
	refreshed, ok := findModelPrice(prices, "gpt-5.6-sol")
	if !ok || refreshed.CacheCreationPer1M != 6.25 || refreshed.Source != gpt56PricingSource {
		t.Fatalf("refreshed gpt-5.6-sol default price = %#v", refreshed)
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
