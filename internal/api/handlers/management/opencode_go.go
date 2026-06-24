package management

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

const defaultOpenCodeGoProviderName = "opencode-go"
const defaultOpenCodeGoBaseURL = "https://opencode.ai/zen/go/v1"
const defaultOpenCodeGoSiteURL = "https://opencode.ai"
const openCodeGoProviderKeySourcePrefix = "opencode-go:"

var openCodeGoSiteURL = defaultOpenCodeGoSiteURL
var openCodeGoHTTPClient = http.DefaultClient

const openCodeGoBrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

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
	ID                 string                         `json:"id"`
	Alias              string                         `json:"alias,omitempty"`
	Email              string                         `json:"email,omitempty"`
	Username           string                         `json:"username,omitempty"`
	WorkspaceID        string                         `json:"workspace-id,omitempty"`
	APIKeyPreview      string                         `json:"api-key-preview,omitempty"`
	HasAPIKey          bool                           `json:"has-api-key"`
	HasCookie          bool                           `json:"has-cookie"`
	Usage              config.OpenCodeGoUsageSnapshot `json:"usage,omitempty"`
	ProviderName       string                         `json:"provider-name,omitempty"`
	BaseURL            string                         `json:"base-url,omitempty"`
	APIKeySynced       bool                           `json:"api-key-synced"`
	ProviderKeyManaged bool                           `json:"provider-key-managed"`
	ProviderSyncedAt   string                         `json:"provider-synced-at,omitempty"`
	ProviderSyncError  string                         `json:"provider-sync-error,omitempty"`
	CreatedAt          string                         `json:"created-at,omitempty"`
	UpdatedAt          string                         `json:"updated-at,omitempty"`
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
		"base-url":      openCodeGoBaseURL(h.cfg.OpenCodeGo),
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

	if h.cfg == nil {
		h.mu.Unlock()
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
			account.ProviderKeyManaged = false
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
		account.BaseURL = openCodeGoBaseURL(h.cfg.OpenCodeGo)
	}

	if idx >= 0 {
		h.cfg.OpenCodeGo.Accounts[idx] = account
	} else {
		h.cfg.OpenCodeGo.Accounts = append(h.cfg.OpenCodeGo.Accounts, account)
	}

	snapshot, ok := h.saveConfigAndSnapshotLocked(c)
	if !ok {
		h.mu.Unlock()
		return
	}
	view := openCodeGoAccountView(account, h.cfg.OpenCodeGo)
	h.mu.Unlock()
	h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
	c.JSON(http.StatusOK, gin.H{"account": view})
}

func (h *Handler) SyncOpenCodeGoProvider(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	now := time.Now().UTC().Format(time.RFC3339)

	h.mu.Lock()
	if h.cfg == nil {
		h.mu.Unlock()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}
	idx := findOpenCodeGoAccountIndex(h.cfg.OpenCodeGo.Accounts, id, "", "")
	if idx < 0 {
		h.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}

	account := &h.cfg.OpenCodeGo.Accounts[idx]
	apiKey := strings.TrimSpace(account.APIKey)
	if apiKey == "" {
		msg := "account api-key is empty"
		account.ProviderSyncError = msg
		account.UpdatedAt = now
		snapshot, ok := h.saveConfigAndSnapshotLocked(c)
		if !ok {
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()
		h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}

	providerName := openCodeGoProviderName(h.cfg.OpenCodeGo)
	if accountProviderName := strings.TrimSpace(account.ProviderName); accountProviderName != "" {
		providerName = accountProviderName
	}
	baseURL := strings.TrimSpace(account.BaseURL)
	if baseURL == "" {
		baseURL = openCodeGoBaseURL(h.cfg.OpenCodeGo)
	}
	if baseURL == "" {
		msg := "base-url is required before syncing provider"
		account.ProviderSyncError = msg
		account.UpdatedAt = now
		snapshot, ok := h.saveConfigAndSnapshotLocked(c)
		if !ok {
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()
		h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	if errConflict := validateOpenCodeGoProviderBaseURL(h.cfg, providerName, baseURL); errConflict != nil {
		msg := errConflict.Error()
		account.ProviderName = providerName
		account.BaseURL = baseURL
		account.ProviderSyncError = msg
		account.UpdatedAt = now
		snapshot, ok := h.saveConfigAndSnapshotLocked(c)
		if !ok {
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()
		h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}

	source := openCodeGoProviderKeySource(account.ID)
	h.mu.Unlock()

	models, errModels := fetchOpenCodeGoModels(c.Request.Context(), baseURL, apiKey)
	if errModels != nil {
		msg := errModels.Error()
		h.mu.Lock()
		if h.cfg == nil {
			h.mu.Unlock()
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
			return
		}
		idx = findOpenCodeGoAccountIndex(h.cfg.OpenCodeGo.Accounts, id, "", "")
		if idx < 0 {
			h.mu.Unlock()
			c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			return
		}
		h.cfg.OpenCodeGo.Accounts[idx].ProviderName = providerName
		h.cfg.OpenCodeGo.Accounts[idx].BaseURL = baseURL
		h.cfg.OpenCodeGo.Accounts[idx].ProviderSyncError = msg
		h.cfg.OpenCodeGo.Accounts[idx].UpdatedAt = now
		snapshot, ok := h.saveConfigAndSnapshotLocked(c)
		if !ok {
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()
		h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}

	h.mu.Lock()
	if h.cfg == nil {
		h.mu.Unlock()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}
	idx = findOpenCodeGoAccountIndex(h.cfg.OpenCodeGo.Accounts, id, "", "")
	if idx < 0 {
		h.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	account = &h.cfg.OpenCodeGo.Accounts[idx]
	managed, errSync := upsertOpenCodeGoProvider(h.cfg, providerName, baseURL, apiKey, source, models)
	if errSync != nil {
		msg := errSync.Error()
		account.ProviderName = providerName
		account.BaseURL = baseURL
		account.ProviderSyncError = msg
		account.UpdatedAt = now
		snapshot, ok := h.saveConfigAndSnapshotLocked(c)
		if !ok {
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()
		h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	account.ProviderName = providerName
	account.BaseURL = baseURL
	account.APIKeySynced = true
	account.ProviderKeyManaged = managed
	account.ProviderSyncedAt = now
	account.ProviderSyncError = ""
	account.UpdatedAt = now

	view := openCodeGoAccountView(*account, h.cfg.OpenCodeGo)
	snapshot, ok := h.saveConfigAndSnapshotLocked(c)
	if !ok {
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
	c.JSON(http.StatusOK, gin.H{"account": view})
}

func (h *Handler) RefreshOpenCodeGoUsage(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))

	h.mu.Lock()
	if h.cfg == nil {
		h.mu.Unlock()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}
	idx := findOpenCodeGoAccountIndex(h.cfg.OpenCodeGo.Accounts, id, "", "")
	if idx < 0 {
		h.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	account := h.cfg.OpenCodeGo.Accounts[idx]
	h.mu.Unlock()

	cookie := strings.TrimSpace(account.Cookie)
	if cookie == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account cookie is empty"})
		return
	}

	workspaceID, usage, err := fetchOpenCodeGoUsage(c.Request.Context(), cookie, account.WorkspaceID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	h.mu.Lock()
	if h.cfg == nil {
		h.mu.Unlock()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}
	idx = findOpenCodeGoAccountIndex(h.cfg.OpenCodeGo.Accounts, id, "", "")
	if idx < 0 {
		h.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	h.cfg.OpenCodeGo.Accounts[idx].WorkspaceID = workspaceID
	h.cfg.OpenCodeGo.Accounts[idx].Usage = usage
	h.cfg.OpenCodeGo.Accounts[idx].UpdatedAt = now
	view := openCodeGoAccountView(h.cfg.OpenCodeGo.Accounts[idx], h.cfg.OpenCodeGo)
	snapshot, ok := h.saveConfigAndSnapshotLocked(c)
	if !ok {
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
	c.JSON(http.StatusOK, gin.H{"account": view})
}

func fetchOpenCodeGoUsage(ctx context.Context, cookie, workspaceID string) (string, config.OpenCodeGoUsageSnapshot, error) {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return "", config.OpenCodeGoUsageSnapshot{}, fmt.Errorf("account cookie is empty")
	}

	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		discovered, err := discoverOpenCodeGoWorkspaceID(ctx, cookie)
		if err != nil {
			return "", config.OpenCodeGoUsageSnapshot{}, err
		}
		workspaceID = discovered
	}
	if workspaceID == "" {
		return "", config.OpenCodeGoUsageSnapshot{}, fmt.Errorf("opencode workspace id not found")
	}

	workspaceURL := openCodeGoSitePath("/workspace/" + url.PathEscape(workspaceID))
	goURL := openCodeGoSitePath("/workspace/" + url.PathEscape(workspaceID) + "/go")
	scriptURLs := make([]string, 0, 16)
	for _, pageURL := range []string{workspaceURL, goURL} {
		body, err := fetchOpenCodeGoText(ctx, pageURL, cookie, workspaceURL)
		if err != nil {
			return "", config.OpenCodeGoUsageSnapshot{}, err
		}
		scriptURLs = append(scriptURLs, extractOpenCodeGoScriptURLs(body, pageURL)...)
	}

	hash, err := findOpenCodeGoLiteSubscriptionHash(ctx, scriptURLs, cookie, goURL)
	if err != nil {
		return "", config.OpenCodeGoUsageSnapshot{}, err
	}

	args, err := json.Marshal([]string{workspaceID})
	if err != nil {
		return "", config.OpenCodeGoUsageSnapshot{}, err
	}
	serverURL := openCodeGoSitePath("/_server") + "?id=" + url.QueryEscape(hash) + "&args=" + url.QueryEscape(string(args))
	body, err := fetchOpenCodeGoTextWithHeaders(ctx, serverURL, cookie, goURL, map[string]string{
		"X-Server-Id":       hash,
		"X-Server-Instance": "server-fn:0",
	})
	if err != nil {
		return "", config.OpenCodeGoUsageSnapshot{}, err
	}

	usage, err := parseOpenCodeGoUsageSnapshot(body, time.Now().UTC())
	if err != nil {
		return "", config.OpenCodeGoUsageSnapshot{}, err
	}
	return workspaceID, usage, nil
}

func fetchOpenCodeGoModels(ctx context.Context, baseURL, apiKey string) ([]config.OpenAICompatibilityModel, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	if baseURL == "" {
		return nil, fmt.Errorf("base-url is required before syncing provider")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("account api-key is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", openCodeGoBrowserUserAgent)
	resp, err := openCodeGoHTTPClientOrDefault().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch opencode go models failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read opencode go models response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch opencode go models returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse opencode go models response: %w", err)
	}
	modelIDs := make([]string, 0, len(payload.Data)+len(payload.Models))
	for _, item := range payload.Data {
		modelIDs = append(modelIDs, item.ID)
	}
	modelIDs = append(modelIDs, payload.Models...)
	models := make([]config.OpenAICompatibilityModel, 0, len(modelIDs))
	seen := make(map[string]struct{}, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		if _, ok := seen[modelID]; ok {
			continue
		}
		seen[modelID] = struct{}{}
		models = append(models, config.OpenAICompatibilityModel{Name: modelID, Alias: modelID})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("opencode go model list is empty")
	}
	return models, nil
}

func discoverOpenCodeGoWorkspaceID(ctx context.Context, cookie string) (string, error) {
	authURL := openCodeGoSitePath("/auth")
	req, err := newOpenCodeGoRequest(ctx, authURL, cookie, "")
	if err != nil {
		return "", err
	}
	resp, err := openCodeGoHTTPClientOrDefault().Do(req)
	if err != nil {
		return "", fmt.Errorf("opencode auth request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("opencode auth returned HTTP %d", resp.StatusCode)
	}
	if resp.Request != nil && resp.Request.URL != nil {
		if workspaceID := extractOpenCodeGoWorkspaceID(resp.Request.URL.Path); workspaceID != "" {
			return workspaceID, nil
		}
	}
	if location := resp.Header.Get("Location"); location != "" {
		if workspaceID := extractOpenCodeGoWorkspaceID(location); workspaceID != "" {
			return workspaceID, nil
		}
	}
	return "", fmt.Errorf("opencode auth redirect did not include workspace id")
}

func fetchOpenCodeGoText(ctx context.Context, targetURL, cookie, referer string) (string, error) {
	return fetchOpenCodeGoTextWithHeaders(ctx, targetURL, cookie, referer, nil)
}

func fetchOpenCodeGoTextWithHeaders(ctx context.Context, targetURL, cookie, referer string, extraHeaders map[string]string) (string, error) {
	req, err := newOpenCodeGoRequest(ctx, targetURL, cookie, referer)
	if err != nil {
		return "", err
	}
	for key, value := range extraHeaders {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := openCodeGoHTTPClientOrDefault().Do(req)
	if err != nil {
		return "", fmt.Errorf("opencode request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read opencode response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("opencode returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

func newOpenCodeGoRequest(ctx context.Context, targetURL, cookie, referer string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", openCodeGoBrowserUserAgent)
	req.Header.Set("Accept", "*/*")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	return req, nil
}

func openCodeGoHTTPClientOrDefault() *http.Client {
	if openCodeGoHTTPClient != nil {
		return openCodeGoHTTPClient
	}
	return http.DefaultClient
}

func openCodeGoSitePath(path string) string {
	base := strings.TrimRight(strings.TrimSpace(openCodeGoSiteURL), "/")
	if base == "" {
		base = defaultOpenCodeGoSiteURL
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func extractOpenCodeGoWorkspaceID(value string) string {
	match := regexp.MustCompile(`/workspace/(wrk_[A-Za-z0-9]+)`).FindStringSubmatch(value)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func extractOpenCodeGoScriptURLs(pageHTML, pageURL string) []string {
	matches := regexp.MustCompile(`(?i)(?:src|href)=["']([^"']+\.js[^"']*)["']`).FindAllStringSubmatch(pageHTML, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	base, _ := url.Parse(pageURL)
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		ref, err := url.Parse(strings.TrimSpace(match[1]))
		if err != nil {
			continue
		}
		resolved := base.ResolveReference(ref).String()
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		out = append(out, resolved)
	}
	return out
}

func findOpenCodeGoLiteSubscriptionHash(ctx context.Context, scriptURLs []string, cookie, referer string) (string, error) {
	seen := make(map[string]struct{}, len(scriptURLs))
	for index := 0; index < len(scriptURLs) && index < 128; index++ {
		scriptURL := scriptURLs[index]
		scriptURL = strings.TrimSpace(scriptURL)
		if scriptURL == "" {
			continue
		}
		if _, ok := seen[scriptURL]; ok {
			continue
		}
		seen[scriptURL] = struct{}{}
		body, err := fetchOpenCodeGoText(ctx, scriptURL, cookie, referer)
		if err != nil {
			continue
		}
		if hash := extractOpenCodeGoLiteSubscriptionHash(body); hash != "" {
			return hash, nil
		}
		for _, dependencyURL := range extractOpenCodeGoJSDependencyURLs(body, scriptURL) {
			if _, ok := seen[dependencyURL]; !ok {
				scriptURLs = append(scriptURLs, dependencyURL)
			}
		}
	}
	return "", fmt.Errorf("opencode lite.subscription.get server function not found")
}

func extractOpenCodeGoJSDependencyURLs(script, scriptURL string) []string {
	matches := regexp.MustCompile(`["']((?:\.{1,2}/|/|_build/assets/)[^"']+\.js[^"']*)["']`).FindAllStringSubmatch(script, -1)
	if len(matches) == 0 {
		return nil
	}
	base, _ := url.Parse(scriptURL)
	out := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		rawRef := strings.TrimSpace(match[1])
		if strings.HasPrefix(rawRef, "_build/assets/") {
			rawRef = "/" + rawRef
		}
		ref, err := url.Parse(rawRef)
		if err != nil {
			continue
		}
		resolved := base.ResolveReference(ref).String()
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		out = append(out, resolved)
	}
	return out
}

func extractOpenCodeGoLiteSubscriptionHash(script string) string {
	refPattern := regexp.MustCompile(`(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*createServerReference\("([0-9a-f]{64})"\)`)
	matches := refPattern.FindAllStringSubmatch(script, -1)
	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		queryPattern := regexp.MustCompile(`query\(\s*` + regexp.QuoteMeta(match[1]) + `\s*,\s*"lite\.subscription\.get"\s*\)`)
		if queryPattern.MatchString(script) {
			return match[2]
		}
	}
	return ""
}

func parseOpenCodeGoUsageSnapshot(body string, now time.Time) (config.OpenCodeGoUsageSnapshot, error) {
	rolling, okRolling := parseOpenCodeGoUsageWindow(body, "rollingUsage", now)
	weekly, okWeekly := parseOpenCodeGoUsageWindow(body, "weeklyUsage", now)
	monthly, okMonthly := parseOpenCodeGoUsageWindow(body, "monthlyUsage", now)
	if !okRolling && !okWeekly && !okMonthly {
		return config.OpenCodeGoUsageSnapshot{}, fmt.Errorf("opencode usage data not found")
	}
	return config.OpenCodeGoUsageSnapshot{
		Rolling: rolling,
		Weekly:  weekly,
		Monthly: monthly,
	}, nil
}

func parseOpenCodeGoUsageWindow(body, name string, now time.Time) (config.OpenCodeGoUsageWindow, bool) {
	object := ""
	inlinePattern := regexp.MustCompile(regexp.QuoteMeta(name) + `\s*:\s*(?:\$R\[(\d+)\]\s*=\s*)?(\{[^}]*\}|\$R\[(\d+)\])`)
	match := inlinePattern.FindStringSubmatch(body)
	if len(match) == 0 {
		return config.OpenCodeGoUsageWindow{}, false
	}
	if strings.HasPrefix(match[2], "{") {
		object = match[2]
	} else {
		ref := match[1]
		if ref == "" {
			ref = match[3]
		}
		refPattern := regexp.MustCompile(`\$R\[` + regexp.QuoteMeta(ref) + `\]\s*=\s*(\{[^}]*\})`)
		refMatch := refPattern.FindStringSubmatch(body)
		if len(refMatch) == 2 {
			object = refMatch[1]
		}
	}
	if object == "" {
		return config.OpenCodeGoUsageWindow{}, false
	}

	usagePercent, ok := parseOpenCodeGoObjectNumber(object, "usagePercent")
	if !ok {
		return config.OpenCodeGoUsageWindow{}, false
	}
	window := config.OpenCodeGoUsageWindow{
		Used:  usagePercent,
		Limit: 100,
	}
	if resetInSec, ok := parseOpenCodeGoObjectNumber(object, "resetInSec"); ok && resetInSec >= 0 {
		window.ResetAt = now.Add(time.Duration(resetInSec) * time.Second).UTC().Format(time.RFC3339)
	}
	return window, true
}

func parseOpenCodeGoObjectNumber(object, key string) (float64, bool) {
	pattern := regexp.MustCompile(regexp.QuoteMeta(key) + `\s*:\s*([0-9]+(?:\.[0-9]+)?)`)
	match := pattern.FindStringSubmatch(object)
	if len(match) != 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func (h *Handler) DeleteOpenCodeGoAccount(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	removeProviderKey := strings.EqualFold(c.Query("remove-provider-key"), "true")

	h.mu.Lock()
	if h.cfg == nil {
		h.mu.Unlock()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}
	idx := findOpenCodeGoAccountIndex(h.cfg.OpenCodeGo.Accounts, id, "", "")
	if idx < 0 {
		h.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}

	account := h.cfg.OpenCodeGo.Accounts[idx]
	h.cfg.OpenCodeGo.Accounts = append(h.cfg.OpenCodeGo.Accounts[:idx], h.cfg.OpenCodeGo.Accounts[idx+1:]...)
	if removeProviderKey {
		providerName := strings.TrimSpace(account.ProviderName)
		if providerName == "" {
			providerName = openCodeGoProviderName(h.cfg.OpenCodeGo)
		}
		baseURL := strings.TrimSpace(account.BaseURL)
		if baseURL == "" {
			baseURL = openCodeGoBaseURL(h.cfg.OpenCodeGo)
		}
		if account.ProviderKeyManaged {
			removeOpenCodeGoProviderKey(h.cfg, providerName, baseURL, account.APIKey, openCodeGoProviderKeySource(account.ID))
		}
	}

	snapshot, ok := h.saveConfigAndSnapshotLocked(c)
	if !ok {
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
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

func (h *Handler) GetOpenCodeGoUserscriptConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"name":            "opencode go账号助手",
		"match":           "https://opencode.ai/*",
		"management-base": "/v0/management",
		"endpoints": gin.H{
			"accounts":      "/opencode-go/accounts",
			"sync":          "/opencode-go/sync",
			"sync-provider": "/opencode-go/accounts/{id}/sync-provider",
			"delete":        "/opencode-go/accounts/{id}",
			"switch-cookie": "/opencode-go/accounts/{id}/switch-cookie",
		},
	})
}

func openCodeGoProviderName(cfg config.OpenCodeGoConfig) string {
	name := strings.TrimSpace(cfg.ProviderName)
	if name == "" {
		return defaultOpenCodeGoProviderName
	}
	return strings.ToLower(name)
}

func openCodeGoBaseURL(cfg config.OpenCodeGoConfig) string {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		return defaultOpenCodeGoBaseURL
	}
	return strings.TrimRight(baseURL, "/")
}

func openCodeGoAccountView(account config.OpenCodeGoAccount, cfg config.OpenCodeGoConfig) openCodeGoAccountResponse {
	providerName := strings.TrimSpace(account.ProviderName)
	if providerName == "" {
		providerName = openCodeGoProviderName(cfg)
	}
	baseURL := strings.TrimSpace(account.BaseURL)
	if baseURL == "" {
		baseURL = openCodeGoBaseURL(cfg)
	}
	return openCodeGoAccountResponse{
		ID:                 account.ID,
		Alias:              account.Alias,
		Email:              account.Email,
		Username:           account.Username,
		WorkspaceID:        account.WorkspaceID,
		APIKeyPreview:      maskOpenCodeGoSecret(account.APIKey),
		HasAPIKey:          strings.TrimSpace(account.APIKey) != "",
		HasCookie:          strings.TrimSpace(account.Cookie) != "",
		Usage:              account.Usage,
		ProviderName:       providerName,
		BaseURL:            baseURL,
		APIKeySynced:       account.APIKeySynced,
		ProviderKeyManaged: account.ProviderKeyManaged,
		ProviderSyncedAt:   account.ProviderSyncedAt,
		ProviderSyncError:  account.ProviderSyncError,
		CreatedAt:          account.CreatedAt,
		UpdatedAt:          account.UpdatedAt,
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

func openCodeGoProviderKeySource(accountID string) string {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return openCodeGoProviderKeySourcePrefix + "unknown"
	}
	return openCodeGoProviderKeySourcePrefix + accountID
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

func upsertOpenCodeGoProvider(cfg *config.Config, providerName, baseURL, apiKey, source string, models []config.OpenAICompatibilityModel) (bool, error) {
	if cfg == nil {
		return false, nil
	}
	providerName = strings.TrimSpace(providerName)
	baseURL = strings.TrimSpace(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	source = strings.TrimSpace(source)
	if providerName == "" || baseURL == "" || apiKey == "" {
		return false, nil
	}
	for i := range cfg.OpenAICompatibility {
		provider := &cfg.OpenAICompatibility[i]
		if !strings.EqualFold(strings.TrimSpace(provider.Name), providerName) {
			continue
		}
		if strings.TrimSpace(provider.BaseURL) != baseURL {
			continue
		}
		provider.Models = cloneOpenCodeGoProviderModels(models)
		for j := range provider.APIKeyEntries {
			if strings.TrimSpace(provider.APIKeyEntries[j].APIKey) == apiKey {
				return strings.TrimSpace(provider.APIKeyEntries[j].Source) == source, nil
			}
		}
		provider.APIKeyEntries = append(provider.APIKeyEntries, config.OpenAICompatibilityAPIKey{APIKey: apiKey, Source: source})
		return true, nil
	}
	cfg.OpenAICompatibility = append(cfg.OpenAICompatibility, config.OpenAICompatibility{
		Name:          providerName,
		BaseURL:       baseURL,
		Models:        cloneOpenCodeGoProviderModels(models),
		APIKeyEntries: []config.OpenAICompatibilityAPIKey{{APIKey: apiKey, Source: source}},
	})
	return true, nil
}

func cloneOpenCodeGoProviderModels(models []config.OpenAICompatibilityModel) []config.OpenAICompatibilityModel {
	if len(models) == 0 {
		return nil
	}
	out := make([]config.OpenAICompatibilityModel, len(models))
	copy(out, models)
	return out
}

func validateOpenCodeGoProviderBaseURL(cfg *config.Config, providerName, baseURL string) error {
	if cfg == nil {
		return nil
	}
	providerName = strings.TrimSpace(providerName)
	baseURL = strings.TrimSpace(baseURL)
	for i := range cfg.OpenAICompatibility {
		provider := &cfg.OpenAICompatibility[i]
		if !strings.EqualFold(strings.TrimSpace(provider.Name), providerName) {
			continue
		}
		if strings.TrimSpace(provider.BaseURL) != baseURL {
			return fmt.Errorf("provider %q already exists with a different base-url; use a unique provider-name or align opencode-go base-url", providerName)
		}
	}
	return nil
}

func removeOpenCodeGoProviderKey(cfg *config.Config, providerName, baseURL, apiKey, source string) {
	if cfg == nil {
		return
	}
	providerName = strings.TrimSpace(providerName)
	baseURL = strings.TrimSpace(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	source = strings.TrimSpace(source)
	if providerName == "" || baseURL == "" || apiKey == "" || source == "" {
		return
	}
	for i := range cfg.OpenAICompatibility {
		provider := &cfg.OpenAICompatibility[i]
		if !strings.EqualFold(strings.TrimSpace(provider.Name), providerName) {
			continue
		}
		if strings.TrimSpace(provider.BaseURL) != baseURL {
			continue
		}
		filtered := provider.APIKeyEntries[:0]
		for _, entry := range provider.APIKeyEntries {
			if strings.TrimSpace(entry.APIKey) != apiKey || strings.TrimSpace(entry.ProxyURL) != "" || strings.TrimSpace(entry.Source) != source {
				filtered = append(filtered, entry)
			}
		}
		provider.APIKeyEntries = filtered
		return
	}
}
