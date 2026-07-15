package usageledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const cacheAccountingMigration = "cache-accounting-v1"
const cacheAccountingMigrationBatchSize = 1000

type cacheAccountingMigrationRow struct {
	id            int64
	provider      string
	model         string
	executorType  string
	explicitMode  string
	inputTokens   int64
	cachedTokens  int64
	cacheRead     int64
	cacheCreation int64
}

func (s *SQLiteStore) migrateCacheAccounting(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var applied int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM usage_ledger_migrations WHERE name = ?`, cacheAccountingMigration).Scan(&applied)
	if err == nil {
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	update, err := tx.PrepareContext(ctx, `UPDATE usage_events SET
		cache_input_mode = ?,
		normalized_cached_tokens = ?,
		normalized_cache_read_tokens = ?,
		normalized_cache_creation_tokens = ?,
		uncached_input_tokens = ?,
		total_input_tokens = ?
		WHERE id = ?`)
	if err != nil {
		return err
	}
	var lastID int64
	for {
		pending, err := readCacheAccountingMigrationBatch(ctx, tx, lastID)
		if err != nil {
			update.Close()
			return err
		}
		if len(pending) == 0 {
			break
		}
		for _, row := range pending {
			accounting := NormalizeCacheAccounting(CacheInputContext{
				ExplicitMode: row.explicitMode,
				ExecutorType: row.executorType,
				Provider:     row.provider,
				Model:        row.model,
			}, TokenUsage{
				InputTokens:         row.inputTokens,
				CachedTokens:        row.cachedTokens,
				CacheReadTokens:     row.cacheRead,
				CacheCreationTokens: row.cacheCreation,
			})
			if _, err := update.ExecContext(ctx,
				accounting.Mode,
				accounting.CachedTokens,
				accounting.CacheReadTokens,
				accounting.CacheCreationTokens,
				accounting.UncachedInputTokens,
				accounting.TotalInputTokens,
				row.id,
			); err != nil {
				update.Close()
				return err
			}
		}
		lastID = pending[len(pending)-1].id
	}
	if err := update.Close(); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_rollups`); err != nil {
		return err
	}
	for _, bucket := range []struct {
		kind  string
		nanos int64
	}{
		{kind: "hour", nanos: int64(time.Hour)},
		{kind: "day", nanos: int64(24 * time.Hour)},
	} {
		if err := rebuildUsageRollupsTx(ctx, tx, bucket.kind, bucket.nanos); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_ledger_migrations (name, applied_at) VALUES (?, ?)`,
		cacheAccountingMigration,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func readCacheAccountingMigrationBatch(ctx context.Context, tx *sql.Tx, afterID int64) ([]cacheAccountingMigrationRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT
		id,
		provider,
		model,
		executor_type,
		cache_input_mode,
		input_tokens,
		cached_tokens,
		cache_read_tokens,
		cache_creation_tokens
		FROM usage_events
		WHERE id > ?
		ORDER BY id
		LIMIT ?`, afterID, cacheAccountingMigrationBatchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pending := make([]cacheAccountingMigrationRow, 0, cacheAccountingMigrationBatchSize)
	for rows.Next() {
		var row cacheAccountingMigrationRow
		if err := rows.Scan(
			&row.id,
			&row.provider,
			&row.model,
			&row.executorType,
			&row.explicitMode,
			&row.inputTokens,
			&row.cachedTokens,
			&row.cacheRead,
			&row.cacheCreation,
		); err != nil {
			return nil, err
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pending, nil
}

func rebuildUsageRollupsTx(ctx context.Context, tx *sql.Tx, kind string, bucketNanos int64) error {
	query := fmt.Sprintf(`INSERT INTO usage_rollups (
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
		normalized_cached_tokens,
		normalized_cache_read_tokens,
		normalized_cache_creation_tokens,
		uncached_input_tokens,
		total_input_tokens,
		long_input_tokens,
		long_output_tokens,
		long_cached_tokens,
		long_cache_read_tokens,
		long_cache_creation_tokens,
		total_tokens
	) SELECT
		?,
		(ts_ns / %[1]d) * %[1]d,
		provider,
		model,
		endpoint,
		auth_index,
		auth_file_name,
		api_key_hash,
		account_ref,
		service_tier,
		COUNT(*),
		SUM(CASE WHEN failed <> 0 THEN 1 ELSE 0 END),
		SUM(input_tokens),
		SUM(output_tokens),
		SUM(reasoning_tokens),
		SUM(cached_tokens),
		SUM(cache_read_tokens),
		SUM(cache_creation_tokens),
		SUM(normalized_cached_tokens),
		SUM(normalized_cache_read_tokens),
		SUM(normalized_cache_creation_tokens),
		SUM(uncached_input_tokens),
		SUM(total_input_tokens),
		SUM(CASE WHEN total_input_tokens > %[2]d THEN total_input_tokens ELSE 0 END),
		SUM(CASE WHEN total_input_tokens > %[2]d THEN output_tokens ELSE 0 END),
		SUM(CASE WHEN total_input_tokens > %[2]d THEN normalized_cached_tokens ELSE 0 END),
		SUM(CASE WHEN total_input_tokens > %[2]d THEN normalized_cache_read_tokens ELSE 0 END),
		SUM(CASE WHEN total_input_tokens > %[2]d THEN normalized_cache_creation_tokens ELSE 0 END),
		SUM(total_tokens)
	FROM usage_events
	GROUP BY
		(ts_ns / %[1]d) * %[1]d,
		provider,
		model,
		endpoint,
		auth_index,
		auth_file_name,
		api_key_hash,
		account_ref,
		service_tier`, bucketNanos, LongContextInputTokenThreshold)
	_, err := tx.ExecContext(ctx, query, kind)
	return err
}
