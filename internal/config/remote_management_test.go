package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConfigBytesMigratesLegacyDefaultManagementPanelRepository(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte(`
remote-management:
  panel-github-repository: "https://github.com/router-for-me/Cli-Proxy-API-Management-Center"
`))
	if err != nil {
		t.Fatalf("ParseConfigBytes() error = %v", err)
	}

	if got := cfg.RemoteManagement.PanelGitHubRepository; got != DefaultPanelGitHubRepository {
		t.Fatalf("PanelGitHubRepository = %q, want %q", got, DefaultPanelGitHubRepository)
	}
}

func TestLoadConfigOptionalMigratesLegacyDefaultManagementPanelRepository(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
remote-management:
  panel-github-repository: "https://github.com/router-for-me/Cli-Proxy-API-Management-Center"
`), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	if got := cfg.RemoteManagement.PanelGitHubRepository; got != DefaultPanelGitHubRepository {
		t.Fatalf("PanelGitHubRepository = %q, want %q", got, DefaultPanelGitHubRepository)
	}
}

func TestParseConfigBytesKeepsCustomManagementPanelRepository(t *testing.T) {
	const customRepo = "https://github.com/example/custom-panel"
	cfg, err := ParseConfigBytes([]byte(`
remote-management:
  panel-github-repository: "https://github.com/example/custom-panel"
`))
	if err != nil {
		t.Fatalf("ParseConfigBytes() error = %v", err)
	}

	if got := cfg.RemoteManagement.PanelGitHubRepository; got != customRepo {
		t.Fatalf("PanelGitHubRepository = %q, want %q", got, customRepo)
	}
}
