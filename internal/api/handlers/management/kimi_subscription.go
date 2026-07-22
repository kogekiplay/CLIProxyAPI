package management

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const kimiSubscriptionStatsURL = "https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats"

// kimiSubscriptionStatsRequest is the request body for the management endpoint.
type kimiSubscriptionStatsRequest struct {
	AuthIndexSnake  *string `json:"auth_index"`
	AuthIndexCamel  *string `json:"authIndex"`
	AuthIndexPascal *string `json:"AuthIndex"`
}

// KimiSubscriptionStatsResponse is the normalized response returned to callers.
type KimiSubscriptionStatsResponse struct {
	TotalUsagePercent   *float64 `json:"total_usage_percent,omitempty"`
	TotalResetAt        *string  `json:"total_reset_at,omitempty"`
	FiveHourCodePercent *float64 `json:"five_hour_code_percent,omitempty"`
	FiveHourResetAt     *string  `json:"five_hour_reset_at,omitempty"`
	WeeklyCodePercent   *float64 `json:"weekly_code_percent,omitempty"`
	WeeklyResetAt       *string  `json:"weekly_reset_at,omitempty"`
	Error               string   `json:"error,omitempty"`
}

// kimConnectRateLimit mirrors the rate-limit object returned by Kimi's Connect-RPC endpoint.
type kimiConnectRateLimit struct {
	Ratio     float64 `json:"ratio"`
	Enabled   bool    `json:"enabled"`
	ResetTime string  `json:"resetTime"`
}

// kimiConnectSubscriptionBalance mirrors the subscription-balance object returned by Kimi's Connect-RPC endpoint.
type kimiConnectSubscriptionBalance struct {
	ID              string  `json:"id"`
	Feature         string  `json:"feature"`
	Type            string  `json:"type"`
	Unit            string  `json:"unit"`
	AmountUsedRatio float64 `json:"amountUsedRatio"`
	KimiCodeUsedRatio float64 `json:"kimiCodeUsedRatio"`
	ExpireTime      string  `json:"expireTime"`
	Domain          string  `json:"domain"`
}

// kimiConnectSubscriptionStats mirrors the raw JSON body returned by Kimi's Connect-RPC endpoint.
type kimiConnectSubscriptionStats struct {
	RateLimitCode5h     *kimiConnectRateLimit            `json:"ratelimitCode5h,omitempty"`
	RateLimitCode7d     *kimiConnectRateLimit            `json:"ratelimitCode7d,omitempty"`
	SubscriptionBalance *kimiConnectSubscriptionBalance  `json:"subscriptionBalance,omitempty"`
	BoosterWallets      []json.RawMessage                `json:"boosterWallets,omitempty"`
}

// KimiSubscriptionStats proxies Kimi's web-only GetSubscriptionStats Connect-RPC
// endpoint using the web_access_token stored in the auth file metadata.
//
// Endpoint:
//
//	POST /v0/management/kimi-subscription-stats
//
// Request JSON:
//   - auth_index / authIndex / AuthIndex (required): the credential auth_index.
//
// Response JSON:
//   - total_usage_percent: monthly subscription usage ratio (0-100).
//   - total_reset_at: ISO timestamp when the monthly quota expires.
//   - five_hour_code_percent: 5-hour Code window usage ratio (0-100).
//   - five_hour_reset_at: ISO timestamp when the 5-hour window resets.
//   - weekly_code_percent: 7-day Code window usage ratio (0-100).
//   - weekly_reset_at: ISO timestamp when the 7-day window resets.
func (h *Handler) KimiSubscriptionStats(c *gin.Context) {
	var body kimiSubscriptionStatsRequest
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	authIndex := firstNonEmptyString(body.AuthIndexSnake, body.AuthIndexCamel, body.AuthIndexPascal)
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing auth_index"})
		return
	}

	auth := h.authByIndex(authIndex)
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}

	webToken := ""
	if auth.Metadata != nil {
		if v, ok := auth.Metadata["web_access_token"].(string); ok {
			webToken = strings.TrimSpace(v)
		}
	}
	if webToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "web_access_token not found in auth metadata"})
		return
	}

	resp, err := h.callKimiSubscriptionStats(c.Request.Context(), webToken, auth)
	if err != nil {
		log.WithError(err).Debug("kimi subscription stats request failed")
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *Handler) callKimiSubscriptionStats(ctx context.Context, webToken string, auth *coreauth.Auth) (*KimiSubscriptionStatsResponse, error) {
	// Kimi exposes GetSubscriptionStats as a Connect-RPC JSON endpoint.
	// An empty JSON object is sufficient because the auth token carries the user identity.
	reqBody := []byte("{}")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, kimiSubscriptionStatsURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+webToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("connect-protocol-version", "1")
	req.Header.Set("Origin", "https://www.kimi.com")
	req.Header.Set("Referer", "https://www.kimi.com/membership/subscription?tab=quota")

	httpClient := &http.Client{Timeout: defaultAPICallTimeout}
	httpClient.Transport = h.apiCallTransport(auth)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("kimi subscription stats response body close error: %v", errClose)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("kimi subscription stats http error: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	stats, err := parseKimiSubscriptionStatsResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kimi subscription stats: %w", err)
	}
	return stats, nil
}

func parseKimiSubscriptionStatsResponse(data []byte) (*KimiSubscriptionStatsResponse, error) {
	var raw kimiConnectSubscriptionStats
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	result := &KimiSubscriptionStatsResponse{}

	if raw.RateLimitCode5h != nil {
		p := normalizePercent(raw.RateLimitCode5h.Ratio)
		result.FiveHourCodePercent = &p
		if raw.RateLimitCode5h.ResetTime != "" {
			ts, err := parseKimiConnectTimestamp(raw.RateLimitCode5h.ResetTime)
			if err == nil {
				result.FiveHourResetAt = &ts
			}
		}
	}

	if raw.RateLimitCode7d != nil {
		p := normalizePercent(raw.RateLimitCode7d.Ratio)
		result.WeeklyCodePercent = &p
		if raw.RateLimitCode7d.ResetTime != "" {
			ts, err := parseKimiConnectTimestamp(raw.RateLimitCode7d.ResetTime)
			if err == nil {
				result.WeeklyResetAt = &ts
			}
		}
	}

	if raw.SubscriptionBalance != nil {
		p := normalizePercent(raw.SubscriptionBalance.AmountUsedRatio)
		result.TotalUsagePercent = &p
		if raw.SubscriptionBalance.ExpireTime != "" {
			ts, err := parseKimiConnectTimestamp(raw.SubscriptionBalance.ExpireTime)
			if err == nil {
				result.TotalResetAt = &ts
			}
		}
	}

	return result, nil
}

func parseKimiConnectTimestamp(value string) (string, error) {
	// Kimi returns RFC 3339 timestamps with nanoseconds (e.g. 2026-07-22T11:10:48.227274070Z).
	// Truncate to valid RFC 3339 and re-format for consistency.
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("empty timestamp")
	}
	// Some timestamps may have more than 9 fractional digits; trim to 9.
	value = strings.Replace(value, "Z", "+00:00", 1)
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", err
	}
	return t.UTC().Format(time.RFC3339), nil
}

// normalizePercent converts a ratio to a 0-100 percentage value.
// Kimi returns ratios as fractions (e.g. 0.1469); we normalize to 0-100.
func normalizePercent(v float64) float64 {
	if v >= 0 && v <= 1 {
		return v * 100
	}
	return v
}
