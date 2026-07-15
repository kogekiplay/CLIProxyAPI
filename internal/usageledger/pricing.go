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
	model = strings.TrimSpace(model)
	price, ok := MatchModelPrice(model, prices)
	return costForUsageWithPrice(model, tokens, price, ok)
}

func costForUsageWithPriceIndex(model string, tokens TokenUsage, prices modelPriceIndex) (float64, bool, []string) {
	model = strings.TrimSpace(model)
	price, ok := prices.match(model)
	return costForUsageWithPrice(model, tokens, price, ok)
}

func costForUsageWithPrice(model string, tokens TokenUsage, price ModelPrice, ok bool) (float64, bool, []string) {
	if !ok {
		if model == "" {
			model = "unknown"
		}
		return 0, false, []string{model}
	}

	tokens = tokens.Normalize()
	cacheRead := maxInt64(tokens.CacheReadTokens, 0)
	cacheCreation := maxInt64(tokens.CacheCreationTokens, 0)
	compatCached := maxInt64(tokens.CachedTokens-cacheRead-cacheCreation, 0)
	billableInput := maxInt64(tokens.InputTokens-cacheRead-cacheCreation-compatCached, 0)
	output := maxInt64(tokens.OutputTokens+tokens.ReasoningTokens, 0)

	cost := float64(billableInput)/tokensPerMillion*price.InputPer1M +
		float64(output)/tokensPerMillion*price.OutputPer1M +
		float64(cacheRead)/tokensPerMillion*price.CacheReadPer1M +
		float64(cacheCreation)/tokensPerMillion*price.CacheCreationPer1M +
		float64(compatCached)/tokensPerMillion*price.CachedPer1M

	return cost, true, nil
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
