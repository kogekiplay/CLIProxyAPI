package usageledger

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func BenchmarkSQLiteStoreAnalyticsAll(b *testing.B) {
	const eventCount = 20_000
	store := newAnalyticsBenchmarkStore(b, eventCount)
	defer store.Close()

	to := time.Now().UTC().Add(time.Hour)
	request := AnalyticsRequest{
		FromMS: to.Add(-30 * 24 * time.Hour).UnixMilli(),
		ToMS:   to.UnixMilli(),
		Include: AnalyticsInclude{
			Summary:         true,
			Timeline:        true,
			ModelStats:      true,
			APIKeyStats:     true,
			CredentialStats: true,
			EventsPage:      &AnalyticsEventsPage{Limit: 100},
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		response, err := store.Analytics(context.Background(), request)
		if err != nil {
			b.Fatal(err)
		}
		if response.Summary == nil || response.Summary.TotalCalls != eventCount {
			b.Fatalf("summary calls = %v, want %d", response.Summary, eventCount)
		}
	}
}

func BenchmarkSQLiteStoreInsertEvent(b *testing.B) {
	for _, benchmark := range []struct {
		name              string
		dropProviderIndex bool
	}{
		{name: "without_provider_time_index", dropProviderIndex: true},
		{name: "with_provider_time_index"},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			store := newAnalyticsBenchmarkStore(b, 0)
			defer store.Close()
			if benchmark.dropProviderIndex {
				if _, err := store.db.Exec(`DROP INDEX usage_events_provider_time_idx`); err != nil {
					b.Fatal(err)
				}
			}

			base := time.Now().UTC()
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				_, err := store.InsertEvent(context.Background(), Event{
					RequestID: fmt.Sprintf("insert-benchmark-%d", i),
					Timestamp: base.Add(time.Duration(i) * time.Millisecond),
					Provider:  "codex",
					Model:     "gpt-5.6-sol",
					AuthIndex: "auth-01",
					Tokens: TokenUsage{
						InputTokens:  10_000,
						OutputTokens: 1_000,
						TotalTokens:  11_000,
					},
				})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func newAnalyticsBenchmarkStore(tb testing.TB, eventCount int) *SQLiteStore {
	tb.Helper()
	store, err := OpenSQLite(tb.TempDir() + "/analytics-benchmark.sqlite")
	if err != nil {
		tb.Fatal(err)
	}

	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		store.Close()
		tb.Fatal(err)
	}
	base := time.Now().UTC().Add(-7 * 24 * time.Hour)
	failureBody := strings.Repeat("upstream request failed with verbose details; ", 32)
	for i := range eventCount {
		failed := i%10 == 0
		authType := "oauth"
		credentialHash := ""
		if i%3 == 0 {
			authType = "apikey"
			credentialHash = fmt.Sprintf("key-%02d", i%12)
		}
		event := normalizeEvent(Event{
			RequestID:         fmt.Sprintf("benchmark-%d", i),
			Timestamp:         base.Add(time.Duration(i) * time.Second),
			Provider:          []string{"codex", "openai-compatible-opencode-go", "claude"}[i%3],
			Model:             []string{"gpt-5.6-sol", "gpt-5.5", "claude-sonnet-4-5"}[i%3],
			Endpoint:          "/v1/responses",
			AuthIndex:         fmt.Sprintf("auth-%02d", i%24),
			AuthFileName:      fmt.Sprintf("credential-%02d.json", i%24),
			APIKeyHash:        fmt.Sprintf("client-%02d", i%8),
			CredentialKeyHash: credentialHash,
			AccountRef:        fmt.Sprintf("account-%02d", i%24),
			AuthType:          authType,
			ServiceTier:       []string{"", "flex", "priority"}[i%3],
			StatusCode:        map[bool]int{false: 200, true: 429}[failed],
			FailSummary:       map[bool]string{false: "", true: "rate limit"}[failed],
			FailBody:          map[bool]string{false: "", true: failureBody}[failed],
			Failed:            failed,
			Tokens: TokenUsage{
				InputTokens:         10_000,
				OutputTokens:        1_000,
				ReasoningTokens:     500,
				CacheReadTokens:     2_000,
				CacheCreationTokens: 200,
				TotalTokens:         11_000,
			},
		})
		if _, err := insertEventTx(context.Background(), tx, event); err != nil {
			tx.Rollback()
			store.Close()
			tb.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		store.Close()
		tb.Fatal(err)
	}
	return store
}
