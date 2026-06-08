package auth

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

type apiKeyAccessScope struct {
	restricted      bool
	providers       map[string]struct{}
	providerTargets map[string]struct{}
	authFiles       map[string]struct{}
}

func (m *Manager) apiKeyAccessScopeForContext(ctx context.Context) apiKeyAccessScope {
	if m == nil {
		return apiKeyAccessScope{}
	}
	clientKey := clientAPIKeyFromContext(ctx)
	if clientKey == "" {
		return apiKeyAccessScope{}
	}
	rawCfg := m.runtimeConfig.Load()
	cfg, _ := rawCfg.(*internalconfig.Config)
	if cfg == nil || len(cfg.APIKeyAccess) == 0 {
		return apiKeyAccessScope{}
	}
	rule, ok := cfg.APIKeyAccess[clientKey]
	if !ok {
		return apiKeyAccessScope{}
	}
	rule = internalconfig.NormalizeAPIKeyAccessRule(rule)
	if strings.EqualFold(rule.Access, internalconfig.APIKeyAccessAll) {
		return apiKeyAccessScope{}
	}
	scope := apiKeyAccessScope{
		restricted:      true,
		providers:       stringSet(rule.Providers, true),
		providerTargets: providerTargetSet(rule.ProviderTargets),
		authFiles:       stringSet(rule.AuthFiles, false),
	}
	return scope
}

// AllowedAuthsForContext returns auth entries visible to the client API key in ctx.
// The boolean return is true only when the key has an explicit restricted rule.
func (m *Manager) AllowedAuthsForContext(ctx context.Context) ([]*Auth, bool) {
	scope := m.apiKeyAccessScopeForContext(ctx)
	if !scope.restricted {
		return nil, false
	}
	if m == nil {
		return nil, true
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	auths := make([]*Auth, 0, len(m.auths))
	for _, candidate := range m.auths {
		if !scope.allows(candidate) {
			continue
		}
		auths = append(auths, candidate.Clone())
	}
	sort.Slice(auths, func(i, j int) bool {
		return auths[i].ID < auths[j].ID
	})
	return auths, true
}

func clientAPIKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	ginCtx, ok := ctx.Value("gin").(interface{ Get(string) (any, bool) })
	if !ok || ginCtx == nil {
		return ""
	}
	raw, ok := ginCtx.Get("userApiKey")
	if !ok {
		return ""
	}
	return contextStringValue(raw)
}

func stringSet(values []string, lower bool) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if lower {
			value = strings.ToLower(value)
		}
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func providerTargetSet(values []internalconfig.APIKeyAccessProviderTarget) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, raw := range values {
		key := providerTargetKey(raw.Provider, raw.BaseURL)
		if key == "" {
			continue
		}
		out[key] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func providerTargetKey(provider, baseURL string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return ""
	}
	return provider + "\x00" + strings.TrimSpace(baseURL)
}

func (s apiKeyAccessScope) allows(auth *Auth) bool {
	if !s.restricted {
		return true
	}
	if auth == nil {
		return false
	}
	if len(s.providers) == 0 && len(s.providerTargets) == 0 && len(s.authFiles) == 0 {
		return false
	}
	if len(s.providers) > 0 || len(s.providerTargets) > 0 {
		if s.matchesProvider(auth) {
			return true
		}
	}
	if len(s.authFiles) > 0 {
		return s.matchesAuthFile(auth)
	}
	return false
}

func (s apiKeyAccessScope) matchesProvider(auth *Auth) bool {
	if auth == nil {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	if provider == "" {
		return false
	}
	if _, ok := s.providers[provider]; ok {
		return true
	}
	if len(s.providerTargets) == 0 {
		return false
	}
	baseURL := ""
	if auth.Attributes != nil {
		baseURL = strings.TrimSpace(auth.Attributes["base_url"])
		if baseURL == "" {
			baseURL = strings.TrimSpace(auth.Attributes["base-url"])
		}
	}
	_, ok := s.providerTargets[providerTargetKey(provider, baseURL)]
	return ok
}

func (s apiKeyAccessScope) matchesAuthFile(auth *Auth) bool {
	if auth == nil || len(s.authFiles) == 0 {
		return false
	}
	candidates := []string{
		strings.TrimSpace(auth.ID),
		strings.TrimSpace(auth.FileName),
		filepath.Base(strings.TrimSpace(auth.FileName)),
	}
	for _, candidate := range candidates {
		if candidate == "" || candidate == "." {
			continue
		}
		if _, ok := s.authFiles[candidate]; ok {
			return true
		}
	}
	return false
}

func apiKeyAccessDeniedError() *Error {
	return &Error{Code: "auth_not_found", Message: "no auth available for current api key access scope"}
}
