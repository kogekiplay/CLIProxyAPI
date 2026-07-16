package usageledger

import (
	"sort"
	"strings"
	"time"
)

type analyticsAPIKeyMeta struct {
	hash       string
	provider   string
	accountRef string
	providers  map[string]struct{}
}

type analyticsCredentialMeta struct {
	provider     string
	authIndex    string
	authFileName string
	accountRef   string
}

type analyticsAccumulator struct {
	include        AnalyticsInclude
	summary        analyticsAggregate
	timeline       map[int64]analyticsAggregate
	models         map[string]analyticsAggregate
	apiKeys        map[string]analyticsAggregate
	apiKeyMeta     map[string]analyticsAPIKeyMeta
	credentials    map[string]analyticsAggregate
	credentialMeta map[string]analyticsCredentialMeta
}

func newAnalyticsAccumulator(include AnalyticsInclude) *analyticsAccumulator {
	acc := &analyticsAccumulator{include: include}
	if include.Timeline {
		acc.timeline = make(map[int64]analyticsAggregate)
	}
	if include.ModelStats {
		acc.models = make(map[string]analyticsAggregate)
	}
	if include.APIKeyStats {
		acc.apiKeys = make(map[string]analyticsAggregate)
		acc.apiKeyMeta = make(map[string]analyticsAPIKeyMeta)
	}
	if include.CredentialStats {
		acc.credentials = make(map[string]analyticsAggregate)
		acc.credentialMeta = make(map[string]analyticsCredentialMeta)
	}
	return acc
}

func (a *analyticsAccumulator) add(event analyticsEvent) {
	if a == nil {
		return
	}
	if a.include.Summary {
		addAnalyticsEvent(&a.summary, event)
	}
	if a.include.Timeline {
		bucket := event.event.Timestamp.UTC().Truncate(time.Hour).UnixMilli()
		agg := a.timeline[bucket]
		addAnalyticsEvent(&agg, event)
		a.timeline[bucket] = agg
	}
	if a.include.ModelStats {
		key := event.event.Model
		agg := a.models[key]
		addAnalyticsEvent(&agg, event)
		a.models[key] = agg
	}
	if !a.include.APIKeyStats && !a.include.CredentialStats {
		return
	}
	if isAnalyticsAPIKeyCredentialEvent(event.event) {
		if a.include.APIKeyStats {
			a.addAPIKey(event)
		}
		return
	}
	if a.include.CredentialStats {
		a.addCredential(event)
	}
}

func (a *analyticsAccumulator) addAPIKey(event analyticsEvent) {
	provider := strings.TrimSpace(event.event.Provider)
	hash := analyticsAPIKeyCredentialHash(event.event)
	agg := a.apiKeys[hash]
	addAnalyticsEvent(&agg, event)
	a.apiKeys[hash] = agg

	item := a.apiKeyMeta[hash]
	if item.hash == "" {
		item = analyticsAPIKeyMeta{
			hash:       hash,
			provider:   provider,
			accountRef: strings.TrimSpace(event.event.AccountRef),
			providers:  make(map[string]struct{}),
		}
	}
	if item.provider == "" {
		item.provider = provider
	}
	if item.accountRef == "" {
		item.accountRef = strings.TrimSpace(event.event.AccountRef)
	}
	if provider != "" {
		item.providers[provider] = struct{}{}
	}
	a.apiKeyMeta[hash] = item
}

func (a *analyticsAccumulator) addCredential(event analyticsEvent) {
	if strings.TrimSpace(event.event.AuthIndex) == "" && strings.TrimSpace(event.event.AuthFileName) == "" {
		return
	}
	key := strings.Join([]string{
		event.event.Provider,
		event.event.AuthIndex,
		event.event.AuthFileName,
		event.event.AccountRef,
	}, "\x00")
	if key == "\x00\x00\x00" {
		key = "unknown"
	}
	agg := a.credentials[key]
	addAnalyticsEvent(&agg, event)
	a.credentials[key] = agg
	if _, ok := a.credentialMeta[key]; !ok {
		a.credentialMeta[key] = analyticsCredentialMeta{
			provider:     event.event.Provider,
			authIndex:    event.event.AuthIndex,
			authFileName: event.event.AuthFileName,
			accountRef:   event.event.AccountRef,
		}
	}
}

func (a *analyticsAccumulator) apply(response *AnalyticsResponse) {
	if a == nil || response == nil {
		return
	}
	if a.include.Summary {
		response.Summary = analyticsSummaryFromAggregate(a.summary)
	}
	if a.include.Timeline {
		response.Timeline = analyticsTimelineFromAggregates(a.timeline)
	}
	if a.include.ModelStats {
		response.ModelStats = analyticsModelStatsFromAggregates(a.models)
	}
	if a.include.APIKeyStats {
		response.APIKeyStats = analyticsAPIKeyStatsFromAggregates(a.apiKeys, a.apiKeyMeta)
	}
	if a.include.CredentialStats {
		response.CredentialStats = analyticsCredentialStatsFromAggregates(a.credentials, a.credentialMeta)
	}
}

func analyticsSummaryFromAggregate(agg analyticsAggregate) *AnalyticsSummary {
	summary := &AnalyticsSummary{
		TotalCalls:          agg.calls,
		SuccessCalls:        agg.success,
		FailureCalls:        agg.failure,
		InputTokens:         agg.tokens.InputTokens,
		UncachedInputTokens: agg.tokens.UncachedInputTokens,
		TotalInputTokens:    agg.tokens.TotalInputTokens,
		OutputTokens:        agg.tokens.OutputTokens,
		ReasoningTokens:     agg.tokens.ReasoningTokens,
		CachedTokens:        agg.tokens.CachedTokens,
		CacheReadTokens:     agg.tokens.CacheReadTokens,
		CacheCreationTokens: agg.tokens.CacheCreationTokens,
		TotalTokens:         agg.tokens.TotalTokens,
		CacheHitRate:        CacheHitRate(agg.tokens),
	}
	if agg.hasCost {
		summary.TotalCost = floatPtr(agg.cost)
	}
	return summary
}

func analyticsTimelineFromAggregates(grouped map[int64]analyticsAggregate) []AnalyticsTimelinePoint {
	keys := make([]int64, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]AnalyticsTimelinePoint, 0, len(keys))
	for _, key := range keys {
		agg := grouped[key]
		point := AnalyticsTimelinePoint{
			BucketMS:            key,
			Calls:               agg.calls,
			Success:             agg.success,
			Failure:             agg.failure,
			InputTokens:         agg.tokens.InputTokens,
			UncachedInputTokens: agg.tokens.UncachedInputTokens,
			TotalInputTokens:    agg.tokens.TotalInputTokens,
			OutputTokens:        agg.tokens.OutputTokens,
			ReasoningTokens:     agg.tokens.ReasoningTokens,
			CachedTokens:        agg.tokens.CachedTokens,
			CacheReadTokens:     agg.tokens.CacheReadTokens,
			CacheCreationTokens: agg.tokens.CacheCreationTokens,
			TotalTokens:         agg.tokens.TotalTokens,
			CacheHitRate:        CacheHitRate(agg.tokens),
		}
		if agg.hasCost {
			point.Cost = floatPtr(agg.cost)
		}
		out = append(out, point)
	}
	return out
}

func analyticsModelStatsFromAggregates(grouped map[string]analyticsAggregate) []AnalyticsModelStat {
	keys := sortedAnalyticsStatKeys(grouped)
	out := make([]AnalyticsModelStat, 0, len(keys))
	for _, key := range keys {
		agg := grouped[key]
		row := AnalyticsModelStat{
			Model:               key,
			Calls:               agg.calls,
			SuccessCalls:        agg.success,
			FailureCalls:        agg.failure,
			InputTokens:         agg.tokens.InputTokens,
			UncachedInputTokens: agg.tokens.UncachedInputTokens,
			TotalInputTokens:    agg.tokens.TotalInputTokens,
			OutputTokens:        agg.tokens.OutputTokens,
			ReasoningTokens:     agg.tokens.ReasoningTokens,
			CachedTokens:        agg.tokens.CachedTokens,
			CacheReadTokens:     agg.tokens.CacheReadTokens,
			CacheCreationTokens: agg.tokens.CacheCreationTokens,
			TotalTokens:         agg.tokens.TotalTokens,
			CacheHitRate:        CacheHitRate(agg.tokens),
		}
		if agg.hasCost {
			row.Cost = floatPtr(agg.cost)
		}
		out = append(out, row)
	}
	return out
}

func analyticsAPIKeyStatsFromAggregates(grouped map[string]analyticsAggregate, meta map[string]analyticsAPIKeyMeta) []AnalyticsAPIKeyStat {
	keys := sortedAnalyticsStatKeys(grouped)
	out := make([]AnalyticsAPIKeyStat, 0, len(keys))
	for _, key := range keys {
		agg := grouped[key]
		item := meta[key]
		row := AnalyticsAPIKeyStat{
			Provider:            item.provider,
			Providers:           sortedStringSet(item.providers),
			APIKeyHash:          item.hash,
			AccountRef:          item.accountRef,
			Calls:               agg.calls,
			SuccessCalls:        agg.success,
			FailureCalls:        agg.failure,
			InputTokens:         agg.tokens.InputTokens,
			UncachedInputTokens: agg.tokens.UncachedInputTokens,
			TotalInputTokens:    agg.tokens.TotalInputTokens,
			OutputTokens:        agg.tokens.OutputTokens,
			ReasoningTokens:     agg.tokens.ReasoningTokens,
			CachedTokens:        agg.tokens.CachedTokens,
			CacheReadTokens:     agg.tokens.CacheReadTokens,
			CacheCreationTokens: agg.tokens.CacheCreationTokens,
			TotalTokens:         agg.tokens.TotalTokens,
			CacheHitRate:        CacheHitRate(agg.tokens),
		}
		if agg.hasCost {
			row.Cost = floatPtr(agg.cost)
		}
		out = append(out, row)
	}
	return out
}

func analyticsCredentialStatsFromAggregates(grouped map[string]analyticsAggregate, meta map[string]analyticsCredentialMeta) []AnalyticsCredentialStat {
	keys := sortedAnalyticsStatKeys(grouped)
	out := make([]AnalyticsCredentialStat, 0, len(keys))
	for _, key := range keys {
		agg := grouped[key]
		item := meta[key]
		row := AnalyticsCredentialStat{
			Provider:            item.provider,
			AuthIndex:           item.authIndex,
			AuthFileName:        item.authFileName,
			AccountRef:          item.accountRef,
			Calls:               agg.calls,
			SuccessCalls:        agg.success,
			FailureCalls:        agg.failure,
			InputTokens:         agg.tokens.InputTokens,
			UncachedInputTokens: agg.tokens.UncachedInputTokens,
			TotalInputTokens:    agg.tokens.TotalInputTokens,
			OutputTokens:        agg.tokens.OutputTokens,
			ReasoningTokens:     agg.tokens.ReasoningTokens,
			CachedTokens:        agg.tokens.CachedTokens,
			CacheReadTokens:     agg.tokens.CacheReadTokens,
			CacheCreationTokens: agg.tokens.CacheCreationTokens,
			TotalTokens:         agg.tokens.TotalTokens,
			CacheHitRate:        CacheHitRate(agg.tokens),
		}
		if agg.hasCost {
			row.Cost = floatPtr(agg.cost)
		}
		out = append(out, row)
	}
	return out
}
