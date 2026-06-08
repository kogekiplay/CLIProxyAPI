package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestGetAPIKeyAccess_RedactsKeyLabelsAndReturnsRules(t *testing.T) {
	t.Parallel()

	const rawKey = "sk-secret-123456"
	h := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{rawKey},
				APIKeyAccess: map[string]config.APIKeyAccessRule{
					rawKey: {Providers: []string{"gemini"}},
				},
			},
		},
		configFilePath: writeTestConfigFile(t),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/api-key-access", nil)

	h.GetAPIKeyAccess(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"label":"`+rawKey+`"`) {
		t.Fatalf("response leaks full key as label: %s", rec.Body.String())
	}

	var body struct {
		APIKeyAccess map[string]config.APIKeyAccessRule `json:"api-key-access"`
		APIKeys      []struct {
			Key     string `json:"key"`
			Label   string `json:"label"`
			HasRule bool   `json:"has-rule"`
		} `json:"api-keys"`
	}
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("decode response: %v; body=%s", errDecode, rec.Body.String())
	}

	rule, ok := body.APIKeyAccess[rawKey]
	if !ok {
		t.Fatalf("missing api-key-access rule for %q in %#v", rawKey, body.APIKeyAccess)
	}
	if got, want := rule.Providers, []string{"gemini"}; !slices.Equal(got, want) {
		t.Fatalf("providers = %#v, want %#v", got, want)
	}
	if len(body.APIKeys) != 1 {
		t.Fatalf("api-keys len = %d, want 1", len(body.APIKeys))
	}
	if body.APIKeys[0].Key != rawKey {
		t.Fatalf("api key = %q, want %q", body.APIKeys[0].Key, rawKey)
	}
	if body.APIKeys[0].Label == "" || body.APIKeys[0].Label == rawKey {
		t.Fatalf("label = %q, want non-empty redacted label", body.APIKeys[0].Label)
	}
	if !body.APIKeys[0].HasRule {
		t.Fatalf("has-rule = false, want true")
	}
}

func TestPutPatchDeleteAPIKeyAccess_NormalizesRules(t *testing.T) {
	t.Parallel()

	h := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"key-1", "key-2"},
			},
		},
		configFilePath: writeTestConfigFile(t),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/v0/management/api-key-access", bytes.NewBufferString(`{
		"api-key-access": {
			" key-1 ": {
				"providers": [" Gemini ", "GEMINI", ""],
				"auth-files": [" auth-a.json ", "auth-a.json", ""]
			}
		}
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PutAPIKeyAccess(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	rule := h.cfg.APIKeyAccess["key-1"]
	if got, want := rule.Providers, []string{"gemini"}; !slices.Equal(got, want) {
		t.Fatalf("PUT providers = %#v, want %#v", got, want)
	}
	if got, want := rule.AuthFiles, []string{"auth-a.json"}; !slices.Equal(got, want) {
		t.Fatalf("PUT auth-files = %#v, want %#v", got, want)
	}

	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/api-key-access", bytes.NewBufferString(`{
		"key": " key-2 ",
		"rule": {
			"access": " ALL ",
			"providers": ["claude"],
			"auth-files": ["claude-a.json"]
		}
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PatchAPIKeyAccess(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	rule = h.cfg.APIKeyAccess["key-2"]
	if rule.Access != config.APIKeyAccessAll || len(rule.Providers) != 0 || len(rule.AuthFiles) != 0 {
		t.Fatalf("PATCH access all rule = %#v, want only access=all", rule)
	}

	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/v0/management/api-key-access?key=key-1", nil)

	h.DeleteAPIKeyAccess(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if _, ok := h.cfg.APIKeyAccess["key-1"]; ok {
		t.Fatalf("key-1 rule still present: %#v", h.cfg.APIKeyAccess)
	}
	if _, ok := h.cfg.APIKeyAccess["key-2"]; !ok {
		t.Fatalf("key-2 rule was removed: %#v", h.cfg.APIKeyAccess)
	}
}

func TestPatchAPIKeyAccessUpdatesAuthManagerRuntimeConfig(t *testing.T) {
	t.Parallel()

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "auth-a",
		Provider: "gemini",
		FileName: "auth-a.json",
	}); err != nil {
		t.Fatalf("register auth-a: %v", err)
	}
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "auth-b",
		Provider: "gemini",
		FileName: "auth-b.json",
	}); err != nil {
		t.Fatalf("register auth-b: %v", err)
	}
	manager.SetConfig(&config.Config{})

	h := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"key-1"},
			},
		},
		configFilePath: writeTestConfigFile(t),
		authManager:    manager,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/api-key-access", bytes.NewBufferString(`{
		"key": "key-1",
		"rule": {
			"auth-files": ["auth-a.json"]
		}
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PatchAPIKeyAccess(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	checkRecorder := httptest.NewRecorder()
	checkCtx, _ := gin.CreateTestContext(checkRecorder)
	checkCtx.Set("userApiKey", "key-1")
	ids, restricted := manager.AllowedAuthIDsForContext(context.WithValue(context.Background(), "gin", checkCtx))
	if !restricted {
		t.Fatalf("runtime access scope is unrestricted after management patch")
	}
	if got, want := strings.Join(ids, ","), "auth-a"; got != want {
		t.Fatalf("runtime scoped auth ids = %q, want %q", got, want)
	}
}

func TestGetAPIKeyAccessAuthTargets_DoNotLeakUpstreamAPIKeys(t *testing.T) {
	t.Parallel()

	const upstreamAPIKey = "upstream-secret-key-123456"
	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "gemini-auth",
		Provider: "gemini",
		FileName: "gemini-auth.json",
		Label:    "Gemini auth",
		Attributes: map[string]string{
			"api_key": upstreamAPIKey,
			"path":    "/tmp/gemini-auth.json",
		},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := &Handler{
		cfg:            &config.Config{},
		configFilePath: writeTestConfigFile(t),
		authManager:    manager,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/api-key-access", nil)

	h.GetAPIKeyAccess(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), upstreamAPIKey) {
		t.Fatalf("response leaks upstream API key: %s", rec.Body.String())
	}
}

func TestGetAPIKeyAccessAuthTargets_ReturnsProviderTargetMetadata(t *testing.T) {
	t.Parallel()

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "claude-a",
		Provider: "claude",
		FileName: "claude-a.json",
		Label:    "Claude A",
		Attributes: map[string]string{
			"base_url": "https://a.example.com",
		},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := &Handler{
		cfg:            &config.Config{},
		configFilePath: writeTestConfigFile(t),
		authManager:    manager,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/api-key-access", nil)

	h.GetAPIKeyAccess(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		AuthTargets []map[string]any `json:"auth-targets"`
	}
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("decode response: %v; body=%s", errDecode, rec.Body.String())
	}
	if len(body.AuthTargets) != 1 {
		t.Fatalf("auth-targets len = %d, want 1; body=%s", len(body.AuthTargets), rec.Body.String())
	}
	target := body.AuthTargets[0]
	if got, want := target["base-url"], "https://a.example.com"; got != want {
		t.Fatalf("base-url = %#v, want %#v; target=%#v", got, want, target)
	}
	if got, want := target["base_url"], "https://a.example.com"; got != want {
		t.Fatalf("base_url = %#v, want %#v; target=%#v", got, want, target)
	}
	providerTarget, ok := target["provider-target"].(map[string]any)
	if !ok {
		t.Fatalf("provider-target missing or wrong type: %#v", target["provider-target"])
	}
	if got, want := providerTarget["provider"], "claude"; got != want {
		t.Fatalf("provider-target.provider = %#v, want %#v", got, want)
	}
	if got, want := providerTarget["base-url"], "https://a.example.com"; got != want {
		t.Fatalf("provider-target.base-url = %#v, want %#v", got, want)
	}
	providerTargetSnake, ok := target["provider_target"].(map[string]any)
	if !ok {
		t.Fatalf("provider_target missing or wrong type: %#v", target["provider_target"])
	}
	if got, want := providerTargetSnake["base_url"], "https://a.example.com"; got != want {
		t.Fatalf("provider_target.base_url = %#v, want %#v", got, want)
	}
}

func TestGetAPIKeyAccessProviderTargetsComeFromConfiguredProviderEndpoints(t *testing.T) {
	t.Parallel()

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-oauth-default",
		Provider: "codex",
		FileName: "codex-oauth-default.json",
		Label:    "Codex OAuth default",
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := &Handler{
		cfg: &config.Config{
			CodexKey: []config.CodexKey{
				{APIKey: "codex-key-a", BaseURL: "https://aigw.c5y.moe/v1"},
				{APIKey: "codex-key-b", BaseURL: "https://muyuan.do/v1"},
			},
		},
		configFilePath: writeTestConfigFile(t),
		authManager:    manager,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/api-key-access", nil)

	h.GetAPIKeyAccess(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		ProviderTargets []struct {
			Provider string `json:"provider"`
			BaseURL  string `json:"base-url"`
		} `json:"provider-targets"`
		AuthTargets []map[string]any `json:"auth-targets"`
	}
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("decode response: %v; body=%s", errDecode, rec.Body.String())
	}

	if len(body.AuthTargets) != 1 {
		t.Fatalf("auth-targets len = %d, want 1; body=%s", len(body.AuthTargets), rec.Body.String())
	}
	if got, want := len(body.ProviderTargets), 2; got != want {
		t.Fatalf("provider-targets len = %d, want %d; body=%s", got, want, rec.Body.String())
	}
	got := make([]string, 0, len(body.ProviderTargets))
	for _, target := range body.ProviderTargets {
		got = append(got, target.Provider+" "+target.BaseURL)
		if target.BaseURL == "" {
			t.Fatalf("provider-targets includes empty/default base-url target: %#v; body=%s", target, rec.Body.String())
		}
	}
	want := []string{
		"codex https://aigw.c5y.moe/v1",
		"codex https://muyuan.do/v1",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("provider-targets = %#v, want %#v; body=%s", got, want, rec.Body.String())
	}
}

func TestGetAPIKeyAccessAuthTargetsExcludeAPIKeyCredentials(t *testing.T) {
	t.Parallel()

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "claude-apikey-1",
		Provider: "claude",
		FileName: "",
		Label:    "Claude API key",
		Attributes: map[string]string{
			"api_key":  "upstream-secret-key-123456",
			"base_url": "https://aigw.c5y.moe",
		},
	}); err != nil {
		t.Fatalf("register api key auth: %v", err)
	}
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-user",
		Provider: "codex",
		FileName: "codex-user.json",
		Label:    "Codex User",
		Metadata: map[string]any{
			"email": "user@example.com",
		},
	}); err != nil {
		t.Fatalf("register oauth auth: %v", err)
	}

	h := &Handler{
		cfg:            &config.Config{},
		configFilePath: writeTestConfigFile(t),
		authManager:    manager,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/api-key-access", nil)

	h.GetAPIKeyAccess(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		AuthTargets []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			AccountType string `json:"account_type"`
		} `json:"auth-targets"`
	}
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("decode response: %v; body=%s", errDecode, rec.Body.String())
	}
	if got, want := len(body.AuthTargets), 1; got != want {
		t.Fatalf("auth-targets len = %d, want %d; targets=%#v; body=%s", got, want, body.AuthTargets, rec.Body.String())
	}
	if got, want := body.AuthTargets[0].ID, "codex-user"; got != want {
		t.Fatalf("auth-target id = %q, want %q; body=%s", got, want, rec.Body.String())
	}
	if body.AuthTargets[0].AccountType == "api_key" || strings.Contains(rec.Body.String(), "claude-apikey") {
		t.Fatalf("auth-targets includes API key credential: %#v; body=%s", body.AuthTargets, rec.Body.String())
	}
}
