package auth

import (
	"context"
	"path/filepath"
	"strings"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

type apiKeyAccessScope struct {
	restricted bool
	providers  map[string]struct{}
	authFiles  map[string]struct{}
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
		restricted: true,
		providers:  stringSet(rule.Providers, true),
		authFiles:  stringSet(rule.AuthFiles, false),
	}
	return scope
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

func (s apiKeyAccessScope) allows(auth *Auth) bool {
	if !s.restricted {
		return true
	}
	if auth == nil {
		return false
	}
	if len(s.providers) == 0 && len(s.authFiles) == 0 {
		return false
	}
	if len(s.providers) > 0 {
		provider := strings.ToLower(strings.TrimSpace(auth.Provider))
		if _, ok := s.providers[provider]; !ok {
			return false
		}
	}
	if len(s.authFiles) > 0 && !s.matchesAuthFile(auth) {
		return false
	}
	return true
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
