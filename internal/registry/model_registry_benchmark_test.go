package registry

import (
	"fmt"
	"strings"
	"testing"
)

func benchmarkScopedModelRegistry(b *testing.B, totalClients int) (*ModelRegistry, []string, string) {
	b.Helper()

	r := newTestModelRegistry()
	clientIDs := make([]string, 0, totalClients)
	for i := 0; i < totalClients; i++ {
		clientID := fmt.Sprintf("client-%04d", i)
		clientIDs = append(clientIDs, clientID)
		r.RegisterClient(clientID, "openai", []*ModelInfo{{
			ID:          "shared-model",
			OwnedBy:     "bench",
			DisplayName: "Shared Model",
		}})
	}
	return r, clientIDs, strings.Join(clientIDs, "\x00")
}

func BenchmarkGetAvailableModelsForClientsCacheHit500(b *testing.B) {
	r, clientIDs, _ := benchmarkScopedModelRegistry(b, 500)
	if models := r.GetAvailableModelsForClients("openai", clientIDs); len(models) != 1 {
		b.Fatalf("warmup GetAvailableModelsForClients models=%d, want 1", len(models))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		models := r.GetAvailableModelsForClients("openai", clientIDs)
		if len(models) != 1 {
			b.Fatalf("GetAvailableModelsForClients models=%d, want 1", len(models))
		}
	}
}

func BenchmarkGetAvailableModelsForClientCacheHit500(b *testing.B) {
	r, clientIDs, clientCacheKey := benchmarkScopedModelRegistry(b, 500)
	if models := r.GetAvailableModelsForClientCache("openai", clientCacheKey, clientIDs); len(models) != 1 {
		b.Fatalf("warmup GetAvailableModelsForClientCache models=%d, want 1", len(models))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		models := r.GetAvailableModelsForClientCache("openai", clientCacheKey, clientIDs)
		if len(models) != 1 {
			b.Fatalf("GetAvailableModelsForClientCache models=%d, want 1", len(models))
		}
	}
}
