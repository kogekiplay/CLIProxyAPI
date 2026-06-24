# OpenCode Go Account Helper Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a safe MVP for importing user-controlled OpenCode Go accounts into CPA, syncing their usage/API keys through a Tampermonkey script, and managing the imported accounts from the CPA management UI.

**Architecture:** CPA backend stores OpenCode Go account metadata under a new `opencode-go` config section and writes selected Go API keys into the existing `openai-compatibility` provider list. The management UI calls new management endpoints and shows account/usage/provider sync state. A separate userscript project runs only on `https://opencode.ai/*`, extracts account data in the logged-in page context, syncs it to CPA with the user's management key, and switches accounts only after explicit confirmation.

**Tech Stack:** Go/Gin/YAML config in `CLIProxyAPI`; React 19/Vite/TypeScript/Bun in `Cli-Proxy-API-Management-Center`; plain Tampermonkey userscript JavaScript in `opencode-go-account-helper-userscript`.

---

## Important Boundaries

- Do not implement automatic registration.
- Do not implement protocol-level automatic reward claiming.
- Do not add a CPA "open workspace as this account" button.
- Cookie upload is opt-in in the userscript.
- Account switching is done by the userscript only after a browser confirmation dialog.
- Do not log full API keys, cookies, management keys, or bearer tokens.
- Existing API key access authorization must keep working unchanged.
- The backend follows the existing CPA storage model: sensitive provider API keys already live in the config file. This MVP redacts secrets from API responses and logs, but does not claim strong at-rest encryption unless a proper external key source is added later.

## File Structure

### Backend: `/Users/kogeki/dev/CLIProxyAPI`

- Modify `internal/config/config.go`
  - Add `OpenCodeGo OpenCodeGoConfig` to `Config`.
  - Add config structs for account metadata, usage snapshots, and provider sync state.
  - Add helper methods that normalize provider name/base URL and account IDs.
- Create `internal/config/opencode_go_test.go`
  - Test YAML load/save shape and deep clone behavior for the new config section.
- Create `internal/api/handlers/management/opencode_go.go`
  - Add list/sync/delete/provider-sync/userscript-config/switch-cookie handlers.
  - Add secret redaction helpers.
  - Add provider update helpers that upsert into `Config.OpenAICompatibility`.
- Create `internal/api/handlers/management/opencode_go_test.go`
  - Test account sync, redaction, usage preservation, provider sync, delete behavior, and switch-cookie access.
- Modify `internal/api/server.go`
  - Register routes under `/v0/management/opencode-go`.

### Management UI: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center`

- Create `src/types/opencodeGo.ts`
  - TypeScript types for accounts, usage snapshots, sync payloads, and provider sync responses.
- Create `src/services/api/opencodeGo.ts`
  - API wrapper for the new management endpoints.
- Create `src/pages/OpenCodeGoPage.tsx`
  - Account list, usage cards, provider settings, userscript config copy, sync/delete actions.
- Create `src/pages/OpenCodeGoPage.module.scss`
  - Page layout and compact responsive table/card styles.
- Modify `src/router/MainRoutes.tsx`
  - Add `/opencode-go` route.
- Modify `src/components/layout/MainLayout.tsx`
  - Add sidebar entry in the gateway group.
- Modify `src/i18n/locales/zh-CN.json`, `zh-TW.json`, `en.json`, `ru.json`
  - Add navigation labels and page copy.

### Userscript: `/Users/kogeki/dev/opencode-go-account-helper-userscript`

- Create `README.md`
  - Installation, configuration, security notes, and troubleshooting.
- Create `opencode-go-account-helper.user.js`
  - Tampermonkey script named `opencode go账号助手`.
  - `@match https://opencode.ai/*`.
  - Uses `GM_xmlhttpRequest`, `GM_getValue`, `GM_setValue`, `GM_registerMenuCommand`, and `GM_cookie` when available.

---

### Task 1: Backend Config Model

**Files:**
- Modify: `/Users/kogeki/dev/CLIProxyAPI/internal/config/config.go`
- Test: `/Users/kogeki/dev/CLIProxyAPI/internal/config/opencode_go_test.go`

- [ ] **Step 1: Write failing config tests**

Create `/Users/kogeki/dev/CLIProxyAPI/internal/config/opencode_go_test.go`:

```go
package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenCodeGoConfigYAMLRoundTrip(t *testing.T) {
	const raw = `
opencode-go:
  provider-name: opencode-go
  base-url: https://api.opencode.example/v1
  accounts:
    - id: acc_1
      alias: main
      email: user@example.com
      username: user
      workspace-id: ws_123
      api-key: sk-test1234567890
      cookie: session=value
      api-key-synced: true
      provider-name: opencode-go
      provider-sync-error: ""
      usage:
        rolling:
          used: 12
          limit: 100
          reset-at: "2026-06-25T00:00:00Z"
        weekly:
          used: 34
          limit: 200
        monthly:
          used: 56
          limit: 500
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal opencode-go config: %v", err)
	}
	if cfg.OpenCodeGo.ProviderName != "opencode-go" {
		t.Fatalf("provider-name = %q", cfg.OpenCodeGo.ProviderName)
	}
	if got := len(cfg.OpenCodeGo.Accounts); got != 1 {
		t.Fatalf("accounts len = %d, want 1", got)
	}
	account := cfg.OpenCodeGo.Accounts[0]
	if account.WorkspaceID != "ws_123" || account.APIKey != "sk-test1234567890" || account.Cookie != "session=value" {
		t.Fatalf("unexpected account: %#v", account)
	}

	out, err := yaml.Marshal(&cfg)
	if err != nil {
		t.Fatalf("marshal opencode-go config: %v", err)
	}
	text := string(out)
	for _, want := range []string{
		"opencode-go:",
		"provider-name: opencode-go",
		"workspace-id: ws_123",
		"api-key: sk-test1234567890",
		"rolling:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("marshaled yaml missing %q:\n%s", want, text)
		}
	}
}

func TestOpenCodeGoConfigCloneDoesNotShareAccounts(t *testing.T) {
	cfg := &Config{
		OpenCodeGo: OpenCodeGoConfig{
			ProviderName: "opencode-go",
			BaseURL:      "https://api.opencode.example/v1",
			Accounts: []OpenCodeGoAccount{{
				ID:          "acc_1",
				WorkspaceID: "ws_123",
				APIKey:      "sk-test1234567890",
				Usage: OpenCodeGoUsageSnapshot{
					Rolling: OpenCodeGoUsageWindow{Used: 1, Limit: 10},
				},
			}},
		},
	}
	clone := cfg.CloneForRuntime()
	cfg.OpenCodeGo.Accounts[0].APIKey = "mutated"
	cfg.OpenCodeGo.Accounts[0].Usage.Rolling.Used = 9

	if clone.OpenCodeGo.Accounts[0].APIKey != "sk-test1234567890" {
		t.Fatalf("clone api key = %q", clone.OpenCodeGo.Accounts[0].APIKey)
	}
	if clone.OpenCodeGo.Accounts[0].Usage.Rolling.Used != 1 {
		t.Fatalf("clone rolling used = %v", clone.OpenCodeGo.Accounts[0].Usage.Rolling.Used)
	}
}
```

- [ ] **Step 2: Run the tests and verify RED**

Run:

```bash
go test ./internal/config -run 'TestOpenCodeGo' -count=1
```

Expected: fails because `OpenCodeGoConfig`, `OpenCodeGoAccount`, `OpenCodeGoUsageSnapshot`, and `OpenCodeGoUsageWindow` are not defined.

- [ ] **Step 3: Add config structs**

In `/Users/kogeki/dev/CLIProxyAPI/internal/config/config.go`, add this field to `Config` after `OpenAICompatibility`:

```go
	// OpenCodeGo stores accounts imported from the OpenCode Go browser helper.
	OpenCodeGo OpenCodeGoConfig `yaml:"opencode-go,omitempty" json:"opencode-go,omitempty"`
```

Add these structs near the OpenAI compatibility structs:

```go
// OpenCodeGoConfig stores account data synced by the OpenCode Go userscript.
type OpenCodeGoConfig struct {
	ProviderName string              `yaml:"provider-name,omitempty" json:"provider-name,omitempty"`
	BaseURL      string              `yaml:"base-url,omitempty" json:"base-url,omitempty"`
	Accounts     []OpenCodeGoAccount `yaml:"accounts,omitempty" json:"accounts,omitempty"`
}

type OpenCodeGoAccount struct {
	ID                string                  `yaml:"id" json:"id"`
	Alias             string                  `yaml:"alias,omitempty" json:"alias,omitempty"`
	Email             string                  `yaml:"email,omitempty" json:"email,omitempty"`
	Username          string                  `yaml:"username,omitempty" json:"username,omitempty"`
	WorkspaceID       string                  `yaml:"workspace-id,omitempty" json:"workspace-id,omitempty"`
	APIKey            string                  `yaml:"api-key,omitempty" json:"api-key,omitempty"`
	Cookie            string                  `yaml:"cookie,omitempty" json:"cookie,omitempty"`
	Usage             OpenCodeGoUsageSnapshot `yaml:"usage,omitempty" json:"usage,omitempty"`
	ProviderName      string                  `yaml:"provider-name,omitempty" json:"provider-name,omitempty"`
	BaseURL           string                  `yaml:"base-url,omitempty" json:"base-url,omitempty"`
	APIKeySynced      bool                    `yaml:"api-key-synced,omitempty" json:"api-key-synced,omitempty"`
	ProviderSyncedAt  string                  `yaml:"provider-synced-at,omitempty" json:"provider-synced-at,omitempty"`
	ProviderSyncError string                  `yaml:"provider-sync-error,omitempty" json:"provider-sync-error,omitempty"`
	CreatedAt         string                  `yaml:"created-at,omitempty" json:"created-at,omitempty"`
	UpdatedAt         string                  `yaml:"updated-at,omitempty" json:"updated-at,omitempty"`
	LastSyncedAt       string                  `yaml:"last-synced-at,omitempty" json:"last-synced-at,omitempty"`
}

type OpenCodeGoUsageSnapshot struct {
	Rolling OpenCodeGoUsageWindow `yaml:"rolling,omitempty" json:"rolling,omitempty"`
	Weekly  OpenCodeGoUsageWindow `yaml:"weekly,omitempty" json:"weekly,omitempty"`
	Monthly OpenCodeGoUsageWindow `yaml:"monthly,omitempty" json:"monthly,omitempty"`
}

type OpenCodeGoUsageWindow struct {
	Used    float64 `yaml:"used,omitempty" json:"used,omitempty"`
	Limit   float64 `yaml:"limit,omitempty" json:"limit,omitempty"`
	ResetAt string  `yaml:"reset-at,omitempty" json:"reset-at,omitempty"`
}
```

- [ ] **Step 4: Run config tests and verify GREEN**

Run:

```bash
go test ./internal/config -run 'TestOpenCodeGo|TestCloneForRuntime' -count=1
```

Expected: all selected tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/opencode_go_test.go
git commit -m "feat: add opencode go config model"
```

---

### Task 2: Backend Management Account Sync

**Files:**
- Create: `/Users/kogeki/dev/CLIProxyAPI/internal/api/handlers/management/opencode_go.go`
- Test: `/Users/kogeki/dev/CLIProxyAPI/internal/api/handlers/management/opencode_go_test.go`

- [ ] **Step 1: Write failing account sync tests**

Create `/Users/kogeki/dev/CLIProxyAPI/internal/api/handlers/management/opencode_go_test.go` with these tests first:

```go
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

func performJSON(method, target string, body any, handler func(*gin.Context)) *httptest.ResponseRecorder {
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

func TestOpenCodeGoSyncCreatesAccountAndRedactsList(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{}, configFilePath: writeTestConfigFile(t)}

	rec := performJSON(http.MethodPost, "/v0/management/opencode-go/sync", map[string]any{
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

	list := performJSON(http.MethodGet, "/v0/management/opencode-go/accounts", nil, h.ListOpenCodeGoAccounts)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", list.Code, list.Body.String())
	}
	body := list.Body.String()
	if strings.Contains(body, "sk-abcdefghijklmnopqrstuvwxyz") || strings.Contains(body, "session=secret") {
		t.Fatalf("list leaked secret: %s", body)
	}
	if !strings.Contains(body, "sk-a") || !strings.Contains(body, "has-cookie") {
		t.Fatalf("list missing redacted metadata: %s", body)
	}
}

func TestOpenCodeGoSyncUpdatesByWorkspaceAndPreservesOldUsageWhenOmitted(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{}, configFilePath: writeTestConfigFile(t)}

	performJSON(http.MethodPost, "/v0/management/opencode-go/sync", map[string]any{
		"alias":        "before",
		"workspace-id": "ws_123",
		"api-key":      "sk-before",
		"usage": map[string]any{
			"weekly": map[string]any{"used": 7, "limit": 20},
		},
	}, h.SyncOpenCodeGoAccount)
	performJSON(http.MethodPost, "/v0/management/opencode-go/sync", map[string]any{
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
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/api/handlers/management -run 'TestOpenCodeGoSync' -count=1
```

Expected: fails because `SyncOpenCodeGoAccount` and `ListOpenCodeGoAccounts` do not exist.

- [ ] **Step 3: Implement sync/list handlers**

Create `/Users/kogeki/dev/CLIProxyAPI/internal/api/handlers/management/opencode_go.go`:

```go
package management

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

const defaultOpenCodeGoProviderName = "opencode-go"

type openCodeGoSyncRequest struct {
	ID          string                         `json:"id"`
	Alias       string                         `json:"alias"`
	Email       string                         `json:"email"`
	Username    string                         `json:"username"`
	WorkspaceID string                         `json:"workspace-id"`
	APIKey      string                         `json:"api-key"`
	Cookie      string                         `json:"cookie"`
	Usage       *config.OpenCodeGoUsageSnapshot `json:"usage"`
}

type openCodeGoAccountResponse struct {
	ID                string                         `json:"id"`
	Alias             string                         `json:"alias,omitempty"`
	Email             string                         `json:"email,omitempty"`
	Username          string                         `json:"username,omitempty"`
	WorkspaceID       string                         `json:"workspace-id,omitempty"`
	APIKeyPreview     string                         `json:"api-key-preview,omitempty"`
	HasAPIKey         bool                           `json:"has-api-key"`
	HasCookie         bool                           `json:"has-cookie"`
	Usage             config.OpenCodeGoUsageSnapshot `json:"usage,omitempty"`
	ProviderName      string                         `json:"provider-name,omitempty"`
	BaseURL           string                         `json:"base-url,omitempty"`
	APIKeySynced      bool                           `json:"api-key-synced"`
	ProviderSyncedAt  string                         `json:"provider-synced-at,omitempty"`
	ProviderSyncError string                         `json:"provider-sync-error,omitempty"`
	CreatedAt         string                         `json:"created-at,omitempty"`
	UpdatedAt         string                         `json:"updated-at,omitempty"`
	LastSyncedAt       string                         `json:"last-synced-at,omitempty"`
}

func (h *Handler) ListOpenCodeGoAccounts(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}
	accounts := make([]openCodeGoAccountResponse, 0, len(h.cfg.OpenCodeGo.Accounts))
	for i := range h.cfg.OpenCodeGo.Accounts {
		accounts = append(accounts, openCodeGoAccountView(h.cfg.OpenCodeGo.Accounts[i], h.cfg.OpenCodeGo))
	}
	c.JSON(http.StatusOK, gin.H{
		"provider-name": openCodeGoProviderName(h.cfg.OpenCodeGo),
		"base-url":      strings.TrimSpace(h.cfg.OpenCodeGo.BaseURL),
		"accounts":      accounts,
	})
}

func (h *Handler) SyncOpenCodeGoAccount(c *gin.Context) {
	var req openCodeGoSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	req.APIKey = strings.TrimSpace(req.APIKey)
	if req.WorkspaceID == "" && req.APIKey == "" && strings.TrimSpace(req.ID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace-id, api-key, or id is required"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}

	idx := findOpenCodeGoAccountIndex(h.cfg.OpenCodeGo.Accounts, req.ID, req.WorkspaceID, req.APIKey)
	account := config.OpenCodeGoAccount{}
	if idx >= 0 {
		account = h.cfg.OpenCodeGo.Accounts[idx]
	} else {
		account.ID = openCodeGoAccountID(req.ID, req.WorkspaceID, req.APIKey)
		account.CreatedAt = now
	}
	account.Alias = strings.TrimSpace(req.Alias)
	account.Email = strings.TrimSpace(req.Email)
	account.Username = strings.TrimSpace(req.Username)
	account.WorkspaceID = req.WorkspaceID
	if req.APIKey != "" {
		account.APIKey = req.APIKey
		account.APIKeySynced = false
		account.ProviderSyncError = ""
	}
	if strings.TrimSpace(req.Cookie) != "" {
		account.Cookie = strings.TrimSpace(req.Cookie)
	}
	if req.Usage != nil {
		account.Usage = *req.Usage
	}
	account.LastSyncedAt = now
	account.UpdatedAt = now
	if account.ProviderName == "" {
		account.ProviderName = openCodeGoProviderName(h.cfg.OpenCodeGo)
	}
	if account.BaseURL == "" {
		account.BaseURL = strings.TrimSpace(h.cfg.OpenCodeGo.BaseURL)
	}

	if idx >= 0 {
		h.cfg.OpenCodeGo.Accounts[idx] = account
	} else {
		h.cfg.OpenCodeGo.Accounts = append(h.cfg.OpenCodeGo.Accounts, account)
	}
	snapshot, ok := h.saveConfigAndSnapshotLocked(c)
	if !ok {
		return
	}
	h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
	c.JSON(http.StatusOK, gin.H{"account": openCodeGoAccountView(account, h.cfg.OpenCodeGo)})
}

func openCodeGoProviderName(cfg config.OpenCodeGoConfig) string {
	name := strings.TrimSpace(cfg.ProviderName)
	if name == "" {
		return defaultOpenCodeGoProviderName
	}
	return strings.ToLower(name)
}

func openCodeGoAccountView(account config.OpenCodeGoAccount, cfg config.OpenCodeGoConfig) openCodeGoAccountResponse {
	providerName := strings.TrimSpace(account.ProviderName)
	if providerName == "" {
		providerName = openCodeGoProviderName(cfg)
	}
	baseURL := strings.TrimSpace(account.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(cfg.BaseURL)
	}
	return openCodeGoAccountResponse{
		ID:                account.ID,
		Alias:             account.Alias,
		Email:             account.Email,
		Username:          account.Username,
		WorkspaceID:       account.WorkspaceID,
		APIKeyPreview:     maskOpenCodeGoSecret(account.APIKey),
		HasAPIKey:         strings.TrimSpace(account.APIKey) != "",
		HasCookie:         strings.TrimSpace(account.Cookie) != "",
		Usage:             account.Usage,
		ProviderName:      providerName,
		BaseURL:           baseURL,
		APIKeySynced:      account.APIKeySynced,
		ProviderSyncedAt:  account.ProviderSyncedAt,
		ProviderSyncError: account.ProviderSyncError,
		CreatedAt:         account.CreatedAt,
		UpdatedAt:         account.UpdatedAt,
		LastSyncedAt:       account.LastSyncedAt,
	}
}

func findOpenCodeGoAccountIndex(accounts []config.OpenCodeGoAccount, id, workspaceID, apiKey string) int {
	id = strings.TrimSpace(id)
	workspaceID = strings.TrimSpace(workspaceID)
	apiKey = strings.TrimSpace(apiKey)
	for i := range accounts {
		if id != "" && accounts[i].ID == id {
			return i
		}
		if workspaceID != "" && accounts[i].WorkspaceID == workspaceID {
			return i
		}
		if apiKey != "" && accounts[i].APIKey == apiKey {
			return i
		}
	}
	return -1
}

func openCodeGoAccountID(id, workspaceID, apiKey string) string {
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		return trimmed
	}
	seed := strings.TrimSpace(workspaceID)
	if seed == "" {
		seed = strings.TrimSpace(apiKey)
	}
	sum := sha256.Sum256([]byte(seed))
	return "opencode_go_" + hex.EncodeToString(sum[:])[:12]
}

func maskOpenCodeGoSecret(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return secret[:1] + "***"
	}
	return secret[:4] + "***" + secret[len(secret)-4:]
}
```

- [ ] **Step 4: Run account sync tests and verify GREEN**

Run:

```bash
go test ./internal/api/handlers/management -run 'TestOpenCodeGoSync' -count=1
```

Expected: tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/api/handlers/management/opencode_go.go internal/api/handlers/management/opencode_go_test.go
git commit -m "feat: add opencode go account sync api"
```

---

### Task 3: Backend Provider Sync, Delete, Cookie Export, Routes

**Files:**
- Modify: `/Users/kogeki/dev/CLIProxyAPI/internal/api/handlers/management/opencode_go.go`
- Modify: `/Users/kogeki/dev/CLIProxyAPI/internal/api/server.go`
- Test: `/Users/kogeki/dev/CLIProxyAPI/internal/api/handlers/management/opencode_go_test.go`

- [ ] **Step 1: Add failing provider/delete/cookie tests**

Append these tests to `/Users/kogeki/dev/CLIProxyAPI/internal/api/handlers/management/opencode_go_test.go`:

```go
func TestOpenCodeGoSyncProviderRequiresBaseURL(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			Accounts: []config.OpenCodeGoAccount{{ID: "acc_1", APIKey: "sk-open"}},
		},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_1/sync-provider", nil, h.SyncOpenCodeGoProvider)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "base-url") {
		t.Fatalf("missing base-url error: %s", rec.Body.String())
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

	rec := performJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_1/sync-provider", nil, h.SyncOpenCodeGoProvider)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := len(h.cfg.OpenAICompatibility); got != 1 {
		t.Fatalf("openai compatibility len = %d, want 1", got)
	}
	provider := h.cfg.OpenAICompatibility[0]
	if provider.Name != "opencode-go" || provider.BaseURL != "https://go.example/v1" {
		t.Fatalf("provider mismatch: %#v", provider)
	}
	if got := len(provider.APIKeyEntries); got != 1 {
		t.Fatalf("api key entries len = %d, want 1", got)
	}
	if provider.APIKeyEntries[0].APIKey != "sk-open-1" {
		t.Fatalf("api key entry = %q", provider.APIKeyEntries[0].APIKey)
	}

	rec = performJSON(http.MethodPost, "/v0/management/opencode-go/accounts/acc_2/sync-provider", nil, h.SyncOpenCodeGoProvider)
	if rec.Code != http.StatusOK {
		t.Fatalf("second status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := len(h.cfg.OpenAICompatibility[0].APIKeyEntries); got != 2 {
		t.Fatalf("api key entries len = %d, want 2", got)
	}
}

func TestOpenCodeGoDeleteOptionallyRemovesProviderKey(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			ProviderName: "opencode-go",
			BaseURL:      "https://go.example/v1",
			Accounts: []config.OpenCodeGoAccount{
				{ID: "acc_1", APIKey: "sk-open-1"},
				{ID: "acc_2", APIKey: "sk-open-2"},
			},
		},
		OpenAICompatibility: []config.OpenAICompatibility{{
			Name: "opencode-go", BaseURL: "https://go.example/v1",
			APIKeyEntries: []config.OpenAICompatibilityAPIKey{{APIKey: "sk-open-1"}, {APIKey: "sk-open-2"}},
		}},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performJSON(http.MethodDelete, "/v0/management/opencode-go/accounts/acc_1?remove-provider-key=true", nil, h.DeleteOpenCodeGoAccount)
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

func TestOpenCodeGoSwitchCookieReturnsCookieOnlyForExistingAccount(t *testing.T) {
	t.Parallel()
	h := &Handler{cfg: &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{
			Accounts: []config.OpenCodeGoAccount{{ID: "acc_1", Cookie: "session=secret"}},
		},
	}, configFilePath: writeTestConfigFile(t)}

	rec := performJSON(http.MethodGet, "/v0/management/opencode-go/accounts/acc_1/switch-cookie", nil, h.GetOpenCodeGoSwitchCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "session=secret") {
		t.Fatalf("cookie missing: %s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/api/handlers/management -run 'TestOpenCodeGo(SyncProvider|Delete|SwitchCookie)' -count=1
```

Expected: fails because `SyncOpenCodeGoProvider`, `DeleteOpenCodeGoAccount`, and `GetOpenCodeGoSwitchCookie` do not exist.

- [ ] **Step 3: Implement provider/delete/cookie handlers**

Append these functions to `/Users/kogeki/dev/CLIProxyAPI/internal/api/handlers/management/opencode_go.go`:

```go
func (h *Handler) SyncOpenCodeGoProvider(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	now := time.Now().UTC().Format(time.RFC3339)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}
	idx := findOpenCodeGoAccountIndex(h.cfg.OpenCodeGo.Accounts, id, "", "")
	if idx < 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	account := &h.cfg.OpenCodeGo.Accounts[idx]
	apiKey := strings.TrimSpace(account.APIKey)
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account api-key is empty"})
		return
	}
	providerName := openCodeGoProviderName(h.cfg.OpenCodeGo)
	baseURL := strings.TrimSpace(account.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(h.cfg.OpenCodeGo.BaseURL)
	}
	if baseURL == "" {
		account.ProviderSyncError = "base-url is required before syncing provider"
		c.JSON(http.StatusBadRequest, gin.H{"error": account.ProviderSyncError})
		return
	}
	upsertOpenCodeGoProviderKey(h.cfg, providerName, baseURL, apiKey)
	account.ProviderName = providerName
	account.BaseURL = baseURL
	account.APIKeySynced = true
	account.ProviderSyncedAt = now
	account.ProviderSyncError = ""
	account.UpdatedAt = now
	snapshot, ok := h.saveConfigAndSnapshotLocked(c)
	if !ok {
		return
	}
	h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
	c.JSON(http.StatusOK, gin.H{"account": openCodeGoAccountView(*account, h.cfg.OpenCodeGo)})
}

func (h *Handler) DeleteOpenCodeGoAccount(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	removeProviderKey := strings.EqualFold(c.Query("remove-provider-key"), "true")
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}
	idx := findOpenCodeGoAccountIndex(h.cfg.OpenCodeGo.Accounts, id, "", "")
	if idx < 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	account := h.cfg.OpenCodeGo.Accounts[idx]
	h.cfg.OpenCodeGo.Accounts = append(h.cfg.OpenCodeGo.Accounts[:idx], h.cfg.OpenCodeGo.Accounts[idx+1:]...)
	if removeProviderKey && strings.TrimSpace(account.APIKey) != "" {
		removeOpenCodeGoProviderKey(h.cfg, openCodeGoProviderName(h.cfg.OpenCodeGo), account.APIKey)
	}
	snapshot, ok := h.saveConfigAndSnapshotLocked(c)
	if !ok {
		return
	}
	h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (h *Handler) GetOpenCodeGoSwitchCookie(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}
	idx := findOpenCodeGoAccountIndex(h.cfg.OpenCodeGo.Accounts, id, "", "")
	if idx < 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	cookie := strings.TrimSpace(h.cfg.OpenCodeGo.Accounts[idx].Cookie)
	if cookie == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account has no stored cookie"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"cookie": cookie})
}

func upsertOpenCodeGoProviderKey(cfg *config.Config, providerName, baseURL, apiKey string) {
	if cfg == nil {
		return
	}
	providerName = strings.TrimSpace(providerName)
	baseURL = strings.TrimSpace(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	if providerName == "" || baseURL == "" || apiKey == "" {
		return
	}
	for i := range cfg.OpenAICompatibility {
		provider := &cfg.OpenAICompatibility[i]
		if !strings.EqualFold(strings.TrimSpace(provider.Name), providerName) {
			continue
		}
		provider.BaseURL = baseURL
		for j := range provider.APIKeyEntries {
			if provider.APIKeyEntries[j].APIKey == apiKey {
				return
			}
		}
		provider.APIKeyEntries = append(provider.APIKeyEntries, config.OpenAICompatibilityAPIKey{APIKey: apiKey})
		return
	}
	cfg.OpenAICompatibility = append(cfg.OpenAICompatibility, config.OpenAICompatibility{
		Name:          providerName,
		BaseURL:       baseURL,
		APIKeyEntries: []config.OpenAICompatibilityAPIKey{{APIKey: apiKey}},
	})
}

func removeOpenCodeGoProviderKey(cfg *config.Config, providerName, apiKey string) {
	if cfg == nil {
		return
	}
	for i := range cfg.OpenAICompatibility {
		provider := &cfg.OpenAICompatibility[i]
		if !strings.EqualFold(strings.TrimSpace(provider.Name), providerName) {
			continue
		}
		out := provider.APIKeyEntries[:0]
		for _, entry := range provider.APIKeyEntries {
			if entry.APIKey != apiKey {
				out = append(out, entry)
			}
		}
		provider.APIKeyEntries = out
		return
	}
}
```

- [ ] **Step 4: Add userscript config handler**

Add to `opencode_go.go`:

```go
func (h *Handler) GetOpenCodeGoUserscriptConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"name":            "opencode go账号助手",
		"match":           "https://opencode.ai/*",
		"management-base": "/v0/management",
		"endpoints": gin.H{
			"accounts":      "/opencode-go/accounts",
			"sync":          "/opencode-go/sync",
			"sync-provider": "/opencode-go/accounts/{id}/sync-provider",
			"switch-cookie": "/opencode-go/accounts/{id}/switch-cookie",
		},
	})
}
```

- [ ] **Step 5: Register routes**

In `/Users/kogeki/dev/CLIProxyAPI/internal/api/server.go`, inside `registerManagementRoutes`, after OpenAI compatibility routes add:

```go
		mgmt.GET("/opencode-go/accounts", s.mgmt.ListOpenCodeGoAccounts)
		mgmt.POST("/opencode-go/sync", s.mgmt.SyncOpenCodeGoAccount)
		mgmt.POST("/opencode-go/accounts/:id/sync-provider", s.mgmt.SyncOpenCodeGoProvider)
		mgmt.DELETE("/opencode-go/accounts/:id", s.mgmt.DeleteOpenCodeGoAccount)
		mgmt.GET("/opencode-go/accounts/:id/switch-cookie", s.mgmt.GetOpenCodeGoSwitchCookie)
		mgmt.GET("/opencode-go/userscript-config", s.mgmt.GetOpenCodeGoUserscriptConfig)
```

- [ ] **Step 6: Run backend tests and verify GREEN**

Run:

```bash
go test ./internal/api/handlers/management -run 'TestOpenCodeGo' -count=1
go test ./internal/api -run 'Test.*Management' -count=1
```

Expected: selected tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/api/handlers/management/opencode_go.go internal/api/handlers/management/opencode_go_test.go internal/api/server.go
git commit -m "feat: sync opencode go keys into providers"
```

---

### Task 4: Management UI API Layer and Navigation

**Files:**
- Create: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/types/opencodeGo.ts`
- Create: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/services/api/opencodeGo.ts`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/router/MainRoutes.tsx`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/components/layout/MainLayout.tsx`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/i18n/locales/zh-CN.json`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/i18n/locales/zh-TW.json`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/i18n/locales/en.json`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/i18n/locales/ru.json`

- [ ] **Step 1: Add TypeScript types**

Create `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/types/opencodeGo.ts`:

```typescript
export interface OpenCodeGoUsageWindow {
  used?: number;
  limit?: number;
  resetAt?: string;
}

export interface OpenCodeGoUsageSnapshot {
  rolling?: OpenCodeGoUsageWindow;
  weekly?: OpenCodeGoUsageWindow;
  monthly?: OpenCodeGoUsageWindow;
}

export interface OpenCodeGoAccount {
  id: string;
  alias?: string;
  email?: string;
  username?: string;
  workspaceId?: string;
  apiKeyPreview?: string;
  hasApiKey: boolean;
  hasCookie: boolean;
  usage?: OpenCodeGoUsageSnapshot;
  providerName?: string;
  baseUrl?: string;
  apiKeySynced: boolean;
  providerSyncedAt?: string;
  providerSyncError?: string;
  createdAt?: string;
  updatedAt?: string;
  lastSyncedAt?: string;
}

export interface OpenCodeGoAccountsResponse {
  providerName: string;
  baseUrl: string;
  accounts: OpenCodeGoAccount[];
}

export interface OpenCodeGoUserscriptConfig {
  name: string;
  match: string;
  managementBase: string;
  endpoints: Record<string, string>;
}
```

- [ ] **Step 2: Add API wrapper**

Create `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/services/api/opencodeGo.ts`:

```typescript
import { apiClient } from './client';
import type {
  OpenCodeGoAccount,
  OpenCodeGoAccountsResponse,
  OpenCodeGoUserscriptConfig,
} from '@/types/opencodeGo';

const normalizeUsageWindow = (raw: unknown) => {
  if (!raw || typeof raw !== 'object') return undefined;
  const record = raw as Record<string, unknown>;
  return {
    used: typeof record.used === 'number' ? record.used : undefined,
    limit: typeof record.limit === 'number' ? record.limit : undefined,
    resetAt: typeof record['reset-at'] === 'string' ? record['reset-at'] : undefined,
  };
};

const normalizeAccount = (raw: unknown): OpenCodeGoAccount | null => {
  if (!raw || typeof raw !== 'object') return null;
  const record = raw as Record<string, unknown>;
  const id = typeof record.id === 'string' ? record.id : '';
  if (!id) return null;
  const usage = record.usage && typeof record.usage === 'object' ? (record.usage as Record<string, unknown>) : {};
  return {
    id,
    alias: typeof record.alias === 'string' ? record.alias : undefined,
    email: typeof record.email === 'string' ? record.email : undefined,
    username: typeof record.username === 'string' ? record.username : undefined,
    workspaceId: typeof record['workspace-id'] === 'string' ? record['workspace-id'] : undefined,
    apiKeyPreview: typeof record['api-key-preview'] === 'string' ? record['api-key-preview'] : undefined,
    hasApiKey: Boolean(record['has-api-key']),
    hasCookie: Boolean(record['has-cookie']),
    usage: {
      rolling: normalizeUsageWindow(usage.rolling),
      weekly: normalizeUsageWindow(usage.weekly),
      monthly: normalizeUsageWindow(usage.monthly),
    },
    providerName: typeof record['provider-name'] === 'string' ? record['provider-name'] : undefined,
    baseUrl: typeof record['base-url'] === 'string' ? record['base-url'] : undefined,
    apiKeySynced: Boolean(record['api-key-synced']),
    providerSyncedAt: typeof record['provider-synced-at'] === 'string' ? record['provider-synced-at'] : undefined,
    providerSyncError: typeof record['provider-sync-error'] === 'string' ? record['provider-sync-error'] : undefined,
    createdAt: typeof record['created-at'] === 'string' ? record['created-at'] : undefined,
    updatedAt: typeof record['updated-at'] === 'string' ? record['updated-at'] : undefined,
    lastSyncedAt: typeof record['last-synced-at'] === 'string' ? record['last-synced-at'] : undefined,
  };
};

export const opencodeGoApi = {
  async list(): Promise<OpenCodeGoAccountsResponse> {
    const data = await apiClient.get<Record<string, unknown>>('/opencode-go/accounts');
    const accountsRaw = Array.isArray(data.accounts) ? data.accounts : [];
    return {
      providerName: typeof data['provider-name'] === 'string' ? data['provider-name'] : 'opencode-go',
      baseUrl: typeof data['base-url'] === 'string' ? data['base-url'] : '',
      accounts: accountsRaw.map(normalizeAccount).filter(Boolean) as OpenCodeGoAccount[],
    };
  },

  syncProvider(id: string): Promise<{ account: OpenCodeGoAccount }> {
    return apiClient.post(`/opencode-go/accounts/${encodeURIComponent(id)}/sync-provider`);
  },

  deleteAccount(id: string, removeProviderKey: boolean): Promise<{ deleted: boolean }> {
    return apiClient.delete(
      `/opencode-go/accounts/${encodeURIComponent(id)}?remove-provider-key=${removeProviderKey ? 'true' : 'false'}`
    );
  },

  userscriptConfig(): Promise<OpenCodeGoUserscriptConfig> {
    return apiClient.get('/opencode-go/userscript-config');
  },
};
```

- [ ] **Step 3: Add placeholder page and route**

Create a temporary page in `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/pages/OpenCodeGoPage.tsx`:

```tsx
export function OpenCodeGoPage() {
  return <div>OpenCode Go</div>;
}
```

Modify `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/router/MainRoutes.tsx`:

```tsx
import { OpenCodeGoPage } from '@/pages/OpenCodeGoPage';
```

Add this route near `/ai-providers` and `/auth-files`:

```tsx
  { path: '/opencode-go', element: <OpenCodeGoPage /> },
```

- [ ] **Step 4: Add sidebar entry**

In `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/components/layout/MainLayout.tsx`, add an icon entry to `sidebarIcons` using the same local SVG style as existing icons:

```tsx
  opencodeGo: (
    <svg {...iconProps}>
      <path d="M7 8h10" />
      <path d="M7 12h10" />
      <path d="M7 16h6" />
      <path d="M5 4h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z" />
    </svg>
  ),
```

Add a gateway nav item after AI providers:

```tsx
        {
          path: '/opencode-go',
          labelKey: 'nav.opencode_go',
          metaKey: 'nav_meta.opencode_go',
          icon: sidebarIcons.opencodeGo,
        },
```

- [ ] **Step 5: Add i18n keys**

Add these keys to each locale's `nav` and `nav_meta` objects:

Chinese simplified:

```json
"opencode_go": "OpenCode Go"
```

```json
"opencode_go": "同步 Go 账号与用量"
```

Chinese traditional:

```json
"opencode_go": "OpenCode Go"
```

```json
"opencode_go": "同步 Go 帳號與用量"
```

English:

```json
"opencode_go": "OpenCode Go"
```

```json
"opencode_go": "Sync Go accounts and usage"
```

Russian:

```json
"opencode_go": "OpenCode Go"
```

```json
"opencode_go": "Синхронизация аккаунтов Go"
```

- [ ] **Step 6: Verify typecheck**

Run:

```bash
bun run type-check
```

Expected: TypeScript succeeds with the placeholder page.

- [ ] **Step 7: Commit**

```bash
git add src/types/opencodeGo.ts src/services/api/opencodeGo.ts src/pages/OpenCodeGoPage.tsx src/router/MainRoutes.tsx src/components/layout/MainLayout.tsx src/i18n/locales/zh-CN.json src/i18n/locales/zh-TW.json src/i18n/locales/en.json src/i18n/locales/ru.json
git commit -m "feat: add opencode go management route"
```

---

### Task 5: Management UI OpenCode Go Page

**Files:**
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/pages/OpenCodeGoPage.tsx`
- Create: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/pages/OpenCodeGoPage.module.scss`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/i18n/locales/zh-CN.json`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/i18n/locales/zh-TW.json`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/i18n/locales/en.json`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/i18n/locales/ru.json`

- [ ] **Step 1: Replace placeholder page with real UI**

Implement `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/pages/OpenCodeGoPage.tsx`:

```tsx
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/common';
import { opencodeGoApi } from '@/services/api/opencodeGo';
import { useNotificationStore } from '@/stores';
import { copyToClipboard } from '@/utils/clipboard';
import { formatDateValue } from '@/utils/format';
import type { OpenCodeGoAccount } from '@/types/opencodeGo';
import styles from './OpenCodeGoPage.module.scss';

const usagePercent = (used?: number, limit?: number) => {
  if (!limit || limit <= 0 || used === undefined) return null;
  return Math.min(100, Math.max(0, (used / limit) * 100));
};

export function OpenCodeGoPage() {
  const { t, i18n } = useTranslation();
  const { showNotification } = useNotificationStore();
  const [accounts, setAccounts] = useState<OpenCodeGoAccount[]>([]);
  const [providerName, setProviderName] = useState('opencode-go');
  const [baseUrl, setBaseUrl] = useState('');
  const [loading, setLoading] = useState(false);
  const [busyID, setBusyID] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await opencodeGoApi.list();
      setProviderName(result.providerName);
      setBaseUrl(result.baseUrl);
      setAccounts(result.accounts);
    } catch (error) {
      showNotification(error instanceof Error ? error.message : t('opencode_go.load_failed'), 'error');
    } finally {
      setLoading(false);
    }
  }, [showNotification, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const configText = useMemo(
    () =>
      JSON.stringify(
        {
          cpaBaseUrl: window.location.origin,
          managementBase: '/v0/management',
          syncEndpoint: '/opencode-go/sync',
          accountsEndpoint: '/opencode-go/accounts',
        },
        null,
        2
      ),
    []
  );

  const copyConfig = async () => {
    const ok = await copyToClipboard(configText);
    showNotification(ok ? t('opencode_go.config_copied') : t('notification.copy_failed'), ok ? 'success' : 'error');
  };

  const syncProvider = async (account: OpenCodeGoAccount) => {
    setBusyID(account.id);
    try {
      await opencodeGoApi.syncProvider(account.id);
      showNotification(t('opencode_go.provider_synced'), 'success');
      await load();
    } catch (error) {
      showNotification(error instanceof Error ? error.message : t('opencode_go.provider_sync_failed'), 'error');
    } finally {
      setBusyID(null);
    }
  };

  const deleteAccount = async (account: OpenCodeGoAccount) => {
    if (!window.confirm(t('opencode_go.delete_confirm'))) return;
    setBusyID(account.id);
    try {
      await opencodeGoApi.deleteAccount(account.id, true);
      showNotification(t('opencode_go.deleted'), 'success');
      await load();
    } catch (error) {
      showNotification(error instanceof Error ? error.message : t('opencode_go.delete_failed'), 'error');
    } finally {
      setBusyID(null);
    }
  };

  const usageWindows = (account: OpenCodeGoAccount) => [
    { key: 'rolling', label: t('opencode_go.rolling'), value: account.usage?.rolling },
    { key: 'weekly', label: t('opencode_go.weekly'), value: account.usage?.weekly },
    { key: 'monthly', label: t('opencode_go.monthly'), value: account.usage?.monthly },
  ];

  return (
    <div className={styles.page}>
      <section className={styles.header}>
        <div>
          <h1>{t('opencode_go.title')}</h1>
          <p>{t('opencode_go.subtitle')}</p>
        </div>
        <div className={styles.headerActions}>
          <Button variant="secondary" onClick={copyConfig}>
            {t('opencode_go.copy_config')}
          </Button>
          <Button onClick={load} disabled={loading}>
            {loading ? t('common.loading') : t('common.refresh')}
          </Button>
        </div>
      </section>

      <section className={styles.providerBar}>
        <span>{t('opencode_go.provider_name')}: {providerName}</span>
        <span>{t('opencode_go.base_url')}: {baseUrl || t('opencode_go.base_url_missing')}</span>
      </section>

      <section className={styles.accountList}>
        {accounts.length === 0 && !loading ? (
          <div className={styles.empty}>{t('opencode_go.empty')}</div>
        ) : null}
        {accounts.map((account) => (
          <article className={styles.accountCard} key={account.id}>
            <div className={styles.accountMain}>
              <div>
                <h2>{account.alias || account.email || account.username || account.workspaceId || account.id}</h2>
                <p>{account.email || account.username || account.workspaceId || account.id}</p>
              </div>
              <div className={styles.badges}>
                <span className={account.hasApiKey ? styles.badgeOk : styles.badgeWarn}>
                  {account.apiKeyPreview || t('opencode_go.no_api_key')}
                </span>
                <span className={account.hasCookie ? styles.badgeOk : styles.badgeMuted}>
                  {account.hasCookie ? t('opencode_go.cookie_saved') : t('opencode_go.cookie_missing')}
                </span>
                <span className={account.apiKeySynced ? styles.badgeOk : styles.badgeWarn}>
                  {account.apiKeySynced ? t('opencode_go.provider_synced_status') : t('opencode_go.provider_not_synced')}
                </span>
              </div>
            </div>

            <div className={styles.usageGrid}>
              {usageWindows(account).map((item) => {
                const percent = usagePercent(item.value?.used, item.value?.limit);
                return (
                  <div className={styles.usageItem} key={item.key}>
                    <div className={styles.usageHeader}>
                      <span>{item.label}</span>
                      <strong>{item.value?.used ?? '-'} / {item.value?.limit ?? '-'}</strong>
                    </div>
                    <div className={styles.meter}>
                      <span style={{ width: `${percent ?? 0}%` }} />
                    </div>
                    {item.value?.resetAt ? (
                      <small>{formatDateValue(item.value.resetAt, i18n.language)}</small>
                    ) : null}
                  </div>
                );
              })}
            </div>

            {account.providerSyncError ? <p className={styles.error}>{account.providerSyncError}</p> : null}
            <div className={styles.meta}>
              <span>{t('opencode_go.workspace_id')}: {account.workspaceId || '-'}</span>
              <span>{t('opencode_go.last_sync')}: {formatDateValue(account.lastSyncedAt, i18n.language) || '-'}</span>
            </div>
            <div className={styles.actions}>
              <Button size="sm" onClick={() => syncProvider(account)} disabled={busyID === account.id || !account.hasApiKey}>
                {t('opencode_go.sync_provider')}
              </Button>
              <Button size="sm" variant="danger" onClick={() => deleteAccount(account)} disabled={busyID === account.id}>
                {t('common.delete')}
              </Button>
            </div>
          </article>
        ))}
      </section>
    </div>
  );
}
```

- [ ] **Step 2: Add page styles**

Create `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/pages/OpenCodeGoPage.module.scss`:

```scss
.page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;

  h1 {
    margin: 0;
    font-size: 28px;
  }

  p {
    margin: 6px 0 0;
    color: var(--text-secondary);
  }
}

.headerActions,
.actions,
.badges,
.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.providerBar,
.empty,
.accountCard {
  border: 1px solid var(--border-color);
  border-radius: 8px;
  background: var(--card-bg);
}

.providerBar {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  padding: 12px 14px;
  color: var(--text-secondary);
}

.empty {
  padding: 28px;
  color: var(--text-secondary);
  text-align: center;
}

.accountList {
  display: grid;
  gap: 12px;
}

.accountCard {
  display: grid;
  gap: 14px;
  padding: 16px;
}

.accountMain {
  display: flex;
  justify-content: space-between;
  gap: 12px;

  h2 {
    margin: 0;
    font-size: 18px;
  }

  p {
    margin: 4px 0 0;
    color: var(--text-secondary);
  }
}

.badgeOk,
.badgeWarn,
.badgeMuted {
  display: inline-flex;
  align-items: center;
  min-height: 24px;
  padding: 0 8px;
  border-radius: 999px;
  font-size: 12px;
}

.badgeOk {
  color: #116149;
  background: #dff6ed;
}

.badgeWarn {
  color: #7a4b00;
  background: #fff1cf;
}

.badgeMuted {
  color: var(--text-secondary);
  background: var(--bg-secondary);
}

.usageGrid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}

.usageItem {
  display: grid;
  gap: 6px;
  min-width: 0;
}

.usageHeader {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-size: 13px;
}

.meter {
  height: 6px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--bg-secondary);

  span {
    display: block;
    height: 100%;
    background: #2f7d62;
  }
}

.error {
  margin: 0;
  color: var(--danger-color);
}

.meta {
  color: var(--text-secondary);
  font-size: 13px;
}

@media (max-width: 720px) {
  .header,
  .accountMain {
    flex-direction: column;
  }

  .usageGrid {
    grid-template-columns: 1fr;
  }
}
```

- [ ] **Step 3: Add page i18n keys**

Add an `opencode_go` object to all locale JSON files. Chinese simplified values:

```json
"opencode_go": {
  "title": "OpenCode Go 账号",
  "subtitle": "从浏览器助手同步账号、API key 和用量，并写入 OpenAI-compatible 提供商。",
  "copy_config": "复制脚本配置",
  "config_copied": "脚本配置已复制",
  "provider_name": "提供商",
  "base_url": "服务地址",
  "base_url_missing": "未配置",
  "empty": "还没有同步账号，请在 opencode.ai 页面使用脚本同步当前账号。",
  "load_failed": "加载 OpenCode Go 账号失败",
  "provider_synced": "提供商已同步",
  "provider_sync_failed": "同步提供商失败",
  "provider_synced_status": "已写入提供商",
  "provider_not_synced": "未写入提供商",
  "delete_confirm": "确定删除这个 OpenCode Go 账号，并移除对应 provider key 吗？",
  "deleted": "账号已删除",
  "delete_failed": "删除账号失败",
  "rolling": "滚动",
  "weekly": "每周",
  "monthly": "每月",
  "no_api_key": "无 API key",
  "cookie_saved": "Cookie 已保存",
  "cookie_missing": "无 Cookie",
  "workspace_id": "工作区",
  "last_sync": "最近同步",
  "sync_provider": "同步到提供商"
}
```

Translate the same keys for `zh-TW`, `en`, and `ru`; keep the keys identical.

- [ ] **Step 4: Verify UI build**

Run:

```bash
bun run type-check
bun run build
```

Expected: both pass.

- [ ] **Step 5: Commit**

```bash
git add src/pages/OpenCodeGoPage.tsx src/pages/OpenCodeGoPage.module.scss src/i18n/locales/zh-CN.json src/i18n/locales/zh-TW.json src/i18n/locales/en.json src/i18n/locales/ru.json
git commit -m "feat: build opencode go management page"
```

---

### Task 6: Tampermonkey Userscript Project

**Files:**
- Create directory: `/Users/kogeki/dev/opencode-go-account-helper-userscript`
- Create: `/Users/kogeki/dev/opencode-go-account-helper-userscript/opencode-go-account-helper.user.js`
- Create: `/Users/kogeki/dev/opencode-go-account-helper-userscript/README.md`

- [ ] **Step 1: Create userscript file**

Create `/Users/kogeki/dev/opencode-go-account-helper-userscript/opencode-go-account-helper.user.js`:

```javascript
// ==UserScript==
// @name         opencode go账号助手
// @namespace    https://your-cpa.example.com/opencode-go
// @version      0.1.0
// @description  Sync OpenCode Go account metadata, usage, API key, and opt-in cookies to CPA.
// @match        https://opencode.ai/*
// @grant        GM_xmlhttpRequest
// @grant        GM_getValue
// @grant        GM_setValue
// @grant        GM_registerMenuCommand
// @grant        GM_cookie
// @connect      *
// ==/UserScript==

(function () {
  'use strict';

  const SCRIPT_NAME = 'opencode go账号助手';
  const DEFAULT_CPA_BASE = 'https://your-cpa.example.com/v0/management';

  const state = {
    panel: null,
    accounts: [],
    message: '',
  };

  const getSetting = (key, fallback = '') => GM_getValue(key, fallback);
  const setSetting = (key, value) => GM_setValue(key, value);

  function cpaBase() {
    return String(getSetting('cpaBase', DEFAULT_CPA_BASE)).replace(/\/+$/, '');
  }

  function managementKey() {
    return String(getSetting('managementKey', ''));
  }

  function request(method, path, body) {
    return new Promise((resolve, reject) => {
      const headers = { 'Content-Type': 'application/json' };
      const key = managementKey();
      if (key) headers.Authorization = `Bearer ${key}`;
      GM_xmlhttpRequest({
        method,
        url: `${cpaBase()}${path}`,
        headers,
        data: body ? JSON.stringify(body) : undefined,
        onload: (response) => {
          let data = null;
          try {
            data = response.responseText ? JSON.parse(response.responseText) : null;
          } catch {
            data = response.responseText;
          }
          if (response.status >= 200 && response.status < 300) {
            resolve(data);
            return;
          }
          reject(new Error((data && data.error) || `HTTP ${response.status}`));
        },
        onerror: () => reject(new Error('request failed')),
      });
    });
  }

  function detectWorkspaceID() {
    const match = location.pathname.match(/\/workspace\/([^/]+)/);
    return match ? decodeURIComponent(match[1]) : '';
  }

  function detectAccount() {
    const text = document.body ? document.body.innerText : '';
    const emailMatch = text.match(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/i);
    return {
      workspaceId: detectWorkspaceID(),
      email: emailMatch ? emailMatch[0] : '',
    };
  }

  function findApiKeyInPage() {
    const text = document.body ? document.body.innerText : '';
    const match = text.match(/sk-[A-Za-z0-9_-]{20,}/);
    return match ? match[0] : '';
  }

  function usageFromWindow() {
    const text = document.body ? document.body.innerText : '';
    const read = (label) => {
      const pattern = new RegExp(`${label}[^0-9]*([0-9.]+)\\s*/\\s*([0-9.]+)`, 'i');
      const match = text.match(pattern);
      if (!match) return undefined;
      return { used: Number(match[1]), limit: Number(match[2]) };
    };
    return {
      rolling: read('rolling'),
      weekly: read('weekly'),
      monthly: read('monthly'),
    };
  }

  async function readCookieString() {
    const allowCookie = Boolean(getSetting('allowCookieUpload', false));
    if (!allowCookie) return '';
    if (typeof GM_cookie === 'undefined' || !GM_cookie.list) {
      return document.cookie || '';
    }
    const cookies = await new Promise((resolve) => {
      GM_cookie.list({ domain: 'opencode.ai' }, (items) => resolve(items || []));
    });
    return cookies.map((item) => `${item.name}=${item.value}`).join('; ');
  }

  async function syncCurrentAccount() {
    const detected = detectAccount();
    const apiKey = findApiKeyInPage();
    const cookie = await readCookieString();
    const payload = {
      alias: detected.email || detected.workspaceId || 'OpenCode Go',
      email: detected.email,
      'workspace-id': detected.workspaceId,
      'api-key': apiKey,
      cookie,
      usage: usageFromWindow(),
    };
    const result = await request('POST', '/opencode-go/sync', payload);
    state.message = `已同步: ${(result && result.account && result.account.id) || 'ok'}`;
    renderPanel();
  }

  async function loadAccounts() {
    const result = await request('GET', '/opencode-go/accounts');
    state.accounts = Array.isArray(result.accounts) ? result.accounts : [];
    state.message = `已加载 ${state.accounts.length} 个账号`;
    renderPanel();
  }

  async function switchAccount(account) {
    if (!window.confirm(`切换到 ${account.alias || account.email || account.id}？这会覆盖当前 opencode.ai Cookie 并刷新页面。`)) {
      return;
    }
    const result = await request('GET', `/opencode-go/accounts/${encodeURIComponent(account.id)}/switch-cookie`);
    const cookie = result && result.cookie;
    if (!cookie) throw new Error('该账号没有可用 Cookie');
    const pairs = cookie.split(';').map((part) => part.trim()).filter(Boolean);
    for (const pair of pairs) {
      const eq = pair.indexOf('=');
      if (eq <= 0) continue;
      document.cookie = `${pair}; domain=.opencode.ai; path=/; secure; SameSite=Lax`;
    }
    location.reload();
  }

  function renderPanel() {
    if (!state.panel) return;
    const rows = state.accounts
      .map((account) => `<button data-switch="${account.id}">${account.alias || account.email || account.workspaceId || account.id}</button>`)
      .join('');
    state.panel.innerHTML = `
      <div class="ocg-title">${SCRIPT_NAME}</div>
      <label>CPA 地址<input data-field="cpaBase" value="${cpaBase()}"></label>
      <label>管理密钥<input data-field="managementKey" type="password" value="${managementKey()}"></label>
      <label class="ocg-row"><input data-field="allowCookieUpload" type="checkbox" ${getSetting('allowCookieUpload', false) ? 'checked' : ''}> 允许上传 Cookie</label>
      <div class="ocg-actions">
        <button data-action="sync">同步当前账号</button>
        <button data-action="load">拉取账号</button>
      </div>
      <div class="ocg-message">${state.message || ''}</div>
      <div class="ocg-accounts">${rows}</div>
    `;
    state.panel.querySelectorAll('[data-field]').forEach((input) => {
      input.addEventListener('change', () => {
        if (input.type === 'checkbox') setSetting(input.dataset.field, input.checked);
        else setSetting(input.dataset.field, input.value);
      });
    });
    state.panel.querySelector('[data-action="sync"]').addEventListener('click', () => syncCurrentAccount().catch(showError));
    state.panel.querySelector('[data-action="load"]').addEventListener('click', () => loadAccounts().catch(showError));
    state.panel.querySelectorAll('[data-switch]').forEach((button) => {
      button.addEventListener('click', () => {
        const account = state.accounts.find((item) => item.id === button.dataset.switch);
        if (account) switchAccount(account).catch(showError);
      });
    });
  }

  function showError(error) {
    state.message = error && error.message ? error.message : String(error);
    renderPanel();
  }

  function togglePanel() {
    if (!state.panel) {
      state.panel = document.createElement('div');
      state.panel.id = 'opencode-go-account-helper';
      document.body.appendChild(state.panel);
      const style = document.createElement('style');
      style.textContent = `
        #opencode-go-account-helper{position:fixed;right:16px;bottom:16px;z-index:2147483647;width:320px;padding:12px;border:1px solid #d0d7de;border-radius:8px;background:#fff;color:#1f2328;box-shadow:0 12px 32px rgba(0,0,0,.18);font:13px system-ui,sans-serif}
        #opencode-go-account-helper .ocg-title{font-weight:700;margin-bottom:8px}
        #opencode-go-account-helper label{display:grid;gap:4px;margin:8px 0}
        #opencode-go-account-helper input{box-sizing:border-box;width:100%;padding:6px;border:1px solid #d0d7de;border-radius:6px}
        #opencode-go-account-helper .ocg-row{display:flex;align-items:center;gap:6px}
        #opencode-go-account-helper .ocg-row input{width:auto}
        #opencode-go-account-helper button{margin:4px 4px 0 0;padding:6px 8px;border:1px solid #d0d7de;border-radius:6px;background:#f6f8fa;cursor:pointer}
        #opencode-go-account-helper .ocg-message{margin-top:8px;color:#57606a}
        #opencode-go-account-helper .ocg-accounts{display:grid;gap:4px;margin-top:8px}
      `;
      document.head.appendChild(style);
    } else {
      state.panel.hidden = !state.panel.hidden;
    }
    renderPanel();
  }

  GM_registerMenuCommand('打开 OpenCode Go 账号助手', togglePanel);
  GM_registerMenuCommand('同步当前 OpenCode Go 账号', () => syncCurrentAccount().catch(showError));
})();
```

- [ ] **Step 2: Add README**

Create `/Users/kogeki/dev/opencode-go-account-helper-userscript/README.md`:

```markdown
# opencode go账号助手

这是给 CPA OpenCode Go 安全 MVP 使用的 Tampermonkey 脚本。

## 安装

1. 安装 Tampermonkey。
2. 新建脚本并粘贴 `opencode-go-account-helper.user.js`。
3. 打开 `https://opencode.ai/` 并登录目标账号。
4. 从 Tampermonkey 菜单打开「opencode go账号助手」。
5. 填写 CPA management 地址，例如 `https://your-cpa.example.com/v0/management`。
6. 填写 CPA 管理密钥。

## 功能

- 同步当前账号的 workspaceId、邮箱、页面中可见的 API key 和用量。
- Cookie 上传默认关闭，需要手动勾选。
- 从 CPA 拉取已保存账号列表。
- 用户确认后写入 Cookie 并刷新页面来切换账号。

## 限制

- `document.cookie` 无法读取 HttpOnly Cookie。
- `GM_cookie` 是否能读取/写入 HttpOnly Cookie 取决于浏览器和 Tampermonkey 环境。
- 脚本不做自动注册、不做自动领奖。
```

- [ ] **Step 3: Syntax-check userscript**

Run:

```bash
node --check /Users/kogeki/dev/opencode-go-account-helper-userscript/opencode-go-account-helper.user.js
```

Expected: no syntax errors.

- [ ] **Step 4: Commit if the userscript folder is a git repo**

If `/Users/kogeki/dev/opencode-go-account-helper-userscript/.git` exists:

```bash
git add opencode-go-account-helper.user.js README.md
git commit -m "feat: add opencode go account helper userscript"
```

If it is not a git repo, do not commit it from inside `CLIProxyAPI`.

---

### Task 7: Integration Verification

**Files:**
- Backend and management UI only if verification reveals small build fixes.

- [ ] **Step 1: Run backend verification**

Run in `/Users/kogeki/dev/CLIProxyAPI`:

```bash
gofmt -w internal/config/config.go internal/config/opencode_go_test.go internal/api/handlers/management/opencode_go.go internal/api/handlers/management/opencode_go_test.go internal/api/server.go
go test ./internal/config ./internal/api/handlers/management ./internal/api -count=1
go test ./sdk/cliproxy/auth -run 'TestAPIKeyAccess|TestOpenAICompat' -count=1
```

Expected: all selected tests pass.

- [ ] **Step 2: Run management UI verification**

Run in `/Users/kogeki/dev/Cli-Proxy-API-Management-Center`:

```bash
bun run type-check
bun run build
```

Expected: both pass.

- [ ] **Step 3: Run userscript verification**

Run:

```bash
node --check /Users/kogeki/dev/opencode-go-account-helper-userscript/opencode-go-account-helper.user.js
```

Expected: no syntax errors.

- [ ] **Step 4: Manual API smoke commands**

With a running CPA server and valid management key, use:

```bash
curl -sS -H "Authorization: Bearer $MANAGEMENT_KEY" \
  "$CPA_BASE/v0/management/opencode-go/accounts"
```

Expected: JSON contains `accounts`.

```bash
curl -sS -X POST -H "Authorization: Bearer $MANAGEMENT_KEY" -H "Content-Type: application/json" \
  "$CPA_BASE/v0/management/opencode-go/sync" \
  -d '{"alias":"smoke","workspace-id":"ws_smoke","api-key":"sk-smoke12345678901234567890","usage":{"rolling":{"used":1,"limit":10}}}'
```

Expected: JSON contains an `account` object with redacted `api-key-preview` and no full `api-key`.

- [ ] **Step 5: Commit build fixes if any**

If verification required code changes:

```bash
git add <changed files>
git commit -m "fix: polish opencode go integration"
```

If no changes were needed, do not create an empty commit.

---

## Self-Review

- Spec coverage:
  - CPA backend account sync, usage snapshot, optional cookie, provider write, delete, userscript config: covered by Tasks 1-3.
  - CPA management page listing, sync state, provider sync, delete, userscript config copy: covered by Tasks 4-5.
  - Tampermonkey script sync and user-confirmed switch: covered by Task 6.
  - No automatic registration, no automatic reward claiming, no CPA workspace opening: enforced in boundaries and omitted from tasks.
- Placeholder scan:
  - No `TBD`, `TODO`, or open-ended "add tests" steps.
- Type consistency:
  - Backend JSON keys use kebab-case to match existing config/management style.
  - Frontend normalizes kebab-case backend fields to camelCase TypeScript fields.
  - Provider name defaults to `opencode-go` in backend and UI.
