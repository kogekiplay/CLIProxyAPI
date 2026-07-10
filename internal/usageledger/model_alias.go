package usageledger

import "strings"

// ModelAliasRule maps an upstream model to the configured model alias.
type ModelAliasRule struct {
	Provider      string
	AuthIndex     string
	UpstreamModel string
	Alias         string
}

type modelAliasExactKey struct {
	provider      string
	authIndex     string
	upstreamModel string
}

type modelAliasProviderKey struct {
	provider      string
	upstreamModel string
}

type modelAliasResolution struct {
	alias     string
	ambiguous bool
}

type modelAliasIndex struct {
	exact            map[modelAliasExactKey]modelAliasResolution
	provider         map[modelAliasProviderKey]modelAliasResolution
	aliasToUpstreams map[string][]string
}

func compileModelAliasIndex(rules []ModelAliasRule) modelAliasIndex {
	index := modelAliasIndex{
		exact:            make(map[modelAliasExactKey]modelAliasResolution),
		provider:         make(map[modelAliasProviderKey]modelAliasResolution),
		aliasToUpstreams: make(map[string][]string),
	}
	for _, rule := range rules {
		if !isModelAliasRule(rule) {
			continue
		}
		provider := normalizedModelAliasKey(rule.Provider)
		authIndex := normalizedModelAliasKey(rule.AuthIndex)
		upstreamModel := strings.TrimSpace(rule.UpstreamModel)
		upstreamKey := normalizedModelAliasKey(upstreamModel)
		alias := strings.TrimSpace(rule.Alias)

		exactKey := modelAliasExactKey{provider: provider, authIndex: authIndex, upstreamModel: upstreamKey}
		index.exact[exactKey] = addModelAliasResolution(index.exact[exactKey], alias)
		providerKey := modelAliasProviderKey{provider: provider, upstreamModel: upstreamKey}
		index.provider[providerKey] = addModelAliasResolution(index.provider[providerKey], alias)

		aliasKey := normalizedModelAliasKey(alias)
		index.aliasToUpstreams[aliasKey] = appendUniqueModelAlias(index.aliasToUpstreams[aliasKey], upstreamModel)
	}
	return index
}

func addModelAliasResolution(current modelAliasResolution, alias string) modelAliasResolution {
	if current.alias == "" {
		current.alias = alias
		return current
	}
	if !strings.EqualFold(current.alias, alias) {
		current.ambiguous = true
	}
	return current
}

func (i modelAliasIndex) resolve(event Event) string {
	if alias := strings.TrimSpace(event.ModelAlias); alias != "" {
		return alias
	}

	upstreamModel := strings.TrimSpace(event.Model)
	if upstreamModel == "" {
		return ""
	}
	provider := normalizedModelAliasKey(event.Provider)
	upstreamKey := normalizedModelAliasKey(upstreamModel)
	exactKey := modelAliasExactKey{
		provider:      provider,
		authIndex:     normalizedModelAliasKey(event.AuthIndex),
		upstreamModel: upstreamKey,
	}
	if resolution, ok := i.exact[exactKey]; ok {
		if !resolution.ambiguous {
			return resolution.alias
		}
		return upstreamModel
	}
	if resolution, ok := i.provider[modelAliasProviderKey{provider: provider, upstreamModel: upstreamKey}]; ok && !resolution.ambiguous {
		return resolution.alias
	}
	return upstreamModel
}

func (i modelAliasIndex) expandRequestedModels(requested []string) []string {
	candidates := append([]string{}, requested...)
	for _, model := range requested {
		for _, upstreamModel := range i.aliasToUpstreams[normalizedModelAliasKey(model)] {
			candidates = appendUniqueModelAlias(candidates, upstreamModel)
		}
	}
	return candidates
}

func resolveAnalyticsModel(event Event, rules []ModelAliasRule) string {
	return compileModelAliasIndex(rules).resolve(event)
}

func normalizedModelAliasKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isModelAliasRule(rule ModelAliasRule) bool {
	upstreamModel := strings.TrimSpace(rule.UpstreamModel)
	alias := strings.TrimSpace(rule.Alias)
	return strings.TrimSpace(rule.Provider) != "" && upstreamModel != "" && alias != "" && !strings.EqualFold(upstreamModel, alias)
}

func appendUniqueModelAlias(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}
