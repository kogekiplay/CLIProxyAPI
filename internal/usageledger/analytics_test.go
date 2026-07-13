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

func TestSQLiteStoreAnalyticsEventsOnlyPaginatesNewestFirst(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	base := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	events := []Event{
		{RequestID: "req-oldest", Timestamp: base.Add(-time.Second), Provider: "codex", Model: "gpt-5.5"},
		{RequestID: "req-same-ms-1", Timestamp: base.Add(100 * time.Microsecond), Provider: "codex", Model: "gpt-5.5"},
		{RequestID: "req-same-ms-2", Timestamp: base.Add(200 * time.Microsecond), Provider: "codex", Model: "gpt-5.5"},
		{RequestID: "req-newest", Timestamp: base.Add(time.Second), Provider: "codex", Model: "gpt-5.5"},
		{RequestID: "req-other-provider", Timestamp: base.Add(2 * time.Second), Provider: "gemini", Model: "gemini-2.5-pro"},
	}
	for _, event := range events {
		if _, err := store.InsertEvent(context.Background(), event); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}

	request := AnalyticsRequest{
		FromMS: base.Add(-time.Minute).UnixMilli(),
		ToMS:   base.Add(time.Minute).UnixMilli(),
		Filters: AnalyticsFilters{
			Providers: []string{"codex"},
		},
		Include: AnalyticsInclude{
			EventsPage: &AnalyticsEventsPage{Limit: 2},
		},
	}
	first, err := store.Analytics(context.Background(), request)
	if err != nil {
		t.Fatalf("analytics first page: %v", err)
	}
	if first.Summary != nil || len(first.Timeline) != 0 || len(first.ModelStats) != 0 {
		t.Fatalf("events-only response unexpectedly contains stats: %#v", first)
	}
	if first.Events == nil || first.Events.TotalCount != 4 || !first.Events.HasMore {
		t.Fatalf("first page metadata = %#v", first.Events)
	}
	if got := []string{first.Events.Items[0].RequestID, first.Events.Items[1].RequestID}; got[0] != "req-newest" || got[1] != "req-same-ms-2" {
		t.Fatalf("first page order = %v", got)
	}

	request.Include.EventsPage.BeforeMS = &first.Events.NextBeforeMS
	request.Include.EventsPage.BeforeID = &first.Events.NextBeforeID
	second, err := store.Analytics(context.Background(), request)
	if err != nil {
		t.Fatalf("analytics second page: %v", err)
	}
	if second.Events == nil || second.Events.TotalCount != 4 || second.Events.HasMore {
		t.Fatalf("second page metadata = %#v", second.Events)
	}
	if got := []string{second.Events.Items[0].RequestID, second.Events.Items[1].RequestID}; got[0] != "req-same-ms-1" || got[1] != "req-oldest" {
		t.Fatalf("second page order = %v", got)
	}

	var indexName string
	if err := store.db.QueryRowContext(context.Background(), `SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'usage_events_time_idx'`).Scan(&indexName); err != nil {
		t.Fatalf("lookup usage events time index: %v", err)
	}
}

func TestSQLiteStoreAnalyticsEventsReadsModelAlias(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 7, 10, 1, 0, 0, 0, time.UTC)
	if _, err := store.InsertEvent(context.Background(), Event{
		RequestID:  "req-model-alias-readback",
		Timestamp:  now,
		Provider:   "openai-compatible-cf worker",
		Model:      "@cf/zai-org/glm-5.2",
		ModelAlias: "glm-5.2",
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	events, err := store.analyticsEvents(context.Background(), AnalyticsRequest{
		FromMS: now.Add(-time.Minute).UnixMilli(),
		ToMS:   now.Add(time.Minute).UnixMilli(),
	}, nil)
	if err != nil {
		t.Fatalf("analytics events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one event", events)
	}
	if got := events[0].event.ModelAlias; got != "glm-5.2" {
		t.Fatalf("model alias = %q, want glm-5.2", got)
	}
}

func TestSQLiteStoreAnalyticsResolveAliasRules(t *testing.T) {
	rules := []ModelAliasRule{
		{
			Provider:      "openai-compatible-cf worker",
			AuthIndex:     "auth-cf",
			UpstreamModel: "@cf/zai-org/glm-5.2",
			Alias:         "glm-5.2",
		},
		{
			Provider:      "openai-compatible-cf worker",
			AuthIndex:     "auth-other",
			UpstreamModel: "@cf/zai-org/glm-5.2",
			Alias:         "glm-5.2-other",
		},
	}

	tests := []struct {
		name  string
		event Event
		rules []ModelAliasRule
		want  string
	}{
		{
			name: "stored alias wins",
			event: Event{
				Provider:   "openai-compatible-cf worker",
				AuthIndex:  "auth-cf",
				Model:      "@cf/zai-org/glm-5.2",
				ModelAlias: "stored-glm-5.2",
			},
			rules: rules,
			want:  "stored-glm-5.2",
		},
		{
			name: "exact mapping is case insensitive",
			event: Event{
				Provider:  "OPENAI-COMPATIBLE-CF WORKER",
				AuthIndex: "AUTH-CF",
				Model:     "@CF/ZAI-ORG/GLM-5.2",
			},
			rules: rules,
			want:  "glm-5.2",
		},
		{
			name: "unique provider fallback",
			event: Event{
				Provider:  "openai-compatible-cf worker",
				AuthIndex: "unmapped-auth",
				Model:     "@cf/zai-org/glm-5.2",
			},
			rules: []ModelAliasRule{rules[0]},
			want:  "glm-5.2",
		},
		{
			name: "conflicting provider fallback keeps upstream model",
			event: Event{
				Provider:  "openai-compatible-cf worker",
				AuthIndex: "unmapped-auth",
				Model:     "@cf/zai-org/glm-5.2",
			},
			rules: rules,
			want:  "@cf/zai-org/glm-5.2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveAnalyticsModel(test.event, test.rules); got != test.want {
				t.Fatalf("resolveAnalyticsModel() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCompiledModelAliasIndexPreservesResolutionAndFilterExpansion(t *testing.T) {
	index := compileModelAliasIndex([]ModelAliasRule{
		{Provider: "Provider", AuthIndex: "auth-a", UpstreamModel: "shared-upstream", Alias: "alias-a"},
		{Provider: "Provider", AuthIndex: "auth-a", UpstreamModel: "shared-upstream", Alias: "alias-b"},
		{Provider: "Provider", AuthIndex: "auth-b", UpstreamModel: "shared-upstream", Alias: "alias-a"},
	})

	if got := index.resolve(Event{Provider: "PROVIDER", AuthIndex: "AUTH-A", Model: "SHARED-UPSTREAM"}); got != "SHARED-UPSTREAM" {
		t.Fatalf("exact conflict resolution = %q, want upstream model", got)
	}
	if got := index.resolve(Event{Provider: "provider", AuthIndex: "unmapped", Model: "shared-upstream"}); got != "shared-upstream" {
		t.Fatalf("provider conflict resolution = %q, want upstream model", got)
	}
	if got := index.resolve(Event{Provider: "provider", AuthIndex: "auth-b", Model: "shared-upstream"}); got != "alias-a" {
		t.Fatalf("exact resolution = %q, want configured alias spelling", got)
	}

	candidates := index.expandRequestedModels([]string{"ALIAS-A"})
	if len(candidates) != 2 || candidates[0] != "ALIAS-A" || candidates[1] != "shared-upstream" {
		t.Fatalf("filter candidates = %#v, want requested alias plus upstream", candidates)
	}
}

func TestSQLiteStoreAnalyticsHistoricalAliasPricingAndFilters(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	const (
		provider = "openai-compatible-cf worker"
		upstream = "@cf/zai-org/glm-5.2"
		alias    = "glm-5.2"
	)
	now := time.Date(2026, 7, 10, 1, 0, 0, 0, time.UTC)
	rules := []ModelAliasRule{{
		Provider:      provider,
		AuthIndex:     "auth-cf",
		UpstreamModel: upstream,
		Alias:         alias,
	}}
	if err := store.UpsertModelPrice(context.Background(), ModelPrice{
		Model:      alias,
		InputPer1M: 10,
	}); err != nil {
		t.Fatalf("upsert alias price: %v", err)
	}
	for _, event := range []Event{
		{
			RequestID:         "req-historical-alias-apikey",
			Timestamp:         now.Add(-2 * time.Minute),
			Provider:          provider,
			Model:             upstream,
			AuthIndex:         "auth-cf",
			AuthType:          "api-key",
			CredentialKeyHash: "key-cf",
			Tokens:            TokenUsage{InputTokens: 100, TotalTokens: 100},
		},
		{
			RequestID:    "req-historical-alias-credential",
			Timestamp:    now.Add(-time.Minute),
			Provider:     provider,
			Model:        upstream,
			AuthIndex:    "auth-cf",
			AuthFileName: "cf.json",
			AuthType:     "oauth",
			Tokens:       TokenUsage{InputTokens: 100, TotalTokens: 100},
		},
	} {
		if _, err := store.InsertEvent(context.Background(), event); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}

	request := AnalyticsRequest{
		FromMS:       now.Add(-time.Hour).UnixMilli(),
		ToMS:         now.Add(time.Minute).UnixMilli(),
		ModelAliases: rules,
		Include: AnalyticsInclude{
			Summary:         true,
			Timeline:        true,
			ModelStats:      true,
			APIKeyStats:     true,
			CredentialStats: true,
			EventsPage:      &AnalyticsEventsPage{Limit: 10},
		},
	}
	result, err := store.Analytics(context.Background(), request)
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	if result.Events == nil || len(result.Events.Items) != 2 {
		t.Fatalf("events = %#v", result.Events)
	}
	for _, item := range result.Events.Items {
		if item.Model != alias || item.UpstreamModel != upstream || item.EstimatedCostUSD == nil {
			t.Fatalf("event = %#v", item)
		}
	}
	const wantCost = 0.002
	if result.Summary == nil || result.Summary.TotalCost == nil || math.Abs(*result.Summary.TotalCost-wantCost) > 0.000000001 {
		t.Fatalf("summary = %#v, want cost %v", result.Summary, wantCost)
	}
	if len(result.Timeline) != 1 || result.Timeline[0].Cost == nil || math.Abs(*result.Timeline[0].Cost-wantCost) > 0.000000001 {
		t.Fatalf("timeline = %#v, want cost %v", result.Timeline, wantCost)
	}
	if len(result.ModelStats) != 1 || result.ModelStats[0].Model != alias || result.ModelStats[0].Cost == nil || math.Abs(*result.ModelStats[0].Cost-wantCost) > 0.000000001 {
		t.Fatalf("model stats = %#v, want alias %q with cost %v", result.ModelStats, alias, wantCost)
	}
	if len(result.APIKeyStats) != 1 || result.APIKeyStats[0].Cost == nil || math.Abs(*result.APIKeyStats[0].Cost-0.001) > 0.000000001 {
		t.Fatalf("api key stats = %#v", result.APIKeyStats)
	}
	if len(result.CredentialStats) != 1 || result.CredentialStats[0].Cost == nil || math.Abs(*result.CredentialStats[0].Cost-0.001) > 0.000000001 {
		t.Fatalf("credential stats = %#v", result.CredentialStats)
	}

	for _, model := range []string{alias, upstream} {
		t.Run("filter "+model, func(t *testing.T) {
			filtered := request
			filtered.Filters.Models = []string{model}
			result, err := store.Analytics(context.Background(), filtered)
			if err != nil {
				t.Fatalf("analytics: %v", err)
			}
			if result.Events == nil || len(result.Events.Items) != 2 {
				t.Fatalf("events = %#v, want both historical events", result.Events)
			}
		})
	}
}

func TestSQLiteStoreAnalyticsAliasFilterMatchesEffectiveModelPerAuth(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	const (
		provider = "provider-a"
		upstream = "shared-upstream"
		aliasA   = "alias-a"
		aliasB   = "alias-b"
	)
	now := time.Date(2026, 7, 10, 1, 0, 0, 0, time.UTC)
	for _, event := range []Event{
		{
			RequestID: "req-auth-a",
			Timestamp: now.Add(-2 * time.Minute),
			Provider:  provider,
			Model:     upstream,
			AuthIndex: "auth-a",
			Tokens:    TokenUsage{InputTokens: 2, TotalTokens: 2},
		},
		{
			RequestID: "req-auth-b",
			Timestamp: now.Add(-time.Minute),
			Provider:  provider,
			Model:     upstream,
			AuthIndex: "auth-b",
			Tokens:    TokenUsage{InputTokens: 3, TotalTokens: 3},
		},
	} {
		if _, err := store.InsertEvent(context.Background(), event); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}
	if err := store.UpsertModelPrice(context.Background(), ModelPrice{Model: aliasA, InputPer1M: 1_000_000}); err != nil {
		t.Fatalf("upsert alias price: %v", err)
	}

	request := AnalyticsRequest{
		FromMS: now.Add(-time.Hour).UnixMilli(),
		ToMS:   now.Add(time.Minute).UnixMilli(),
		ModelAliases: []ModelAliasRule{
			{Provider: provider, AuthIndex: "auth-a", UpstreamModel: upstream, Alias: aliasA},
			{Provider: provider, AuthIndex: "auth-b", UpstreamModel: upstream, Alias: aliasB},
		},
		Include: AnalyticsInclude{
			Summary:         true,
			Timeline:        true,
			ModelStats:      true,
			CredentialStats: true,
			EventsPage:      &AnalyticsEventsPage{Limit: 10},
		},
	}

	t.Run("alias only includes its matching auth and aggregates", func(t *testing.T) {
		filtered := request
		filtered.Filters.Models = []string{aliasA}
		result, err := store.Analytics(context.Background(), filtered)
		if err != nil {
			t.Fatalf("analytics: %v", err)
		}
		if result.Summary == nil || result.Summary.TotalCalls != 1 || result.Summary.TotalTokens != 2 || result.Summary.TotalCost == nil || math.Abs(*result.Summary.TotalCost-2) > 0.000000001 {
			t.Fatalf("summary = %#v", result.Summary)
		}
		if len(result.Timeline) != 1 || result.Timeline[0].Calls != 1 || result.Timeline[0].Cost == nil || math.Abs(*result.Timeline[0].Cost-2) > 0.000000001 {
			t.Fatalf("timeline = %#v", result.Timeline)
		}
		if len(result.ModelStats) != 1 || result.ModelStats[0].Model != aliasA || result.ModelStats[0].Calls != 1 || result.ModelStats[0].Cost == nil || math.Abs(*result.ModelStats[0].Cost-2) > 0.000000001 {
			t.Fatalf("model stats = %#v", result.ModelStats)
		}
		if len(result.CredentialStats) != 1 || result.CredentialStats[0].AuthIndex != "auth-a" || result.CredentialStats[0].Calls != 1 {
			t.Fatalf("credential stats = %#v", result.CredentialStats)
		}
		if result.Events == nil || len(result.Events.Items) != 1 || result.Events.Items[0].RequestID != "req-auth-a" || result.Events.Items[0].EstimatedCostUSD == nil || math.Abs(*result.Events.Items[0].EstimatedCostUSD-2) > 0.000000001 {
			t.Fatalf("events = %#v", result.Events)
		}
	})

	t.Run("upstream includes both auths", func(t *testing.T) {
		filtered := request
		filtered.Filters.Models = []string{upstream}
		result, err := store.Analytics(context.Background(), filtered)
		if err != nil {
			t.Fatalf("analytics: %v", err)
		}
		if result.Summary == nil || result.Summary.TotalCalls != 2 || result.Summary.TotalTokens != 5 {
			t.Fatalf("summary = %#v", result.Summary)
		}
		if result.Events == nil || len(result.Events.Items) != 2 {
			t.Fatalf("events = %#v", result.Events)
		}
	})
}

func TestSQLiteStoreAnalyticsAliasFilterDoesNotOverrideStoredAliasOrCrossProvider(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	const (
		providerA = "provider-a"
		providerB = "provider-b"
		upstream  = "shared-upstream"
		aliasA    = "alias-a"
		aliasB    = "alias-b"
	)
	now := time.Date(2026, 7, 10, 1, 0, 0, 0, time.UTC)
	for _, event := range []Event{
		{
			RequestID:  "req-stored-alias-b",
			Timestamp:  now.Add(-2 * time.Minute),
			Provider:   providerA,
			Model:      upstream,
			ModelAlias: aliasB,
			AuthIndex:  "auth-a",
			Tokens:     TokenUsage{TotalTokens: 2},
		},
		{
			RequestID: "req-other-provider",
			Timestamp: now.Add(-time.Minute),
			Provider:  providerB,
			Model:     upstream,
			AuthIndex: "auth-a",
			Tokens:    TokenUsage{TotalTokens: 3},
		},
	} {
		if _, err := store.InsertEvent(context.Background(), event); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}

	result, err := store.Analytics(context.Background(), AnalyticsRequest{
		FromMS: now.Add(-time.Hour).UnixMilli(),
		ToMS:   now.Add(time.Minute).UnixMilli(),
		Filters: AnalyticsFilters{
			Models: []string{aliasA},
		},
		ModelAliases: []ModelAliasRule{{
			Provider:      providerA,
			AuthIndex:     "auth-a",
			UpstreamModel: upstream,
			Alias:         aliasA,
		}},
		Include: AnalyticsInclude{
			Summary:    true,
			ModelStats: true,
			EventsPage: &AnalyticsEventsPage{Limit: 10},
		},
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	if result.Summary == nil || result.Summary.TotalCalls != 0 || result.Summary.TotalTokens != 0 {
		t.Fatalf("summary = %#v", result.Summary)
	}
	if len(result.ModelStats) != 0 {
		t.Fatalf("model stats = %#v", result.ModelStats)
	}
	if result.Events == nil || len(result.Events.Items) != 0 {
		t.Fatalf("events = %#v", result.Events)
	}
}

func TestSQLiteStoreAnalyticsAliasFilterStaysScopedInSQL(t *testing.T) {
	where, args := buildAnalyticsWhere(AnalyticsRequest{
		FromMS: 100,
		ToMS:   200,
		Filters: AnalyticsFilters{
			Models: []string{"glm-5.2"},
		},
		ModelAliases: []ModelAliasRule{{
			Provider:      "openai-compatible-cf worker",
			AuthIndex:     "auth-cf",
			UpstreamModel: "@cf/zai-org/glm-5.2",
			Alias:         "glm-5.2",
		}},
	})
	if !strings.Contains(where, "(model_alias COLLATE NOCASE IN (?) OR model COLLATE NOCASE IN (?,?))") {
		t.Fatalf("where = %q", where)
	}
	if len(args) != 5 || args[2] != "glm-5.2" || args[3] != "glm-5.2" || args[4] != "@cf/zai-org/glm-5.2" {
		t.Fatalf("args = %#v", args)
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
		RequestID:       "req-monitoring",
		Timestamp:       now,
		Provider:        "codex",
		Model:           "gpt-5.5",
		ReasoningEffort: "max",
		Endpoint:        "POST /v1/responses",
		AuthIndex:       "auth-1",
		StatusCode:      429,
		LatencyMS:       1530,
		TTFTMS:          420,
		Failed:          true,
		FailStatusCode:  429,
		FailBody:        `{"error":{"message":"rate limit for sk-secret-value","code":"rate_limit_exceeded"},"Authorization":"Bearer bearer-secret-12345"}`,
		Tokens:          TokenUsage{TotalTokens: 0},
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
	if row.ReasoningEffort != "max" {
		t.Fatalf("reasoning effort = %q, want max", row.ReasoningEffort)
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
