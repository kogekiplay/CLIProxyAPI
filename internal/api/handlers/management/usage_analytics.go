package management

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/usageledger"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// PostUsageAnalytics returns aggregated request monitoring data from the usage ledger.
func (h *Handler) PostUsageAnalytics(c *gin.Context) {
	h.postUsageAnalytics(c, false)
}

func (h *Handler) postUsageAnalytics(c *gin.Context, public bool) {
	store, ok := h.requireUsageLedger(c)
	if !ok {
		return
	}
	var req usageledger.AnalyticsRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid analytics request"})
		return
	}
	if public {
		normalizePublicUsageAnalyticsRequest(&req)
	}
	req.ModelAliases = h.usageAnalyticsModelAliases()
	resp, err := store.Analytics(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.reclassifyUsageAnalyticsCredentialAPIKeys(&resp)
	h.enrichUsageAnalyticsAPIKeyStats(&resp)
	h.enrichUsageAnalyticsCredentialNames(&resp)
	if public {
		redactPublicUsageAnalytics(&resp)
	}
	c.JSON(http.StatusOK, resp)
}

type usageAnalyticsAPIKeyInfo struct {
	Hash    string
	Preview string
}

func (h *Handler) reclassifyUsageAnalyticsCredentialAPIKeys(resp *usageledger.AnalyticsResponse) {
	if h == nil || resp == nil || len(resp.CredentialStats) == 0 {
		return
	}
	infos := h.usageAnalyticsAPIKeyInfos()
	if len(infos) == 0 {
		return
	}

	nextCredentialStats := make([]usageledger.AnalyticsCredentialStat, 0, len(resp.CredentialStats))
	for _, row := range resp.CredentialStats {
		info, ok := usageAnalyticsAPIKeyInfoForCredential(row, infos)
		if !ok {
			nextCredentialStats = append(nextCredentialStats, row)
			continue
		}
		apiKeyHash := info.Hash
		if apiKeyHash == "" {
			apiKeyHash = strings.TrimSpace(row.AuthIndex)
		}
		if apiKeyHash == "" {
			apiKeyHash = strings.TrimSpace(row.AccountRef)
		}
		if apiKeyHash == "" {
			nextCredentialStats = append(nextCredentialStats, row)
			continue
		}
		stat := usageledger.AnalyticsAPIKeyStat{
			Provider:            row.Provider,
			Providers:           []string{row.Provider},
			APIKeyHash:          apiKeyHash,
			APIKeyPreview:       info.Preview,
			AccountRef:          row.AccountRef,
			Calls:               row.Calls,
			SuccessCalls:        row.SuccessCalls,
			FailureCalls:        row.FailureCalls,
			InputTokens:         row.InputTokens,
			OutputTokens:        row.OutputTokens,
			ReasoningTokens:     row.ReasoningTokens,
			CachedTokens:        row.CachedTokens,
			CacheReadTokens:     row.CacheReadTokens,
			CacheCreationTokens: row.CacheCreationTokens,
			TotalTokens:         row.TotalTokens,
			Cost:                row.Cost,
		}
		resp.APIKeyStats = mergeUsageAnalyticsAPIKeyStat(resp.APIKeyStats, stat)
	}
	resp.CredentialStats = nextCredentialStats
}

func usageAnalyticsAPIKeyInfoForCredential(row usageledger.AnalyticsCredentialStat, infos map[string]usageAnalyticsAPIKeyInfo) (usageAnalyticsAPIKeyInfo, bool) {
	for _, key := range []string{row.AuthIndex, row.AuthFileName, filepath.Base(row.AuthFileName), row.AccountRef} {
		key = strings.TrimSpace(key)
		if key == "" || key == "." {
			continue
		}
		if info, ok := infos[key]; ok {
			return info, true
		}
	}
	return usageAnalyticsAPIKeyInfo{}, false
}

func mergeUsageAnalyticsAPIKeyStat(rows []usageledger.AnalyticsAPIKeyStat, stat usageledger.AnalyticsAPIKeyStat) []usageledger.AnalyticsAPIKeyStat {
	for i := range rows {
		if strings.TrimSpace(rows[i].APIKeyHash) != strings.TrimSpace(stat.APIKeyHash) {
			continue
		}
		rows[i].Calls += stat.Calls
		rows[i].SuccessCalls += stat.SuccessCalls
		rows[i].FailureCalls += stat.FailureCalls
		rows[i].InputTokens += stat.InputTokens
		rows[i].OutputTokens += stat.OutputTokens
		rows[i].ReasoningTokens += stat.ReasoningTokens
		rows[i].CachedTokens += stat.CachedTokens
		rows[i].CacheReadTokens += stat.CacheReadTokens
		rows[i].CacheCreationTokens += stat.CacheCreationTokens
		rows[i].TotalTokens += stat.TotalTokens
		if rows[i].Provider == "" {
			rows[i].Provider = stat.Provider
		}
		rows[i].Providers = appendUsageAnalyticsProvider(rows[i].Providers, stat.Provider)
		if rows[i].APIKeyPreview == "" {
			rows[i].APIKeyPreview = stat.APIKeyPreview
		}
		if rows[i].AccountRef == "" {
			rows[i].AccountRef = stat.AccountRef
		}
		if rows[i].Cost != nil && stat.Cost != nil {
			mergedCost := *rows[i].Cost + *stat.Cost
			rows[i].Cost = &mergedCost
		} else if rows[i].Calls == stat.Calls {
			rows[i].Cost = stat.Cost
		} else {
			rows[i].Cost = nil
		}
		return rows
	}
	stat.Providers = appendUsageAnalyticsProvider(nil, stat.Provider)
	return append(rows, stat)
}

func appendUsageAnalyticsProvider(providers []string, provider string) []string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return providers
	}
	for _, existing := range providers {
		if existing == provider {
			return providers
		}
	}
	return append(providers, provider)
}

func (h *Handler) enrichUsageAnalyticsAPIKeyStats(resp *usageledger.AnalyticsResponse) {
	if h == nil || resp == nil || len(resp.APIKeyStats) == 0 {
		return
	}
	labels := h.usageAnalyticsAPIKeyLabels()
	if len(labels) == 0 {
		return
	}
	for i := range resp.APIKeyStats {
		row := &resp.APIKeyStats[i]
		for _, key := range []string{row.APIKeyHash, row.AccountRef} {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			if label := labels[key]; label != "" {
				row.APIKeyPreview = label
				break
			}
		}
	}
}

func (h *Handler) usageAnalyticsAPIKeyLabels() map[string]string {
	infos := h.usageAnalyticsAPIKeyInfos()
	out := make(map[string]string)
	for key, info := range infos {
		if info.Preview != "" {
			out[key] = info.Preview
		}
	}
	return out
}

func (h *Handler) usageAnalyticsAPIKeyInfos() map[string]usageAnalyticsAPIKeyInfo {
	out := make(map[string]usageAnalyticsAPIKeyInfo)
	add := func(keys []string, label string) {
		label = strings.TrimSpace(label)
		if label == "" {
			return
		}
		for _, key := range keys {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			info := out[key]
			info.Preview = label
			out[key] = info
		}
	}
	addHash := func(keys []string, hash string) {
		hash = strings.TrimSpace(hash)
		if hash == "" {
			return
		}
		for _, key := range keys {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			info := out[key]
			info.Hash = hash
			out[key] = info
		}
	}

	h.mu.Lock()
	manager := h.authManager
	cfg := h.cfg
	h.mu.Unlock()

	if manager != nil {
		for _, auth := range manager.List() {
			if auth == nil || auth.AuthKind() != coreauth.AuthKindAPIKey {
				continue
			}
			_, apiKey := auth.AccountInfo()
			apiKey = strings.TrimSpace(apiKey)
			if apiKey == "" {
				continue
			}
			keyHash := usageledger.HashAPIKey(apiKey)
			keys := []string{auth.EnsureIndex(), auth.ID, usageledger.HashAPIKey(apiKey)}
			if auth.Attributes != nil {
				keys = append(keys,
					auth.Attributes["usage_source"],
					auth.Attributes[coreauth.AttributeSource],
				)
			}
			add(keys, maskOpenCodeGoSecret(apiKey))
			addHash(keys, keyHash)
		}
	}

	if cfg != nil {
		for _, account := range cfg.OpenCodeGo.Accounts {
			apiKey := strings.TrimSpace(account.APIKey)
			if apiKey == "" {
				continue
			}
			add([]string{
				openCodeGoProviderKeySource(account.ID),
				usageledger.HashAPIKey(apiKey),
			}, maskOpenCodeGoSecret(apiKey))
			addHash([]string{
				openCodeGoProviderKeySource(account.ID),
				usageledger.HashAPIKey(apiKey),
			}, usageledger.HashAPIKey(apiKey))
		}
	}

	return out
}

func (h *Handler) enrichUsageAnalyticsCredentialNames(resp *usageledger.AnalyticsResponse) {
	if h == nil || resp == nil {
		return
	}
	if len(resp.CredentialStats) == 0 && (resp.Events == nil || len(resp.Events.Items) == 0) {
		return
	}
	labels := h.usageAnalyticsCredentialLabels()
	if len(labels) == 0 {
		return
	}
	resolve := func(authIndex, authFileName, accountRef string) string {
		for _, key := range []string{authIndex, authFileName, filepath.Base(authFileName), accountRef} {
			key = strings.TrimSpace(key)
			if key == "" || key == "." {
				continue
			}
			if label := labels[key]; label != "" {
				return label
			}
		}
		return ""
	}
	for i := range resp.CredentialStats {
		row := &resp.CredentialStats[i]
		row.CredentialDisplayName = resolve(row.AuthIndex, row.AuthFileName, row.AccountRef)
	}
	if resp.Events != nil {
		for i := range resp.Events.Items {
			row := &resp.Events.Items[i]
			row.CredentialDisplayName = resolve(row.AuthIndex, row.AuthFileName, row.AccountRef)
		}
	}
}

func (h *Handler) usageAnalyticsCredentialLabels() map[string]string {
	out := make(map[string]string)
	add := func(keys []string, label string) {
		label = strings.TrimSpace(label)
		if label == "" {
			return
		}
		for _, key := range keys {
			key = strings.TrimSpace(key)
			if key == "" || key == "." {
				continue
			}
			out[key] = label
		}
	}

	h.mu.Lock()
	manager := h.authManager
	h.mu.Unlock()
	if manager == nil {
		return out
	}

	for _, auth := range manager.List() {
		if auth == nil {
			continue
		}
		label := usageAnalyticsCredentialDisplayName(auth)
		if label == "" {
			continue
		}
		keys := []string{auth.EnsureIndex(), auth.ID, auth.FileName, filepath.Base(auth.FileName)}
		if _, account := auth.AccountInfo(); strings.TrimSpace(account) != "" && auth.AuthKind() != coreauth.AuthKindAPIKey {
			keys = append(keys, account)
		}
		if auth.Metadata != nil {
			for _, key := range []string{"email", "account"} {
				if value, ok := auth.Metadata[key].(string); ok {
					keys = append(keys, value)
				}
			}
		}
		add(keys, label)
	}
	return out
}

func usageAnalyticsCredentialDisplayName(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.AuthKind() != coreauth.AuthKindAPIKey {
		if _, account := auth.AccountInfo(); strings.TrimSpace(account) != "" {
			return strings.TrimSpace(account)
		}
	}
	if email := authEmail(auth); email != "" {
		return email
	}
	if label := strings.TrimSpace(auth.Label); label != "" {
		if auth.AuthKind() == coreauth.AuthKindAPIKey && isGenericUsageAnalyticsAPIKeyLabel(auth.Provider, label) {
			if apiKeyLabel := usageAnalyticsProviderAPIKeyLabel(auth); apiKeyLabel != "" {
				return apiKeyLabel
			}
		}
		return label
	}
	if auth.AuthKind() == coreauth.AuthKindAPIKey {
		if apiKeyLabel := usageAnalyticsProviderAPIKeyLabel(auth); apiKeyLabel != "" {
			return apiKeyLabel
		}
	}
	if name := strings.TrimSpace(auth.FileName); name != "" {
		return filepath.Base(name)
	}
	return strings.TrimSpace(auth.ID)
}

func isGenericUsageAnalyticsAPIKeyLabel(provider, label string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	label = strings.ToLower(strings.TrimSpace(label))
	if provider == "" || label == "" {
		return false
	}
	return label == provider+"-apikey" || label == provider+"-api-key"
}

func usageAnalyticsProviderAPIKeyLabel(auth *coreauth.Auth) string {
	if auth == nil || auth.AuthKind() != coreauth.AuthKindAPIKey {
		return ""
	}
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	if provider == "" {
		provider = "api-key"
	}
	_, apiKey := auth.AccountInfo()
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" && auth.Attributes != nil {
		apiKey = strings.TrimSpace(auth.Attributes[coreauth.AttributeAPIKey])
	}
	if apiKey == "" {
		return ""
	}
	return provider + "-" + maskOpenCodeGoSecret(apiKey)
}
