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
	ID          string                          `json:"id"`
	Alias       string                          `json:"alias"`
	Email       string                          `json:"email"`
	Username    string                          `json:"username"`
	WorkspaceID string                          `json:"workspace-id"`
	APIKey      string                          `json:"api-key"`
	Cookie      string                          `json:"cookie"`
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
	LastSyncedAt      string                         `json:"last-synced-at,omitempty"`
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

	req.ID = strings.TrimSpace(req.ID)
	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	req.APIKey = strings.TrimSpace(req.APIKey)
	if req.ID == "" && req.WorkspaceID == "" && req.APIKey == "" {
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
		if account.APIKey != req.APIKey {
			account.APIKeySynced = false
			account.ProviderSyncedAt = ""
			account.ProviderSyncError = ""
		}
		account.APIKey = req.APIKey
	}
	if cookie := strings.TrimSpace(req.Cookie); cookie != "" {
		account.Cookie = cookie
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
		LastSyncedAt:      account.LastSyncedAt,
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
