package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenCodeGoConfigYAMLRoundTrip(t *testing.T) {
	const raw = `
opencode-go:
  provider-name: opencode-go
  base-url: https://api.opencode.example/v1
  accounts:
    - id: acc_1
      alias: main
      email: user@example.com
      username: user
      workspace-id: ws_123
      api-key: sk-test1234567890
      cookie: session=value
      api-key-synced: true
      provider-key-managed: true
      provider-name: opencode-go
      provider-sync-error: ""
      usage:
        rolling:
          used: 12
          limit: 100
          reset-at: "2026-06-25T00:00:00Z"
        weekly:
          used: 34
          limit: 200
        monthly:
          used: 56
          limit: 500
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal opencode-go config: %v", err)
	}
	if cfg.OpenCodeGo.ProviderName != "opencode-go" {
		t.Fatalf("provider-name = %q", cfg.OpenCodeGo.ProviderName)
	}
	if got := len(cfg.OpenCodeGo.Accounts); got != 1 {
		t.Fatalf("accounts len = %d, want 1", got)
	}
	account := cfg.OpenCodeGo.Accounts[0]
	if account.WorkspaceID != "ws_123" || account.APIKey != "sk-test1234567890" || account.Cookie != "session=value" {
		t.Fatalf("unexpected account: %#v", account)
	}
	if !account.ProviderKeyManaged {
		t.Fatalf("provider-key-managed = false, want true")
	}

	out, err := yaml.Marshal(&cfg)
	if err != nil {
		t.Fatalf("marshal opencode-go config: %v", err)
	}
	text := string(out)
	for _, want := range []string{
		"opencode-go:",
		"provider-name: opencode-go",
		"workspace-id: ws_123",
		"api-key: sk-test1234567890",
		"provider-key-managed: true",
		"rolling:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("marshaled yaml missing %q:\n%s", want, text)
		}
	}
}

func TestOpenCodeGoConfigCloneDoesNotShareAccounts(t *testing.T) {
	cfg := &Config{
		OpenCodeGo: OpenCodeGoConfig{
			ProviderName: "opencode-go",
			BaseURL:      "https://api.opencode.example/v1",
			Accounts: []OpenCodeGoAccount{{
				ID:          "acc_1",
				WorkspaceID: "ws_123",
				APIKey:      "sk-test1234567890",
				Usage: OpenCodeGoUsageSnapshot{
					Rolling: OpenCodeGoUsageWindow{Used: 1, Limit: 10},
				},
			}},
		},
	}
	clone := cfg.CloneForRuntime()
	cfg.OpenCodeGo.Accounts[0].APIKey = "mutated"
	cfg.OpenCodeGo.Accounts[0].Usage.Rolling.Used = 9

	if clone.OpenCodeGo.Accounts[0].APIKey != "sk-test1234567890" {
		t.Fatalf("clone api key = %q", clone.OpenCodeGo.Accounts[0].APIKey)
	}
	if clone.OpenCodeGo.Accounts[0].Usage.Rolling.Used != 1 {
		t.Fatalf("clone rolling used = %v", clone.OpenCodeGo.Accounts[0].Usage.Rolling.Used)
	}
}
