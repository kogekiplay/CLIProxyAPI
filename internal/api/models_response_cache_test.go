package api

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

func TestModelsResponseCacheUsesVersionAndExpiry(t *testing.T) {
	cache := newModelsResponseCache()
	key := scopedModelsResponseCacheKey("openai", "client-1", scopedModelsResponseOpenAI)
	now := time.Now()
	body := []byte(`{"object":"list","data":[]}`)

	cache.Set(key, 7, now.Add(time.Minute), body)

	got, ok := cache.Get(key, 7, now)
	if !ok {
		t.Fatal("expected cache hit for matching version before expiry")
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("cached body = %s, want %s", got, body)
	}

	if _, ok := cache.Get(key, 8, now); ok {
		t.Fatal("expected version mismatch to miss")
	}
	if _, ok := cache.Get(key, 7, now.Add(2*time.Minute)); ok {
		t.Fatal("expected expired entry to miss")
	}
	if len(cache.entries) != 0 {
		t.Fatalf("expired cache entry was not removed: %d entries", len(cache.entries))
	}
}

func TestModelsResponseCacheDropsOldRegistryVersions(t *testing.T) {
	cache := newModelsResponseCache()
	now := time.Now()
	oldKey := scopedModelsResponseCacheKey("openai", "old-client", scopedModelsResponseOpenAI)
	newKey := scopedModelsResponseCacheKey("openai", "new-client", scopedModelsResponseOpenAI)

	cache.Set(oldKey, 7, now.Add(time.Minute), []byte("old"))
	cache.Set(newKey, 8, now.Add(time.Minute), []byte("new"))

	if len(cache.entries) != 1 {
		t.Fatalf("cache entries after version advance = %d, want 1", len(cache.entries))
	}
	if _, ok := cache.Get(oldKey, 7, now); ok {
		t.Fatal("old registry version remained cached")
	}
	if body, ok := cache.Get(newKey, 8, now); !ok || string(body) != "new" {
		t.Fatalf("new registry version cache = (%q, %v), want (new, true)", body, ok)
	}

	cache.Set(oldKey, 7, now.Add(time.Minute), []byte("stale"))
	if _, ok := cache.Get(oldKey, 7, now); ok {
		t.Fatal("late write for an old registry version was cached")
	}
}

func TestModelsResponseCacheIsBounded(t *testing.T) {
	cache := newModelsResponseCache()
	cache.maxEntries = 3
	now := time.Now()
	for i, key := range []string{"one", "two", "three", "four"} {
		cache.Set(key, 1, now.Add(time.Minute), []byte{byte(i)})
	}

	if len(cache.entries) != cache.maxEntries {
		t.Fatalf("cache entries = %d, want %d", len(cache.entries), cache.maxEntries)
	}
	if _, ok := cache.Get("four", 1, now); !ok {
		t.Fatal("newest cache entry was evicted")
	}
}

func BenchmarkScopedModelsCacheHit(b *testing.B) {
	const clientID = "models-response-cache-benchmark-client"
	models := make([]*registry.ModelInfo, 0, 200)
	for i := 0; i < cap(models); i++ {
		models = append(models, &registry.ModelInfo{ID: fmt.Sprintf("benchmark-model-%03d", i)})
	}
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(clientID, "openai", models)
	b.Cleanup(func() { reg.UnregisterClient(clientID) })

	clientIDs := []string{clientID}
	clientCacheKey := clientID
	_, expiresAt, version := reg.GetAvailableModelsForClientCacheSnapshot("openai", clientCacheKey, clientIDs)
	cache := newModelsResponseCache()
	key := scopedModelsResponseCacheKey("openai", clientCacheKey, scopedModelsResponseOpenAI)
	cache.Set(key, version, expiresAt, bytes.Repeat([]byte("x"), 16*1024))
	now := time.Now()

	b.Run("registry-snapshot", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkModels, _, _ = reg.GetAvailableModelsForClientCacheSnapshot("openai", clientCacheKey, clientIDs)
		}
	})
	b.Run("encoded-response", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkModelsBody, benchmarkModelsOK = cache.Get(key, version, now)
		}
	})
}

var (
	benchmarkModels     []map[string]any
	benchmarkModelsBody []byte
	benchmarkModelsOK   bool
)
