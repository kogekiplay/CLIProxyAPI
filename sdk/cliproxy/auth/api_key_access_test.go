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
