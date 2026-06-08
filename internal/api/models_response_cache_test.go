package api

import (
	"bytes"
	"testing"
	"time"
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
}
