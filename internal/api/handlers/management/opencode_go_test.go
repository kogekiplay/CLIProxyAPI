package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	group.POST("/accounts/:id/refresh-usage", h.RefreshOpenCodeGoUsage)
	group.POST("/accounts/:id/sync-provider", h.SyncOpenCodeGoProvider)
	group.DELETE("/accounts/:id", h.DeleteOpenCodeGoAccount)
	group.GET("/accounts/:id/switch-cookie", h.GetOpenCodeGoSwitchCookie)
	group.GET("/userscript-config", h.GetOpenCodeGoUserscriptConfig)
	return router
}

func newOpenCodeGoModelsTestServer(t *testing.T, modelIDs []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer sk-") {
			t.Fatalf("authorization = %q, want bearer api key", got)
		}
		var items []map[string]string
		for _, id := range modelIDs {
			items = append(items, map[string]string{"id": id})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
	}))
}

func TestOpenCodeGoRefreshUsageFetchesFromOpenCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=secret") {
			t.Fatalf("auth cookie = %q, want session cookie", got)
		}
		http.Redirect(w, r, "/workspace/wrk_test123", http.StatusFound)
	})
	mux.HandleFunc("/workspace/wrk_test123", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<script src="/_build/assets/app.js"></script>`))
	})
	mux.HandleFunc("/workspace/wrk_test123/go", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<script src="/_build/assets/app.js"></script>`))
	})
	mux.HandleFunc("/_build/assets/app.js", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`"_build/assets/go.js";const a=createServerReference("1111111111111111111111111111111111111111111111111111111111111111");query(a,"other.fn")`))
	})
	mux.HandleFunc("/_build/assets/go.js", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`const b=createServerReference("2222222222222222222222222222222222222222222222222222222222222222");query(b,"lite.subscription.get")`))
	})
	mux.HandleFunc("/_server", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("id"); got != "2222222222222222222222222222222222222222222222222222222222222222" {
			t.Fatalf("server id = %q", got)
		}
		_, _ = w.Write([]byte(`rollingUsage:$R[1],weeklyUsage:$R[2],monthlyUsage:{usagePercent:20,resetInSec:30,status:"ok"};$R[1]={usagePercent:0,resetInSec:60,status:"ok"};$R[2]={usagePercent:40,resetInSec:120,status:"ok"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	oldSiteURL := openCodeGoSiteURL
	openCodeGoSiteURL = server.URL
	defer func() { openCodeGoSiteURL = oldSiteURL }()

	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			Accounts: []config.OpenCodeGoAccount{{ID: "acc_1", Cookie: "session=secret"}},
		},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performOpenCodeGoRouteJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_1/refresh-usage", nil, openCodeGoTestRouter(h))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	account := h.cfg.OpenCodeGo.Accounts[0]
	if account.WorkspaceID != "wrk_test123" {
		t.Fatalf("workspace id = %q, want wrk_test123", account.WorkspaceID)
	}
	if account.Usage.Rolling.Used != 0 || account.Usage.Weekly.Used != 0 || account.Usage.Monthly.Used != 0 {
		t.Fatalf("usage persisted to config: %#v", account.Usage)
	}
	var body struct {
		Account struct {
			Usage config.OpenCodeGoUsageSnapshot `json:"usage"`
		} `json:"account"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"used":0`) {
		t.Fatalf("response omitted zero usage: %s", rec.Body.String())
	}
	if body.Account.Usage.Rolling.Used != 0 || body.Account.Usage.Rolling.Limit != 100 {
		t.Fatalf("response rolling usage = %#v", body.Account.Usage.Rolling)
	}
	if body.Account.Usage.Weekly.Used != 40 || body.Account.Usage.Monthly.Used != 20 {
		t.Fatalf("response usage snapshot = %#v", body.Account.Usage)
	}
	if body.Account.Usage.Rolling.ResetAt == "" {
		t.Fatalf("response rolling reset-at empty")
	}
}

func TestOpenCodeGoListAccountsOmitsPersistedUsage(t *testing.T) {
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			Accounts: []config.OpenCodeGoAccount{{
				ID:     "acc_1",
				Cookie: "session=secret",
				Usage: config.OpenCodeGoUsageSnapshot{
					Rolling: config.OpenCodeGoUsageWindow{Used: 99, Limit: 100, ResetAt: "2026-06-24T12:00:00Z"},
				},
			}},
		},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performOpenCodeGoRouteJSON(http.MethodGet, "/v0/management/opencode-go/accounts", nil, openCodeGoTestRouter(h))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"usage"`) || strings.Contains(rec.Body.String(), "99") {
		t.Fatalf("list response leaked persisted usage: %s", rec.Body.String())
	}
}

func TestOpenCodeGoRefreshUsageRejectsLocaleOnlyCookie(t *testing.T) {
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			Accounts: []config.OpenCodeGoAccount{{
				ID:          "acc_1",
				WorkspaceID: "wrk_test123",
				Cookie:      "oc_locale=zh",
			}},
		},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performOpenCodeGoRouteJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_1/refresh-usage", nil, openCodeGoTestRouter(h))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cookie") || !strings.Contains(rec.Body.String(), "authentication") {
		t.Fatalf("body = %s, want authentication cookie error", rec.Body.String())
	}
}

func TestOpenCodeGoFetchUsageFetchesWorkspacePagesConcurrently(t *testing.T) {
	clearOpenCodeGoLiteSubscriptionHashCache("")

	mux := http.NewServeMux()
	mux.HandleFunc("/workspace/wrk_page123", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		_, _ = w.Write([]byte(`<script src="/_build/assets/app.js"></script>`))
	})
	mux.HandleFunc("/workspace/wrk_page123/go", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		_, _ = w.Write([]byte(`<script src="/_build/assets/app.js"></script>`))
	})
	mux.HandleFunc("/_build/assets/app.js", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`const b=createServerReference("2222222222222222222222222222222222222222222222222222222222222222");query(b,"lite.subscription.get")`))
	})
	mux.HandleFunc("/_server", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`rollingUsage:{usagePercent:40,resetInSec:60,status:"ok"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	oldSiteURL := openCodeGoSiteURL
	openCodeGoSiteURL = server.URL
	defer func() { openCodeGoSiteURL = oldSiteURL }()

	start := time.Now()
	workspaceID, usage, err := fetchOpenCodeGoUsage(context.Background(), "session=secret", "wrk_page123")
	if err != nil {
		t.Fatalf("fetch usage: %v", err)
	}
	if workspaceID != "wrk_page123" || usage.Rolling.Used != 40 {
		t.Fatalf("usage = workspace %q snapshot %#v", workspaceID, usage)
	}
	if elapsed := time.Since(start); elapsed >= 400*time.Millisecond {
		t.Fatalf("usage fetch took %s, want concurrent page fetch under 400ms", elapsed)
	}
}

func TestOpenCodeGoJSDependencyURLsResolveBuildAssetPaths(t *testing.T) {
	urls := extractOpenCodeGoJSDependencyURLs(`"_build/assets/index-DtPYjwk4.js";"./query-B0ORTVO5.js";"/_build/assets/root.js"`, "https://opencode.ai/_build/assets/entry-client.js")
	want := []string{
		"https://opencode.ai/_build/assets/index-DtPYjwk4.js",
		"https://opencode.ai/_build/assets/query-B0ORTVO5.js",
		"https://opencode.ai/_build/assets/root.js",
	}
	if len(urls) != len(want) {
		t.Fatalf("dependency len = %d, want %d; urls=%#v", len(urls), len(want), urls)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Fatalf("dependency[%d] = %q, want %q; urls=%#v", i, urls[i], want[i], urls)
		}
	}
}

func TestFindOpenCodeGoLiteSubscriptionHashSearchesScriptsConcurrently(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/slow.js", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_, _ = w.Write([]byte(`const a=createServerReference("1111111111111111111111111111111111111111111111111111111111111111");query(a,"other.fn")`))
	})
	mux.HandleFunc("/fast.js", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`const b=createServerReference("2222222222222222222222222222222222222222222222222222222222222222");query(b,"lite.subscription.get")`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	start := time.Now()
	hash, err := findOpenCodeGoLiteSubscriptionHash(context.Background(), []string{
		server.URL + "/slow.js",
		server.URL + "/fast.js",
	}, "session=secret", server.URL+"/go")
	if err != nil {
		t.Fatalf("find hash: %v", err)
	}
	if hash != "2222222222222222222222222222222222222222222222222222222222222222" {
		t.Fatalf("hash = %q", hash)
	}
	if elapsed := time.Since(start); elapsed >= 300*time.Millisecond {
		t.Fatalf("hash lookup took %s, want concurrent lookup under 300ms", elapsed)
	}
}

func TestOpenCodeGoSyncCreatesAccountAndRedactsList(t *testing.T) {
	t.Parallel()
	modelsServer := newOpenCodeGoModelsTestServer(t, []string{"opencode-go"})
	defer modelsServer.Close()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{BaseURL: modelsServer.URL},
	}, configFilePath: writeTestConfigFile(t)}

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
	if got := len(h.cfg.OpenAICompatibility); got != 1 {
		t.Fatalf("openai compatibility len = %d, want 1 after auto provider sync", got)
	}
	if got := h.cfg.OpenAICompatibility[0].APIKeyEntries[0].Source; got != "opencode-go:"+h.cfg.OpenCodeGo.Accounts[0].ID {
		t.Fatalf("provider key source = %q, want opencode-go account source", got)
	}
	if !h.cfg.OpenCodeGo.Accounts[0].APIKeySynced || !h.cfg.OpenCodeGo.Accounts[0].ProviderKeyManaged {
		t.Fatalf("account provider sync state = %#v, want synced managed", h.cfg.OpenCodeGo.Accounts[0])
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
	modelsServer := newOpenCodeGoModelsTestServer(t, []string{"opencode-go"})
	defer modelsServer.Close()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{BaseURL: modelsServer.URL},
	}, configFilePath: writeTestConfigFile(t)}

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
		"id": "acc_route",
	}, router)
	if sync.Code != http.StatusOK {
		t.Fatalf("sync status = %d, want 200; body=%s", sync.Code, sync.Body.String())
	}
	if got := len(h.cfg.OpenCodeGo.Accounts); got != 1 {
		t.Fatalf("accounts len = %d, want 1", got)
	}
}

func TestOpenCodeGoSyncProviderUsesDefaultBaseURL(t *testing.T) {
	t.Parallel()
	modelsServer := newOpenCodeGoModelsTestServer(t, []string{"opencode-go", "opencode-small"})
	defer modelsServer.Close()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			BaseURL:  modelsServer.URL,
			Accounts: []config.OpenCodeGoAccount{{ID: "acc_1", APIKey: "sk-open"}},
		},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performOpenCodeGoRouteJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_1/sync-provider", nil, openCodeGoTestRouter(h))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := h.cfg.OpenCodeGo.Accounts[0].BaseURL; got != modelsServer.URL {
		t.Fatalf("account base-url = %q, want %q", got, modelsServer.URL)
	}
	if got := len(h.cfg.OpenAICompatibility); got != 1 {
		t.Fatalf("openai compatibility len = %d, want 1", got)
	}
	if got := h.cfg.OpenAICompatibility[0].BaseURL; got != modelsServer.URL {
		t.Fatalf("provider base-url = %q, want %q", got, modelsServer.URL)
	}
	if got := len(h.cfg.OpenAICompatibility[0].Models); got != 2 {
		t.Fatalf("provider models len = %d, want 2; models=%#v", got, h.cfg.OpenAICompatibility[0].Models)
	}
	if model := h.cfg.OpenAICompatibility[0].Models[0]; model.Name != "opencode-go" || model.Alias != "opencode-go" {
		t.Fatalf("first provider model = %#v", model)
	}
}

func TestOpenCodeGoSyncProviderUpsertsOpenAICompatibilityEntry(t *testing.T) {
	t.Parallel()
	modelsServer := newOpenCodeGoModelsTestServer(t, []string{"opencode-go", "opencode-reasoning"})
	defer modelsServer.Close()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			ProviderName: "opencode-go",
			BaseURL:      modelsServer.URL,
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
	if provider.Name != "opencode-go" || provider.BaseURL != modelsServer.URL {
		t.Fatalf("provider mismatch: %#v", provider)
	}
	if got := len(provider.Models); got != 2 {
		t.Fatalf("provider models len = %d, want 2; models=%#v", got, provider.Models)
	}
	if got := len(provider.APIKeyEntries); got != 1 {
		t.Fatalf("api key entries len after repeat = %d, want 1", got)
	}
	if provider.APIKeyEntries[0].APIKey != "sk-open-1" {
		t.Fatalf("api key entry = %q", provider.APIKeyEntries[0].APIKey)
	}
	if provider.APIKeyEntries[0].Source != "opencode-go:acc_1" {
		t.Fatalf("api key source = %q, want opencode-go:acc_1", provider.APIKeyEntries[0].Source)
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

func TestOpenCodeGoSyncProviderRejectsEmptyModelList(t *testing.T) {
	t.Parallel()
	modelsServer := newOpenCodeGoModelsTestServer(t, nil)
	defer modelsServer.Close()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			BaseURL:  modelsServer.URL,
			Accounts: []config.OpenCodeGoAccount{{ID: "acc_1", APIKey: "sk-open-1"}},
		},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performOpenCodeGoRouteJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_1/sync-provider", nil, openCodeGoTestRouter(h))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model list is empty") {
		t.Fatalf("missing empty model list error: %s", rec.Body.String())
	}
	if got := len(h.cfg.OpenAICompatibility); got != 0 {
		t.Fatalf("openai compatibility len = %d, want 0", got)
	}
}

func TestOpenCodeGoSyncProviderRejectsExistingProviderWithDifferentBaseURL(t *testing.T) {
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
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "different base-url") {
		t.Fatalf("missing base-url conflict error: %s", rec.Body.String())
	}
	if got := len(h.cfg.OpenAICompatibility); got != 1 {
		t.Fatalf("openai compatibility len = %d, want 1", got)
	}
	if got := h.cfg.OpenAICompatibility[0].BaseURL; got != "https://manual.example/v1" {
		t.Fatalf("manual provider base-url = %q, want unchanged", got)
	}
	if got := h.cfg.OpenAICompatibility[0].APIKeyEntries[0].APIKey; got != "sk-manual" {
		t.Fatalf("manual provider key = %q, want sk-manual", got)
	}
	account := h.cfg.OpenCodeGo.Accounts[0]
	if account.ProviderKeyManaged {
		t.Fatalf("provider-key-managed = true, want false after provider-name conflict")
	}
	if !strings.Contains(account.ProviderSyncError, "different base-url") {
		t.Fatalf("provider sync error = %q, want base-url conflict", account.ProviderSyncError)
	}
}

func TestOpenCodeGoDeleteDoesNotRemovePreexistingProviderKey(t *testing.T) {
	t.Parallel()
	modelsServer := newOpenCodeGoModelsTestServer(t, []string{"opencode-go"})
	defer modelsServer.Close()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			ProviderName: "opencode-go",
			BaseURL:      modelsServer.URL,
			Accounts: []config.OpenCodeGoAccount{
				{ID: "acc_1", APIKey: "sk-existing"},
			},
		},
		OpenAICompatibility: []config.OpenAICompatibility{{
			Name: "opencode-go", BaseURL: modelsServer.URL,
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

func TestOpenCodeGoDeleteDoesNotRemoveProviderKeyWithStaleManagedFlag(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			ProviderName: "opencode-go",
			BaseURL:      "https://go.example/v1",
			Accounts: []config.OpenCodeGoAccount{
				{ID: "acc_1", APIKey: "sk-existing", BaseURL: "https://go.example/v1", ProviderKeyManaged: true},
			},
		},
		OpenAICompatibility: []config.OpenAICompatibility{{
			Name: "opencode-go", BaseURL: "https://go.example/v1",
			APIKeyEntries: []config.OpenAICompatibilityAPIKey{{APIKey: "sk-existing"}},
		}},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performOpenCodeGoRouteJSON(http.MethodDelete, "/v0/management/opencode-go/accounts/acc_1?remove-provider-key=true", nil, openCodeGoTestRouter(h))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := len(h.cfg.OpenAICompatibility[0].APIKeyEntries); got != 1 {
		t.Fatalf("provider key entries len = %d, want stale manual key preserved", got)
	}
	if got := h.cfg.OpenAICompatibility[0].APIKeyEntries[0].APIKey; got != "sk-existing" {
		t.Fatalf("remaining provider key = %q, want sk-existing", got)
	}
}

func TestOpenCodeGoDeleteRemovesManagedProviderKeyByDefault(t *testing.T) {
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
			APIKeyEntries: []config.OpenAICompatibilityAPIKey{{APIKey: "sk-open-1", Source: "opencode-go:acc_1"}, {APIKey: "sk-open-2", Source: "opencode-go:acc_2"}},
		}},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performOpenCodeGoRouteJSON(http.MethodDelete, "/v0/management/opencode-go/accounts/acc_1", nil, openCodeGoTestRouter(h))
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
