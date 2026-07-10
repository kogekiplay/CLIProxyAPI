package usageledger

import (
	"context"
	"strings"
	"testing"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestPluginNormalizesUsageRecord(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	plugin := NewPlugin(store, func() time.Time { return now })
	plugin.HandleUsage(context.Background(), coreusage.Record{
		Provider:  "codex",
		Model:     "gpt-5.5",
		APIKey:    "sk-test-secret",
		AuthIndex: "auth-1",
		AuthType:  "oauth",
		Source:    "codex",
		Detail: coreusage.Detail{
			InputTokens:     10,
			OutputTokens:    5,
			ReasoningTokens: 2,
		},
	})

	summary, err := store.Summary(context.Background(), SummaryFilter{
		Provider:  "codex",
		AuthIndex: "auth-1",
		Window:    Window{Start: now.Add(-time.Minute), End: now.Add(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Tokens.TotalTokens != 17 {
		t.Fatalf("total tokens = %d", summary.Tokens.TotalTokens)
	}
	if summary.Rows[0].Model != "gpt-5.5" {
		t.Fatalf("rows = %#v", summary.Rows)
	}
}

func TestPluginStoresOpenCodeAPIKeyHashAndAccountRef(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	plugin := NewPlugin(store, func() time.Time { return now })
	apiKey := "sk-opencode-secret"
	plugin.HandleUsage(context.Background(), coreusage.Record{
		Provider:          "opencode-go",
		Model:             "claude-sonnet-4",
		APIKey:            "sk-client-key",
		CredentialKeyHash: HashAPIKey(apiKey),
		AuthType:          "apikey",
		Source:            "opencode-go:acc-a",
		Detail:            coreusage.Detail{TotalTokens: 21},
	})

	summary, err := store.Summary(context.Background(), SummaryFilter{
		Provider:   "opencode-go",
		AccountRef: "opencode-go:acc-a",
		Window:     Window{Start: now.Add(-time.Minute), End: now.Add(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Tokens.TotalTokens != 21 {
		t.Fatalf("total tokens = %d", summary.Tokens.TotalTokens)
	}

	result, err := store.Analytics(context.Background(), AnalyticsRequest{
		FromMS: now.Add(-time.Minute).UnixMilli(),
		ToMS:   now.Add(time.Minute).UnixMilli(),
		Include: AnalyticsInclude{
			APIKeyStats: true,
		},
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	if len(result.APIKeyStats) != 1 || result.APIKeyStats[0].APIKeyHash != HashAPIKey(apiKey) {
		t.Fatalf("api key stats = %#v", result.APIKeyStats)
	}
}

func TestPluginStoresMonitoringFieldsFromUsageRecord(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	plugin := NewPlugin(store, func() time.Time { return now })
	ctx := internallogging.WithEndpoint(context.Background(), "POST /v1/chat/completions")
	ctx = internallogging.WithResponseStatusHolder(ctx)
	internallogging.SetResponseStatus(ctx, 200)

	plugin.HandleUsage(ctx, coreusage.Record{
		Provider:        "codex",
		Model:           "gpt-5.5",
		AuthIndex:       "auth-1",
		ReasoningEffort: "ultra",
		RequestedAt:     now,
		Latency:         1500 * time.Millisecond,
		TTFT:            375 * time.Millisecond,
		Detail:          coreusage.Detail{TotalTokens: 23},
	})

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
	if row.StatusCode != 200 || row.LatencyMS == nil || *row.LatencyMS != 1500 || row.TTFTMS == nil || *row.TTFTMS != 375 {
		t.Fatalf("monitoring fields = %#v", row)
	}
	if row.ReasoningEffort != "ultra" {
		t.Fatalf("reasoning effort = %q, want ultra", row.ReasoningEffort)
	}
}

func TestPluginStoresModelAlias(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	plugin := NewPlugin(store, nil)
	plugin.HandleUsage(context.Background(), coreusage.Record{
		Provider: "openai-compatible-cf worker",
		Model:    "@cf/zai-org/glm-5.2",
		Alias:    "glm-5.2",
	})

	var model, modelAlias string
	if err := store.db.QueryRow(`SELECT model, model_alias FROM usage_events LIMIT 1`).Scan(&model, &modelAlias); err != nil {
		t.Fatal(err)
	}
	if model != "@cf/zai-org/glm-5.2" || modelAlias != "glm-5.2" {
		t.Fatalf("model names = %q / %q", model, modelAlias)
	}
}

func TestPluginStoresReasoningEffortFromContextFallback(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 7, 10, 1, 0, 0, 0, time.UTC)
	plugin := NewPlugin(store, func() time.Time { return now })
	ctx := coreusage.WithReasoningEffort(context.Background(), "high")

	plugin.HandleUsage(ctx, coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-5.6-sol",
		RequestedAt: now,
	})

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
	if got := result.Events.Items[0].ReasoningEffort; got != "high" {
		t.Fatalf("reasoning effort = %q, want high", got)
	}
}

func TestPluginStoresSanitizedFailureDetailsFromUsageRecord(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	plugin := NewPlugin(store, func() time.Time { return now })
	ctx := internallogging.WithEndpoint(context.Background(), "POST /v1/responses")
	ctx = internallogging.WithResponseStatusHolder(ctx)

	plugin.HandleUsage(ctx, coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-5.5",
		AuthIndex:   "auth-1",
		RequestedAt: now,
		Failed:      true,
		Fail: coreusage.Failure{
			StatusCode: 503,
			Body:       `{"error":{"message":"upstream failed for sk-test-secret"},"Cookie":"session=secret-value"}`,
		},
	})

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
	if row.StatusCode != 503 || row.FailStatusCode != 503 || row.FailSummary == "" || row.FailBody == "" {
		t.Fatalf("failure fields = %#v", row)
	}
	for _, secret := range []string{"sk-test-secret", "secret-value"} {
		if strings.Contains(row.FailSummary, secret) || strings.Contains(row.FailBody, secret) {
			t.Fatalf("failure detail leaked secret %q: summary=%q body=%q", secret, row.FailSummary, row.FailBody)
		}
	}
	if !strings.Contains(row.FailSummary, "upstream failed") || !strings.Contains(row.FailSummary, "[redacted]") {
		t.Fatalf("fail summary = %q", row.FailSummary)
	}
}
