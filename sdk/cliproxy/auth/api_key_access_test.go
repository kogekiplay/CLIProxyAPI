package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

type apiKeyAccessTestGinContext struct {
	values map[string]any
}

func (g *apiKeyAccessTestGinContext) Get(key string) (any, bool) {
	v, ok := g.values[key]
	return v, ok
}

func (g *apiKeyAccessTestGinContext) Set(key string, value any) {
	if g.values == nil {
		g.values = make(map[string]any)
	}
	g.values[key] = value
}

func contextWithUserAPIKey(key string) context.Context {
	return context.WithValue(context.Background(), "gin", &apiKeyAccessTestGinContext{
		values: map[string]any{"userApiKey": key},
	})
}

type apiKeyAccessHomeDispatcher struct {
	response homeAuthDispatchResponse
}

func (d *apiKeyAccessHomeDispatcher) HeartbeatOK() bool {
	return true
}

func (d *apiKeyAccessHomeDispatcher) RPopAuth(context.Context, string, string, http.Header, int) ([]byte, error) {
	return json.Marshal(d.response)
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

func TestAPIKeyAccessScope_ProvidersAndAuthFilesAreIndependent(t *testing.T) {
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
	if !scope.allows(&Auth{ID: "auth-2", Provider: "gemini", FileName: "auth-2.json"}) {
		t.Fatalf("matching provider should be allowed without an auth-file match")
	}
	if !scope.allows(&Auth{ID: "auth-1", Provider: "claude", FileName: "auth-1.json"}) {
		t.Fatalf("matching auth file should be allowed without a provider match")
	}
	if scope.allows(&Auth{ID: "auth-3", Provider: "claude", FileName: "auth-3.json"}) {
		t.Fatalf("auth without a matching provider or auth file should not be allowed")
	}
}

func TestAllowedAuthIDCacheForContextRefreshesOnAuthChanges(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.SetConfig(&internalconfig.Config{
		SDKConfig: internalconfig.SDKConfig{
			APIKeyAccess: map[string]internalconfig.APIKeyAccessRule{
				"key-1": {
					AuthFiles: []string{"auth-1.json"},
				},
			},
		},
	})
	ctx := contextWithUserAPIKey("key-1")

	ids, cacheKey, restricted := m.AllowedAuthIDCacheForContext(ctx)
	if !restricted {
		t.Fatalf("restricted = false, want true")
	}
	if len(ids) != 0 || cacheKey != "" {
		t.Fatalf("initial ids/cacheKey = %#v/%q, want empty", ids, cacheKey)
	}

	if _, err := m.Register(context.Background(), &Auth{ID: "auth-2", Provider: "gemini", FileName: "auth-2.json"}); err != nil {
		t.Fatalf("Register auth-2 error = %v", err)
	}
	ids, cacheKey, restricted = m.AllowedAuthIDCacheForContext(ctx)
	if !restricted {
		t.Fatalf("restricted after auth-2 = false, want true")
	}
	if len(ids) != 0 || cacheKey != "" {
		t.Fatalf("ids/cacheKey after disallowed auth = %#v/%q, want empty", ids, cacheKey)
	}

	if _, err := m.Register(context.Background(), &Auth{ID: "auth-1", Provider: "gemini", FileName: "auth-1.json"}); err != nil {
		t.Fatalf("Register auth-1 error = %v", err)
	}
	ids, cacheKey, restricted = m.AllowedAuthIDCacheForContext(ctx)
	if !restricted {
		t.Fatalf("restricted after auth-1 = false, want true")
	}
	if len(ids) != 1 || ids[0] != "auth-1" || cacheKey != "auth-1" {
		t.Fatalf("ids/cacheKey after allowed auth = %#v/%q, want [auth-1]/auth-1", ids, cacheKey)
	}

	if _, err := m.Update(context.Background(), &Auth{ID: "auth-1", Provider: "gemini", FileName: "auth-other.json"}); err != nil {
		t.Fatalf("Update auth-1 error = %v", err)
	}
	ids, cacheKey, restricted = m.AllowedAuthIDCacheForContext(ctx)
	if !restricted {
		t.Fatalf("restricted after update = false, want true")
	}
	if len(ids) != 0 || cacheKey != "" {
		t.Fatalf("ids/cacheKey after update = %#v/%q, want empty", ids, cacheKey)
	}
}

func TestAllowedAuthIDsForContextReturnsClonedCache(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	if _, err := m.Register(context.Background(), &Auth{ID: "auth-1", Provider: "gemini"}); err != nil {
		t.Fatalf("Register error = %v", err)
	}
	m.SetConfig(&internalconfig.Config{
		SDKConfig: internalconfig.SDKConfig{
			APIKeyAccess: map[string]internalconfig.APIKeyAccessRule{
				"key-1": {
					Providers: []string{"gemini"},
				},
			},
		},
	})
	ctx := contextWithUserAPIKey("key-1")

	first, restricted := m.AllowedAuthIDsForContext(ctx)
	if !restricted || len(first) != 1 || first[0] != "auth-1" {
		t.Fatalf("first AllowedAuthIDsForContext restricted=%v ids=%#v", restricted, first)
	}
	first[0] = "mutated"

	second, restricted := m.AllowedAuthIDsForContext(ctx)
	if !restricted || len(second) != 1 || second[0] != "auth-1" {
		t.Fatalf("second AllowedAuthIDsForContext restricted=%v ids=%#v, want [auth-1]", restricted, second)
	}
}

func TestAPIKeyAccessScope_ProviderTargetsDistinguishBaseURL(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.SetConfig(&internalconfig.Config{
		SDKConfig: internalconfig.SDKConfig{
			APIKeyAccess: map[string]internalconfig.APIKeyAccessRule{
				"key-1": {
					ProviderTargets: []internalconfig.APIKeyAccessProviderTarget{
						{Provider: "claude", BaseURL: "https://a.example.com"},
					},
					AuthFiles: []string{"claude-a.json", "claude-b.json"},
				},
				"key-2": {
					Providers: []string{"claude"},
				},
			},
		},
	})

	scoped := m.apiKeyAccessScopeForContext(contextWithUserAPIKey("key-1"))
	if !scoped.restricted {
		t.Fatalf("scope.restricted = false, want true")
	}
	if !scoped.allows(&Auth{
		ID:       "claude-a",
		Provider: "claude",
		FileName: "claude-a.json",
		Attributes: map[string]string{
			"base_url": "https://a.example.com",
		},
	}) {
		t.Fatalf("matching provider target and auth file should be allowed")
	}
	if !scoped.allows(&Auth{
		ID:       "claude-b",
		Provider: "claude",
		FileName: "claude-b.json",
		Attributes: map[string]string{
			"base_url": "https://b.example.com",
		},
	}) {
		t.Fatalf("matching auth file should be allowed even with a different base URL")
	}
	if !scoped.allows(&Auth{
		ID:       "claude-c",
		Provider: "claude",
		FileName: "claude-c.json",
		Attributes: map[string]string{
			"base_url": "https://a.example.com",
		},
	}) {
		t.Fatalf("matching provider target without matching auth file should be allowed")
	}
	if scoped.allows(&Auth{
		ID:       "claude-d",
		Provider: "claude",
		FileName: "claude-d.json",
		Attributes: map[string]string{
			"base_url": "https://b.example.com",
		},
	}) {
		t.Fatalf("auth without matching provider target or auth file should not be allowed")
	}

	legacyProvider := m.apiKeyAccessScopeForContext(contextWithUserAPIKey("key-2"))
	if !legacyProvider.allows(&Auth{
		ID:       "claude-b",
		Provider: "claude",
		FileName: "claude-b.json",
		Attributes: map[string]string{
			"base_url": "https://b.example.com",
		},
	}) {
		t.Fatalf("legacy provider allow-list should allow any base URL for that provider")
	}
}

func TestAPIKeyAccessScope_ProviderTargetsAndAuthFilesAreIndependent(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.SetConfig(&internalconfig.Config{
		SDKConfig: internalconfig.SDKConfig{
			APIKeyAccess: map[string]internalconfig.APIKeyAccessRule{
				"key-1": {
					ProviderTargets: []internalconfig.APIKeyAccessProviderTarget{
						{Provider: "codex", BaseURL: "https://a.example.com/v1"},
					},
					AuthFiles: []string{"codex-q981612327@outlook.com-pro.json"},
				},
			},
		},
	})

	scope := m.apiKeyAccessScopeForContext(contextWithUserAPIKey("key-1"))
	if !scope.allows(&Auth{
		ID:       "codex-q981612327@outlook.com-pro.json",
		Provider: "codex",
		FileName: "codex-q981612327@outlook.com-pro.json",
	}) {
		t.Fatalf("matching auth file should be allowed even when it has no provider base URL")
	}
	if !scope.allows(&Auth{
		ID:       "codex-api-key-a",
		Provider: "codex",
		FileName: "codex-api-key-a",
		Attributes: map[string]string{
			"base_url": "https://a.example.com/v1",
		},
	}) {
		t.Fatalf("matching provider target should be allowed even when it is not an auth-file target")
	}
	if scope.allows(&Auth{
		ID:       "codex-other-file.json",
		Provider: "codex",
		FileName: "codex-other-file.json",
	}) {
		t.Fatalf("unlisted auth file without matching provider target should not be allowed")
	}
	if scope.allows(&Auth{
		ID:       "codex-api-key-b",
		Provider: "codex",
		FileName: "codex-api-key-b",
		Attributes: map[string]string{
			"base_url": "https://b.example.com/v1",
		},
	}) {
		t.Fatalf("different provider target should not be allowed")
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

func TestAllowedAuthIDsForContext_ReturnsScopedIDs(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.SetConfig(&internalconfig.Config{
		SDKConfig: internalconfig.SDKConfig{
			APIKeyAccess: map[string]internalconfig.APIKeyAccessRule{
				"key-1": {AuthFiles: []string{"auth-1.json", "auth-3.json"}},
			},
		},
	})
	m.auths["auth-3"] = &Auth{ID: "auth-3", Provider: "test", FileName: "auth-3.json"}
	m.auths["auth-2"] = &Auth{ID: "auth-2", Provider: "test", FileName: "auth-2.json"}
	m.auths["auth-1"] = &Auth{ID: "auth-1", Provider: "test", FileName: "auth-1.json"}

	ids, restricted := m.AllowedAuthIDsForContext(contextWithUserAPIKey("key-1"))
	if !restricted {
		t.Fatalf("restricted = false, want true")
	}
	if got, want := strings.Join(ids, ","), "auth-1,auth-3"; got != want {
		t.Fatalf("AllowedAuthIDsForContext() = %q, want %q", got, want)
	}
}

func TestPickNextLegacy_RespectsAPIKeyAccessScope(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.RegisterExecutor(schedulerTestExecutor{})
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient("auth-1", "test", []*registry.ModelInfo{{ID: "gpt5.5"}})
	reg.RegisterClient("auth-2", "test", []*registry.ModelInfo{{ID: "gpt5.5"}})
	t.Cleanup(func() {
		reg.UnregisterClient("auth-1")
		reg.UnregisterClient("auth-2")
	})
	m.SetConfig(&internalconfig.Config{
		SDKConfig: internalconfig.SDKConfig{
			APIKeyAccess: map[string]internalconfig.APIKeyAccessRule{
				"key-1": {AuthFiles: []string{"auth-1.json"}},
			},
		},
	})
	m.auths["auth-1"] = &Auth{
		ID:       "auth-1",
		Provider: "test",
		FileName: "auth-1.json",
		ModelStates: map[string]*ModelState{
			"gpt5.5": {
				Status:         StatusError,
				Unavailable:    true,
				NextRetryAfter: time.Now().Add(time.Hour),
				Quota: QuotaState{
					Exceeded:      true,
					NextRecoverAt: time.Now().Add(time.Hour),
				},
			},
		},
	}
	m.auths["auth-2"] = &Auth{ID: "auth-2", Provider: "test", FileName: "auth-2.json"}

	_, _, err := m.pickNextLegacy(contextWithUserAPIKey("key-1"), "test", "gpt5.5", cliproxyexecutor.Options{}, nil)
	if err == nil {
		t.Fatalf("pickNextLegacy() error = nil, want access-scope bounded failure")
	}
	var cooldownErr *modelCooldownError
	if !errors.As(err, &cooldownErr) {
		t.Fatalf("pickNextLegacy() error = %v, want model cooldown error", err)
	}
}

func TestPickNext_ScopedKeyDoesNotUseSchedulerUnfilteredAuth(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.RegisterExecutor(schedulerTestExecutor{})
	m.SetConfig(&internalconfig.Config{
		SDKConfig: internalconfig.SDKConfig{
			APIKeyAccess: map[string]internalconfig.APIKeyAccessRule{
				"key-1": {AuthFiles: []string{"auth-1.json"}},
			},
		},
	})
	auth1 := &Auth{ID: "auth-1", Provider: "test", FileName: "auth-1.json"}
	auth2 := &Auth{ID: "auth-2", Provider: "test", FileName: "auth-2.json", Attributes: map[string]string{"priority": "10"}}
	m.auths["auth-1"] = auth1
	m.auths["auth-2"] = auth2
	m.syncScheduler()

	selected, _, err := m.pickNext(contextWithUserAPIKey("key-1"), "test", "", cliproxyexecutor.Options{}, nil)
	if err != nil {
		t.Fatalf("pickNext() error = %v", err)
	}
	if selected.ID != "auth-1" {
		t.Fatalf("selected auth = %q, want auth-1", selected.ID)
	}
}

func TestPickNextMixed_ScopedKeyDoesNotUseSchedulerUnfilteredAuth(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.RegisterExecutor(schedulerTestExecutor{})
	m.executors["other"] = schedulerBenchmarkExecutor{id: "other"}
	m.SetConfig(&internalconfig.Config{
		SDKConfig: internalconfig.SDKConfig{
			APIKeyAccess: map[string]internalconfig.APIKeyAccessRule{
				"key-1": {AuthFiles: []string{"auth-1.json"}},
			},
		},
	})
	auth1 := &Auth{ID: "auth-1", Provider: "test", FileName: "auth-1.json"}
	auth2 := &Auth{ID: "auth-2", Provider: "other", FileName: "auth-2.json", Attributes: map[string]string{"priority": "10"}}
	m.auths["auth-1"] = auth1
	m.auths["auth-2"] = auth2
	m.syncScheduler()

	selected, _, provider, err := m.pickNextMixed(contextWithUserAPIKey("key-1"), []string{"test", "other"}, "", cliproxyexecutor.Options{}, nil)
	if err != nil {
		t.Fatalf("pickNextMixed() error = %v", err)
	}
	if selected.ID != "auth-1" {
		t.Fatalf("selected auth = %q, want auth-1", selected.ID)
	}
	if provider != "test" {
		t.Fatalf("selected provider = %q, want test", provider)
	}
}

func TestAPIKeyAccessScope_HomeDispatchUsesDispatchedAPIKey(t *testing.T) {
	dispatcher := &apiKeyAccessHomeDispatcher{
		response: homeAuthDispatchResponse{
			UserAPIKey: "effective-key",
			Auth: Auth{
				ID:       "home-auth-outside",
				Provider: "test",
				Status:   StatusActive,
			},
		},
	}
	oldCurrentHomeDispatcher := currentHomeDispatcher
	currentHomeDispatcher = func() homeAuthDispatcher {
		return dispatcher
	}
	t.Cleanup(func() {
		currentHomeDispatcher = oldCurrentHomeDispatcher
	})

	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.RegisterExecutor(schedulerTestExecutor{})
	m.SetConfig(&internalconfig.Config{
		Home: internalconfig.HomeConfig{Enabled: true},
		SDKConfig: internalconfig.SDKConfig{
			APIKeyAccess: map[string]internalconfig.APIKeyAccessRule{
				"effective-key": {AuthFiles: []string{"home-auth-allowed"}},
			},
		},
	})

	selected, executor, provider, err := m.pickNextViaHome(contextWithUserAPIKey("client-key"), "gpt5.5", cliproxyexecutor.Options{}, nil)
	if err == nil {
		t.Fatalf("pickNextViaHome() error = nil, selected=%#v executor=%#v provider=%q, want access-scope failure", selected, executor, provider)
	}
	if got := err.Error(); !strings.Contains(got, "access scope") {
		t.Fatalf("pickNextViaHome() error = %q, want access-scope failure", got)
	}
}
