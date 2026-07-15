package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

const (
	defaultCodexClientVersion          = "0.144.1"
	defaultCodexModelsUserAgent        = "codex_cli_rs/0.144.1"
	defaultCodexModelsOriginator       = "codex_cli_rs"
	codexOfficialModelsRefreshInterval = 5 * time.Minute
	codexOfficialModelsRetryInterval   = 30 * time.Second
	codexOfficialModelsRequestTimeout  = 8 * time.Second
	codexOfficialModelsMaxResponseSize = 8 << 20
)

var codexOfficialModelsURL = "https://chatgpt.com/backend-api/codex/models"

// RefreshCodexClientModels refreshes the runtime client catalog from the
// official Codex models endpoint. Failures retain the last validated catalog so
// model discovery remains available when the official endpoint is unreachable.
func (h *OpenAIAPIHandler) RefreshCodexClientModels(ctx context.Context, clientVersion string, requestHeaders http.Header) {
	if h == nil || h.AuthManager == nil {
		return
	}

	clientVersion = normalizeCodexClientVersion(clientVersion)
	h.codexModelsRefreshMu.Lock()
	defer h.codexModelsRefreshMu.Unlock()

	now := time.Now()
	if h.codexModelsRefreshVersion == clientVersion && now.Before(h.codexModelsRefreshAfter) {
		return
	}
	h.codexModelsRefreshVersion = clientVersion
	h.codexModelsRefreshAfter = now.Add(codexOfficialModelsRetryInterval)

	if ctx == nil {
		ctx = context.Background()
	}
	refreshCtx, cancel := context.WithTimeout(ctx, codexOfficialModelsRequestTimeout)
	defer cancel()

	data, err := h.fetchOfficialCodexClientModels(refreshCtx, clientVersion, requestHeaders)
	if err != nil {
		log.Debugf("official Codex client model refresh failed, keeping current catalog: %v", err)
		return
	}

	changed, err := registry.UpdateCodexClientModelsFromOfficial(data, "official Codex models endpoint")
	if err != nil {
		log.Debugf("official Codex client model catalog rejected, keeping current catalog: %v", err)
		return
	}
	h.codexModelsRefreshAfter = time.Now().Add(codexOfficialModelsRefreshInterval)
	if changed {
		log.Infof("official Codex client model catalog updated (client_version=%s)", clientVersion)
	} else {
		log.Debugf("official Codex client model catalog is current (client_version=%s)", clientVersion)
	}
}

func (h *OpenAIAPIHandler) fetchOfficialCodexClientModels(ctx context.Context, clientVersion string, requestHeaders http.Header) ([]byte, error) {
	auths := h.AuthManager.List()
	attempted := false
	var lastErr error
	for _, auth := range auths {
		if !isCodexOAuthAuth(auth) {
			continue
		}
		attempted = true
		data, err := h.fetchOfficialCodexClientModelsWithAuth(ctx, auth, clientVersion, requestHeaders)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if !attempted {
		return nil, fmt.Errorf("no enabled Codex OAuth credential is available")
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no Codex OAuth credential completed the request")
	}
	return nil, lastErr
}

func (h *OpenAIAPIHandler) fetchOfficialCodexClientModelsWithAuth(ctx context.Context, auth *coreauth.Auth, clientVersion string, requestHeaders http.Header) ([]byte, error) {
	targetURL, err := codexOfficialModelsRequestURL(clientVersion)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Close = true
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+codexAuthMetadataString(auth, "access_token"))
	if accountID := codexAuthMetadataString(auth, "account_id"); accountID != "" {
		req.Header.Set("ChatGPT-Account-ID", accountID)
	}
	copyCodexModelsClientHeaders(req.Header, requestHeaders)

	proxyURL := strings.TrimSpace(auth.ProxyURL)
	if proxyURL == "" && h.Cfg != nil {
		proxyURL = strings.TrimSpace(h.Cfg.ProxyURL)
	}
	transport, _, err := proxyutil.BuildHTTPTransport(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("build Codex models transport: %w", err)
	}
	client := &http.Client{Timeout: codexOfficialModelsRequestTimeout}
	if transport != nil {
		client.Transport = transport
		defer transport.CloseIdleConnections()
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request official Codex models: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, codexOfficialModelsMaxResponseSize+1))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read official Codex models response: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close official Codex models response: %w", closeErr)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("official Codex models returned status %d", resp.StatusCode)
	}
	if len(data) > codexOfficialModelsMaxResponseSize {
		return nil, fmt.Errorf("official Codex models response exceeds %d bytes", codexOfficialModelsMaxResponseSize)
	}
	return data, nil
}

func normalizeCodexClientVersion(clientVersion string) string {
	clientVersion = strings.TrimSpace(clientVersion)
	if clientVersion == "" {
		return defaultCodexClientVersion
	}
	if len(clientVersion) > 128 {
		return clientVersion[:128]
	}
	return clientVersion
}

func codexOfficialModelsRequestURL(clientVersion string) (string, error) {
	targetURL, err := url.Parse(codexOfficialModelsURL)
	if err != nil {
		return "", fmt.Errorf("parse official Codex models URL: %w", err)
	}
	query := targetURL.Query()
	query.Set("client_version", normalizeCodexClientVersion(clientVersion))
	targetURL.RawQuery = query.Encode()
	return targetURL.String(), nil
}

func isCodexOAuthAuth(auth *coreauth.Auth) bool {
	if auth == nil || auth.Disabled || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		return false
	}
	return codexAuthMetadataString(auth, "access_token") != ""
}

func codexAuthMetadataString(auth *coreauth.Auth, key string) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	value, _ := auth.Metadata[key].(string)
	return strings.TrimSpace(value)
}

func copyCodexModelsClientHeaders(destination, source http.Header) {
	for _, headerName := range []string{"User-Agent", "Originator", "Version", "X-Codex-Beta-Features"} {
		if value := strings.TrimSpace(source.Get(headerName)); value != "" {
			destination.Set(headerName, value)
		}
	}
	if destination.Get("User-Agent") == "" {
		destination.Set("User-Agent", defaultCodexModelsUserAgent)
	}
	if destination.Get("Originator") == "" {
		destination.Set("Originator", defaultCodexModelsOriginator)
	}
}
