package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"golang.org/x/net/context"
)

func TestRequestExecutionMetadataIncludesExecutionSessionWithoutIdempotencyKey(t *testing.T) {
	ctx := WithExecutionSessionID(context.Background(), "session-1")

	meta := requestExecutionMetadata(ctx)
	if got := meta[coreexecutor.ExecutionSessionMetadataKey]; got != "session-1" {
		t.Fatalf("ExecutionSessionMetadataKey = %v, want %q", got, "session-1")
	}
	if _, ok := meta[idempotencyKeyMetadataKey]; ok {
		t.Fatalf("unexpected idempotency key in metadata: %v", meta[idempotencyKeyMetadataKey])
	}
}

func TestRequestExecutionMetadataTraceCallbackWebsocketDetection(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("skips websocket upgrade", func(t *testing.T) {
		ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		ginCtx.Request.Header.Set("Connection", "Upgrade")
		ginCtx.Request.Header.Set("Upgrade", "websocket")
		logging.SetGinRequestID(ginCtx, "1234abcd")
		ctx := context.WithValue(context.Background(), "gin", ginCtx)

		meta := requestExecutionMetadata(ctx)

		if _, exists := meta[coreexecutor.SelectedAuthIndexCallbackMetadataKey]; exists {
			t.Fatal("unexpected selected auth index callback for websocket upgrade")
		}
	})

	t.Run("keeps callback for incomplete upgrade headers", func(t *testing.T) {
		ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		ginCtx.Request.Header.Set("Upgrade", "websocket")
		logging.SetGinRequestID(ginCtx, "1234abcd")
		ctx := context.WithValue(context.Background(), "gin", ginCtx)

		meta := requestExecutionMetadata(ctx)

		if _, exists := meta[coreexecutor.SelectedAuthIndexCallbackMetadataKey]; !exists {
			t.Fatal("missing selected auth index callback for ordinary HTTP request")
		}
	})
}

func TestSetReasoningEffortMetadataUsesSuffixOverBody(t *testing.T) {
	meta := make(map[string]any)

	setReasoningEffortMetadata(meta, "openai", "gpt-5.4(high)", []byte(`{"reasoning_effort":"low"}`))

	if got := meta[coreexecutor.ReasoningEffortMetadataKey]; got != "high" {
		t.Fatalf("ReasoningEffortMetadataKey = %v, want %q", got, "high")
	}
}

func TestSetReasoningEffortMetadataUsesMaxSuffixOverLowerBody(t *testing.T) {
	meta := make(map[string]any)

	setReasoningEffortMetadata(meta, "openai", "alias(max)", []byte(`{"reasoning_effort":"low"}`))

	if got := meta[coreexecutor.ReasoningEffortMetadataKey]; got != "max" {
		t.Fatalf("ReasoningEffortMetadataKey = %v, want %q", got, "max")
	}
}

func TestSetReasoningEffortMetadataSupportsOpenAIResponses(t *testing.T) {
	meta := make(map[string]any)

	setReasoningEffortMetadata(meta, "openai-response", "gpt-5.4", []byte(`{"reasoning":{"effort":"medium"}}`))

	if got := meta[coreexecutor.ReasoningEffortMetadataKey]; got != "medium" {
		t.Fatalf("ReasoningEffortMetadataKey = %v, want %q", got, "medium")
	}
}

func TestSetServiceTierMetadataExtractsValue(t *testing.T) {
	meta := make(map[string]any)

	setServiceTierMetadata(meta, []byte(`{"service_tier":"priority"}`))

	gotServiceTier := meta[coreexecutor.ServiceTierMetadataKey]
	if gotServiceTier != "priority" {
		t.Fatalf("ServiceTierMetadataKey = %v, want %q", gotServiceTier, "priority")
	}
}

func TestSetServiceTierMetadataDefaultsWhenMissing(t *testing.T) {
	meta := make(map[string]any)

	setServiceTierMetadata(meta, []byte(`{"model":"gpt-5.4"}`))

	gotServiceTier := meta[coreexecutor.ServiceTierMetadataKey]
	if gotServiceTier != "auto" {
		t.Fatalf("ServiceTierMetadataKey = %v, want %q", gotServiceTier, "auto")
	}
}

func TestSetServiceTierMetadataPreservesExplicitDefault(t *testing.T) {
	meta := make(map[string]any)

	setServiceTierMetadata(meta, []byte(`{"service_tier":"default"}`))

	if gotServiceTier := meta[coreexecutor.ServiceTierMetadataKey]; gotServiceTier != "default" {
		t.Fatalf("ServiceTierMetadataKey = %v, want %q", gotServiceTier, "default")
	}
}

type reasoningEffortMetadataCaptureExecutor struct {
	modelExecutionCaptureExecutor
}

func (e *reasoningEffortMetadataCaptureExecutor) CountTokens(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.capture(req, opts)
	return coreexecutor.Response{Payload: []byte("0")}, nil
}

func newReasoningEffortMetadataHandler(t *testing.T, executor *reasoningEffortMetadataCaptureExecutor) *BaseAPIHandler {
	t.Helper()
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	auth := &coreauth.Auth{
		ID:       "reasoning-effort-metadata-auth",
		Provider: executor.Identifier(),
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "reasoning-effort-metadata@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("manager.Register(): %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "canonical-model"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	})
	return NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
}

func TestReasoningEffortMetadataUsesOriginalRequestedModelAcrossExecutionPaths(t *testing.T) {
	const requestedModel = "client-alias(high)"
	const canonicalModel = "canonical-model"
	body := []byte(`{"model":"client-alias"}`)

	newProviderRouteHost := func() *handlerModelRouterTestHost {
		host := &handlerModelRouterTestHost{hasRouters: true}
		host.route = func(context.Context, pluginapi.ModelRouteRequest, string) (pluginapi.ModelRouteResponse, bool) {
			return pluginapi.ModelRouteResponse{
				Handled:     true,
				TargetKind:  pluginapi.ModelRouteTargetProvider,
				Target:      "codex",
				TargetModel: canonicalModel,
			}, true
		}
		return host
	}

	t.Run("non streaming", func(t *testing.T) {
		executor := &reasoningEffortMetadataCaptureExecutor{modelExecutionCaptureExecutor: modelExecutionCaptureExecutor{provider: "codex"}}
		handler := newReasoningEffortMetadataHandler(t, executor)
		handler.SetModelRouterHost(newProviderRouteHost())

		if _, _, errMsg := handler.ExecuteWithAuthManager(context.Background(), "openai", requestedModel, body, ""); errMsg != nil {
			t.Fatalf("ExecuteWithAuthManager() error = %+v", errMsg)
		}
		_, opts := executor.captured()
		if got := opts.Metadata[coreexecutor.ReasoningEffortMetadataKey]; got != "high" {
			t.Fatalf("reasoning effort metadata = %#v, want high", got)
		}
	})

	t.Run("count", func(t *testing.T) {
		executor := &reasoningEffortMetadataCaptureExecutor{modelExecutionCaptureExecutor: modelExecutionCaptureExecutor{provider: "codex"}}
		handler := newReasoningEffortMetadataHandler(t, executor)
		handler.SetModelRouterHost(newProviderRouteHost())

		if _, _, errMsg := handler.ExecuteCountWithAuthManager(context.Background(), "openai", requestedModel, body, ""); errMsg != nil {
			t.Fatalf("ExecuteCountWithAuthManager() error = %+v", errMsg)
		}
		_, opts := executor.captured()
		if got := opts.Metadata[coreexecutor.ReasoningEffortMetadataKey]; got != "high" {
			t.Fatalf("reasoning effort metadata = %#v, want high", got)
		}
	})

	t.Run("plugin executor", func(t *testing.T) {
		host := &handlerDirectExecutorRouteHost{}
		handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
		handler.SetModelRouterHost(host)

		if _, _, errMsg := handler.executeWithPluginExecutor(context.Background(), "openai", "openai", canonicalModel, requestedModel, body, "", "plugin", modelExecutionOptions{}); errMsg != nil {
			t.Fatalf("executeWithPluginExecutor() error = %+v", errMsg)
		}
		if got := host.lastOptions.Metadata[coreexecutor.ReasoningEffortMetadataKey]; got != "high" {
			t.Fatalf("reasoning effort metadata = %#v, want high", got)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		executor := &reasoningEffortMetadataCaptureExecutor{modelExecutionCaptureExecutor: modelExecutionCaptureExecutor{
			provider: "codex",
			stream: func(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
				chunks := make(chan coreexecutor.StreamChunk, 1)
				chunks <- coreexecutor.StreamChunk{Payload: []byte("stream")}
				close(chunks)
				return &coreexecutor.StreamResult{Chunks: chunks}, nil
			},
		}}
		handler := newReasoningEffortMetadataHandler(t, executor)
		handler.SetModelRouterHost(newProviderRouteHost())

		dataChan, _, errChan := handler.ExecuteStreamWithAuthManager(context.Background(), "openai", requestedModel, body, "")
		for range dataChan {
		}
		if errMsg := <-errChan; errMsg != nil {
			t.Fatalf("ExecuteStreamWithAuthManager() error = %+v", errMsg)
		}
		_, opts := executor.captured()
		if got := opts.Metadata[coreexecutor.ReasoningEffortMetadataKey]; got != "high" {
			t.Fatalf("reasoning effort metadata = %#v, want high", got)
		}
	})
}

func TestSetGenerateMetadataDefaultsWhenMissing(t *testing.T) {
	meta := make(map[string]any)

	setGenerateMetadata(meta, []byte(`{"model":"gpt-5.4"}`))

	if got := meta[coreexecutor.GenerateMetadataKey]; got != true {
		t.Fatalf("GenerateMetadataKey = %v, want true", got)
	}
}

func TestSetGenerateMetadataPreservesTrue(t *testing.T) {
	meta := make(map[string]any)

	setGenerateMetadata(meta, []byte(`{"generate":true}`))

	if got := meta[coreexecutor.GenerateMetadataKey]; got != true {
		t.Fatalf("GenerateMetadataKey = %v, want true", got)
	}
}

func TestSetGenerateMetadataHonorsExplicitFalse(t *testing.T) {
	meta := make(map[string]any)

	setGenerateMetadata(meta, []byte(`{"generate":false}`))

	if got := meta[coreexecutor.GenerateMetadataKey]; got != false {
		t.Fatalf("GenerateMetadataKey = %v, want false", got)
	}
}
