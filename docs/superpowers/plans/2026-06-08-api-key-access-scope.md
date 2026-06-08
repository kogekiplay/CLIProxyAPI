# API Key Access Scope Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 CPA 客户端 API key 增加硬授权范围，让每个 key 只能使用被授权的 auth file 和 AI provider，并在管理页面里可视化配置。

**Architecture:** 后端新增顶层 `api-key-access` 配置，运行时在 auth 选择前过滤候选凭据；未配置规则的 key 保持全量访问。管理 API 提供规则 CRUD 和可选 auth 目标，管理页面在现有配置页的 API key 卡片里编辑规则，并保存回 YAML。访问范围是硬边界，额度耗尽、冷却、重试、模型 fallback、provider fallback、auth fallback 都不能越过当前 key 的授权集合。

**Tech Stack:** Go 1.x, Gin, YAML v3, React 19, TypeScript, Vite, Bun.

---

## 关键语义

- `api-key-access` 的 map key 是现有 `api-keys` 里的原始客户端 key。
- 没有 `api-key-access`，或某个 key 没有规则：保持老行为，允许全部。
- `access: all`：允许全部。
- 有规则但 `providers` 和 `auth-files` 都为空：禁止使用任何 auth。
- 同时配置 `providers` 和 `auth-files`：取交集。
- provider 比较统一 trim + lowercase。
- auth file 比较支持 `Auth.ID`、`Auth.FileName` 和文件名 basename。
- 只授权 auth file 1 的 key，永远不能用 auth file 2 的额度，即使 auth file 1 对目标模型没额度或正在冷却。

## 文件结构

后端仓库：`/Users/kogeki/dev/CLIProxyAPI`

- Modify: `internal/config/sdk_config.go`  
  给 `SDKConfig` 增加 `APIKeyAccess map[string]APIKeyAccessRule`。
- Create: `internal/config/api_key_access.go`  
  定义 `APIKeyAccessRule`、规范化函数、规则拷贝函数。
- Create: `internal/config/api_key_access_test.go`  
  覆盖解析、规范化、YAML round trip、空受限规则。
- Modify: `internal/config/config.go`  
  在 `LoadConfigOptional` 的正常和 optional 路径调用 `cfg.SanitizeAPIKeyAccess()`。
- Create: `sdk/cliproxy/auth/api_key_access.go`  
  从请求上下文解析当前客户端 key，构建访问 scope，判断 auth 是否允许。
- Create: `sdk/cliproxy/auth/api_key_access_test.go`  
  覆盖 provider-only、auth-file-only、交集、空规则、未配置规则、硬 fallback 边界。
- Modify: `sdk/cliproxy/auth/conductor.go`  
  在 legacy/mixed/Home 路径过滤候选；受限 key 让 scheduler 快路径回退到 legacy。
- Create: `internal/api/handlers/management/api_key_access.go`  
  新增管理 API handler、响应结构、auth target 列表和脱敏 key label。
- Create: `internal/api/handlers/management/api_key_access_test.go`  
  覆盖 GET/PUT/PATCH/DELETE、错误 body、规范化、脱敏响应。
- Modify: `internal/api/server.go`  
  注册 `/api-key-access` 管理路由。
- Modify: `internal/watcher/diff/config_diff.go`  
  配置 diff 增加 `api-key-access` 变化摘要，不打印原始 key。
- Modify: `internal/watcher/diff/config_diff_test.go`  
  覆盖 diff 脱敏。
- Modify: `sdk/config/config.go`  
  re-export `APIKeyAccessRule` 给 SDK 用户。
- Modify: `config.example.yaml`  
  增加简短示例。

管理页面仓库：`/Users/kogeki/dev/Cli-Proxy-API-Management-Center`

- Modify: `src/types/config.ts`  
  增加 `ApiKeyAccessRule`、`ApiKeyAccessRules`、`apiKeyAccess`。
- Modify: `src/types/visualConfig.ts`  
  增加 `apiKeyAccessRules` 到 `VisualConfigValues`。
- Modify: `src/hooks/useVisualConfig.ts`  
  解析和写回 YAML 的 `api-key-access`。
- Create: `src/services/api/apiKeyAccess.ts`  
  调用后端 `/api-key-access`，读取 auth target 选项。
- Modify: `src/services/api/index.ts`  
  导出新 service。
- Modify: `src/components/config/VisualConfigEditor.tsx`  
  把 `apiKeyAccessRules` 和 auth target 选项传给 `ApiKeysCardEditor`。
- Modify: `src/components/config/VisualConfigEditorBlocks.tsx`  
  在 API key 列表中加入“全部/受限”编辑 UI。
- Modify: `src/components/config/VisualConfigEditor.module.scss`  
  给受限 scope 编辑器补样式。
- Modify: `src/pages/ConfigPage.tsx`  
  加载 auth target 元数据，兼容旧后端 404。
- Modify: `src/i18n/locales/en.json`
- Modify: `src/i18n/locales/zh-CN.json`
- Modify: `src/i18n/locales/zh-TW.json`
- Modify: `src/i18n/locales/ru.json`

---

### Task 0: 执行前仓库检查

**Files:** 无代码改动

- [ ] **Step 1: 确认后端分支和未提交文件**

Run:

```bash
git status --short
git branch --show-current
```

Expected:

```text
feature/api-key-access-scope
```

`?? .codegraph/` 可以存在，不能提交。

- [ ] **Step 2: 确认管理页面仓库干净，并创建功能分支**

Run in `/Users/kogeki/dev/Cli-Proxy-API-Management-Center`:

```bash
git status --short
git switch -c feature/api-key-access-scope
```

Expected: 工作区干净，新分支创建成功。如果分支已存在，运行：

```bash
git switch feature/api-key-access-scope
```

---

### Task 1: 后端配置类型和规范化

**Files:**
- Modify: `/Users/kogeki/dev/CLIProxyAPI/internal/config/sdk_config.go`
- Create: `/Users/kogeki/dev/CLIProxyAPI/internal/config/api_key_access.go`
- Create: `/Users/kogeki/dev/CLIProxyAPI/internal/config/api_key_access_test.go`
- Modify: `/Users/kogeki/dev/CLIProxyAPI/internal/config/config.go`
- Modify: `/Users/kogeki/dev/CLIProxyAPI/sdk/config/config.go`

- [ ] **Step 1: 写失败测试**

Create `/Users/kogeki/dev/CLIProxyAPI/internal/config/api_key_access_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNormalizeAPIKeyAccessRules(t *testing.T) {
	rules := NormalizeAPIKeyAccessRules(map[string]APIKeyAccessRule{
		" key-limited ": {
			Providers: []string{" Claude ", "claude", "GEMINI", ""},
			AuthFiles: []string{" claude-a.json ", "claude-a.json", "gemini-b.json"},
		},
		"key-all": {
			Access:    " ALL ",
			Providers: []string{"claude"},
			AuthFiles: []string{"claude-a.json"},
		},
		" ": {Providers: []string{"gemini"}},
	})

	limited := rules["key-limited"]
	if got, want := limited.Providers, []string{"claude", "gemini"}; !equalStringSlices(got, want) {
		t.Fatalf("providers = %#v, want %#v", got, want)
	}
	if got, want := limited.AuthFiles, []string{"claude-a.json", "gemini-b.json"}; !equalStringSlices(got, want) {
		t.Fatalf("auth-files = %#v, want %#v", got, want)
	}
	if _, ok := rules[" "]; ok {
		t.Fatalf("blank key rule was retained")
	}
	if got := rules["key-all"]; got.Access != APIKeyAccessAll || len(got.Providers) != 0 || len(got.AuthFiles) != 0 {
		t.Fatalf("access all rule = %#v, want only access=all", got)
	}
}

func TestLoadConfigOptional_APIKeyAccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte(`
api-keys:
  - key-all
  - key-limited
api-key-access:
  key-all:
    access: all
  key-limited:
    providers: ["Claude", "gemini"]
    auth-files:
      - " claude-a.json "
      - "gemini-b.json"
  key-empty: {}
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigOptional(path, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	if cfg.APIKeyAccess["key-limited"].Providers[0] != "claude" {
		t.Fatalf("provider was not normalized: %#v", cfg.APIKeyAccess["key-limited"])
	}
	if _, ok := cfg.APIKeyAccess["key-empty"]; !ok {
		t.Fatalf("empty restricted rule should be retained")
	}

	rendered, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if !strings.Contains(string(rendered), "api-key-access:") {
		t.Fatalf("rendered YAML does not include api-key-access:\n%s", rendered)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
go test ./internal/config -run 'TestNormalizeAPIKeyAccessRules|TestLoadConfigOptional_APIKeyAccess' -count=1
```

Expected: FAIL，提示 `APIKeyAccessRule` 或 `NormalizeAPIKeyAccessRules` 未定义。

- [ ] **Step 3: 增加配置类型**

Modify `/Users/kogeki/dev/CLIProxyAPI/internal/config/sdk_config.go` near `APIKeys`:

```go
	// APIKeys is a list of keys for authenticating clients to this proxy server.
	APIKeys []string `yaml:"api-keys" json:"api-keys"`

	// APIKeyAccess maps client API keys to auth/provider access rules.
	APIKeyAccess map[string]APIKeyAccessRule `yaml:"api-key-access,omitempty" json:"api-key-access,omitempty"`
```

Create `/Users/kogeki/dev/CLIProxyAPI/internal/config/api_key_access.go`:

```go
package config

import "strings"

const APIKeyAccessAll = "all"

type APIKeyAccessRule struct {
	Access    string   `yaml:"access,omitempty" json:"access,omitempty"`
	Providers []string `yaml:"providers,omitempty" json:"providers,omitempty"`
	AuthFiles []string `yaml:"auth-files,omitempty" json:"auth-files,omitempty"`
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
		Access:    access,
		Providers: normalizeLowerStringList(rule.Providers),
		AuthFiles: normalizeStringList(rule.AuthFiles),
	}
}

func CloneAPIKeyAccessRules(rules map[string]APIKeyAccessRule) map[string]APIKeyAccessRule {
	if len(rules) == 0 {
		return nil
	}
	out := make(map[string]APIKeyAccessRule, len(rules))
	for key, rule := range rules {
		out[key] = APIKeyAccessRule{
			Access:    rule.Access,
			Providers: append([]string(nil), rule.Providers...),
			AuthFiles: append([]string(nil), rule.AuthFiles...),
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
```

- [ ] **Step 4: 在配置加载路径调用规范化**

Modify `/Users/kogeki/dev/CLIProxyAPI/internal/config/config.go`:

After successful `yaml.Unmarshal(data, &cfg)`, add:

```go
	cfg.SanitizeAPIKeyAccess()
```

For optional empty/missing config branches that return `cfg := &Config{}`, no rule exists, no extra mutation is required.

- [ ] **Step 5: SDK re-export**

Modify `/Users/kogeki/dev/CLIProxyAPI/sdk/config/config.go` near other type aliases:

```go
type APIKeyAccessRule = internalconfig.APIKeyAccessRule
```

- [ ] **Step 6: 跑配置测试**

Run:

```bash
go test ./internal/config -run 'TestNormalizeAPIKeyAccessRules|TestLoadConfigOptional_APIKeyAccess' -count=1
```

Expected: PASS.

- [ ] **Step 7: 提交**

Run:

```bash
git add internal/config/sdk_config.go internal/config/api_key_access.go internal/config/api_key_access_test.go internal/config/config.go sdk/config/config.go
git commit -m "feat: add api key access config"
```

---

### Task 2: 后端运行时 scope helper

**Files:**
- Create: `/Users/kogeki/dev/CLIProxyAPI/sdk/cliproxy/auth/api_key_access.go`
- Create: `/Users/kogeki/dev/CLIProxyAPI/sdk/cliproxy/auth/api_key_access_test.go`

- [ ] **Step 1: 写失败测试**

Create `/Users/kogeki/dev/CLIProxyAPI/sdk/cliproxy/auth/api_key_access_test.go`:

```go
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
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
go test ./sdk/cliproxy/auth -run 'TestAPIKeyAccessScope' -count=1
```

Expected: FAIL，提示 `apiKeyAccessScopeForContext` 未定义。

- [ ] **Step 3: 实现 helper**

Create `/Users/kogeki/dev/CLIProxyAPI/sdk/cliproxy/auth/api_key_access.go`:

```go
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
```

- [ ] **Step 4: 跑 helper 测试**

Run:

```bash
go test ./sdk/cliproxy/auth -run 'TestAPIKeyAccessScope' -count=1
```

Expected: PASS.

- [ ] **Step 5: 提交**

Run:

```bash
git add sdk/cliproxy/auth/api_key_access.go sdk/cliproxy/auth/api_key_access_test.go
git commit -m "feat: add api key access scope helper"
```

---

### Task 3: 后端认证选择硬边界

**Files:**
- Modify: `/Users/kogeki/dev/CLIProxyAPI/sdk/cliproxy/auth/conductor.go`
- Modify: `/Users/kogeki/dev/CLIProxyAPI/sdk/cliproxy/auth/api_key_access_test.go`

- [ ] **Step 1: 写失败测试：legacy 路径不借未授权 auth**

Append to `/Users/kogeki/dev/CLIProxyAPI/sdk/cliproxy/auth/api_key_access_test.go`:

```go
func TestPickNextLegacy_RespectsAPIKeyAccessScope(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.RegisterExecutor(schedulerTestExecutor{})
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
			},
		},
	}
	m.auths["auth-2"] = &Auth{ID: "auth-2", Provider: "test", FileName: "auth-2.json"}

	_, _, err := m.pickNextLegacy(contextWithUserAPIKey("key-1"), "test", "gpt5.5", cliproxyexecutor.Options{}, nil)
	if err == nil {
		t.Fatalf("pickNextLegacy() error = nil, want access-scope bounded failure")
	}
	if got := err.Error(); !strings.Contains(got, "cooling down") && !strings.Contains(got, "access scope") {
		t.Fatalf("pickNextLegacy() error = %q, want scoped cooldown or access error", got)
	}
}
```

Add imports if missing:

```go
	"strings"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
```

- [ ] **Step 2: 写失败测试：scheduler 快路径回退到 scoped legacy**

Append:

```go
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
	auth2 := &Auth{ID: "auth-2", Provider: "test", FileName: "auth-2.json"}
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
```

- [ ] **Step 3: 跑测试确认失败**

Run:

```bash
go test ./sdk/cliproxy/auth -run 'TestPickNextLegacy_RespectsAPIKeyAccessScope|TestPickNext_ScopedKeyDoesNotUseSchedulerUnfilteredAuth' -count=1
```

Expected: FAIL，当前选择逻辑未过滤 scope。

- [ ] **Step 4: 修改 legacy 单 provider 选择**

Modify `/Users/kogeki/dev/CLIProxyAPI/sdk/cliproxy/auth/conductor.go` in `pickNextLegacy`:

```go
	scope := m.apiKeyAccessScopeForContext(ctx)
	for _, candidate := range m.auths {
		if candidate.Provider != provider || candidate.Disabled {
			continue
		}
		if !scope.allows(candidate) {
			continue
		}
		// keep the existing pinnedAuthID, disallowFreeAuth, tried, and model checks
	}
	if len(candidates) == 0 {
		m.mu.RUnlock()
		if scope.restricted {
			return nil, nil, apiKeyAccessDeniedError()
		}
		return nil, nil, &Error{Code: "auth_not_found", Message: "no auth available"}
	}
```

- [ ] **Step 5: 修改 legacy mixed 选择**

Modify `pickNextMixedLegacy` similarly:

```go
	scope := m.apiKeyAccessScopeForContext(ctx)
	for _, candidate := range m.auths {
		if candidate == nil || candidate.Disabled {
			continue
		}
		if !scope.allows(candidate) {
			continue
		}
		// keep the existing providerSet, pinnedAuthID, disallowFreeAuth, tried, executor, and model checks
	}
	if len(candidates) == 0 {
		m.mu.RUnlock()
		if scope.restricted {
			return nil, nil, "", apiKeyAccessDeniedError()
		}
		return nil, nil, "", &Error{Code: "auth_not_found", Message: "no auth available"}
	}
```

- [ ] **Step 6: 让 scheduler 快路径在受限 key 下回退到 legacy**

Modify `pickNext` after `HomeEnabled()` check:

```go
	if m.apiKeyAccessScopeForContext(ctx).restricted {
		return m.pickNextLegacy(ctx, provider, model, opts, tried)
	}
```

Modify `pickNextMixed` after `HomeEnabled()` check:

```go
	if m.apiKeyAccessScopeForContext(ctx).restricted {
		return m.pickNextMixedLegacy(ctx, providers, model, opts, tried)
	}
```

- [ ] **Step 7: 修改 Home runtime 返回 auth 的边界**

Modify `pickNextViaHome`:

```go
	scope := m.apiKeyAccessScopeForContext(ctx)
```

Before returning cached websocket auth:

```go
				if auth, executor, providerKey, ok := m.homeRuntimeAuthByID(executionSessionID, pinnedAuthID); ok {
					if !scope.allows(auth) {
						return nil, nil, "", apiKeyAccessDeniedError()
					}
					return auth, executor, providerKey, nil
				}
```

After `providerKey` is validated and before `Executor(providerKey)`:

```go
	if !scope.allows(&auth) {
		return nil, nil, "", apiKeyAccessDeniedError()
	}
```

- [ ] **Step 8: 跑 auth 测试**

Run:

```bash
go test ./sdk/cliproxy/auth -run 'TestAPIKeyAccessScope|TestPickNextLegacy_RespectsAPIKeyAccessScope|TestPickNext_ScopedKeyDoesNotUseSchedulerUnfilteredAuth' -count=1
```

Expected: PASS.

- [ ] **Step 9: 跑 auth 包完整测试**

Run:

```bash
go test ./sdk/cliproxy/auth -count=1
```

Expected: PASS.

- [ ] **Step 10: 提交**

Run:

```bash
git add sdk/cliproxy/auth/conductor.go sdk/cliproxy/auth/api_key_access_test.go
git commit -m "feat: enforce api key auth scopes"
```

---

### Task 4: 后端管理 API

**Files:**
- Create: `/Users/kogeki/dev/CLIProxyAPI/internal/api/handlers/management/api_key_access.go`
- Create: `/Users/kogeki/dev/CLIProxyAPI/internal/api/handlers/management/api_key_access_test.go`
- Modify: `/Users/kogeki/dev/CLIProxyAPI/internal/api/server.go`

- [ ] **Step 1: 写失败测试**

Create `/Users/kogeki/dev/CLIProxyAPI/internal/api/handlers/management/api_key_access_test.go`:

```go
package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestGetAPIKeyAccess_RedactsKeyLabelsAndReturnsRules(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	h := &Handler{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				APIKeys: []string{"sk-secret-123456"},
				APIKeyAccess: map[string]config.APIKeyAccessRule{
					"sk-secret-123456": {Providers: []string{"gemini"}},
				},
			},
		},
		configFilePath: writeTestConfigFile(t),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/api-key-access", nil)

	h.GetAPIKeyAccess(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"label":"sk-secret-123456"`) {
		t.Fatalf("response label leaked raw key: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"api-key-access"`) {
		t.Fatalf("response missing api-key-access: %s", rec.Body.String())
	}
}

func TestPutPatchDeleteAPIKeyAccess_NormalizesRules(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	h := &Handler{
		cfg:            &config.Config{},
		configFilePath: writeTestConfigFile(t),
	}

	putBody := []byte(`{"api-key-access":{"key-1":{"providers":["Gemini","gemini"],"auth-files":[" auth-1.json ","auth-1.json"]}}}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/v0/management/api-key-access", bytes.NewReader(putBody))
	h.PutAPIKeyAccess(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := h.cfg.APIKeyAccess["key-1"].Providers; len(got) != 1 || got[0] != "gemini" {
		t.Fatalf("providers = %#v, want [gemini]", got)
	}

	patchPayload := map[string]any{
		"key": "key-2",
		"value": map[string]any{
			"access": "all",
		},
	}
	data, _ := json.Marshal(patchPayload)
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/api-key-access", bytes.NewReader(data))
	h.PatchAPIKeyAccess(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if h.cfg.APIKeyAccess["key-2"].Access != config.APIKeyAccessAll {
		t.Fatalf("key-2 rule = %#v, want access all", h.cfg.APIKeyAccess["key-2"])
	}

	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/v0/management/api-key-access?key=key-1", nil)
	h.DeleteAPIKeyAccess(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := h.cfg.APIKeyAccess["key-1"]; ok {
		t.Fatalf("key-1 rule still exists")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
go test ./internal/api/handlers/management -run 'TestGetAPIKeyAccess|TestPutPatchDeleteAPIKeyAccess' -count=1
```

Expected: FAIL，handler 未定义。

- [ ] **Step 3: 实现 handler**

Create `/Users/kogeki/dev/CLIProxyAPI/internal/api/handlers/management/api_key_access.go`:

```go
package management

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type apiKeyAccessKeyView struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	HasRule bool   `json:"has-rule"`
}

type apiKeyAccessAuthTarget struct {
	ID        string         `json:"id"`
	Provider  string         `json:"provider"`
	FileName  string         `json:"filename,omitempty"`
	Label     string         `json:"label,omitempty"`
	AuthIndex string         `json:"auth-index,omitempty"`
	Email     string         `json:"email,omitempty"`
	ProjectID string         `json:"project-id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func (h *Handler) GetAPIKeyAccess(c *gin.Context) {
	h.mu.Lock()
	cfg := h.cfg
	rules := map[string]config.APIKeyAccessRule(nil)
	keys := []string(nil)
	if cfg != nil {
		rules = config.CloneAPIKeyAccessRules(cfg.APIKeyAccess)
		keys = append([]string(nil), cfg.APIKeys...)
	}
	h.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"api-key-access": rules,
		"api-keys":       buildAPIKeyAccessKeyViews(keys, rules),
		"auth-targets":   h.apiKeyAccessAuthTargets(),
	})
}

func (h *Handler) PutAPIKeyAccess(c *gin.Context) {
	data, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}
	rules, ok := decodeAPIKeyAccessRules(data)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	h.mu.Lock()
	h.cfg.APIKeyAccess = config.NormalizeAPIKeyAccessRules(rules)
	h.mu.Unlock()
	h.persist(c)
}

func (h *Handler) PatchAPIKeyAccess(c *gin.Context) {
	var body struct {
		Key   string                    `json:"key"`
		Value config.APIKeyAccessRule   `json:"value"`
		Rule  *config.APIKeyAccessRule  `json:"rule"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	key := strings.TrimSpace(body.Key)
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing key"})
		return
	}
	rule := body.Value
	if body.Rule != nil {
		rule = *body.Rule
	}
	h.mu.Lock()
	if h.cfg.APIKeyAccess == nil {
		h.cfg.APIKeyAccess = make(map[string]config.APIKeyAccessRule)
	}
	h.cfg.APIKeyAccess[key] = config.NormalizeAPIKeyAccessRule(rule)
	h.cfg.SanitizeAPIKeyAccess()
	h.mu.Unlock()
	h.persist(c)
}

func (h *Handler) DeleteAPIKeyAccess(c *gin.Context) {
	key := strings.TrimSpace(c.Query("key"))
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing key"})
		return
	}
	h.mu.Lock()
	delete(h.cfg.APIKeyAccess, key)
	if len(h.cfg.APIKeyAccess) == 0 {
		h.cfg.APIKeyAccess = nil
	}
	h.mu.Unlock()
	h.persist(c)
}

func decodeAPIKeyAccessRules(data []byte) (map[string]config.APIKeyAccessRule, bool) {
	var wrapped struct {
		APIKeyAccess map[string]config.APIKeyAccessRule `json:"api-key-access"`
		Rules        map[string]config.APIKeyAccessRule `json:"rules"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil {
		if wrapped.APIKeyAccess != nil {
			return wrapped.APIKeyAccess, true
		}
		if wrapped.Rules != nil {
			return wrapped.Rules, true
		}
	}
	var raw map[string]config.APIKeyAccessRule
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false
	}
	return raw, true
}

func buildAPIKeyAccessKeyViews(keys []string, rules map[string]config.APIKeyAccessRule) []apiKeyAccessKeyView {
	out := make([]apiKeyAccessKeyView, 0, len(keys))
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		_, hasRule := rules[trimmed]
		out = append(out, apiKeyAccessKeyView{
			Key:     trimmed,
			Label:   redactedAPIKeyLabel(trimmed),
			HasRule: hasRule,
		})
	}
	return out
}

func redactedAPIKeyLabel(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return "********"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func (h *Handler) apiKeyAccessAuthTargets() []apiKeyAccessAuthTarget {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	manager := h.authManager
	h.mu.Unlock()
	if manager == nil {
		return nil
	}
	auths := manager.List()
	out := make([]apiKeyAccessAuthTarget, 0, len(auths))
	for _, auth := range auths {
		target := apiKeyAccessAuthTargetFromAuth(auth)
		if target.ID != "" {
			out = append(out, target)
		}
	}
	return out
}

func apiKeyAccessAuthTargetFromAuth(auth *coreauth.Auth) apiKeyAccessAuthTarget {
	if auth == nil {
		return apiKeyAccessAuthTarget{}
	}
	target := apiKeyAccessAuthTarget{
		ID:        strings.TrimSpace(auth.ID),
		Provider:  strings.ToLower(strings.TrimSpace(auth.Provider)),
		FileName:  strings.TrimSpace(auth.FileName),
		Label:     strings.TrimSpace(auth.Label),
		AuthIndex: strings.TrimSpace(auth.Index),
	}
	if target.FileName == "" {
		target.FileName = filepath.Base(target.ID)
	}
	if auth.Attributes != nil {
		target.Email = strings.TrimSpace(auth.Attributes["email"])
		target.ProjectID = strings.TrimSpace(auth.Attributes["project_id"])
	}
	if auth.Metadata != nil {
		if target.Email == "" {
			if email, ok := auth.Metadata["email"].(string); ok {
				target.Email = strings.TrimSpace(email)
			}
		}
		if target.ProjectID == "" {
			if projectID, ok := auth.Metadata["project_id"].(string); ok {
				target.ProjectID = strings.TrimSpace(projectID)
			}
		}
	}
	return target
}
```

- [ ] **Step 4: 注册路由**

Modify `/Users/kogeki/dev/CLIProxyAPI/internal/api/server.go` after `/api-keys` routes:

```go
		mgmt.GET("/api-key-access", s.mgmt.GetAPIKeyAccess)
		mgmt.PUT("/api-key-access", s.mgmt.PutAPIKeyAccess)
		mgmt.PATCH("/api-key-access", s.mgmt.PatchAPIKeyAccess)
		mgmt.DELETE("/api-key-access", s.mgmt.DeleteAPIKeyAccess)
```

- [ ] **Step 5: 跑管理 handler 测试**

Run:

```bash
go test ./internal/api/handlers/management -run 'TestGetAPIKeyAccess|TestPutPatchDeleteAPIKeyAccess' -count=1
```

Expected: PASS.

- [ ] **Step 6: 提交**

Run:

```bash
git add internal/api/handlers/management/api_key_access.go internal/api/handlers/management/api_key_access_test.go internal/api/server.go
git commit -m "feat: add api key access management api"
```

---

### Task 5: 配置 diff 脱敏和示例

**Files:**
- Modify: `/Users/kogeki/dev/CLIProxyAPI/internal/watcher/diff/config_diff.go`
- Modify: `/Users/kogeki/dev/CLIProxyAPI/internal/watcher/diff/config_diff_test.go`
- Modify: `/Users/kogeki/dev/CLIProxyAPI/config.example.yaml`

- [ ] **Step 1: 写失败测试**

Append to `/Users/kogeki/dev/CLIProxyAPI/internal/watcher/diff/config_diff_test.go`:

```go
func TestBuildConfigChangeDetails_APIKeyAccessRedacted(t *testing.T) {
	oldCfg := &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeyAccess: map[string]config.APIKeyAccessRule{
				"sk-old-secret": {Providers: []string{"gemini"}},
			},
		},
	}
	newCfg := &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeyAccess: map[string]config.APIKeyAccessRule{
				"sk-new-secret": {AuthFiles: []string{"auth-1.json"}},
			},
		},
	}

	details := BuildConfigChangeDetails(oldCfg, newCfg)
	expectContains(t, details, "api-key-access: updated (1 -> 1 rules, redacted)")
	joined := strings.Join(details, "\n")
	if strings.Contains(joined, "sk-old-secret") || strings.Contains(joined, "sk-new-secret") {
		t.Fatalf("diff leaked API keys: %s", joined)
	}
}
```

Add import if missing:

```go
	"strings"
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
go test ./internal/watcher/diff -run TestBuildConfigChangeDetails_APIKeyAccessRedacted -count=1
```

Expected: FAIL，缺少 `api-key-access` diff。

- [ ] **Step 3: 实现 diff**

Modify `/Users/kogeki/dev/CLIProxyAPI/internal/watcher/diff/config_diff.go` after API keys diff:

```go
	if len(oldCfg.APIKeyAccess) != len(newCfg.APIKeyAccess) {
		changes = append(changes, fmt.Sprintf("api-key-access: updated (%d -> %d rules, redacted)", len(oldCfg.APIKeyAccess), len(newCfg.APIKeyAccess)))
	} else if !reflect.DeepEqual(config.NormalizeAPIKeyAccessRules(oldCfg.APIKeyAccess), config.NormalizeAPIKeyAccessRules(newCfg.APIKeyAccess)) {
		changes = append(changes, fmt.Sprintf("api-key-access: updated (%d -> %d rules, redacted)", len(oldCfg.APIKeyAccess), len(newCfg.APIKeyAccess)))
	}
```

- [ ] **Step 4: 更新示例配置**

Modify `/Users/kogeki/dev/CLIProxyAPI/config.example.yaml` near `api-keys`:

```yaml
# Optional per-client API key access scopes.
# A key without a rule keeps unrestricted access.
api-key-access:
  example-admin-key:
    access: all
  example-limited-key:
    providers:
      - gemini
      - claude
    auth-files:
      - user@gmail.com-project.json
      - claude-user@example.com.json
```

- [ ] **Step 5: 跑 diff 测试**

Run:

```bash
go test ./internal/watcher/diff -run 'TestBuildConfigChangeDetails' -count=1
```

Expected: PASS.

- [ ] **Step 6: 提交**

Run:

```bash
git add internal/watcher/diff/config_diff.go internal/watcher/diff/config_diff_test.go config.example.yaml
git commit -m "docs: document api key access scopes"
```

---

### Task 6: 后端整体验证

**Files:** 无新代码，验证后端

- [ ] **Step 1: 跑目标包测试**

Run:

```bash
go test ./internal/config ./internal/access/... ./internal/api/handlers/management ./internal/watcher/diff ./sdk/cliproxy/auth -count=1
```

Expected: PASS.

- [ ] **Step 2: 跑构建**

Run:

```bash
go build -o test-output ./cmd/server
rm test-output
```

Expected: build 成功。

- [ ] **Step 3: 如果目标包测试或构建失败，修复后重跑**

Run the exact failing package again. Example:

```bash
go test ./sdk/cliproxy/auth -run TestName -count=1 -v
```

Expected: failing test PASS 后，再回到 Step 1。

- [ ] **Step 4: 提交验证修复**

Only if Step 3 changed files in the planned backend scope:

```bash
git add internal/config internal/api/handlers/management internal/api/server.go internal/watcher/diff sdk/config sdk/cliproxy/auth config.example.yaml
git commit -m "fix: stabilize api key access scope tests"
```

---

### Task 7: 管理页面类型和 YAML 读写

**Files:**
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/types/config.ts`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/types/visualConfig.ts`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/hooks/useVisualConfig.ts`

- [ ] **Step 1: 增加类型**

Modify `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/types/config.ts`:

```ts
export interface ApiKeyAccessRule {
  access?: 'all' | string;
  providers?: string[];
  authFiles?: string[];
}

export type ApiKeyAccessRules = Record<string, ApiKeyAccessRule>;
```

Add to `Config`:

```ts
  apiKeyAccess?: ApiKeyAccessRules;
```

Add to `RawConfigSection`:

```ts
  | 'api-key-access'
```

Modify `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/types/visualConfig.ts`:

```ts
import type { ApiKeyAccessRules } from './config';
```

Add to `VisualConfigValues`:

```ts
  apiKeyAccessRules: ApiKeyAccessRules;
```

Add to `DEFAULT_VISUAL_VALUES`:

```ts
  apiKeyAccessRules: {},
```

- [ ] **Step 2: 增加 YAML 解析 helper**

Modify `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/hooks/useVisualConfig.ts` imports:

```ts
import type { ApiKeyAccessRule, ApiKeyAccessRules } from '@/types/config';
```

Add near `parseApiKeysText`:

```ts
function normalizeStringList(raw: unknown, lowercase = false): string[] {
  if (!Array.isArray(raw)) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of raw) {
    const value = String(item ?? '').trim();
    const normalized = lowercase ? value.toLowerCase() : value;
    if (!normalized || seen.has(normalized)) continue;
    seen.add(normalized);
    out.push(normalized);
  }
  return out;
}

function parseApiKeyAccessRules(raw: unknown): ApiKeyAccessRules {
  const record = asRecord(raw);
  if (!record) return {};
  const out: ApiKeyAccessRules = {};
  Object.entries(record).forEach(([rawKey, rawRule]) => {
    const key = rawKey.trim();
    if (!key) return;
    const ruleRecord = asRecord(rawRule) ?? {};
    const accessRaw = typeof ruleRecord.access === 'string' ? ruleRecord.access.trim().toLowerCase() : '';
    const rule: ApiKeyAccessRule = {};
    if (accessRaw === 'all') {
      rule.access = 'all';
    } else {
      const providers = normalizeStringList(ruleRecord.providers, true);
      const authFiles = normalizeStringList(ruleRecord['auth-files'], false);
      if (providers.length > 0) rule.providers = providers;
      if (authFiles.length > 0) rule.authFiles = authFiles;
    }
    out[key] = rule;
  });
  return out;
}
```

When building `newValues` after YAML parse, set:

```ts
apiKeyAccessRules: parseApiKeyAccessRules(parsed['api-key-access']),
```

- [ ] **Step 3: 增加 YAML 写回 helper**

Add near other doc helper functions:

```ts
function setApiKeyAccessRulesInDoc(doc: YamlDocument, rules: ApiKeyAccessRules): void {
  const entries = Object.entries(rules)
    .map(([key, rule]) => [key.trim(), rule] as const)
    .filter(([key]) => key.length > 0);

  if (entries.length === 0) {
    if (docHas(doc, ['api-key-access'])) doc.deleteIn(['api-key-access']);
    return;
  }

  const serialized: Record<string, unknown> = {};
  entries.forEach(([key, rule]) => {
    if (rule.access === 'all') {
      serialized[key] = { access: 'all' };
      return;
    }
    const value: Record<string, unknown> = {};
    const providers = normalizeStringList(rule.providers, true);
    const authFiles = normalizeStringList(rule.authFiles, false);
    if (providers.length > 0) value.providers = providers;
    if (authFiles.length > 0) value['auth-files'] = authFiles;
    serialized[key] = value;
  });
  doc.setIn(['api-key-access'], serialized);
}
```

In `mergeVisualConfigIntoYaml`, after `api-keys` write:

```ts
        setApiKeyAccessRulesInDoc(doc, values.apiKeyAccessRules);
```

- [ ] **Step 4: 跑类型检查确认当前改动**

Run in `/Users/kogeki/dev/Cli-Proxy-API-Management-Center`:

```bash
bun run type-check
```

Expected: PASS.

- [ ] **Step 5: 提交**

Run:

```bash
git add src/types/config.ts src/types/visualConfig.ts src/hooks/useVisualConfig.ts
git commit -m "feat: parse api key access config"
```

---

### Task 8: 管理页面 API service 和 auth target 选项

**Files:**
- Create: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/services/api/apiKeyAccess.ts`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/services/api/index.ts`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/pages/ConfigPage.tsx`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/components/config/VisualConfigEditor.tsx`

- [ ] **Step 1: 创建 service**

Create `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/services/api/apiKeyAccess.ts`:

```ts
import { apiClient } from './client';
import type { ApiKeyAccessRules } from '@/types/config';

export interface ApiKeyAccessKeyView {
  key: string;
  label: string;
  'has-rule'?: boolean;
}

export interface ApiKeyAccessAuthTarget {
  id: string;
  provider: string;
  filename?: string;
  label?: string;
  'auth-index'?: string;
  email?: string;
  'project-id'?: string;
}

export interface ApiKeyAccessResponse {
  'api-key-access'?: ApiKeyAccessRules;
  'api-keys'?: ApiKeyAccessKeyView[];
  'auth-targets'?: ApiKeyAccessAuthTarget[];
}

export const apiKeyAccessApi = {
  get: () => apiClient.get<ApiKeyAccessResponse>('/api-key-access'),
  replace: (rules: ApiKeyAccessRules) => apiClient.put('/api-key-access', { 'api-key-access': rules }),
  update: (key: string, value: ApiKeyAccessRules[string]) =>
    apiClient.patch('/api-key-access', { key, value }),
  delete: (key: string) => apiClient.delete('/api-key-access', { params: { key } }),
};
```

Modify `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/services/api/index.ts`:

```ts
export { apiKeyAccessApi } from './apiKeyAccess';
export type {
  ApiKeyAccessAuthTarget,
  ApiKeyAccessKeyView,
  ApiKeyAccessResponse,
} from './apiKeyAccess';
```

- [ ] **Step 2: ConfigPage 加载 auth target**

Modify `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/pages/ConfigPage.tsx` imports:

```ts
import { apiKeyAccessApi, type ApiKeyAccessAuthTarget } from '@/services/api';
```

Add state:

```ts
const [apiKeyAccessTargets, setApiKeyAccessTargets] = useState<ApiKeyAccessAuthTarget[]>([]);
```

In `loadConfig`, after YAML fetch succeeds, load metadata without blocking config editor:

```ts
      apiKeyAccessApi
        .get()
        .then((payload) => setApiKeyAccessTargets(payload['auth-targets'] ?? []))
        .catch(() => setApiKeyAccessTargets([]));
```

Pass to `VisualConfigEditor`:

```tsx
apiKeyAccessTargets={apiKeyAccessTargets}
```

- [ ] **Step 3: VisualConfigEditor 接收并传递**

Modify `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/components/config/VisualConfigEditor.tsx`:

```ts
import type { ApiKeyAccessAuthTarget } from '@/services/api';
```

Extend props:

```ts
  apiKeyAccessTargets?: ApiKeyAccessAuthTarget[];
```

Pass to `ApiKeysCardEditor`:

```tsx
apiKeyAccessRules={values.apiKeyAccessRules}
apiKeyAccessTargets={apiKeyAccessTargets ?? []}
onApiKeyAccessChange={(apiKeyAccessRules) => onChange({ apiKeyAccessRules })}
```

- [ ] **Step 4: 跑类型检查**

Run:

```bash
bun run type-check
```

Expected: FAIL at first if `ApiKeysCardEditor` props are not implemented yet. Keep this failure as the handoff to Task 9.

---

### Task 9: 管理页面 API key scope 编辑器

**Files:**
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/components/config/VisualConfigEditorBlocks.tsx`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/components/config/VisualConfigEditor.module.scss`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/i18n/locales/en.json`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/i18n/locales/zh-CN.json`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/i18n/locales/zh-TW.json`
- Modify: `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/i18n/locales/ru.json`

- [ ] **Step 1: 扩展 API key editor props**

Modify imports in `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/components/config/VisualConfigEditorBlocks.tsx`:

```ts
import type { ApiKeyAccessRule, ApiKeyAccessRules } from '@/types/config';
import type { ApiKeyAccessAuthTarget } from '@/services/api';
```

Extend props:

```ts
  apiKeyAccessRules?: ApiKeyAccessRules;
  apiKeyAccessTargets?: ApiKeyAccessAuthTarget[];
  onApiKeyAccessChange?: (nextRules: ApiKeyAccessRules) => void;
```

- [ ] **Step 2: 增加规则更新 helper**

Inside `ApiKeysCardEditor`:

```ts
  const accessRules = apiKeyAccessRules ?? {};

  const updateAccessRule = (key: string, rule: ApiKeyAccessRule) => {
    if (!onApiKeyAccessChange) return;
    const trimmed = key.trim();
    if (!trimmed) return;
    onApiKeyAccessChange({ ...accessRules, [trimmed]: rule });
  };

  const removeAccessRule = (key: string) => {
    if (!onApiKeyAccessChange) return;
    const trimmed = key.trim();
    if (!trimmed) return;
    const next = { ...accessRules };
    delete next[trimmed];
    onApiKeyAccessChange(next);
  };

  const renameAccessRule = (oldKey: string, newKey: string) => {
    if (!onApiKeyAccessChange) return;
    const oldTrimmed = oldKey.trim();
    const newTrimmed = newKey.trim();
    if (!oldTrimmed || !newTrimmed || oldTrimmed === newTrimmed) return;
    const next = { ...accessRules };
    if (next[oldTrimmed] && !next[newTrimmed]) {
      next[newTrimmed] = next[oldTrimmed];
    }
    delete next[oldTrimmed];
    onApiKeyAccessChange(next);
  };
```

In `handleSave`, after computing `nextKeys`:

```ts
    if (editingApiKeyId !== null && editingIndex >= 0) {
      renameAccessRule(apiKeys[editingIndex], trimmed);
    }
```

In `handleDelete`, before `updateApiKeys`:

```ts
    removeAccessRule(apiKeys[index]);
```

- [ ] **Step 3: 增加 provider 和 auth target 选项**

Inside `ApiKeysCardEditor`:

```ts
  const providerOptions = useMemo(() => {
    const providers = new Set([
      'gemini',
      'claude',
      'codex',
      'openai-compatibility',
      'vertex',
      'antigravity',
      'xai',
      'kimi',
    ]);
    apiKeyAccessTargets?.forEach((target) => {
      const provider = target.provider?.trim().toLowerCase();
      if (provider) providers.add(provider);
    });
    return Array.from(providers).sort();
  }, [apiKeyAccessTargets]);

  const authTargetOptions = useMemo(
    () =>
      (apiKeyAccessTargets ?? [])
        .filter((target) => target.id?.trim())
        .map((target) => {
          const parts = [
            target.provider,
            target['auth-index'],
            target.filename,
            target.email,
            target['project-id'],
          ].filter(Boolean);
          return {
            value: target.filename || target.id,
            label: parts.join(' · ') || target.id,
          };
        }),
    [apiKeyAccessTargets]
  );
```

- [ ] **Step 4: 渲染 scope UI**

Inside each API key row, below `item-subtitle`, render:

```tsx
                <div className={styles.apiKeyAccessScope}>
                  <Select
                    value={accessRules[key]?.access === 'all' || !accessRules[key] ? 'all' : 'restricted'}
                    onChange={(nextMode) => {
                      if (nextMode === 'all') {
                        updateAccessRule(key, { access: 'all' });
                        return;
                      }
                      updateAccessRule(key, accessRules[key] ?? { providers: [], authFiles: [] });
                    }}
                    options={[
                      { value: 'all', label: t('config_management.visual.api_keys.access_all') },
                      {
                        value: 'restricted',
                        label: t('config_management.visual.api_keys.access_restricted'),
                      },
                    ]}
                    disabled={disabled}
                  />
                  {accessRules[key] && accessRules[key].access !== 'all' && (
                    <div className={styles.apiKeyAccessPickers}>
                      <StringListEditor
                        value={accessRules[key].providers ?? []}
                        disabled={disabled}
                        placeholder={t('config_management.visual.api_keys.provider_placeholder')}
                        inputAriaLabel={t('config_management.visual.api_keys.provider_placeholder')}
                        onChange={(providers) =>
                          updateAccessRule(key, { ...accessRules[key], providers, access: undefined })
                        }
                      />
                      <StringListEditor
                        value={accessRules[key].authFiles ?? []}
                        disabled={disabled}
                        placeholder={t('config_management.visual.api_keys.auth_file_placeholder')}
                        inputAriaLabel={t('config_management.visual.api_keys.auth_file_placeholder')}
                        onChange={(authFiles) =>
                          updateAccessRule(key, { ...accessRules[key], authFiles, access: undefined })
                        }
                      />
                    </div>
                  )}
                </div>
```

If `StringListEditor` is declared after `ApiKeysCardEditor`, move `StringListEditor` above `ApiKeysCardEditor` or create a tiny local `AccessStringListEditor` before it. Keep behavior identical: stable rows, add/delete buttons, no layout shift.

- [ ] **Step 5: 样式**

Add to `/Users/kogeki/dev/Cli-Proxy-API-Management-Center/src/components/config/VisualConfigEditor.module.scss`:

```scss
.apiKeyAccessScope {
  display: grid;
  gap: 8px;
  margin-top: 10px;
}

.apiKeyAccessPickers {
  display: grid;
  gap: 8px;
  max-width: 680px;
}
```

- [ ] **Step 6: i18n**

Add under `config_management.visual.api_keys` in all locale files.

`zh-CN.json`:

```json
"access_all": "允许全部",
"access_restricted": "限制访问",
"provider_placeholder": "允许的 provider，例如 gemini",
"auth_file_placeholder": "允许的认证文件名或 auth ID"
```

`en.json`:

```json
"access_all": "Allow all",
"access_restricted": "Restricted",
"provider_placeholder": "Allowed provider, such as gemini",
"auth_file_placeholder": "Allowed auth filename or auth ID"
```

`zh-TW.json`:

```json
"access_all": "允許全部",
"access_restricted": "限制存取",
"provider_placeholder": "允許的 provider，例如 gemini",
"auth_file_placeholder": "允許的認證檔名或 auth ID"
```

`ru.json`:

```json
"access_all": "Разрешить все",
"access_restricted": "Ограничено",
"provider_placeholder": "Разрешенный provider, например gemini",
"auth_file_placeholder": "Разрешенное имя auth-файла или auth ID"
```

- [ ] **Step 7: 跑类型检查和构建**

Run:

```bash
bun run type-check
bun run build
```

Expected: PASS.

- [ ] **Step 8: 提交前端改动**

Run:

```bash
git add src/types/config.ts src/types/visualConfig.ts src/hooks/useVisualConfig.ts src/services/api/apiKeyAccess.ts src/services/api/index.ts src/pages/ConfigPage.tsx src/components/config/VisualConfigEditor.tsx src/components/config/VisualConfigEditorBlocks.tsx src/components/config/VisualConfigEditor.module.scss src/i18n/locales/en.json src/i18n/locales/zh-CN.json src/i18n/locales/zh-TW.json src/i18n/locales/ru.json
git commit -m "feat: add api key access editor"
```

---

### Task 10: 联合验证

**Files:** 后端和前端验证

- [ ] **Step 1: 后端完整目标验证**

Run in `/Users/kogeki/dev/CLIProxyAPI`:

```bash
go test ./internal/config ./internal/access/... ./internal/api/handlers/management ./internal/watcher/diff ./sdk/cliproxy/auth -count=1
go build -o test-output ./cmd/server
rm test-output
```

Expected: PASS / build 成功。

- [ ] **Step 2: 前端完整验证**

Run in `/Users/kogeki/dev/Cli-Proxy-API-Management-Center`:

```bash
bun run type-check
bun run build
```

Expected: PASS / build 成功。

- [ ] **Step 3: 手动 YAML round trip 验证**

Use the visual editor with this YAML:

```yaml
api-keys:
  - key-all
  - key-limited
api-key-access:
  key-all:
    access: all
  key-limited:
    providers:
      - gemini
    auth-files:
      - auth-1.json
```

Expected after save:

```yaml
api-keys:
  - key-all
  - key-limited
api-key-access:
  key-all:
    access: all
  key-limited:
    providers:
      - gemini
    auth-files:
      - auth-1.json
```

- [ ] **Step 4: 手动硬边界验证**

Create a config where `key-limited` is scoped to `auth-1.json`, while `auth-2.json` supports the same target model. Make a request with `key-limited` for a model unavailable on `auth-1.json`.

Expected:

```text
The request does not use auth-2.json.
The response is auth_not_found, auth_unavailable, or model_cooldown inside the scoped auth set.
```

- [ ] **Step 5: 最终状态检查**

Run in both repos:

```bash
git status --short
git log --oneline -5
```

Expected: only intended commits are present; `.codegraph/` remains untracked in the backend repo and is not committed.
