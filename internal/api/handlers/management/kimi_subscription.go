package management

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"google.golang.org/protobuf/encoding/protowire"
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

// KimiSubscriptionStats proxies Kimi's web-only GetSubscriptionStats grpc-web
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
//   - total_reset_at: ISO timestamp when the monthly quota resets.
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
	// Empty protobuf message wrapped in a grpc-web data frame.
	// grpc-web frame layout: 1 byte flag (0=data) + 3 bytes length (big-endian) + payload.
	reqBody := []byte{0x00, 0x00, 0x00, 0x00, 0x00}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, kimiSubscriptionStatsURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+webToken)
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	req.Header.Set("X-Grpc-Web", "1")
	req.Header.Set("Accept", "application/grpc-web+proto")
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

	grpcStatus := resp.Header.Get("Grpc-Status")
	if grpcStatus != "" && grpcStatus != "0" {
		grpcMessage := resp.Header.Get("Grpc-Message")
		return nil, fmt.Errorf("kimi subscription stats grpc error: status=%s message=%s", grpcStatus, grpcMessage)
	}

	stats, err := parseKimiSubscriptionStatsResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kimi subscription stats: %w", err)
	}
	return stats, nil
}

func parseKimiSubscriptionStatsResponse(data []byte) (*KimiSubscriptionStatsResponse, error) {
	// grpc-web response may contain multiple frames: data frames (type 0)
	// followed by a trailers frame (type 1). Concatenate all data payloads.
	payload, err := decodeGRPCWebDataPayload(data)
	if err != nil {
		return nil, err
	}

	fields := parseProtobufMessage(payload)
	result := &KimiSubscriptionStatsResponse{}

	// field 2 = ratelimit_code_5h
	if v := fields.firstMessage(2); v != nil {
		ratio, ts := extractRateLimitFields(v)
		if ratio != nil {
			result.FiveHourCodePercent = ratio
		}
		if ts != nil {
			s := formatTimestampSeconds(*ts)
			result.FiveHourResetAt = &s
		}
	}

	// field 4 = ratelimit_code_7d
	if v := fields.firstMessage(4); v != nil {
		ratio, ts := extractRateLimitFields(v)
		if ratio != nil {
			result.WeeklyCodePercent = ratio
		}
		if ts != nil {
			s := formatTimestampSeconds(*ts)
			result.WeeklyResetAt = &s
		}
	}

	// field 5 = subscription_balance
	if v := fields.firstMessage(5); v != nil {
		ratio, ts := extractSubscriptionBalanceFields(v)
		if ratio != nil {
			result.TotalUsagePercent = ratio
		}
		if ts != nil {
			s := formatTimestampSeconds(*ts)
			result.TotalResetAt = &s
		}
	}

	return result, nil
}

func extractRateLimitFields(msg protobufFields) (ratio *float64, resetSeconds *int64) {
	// ratio is typically field 1 as a 32-bit float.
	if v := msg.firstFloat(1); v != nil {
		r := normalizePercent(*v)
		ratio = &r
	}
	// reset timestamp may be a varint int64 seconds field.
	if v := msg.firstInt(2); v != nil {
		resetSeconds = v
	}
	return
}

func extractSubscriptionBalanceFields(msg protobufFields) (ratio *float64, expireSeconds *int64) {
	// amount_used_ratio is field 8 (float).
	if v := msg.firstFloat(8); v != nil {
		r := normalizePercent(*v)
		ratio = &r
	}
	// expire_time is field 9; it may be a google.protobuf.Timestamp message
	// (field 1 = seconds) or a direct int64 seconds field.
	if v := msg.firstInt(9); v != nil {
		expireSeconds = v
	} else if nested := msg.firstMessage(9); nested != nil {
		if v := nested.firstInt(1); v != nil {
			expireSeconds = v
		}
	}
	return
}

// normalizePercent converts a protobuf ratio to a 0-100 percentage value.
// Kimi returns ratios as fractions (e.g. 0.1469) in some fields and as
// percentages (e.g. 14.69) in others; we normalize to 0-100.
func normalizePercent(v float64) float64 {
	if v >= 0 && v <= 1 {
		return v * 100
	}
	return v
}

func formatTimestampSeconds(seconds int64) string {
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

func decodeGRPCWebDataPayload(data []byte) ([]byte, error) {
	var payload []byte
	offset := 0
	for offset < len(data) {
		if offset+4 > len(data) {
			return nil, fmt.Errorf("incomplete grpc-web frame header")
		}
		frameType := data[offset]
		length := int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])
		if offset+4+length > len(data) {
			return nil, fmt.Errorf("incomplete grpc-web frame payload")
		}
		if frameType == 0 {
			payload = append(payload, data[offset+4:offset+4+length]...)
		}
		offset += 4 + length
	}
	return payload, nil
}

type protobufFields map[protowire.Number][]interface{}

func parseProtobufMessage(data []byte) protobufFields {
	fields := make(protobufFields)
	offset := 0
	for offset < len(data) {
		num, typ, n := protowire.ConsumeTag(data[offset:])
		if n < 0 {
			break
		}
		offset += n
		switch typ {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(data[offset:])
			if n < 0 {
				return fields
			}
			offset += n
			fields[num] = append(fields[num], int64(v))
		case protowire.Fixed32Type:
			v, n := protowire.ConsumeFixed32(data[offset:])
			if n < 0 {
				return fields
			}
			offset += n
			fields[num] = append(fields[num], math.Float32frombits(v))
		case protowire.Fixed64Type:
			v, n := protowire.ConsumeFixed64(data[offset:])
			if n < 0 {
				return fields
			}
			offset += n
			fields[num] = append(fields[num], math.Float64frombits(v))
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(data[offset:])
			if n < 0 {
				return fields
			}
			offset += n
			// Try to parse as nested message; fall back to raw bytes.
			if nested := parseProtobufMessage(v); len(nested) > 0 {
				fields[num] = append(fields[num], nested)
			} else {
				fields[num] = append(fields[num], v)
			}
		case protowire.StartGroupType:
			// Skip groups by consuming until the matching end group.
			_, n := protowire.ConsumeGroup(num, data[offset:])
			if n < 0 {
				return fields
			}
			offset += n
		default:
			return fields
		}
	}
	return fields
}

func (f protobufFields) firstValue(num protowire.Number) interface{} {
	if vals, ok := f[num]; ok && len(vals) > 0 {
		return vals[0]
	}
	return nil
}

func (f protobufFields) firstFloat(num protowire.Number) *float64 {
	v := f.firstValue(num)
	if v == nil {
		return nil
	}
	switch typed := v.(type) {
	case float32:
		f64 := float64(typed)
		return &f64
	case float64:
		return &typed
	}
	return nil
}

func (f protobufFields) firstInt(num protowire.Number) *int64 {
	v := f.firstValue(num)
	if v == nil {
		return nil
	}
	if i, ok := v.(int64); ok {
		return &i
	}
	return nil
}

func (f protobufFields) firstMessage(num protowire.Number) protobufFields {
	v := f.firstValue(num)
	if v == nil {
		return nil
	}
	if m, ok := v.(protobufFields); ok {
		return m
	}
	return nil
}
