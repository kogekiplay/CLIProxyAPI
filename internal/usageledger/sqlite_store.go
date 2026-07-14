package usageledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore persists usage events, hourly/daily rollups, and model prices.
type SQLiteStore struct {
	db *sql.DB
}

const (
	sqliteBusyTimeoutMS    = 5000
	sqliteFileMaxOpenConns = 4
)

// OpenSQLite opens a usage ledger database and applies the embedded schema.
func OpenSQLite(path string) (*SQLiteStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("usage ledger sqlite path is required")
	}
	db, err := sql.Open("sqlite", sqliteDataSourceName(path))
	if err != nil {
		return nil, err
	}
	maxOpenConns := sqliteMaxOpenConns(path)
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxOpenConns)

	store := &SQLiteStore{db: db}
	if err := store.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func sqliteDataSourceName(path string) string {
	params := url.Values{}
	params.Add("_pragma", fmt.Sprintf("busy_timeout=%d", sqliteBusyTimeoutMS))
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + params.Encode()
}

func sqliteMaxOpenConns(path string) int {
	if strings.Contains(strings.ToLower(path), ":memory:") {
		return 1
	}
	return sqliteFileMaxOpenConns
}

// Close closes the database handle.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) init(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("usage ledger sqlite store is nil")
	}
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE IF NOT EXISTS usage_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			request_id TEXT NOT NULL DEFAULT '',
			ts_ns INTEGER NOT NULL,
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			model_alias TEXT NOT NULL DEFAULT '',
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
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS usage_events_request_id_unique
			ON usage_events(request_id)
			WHERE request_id <> ''`,
		`CREATE INDEX IF NOT EXISTS usage_events_time_idx
			ON usage_events(ts_ns, id)`,
		`CREATE INDEX IF NOT EXISTS usage_events_scope_idx
			ON usage_events(provider, auth_index, api_key_hash, account_ref, ts_ns)`,
		`CREATE INDEX IF NOT EXISTS usage_events_model_idx
			ON usage_events(provider, model, ts_ns)`,
		`CREATE TABLE IF NOT EXISTS usage_rollups (
			bucket_kind TEXT NOT NULL,
			bucket_start_ns INTEGER NOT NULL,
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			endpoint TEXT NOT NULL DEFAULT '',
			auth_index TEXT NOT NULL DEFAULT '',
			auth_file_name TEXT NOT NULL DEFAULT '',
			api_key_hash TEXT NOT NULL DEFAULT '',
			account_ref TEXT NOT NULL DEFAULT '',
			service_tier TEXT NOT NULL DEFAULT '',
			request_count INTEGER NOT NULL DEFAULT 0,
			failed_count INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			reasoning_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (
				bucket_kind,
				bucket_start_ns,
				provider,
				model,
				endpoint,
				auth_index,
				auth_file_name,
				api_key_hash,
				account_ref,
				service_tier
			)
		)`,
		`CREATE INDEX IF NOT EXISTS usage_rollups_scope_idx
			ON usage_rollups(bucket_kind, provider, auth_index, api_key_hash, account_ref, bucket_start_ns)`,
		`CREATE TABLE IF NOT EXISTS model_prices (
			model TEXT PRIMARY KEY,
			input_per_1m REAL NOT NULL DEFAULT 0,
			output_per_1m REAL NOT NULL DEFAULT 0,
			cache_read_per_1m REAL NOT NULL DEFAULT 0,
			cache_creation_per_1m REAL NOT NULL DEFAULT 0,
			cached_per_1m REAL NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT 'manual',
			source_model_id TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := s.ensureUsageEventColumns(ctx); err != nil {
		return err
	}
	return s.ensureDefaultModelPrices(ctx)
}

func (s *SQLiteStore) ensureUsageEventColumns(ctx context.Context) error {
	columns, err := s.tableColumns(ctx, "usage_events")
	if err != nil {
		return err
	}
	for _, column := range []struct {
		name string
		def  string
	}{
		{name: "credential_key_hash", def: "TEXT NOT NULL DEFAULT ''"},
		{name: "model_alias", def: "TEXT NOT NULL DEFAULT ''"},
		{name: "auth_type", def: "TEXT NOT NULL DEFAULT ''"},
		{name: "reasoning_effort", def: "TEXT NOT NULL DEFAULT ''"},
		{name: "status_code", def: "INTEGER NOT NULL DEFAULT 0"},
		{name: "latency_ms", def: "INTEGER NOT NULL DEFAULT 0"},
		{name: "ttft_ms", def: "INTEGER NOT NULL DEFAULT 0"},
		{name: "fail_status_code", def: "INTEGER NOT NULL DEFAULT 0"},
		{name: "fail_summary", def: "TEXT NOT NULL DEFAULT ''"},
		{name: "fail_body", def: "TEXT NOT NULL DEFAULT ''"},
	} {
		if columns[column.name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE usage_events ADD COLUMN %s %s", column.name, column.def)); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

// InsertEvent stores one request event and updates rollups. It returns false
// when a non-empty request ID has already been stored.
func (s *SQLiteStore) InsertEvent(ctx context.Context, event Event) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("usage ledger sqlite store is nil")
	}
	event = normalizeEvent(event)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	inserted, err := insertEventTx(ctx, tx, event)
	if err != nil {
		return false, err
	}
	if !inserted {
		if err = tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	for _, rollup := range []struct {
		kind  string
		start time.Time
	}{
		{kind: "hour", start: event.Timestamp.Truncate(time.Hour)},
		{kind: "day", start: event.Timestamp.Truncate(24 * time.Hour)},
	} {
		if err = upsertRollupTx(ctx, tx, rollup.kind, rollup.start, event); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func insertEventTx(ctx context.Context, tx *sql.Tx, event Event) (bool, error) {
	query := `INSERT INTO usage_events (
		request_id,
		ts_ns,
		provider,
		model,
		model_alias,
		endpoint,
		auth_index,
		auth_file_name,
		api_key_hash,
		credential_key_hash,
		account_ref,
		auth_type,
		service_tier,
		reasoning_effort,
		status_code,
		latency_ms,
		ttft_ms,
		fail_status_code,
		fail_summary,
		fail_body,
		input_tokens,
		output_tokens,
		reasoning_tokens,
		cached_tokens,
		cache_read_tokens,
		cache_creation_tokens,
		total_tokens,
		failed
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if event.RequestID != "" {
		query = `INSERT OR IGNORE INTO usage_events (
			request_id,
			ts_ns,
			provider,
			model,
			model_alias,
			endpoint,
			auth_index,
			auth_file_name,
			api_key_hash,
			credential_key_hash,
			account_ref,
			auth_type,
			service_tier,
			reasoning_effort,
			status_code,
			latency_ms,
			ttft_ms,
			fail_status_code,
			fail_summary,
			fail_body,
			input_tokens,
			output_tokens,
			reasoning_tokens,
			cached_tokens,
			cache_read_tokens,
			cache_creation_tokens,
			total_tokens,
			failed
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	}
	result, err := tx.ExecContext(ctx, query,
		event.RequestID,
		event.Timestamp.UnixNano(),
		event.Provider,
		event.Model,
		event.ModelAlias,
		event.Endpoint,
		event.AuthIndex,
		event.AuthFileName,
		event.APIKeyHash,
		event.CredentialKeyHash,
		event.AccountRef,
		event.AuthType,
		event.ServiceTier,
		event.ReasoningEffort,
		event.StatusCode,
		event.LatencyMS,
		event.TTFTMS,
		event.FailStatusCode,
		event.FailSummary,
		event.FailBody,
		event.Tokens.InputTokens,
		event.Tokens.OutputTokens,
		event.Tokens.ReasoningTokens,
		event.Tokens.CachedTokens,
		event.Tokens.CacheReadTokens,
		event.Tokens.CacheCreationTokens,
		event.Tokens.TotalTokens,
		boolInt(event.Failed),
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func upsertRollupTx(ctx context.Context, tx *sql.Tx, kind string, start time.Time, event Event) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO usage_rollups (
		bucket_kind,
		bucket_start_ns,
		provider,
		model,
		endpoint,
		auth_index,
		auth_file_name,
		api_key_hash,
		account_ref,
		service_tier,
		request_count,
		failed_count,
		input_tokens,
		output_tokens,
		reasoning_tokens,
		cached_tokens,
		cache_read_tokens,
		cache_creation_tokens,
		total_tokens
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (
		bucket_kind,
		bucket_start_ns,
		provider,
		model,
		endpoint,
		auth_index,
		auth_file_name,
		api_key_hash,
		account_ref,
		service_tier
	) DO UPDATE SET
		request_count = request_count + excluded.request_count,
		failed_count = failed_count + excluded.failed_count,
		input_tokens = input_tokens + excluded.input_tokens,
		output_tokens = output_tokens + excluded.output_tokens,
		reasoning_tokens = reasoning_tokens + excluded.reasoning_tokens,
		cached_tokens = cached_tokens + excluded.cached_tokens,
		cache_read_tokens = cache_read_tokens + excluded.cache_read_tokens,
		cache_creation_tokens = cache_creation_tokens + excluded.cache_creation_tokens,
		total_tokens = total_tokens + excluded.total_tokens`,
		kind,
		start.UTC().UnixNano(),
		event.Provider,
		event.Model,
		event.Endpoint,
		event.AuthIndex,
		event.AuthFileName,
		event.APIKeyHash,
		event.AccountRef,
		event.ServiceTier,
		boolInt(event.Failed),
		event.Tokens.InputTokens,
		event.Tokens.OutputTokens,
		event.Tokens.ReasoningTokens,
		event.Tokens.CachedTokens,
		event.Tokens.CacheReadTokens,
		event.Tokens.CacheCreationTokens,
		event.Tokens.TotalTokens,
	)
	return err
}

// Summary returns scoped model usage for a time window.
func (s *SQLiteStore) Summary(ctx context.Context, filter SummaryFilter) (Summary, error) {
	if s == nil || s.db == nil {
		return Summary{}, errors.New("usage ledger sqlite store is nil")
	}
	filter = normalizeSummaryFilter(filter)
	summary := Summary{
		Window:             filter.Window,
		MissingPriceModels: []string{},
		Rows:               []ModelSummary{},
		Source:             "events",
	}
	if filter.Window.IsZero() {
		return summary, nil
	}

	rows, err := s.querySummaryEvents(ctx, filter)
	if err != nil {
		return Summary{}, err
	}
	if len(rows) == 0 {
		rows, err = s.querySummaryRollups(ctx, filter)
		if err != nil {
			return Summary{}, err
		}
		summary.Source = "rollup"
	}
	prices, err := s.ListModelPrices(ctx)
	if err != nil {
		return Summary{}, err
	}
	applySummaryRows(&summary, rows, prices)
	return summary, nil
}

type aggregateRow struct {
	model        string
	requests     int64
	failures     int64
	input        int64
	output       int64
	reasoning    int64
	cached       int64
	cacheRead    int64
	cacheCreated int64
	total        int64
}

func (s *SQLiteStore) querySummaryEvents(ctx context.Context, filter SummaryFilter) ([]aggregateRow, error) {
	where, args := buildSummaryWhere(filter, "ts_ns", false)
	query := `SELECT
		model,
		COUNT(*),
		COALESCE(SUM(CASE WHEN failed <> 0 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(reasoning_tokens), 0),
		COALESCE(SUM(cached_tokens), 0),
		COALESCE(SUM(cache_read_tokens), 0),
		COALESCE(SUM(cache_creation_tokens), 0),
		COALESCE(SUM(total_tokens), 0)
		FROM usage_events ` + where + `
		GROUP BY model
		ORDER BY COUNT(*) DESC, model ASC`
	return scanAggregateRows(s.db.QueryContext(ctx, query, args...))
}

func (s *SQLiteStore) querySummaryRollups(ctx context.Context, filter SummaryFilter) ([]aggregateRow, error) {
	where, args := buildSummaryWhere(filter, "bucket_start_ns", true)
	query := `SELECT
		model,
		COALESCE(SUM(request_count), 0),
		COALESCE(SUM(failed_count), 0),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(reasoning_tokens), 0),
		COALESCE(SUM(cached_tokens), 0),
		COALESCE(SUM(cache_read_tokens), 0),
		COALESCE(SUM(cache_creation_tokens), 0),
		COALESCE(SUM(total_tokens), 0)
		FROM usage_rollups ` + where + `
		GROUP BY model
		ORDER BY SUM(request_count) DESC, model ASC`
	return scanAggregateRows(s.db.QueryContext(ctx, query, args...))
}

func scanAggregateRows(rows *sql.Rows, err error) ([]aggregateRow, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []aggregateRow
	for rows.Next() {
		var row aggregateRow
		if err := rows.Scan(
			&row.model,
			&row.requests,
			&row.failures,
			&row.input,
			&row.output,
			&row.reasoning,
			&row.cached,
			&row.cacheRead,
			&row.cacheCreated,
			&row.total,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func buildSummaryWhere(filter SummaryFilter, timeColumn string, rollup bool) (string, []any) {
	clauses := []string{timeColumn + " >= ?", timeColumn + " < ?"}
	start := filter.Window.Start.UTC().UnixNano()
	if rollup {
		start = filter.Window.Start.UTC().Truncate(time.Hour).UnixNano()
		clauses = append([]string{"bucket_kind = ?"}, clauses...)
	}
	args := []any{start, filter.Window.End.UTC().UnixNano()}
	if rollup {
		args = append([]any{"hour"}, args...)
	}
	add := func(column, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		clauses = append(clauses, column+" = ?")
		args = append(args, value)
	}
	add("provider", filter.Provider)
	add("model", filter.Model)
	add("auth_index", filter.AuthIndex)
	add("api_key_hash", filter.APIKeyHash)
	add("account_ref", filter.AccountRef)
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func applySummaryRows(summary *Summary, rows []aggregateRow, prices []ModelPrice) {
	if summary == nil {
		return
	}
	missingSet := make(map[string]struct{})
	var totalCost float64
	anyPriced := false
	for _, row := range rows {
		tokens := TokenUsage{
			InputTokens:         row.input,
			OutputTokens:        row.output,
			ReasoningTokens:     row.reasoning,
			CachedTokens:        row.cached,
			CacheReadTokens:     row.cacheRead,
			CacheCreationTokens: row.cacheCreated,
			TotalTokens:         row.total,
		}.Normalize()
		modelSummary := ModelSummary{
			Model:        row.model,
			RequestCount: row.requests,
			FailedCount:  row.failures,
			Tokens:       tokens,
		}
		if cost, ok, missing := CostForUsage(row.model, tokens, prices); ok {
			modelSummary.EstimatedCostUSD = floatPtr(cost)
			totalCost += cost
			anyPriced = true
		} else {
			modelSummary.MissingPriceModels = append(modelSummary.MissingPriceModels, missing...)
			for _, model := range missing {
				missingSet[model] = struct{}{}
			}
		}
		summary.RequestCount += row.requests
		summary.FailedCount += row.failures
		summary.Tokens = summary.Tokens.Add(tokens)
		summary.Rows = append(summary.Rows, modelSummary)
	}
	if anyPriced {
		summary.EstimatedCostUSD = floatPtr(totalCost)
	}
	summary.MissingPriceModels = sortedStringSet(missingSet)
}

// ListModelPrices returns all configured model prices.
func (s *SQLiteStore) ListModelPrices(ctx context.Context) ([]ModelPrice, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("usage ledger sqlite store is nil")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
		model,
		input_per_1m,
		output_per_1m,
		cache_read_per_1m,
		cache_creation_per_1m,
		cached_per_1m,
		source,
		source_model_id,
		updated_at
		FROM model_prices
		ORDER BY model ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	prices := make([]ModelPrice, 0)
	for rows.Next() {
		var price ModelPrice
		if err := rows.Scan(
			&price.Model,
			&price.InputPer1M,
			&price.OutputPer1M,
			&price.CacheReadPer1M,
			&price.CacheCreationPer1M,
			&price.CachedPer1M,
			&price.Source,
			&price.SourceModelID,
			&price.UpdatedAt,
		); err != nil {
			return nil, err
		}
		prices = append(prices, price)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return prices, nil
}

// UpsertModelPrice creates or replaces one model price.
func (s *SQLiteStore) UpsertModelPrice(ctx context.Context, price ModelPrice) error {
	if s == nil || s.db == nil {
		return errors.New("usage ledger sqlite store is nil")
	}
	price, err := normalizeModelPrice(price)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, upsertModelPriceSQL,
		price.Model,
		price.InputPer1M,
		price.OutputPer1M,
		price.CacheReadPer1M,
		price.CacheCreationPer1M,
		price.CachedPer1M,
		price.Source,
		price.SourceModelID,
		price.UpdatedAt,
	)
	return err
}

// ReplaceModelPrices replaces the full manual model price table.
func (s *SQLiteStore) ReplaceModelPrices(ctx context.Context, prices []ModelPrice) error {
	if s == nil || s.db == nil {
		return errors.New("usage ledger sqlite store is nil")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `DELETE FROM model_prices`); err != nil {
		return err
	}
	for _, price := range prices {
		price, err = normalizeModelPrice(price)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, upsertModelPriceSQL,
			price.Model,
			price.InputPer1M,
			price.OutputPer1M,
			price.CacheReadPer1M,
			price.CacheCreationPer1M,
			price.CachedPer1M,
			price.Source,
			price.SourceModelID,
			price.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteModelPrice removes one configured model price.
func (s *SQLiteStore) DeleteModelPrice(ctx context.Context, model string) error {
	if s == nil || s.db == nil {
		return errors.New("usage ledger sqlite store is nil")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("model is required")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM model_prices WHERE model = ?`, model)
	return err
}

func (s *SQLiteStore) ensureDefaultModelPrices(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("usage ledger sqlite store is nil")
	}
	for _, price := range defaultModelPrices() {
		price, err := normalizeModelPrice(price)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, insertDefaultModelPriceSQL,
			price.Model,
			price.InputPer1M,
			price.OutputPer1M,
			price.CacheReadPer1M,
			price.CacheCreationPer1M,
			price.CachedPer1M,
			price.Source,
			price.SourceModelID,
			price.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

// CleanupBefore removes raw events before cutoff. Rollups are retained.
func (s *SQLiteStore) CleanupBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("usage ledger sqlite store is nil")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM usage_events WHERE ts_ns < ?`, cutoff.UTC().UnixNano())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

const upsertModelPriceSQL = `INSERT INTO model_prices (
	model,
	input_per_1m,
	output_per_1m,
	cache_read_per_1m,
	cache_creation_per_1m,
	cached_per_1m,
	source,
	source_model_id,
	updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(model) DO UPDATE SET
	input_per_1m = excluded.input_per_1m,
	output_per_1m = excluded.output_per_1m,
	cache_read_per_1m = excluded.cache_read_per_1m,
	cache_creation_per_1m = excluded.cache_creation_per_1m,
	cached_per_1m = excluded.cached_per_1m,
	source = excluded.source,
	source_model_id = excluded.source_model_id,
	updated_at = excluded.updated_at`

const insertDefaultModelPriceSQL = `INSERT OR IGNORE INTO model_prices (
	model,
	input_per_1m,
	output_per_1m,
	cache_read_per_1m,
	cache_creation_per_1m,
	cached_per_1m,
	source,
	source_model_id,
	updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

func normalizeEvent(event Event) Event {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}
	event.RequestID = strings.TrimSpace(event.RequestID)
	event.Provider = defaultString(event.Provider, "unknown")
	event.Model = defaultString(event.Model, "unknown")
	event.ModelAlias = strings.TrimSpace(event.ModelAlias)
	event.Endpoint = strings.TrimSpace(event.Endpoint)
	event.AuthIndex = strings.TrimSpace(event.AuthIndex)
	event.AuthFileName = strings.TrimSpace(event.AuthFileName)
	event.APIKeyHash = strings.TrimSpace(event.APIKeyHash)
	event.CredentialKeyHash = strings.TrimSpace(event.CredentialKeyHash)
	event.AccountRef = strings.TrimSpace(event.AccountRef)
	event.AuthType = strings.TrimSpace(event.AuthType)
	event.ServiceTier = strings.TrimSpace(event.ServiceTier)
	if event.StatusCode < 0 {
		event.StatusCode = 0
	}
	if event.LatencyMS < 0 {
		event.LatencyMS = 0
	}
	if event.TTFTMS < 0 {
		event.TTFTMS = 0
	}
	if event.FailStatusCode < 0 {
		event.FailStatusCode = 0
	}
	if event.StatusCode == 0 && event.FailStatusCode > 0 {
		event.StatusCode = event.FailStatusCode
	}
	if event.FailStatusCode == 0 && event.StatusCode >= 400 {
		event.FailStatusCode = event.StatusCode
	}
	event.FailBody = sanitizeFailureText(event.FailBody)
	event.FailSummary = sanitizeFailureText(event.FailSummary)
	if event.FailSummary == "" && event.FailBody != "" {
		event.FailSummary = failureSummaryFromBody(event.FailBody)
	}
	if event.FailStatusCode >= 400 || event.StatusCode >= 400 {
		event.Failed = true
	}
	event.Tokens = event.Tokens.Normalize()
	return event
}

func normalizeSummaryFilter(filter SummaryFilter) SummaryFilter {
	filter.Provider = strings.TrimSpace(filter.Provider)
	filter.Model = strings.TrimSpace(filter.Model)
	filter.AuthIndex = strings.TrimSpace(filter.AuthIndex)
	filter.APIKeyHash = strings.TrimSpace(filter.APIKeyHash)
	filter.AccountRef = strings.TrimSpace(filter.AccountRef)
	filter.Window.Start = filter.Window.Start.UTC()
	filter.Window.End = filter.Window.End.UTC()
	return filter
}

func normalizeModelPrice(price ModelPrice) (ModelPrice, error) {
	price.Model = strings.TrimSpace(price.Model)
	if price.Model == "" {
		return ModelPrice{}, errors.New("model is required")
	}
	price.Source = strings.TrimSpace(price.Source)
	if price.Source == "" {
		price.Source = "manual"
	}
	price.SourceModelID = strings.TrimSpace(price.SourceModelID)
	price.UpdatedAt = strings.TrimSpace(price.UpdatedAt)
	if price.UpdatedAt == "" {
		price.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if price.InputPer1M < 0 || price.OutputPer1M < 0 || price.CacheReadPer1M < 0 ||
		price.CacheCreationPer1M < 0 || price.CachedPer1M < 0 {
		return ModelPrice{}, fmt.Errorf("model price for %s cannot be negative", price.Model)
	}
	return price, nil
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func floatPtr(value float64) *float64 {
	return &value
}

func sortedStringSet(values map[string]struct{}) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
