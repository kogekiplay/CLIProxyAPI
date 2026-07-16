package usage

import (
	"context"
	"testing"
	"time"
)

type usageContextTestKey struct{}

type usageContextCapturePlugin struct {
	ctxs chan context.Context
}

func (p *usageContextCapturePlugin) HandleUsage(ctx context.Context, record Record) {
	p.ctxs <- ctx
}

func TestManagerAddsBoundedDeadlineToPluginContext(t *testing.T) {
	manager := NewManager(1)
	defer manager.Stop()

	plugin := &usageContextCapturePlugin{ctxs: make(chan context.Context, 1)}
	manager.Register(plugin)

	ctx := context.WithValue(context.Background(), usageContextTestKey{}, "request-id")
	manager.Publish(ctx, Record{Model: "gpt-test"})

	var got context.Context
	select {
	case got = <-plugin.ctxs:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for usage plugin context")
	}

	if _, ok := got.Deadline(); !ok {
		t.Fatal("plugin context has no deadline")
	}
	if value := got.Value(usageContextTestKey{}); value != "request-id" {
		t.Fatalf("plugin context value = %v, want request-id", value)
	}
}

func TestGenerateEnabledDefaultsNilToTrue(t *testing.T) {
	if !GenerateEnabled(nil) {
		t.Fatalf("GenerateEnabled(nil) = false, want true")
	}
}

func TestGenerateEnabledHonorsExplicitFalse(t *testing.T) {
	if GenerateEnabled(GenerateFlag(false)) {
		t.Fatalf("GenerateEnabled(false) = true, want false")
	}
}

func TestGenerateEnabledHonorsExplicitTrue(t *testing.T) {
	if !GenerateEnabled(GenerateFlag(true)) {
		t.Fatalf("GenerateEnabled(true) = false, want true")
	}
}

func TestGenerateFromContextDefaultsMissingToTrue(t *testing.T) {
	if !GenerateFromContext(context.Background()) {
		t.Fatalf("GenerateFromContext(background) = false, want true")
	}
}

func TestGenerateFromContextHonorsExplicitFalse(t *testing.T) {
	ctx := WithGenerate(context.Background(), false)
	if GenerateFromContext(ctx) {
		t.Fatalf("GenerateFromContext(false) = true, want false")
	}
}

func TestRecordOmittedGenerateIsEnabled(t *testing.T) {
	// Existing callers construct Record without setting Generate. Omission must
	// remain distinguishable from explicit false and default to true.
	record := Record{
		Provider: "openai",
		Model:    "gpt-5.4",
	}
	if record.Generate != nil {
		t.Fatalf("Record.Generate = %v, want nil for omitted field", record.Generate)
	}
	if !GenerateEnabled(record.Generate) {
		t.Fatalf("GenerateEnabled(omitted) = false, want true")
	}
}
