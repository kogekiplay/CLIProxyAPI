package management

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// GetAPIKeyAccess returns access rules plus management-page selection metadata.
func (h *Handler) GetAPIKeyAccess(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}

	var (
		apiKeys []string
		rules   map[string]config.APIKeyAccessRule
		manager *coreauth.Manager
	)
	h.mu.Lock()
	if h.cfg != nil {
		apiKeys = append([]string(nil), h.cfg.APIKeys...)
		rules = config.CloneAPIKeyAccessRules(h.cfg.APIKeyAccess)
	}
	manager = h.authManager
	h.mu.Unlock()

	if rules == nil {
		rules = map[string]config.APIKeyAccessRule{}
	}

	c.JSON(http.StatusOK, gin.H{
		"api-key-access": rules,
		"api-keys":       buildAPIKeyAccessKeyViews(apiKeys, rules),
		"auth-targets":   buildAPIKeyAccessAuthTargets(manager),
	})
}

// PutAPIKeyAccess replaces the whole client-key access rule map.
func (h *Handler) PutAPIKeyAccess(c *gin.Context) {
	rules, ok := readAPIKeyAccessRules(c)
	if !ok {
		return
	}
	rules = config.NormalizeAPIKeyAccessRules(rules)

	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config not initialized"})
		return
	}
	h.cfg.APIKeyAccess = rules
	h.cfg.SanitizeAPIKeyAccess()
	h.persistLocked(c)
}

// PatchAPIKeyAccess upserts a single client-key access rule.
func (h *Handler) PatchAPIKeyAccess(c *gin.Context) {
	var body struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
		Rule  json.RawMessage `json:"rule"`
	}
	data, errRead := c.GetRawData()
	if errRead != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}
	if errJSON := json.Unmarshal(data, &body); errJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	key := strings.TrimSpace(body.Key)
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing key"})
		return
	}

	rawRule := body.Value
	if len(rawRule) == 0 {
		rawRule = body.Rule
	}
	if len(rawRule) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing rule"})
		return
	}
	rule, errRule := decodeAPIKeyAccessRuleValue(rawRule)
	if errRule != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	rule = config.NormalizeAPIKeyAccessRule(rule)

	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config not initialized"})
		return
	}
	if h.cfg.APIKeyAccess == nil {
		h.cfg.APIKeyAccess = make(map[string]config.APIKeyAccessRule)
	}
	h.cfg.APIKeyAccess[key] = rule
	h.cfg.SanitizeAPIKeyAccess()
	h.persistLocked(c)
}

// DeleteAPIKeyAccess removes one client-key access rule.
func (h *Handler) DeleteAPIKeyAccess(c *gin.Context) {
	key := strings.TrimSpace(c.Query("key"))
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing key"})
		return
	}
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config not initialized"})
		return
	}
	delete(h.cfg.APIKeyAccess, key)
	if len(h.cfg.APIKeyAccess) == 0 {
		h.cfg.APIKeyAccess = nil
	}
	h.persistLocked(c)
}

func readAPIKeyAccessRules(c *gin.Context) (map[string]config.APIKeyAccessRule, bool) {
	data, errRead := c.GetRawData()
	if errRead != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return nil, false
	}

	var envelope map[string]json.RawMessage
	if errJSON := json.Unmarshal(data, &envelope); errJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return nil, false
	}
	if envelope == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return nil, false
	}

	if rawRules, ok := envelope["api-key-access"]; ok {
		return decodeAPIKeyAccessRulesRaw(c, rawRules)
	}
	if rawRules, ok := envelope["rules"]; ok {
		return decodeAPIKeyAccessRulesRaw(c, rawRules)
	}
	return decodeAPIKeyAccessRulesRaw(c, data)
}

func decodeAPIKeyAccessRulesRaw(c *gin.Context, rawRules json.RawMessage) (map[string]config.APIKeyAccessRule, bool) {
	rules, errRules := decodeAPIKeyAccessRulesValue(rawRules)
	if errRules != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return nil, false
	}
	return rules, true
}

func decodeAPIKeyAccessRulesValue(rawRules json.RawMessage) (map[string]config.APIKeyAccessRule, error) {
	var rawMap map[string]json.RawMessage
	if errJSON := json.Unmarshal(rawRules, &rawMap); errJSON != nil {
		return nil, errJSON
	}
	if rawMap == nil {
		return nil, errors.New("rules must be an object")
	}

	rules := make(map[string]config.APIKeyAccessRule, len(rawMap))
	for key, rawRule := range rawMap {
		rule, errRule := decodeAPIKeyAccessRuleValue(rawRule)
		if errRule != nil {
			return nil, fmt.Errorf("invalid rule for %q: %w", key, errRule)
		}
		rules[key] = rule
	}
	return rules, nil
}

func decodeAPIKeyAccessRuleValue(rawRule json.RawMessage) (config.APIKeyAccessRule, error) {
	var object map[string]json.RawMessage
	if errJSON := json.Unmarshal(rawRule, &object); errJSON != nil {
		return config.APIKeyAccessRule{}, errJSON
	}
	if object == nil {
		return config.APIKeyAccessRule{}, errors.New("rule must be an object")
	}

	var rule config.APIKeyAccessRule
	if errJSON := json.Unmarshal(rawRule, &rule); errJSON != nil {
		return config.APIKeyAccessRule{}, errJSON
	}
	return rule, nil
}

func buildAPIKeyAccessKeyViews(apiKeys []string, rules map[string]config.APIKeyAccessRule) []gin.H {
	views := make([]gin.H, 0, len(apiKeys))
	for _, key := range apiKeys {
		trimmed := strings.TrimSpace(key)
		_, hasRule := rules[key]
		if !hasRule && trimmed != key {
			_, hasRule = rules[trimmed]
		}
		views = append(views, gin.H{
			"key":      key,
			"label":    redactedAPIKeyAccessLabel(key),
			"has-rule": hasRule,
		})
	}
	return views
}

func redactedAPIKeyAccessLabel(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	label := util.HideAPIKey(key)
	if label == key {
		return "<redacted>"
	}
	return label
}

func buildAPIKeyAccessAuthTargets(manager *coreauth.Manager) []gin.H {
	targets := []gin.H{}
	if manager == nil {
		return targets
	}
	auths := manager.List()
	targets = make([]gin.H, 0, len(auths))
	for _, auth := range auths {
		if entry := buildAPIKeyAccessAuthTarget(auth); entry != nil {
			targets = append(targets, entry)
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		providerI, _ := targets[i]["provider"].(string)
		providerJ, _ := targets[j]["provider"].(string)
		if !strings.EqualFold(providerI, providerJ) {
			return strings.ToLower(providerI) < strings.ToLower(providerJ)
		}
		nameI, _ := targets[i]["name"].(string)
		nameJ, _ := targets[j]["name"].(string)
		if !strings.EqualFold(nameI, nameJ) {
			return strings.ToLower(nameI) < strings.ToLower(nameJ)
		}
		idI, _ := targets[i]["id"].(string)
		idJ, _ := targets[j]["id"].(string)
		return idI < idJ
	})
	return targets
}

func buildAPIKeyAccessAuthTarget(auth *coreauth.Auth) gin.H {
	if auth == nil {
		return nil
	}
	auth.EnsureIndex()
	name := strings.TrimSpace(auth.FileName)
	if name == "" {
		name = strings.TrimSpace(auth.ID)
	}
	provider := strings.TrimSpace(auth.Provider)
	entry := gin.H{
		"id":          auth.ID,
		"auth-index":  auth.Index,
		"auth_index":  auth.Index,
		"name":        name,
		"filename":    strings.TrimSpace(auth.FileName),
		"provider":    provider,
		"type":        provider,
		"label":       auth.Label,
		"status":      auth.Status,
		"disabled":    auth.Disabled,
		"unavailable": auth.Unavailable,
	}
	if path := strings.TrimSpace(authAttribute(auth, "path")); path != "" {
		entry["path"] = path
	}
	if email := authEmail(auth); email != "" {
		entry["email"] = email
	}
	if projectID := authProjectID(auth); projectID != "" {
		entry["project_id"] = projectID
	}
	if accountType, account := auth.AccountInfo(); accountType != "" || account != "" {
		if accountType != "" {
			entry["account_type"] = accountType
		}
		if account != "" && !strings.EqualFold(accountType, "api_key") {
			entry["account"] = account
		}
	}
	return entry
}
