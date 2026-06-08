package auth

import (
	"context"
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

type apiKeyAccessTestGinContext struct {
	values map[string]any
}

func (g *apiKeyAccessTestGinContext) Get(key string) (any, bool) {
	v, ok := g.values[key]
	return v, ok
}

func contextWithUserAPIKey(key string) context.Context {
	return context.WithValue(context.Background(), "gin", &apiKeyAccessTestGinContext{
		values: map[string]any{"userApiKey": key},
	})
}

func TestAPIKeyAccessScope_AllowsUnconfiguredKey(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.SetConfig(&internalconfig.Config{
		SDKConfig: internalconfig.SDKConfig{
			APIKeys: []string{"key-1"},
		},
	})

	scope := m.apiKeyAccessScopeForContext(contextWithUserAPIKey("key-1"))
	if !scope.allows(&Auth{ID: "auth-2", Provider: "gemini", FileName: "auth-2.json"}) {
		t.Fatalf("unconfigured key should allow all auths")
	}
}

func TestAPIKeyAccessScope_ProviderAndAuthFileIntersection(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.SetConfig(&internalconfig.Config{
		SDKConfig: internalconfig.SDKConfig{
			APIKeyAccess: map[string]internalconfig.APIKeyAccessRule{
				"key-1": {
					Providers: []string{"gemini"},
					AuthFiles: []string{"auth-1.json"},
				},
			},
		},
	})

	scope := m.apiKeyAccessScopeForContext(contextWithUserAPIKey("key-1"))
	if !scope.restricted {
		t.Fatalf("scope.restricted = false, want true")
	}
	if !scope.allows(&Auth{ID: "auth-1", Provider: "gemini", FileName: "auth-1.json"}) {
		t.Fatalf("matching provider and auth file should be allowed")
	}
	if scope.allows(&Auth{ID: "auth-2", Provider: "gemini", FileName: "auth-2.json"}) {
		t.Fatalf("auth-2 should not be allowed")
	}
	if scope.allows(&Auth{ID: "auth-1", Provider: "claude", FileName: "auth-1.json"}) {
		t.Fatalf("claude should not be allowed")
	}
}

func TestAPIKeyAccessScope_EmptyRuleAllowsNoAuth(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.SetConfig(&internalconfig.Config{
		SDKConfig: internalconfig.SDKConfig{
			APIKeyAccess: map[string]internalconfig.APIKeyAccessRule{
				"key-1": {},
			},
		},
	})

	scope := m.apiKeyAccessScopeForContext(contextWithUserAPIKey("key-1"))
	if !scope.restricted {
		t.Fatalf("empty configured rule should be restricted")
	}
	if scope.allows(&Auth{ID: "auth-1", Provider: "gemini", FileName: "auth-1.json"}) {
		t.Fatalf("empty configured rule should allow no auth")
	}
}
