package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	proxyconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestServeNextControlPanel(t *testing.T) {
	dir := t.TempDir()
	staticDir := filepath.Join(dir, "static")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "next.html"), []byte("<html>next</html>"), 0o644); err != nil {
		t.Fatalf("write next.html: %v", err)
	}

	t.Setenv("MANAGEMENT_STATIC_PATH", staticDir)

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"test-key"}},
		Debug:     true,
	}
	server := newTestServerWithConfig(t, cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/next.html", nil)
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "<html>next</html>" {
		t.Fatalf("body = %q, want %q", got, "<html>next</html>")
	}
}
