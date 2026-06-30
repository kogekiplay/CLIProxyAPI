package usageledger

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"
)

func TestSQLiteStoreAnalyticsAggregatesFilteredUsage(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	if err := store.UpsertModelPrice(context.Background(), ModelPrice{
		Model:       "gpt-5.5",
		InputPer1M:  10,
		OutputPer1M: 20,
	}); err != nil {
		t.Fatalf("upsert price: %v", err)
	}
	events := []Event{
		{
			RequestID:    "req-current-1",
			Timestamp:    now.Add(-30 * time.Minute),
			Provider:     "codex",
			Model:        "gpt-5.5",
			Endpoint:     "/v1/chat/completions",
			AuthIndex:    "auth-1",
			AuthFileName: "codex-a.json",
			APIKeyHash:   "key-a",
			Tokens:       TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
		{
			RequestID:    "req-current-2",
			Timestamp:    now.Add(-15 * time.Minute),
			Provider:     "codex",
			Model:        "gpt-5.5",
			Endpoint:     "/v1/chat/completions",
			AuthIndex:    "auth-1",
			AuthFileName: "codex-a.json",
			APIKeyHash:   "key-a",
			Tokens:       TokenUsage{InputTokens: 8, OutputTokens: 2, TotalTokens: 10},
			Failed:       true,
		},
		{
			RequestID:    "req-current-3",
			Timestamp:    now.Add(-12 * time.Minute),
			Provider:     "opencode-go",
			Model:        "opencode-gpt-5",
			Endpoint:     "/v1/chat/completions",
			AuthIndex:    "auth-1",
			AuthFileName: "codex-a.json",
			APIKeyHash:   "key-a",
			Tokens:       TokenUsage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5},
		},
		{
			RequestID:    "req-other-auth",
			Timestamp:    now.Add(-10 * time.Minute),
			Provider:     "codex",
			Model:        "gpt-5.5",
			AuthIndex:    "auth-2",
			AuthFileName: "codex-b.json",
			APIKeyHash:   "key-b",
			Tokens:       TokenUsage{TotalTokens: 90},
		},
	}
	for _, event := range events {
		if _, err := store.InsertEvent(context.Background(), event); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}

	result, err := store.Analytics(context.Background(), AnalyticsRequest{
		FromMS: now.Add(-time.Hour).UnixMilli(),
		ToMS:   now.Add(time.Minute).UnixMilli(),
		Filters: AnalyticsFilters{
			Providers:   []string{"codex"},
			AuthIndices: []string{"auth-1"},
		},
		Include: AnalyticsInclude{
			Summary:         true,
			Timeline:        true,
			ModelStats:      true,
			APIKeyStats:     true,
			CredentialStats: true,
			EventsPage:      &AnalyticsEventsPage{Limit: 10},
		},
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if result.Summary == nil || result.Summary.TotalCalls != 2 || result.Summary.FailureCalls != 1 {
		t.Fatalf("summary = %#v", result.Summary)
	}
	if result.Summary.TotalTokens != 25 || result.Summary.InputTokens != 18 || result.Summary.OutputTokens != 7 {
		t.Fatalf("summary tokens = %#v", result.Summary)
	}
	if result.Summary.TotalCost == nil || math.Abs(*result.Summary.TotalCost-0.00032) > 0.000000001 {
		t.Fatalf("summary cost = %v, want 0.00032", result.Summary.TotalCost)
	}
	if len(result.ModelStats) != 1 || result.ModelStats[0].Model != "gpt-5.5" || result.ModelStats[0].Calls != 2 {
		t.Fatalf("model stats = %#v", result.ModelStats)
	}
	if len(result.APIKeyStats) != 0 {
		t.Fatalf("api key stats = %#v", result.APIKeyStats)
	}
	if len(result.CredentialStats) != 1 || result.CredentialStats[0].Provider != "codex" || result.CredentialStats[0].AuthIndex != "auth-1" || result.CredentialStats[0].AuthFileName != "codex-a.json" {
		t.Fatalf("credential stats = %#v", result.CredentialStats)
	}
	if len(result.Timeline) == 0 || result.Timeline[0].Calls != 2 {
		t.Fatalf("timeline = %#v", result.Timeline)
	}
	if result.Events == nil || len(result.Events.Items) != 2 || result.Events.Items[0].RequestID != "req-current-2" {
		t.Fatalf("events = %#v", result.Events)
	}
}

func TestSQLiteStoreAnalyticsReturnsKnownCostWhenSomeModelsAreUnpriced(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	if err := store.UpsertModelPrice(context.Background(), ModelPrice{
		Model:       "gpt-5.5",
		InputPer1M:  10,
		OutputPer1M: 20,
	}); err != nil {
		t.Fatalf("upsert price: %v", err)
	}

	events := []Event{
		{
			RequestID:         "req-priced",
			Timestamp:         now.Add(-30 * time.Minute),
			Provider:          "codex",
			Model:             "gpt-5.5",
			AuthType:          "apikey",
			CredentialKeyHash: "key-a",
			Tokens:            TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
		{
			RequestID:         "req-unpriced",
			Timestamp:         now.Add(-20 * time.Minute),
			Provider:          "xai",
			Model:             "grok-unpriced-preview",
			AuthType:          "apikey",
			CredentialKeyHash: "key-a",
			Tokens:            TokenUsage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
	}
	for _, event := range events {
		if _, err := store.InsertEvent(context.Background(), event); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}

	result, err := store.Analytics(context.Background(), AnalyticsRequest{
		FromMS: now.Add(-time.Hour).UnixMilli(),
		ToMS:   now.Add(time.Minute).UnixMilli(),
		Include: AnalyticsInclude{
			Summary:     true,
			Timeline:    true,
			ModelStats:  true,
			APIKeyStats: true,
		},
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	const wantCost = 0.0002
	if result.Summary == nil || result.Summary.TotalCost == nil || math.Abs(*result.Summary.TotalCost-wantCost) > 0.000000001 {
		t.Fatalf("summary cost = %v, want %v", result.Summary, wantCost)
	}
	if len(result.Timeline) != 1 || result.Timeline[0].Cost == nil || math.Abs(*result.Timeline[0].Cost-wantCost) > 0.000000001 {
		t.Fatalf("timeline = %#v, want known cost %v", result.Timeline, wantCost)
	}
	if len(result.APIKeyStats) != 1 || result.APIKeyStats[0].Cost == nil || math.Abs(*result.APIKeyStats[0].Cost-wantCost) > 0.000000001 {
		t.Fatalf("api key stats = %#v, want known cost %v", result.APIKeyStats, wantCost)
	}
	if len(result.ModelStats) != 2 {
		t.Fatalf("model stats = %#v", result.ModelStats)
	}
	var priced, unpriced *AnalyticsModelStat
	for i := range result.ModelStats {
		switch result.ModelStats[i].Model {
		case "gpt-5.5":
			priced = &result.ModelStats[i]
		case "grok-unpriced-preview":
			unpriced = &result.ModelStats[i]
		}
	}
	if priced == nil || priced.Cost == nil || math.Abs(*priced.Cost-wantCost) > 0.000000001 {
		t.Fatalf("priced model stat = %#v, want known cost %v", priced, wantCost)
	}
	if unpriced == nil || unpriced.Cost != nil {
		t.Fatalf("unpriced model stat = %#v, want nil cost", unpriced)
	}
}

func TestSQLiteStoreAnalyticsEventsExposeRequestMonitoringFields(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	if _, err := store.InsertEvent(context.Background(), Event{
		RequestID:      "req-monitoring",
		Timestamp:      now,
		Provider:       "codex",
		Model:          "gpt-5.5",
		Endpoint:       "POST /v1/responses",
		AuthIndex:      "auth-1",
		StatusCode:     429,
		LatencyMS:      1530,
		TTFTMS:         420,
		Failed:         true,
		FailStatusCode: 429,
		FailBody:       `{"error":{"message":"rate limit for sk-secret-value","code":"rate_limit_exceeded"},"Authorization":"Bearer bearer-secret-12345"}`,
		Tokens:         TokenUsage{TotalTokens: 0},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	result, err := store.Analytics(context.Background(), AnalyticsRequest{
		FromMS: now.Add(-time.Minute).UnixMilli(),
		ToMS:   now.Add(time.Minute).UnixMilli(),
		Include: AnalyticsInclude{
			EventsPage: &AnalyticsEventsPage{Limit: 10},
		},
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	if result.Events == nil || len(result.Events.Items) != 1 {
		t.Fatalf("events = %#v", result.Events)
	}
	row := result.Events.Items[0]
	if row.StatusCode != 429 || row.FailStatusCode != 429 || row.LatencyMS == nil || *row.LatencyMS != 1530 || row.TTFTMS == nil || *row.TTFTMS != 420 {
		t.Fatalf("monitoring fields = %#v", row)
	}
	if row.FailSummary == "" || row.FailBody == "" {
		t.Fatalf("fail summary/body missing: %#v", row)
	}
	for _, secret := range []string{"sk-secret-value", "bearer-secret-12345"} {
		if strings.Contains(row.FailSummary, secret) || strings.Contains(row.FailBody, secret) {
			t.Fatalf("failure detail leaked secret %q: summary=%q body=%q", secret, row.FailSummary, row.FailBody)
		}
	}
	if !strings.Contains(row.FailSummary, "rate limit") || !strings.Contains(row.FailSummary, "[redacted]") {
		t.Fatalf("fail summary = %q", row.FailSummary)
	}
}

func TestSQLiteStoreAnalyticsAPIKeyStatsGroupByKeyAcrossProviders(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	events := []Event{
		{
			RequestID:         "req-key-codex",
			Timestamp:         now.Add(-20 * time.Minute),
			Provider:          "codex",
			Model:             "gpt-5.5",
			AuthType:          "apikey",
			CredentialKeyHash: "shared-key",
			Tokens:            TokenUsage{TotalTokens: 10},
		},
		{
			RequestID:         "req-key-opencode",
			Timestamp:         now.Add(-10 * time.Minute),
			Provider:          "opencode-go",
			Model:             "opencode-gpt-5",
			AuthType:          "apikey",
			CredentialKeyHash: "shared-key",
			Tokens:            TokenUsage{TotalTokens: 15},
		},
	}
	for _, event := range events {
		if _, err := store.InsertEvent(context.Background(), event); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}

	result, err := store.Analytics(context.Background(), AnalyticsRequest{
		FromMS: now.Add(-time.Hour).UnixMilli(),
		ToMS:   now.Add(time.Minute).UnixMilli(),
		Include: AnalyticsInclude{
			APIKeyStats: true,
		},
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if len(result.APIKeyStats) != 1 {
		t.Fatalf("api key stats = %#v", result.APIKeyStats)
	}
	stat := result.APIKeyStats[0]
	if stat.APIKeyHash != "shared-key" || stat.Calls != 2 || stat.TotalTokens != 25 {
		t.Fatalf("api key stat = %#v", stat)
	}
	if len(stat.Providers) != 2 || stat.Providers[0] != "codex" || stat.Providers[1] != "opencode-go" {
		t.Fatalf("api key stat providers = %#v", stat.Providers)
	}
}

func TestSQLiteStoreAnalyticsCredentialStatsExcludeAPIKeyOnlyProviders(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	events := []Event{
		{
			RequestID:    "req-codex-auth",
			Timestamp:    now.Add(-20 * time.Minute),
			Provider:     "codex",
			Model:        "gpt-5.5",
			AuthIndex:    "codex-auth-1",
			AuthFileName: "codex-a.json",
			Tokens:       TokenUsage{TotalTokens: 10},
		},
		{
			RequestID: "req-opencode-key",
			Timestamp: now.Add(-10 * time.Minute),
			Provider:  "openai-compatible-opencode-go",
			Model:     "opencode-gpt-5",
			AuthIndex: "opencode-auth-index",
			Tokens:    TokenUsage{TotalTokens: 15},
		},
	}
	for _, event := range events {
		if _, err := store.InsertEvent(context.Background(), event); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}

	result, err := store.Analytics(context.Background(), AnalyticsRequest{
		FromMS: now.Add(-time.Hour).UnixMilli(),
		ToMS:   now.Add(time.Minute).UnixMilli(),
		Include: AnalyticsInclude{
			APIKeyStats:     true,
			CredentialStats: true,
		},
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if len(result.CredentialStats) != 1 {
		t.Fatalf("credential stats = %#v", result.CredentialStats)
	}
	if result.CredentialStats[0].Provider != "codex" || result.CredentialStats[0].AuthIndex != "codex-auth-1" {
		t.Fatalf("credential stat = %#v", result.CredentialStats[0])
	}
	if len(result.APIKeyStats) != 1 {
		t.Fatalf("api key stats = %#v", result.APIKeyStats)
	}
	if result.APIKeyStats[0].Provider != "openai-compatible-opencode-go" || result.APIKeyStats[0].APIKeyHash != "opencode-auth-index" {
		t.Fatalf("api key stat = %#v", result.APIKeyStats[0])
	}
}

func TestSQLiteStoreAnalyticsTreatsLegacyConfiguredAPIKeyAuthAsAPIKey(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	if _, err := store.InsertEvent(context.Background(), Event{
		RequestID:    "req-codex-config-key",
		Timestamp:    now.Add(-10 * time.Minute),
		Provider:     "codex",
		Model:        "gpt-5.5",
		AuthIndex:    "5290ade912c45533",
		AuthFileName: "codex-apikey",
		Tokens:       TokenUsage{TotalTokens: 15},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	result, err := store.Analytics(context.Background(), AnalyticsRequest{
		FromMS: now.Add(-time.Hour).UnixMilli(),
		ToMS:   now.Add(time.Minute).UnixMilli(),
		Include: AnalyticsInclude{
			APIKeyStats:     true,
			CredentialStats: true,
		},
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if len(result.CredentialStats) != 0 {
		t.Fatalf("credential stats = %#v", result.CredentialStats)
	}
	if len(result.APIKeyStats) != 1 {
		t.Fatalf("api key stats = %#v", result.APIKeyStats)
	}
	if result.APIKeyStats[0].Provider != "codex" || result.APIKeyStats[0].APIKeyHash != "5290ade912c45533" {
		t.Fatalf("api key stat = %#v", result.APIKeyStats[0])
	}
}
