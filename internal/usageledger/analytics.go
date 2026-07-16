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

const analyticsEventsSelect = `SELECT
		id,
		request_id,
		ts_ns,
		provider,
		model,
		model_alias,
		endpoint,
		auth_index,
		auth_file_name,
		api_key_hash,
		credential_key_hash,
		account_ref,
		auth_type,
		executor_type,
		service_tier,
		cache_input_mode,
		reasoning_effort,
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
		normalized_cached_tokens,
		normalized_cache_read_tokens,
		normalized_cache_creation_tokens,
		uncached_input_tokens,
		total_input_tokens,
		total_tokens,
		failed
		FROM usage_events `

const analyticsAggregateEventsSelect = `SELECT
		ts_ns,
		provider,
		model,
		model_alias,
		auth_index,
		auth_file_name,
		api_key_hash,
		credential_key_hash,
		account_ref,
		auth_type,
		service_tier,
		output_tokens,
		reasoning_tokens,
		normalized_cached_tokens,
		normalized_cache_read_tokens,
		normalized_cache_creation_tokens,
		uncached_input_tokens,
		total_input_tokens,
		total_tokens,
		failed
		FROM usage_events `

type analyticsEvent struct {
	id            int64
	event         Event
	upstreamModel string
	tokens        TokenUsage
	cost          float64
	hasCost       bool
	missing       string
	failed        bool
	unixNano      int64
}

type analyticsAggregate struct {
	calls   int64
	success int64
	failure int64
	tokens  TokenUsage
	cost    float64
	hasCost bool
}

// Analytics returns management-facing request monitoring aggregates.
func (s *SQLiteStore) Analytics(ctx context.Context, req AnalyticsRequest) (AnalyticsResponse, error) {
	if s == nil || s.db == nil {
		return AnalyticsResponse{}, errors.New("usage ledger sqlite store is nil")
	}
	if req.FromMS <= 0 || req.ToMS <= 0 || req.FromMS >= req.ToMS {
		return AnalyticsResponse{}, errors.New("from_ms and to_ms are required and from_ms must be less than to_ms")
	}

	resp := AnalyticsResponse{GeneratedAtMS: time.Now().UnixMilli()}
	hasAggregates := analyticsIncludesAggregates(req.Include)
	if !hasAggregates && req.Include.EventsPage == nil {
		return resp, nil
	}

	prices, err := s.ListModelPrices(ctx)
	if err != nil {
		return AnalyticsResponse{}, err
	}
	priceIndex := compileModelPriceIndex(prices)
	modelAliases := compileModelAliasIndex(req.ModelAliases)

	// Alias-aware model filters require post-resolution matching. Keep their
	// strict full-event path so pagination and totals remain exact.
	if len(cleanedAnalyticsValues(req.Filters.Models)) > 0 {
		events, queryErr := s.analyticsEventsWithModelAliasIndex(ctx, req, priceIndex, modelAliases)
		if queryErr != nil {
			return AnalyticsResponse{}, queryErr
		}
		acc := newAnalyticsAccumulator(req.Include)
		for _, event := range events {
			acc.add(event)
		}
		acc.apply(&resp)
		if req.Include.EventsPage != nil {
			resp.Events = buildAnalyticsEventsPage(events, *req.Include.EventsPage)
		}
		return resp, nil
	}

	if hasAggregates {
		acc := newAnalyticsAccumulator(req.Include)
		if err = s.scanAnalyticsAggregateEvents(ctx, req, priceIndex, modelAliases, acc.add); err != nil {
			return AnalyticsResponse{}, err
		}
		acc.apply(&resp)
	}
	if req.Include.EventsPage != nil {
		resp.Events, err = s.analyticsEventsPageWithModelAliasIndex(ctx, req, priceIndex, modelAliases)
		if err != nil {
			return AnalyticsResponse{}, err
		}
	}
	return resp, nil
}

func analyticsIncludesAggregates(include AnalyticsInclude) bool {
	return include.Summary || include.Timeline || include.ModelStats || include.APIKeyStats || include.CredentialStats
}

func (s *SQLiteStore) analyticsEvents(ctx context.Context, req AnalyticsRequest, prices []ModelPrice) ([]analyticsEvent, error) {
	return s.analyticsEventsWithModelAliasIndex(ctx, req, compileModelPriceIndex(prices), compileModelAliasIndex(req.ModelAliases))
}

func (s *SQLiteStore) analyticsEventsWithModelAliasIndex(ctx context.Context, req AnalyticsRequest, prices modelPriceIndex, modelAliases modelAliasIndex) ([]analyticsEvent, error) {
	where, args := buildAnalyticsWhereWithModelAliasIndex(req, modelAliases)
	return s.queryAnalyticsEvents(ctx, analyticsEventsSelect+where+` ORDER BY ts_ns ASC, id ASC`, args, prices, modelAliases, cleanedAnalyticsValues(req.Filters.Models))
}

func (s *SQLiteStore) scanAnalyticsAggregateEvents(ctx context.Context, req AnalyticsRequest, prices modelPriceIndex, modelAliases modelAliasIndex, add func(analyticsEvent)) error {
	where, args := buildAnalyticsWhereWithModelAliasIndex(req, modelAliases)
	rows, err := s.db.QueryContext(ctx, analyticsAggregateEventsSelect+where+` ORDER BY ts_ns ASC, id ASC`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	requestedModels := cleanedAnalyticsValues(req.Filters.Models)
	for rows.Next() {
		item, scanErr := scanAnalyticsAggregateEvent(rows, prices, modelAliases)
		if scanErr != nil {
			return scanErr
		}
		if !matchesAnalyticsModelFilter(requestedModels, item.event.Model, item.upstreamModel) {
			continue
		}
		add(item)
	}
	return rows.Err()
}

func (s *SQLiteStore) analyticsEventsPageWithModelAliasIndex(ctx context.Context, req AnalyticsRequest, prices modelPriceIndex, modelAliases modelAliasIndex) (*AnalyticsEventsResponse, error) {
	page := *req.Include.EventsPage
	limit := normalizeAnalyticsEventsLimit(page.Limit)
	where, args := buildAnalyticsWhereWithModelAliasIndex(req, modelAliases)

	var totalCount int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events `+where, args...).Scan(&totalCount); err != nil {
		return nil, err
	}

	pageWhere, pageArgs := addAnalyticsEventsPageCursor(where, append([]any(nil), args...), page)
	pageArgs = append(pageArgs, limit+1)
	events, err := s.queryAnalyticsEvents(
		ctx,
		analyticsEventsSelect+pageWhere+` ORDER BY ts_ns DESC, id DESC LIMIT ?`,
		pageArgs,
		prices,
		modelAliases,
		nil,
	)
	if err != nil {
		return nil, err
	}
	return buildAnalyticsEventsPageFromDescending(events, limit, totalCount), nil
}

func (s *SQLiteStore) queryAnalyticsEvents(ctx context.Context, query string, args []any, prices modelPriceIndex, modelAliases modelAliasIndex, requestedModels []string) ([]analyticsEvent, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]analyticsEvent, 0)
	for rows.Next() {
		item, err := scanAnalyticsEvent(rows, prices, modelAliases)
		if err != nil {
			return nil, err
		}
		if !matchesAnalyticsModelFilter(requestedModels, item.event.Model, item.upstreamModel) {
			continue
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type analyticsRowScanner interface {
	Scan(dest ...any) error
}

func scanAnalyticsEvent(scanner analyticsRowScanner, prices modelPriceIndex, modelAliases modelAliasIndex) (analyticsEvent, error) {
	var item analyticsEvent
	var failed int
	var rawInput, rawCached, rawCacheRead, rawCacheCreation int64
	if err := scanner.Scan(
		&item.id,
		&item.event.RequestID,
		&item.unixNano,
		&item.event.Provider,
		&item.event.Model,
		&item.event.ModelAlias,
		&item.event.Endpoint,
		&item.event.AuthIndex,
		&item.event.AuthFileName,
		&item.event.APIKeyHash,
		&item.event.CredentialKeyHash,
		&item.event.AccountRef,
		&item.event.AuthType,
		&item.event.ExecutorType,
		&item.event.ServiceTier,
		&item.event.CacheInputMode,
		&item.event.ReasoningEffort,
		&item.event.StatusCode,
		&item.event.LatencyMS,
		&item.event.TTFTMS,
		&item.event.FailStatusCode,
		&item.event.FailSummary,
		&item.event.FailBody,
		&rawInput,
		&item.tokens.OutputTokens,
		&item.tokens.ReasoningTokens,
		&rawCached,
		&rawCacheRead,
		&rawCacheCreation,
		&item.tokens.CachedTokens,
		&item.tokens.CacheReadTokens,
		&item.tokens.CacheCreationTokens,
		&item.tokens.UncachedInputTokens,
		&item.tokens.TotalInputTokens,
		&item.tokens.TotalTokens,
		&failed,
	); err != nil {
		return analyticsEvent{}, err
	}
	finalizeAnalyticsEvent(&item, failed, prices, modelAliases)
	return item, nil
}

func scanAnalyticsAggregateEvent(scanner analyticsRowScanner, prices modelPriceIndex, modelAliases modelAliasIndex) (analyticsEvent, error) {
	var item analyticsEvent
	var failed int
	if err := scanner.Scan(
		&item.unixNano,
		&item.event.Provider,
		&item.event.Model,
		&item.event.ModelAlias,
		&item.event.AuthIndex,
		&item.event.AuthFileName,
		&item.event.APIKeyHash,
		&item.event.CredentialKeyHash,
		&item.event.AccountRef,
		&item.event.AuthType,
		&item.event.ServiceTier,
		&item.tokens.OutputTokens,
		&item.tokens.ReasoningTokens,
		&item.tokens.CachedTokens,
		&item.tokens.CacheReadTokens,
		&item.tokens.CacheCreationTokens,
		&item.tokens.UncachedInputTokens,
		&item.tokens.TotalInputTokens,
		&item.tokens.TotalTokens,
		&failed,
	); err != nil {
		return analyticsEvent{}, err
	}
	finalizeAnalyticsEvent(&item, failed, prices, modelAliases)
	return item, nil
}

func finalizeAnalyticsEvent(item *analyticsEvent, failed int, prices modelPriceIndex, modelAliases modelAliasIndex) {
	if item == nil {
		return
	}
	item.tokens.InputTokens = item.tokens.TotalInputTokens
	item.event.NormalizedCached = item.tokens.CachedTokens
	item.event.NormalizedRead = item.tokens.CacheReadTokens
	item.event.NormalizedCreated = item.tokens.CacheCreationTokens
	item.event.UncachedInput = item.tokens.UncachedInputTokens
	item.event.TotalInput = item.tokens.TotalInputTokens
	item.event.Timestamp = time.Unix(0, item.unixNano).UTC()
	item.event.Tokens = item.tokens.Normalize()
	item.tokens = item.event.Tokens
	item.failed = failed != 0
	item.event.Failed = item.failed
	item.upstreamModel = strings.TrimSpace(item.event.Model)
	item.event.Model = modelAliases.resolve(item.event)
	if cost, ok, missing := costForUsageWithBehaviorAndPriceIndex(item.event.Model, item.upstreamModel, item.event.ServiceTier, item.tokens, prices); ok {
		item.cost = cost
		item.hasCost = true
	} else if len(missing) > 0 {
		item.missing = missing[0]
	}
}

func addAnalyticsEventsPageCursor(where string, args []any, page AnalyticsEventsPage) (string, []any) {
	if page.BeforeMS == nil {
		return where, args
	}
	start := time.UnixMilli(*page.BeforeMS).UTC()
	startNS := start.UnixNano()
	nextNS := start.Add(time.Millisecond).UnixNano()
	if page.BeforeID != nil && *page.BeforeID > 0 {
		return where + ` AND (ts_ns < ? OR (ts_ns >= ? AND ts_ns < ? AND id < ?))`, append(args, startNS, startNS, nextNS, *page.BeforeID)
	}
	return where + ` AND ts_ns < ?`, append(args, nextNS)
}

func matchesAnalyticsModelFilter(requestedModels []string, effectiveModel, upstreamModel string) bool {
	if len(requestedModels) == 0 {
		return true
	}
	for _, requestedModel := range requestedModels {
		if strings.EqualFold(requestedModel, effectiveModel) || strings.EqualFold(requestedModel, upstreamModel) {
			return true
		}
	}
	return false
}

func buildAnalyticsWhere(req AnalyticsRequest) (string, []any) {
	return buildAnalyticsWhereWithModelAliasIndex(req, compileModelAliasIndex(req.ModelAliases))
}

func buildAnalyticsWhereWithModelAliasIndex(req AnalyticsRequest, modelAliases modelAliasIndex) (string, []any) {
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
	addAnalyticsModelFilter(req.Filters.Models, modelAliases, &clauses, &args)
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

func addAnalyticsModelFilter(values []string, modelAliases modelAliasIndex, clauses *[]string, args *[]any) {
	requested := cleanedAnalyticsValues(values)
	if len(requested) == 0 || clauses == nil || args == nil {
		return
	}
	modelCandidates := modelAliases.expandRequestedModels(requested)
	aliasPlaceholders := make([]string, len(requested))
	for i, value := range requested {
		aliasPlaceholders[i] = "?"
		*args = append(*args, value)
	}
	modelPlaceholders := make([]string, len(modelCandidates))
	for i, value := range modelCandidates {
		modelPlaceholders[i] = "?"
		*args = append(*args, value)
	}
	*clauses = append(*clauses, "(model_alias COLLATE NOCASE IN ("+strings.Join(aliasPlaceholders, ",")+") OR model COLLATE NOCASE IN ("+strings.Join(modelPlaceholders, ",")+"))")
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

func buildAnalyticsEventsPage(events []analyticsEvent, page AnalyticsEventsPage) *AnalyticsEventsResponse {
	limit := normalizeAnalyticsEventsLimit(page.Limit)
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
		items = append(items, analyticsEventRow(event))
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

func buildAnalyticsEventsPageFromDescending(events []analyticsEvent, limit int, totalCount int64) *AnalyticsEventsResponse {
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	items := make([]AnalyticsEventRow, 0, len(events))
	for _, event := range events {
		items = append(items, analyticsEventRow(event))
	}
	resp := &AnalyticsEventsResponse{
		Items:      items,
		HasMore:    hasMore,
		TotalCount: totalCount,
	}
	if len(items) > 0 {
		last := items[len(items)-1]
		resp.NextBeforeMS = last.TimestampMS
		resp.NextBeforeID = last.ID
	}
	return resp
}

func analyticsEventRow(event analyticsEvent) AnalyticsEventRow {
	row := AnalyticsEventRow{
		ID:                    event.id,
		RequestID:             event.event.RequestID,
		TimestampMS:           event.event.Timestamp.UnixMilli(),
		Provider:              event.event.Provider,
		Model:                 event.event.Model,
		Endpoint:              event.event.Endpoint,
		AuthIndex:             event.event.AuthIndex,
		AuthFileName:          event.event.AuthFileName,
		APIKeyHash:            event.event.APIKeyHash,
		CredentialKeyHash:     event.event.CredentialKeyHash,
		AccountRef:            event.event.AccountRef,
		AuthType:              event.event.AuthType,
		ExecutorType:          event.event.ExecutorType,
		ServiceTier:           event.event.ServiceTier,
		CacheInputMode:        event.event.CacheInputMode,
		ReasoningEffort:       event.event.ReasoningEffort,
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
	if event.upstreamModel != "" && event.upstreamModel != event.event.Model {
		row.UpstreamModel = event.upstreamModel
	}
	if event.hasCost {
		row.EstimatedCostUSD = floatPtr(event.cost)
	}
	return row
}

func normalizeAnalyticsEventsLimit(limit int) int {
	if limit <= 0 {
		return defaultAnalyticsEventsLimit
	}
	if limit > maxAnalyticsEventsLimit {
		return maxAnalyticsEventsLimit
	}
	return limit
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
	if event.hasCost {
		agg.cost += event.cost
		agg.hasCost = true
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
