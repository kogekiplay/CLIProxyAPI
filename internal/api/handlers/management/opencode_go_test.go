package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func performOpenCodeGoJSON(method, target string, body any, handler func(*gin.Context)) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	c.Request = httptest.NewRequest(method, target, reader)
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return rec
}

func performOpenCodeGoRouteJSON(method, target string, body any, router http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

func openCodeGoTestRouter(h *Handler) *gin.Engine {
	router := gin.New()
	group := router.Group("/v0/management/opencode-go")
	group.GET("/accounts", h.ListOpenCodeGoAccounts)
	group.POST("/sync", h.SyncOpenCodeGoAccount)
	group.POST("/accounts/:id/sync-provider", h.SyncOpenCodeGoProvider)
	group.DELETE("/accounts/:id", h.DeleteOpenCodeGoAccount)
	group.GET("/accounts/:id/switch-cookie", h.GetOpenCodeGoSwitchCookie)
	group.GET("/userscript-config", h.GetOpenCodeGoUserscriptConfig)
	return router
}

func TestOpenCodeGoSyncCreatesAccountAndRedactsList(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{}, configFilePath: writeTestConfigFile(t)}

	rec := performOpenCodeGoJSON(http.MethodPost, "/v0/management/opencode-go/sync", map[string]any{
		"alias":        "main",
		"email":        "user@example.com",
		"workspace-id": "ws_123",
		"api-key":      "sk-abcdefghijklmnopqrstuvwxyz",
		"cookie":       "session=secret",
		"usage": map[string]any{
			"rolling": map[string]any{"used": 12, "limit": 100},
		},
	}, h.SyncOpenCodeGoAccount)

	if rec.Code != http.StatusOK {
		t.Fatalf("sync status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := len(h.cfg.OpenCodeGo.Accounts); got != 1 {
		t.Fatalf("accounts len = %d, want 1", got)
	}

	list := performOpenCodeGoJSON(http.MethodGet, "/v0/management/opencode-go/accounts", nil, h.ListOpenCodeGoAccounts)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", list.Code, list.Body.String())
	}
	var body struct {
		Accounts []struct {
			APIKeyPreview string `json:"api-key-preview"`
			APIKey        string `json:"api-key"`
			Cookie        string `json:"cookie"`
			HasCookie     bool   `json:"has-cookie"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list response: %v; body=%s", err, list.Body.String())
	}
	if len(body.Accounts) != 1 {
		t.Fatalf("accounts len = %d, want 1; body=%s", len(body.Accounts), list.Body.String())
	}
	account := body.Accounts[0]
	if account.APIKeyPreview != "sk-a***wxyz" {
		t.Fatalf("api-key-preview = %q, want sk-a***wxyz", account.APIKeyPreview)
	}
	if account.APIKey != "" || account.Cookie != "" {
		t.Fatalf("list leaked full secret fields: %#v", account)
	}
	if !account.HasCookie {
		t.Fatalf("has-cookie = false, want true")
	}
}

func TestOpenCodeGoSyncUpdatesByWorkspaceAndPreservesOldUsageWhenOmitted(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{}, configFilePath: writeTestConfigFile(t)}

	performOpenCodeGoJSON(http.MethodPost, "/v0/management/opencode-go/sync", map[string]any{
		"alias":        "before",
		"workspace-id": "ws_123",
		"api-key":      "sk-before",
		"usage": map[string]any{
			"weekly": map[string]any{"used": 7, "limit": 20},
		},
	}, h.SyncOpenCodeGoAccount)
	performOpenCodeGoJSON(http.MethodPost, "/v0/management/opencode-go/sync", map[string]any{
		"alias":        "after",
		"workspace-id": "ws_123",
		"api-key":      "sk-after",
	}, h.SyncOpenCodeGoAccount)

	if got := len(h.cfg.OpenCodeGo.Accounts); got != 1 {
		t.Fatalf("accounts len = %d, want 1", got)
	}
	account := h.cfg.OpenCodeGo.Accounts[0]
	if account.Alias != "after" || account.APIKey != "sk-after" {
		t.Fatalf("account not updated: %#v", account)
	}
	if account.Usage.Weekly.Used != 7 {
		t.Fatalf("weekly usage = %v, want preserved 7", account.Usage.Weekly.Used)
	}
}

func TestOpenCodeGoGinRoutesHitAccountsAndSync(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{}, configFilePath: writeTestConfigFile(t)}
	router := openCodeGoTestRouter(h)

	list := performOpenCodeGoRouteJSON(http.MethodGet, "/v0/management/opencode-go/accounts", nil, router)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", list.Code, list.Body.String())
	}

	sync := performOpenCodeGoRouteJSON(http.MethodPost, "/v0/management/opencode-go/sync", map[string]any{
		"id":      "acc_route",
		"api-key": "sk-route",
	}, router)
	if sync.Code != http.StatusOK {
		t.Fatalf("sync status = %d, want 200; body=%s", sync.Code, sync.Body.String())
	}
	if got := len(h.cfg.OpenCodeGo.Accounts); got != 1 {
		t.Fatalf("accounts len = %d, want 1", got)
	}
}

func TestOpenCodeGoSyncProviderRequiresBaseURL(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			Accounts: []config.OpenCodeGoAccount{{ID: "acc_1", APIKey: "sk-open"}},
		},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performOpenCodeGoRouteJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_1/sync-provider", nil, openCodeGoTestRouter(h))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "base-url") {
		t.Fatalf("missing base-url error: %s", rec.Body.String())
	}
	if got := h.cfg.OpenCodeGo.Accounts[0].ProviderSyncError; !strings.Contains(got, "base-url") {
		t.Fatalf("provider sync error = %q, want base-url error", got)
	}
}

func TestOpenCodeGoSyncProviderUpsertsOpenAICompatibilityEntry(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			ProviderName: "opencode-go",
			BaseURL:      "https://go.example/v1",
			Accounts: []config.OpenCodeGoAccount{
				{ID: "acc_1", Alias: "main", APIKey: "sk-open-1"},
				{ID: "acc_2", Alias: "backup", APIKey: "sk-open-2"},
			},
		},
	}, configFilePath: writeTestConfigFile(t)}
	router := openCodeGoTestRouter(h)

	rec := performOpenCodeGoRouteJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_1/sync-provider", nil, router)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	rec = performOpenCodeGoRouteJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_1/sync-provider", nil, router)
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := len(h.cfg.OpenAICompatibility); got != 1 {
		t.Fatalf("openai compatibility len = %d, want 1", got)
	}
	provider := h.cfg.OpenAICompatibility[0]
	if provider.Name != "opencode-go" || provider.BaseURL != "https://go.example/v1" {
		t.Fatalf("provider mismatch: %#v", provider)
	}
	if got := len(provider.APIKeyEntries); got != 1 {
		t.Fatalf("api key entries len after repeat = %d, want 1", got)
	}
	if provider.APIKeyEntries[0].APIKey != "sk-open-1" {
		t.Fatalf("api key entry = %q", provider.APIKeyEntries[0].APIKey)
	}
	if !h.cfg.OpenCodeGo.Accounts[0].APIKeySynced || h.cfg.OpenCodeGo.Accounts[0].ProviderSyncedAt == "" || h.cfg.OpenCodeGo.Accounts[0].ProviderSyncError != "" {
		t.Fatalf("account sync state not updated: %#v", h.cfg.OpenCodeGo.Accounts[0])
	}

	rec = performOpenCodeGoRouteJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_2/sync-provider", nil, router)
	if rec.Code != http.StatusOK {
		t.Fatalf("second status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := len(h.cfg.OpenAICompatibility[0].APIKeyEntries); got != 2 {
		t.Fatalf("api key entries len = %d, want 2", got)
	}
}

func TestOpenCodeGoSyncProviderDoesNotRetargetExistingProviderWithDifferentBaseURL(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			ProviderName: "opencode-go",
			BaseURL:      "https://go.example/v1",
			Accounts: []config.OpenCodeGoAccount{
				{ID: "acc_1", APIKey: "sk-open-1"},
			},
		},
		OpenAICompatibility: []config.OpenAICompatibility{{
			Name:    "opencode-go",
			BaseURL: "https://manual.example/v1",
			Models:  []config.OpenAICompatibilityModel{{Name: "manual-upstream", Alias: "manual-client"}},
			APIKeyEntries: []config.OpenAICompatibilityAPIKey{
				{APIKey: "sk-manual", ProxyURL: "http://proxy.example"},
			},
		}},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performOpenCodeGoRouteJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_1/sync-provider", nil, openCodeGoTestRouter(h))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := len(h.cfg.OpenAICompatibility); got != 2 {
		t.Fatalf("openai compatibility len = %d, want 2", got)
	}
	if got := h.cfg.OpenAICompatibility[0].BaseURL; got != "https://manual.example/v1" {
		t.Fatalf("manual provider base-url = %q, want unchanged", got)
	}
	if got := h.cfg.OpenAICompatibility[0].APIKeyEntries[0].APIKey; got != "sk-manual" {
		t.Fatalf("manual provider key = %q, want sk-manual", got)
	}
	if got := h.cfg.OpenAICompatibility[1].BaseURL; got != "https://go.example/v1" {
		t.Fatalf("opencode provider base-url = %q, want https://go.example/v1", got)
	}
	if got := h.cfg.OpenAICompatibility[1].APIKeyEntries[0].APIKey; got != "sk-open-1" {
		t.Fatalf("opencode provider key = %q, want sk-open-1", got)
	}
	if !h.cfg.OpenCodeGo.Accounts[0].ProviderKeyManaged {
		t.Fatalf("provider-key-managed = false, want true for newly appended provider key")
	}
}

func TestOpenCodeGoDeleteDoesNotRemovePreexistingProviderKey(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			ProviderName: "opencode-go",
			BaseURL:      "https://go.example/v1",
			Accounts: []config.OpenCodeGoAccount{
				{ID: "acc_1", APIKey: "sk-existing"},
			},
		},
		OpenAICompatibility: []config.OpenAICompatibility{{
			Name: "opencode-go", BaseURL: "https://go.example/v1",
			APIKeyEntries: []config.OpenAICompatibilityAPIKey{{APIKey: "sk-existing"}},
		}},
	}, configFilePath: writeTestConfigFile(t)}
	router := openCodeGoTestRouter(h)

	rec := performOpenCodeGoRouteJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_1/sync-provider", nil, router)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if h.cfg.OpenCodeGo.Accounts[0].ProviderKeyManaged {
		t.Fatalf("provider-key-managed = true, want false for preexisting provider key")
	}

	rec = performOpenCodeGoRouteJSON(http.MethodDelete, "/v0/management/opencode-go/accounts/acc_1?remove-provider-key=true", nil, router)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := len(h.cfg.OpenAICompatibility[0].APIKeyEntries); got != 1 {
		t.Fatalf("provider key entries len = %d, want manual key preserved", got)
	}
	if got := h.cfg.OpenAICompatibility[0].APIKeyEntries[0].APIKey; got != "sk-existing" {
		t.Fatalf("remaining provider key = %q, want sk-existing", got)
	}
}

func TestOpenCodeGoDeleteOptionallyRemovesProviderKey(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			ProviderName: "opencode-go",
			BaseURL:      "https://go.example/v1",
			Accounts: []config.OpenCodeGoAccount{
				{ID: "acc_1", APIKey: "sk-open-1", BaseURL: "https://go.example/v1", ProviderKeyManaged: true},
				{ID: "acc_2", APIKey: "sk-open-2"},
			},
		},
		OpenAICompatibility: []config.OpenAICompatibility{{
			Name: "opencode-go", BaseURL: "https://go.example/v1",
			APIKeyEntries: []config.OpenAICompatibilityAPIKey{{APIKey: "sk-open-1"}, {APIKey: "sk-open-2"}},
		}},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performOpenCodeGoRouteJSON(http.MethodDelete, "/v0/management/opencode-go/accounts/acc_1?remove-provider-key=true", nil, openCodeGoTestRouter(h))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := len(h.cfg.OpenCodeGo.Accounts); got != 1 {
		t.Fatalf("accounts len = %d, want 1", got)
	}
	if got := len(h.cfg.OpenAICompatibility[0].APIKeyEntries); got != 1 {
		t.Fatalf("provider key entries len = %d, want 1", got)
	}
	if got := h.cfg.OpenAICompatibility[0].APIKeyEntries[0].APIKey; got != "sk-open-2" {
		t.Fatalf("remaining provider key = %q", got)
	}
}

func TestOpenCodeGoSwitchCookieReturnsCookieOnlyForExistingAccountWithCookie(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			Accounts: []config.OpenCodeGoAccount{
				{ID: "acc_1", Cookie: "session=secret"},
				{ID: "acc_2"},
			},
		},
	}, configFilePath: writeTestConfigFile(t)}
	router := openCodeGoTestRouter(h)

	rec := performOpenCodeGoRouteJSON(http.MethodGet, "/v0/management/opencode-go/accounts/acc_1/switch-cookie", nil, router)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Cookie string `json:"cookie"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode cookie response: %v; body=%s", err, rec.Body.String())
	}
	if body.Cookie != "session=secret" {
		t.Fatalf("cookie = %q, want session=secret", body.Cookie)
	}

	missingCookie := performOpenCodeGoRouteJSON(http.MethodGet, "/v0/management/opencode-go/accounts/acc_2/switch-cookie", nil, router)
	if missingCookie.Code != http.StatusBadRequest {
		t.Fatalf("missing cookie status = %d, want 400; body=%s", missingCookie.Code, missingCookie.Body.String())
	}
	missingAccount := performOpenCodeGoRouteJSON(http.MethodGet, "/v0/management/opencode-go/accounts/missing/switch-cookie", nil, router)
	if missingAccount.Code != http.StatusNotFound {
		t.Fatalf("missing account status = %d, want 404; body=%s", missingAccount.Code, missingAccount.Body.String())
	}
}

func TestOpenCodeGoUserscriptConfigDoesNotExposeSecrets(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{}, configFilePath: writeTestConfigFile(t)}

	rec := performOpenCodeGoRouteJSON(http.MethodGet, "/v0/management/opencode-go/userscript-config", nil, openCodeGoTestRouter(h))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"secret", "token", "api-key", "management-key"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("userscript config leaked forbidden term %q: %s", forbidden, body)
		}
	}
}
