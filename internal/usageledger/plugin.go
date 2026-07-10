package usageledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

type plugin struct {
	store Store
	now   func() time.Time
}

// NewPlugin creates a core usage plugin that writes normalized request usage.
func NewPlugin(store Store, clock func() time.Time) coreusage.Plugin {
	if clock == nil {
		clock = time.Now
	}
	return &plugin{store: store, now: clock}
}

// HashAPIKey returns a stable non-reversible identifier for an API key.
func HashAPIKey(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])
}

func (p *plugin) HandleUsage(ctx context.Context, record coreusage.Record) {
	if p == nil || p.store == nil {
		return
	}
	event := p.eventFromRecord(ctx, record)
	if _, err := p.store.InsertEvent(ctx, event); err != nil {
		log.WithError(err).Warn("usage ledger: failed to store usage event")
	}
}

func (p *plugin) eventFromRecord(ctx context.Context, record coreusage.Record) Event {
	timestamp := record.RequestedAt
	if timestamp.IsZero() {
		timestamp = p.now()
	}
	authIndex := strings.TrimSpace(record.AuthIndex)
	if authIndex == "" {
		authIndex = strings.TrimSpace(record.AuthID)
	}
	serviceTier := strings.TrimSpace(record.ServiceTier)
	if serviceTier == "" {
		serviceTier = coreusage.ServiceTierFromContext(ctx)
	}
	reasoningEffort := strings.TrimSpace(record.ReasoningEffort)
	if reasoningEffort == "" {
		reasoningEffort = coreusage.ReasoningEffortFromContext(ctx)
	}
	statusCode := record.Fail.StatusCode
	if statusCode <= 0 {
		statusCode = internallogging.GetResponseStatus(ctx)
	}
	failStatusCode := record.Fail.StatusCode
	if failStatusCode <= 0 && statusCode >= 400 {
		failStatusCode = statusCode
	}

	return Event{
		RequestID:         strings.TrimSpace(internallogging.GetRequestID(ctx)),
		Timestamp:         timestamp,
		Provider:          strings.TrimSpace(record.Provider),
		Model:             strings.TrimSpace(record.Model),
		Endpoint:          strings.TrimSpace(internallogging.GetEndpoint(ctx)),
		AuthIndex:         authIndex,
		APIKeyHash:        HashAPIKey(record.APIKey),
		CredentialKeyHash: strings.TrimSpace(record.CredentialKeyHash),
		AccountRef:        accountRefFromSource(record.Source),
		AuthType:          strings.TrimSpace(record.AuthType),
		ServiceTier:       serviceTier,
		ReasoningEffort:   reasoningEffort,
		StatusCode:        statusCode,
		LatencyMS:         durationMillis(record.Latency),
		TTFTMS:            durationMillis(record.TTFT),
		FailStatusCode:    failStatusCode,
		FailBody:          record.Fail.Body,
		Tokens: TokenUsage{
			InputTokens:         record.Detail.InputTokens,
			OutputTokens:        record.Detail.OutputTokens,
			ReasoningTokens:     record.Detail.ReasoningTokens,
			CachedTokens:        record.Detail.CachedTokens,
			CacheReadTokens:     record.Detail.CacheReadTokens,
			CacheCreationTokens: record.Detail.CacheCreationTokens,
			TotalTokens:         record.Detail.TotalTokens,
		},
		Failed: record.Failed || failureFromRecordOrContext(ctx, record),
	}
}

func accountRefFromSource(source string) string {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "opencode-go:") {
		return source
	}
	return ""
}

func failureFromRecordOrContext(ctx context.Context, record coreusage.Record) bool {
	if record.Fail.StatusCode >= 400 {
		return true
	}
	status := internallogging.GetResponseStatus(ctx)
	return status >= 400
}

func durationMillis(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	return value.Milliseconds()
}
