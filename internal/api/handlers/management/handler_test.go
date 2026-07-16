package management

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	"golang.org/x/crypto/bcrypt"
)

func TestAuthenticateManagementKey_LocalhostIPBan_BlocksCorrectKeyDuringBan(t *testing.T) {
	h := &Handler{
		cfg:            &config.Config{},
		failedAttempts: make(map[string]*attemptInfo),
		envSecret:      "test-secret",
	}

	for i := 0; i < 5; i++ {
		allowed, statusCode, errMsg := h.AuthenticateManagementKey("127.0.0.1", true, "wrong-secret")
		if allowed {
			t.Fatalf("expected auth to be denied at attempt %d", i+1)
		}
		if statusCode != http.StatusUnauthorized || errMsg != "invalid management key" {
			t.Fatalf("unexpected auth failure at attempt %d: status=%d msg=%q", i+1, statusCode, errMsg)
		}
	}

	allowed, statusCode, errMsg := h.AuthenticateManagementKey("127.0.0.1", true, "test-secret")
	if allowed {
		t.Fatalf("expected correct key to be denied while banned")
	}
	if statusCode != http.StatusForbidden {
		t.Fatalf("expected forbidden status while banned, got %d", statusCode)
	}
	if !strings.HasPrefix(errMsg, "IP banned due to too many failed attempts. Try again in") {
		t.Fatalf("unexpected banned message: %q", errMsg)
	}
}

func TestAuthenticateManagementKey_CachedBcryptRejectsWrongKeyAndInvalidatesOnRotation(t *testing.T) {
	hash := func(secret string) string {
		t.Helper()
		value, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
		if err != nil {
			t.Fatalf("hash management key: %v", err)
		}
		return string(value)
	}
	configFor := func(secret string) *config.Config {
		return &config.Config{RemoteManagement: config.RemoteManagement{
			AllowRemote: true,
			SecretKey:   hash(secret),
		}}
	}

	h := &Handler{
		cfg:            configFor("first-secret"),
		failedAttempts: make(map[string]*attemptInfo),
	}
	if allowed, _, _ := h.AuthenticateManagementKey("203.0.113.1", false, "first-secret"); !allowed {
		t.Fatal("first management key was rejected")
	}
	if allowed, status, _ := h.AuthenticateManagementKey("203.0.113.1", false, "wrong-secret"); allowed || status != http.StatusUnauthorized {
		t.Fatalf("wrong key result: allowed=%v status=%d", allowed, status)
	}

	h.SetConfig(configFor("second-secret"))
	if allowed, status, _ := h.AuthenticateManagementKey("203.0.113.2", false, "first-secret"); allowed || status != http.StatusUnauthorized {
		t.Fatalf("rotated old key result: allowed=%v status=%d", allowed, status)
	}
	if allowed, _, _ := h.AuthenticateManagementKey("203.0.113.2", false, "second-secret"); !allowed {
		t.Fatal("rotated management key was rejected")
	}
}

func TestAuthenticateManagementKey_ConcurrentValidBcryptRequests(t *testing.T) {
	secret := "concurrent-secret"
	secretHash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash management key: %v", err)
	}
	h := &Handler{
		cfg: &config.Config{RemoteManagement: config.RemoteManagement{
			AllowRemote: true,
			SecretKey:   string(secretHash),
		}},
		failedAttempts: make(map[string]*attemptInfo),
	}

	var wg sync.WaitGroup
	errors := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, status, message := h.AuthenticateManagementKey("203.0.113.3", false, secret)
			if !allowed || status != 0 || message != "" {
				errors <- message
			}
		}()
	}
	wg.Wait()
	close(errors)
	for message := range errors {
		t.Fatalf("concurrent authentication failed: %s", message)
	}
}

func TestMiddlewareSetsSupportPluginHeader(t *testing.T) {

	h := &Handler{
		cfg:            &config.Config{},
		failedAttempts: make(map[string]*attemptInfo),
		envSecret:      "test-secret",
	}
	middleware := h.Middleware()

	t.Run("invalid key", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/config", nil)
		c.Request.RemoteAddr = "127.0.0.1:12345"
		c.Request.Header.Set("X-Management-Key", "wrong-secret")

		middleware(c)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
		if got := rec.Header().Get("X-CPA-SUPPORT-PLUGIN"); got != pluginhost.SupportPluginHeaderValue() {
			t.Fatalf("X-CPA-SUPPORT-PLUGIN = %q, want %q", got, pluginhost.SupportPluginHeaderValue())
		}
	})

	t.Run("valid key", func(t *testing.T) {
		engine := gin.New()
		engine.GET("/v0/management/config", middleware, func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v0/management/config", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("X-Management-Key", "test-secret")
		engine.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("X-CPA-SUPPORT-PLUGIN"); got != pluginhost.SupportPluginHeaderValue() {
			t.Fatalf("X-CPA-SUPPORT-PLUGIN = %q, want %q", got, pluginhost.SupportPluginHeaderValue())
		}
	})
}

func TestLatestReleaseAPIURLUsesForkRepository(t *testing.T) {
	got := latestReleaseAPIURL()
	want := "https://api.github.com/repos/kogekiplay/CLIProxyAPI/releases/latest"
	if got != want {
		t.Fatalf("latest release API URL = %q, want %q", got, want)
	}
}

func TestLatestVersionFallsBackToForkTagsWhenReleaseIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/kogekiplay/CLIProxyAPI/releases/latest":
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		case "/repos/kogekiplay/CLIProxyAPI/tags":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"name":"v7.2.40-fork"}]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	version, err := fetchLatestVersion(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchLatestVersion() error = %v", err)
	}
	if version != "v7.2.40-fork" {
		t.Fatalf("version = %q, want v7.2.40-fork", version)
	}
}
