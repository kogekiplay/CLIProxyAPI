package usageledger

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

const defaultAnalyticsEventsLimit = 100
const maxAnalyticsEventsLimit = 50000

type analyticsEvent struct {
	id       int64
	event    Event
	tokens   TokenUsage
	cost     float64
	hasCost  bool
	missing  string
	failed   bool
	unixNano int64
}

type analyticsAggregate struct {
	calls    int64
	success  int64
	failure  int64
	tokens   TokenUsage
	cost     float64
	hasCost  bool
	allCost  bool
	initCost bool
}

// Analytics returns management-facing request monitoring aggregates.
func (s *SQLiteStore) Analytics(ctx context.Context, req AnalyticsRequest) (AnalyticsResponse, error) {
	if s == nil || s.db == nil {
		return AnalyticsResponse{}, errors.New("usage ledger sqlite store is nil")
	}
	if req.FromMS <= 0 || req.ToMS <= 0 || req.FromMS >= req.ToMS {
		return AnalyticsResponse{}, errors.New("from_ms and to_ms are required and from_ms must be less than to_ms")
	}

	prices, err := s.ListModelPrices(ctx)
	if err != nil {
		return AnalyticsResponse{}, err
	}
	events, err := s.analyticsEvents(ctx, req, prices)
	if err != nil {
		return AnalyticsResponse{}, err
	}

	resp := AnalyticsResponse{GeneratedAtMS: time.Now().UnixMilli()}
	if req.Include.Summary {
		resp.Summary = buildAnalyticsSummary(events)
	}
	if req.Include.Timeline {
		resp.Timeline = buildAnalyticsTimeline(events)
	}
	if req.Include.ModelStats {
		resp.ModelStats = buildAnalyticsModelStats(events)
	}
	if req.Include.APIKeyStats {
		resp.APIKeyStats = buildAnalyticsAPIKeyStats(events)
	}
	if req.Include.CredentialStats {
		resp.CredentialStats = buildAnalyticsCredentialStats(events)
	}
	if req.Include.EventsPage != nil {
		resp.Events = buildAnalyticsEventsPage(events, *req.Include.EventsPage)
	}
	return resp, nil
}

func (s *SQLiteStore) analyticsEvents(ctx context.Context, req AnalyticsRequest, prices []ModelPrice) ([]analyticsEvent, error) {
	where, args := buildAnalyticsWhere(req)
	rows, err := s.db.QueryContext(ctx, `SELECT
		id,
		request_id,
		ts_ns,
		provider,
		model,
		endpoint,
		auth_index,
		auth_file_name,
		api_key_hash,
		credential_key_hash,
		account_ref,
		auth_type,
		service_tier,
		status_code,
		latency_ms,
		ttft_ms,
		fail_status_code,
		fail_summary,
		fail_body,
		input_tokens,
		output_tokens,
		reasoning_tokens,
		cached_tokens,
		cache_read_tokens,
		cache_creation_tokens,
		total_tokens,
		failed
		FROM usage_events `+where+`
		ORDER BY ts_ns ASC, id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]analyticsEvent, 0)
	for rows.Next() {
		var item analyticsEvent
		var failed int
		if err := rows.Scan(
			&item.id,
			&item.event.RequestID,
			&item.unixNano,
			&item.event.Provider,
			&item.event.Model,
			&item.event.Endpoint,
			&item.event.AuthIndex,
			&item.event.AuthFileName,
			&item.event.APIKeyHash,
			&item.event.CredentialKeyHash,
			&item.event.AccountRef,
			&item.event.AuthType,
			&item.event.ServiceTier,
			&item.event.StatusCode,
			&item.event.LatencyMS,
			&item.event.TTFTMS,
			&item.event.FailStatusCode,
			&item.event.FailSummary,
			&item.event.FailBody,
			&item.tokens.InputTokens,
			&item.tokens.OutputTokens,
			&item.tokens.ReasoningTokens,
			&item.tokens.CachedTokens,
			&item.tokens.CacheReadTokens,
			&item.tokens.CacheCreationTokens,
			&item.tokens.TotalTokens,
			&failed,
		); err != nil {
			return nil, err
		}
		item.event.Timestamp = time.Unix(0, item.unixNano).UTC()
		item.event.Tokens = item.tokens.Normalize()
		item.tokens = item.event.Tokens
		item.failed = failed != 0
		item.event.Failed = item.failed
		if cost, ok, missing := CostForUsage(item.event.Model, item.tokens, prices); ok {
			item.cost = cost
			item.hasCost = true
		} else if len(missing) > 0 {
			item.missing = missing[0]
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func buildAnalyticsWhere(req AnalyticsRequest) (string, []any) {
	clauses := []string{"ts_ns >= ?", "ts_ns < ?"}
	args := []any{time.UnixMilli(req.FromMS).UTC().UnixNano(), time.UnixMilli(req.ToMS).UTC().UnixNano()}
	addIn := func(column string, values []string) {
		cleaned := cleanedAnalyticsValues(values)
		if len(cleaned) == 0 {
			return
		}
		placeholders := make([]string, len(cleaned))
		for i, value := range cleaned {
			placeholders[i] = "?"
			args = append(args, value)
		}
		clauses = append(clauses, column+" IN ("+strings.Join(placeholders, ",")+")")
	}
	addIn("provider", req.Filters.Providers)
	addIn("model", req.Filters.Models)
	addIn("auth_file_name", req.Filters.AuthFiles)
	addIn("auth_index", req.Filters.AuthIndices)
	addAPIKeyHashFilter(req.Filters.APIKeyHashes, &clauses, &args)
	addIn("account_ref", req.Filters.Accounts)
	if req.Filters.FailedOnly {
		clauses = append(clauses, "failed <> 0")
	} else if req.Filters.IncludeFailed != nil && !*req.Filters.IncludeFailed {
		clauses = append(clauses, "failed = 0")
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func addAPIKeyHashFilter(values []string, clauses *[]string, args *[]any) {
	cleaned := cleanedAnalyticsValues(values)
	if len(cleaned) == 0 || clauses == nil || args == nil {
		return
	}
	placeholders := make([]string, len(cleaned))
	for i, value := range cleaned {
		placeholders[i] = "?"
		*args = append(*args, value)
	}
	inClause := strings.Join(placeholders, ",")
	*clauses = append(*clauses, "(api_key_hash IN ("+inClause+") OR credential_key_hash IN ("+inClause+"))")
	for _, value := range cleaned {
		*args = append(*args, value)
	}
}

func cleanedAnalyticsValues(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func buildAnalyticsSummary(events []analyticsEvent) *AnalyticsSummary {
	agg := aggregateAnalyticsEvents(events)
	summary := &AnalyticsSummary{
		TotalCalls:          agg.calls,
		SuccessCalls:        agg.success,
		FailureCalls:        agg.failure,
		InputTokens:         agg.tokens.InputTokens,
		OutputTokens:        agg.tokens.OutputTokens,
		ReasoningTokens:     agg.tokens.ReasoningTokens,
		CachedTokens:        agg.tokens.CachedTokens,
		CacheReadTokens:     agg.tokens.CacheReadTokens,
		CacheCreationTokens: agg.tokens.CacheCreationTokens,
		TotalTokens:         agg.tokens.TotalTokens,
	}
	if agg.hasCost {
		summary.TotalCost = floatPtr(agg.cost)
	}
	return summary
}

func buildAnalyticsTimeline(events []analyticsEvent) []AnalyticsTimelinePoint {
	byBucket := make(map[int64]analyticsAggregate)
	for _, event := range events {
		bucket := event.event.Timestamp.UTC().Truncate(time.Hour).UnixMilli()
		agg := byBucket[bucket]
		addAnalyticsEvent(&agg, event)
		byBucket[bucket] = agg
	}
	keys := make([]int64, 0, len(byBucket))
	for key := range byBucket {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]AnalyticsTimelinePoint, 0, len(keys))
	for _, key := range keys {
		agg := byBucket[key]
		point := AnalyticsTimelinePoint{
			BucketMS:            key,
			Calls:               agg.calls,
			Success:             agg.success,
			Failure:             agg.failure,
			InputTokens:         agg.tokens.InputTokens,
			OutputTokens:        agg.tokens.OutputTokens,
			ReasoningTokens:     agg.tokens.ReasoningTokens,
			CachedTokens:        agg.tokens.CachedTokens,
			CacheReadTokens:     agg.tokens.CacheReadTokens,
			CacheCreationTokens: agg.tokens.CacheCreationTokens,
			TotalTokens:         agg.tokens.TotalTokens,
		}
		if agg.hasCost {
			point.Cost = floatPtr(agg.cost)
		}
		out = append(out, point)
	}
	return out
}

func buildAnalyticsModelStats(events []analyticsEvent) []AnalyticsModelStat {
	grouped := make(map[string]analyticsAggregate)
	for _, event := range events {
		key := event.event.Model
		agg := grouped[key]
		addAnalyticsEvent(&agg, event)
		grouped[key] = agg
	}
	keys := sortedAnalyticsStatKeys(grouped)
	out := make([]AnalyticsModelStat, 0, len(keys))
	for _, key := range keys {
		agg := grouped[key]
		row := AnalyticsModelStat{
			Model:               key,
			Calls:               agg.calls,
			SuccessCalls:        agg.success,
			FailureCalls:        agg.failure,
			InputTokens:         agg.tokens.InputTokens,
			OutputTokens:        agg.tokens.OutputTokens,
			ReasoningTokens:     agg.tokens.ReasoningTokens,
			CachedTokens:        agg.tokens.CachedTokens,
			CacheReadTokens:     agg.tokens.CacheReadTokens,
			CacheCreationTokens: agg.tokens.CacheCreationTokens,
			TotalTokens:         agg.tokens.TotalTokens,
		}
		if agg.hasCost {
			row.Cost = floatPtr(agg.cost)
		}
		out = append(out, row)
	}
	return out
}

func buildAnalyticsAPIKeyStats(events []analyticsEvent) []AnalyticsAPIKeyStat {
	type apiKeyMeta struct {
		hash       string
		provider   string
		accountRef string
		providers  map[string]struct{}
	}
	grouped := make(map[string]analyticsAggregate)
	meta := make(map[string]apiKeyMeta)
	for _, event := range events {
		if !isAnalyticsAPIKeyCredentialEvent(event.event) {
			continue
		}
		provider := strings.TrimSpace(event.event.Provider)
		hash := analyticsAPIKeyCredentialHash(event.event)
		key := hash
		agg := grouped[key]
		addAnalyticsEvent(&agg, event)
		grouped[key] = agg
		item := meta[key]
		if item.hash == "" {
			item = apiKeyMeta{
				hash:       hash,
				provider:   provider,
				accountRef: strings.TrimSpace(event.event.AccountRef),
				providers:  make(map[string]struct{}),
			}
		}
		if item.provider == "" {
			item.provider = provider
		}
		if item.accountRef == "" {
			item.accountRef = strings.TrimSpace(event.event.AccountRef)
		}
		if provider != "" {
			item.providers[provider] = struct{}{}
		}
		meta[key] = item
	}
	keys := sortedAnalyticsStatKeys(grouped)
	out := make([]AnalyticsAPIKeyStat, 0, len(keys))
	for _, key := range keys {
		agg := grouped[key]
		item := meta[key]
		providers := sortedStringSet(item.providers)
		row := AnalyticsAPIKeyStat{
			Provider:            item.provider,
			Providers:           providers,
			APIKeyHash:          item.hash,
			AccountRef:          item.accountRef,
			Calls:               agg.calls,
			SuccessCalls:        agg.success,
			FailureCalls:        agg.failure,
			InputTokens:         agg.tokens.InputTokens,
			OutputTokens:        agg.tokens.OutputTokens,
			ReasoningTokens:     agg.tokens.ReasoningTokens,
			CachedTokens:        agg.tokens.CachedTokens,
			CacheReadTokens:     agg.tokens.CacheReadTokens,
			CacheCreationTokens: agg.tokens.CacheCreationTokens,
			TotalTokens:         agg.tokens.TotalTokens,
		}
		if agg.hasCost {
			row.Cost = floatPtr(agg.cost)
		}
		out = append(out, row)
	}
	return out
}

func isAnalyticsAPIKeyCredentialEvent(event Event) bool {
	authType := normalizedAnalyticsAuthType(event.AuthType)
	switch authType {
	case "apikey":
		return true
	case "oauth":
		return false
	}
	if strings.TrimSpace(event.CredentialKeyHash) != "" {
		return true
	}
	accountRef := strings.TrimSpace(event.AccountRef)
	if strings.HasPrefix(accountRef, "opencode-go:") {
		return true
	}
	provider := strings.ToLower(strings.TrimSpace(event.Provider))
	if strings.HasPrefix(provider, "openai-compatible-") {
		return true
	}
	if isAnalyticsConfiguredAPIKeyName(event.AuthFileName) {
		return true
	}
	if strings.TrimSpace(event.AuthIndex) != "" || strings.TrimSpace(event.AuthFileName) != "" {
		return false
	}
	return false
}

func isAnalyticsConfiguredAPIKeyName(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	value = strings.TrimSuffix(value, ".json")
	return value == "apikey" || value == "api-key" || strings.HasSuffix(value, "-apikey") || strings.HasSuffix(value, "-api-key")
}

func analyticsAPIKeyCredentialHash(event Event) string {
	for _, value := range []string{event.CredentialKeyHash, event.AccountRef, event.AuthIndex, event.APIKeyHash} {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return "unknown"
}

func normalizedAnalyticsAuthType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "apikey", "api_key", "api-key":
		return "apikey"
	case "oauth", "oauth2":
		return "oauth"
	default:
		return ""
	}
}

func buildAnalyticsCredentialStats(events []analyticsEvent) []AnalyticsCredentialStat {
	type credentialMeta struct {
		provider     string
		authIndex    string
		authFileName string
		accountRef   string
	}
	grouped := make(map[string]analyticsAggregate)
	meta := make(map[string]credentialMeta)
	for _, event := range events {
		if isAnalyticsAPIKeyCredentialEvent(event.event) {
			continue
		}
		if strings.TrimSpace(event.event.AuthIndex) == "" && strings.TrimSpace(event.event.AuthFileName) == "" {
			continue
		}
		keyParts := []string{event.event.Provider, event.event.AuthIndex, event.event.AuthFileName, event.event.AccountRef}
		key := strings.Join(keyParts, "\x00")
		if key == "\x00\x00\x00" {
			key = "unknown"
		}
		agg := grouped[key]
		addAnalyticsEvent(&agg, event)
		grouped[key] = agg
		if _, ok := meta[key]; !ok {
			meta[key] = credentialMeta{
				provider:     event.event.Provider,
				authIndex:    event.event.AuthIndex,
				authFileName: event.event.AuthFileName,
				accountRef:   event.event.AccountRef,
			}
		}
	}
	keys := sortedAnalyticsStatKeys(grouped)
	out := make([]AnalyticsCredentialStat, 0, len(keys))
	for _, key := range keys {
		agg := grouped[key]
		item := meta[key]
		row := AnalyticsCredentialStat{
			Provider:            item.provider,
			AuthIndex:           item.authIndex,
			AuthFileName:        item.authFileName,
			AccountRef:          item.accountRef,
			Calls:               agg.calls,
			SuccessCalls:        agg.success,
			FailureCalls:        agg.failure,
			InputTokens:         agg.tokens.InputTokens,
			OutputTokens:        agg.tokens.OutputTokens,
			ReasoningTokens:     agg.tokens.ReasoningTokens,
			CachedTokens:        agg.tokens.CachedTokens,
			CacheReadTokens:     agg.tokens.CacheReadTokens,
			CacheCreationTokens: agg.tokens.CacheCreationTokens,
			TotalTokens:         agg.tokens.TotalTokens,
		}
		if agg.hasCost {
			row.Cost = floatPtr(agg.cost)
		}
		out = append(out, row)
	}
	return out
}

func buildAnalyticsEventsPage(events []analyticsEvent, page AnalyticsEventsPage) *AnalyticsEventsResponse {
	limit := page.Limit
	if limit <= 0 {
		limit = defaultAnalyticsEventsLimit
	}
	if limit > maxAnalyticsEventsLimit {
		limit = maxAnalyticsEventsLimit
	}
	items := make([]AnalyticsEventRow, 0, minInt(limit, len(events)))
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		timestampMS := event.event.Timestamp.UnixMilli()
		if page.BeforeMS != nil {
			beforeID := int64(0)
			if page.BeforeID != nil {
				beforeID = *page.BeforeID
			}
			if timestampMS > *page.BeforeMS || (timestampMS == *page.BeforeMS && beforeID > 0 && event.id >= beforeID) {
				continue
			}
		}
		if len(items) >= limit+1 {
			break
		}
		row := AnalyticsEventRow{
			ID:                    event.id,
			RequestID:             event.event.RequestID,
			TimestampMS:           timestampMS,
			Provider:              event.event.Provider,
			Model:                 event.event.Model,
			Endpoint:              event.event.Endpoint,
			AuthIndex:             event.event.AuthIndex,
			AuthFileName:          event.event.AuthFileName,
			APIKeyHash:            event.event.APIKeyHash,
			CredentialKeyHash:     event.event.CredentialKeyHash,
			AccountRef:            event.event.AccountRef,
			AuthType:              event.event.AuthType,
			ServiceTier:           event.event.ServiceTier,
			StatusCode:            event.event.StatusCode,
			LatencyMS:             int64PtrIfPositive(event.event.LatencyMS),
			TTFTMS:                int64PtrIfPositive(event.event.TTFTMS),
			FailStatusCode:        event.event.FailStatusCode,
			FailSummary:           event.event.FailSummary,
			FailBody:              event.event.FailBody,
			Tokens:                event.tokens,
			Failed:                event.failed,
			MissingPriceModelName: event.missing,
		}
		if event.hasCost {
			row.EstimatedCostUSD = floatPtr(event.cost)
		}
		items = append(items, row)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	resp := &AnalyticsEventsResponse{
		Items:      items,
		HasMore:    hasMore,
		TotalCount: int64(len(events)),
	}
	if len(items) > 0 {
		last := items[len(items)-1]
		resp.NextBeforeMS = last.TimestampMS
		resp.NextBeforeID = last.ID
	}
	return resp
}

func aggregateAnalyticsEvents(events []analyticsEvent) analyticsAggregate {
	var agg analyticsAggregate
	for _, event := range events {
		addAnalyticsEvent(&agg, event)
	}
	return agg
}

func addAnalyticsEvent(agg *analyticsAggregate, event analyticsEvent) {
	if agg == nil {
		return
	}
	agg.calls++
	if event.failed {
		agg.failure++
	} else {
		agg.success++
	}
	agg.tokens = agg.tokens.Add(event.tokens)
	if !agg.initCost {
		agg.allCost = true
		agg.initCost = true
	}
	if event.hasCost {
		agg.cost += event.cost
		agg.hasCost = true
	} else {
		agg.allCost = false
	}
}

func sortedAnalyticsStatKeys(grouped map[string]analyticsAggregate) []string {
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := grouped[keys[i]]
		right := grouped[keys[j]]
		if left.calls != right.calls {
			return left.calls > right.calls
		}
		if left.tokens.TotalTokens != right.tokens.TotalTokens {
			return left.tokens.TotalTokens > right.tokens.TotalTokens
		}
		return keys[i] < keys[j]
	})
	return keys
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func int64PtrIfPositive(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}
