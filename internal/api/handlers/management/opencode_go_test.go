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
