package management

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/usageledger"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestUsageAnalyticsEndpointReturnsUsageLedgerAnalytics(t *testing.T) {
	store := openManagementUsageStore(t)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, nil)
	h.SetUsageLedger(store)
	router := usageManagementTestRouter(h)

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	if _, err := store.InsertEvent(context.Background(), usageledger.Event{
		RequestID:       "req-analytics",
		Timestamp:       now,
		Provider:        "opencode-go",
		Model:           "kimi-k2.6",
		APIKeyHash:      "key-a",
		AccountRef:      "opencode-go:acct-a",
		ReasoningEffort: "high",
		Tokens:          usageledger.TokenUsage{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	body := map[string]any{
		"from_ms": now.Add(-time.Minute).UnixMilli(),
		"to_ms":   now.Add(time.Minute).UnixMilli(),
		"filters": map[string]any{
			"providers": []string{"opencode-go"},
		},
		"include": map[string]any{
			"summary":       true,
			"model_stats":   true,
			"api_key_stats": true,
			"events_page": map[string]any{
				"limit": 5,
			},
		},
	}
	rec := performUsageManagementJSON(http.MethodPost, "/v0/management/usage-analytics", body, router)
	if rec.Code != http.StatusOK {
		t.Fatalf("analytics status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var response usageledger.AnalyticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode analytics: %v", err)
	}
	if response.Summary == nil || response.Summary.TotalCalls != 1 || response.Summary.TotalTokens != 30 {
		t.Fatalf("summary = %#v", response.Summary)
	}
	if response.Events == nil || len(response.Events.Items) != 1 || response.Events.Items[0].RequestID != "req-analytics" {
		t.Fatalf("events = %#v", response.Events)
	}
	if response.Events.Items[0].ReasoningEffort != "high" {
		t.Fatalf("reasoning effort = %q, want high", response.Events.Items[0].ReasoningEffort)
	}
}

func TestUsageAnalyticsEndpointEnrichesAPIKeyPreviewFromRuntimeAuth(t *testing.T) {
	store := openManagementUsageStore(t)
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       "compat-auth",
		Provider: "openai-compatible-opencode-go",
		Attributes: map[string]string{
			coreauth.AttributeAuthKind: "apikey",
			coreauth.AttributeAPIKey:   "sk-api-key-abcdwxyz",
			"usage_source":             "opencode-go:acc-a",
			"base_url":                 "https://opencode.ai/zen/go/v1",
			"compat_name":              "opencode-go",
		},
	}
	authIndex := auth.EnsureIndex()
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	h.SetUsageLedger(store)
	router := usageManagementTestRouter(h)

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	if _, err := store.InsertEvent(context.Background(), usageledger.Event{
		RequestID: "req-compat-key",
		Timestamp: now,
		Provider:  "openai-compatible-opencode-go",
		Model:     "opencode-gpt-5",
		AuthIndex: authIndex,
		AuthType:  "apikey",
		Tokens:    usageledger.TokenUsage{TotalTokens: 30},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	body := map[string]any{
		"from_ms": now.Add(-time.Minute).UnixMilli(),
		"to_ms":   now.Add(time.Minute).UnixMilli(),
		"include": map[string]any{
			"api_key_stats": true,
		},
	}
	rec := performUsageManagementJSON(http.MethodPost, "/v0/management/usage-analytics", body, router)
	if rec.Code != http.StatusOK {
		t.Fatalf("analytics status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var response usageledger.AnalyticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode analytics: %v", err)
	}
	if len(response.APIKeyStats) != 1 {
		t.Fatalf("api key stats = %#v", response.APIKeyStats)
	}
	if response.APIKeyStats[0].APIKeyPreview != "sk-a***wxyz" {
		t.Fatalf("api key preview = %q", response.APIKeyStats[0].APIKeyPreview)
	}
}

func TestUsageAnalyticsEndpointEnrichesCredentialDisplayNameFromRuntimeAuth(t *testing.T) {
	store := openManagementUsageStore(t)
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       "codex-auth",
		Provider: "codex",
		FileName: "codex-hidden.json",
		Attributes: map[string]string{
			coreauth.AttributeAuthKind: coreauth.AuthKindOAuth,
		},
		Metadata: map[string]any{
			"email": "codex-user@example.com",
		},
	}
	authIndex := auth.EnsureIndex()
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	h.SetUsageLedger(store)
	router := usageManagementTestRouter(h)

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	if _, err := store.InsertEvent(context.Background(), usageledger.Event{
		RequestID: "req-codex-oauth",
		Timestamp: now,
		Provider:  "codex",
		Model:     "gpt-5.3-codex-spark",
		AuthIndex: authIndex,
		AuthType:  "oauth",
		Tokens:    usageledger.TokenUsage{TotalTokens: 30},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	body := map[string]any{
		"from_ms": now.Add(-time.Minute).UnixMilli(),
		"to_ms":   now.Add(time.Minute).UnixMilli(),
		"include": map[string]any{
			"credential_stats": true,
			"events_page": map[string]any{
				"limit": 5,
			},
		},
	}
	rec := performUsageManagementJSON(http.MethodPost, "/v0/management/usage-analytics", body, router)
	if rec.Code != http.StatusOK {
		t.Fatalf("analytics status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var response usageledger.AnalyticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode analytics: %v", err)
	}
	if len(response.CredentialStats) != 1 {
		t.Fatalf("credential stats = %#v", response.CredentialStats)
	}
	if response.CredentialStats[0].CredentialDisplayName != "codex-user@example.com" {
		t.Fatalf("credential display name = %q", response.CredentialStats[0].CredentialDisplayName)
	}
	if response.Events == nil || len(response.Events.Items) != 1 {
		t.Fatalf("events = %#v", response.Events)
	}
	if response.Events.Items[0].CredentialDisplayName != "codex-user@example.com" {
		t.Fatalf("event credential display name = %q", response.Events.Items[0].CredentialDisplayName)
	}
}

func TestUsageAnalyticsEndpointReclassifiesLegacyAPIKeyCredentialStats(t *testing.T) {
	store := openManagementUsageStore(t)
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       "codex-api-key-auth",
		Provider: "codex",
		Label:    "codex-apikey",
		Attributes: map[string]string{
			coreauth.AttributeAuthKind: "apikey",
			coreauth.AttributeAPIKey:   "sk-codex-key-abcdwxyz",
		},
	}
	authIndex := auth.EnsureIndex()
	keyHash := usageledger.HashAPIKey("sk-codex-key-abcdwxyz")
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	h.SetUsageLedger(store)
	router := usageManagementTestRouter(h)

	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	if _, err := store.InsertEvent(context.Background(), usageledger.Event{
		RequestID: "req-codex-key-legacy",
		Timestamp: now,
		Provider:  "codex",
		Model:     "gpt-5.5",
		AuthIndex: authIndex,
		Tokens:    usageledger.TokenUsage{TotalTokens: 30},
	}); err != nil {
		t.Fatalf("insert legacy event: %v", err)
	}
	if _, err := store.InsertEvent(context.Background(), usageledger.Event{
		RequestID:         "req-codex-key-current",
		Timestamp:         now.Add(time.Second),
		Provider:          "codex",
		Model:             "gpt-5.5",
		AuthIndex:         authIndex,
		AuthType:          "apikey",
		CredentialKeyHash: keyHash,
		Tokens:            usageledger.TokenUsage{TotalTokens: 10},
	}); err != nil {
		t.Fatalf("insert current event: %v", err)
	}

	body := map[string]any{
		"from_ms": now.Add(-time.Minute).UnixMilli(),
		"to_ms":   now.Add(time.Minute).UnixMilli(),
		"include": map[string]any{
			"api_key_stats":    true,
			"credential_stats": true,
			"events_page": map[string]any{
				"limit": 5,
			},
		},
	}
	rec := performUsageManagementJSON(http.MethodPost, "/v0/management/usage-analytics", body, router)
	if rec.Code != http.StatusOK {
		t.Fatalf("analytics status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var response usageledger.AnalyticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode analytics: %v", err)
	}
	if len(response.CredentialStats) != 0 {
		t.Fatalf("credential stats = %#v", response.CredentialStats)
	}
	if len(response.APIKeyStats) != 1 {
		t.Fatalf("api key stats = %#v", response.APIKeyStats)
	}
	stat := response.APIKeyStats[0]
	if stat.APIKeyHash != keyHash || stat.Calls != 2 || stat.TotalTokens != 40 {
		t.Fatalf("api key stat = %#v", stat)
	}
	if stat.APIKeyPreview != "sk-c***wxyz" {
		t.Fatalf("api key preview = %q", stat.APIKeyPreview)
	}
	if response.Events == nil || len(response.Events.Items) != 2 {
		t.Fatalf("events = %#v", response.Events)
	}
	for _, event := range response.Events.Items {
		if event.CredentialDisplayName != "codex-sk-c***wxyz" {
			t.Fatalf("event credential display name = %q", event.CredentialDisplayName)
		}
	}
}
