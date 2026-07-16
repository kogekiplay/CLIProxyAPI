package managementasset

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestAutoUpdateSkipReason(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *config.Config
		wantReason string
		wantSkip   bool
	}{
		{
			name:       "nil config",
			cfg:        nil,
			wantReason: "config not yet available",
			wantSkip:   true,
		},
		{
			name: "cluster mode",
			cfg: &config.Config{
				Home: config.HomeConfig{Enabled: true},
			},
			wantReason: "cluster mode enabled",
			wantSkip:   true,
		},
		{
			name: "control panel disabled",
			cfg: &config.Config{
				RemoteManagement: config.RemoteManagement{DisableControlPanel: true},
			},
			wantReason: "control panel disabled",
			wantSkip:   true,
		},
		{
			name: "auto update disabled",
			cfg: &config.Config{
				RemoteManagement: config.RemoteManagement{DisableAutoUpdatePanel: true},
			},
			wantReason: "disable-auto-update-panel is enabled",
			wantSkip:   true,
		},
		{
			name:       "enabled",
			cfg:        &config.Config{},
			wantReason: "",
			wantSkip:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReason, gotSkip := autoUpdateSkipReason(tt.cfg)
			if gotReason != tt.wantReason || gotSkip != tt.wantSkip {
				t.Fatalf("autoUpdateSkipReason() = (%q, %t), want (%q, %t)", gotReason, gotSkip, tt.wantReason, tt.wantSkip)
			}
		})
	}
}

func TestDownloadReleaseAssetPrefersAPIURL(t *testing.T) {
	const body = "<html>management</html>"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api-asset":
			if !strings.Contains(r.Header.Get("Accept"), "application/octet-stream") {
				t.Fatalf("Accept = %q, want application/octet-stream", r.Header.Get("Accept"))
			}
			_, _ = w.Write([]byte(body))
		case "/browser-asset":
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	data, _, err := downloadReleaseAsset(context.Background(), server.Client(), &releaseAsset{
		URL:                server.URL + "/api-asset",
		BrowserDownloadURL: server.URL + "/browser-asset",
	})
	if err != nil {
		t.Fatalf("downloadReleaseAsset() error = %v", err)
	}
	if string(data) != body {
		t.Fatalf("downloadReleaseAsset() body = %q, want %q", string(data), body)
	}
}

func TestInstallManagementBundle(t *testing.T) {
	staticDir := t.TempDir()
	bundle := buildManagementBundle(t, map[string]string{
		"management.html":                    `<script type="module" src="/management-assets/v-test/app.js"></script>`,
		"management-assets/v-test/app.js":    `console.log("bundle")`,
		"management-assets/v-test/style.css": `body { color: black; }`,
	})

	if err := installManagementBundle(staticDir, bundle); err != nil {
		t.Fatalf("installManagementBundle() error = %v", err)
	}

	html, err := os.ReadFile(filepath.Join(staticDir, managementAssetName))
	if err != nil {
		t.Fatalf("read management html: %v", err)
	}
	if !strings.Contains(string(html), "/management-assets/v-test/app.js") {
		t.Fatalf("management html = %q", string(html))
	}
	asset, err := os.ReadFile(filepath.Join(staticDir, managementAssetsDirName, "v-test", "app.js"))
	if err != nil {
		t.Fatalf("read management asset: %v", err)
	}
	if string(asset) != `console.log("bundle")` {
		t.Fatalf("management asset = %q", string(asset))
	}
}

func TestExtractManagementBundleRejectsTraversal(t *testing.T) {
	bundle := buildManagementBundle(t, map[string]string{
		"management.html": "<html></html>",
		"../escape.js":    "nope",
	})

	err := extractManagementBundle(bundle, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "invalid management bundle path") {
		t.Fatalf("extractManagementBundle() error = %v, want traversal rejection", err)
	}
}

func TestFetchLatestAssetsFindsBundleAndStandalone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"assets":[{"name":"management.html","digest":"sha256:abc"},{"name":"management-bundle.tar.gz","digest":"sha256:def"}]}`))
	}))
	defer server.Close()

	assets, err := fetchLatestAssets(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchLatestAssets() error = %v", err)
	}
	if assets.bundle == nil || assets.bundleHash != "def" {
		t.Fatalf("bundle = %#v hash=%q", assets.bundle, assets.bundleHash)
	}
	if assets.standalone == nil || assets.standaloneHash != "abc" {
		t.Fatalf("standalone = %#v hash=%q", assets.standalone, assets.standaloneHash)
	}
}

func buildManagementBundle(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, body := range files {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(body)),
		}); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tarWriter.Write([]byte(body)); err != nil {
			t.Fatalf("write tar body: %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buffer.Bytes()
}
