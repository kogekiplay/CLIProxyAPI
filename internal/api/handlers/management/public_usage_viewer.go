package management

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/usageledger"
)

const (
	publicUsageAnalyticsMaxWindow = 60 * 24 * time.Hour
	publicUsageAnalyticsMaxEvents = 100
	publicUsageAnalyticsMaxStats  = 200
	publicUsageAnalyticsMaxBody   = 64 << 10
	publicUsageAnalyticsMaxFilter = 32
	publicUsageAnalyticsMaxValue  = 256
)

// GetPublicUsageViewer reports whether the redacted usage viewer is enabled.
func (h *Handler) GetPublicUsageViewer(c *gin.Context) {
	if !h.publicUsageViewerEnabled() {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	h.setPublicUsageViewerHeaders(c)
	c.JSON(http.StatusOK, gin.H{"enabled": true})
}

// PostPublicUsageAnalytics returns a bounded and redacted analytics response without management authentication.
func (h *Handler) PostPublicUsageAnalytics(c *gin.Context) {
	if !h.publicUsageViewerEnabled() {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	h.setPublicUsageViewerHeaders(c)
	if c.Request.Body != nil {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, publicUsageAnalyticsMaxBody)
	}
	h.postUsageAnalytics(c, true)
}

func (h *Handler) publicUsageViewerEnabled() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg != nil && h.cfg.RemoteManagement.PublicUsageViewer
}

func (h *Handler) setPublicUsageViewerHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-CPA-VERSION", buildinfo.Version)
	c.Header("X-CPA-COMMIT", buildinfo.Commit)
	c.Header("X-CPA-BUILD-DATE", buildinfo.BuildDate)
	c.Header("X-CPA-SUPPORT-PLUGIN", pluginhost.SupportPluginHeaderValue())
}

func normalizePublicUsageAnalyticsRequest(req *usageledger.AnalyticsRequest) {
	if req == nil {
		return
	}
	nowMS := time.Now().UnixMilli()
	if req.ToMS > nowMS {
		req.ToMS = nowMS
	}
	minFromMS := req.ToMS - publicUsageAnalyticsMaxWindow.Milliseconds()
	if req.FromMS < minFromMS {
		req.FromMS = minFromMS
	}
	req.Filters.Providers = normalizePublicUsageAnalyticsValues(req.Filters.Providers)
	req.Filters.Models = normalizePublicUsageAnalyticsValues(req.Filters.Models)
	req.Filters.AuthFiles = normalizePublicUsageAnalyticsValues(req.Filters.AuthFiles)
	req.Filters.AuthIndices = normalizePublicUsageAnalyticsValues(req.Filters.AuthIndices)
	req.Filters.APIKeyHashes = normalizePublicUsageAnalyticsValues(req.Filters.APIKeyHashes)
	req.Filters.Accounts = normalizePublicUsageAnalyticsValues(req.Filters.Accounts)
	if page := req.Include.EventsPage; page != nil {
		if page.Limit <= 0 || page.Limit > publicUsageAnalyticsMaxEvents {
			page.Limit = publicUsageAnalyticsMaxEvents
		}
		includeTotalCount := false
		page.IncludeTotalCount = &includeTotalCount
	}
}

func normalizePublicUsageAnalyticsValues(values []string) []string {
	if len(values) > publicUsageAnalyticsMaxFilter {
		values = values[:publicUsageAnalyticsMaxFilter]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		runes := []rune(value)
		if len(runes) > publicUsageAnalyticsMaxValue {
			value = string(runes[:publicUsageAnalyticsMaxValue])
		}
		out = append(out, value)
	}
	return out
}

func redactPublicUsageAnalytics(resp *usageledger.AnalyticsResponse) {
	if resp == nil {
		return
	}
	for i := range resp.APIKeyStats {
		resp.APIKeyStats[i].AccountRef = ""
	}
	if len(resp.APIKeyStats) > publicUsageAnalyticsMaxStats {
		resp.APIKeyStats = resp.APIKeyStats[:publicUsageAnalyticsMaxStats]
	}
	for i := range resp.CredentialStats {
		row := &resp.CredentialStats[i]
		row.CredentialDisplayName = redactCredentialDisplayName(
			row.CredentialDisplayName,
			row.AuthFileName,
		)
		row.AuthFileName = ""
		row.AccountRef = ""
	}
	if len(resp.CredentialStats) > publicUsageAnalyticsMaxStats {
		resp.CredentialStats = resp.CredentialStats[:publicUsageAnalyticsMaxStats]
	}
	if len(resp.ModelStats) > publicUsageAnalyticsMaxStats {
		resp.ModelStats = resp.ModelStats[:publicUsageAnalyticsMaxStats]
	}
	if resp.Events == nil {
		return
	}
	for i := range resp.Events.Items {
		row := &resp.Events.Items[i]
		row.CredentialDisplayName = redactCredentialDisplayName(
			row.CredentialDisplayName,
			row.AuthFileName,
		)
		row.RequestID = ""
		row.AuthFileName = ""
		row.CredentialKeyHash = ""
		row.AccountRef = ""
		row.FailSummary = usageledger.SanitizeFailureText(row.FailSummary)
		row.FailBody = ""
	}
}

func redactCredentialDisplayName(displayName, authFileName string) string {
	displayName = strings.TrimSpace(displayName)
	authFileName = strings.TrimSpace(authFileName)
	if displayName == "" || authFileName == "" {
		return displayName
	}
	if displayName == authFileName || displayName == filepath.Base(authFileName) {
		return ""
	}
	return displayName
}
