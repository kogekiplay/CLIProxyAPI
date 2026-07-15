package usageledger

import (
	"strings"
)

const tokensPerMillion = 1_000_000.0

type modelPriceWildcard struct {
	prefix string
	price  ModelPrice
}

// modelPriceIndex is compiled once for bulk analytics queries. It preserves the
// first-match behavior of MatchModelPrice while avoiding a full price scan for
// every usage event.
type modelPriceIndex struct {
	exact     map[string]ModelPrice
	wildcards []modelPriceWildcard
}

func compileModelPriceIndex(prices []ModelPrice) modelPriceIndex {
	index := modelPriceIndex{
		exact:     make(map[string]ModelPrice, len(prices)),
		wildcards: make([]modelPriceWildcard, 0),
	}
	for _, price := range prices {
		pattern := strings.TrimSpace(price.Model)
		if pattern == "" {
			continue
		}
		normalizedPattern := strings.ToLower(pattern)
		if _, exists := index.exact[normalizedPattern]; !exists {
			index.exact[normalizedPattern] = price
		}
		if strings.HasSuffix(normalizedPattern, "*") {
			prefix := strings.TrimSuffix(normalizedPattern, "*")
			if prefix != "" {
				index.wildcards = append(index.wildcards, modelPriceWildcard{prefix: prefix, price: price})
			}
		}
	}
	return index
}

func (i modelPriceIndex) match(model string) (ModelPrice, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return ModelPrice{}, false
	}
	normalizedModel := strings.ToLower(model)
	if price, ok := i.exact[normalizedModel]; ok {
		return price, true
	}
	if baseModel := modelWithoutReasoningSuffix(model); baseModel != model {
		if price, ok := i.exact[strings.ToLower(baseModel)]; ok {
			return price, true
		}
	}
	for _, wildcard := range i.wildcards {
		if strings.HasPrefix(normalizedModel, wildcard.prefix) {
			return wildcard.price, true
		}
	}
	return ModelPrice{}, false
}

// CostForUsage computes the estimated USD cost for one model and token set.
func CostForUsage(model string, tokens TokenUsage, prices []ModelPrice) (float64, bool, []string) {
	return CostForUsageWithServiceTier(model, "", tokens, prices)
}

// CostForUsageWithServiceTier computes estimated USD cost using the response
// service tier when one is available.
func CostForUsageWithServiceTier(model, serviceTier string, tokens TokenUsage, prices []ModelPrice) (float64, bool, []string) {
	model = strings.TrimSpace(model)
	price, ok := MatchModelPrice(model, prices)
	return costForUsageWithPrice(model, "", serviceTier, tokens, TokenUsage{}, price, ok)
}

func costForUsageWithPriceIndex(model string, tokens TokenUsage, prices modelPriceIndex) (float64, bool, []string) {
	return costForUsageWithServiceTierAndPriceIndex(model, "", tokens, prices)
}

func costForUsageWithServiceTierAndPriceIndex(model, serviceTier string, tokens TokenUsage, prices modelPriceIndex) (float64, bool, []string) {
	model = strings.TrimSpace(model)
	price, ok := prices.match(model)
	return costForUsageWithPrice(model, "", serviceTier, tokens, TokenUsage{}, price, ok)
}

func costForUsageWithBehaviorAndPriceIndex(priceModel, behaviorModel, serviceTier string, tokens TokenUsage, prices modelPriceIndex) (float64, bool, []string) {
	priceModel = strings.TrimSpace(priceModel)
	price, ok := prices.match(priceModel)
	return costForUsageWithPrice(priceModel, behaviorModel, serviceTier, tokens, TokenUsage{}, price, ok)
}

func costForAggregateWithPriceIndex(model, serviceTier string, tokens, longTokens TokenUsage, prices modelPriceIndex) (float64, bool, []string) {
	model = strings.TrimSpace(model)
	price, ok := prices.match(model)
	return costForUsageWithPrice(model, "", serviceTier, tokens, longTokens, price, ok)
}

type costTokenBuckets struct {
	totalInput int64
	uncached   int64
	output     int64
	cached     int64
	cacheRead  int64
	cacheWrite int64
}

func costForUsageWithPrice(model, behaviorModel, serviceTier string, tokens, aggregateLongTokens TokenUsage, price ModelPrice, ok bool) (float64, bool, []string) {
	if !ok {
		if model == "" {
			model = "unknown"
		}
		return 0, false, []string{model}
	}

	behaviorModel = strings.TrimSpace(behaviorModel)
	if behaviorModel == "" {
		behaviorModel = strings.TrimSpace(price.SourceModelID)
	}
	if behaviorModel == "" {
		behaviorModel = model
	}
	price = effectiveModelPrice(behaviorModel, price)
	all := canonicalCostTokenBuckets(tokens)
	long := canonicalCostTokenBuckets(aggregateLongTokens)
	if long.totalInput == 0 && supportsLongContextPremium(behaviorModel) && all.totalInput > LongContextInputTokenThreshold {
		long = all
	}
	if !supportsLongContextPremium(behaviorModel) {
		long = costTokenBuckets{}
	}
	long = clampCostTokenBuckets(long, all)
	short := subtractCostTokenBuckets(all, long)

	shortMultiplier := serviceTierMultiplier(behaviorModel, serviceTier)
	longMultiplier := shortMultiplier
	if isPriorityServiceTier(serviceTier) {
		longMultiplier = 1
	}
	cost := priceCost(short, price, 1, 1) * shortMultiplier
	cost += priceCost(long, price, 2, 1.5) * longMultiplier

	return cost, true, nil
}

func effectiveModelPrice(model string, price ModelPrice) ModelPrice {
	if strings.EqualFold(strings.TrimSpace(price.Source), "manual") {
		return price
	}
	if price.CacheReadPer1M == 0 {
		price.CacheReadPer1M = price.CachedPer1M
		if price.CacheReadPer1M == 0 {
			price.CacheReadPer1M = price.InputPer1M * 0.1
		}
	}
	if price.CacheCreationPer1M == 0 {
		price.CacheCreationPer1M = price.InputPer1M
		if isModelFamily(model, "gpt-5.6") {
			price.CacheCreationPer1M = price.InputPer1M * 1.25
		}
	}
	return price
}

func canonicalCostTokenBuckets(tokens TokenUsage) costTokenBuckets {
	tokens = tokens.Normalize()
	cacheRead := maxInt64(tokens.CacheReadTokens, 0)
	cacheWrite := maxInt64(tokens.CacheCreationTokens, 0)
	canonical := tokens.TotalInputTokens > 0
	cached := maxInt64(tokens.CachedTokens, 0)
	if !canonical {
		cached = maxInt64(cached-cacheRead-cacheWrite, 0)
	}
	totalInput := maxInt64(tokens.TotalInputTokens, 0)
	if totalInput == 0 {
		totalInput = maxInt64(tokens.InputTokens, 0)
	}
	uncached := maxInt64(tokens.UncachedInputTokens, 0)
	if !canonical || uncached == 0 {
		uncached = maxInt64(totalInput-cached-cacheRead-cacheWrite, 0)
	}
	return costTokenBuckets{
		totalInput: totalInput,
		uncached:   uncached,
		output:     maxInt64(tokens.OutputTokens, 0),
		cached:     cached,
		cacheRead:  cacheRead,
		cacheWrite: cacheWrite,
	}
}

func clampCostTokenBuckets(value, maximum costTokenBuckets) costTokenBuckets {
	value.totalInput = minInt64(maxInt64(value.totalInput, 0), maximum.totalInput)
	value.uncached = minInt64(maxInt64(value.uncached, 0), maximum.uncached)
	value.output = minInt64(maxInt64(value.output, 0), maximum.output)
	value.cached = minInt64(maxInt64(value.cached, 0), maximum.cached)
	value.cacheRead = minInt64(maxInt64(value.cacheRead, 0), maximum.cacheRead)
	value.cacheWrite = minInt64(maxInt64(value.cacheWrite, 0), maximum.cacheWrite)
	return value
}

func subtractCostTokenBuckets(all, part costTokenBuckets) costTokenBuckets {
	return costTokenBuckets{
		totalInput: maxInt64(all.totalInput-part.totalInput, 0),
		uncached:   maxInt64(all.uncached-part.uncached, 0),
		output:     maxInt64(all.output-part.output, 0),
		cached:     maxInt64(all.cached-part.cached, 0),
		cacheRead:  maxInt64(all.cacheRead-part.cacheRead, 0),
		cacheWrite: maxInt64(all.cacheWrite-part.cacheWrite, 0),
	}
}

func priceCost(tokens costTokenBuckets, price ModelPrice, inputMultiplier, outputMultiplier float64) float64 {
	return float64(tokens.uncached)/tokensPerMillion*price.InputPer1M*inputMultiplier +
		float64(tokens.output)/tokensPerMillion*price.OutputPer1M*outputMultiplier +
		float64(tokens.cacheRead)/tokensPerMillion*price.CacheReadPer1M*inputMultiplier +
		float64(tokens.cacheWrite)/tokensPerMillion*price.CacheCreationPer1M*inputMultiplier +
		float64(tokens.cached)/tokensPerMillion*price.CachedPer1M*inputMultiplier
}

func serviceTierMultiplier(model, tier string) float64 {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "flex", "batch":
		return 0.5
	case "priority", "fast":
		switch {
		case isModelFamily(model, "gpt-5.6"):
			return 2
		case isModelFamily(model, "gpt-5.5"):
			return 2.5
		case isModelFamily(model, "gpt-5.4"):
			return 2
		case isModelFamily(model, "gpt-5.3-codex"):
			return 2
		}
	}
	return 1
}

func isPriorityServiceTier(tier string) bool {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "priority", "fast":
		return true
	default:
		return false
	}
}

// MatchModelPrice resolves an exact model price before a trailing-* wildcard.
func MatchModelPrice(model string, prices []ModelPrice) (ModelPrice, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return ModelPrice{}, false
	}
	if price, ok := matchExactModelPrice(model, prices); ok {
		return price, true
	}
	if baseModel := modelWithoutReasoningSuffix(model); baseModel != model {
		if price, ok := matchExactModelPrice(baseModel, prices); ok {
			return price, true
		}
	}
	for _, price := range prices {
		pattern := strings.TrimSpace(price.Model)
		if !strings.HasSuffix(pattern, "*") {
			continue
		}
		prefix := strings.TrimSuffix(pattern, "*")
		if prefix != "" && strings.HasPrefix(strings.ToLower(model), strings.ToLower(prefix)) {
			return price, true
		}
	}
	return ModelPrice{}, false
}

func matchExactModelPrice(model string, prices []ModelPrice) (ModelPrice, bool) {
	for _, price := range prices {
		if strings.EqualFold(strings.TrimSpace(price.Model), model) {
			return price, true
		}
	}
	return ModelPrice{}, false
}

func modelWithoutReasoningSuffix(model string) string {
	open := strings.LastIndex(model, "(")
	if open <= 0 || !strings.HasSuffix(model, ")") {
		return model
	}
	return strings.TrimSpace(model[:open])
}

func maxInt64(value, minimum int64) int64 {
	if value < minimum {
		return minimum
	}
	return value
}

func minInt64(value, maximum int64) int64 {
	if value > maximum {
		return maximum
	}
	return value
}
