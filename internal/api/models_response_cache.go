package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers/openai"
)

type scopedModelsResponseKind string

const (
	scopedModelsResponseOpenAI      scopedModelsResponseKind = "openai"
	scopedModelsResponseClaude      scopedModelsResponseKind = "claude"
	scopedModelsResponseGemini      scopedModelsResponseKind = "gemini"
	scopedModelsResponseCodexClient scopedModelsResponseKind = "codex-client"
	maxModelsResponseCacheEntries                            = 512
)

type scopedModelsSnapshot struct {
	handlerType    string
	clientCacheKey string
	clientIDs      []string
	scoped         bool
}

type modelsResponseCacheEntry struct {
	body            []byte
	registryVersion uint64
	expiresAt       time.Time
	storedAt        time.Time
}

type modelsResponseCache struct {
	mu            sync.RWMutex
	entries       map[string]modelsResponseCacheEntry
	latestVersion uint64
	maxEntries    int
}

func newModelsResponseCache() *modelsResponseCache {
	return &modelsResponseCache{
		entries:    make(map[string]modelsResponseCacheEntry),
		maxEntries: maxModelsResponseCacheEntries,
	}
}

func scopedModelsResponseCacheKey(handlerType, clientCacheKey string, kind scopedModelsResponseKind) string {
	handlerType = strings.TrimSpace(handlerType)
	clientCacheKey = strings.TrimSpace(clientCacheKey)
	if clientCacheKey == "" {
		clientCacheKey = "<empty>"
	}
	return string(kind) + "\x00" + handlerType + "\x00" + clientCacheKey
}

func (c *modelsResponseCache) Get(key string, registryVersion uint64, now time.Time) ([]byte, bool) {
	if c == nil || key == "" {
		return nil, false
	}
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || entry.registryVersion != registryVersion {
		return nil, false
	}
	if !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt) {
		c.deleteExpired(key, now)
		return nil, false
	}
	return entry.body, true
}

func (c *modelsResponseCache) deleteExpired(key string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if ok && !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt) {
		delete(c.entries, key)
	}
}

func (c *modelsResponseCache) Set(key string, registryVersion uint64, expiresAt time.Time, body []byte) {
	if c == nil || key == "" || body == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if registryVersion < c.latestVersion {
		return
	}
	if registryVersion > c.latestVersion {
		clear(c.entries)
		c.latestVersion = registryVersion
	}
	now := time.Now()
	for existingKey, entry := range c.entries {
		if !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt) {
			delete(c.entries, existingKey)
		}
	}
	if _, exists := c.entries[key]; !exists && c.maxEntries > 0 && len(c.entries) >= c.maxEntries {
		c.evictOldestLocked()
	}
	c.entries[key] = modelsResponseCacheEntry{
		body:            append([]byte(nil), body...),
		registryVersion: registryVersion,
		expiresAt:       expiresAt,
		storedAt:        now,
	}
}

func (c *modelsResponseCache) evictOldestLocked() {
	oldestKey := ""
	var oldestAt time.Time
	for key, entry := range c.entries {
		if oldestKey == "" || entry.storedAt.Before(oldestAt) {
			oldestKey = key
			oldestAt = entry.storedAt
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

func (s *Server) writeScopedModelsResponse(c *gin.Context, snapshot scopedModelsSnapshot, kind scopedModelsResponseKind) {
	if c == nil || !snapshot.scoped {
		return
	}

	key := scopedModelsResponseCacheKey(snapshot.handlerType, snapshot.clientCacheKey, kind)
	now := time.Now()
	reg := registry.GetGlobalRegistry()
	registryVersion := reg.CacheVersion()
	if s != nil && s.modelsResponseCache != nil {
		if body, ok := s.modelsResponseCache.Get(key, registryVersion, now); ok {
			writeModelsJSONBody(c, body)
			return
		}
	}

	models, expiresAt, registryVersion := reg.GetAvailableModelsForClientCacheSnapshot(snapshot.handlerType, snapshot.clientCacheKey, snapshot.clientIDs)
	body, err := marshalScopedModelsResponse(kind, models)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode models response"})
		return
	}
	if s != nil && s.modelsResponseCache != nil {
		s.modelsResponseCache.Set(key, registryVersion, expiresAt, body)
	}
	writeModelsJSONBody(c, body)
}

func marshalScopedModelsResponse(kind scopedModelsResponseKind, models []map[string]any) ([]byte, error) {
	switch kind {
	case scopedModelsResponseOpenAI:
		return marshalOpenAIModels(models)
	case scopedModelsResponseClaude:
		return marshalClaudeModels(models)
	case scopedModelsResponseGemini:
		return marshalGeminiModels(models)
	case scopedModelsResponseCodexClient:
		return json.Marshal(openai.CodexClientModelsResponse(models))
	default:
		return nil, &json.UnsupportedValueError{}
	}
}

func marshalOpenAIModels(allModels []map[string]any) ([]byte, error) {
	filteredModels := make([]map[string]any, len(allModels))
	for i, model := range allModels {
		filteredModel := map[string]any{
			"id":     model["id"],
			"object": model["object"],
		}
		if created, exists := model["created"]; exists {
			filteredModel["created"] = created
		}
		if ownedBy, exists := model["owned_by"]; exists {
			filteredModel["owned_by"] = ownedBy
		}
		filteredModels[i] = filteredModel
	}

	return json.Marshal(gin.H{
		"object": "list",
		"data":   filteredModels,
	})
}

func marshalClaudeModels(models []map[string]any) ([]byte, error) {
	firstID := ""
	lastID := ""
	if len(models) > 0 {
		if id, ok := models[0]["id"].(string); ok {
			firstID = id
		}
		if id, ok := models[len(models)-1]["id"].(string); ok {
			lastID = id
		}
	}

	return json.Marshal(gin.H{
		"data":     models,
		"has_more": false,
		"first_id": firstID,
		"last_id":  lastID,
	})
}

func marshalGeminiModels(rawModels []map[string]any) ([]byte, error) {
	normalizedModels := make([]map[string]any, 0, len(rawModels))
	defaultMethods := []string{"generateContent"}
	for _, model := range rawModels {
		normalizedModel := make(map[string]any, len(model))
		for k, v := range model {
			normalizedModel[k] = v
		}
		if name, ok := normalizedModel["name"].(string); ok && name != "" {
			if !strings.HasPrefix(name, "models/") {
				normalizedModel["name"] = "models/" + name
			}
			if displayName, _ := normalizedModel["displayName"].(string); displayName == "" {
				normalizedModel["displayName"] = name
			}
			if description, _ := normalizedModel["description"].(string); description == "" {
				normalizedModel["description"] = name
			}
		}
		if _, ok := normalizedModel["supportedGenerationMethods"]; !ok {
			normalizedModel["supportedGenerationMethods"] = defaultMethods
		}
		normalizedModels = append(normalizedModels, normalizedModel)
	}
	return json.Marshal(gin.H{
		"models": normalizedModels,
	})
}

func writeModelsJSONBody(c *gin.Context, body []byte) {
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}
