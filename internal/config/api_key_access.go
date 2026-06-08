package config

import "strings"

const APIKeyAccessAll = "all"

type APIKeyAccessRule struct {
	Access          string                       `yaml:"access,omitempty" json:"access,omitempty"`
	Providers       []string                     `yaml:"providers,omitempty" json:"providers,omitempty"`
	ProviderTargets []APIKeyAccessProviderTarget `yaml:"provider-targets,omitempty" json:"provider-targets,omitempty"`
	AuthFiles       []string                     `yaml:"auth-files,omitempty" json:"auth-files,omitempty"`
}

type APIKeyAccessProviderTarget struct {
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"`
	BaseURL  string `yaml:"base-url,omitempty" json:"base-url,omitempty"`
}

func NormalizeAPIKeyAccessRules(rules map[string]APIKeyAccessRule) map[string]APIKeyAccessRule {
	if len(rules) == 0 {
		return nil
	}
	out := make(map[string]APIKeyAccessRule, len(rules))
	for rawKey, rawRule := range rules {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			continue
		}
		rule := NormalizeAPIKeyAccessRule(rawRule)
		out[key] = rule
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func NormalizeAPIKeyAccessRule(rule APIKeyAccessRule) APIKeyAccessRule {
	access := strings.ToLower(strings.TrimSpace(rule.Access))
	if access == APIKeyAccessAll {
		return APIKeyAccessRule{Access: APIKeyAccessAll}
	}
	return APIKeyAccessRule{
		Access:          access,
		Providers:       normalizeLowerStringList(rule.Providers),
		ProviderTargets: normalizeAPIKeyAccessProviderTargets(rule.ProviderTargets),
		AuthFiles:       normalizeStringList(rule.AuthFiles),
	}
}

func CloneAPIKeyAccessRules(rules map[string]APIKeyAccessRule) map[string]APIKeyAccessRule {
	if len(rules) == 0 {
		return nil
	}
	out := make(map[string]APIKeyAccessRule, len(rules))
	for key, rule := range rules {
		out[key] = APIKeyAccessRule{
			Access:          rule.Access,
			Providers:       append([]string(nil), rule.Providers...),
			ProviderTargets: append([]APIKeyAccessProviderTarget(nil), rule.ProviderTargets...),
			AuthFiles:       append([]string(nil), rule.AuthFiles...),
		}
	}
	return out
}

func (cfg *Config) SanitizeAPIKeyAccess() {
	if cfg == nil {
		return
	}
	cfg.APIKeyAccess = NormalizeAPIKeyAccessRules(cfg.APIKeyAccess)
}

func normalizeLowerStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeAPIKeyAccessProviderTargets(values []APIKeyAccessProviderTarget) []APIKeyAccessProviderTarget {
	if len(values) == 0 {
		return nil
	}
	out := make([]APIKeyAccessProviderTarget, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		provider := strings.ToLower(strings.TrimSpace(raw.Provider))
		if provider == "" {
			continue
		}
		target := APIKeyAccessProviderTarget{
			Provider: provider,
			BaseURL:  strings.TrimSpace(raw.BaseURL),
		}
		key := target.Provider + "\x00" + target.BaseURL
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, target)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
