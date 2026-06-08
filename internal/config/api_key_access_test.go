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
	if got, want := cfg.APIKeyAccess["key-limited"].Providers, []string{"claude", "gemini"}; !equalStringSlices(got, want) {
		t.Fatalf("providers = %#v, want %#v", got, want)
	}
	if got, want := cfg.APIKeyAccess["key-limited"].AuthFiles, []string{"claude-a.json", "gemini-b.json"}; !equalStringSlices(got, want) {
		t.Fatalf("auth-files = %#v, want %#v", got, want)
	}
	if _, ok := cfg.APIKeyAccess["key-empty"]; !ok {
		t.Fatalf("empty restricted rule should be retained")
	}
	if got := cfg.APIKeyAccess["key-all"]; got.Access != APIKeyAccessAll || len(got.Providers) != 0 || len(got.AuthFiles) != 0 {
		t.Fatalf("access all rule = %#v, want only access=all", got)
	}

	rendered, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if !strings.Contains(string(rendered), "api-key-access:") {
		t.Fatalf("rendered YAML does not include api-key-access:\n%s", rendered)
	}
	for _, fragment := range []string{
		"access: all",
		"- claude-a.json",
		"- gemini-b.json",
		"key-empty: {}",
	} {
		if !strings.Contains(string(rendered), fragment) {
			t.Fatalf("rendered YAML does not include %q:\n%s", fragment, rendered)
		}
	}
}

func TestParseConfigBytes_APIKeyAccess(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte(`
api-key-access:
  key-all:
    access: " ALL "
    providers: ["Claude"]
    auth-files: ["claude-a.json"]
  key-limited:
    providers: [" Claude ", "claude", "GEMINI", ""]
    auth-files:
      - " claude-a.json "
      - "claude-a.json"
      - "gemini-b.json"
      - ""
  key-empty: {}
  " ":
    providers: ["gemini"]
`))
	if err != nil {
		t.Fatalf("ParseConfigBytes() error = %v", err)
	}

	if _, ok := cfg.APIKeyAccess[" "]; ok {
		t.Fatalf("blank key rule was retained")
	}
	if _, ok := cfg.APIKeyAccess["key-empty"]; !ok {
		t.Fatalf("empty restricted rule should be retained")
	}
	if got := cfg.APIKeyAccess["key-all"]; got.Access != APIKeyAccessAll || len(got.Providers) != 0 || len(got.AuthFiles) != 0 {
		t.Fatalf("access all rule = %#v, want only access=all", got)
	}
	if got, want := cfg.APIKeyAccess["key-limited"].Providers, []string{"claude", "gemini"}; !equalStringSlices(got, want) {
		t.Fatalf("providers = %#v, want %#v", got, want)
	}
	if got, want := cfg.APIKeyAccess["key-limited"].AuthFiles, []string{"claude-a.json", "gemini-b.json"}; !equalStringSlices(got, want) {
		t.Fatalf("auth-files = %#v, want %#v", got, want)
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
