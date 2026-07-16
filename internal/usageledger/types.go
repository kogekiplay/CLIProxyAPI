package usageledger

import (
	"context"
	"time"
)

// TokenUsage stores token buckets collected from one request or an aggregate.
type TokenUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	UncachedInputTokens int64 `json:"uncached_input_tokens,omitempty"`
	TotalInputTokens    int64 `json:"total_input_tokens,omitempty"`
	OutputTokens        int64 `json:"output_tokens"`
	ReasoningTokens     int64 `json:"reasoning_tokens"`
	CachedTokens        int64 `json:"cached_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
}

// Normalize fills derived totals without changing explicit token buckets.
func (t TokenUsage) Normalize() TokenUsage {
	if t.TotalTokens <= 0 {
		t.TotalTokens = t.InputTokens + t.OutputTokens + t.ReasoningTokens
	}
	if t.TotalTokens <= 0 {
		t.TotalTokens = t.InputTokens + t.OutputTokens + t.ReasoningTokens + t.CachedTokens
	}
	return t
}

// Add returns the bucket-wise sum of two token values.
func (t TokenUsage) Add(other TokenUsage) TokenUsage {
	return TokenUsage{
		InputTokens:         t.InputTokens + other.InputTokens,
		UncachedInputTokens: t.UncachedInputTokens + other.UncachedInputTokens,
		TotalInputTokens:    t.TotalInputTokens + other.TotalInputTokens,
		OutputTokens:        t.OutputTokens + other.OutputTokens,
		ReasoningTokens:     t.ReasoningTokens + other.ReasoningTokens,
		CachedTokens:        t.CachedTokens + other.CachedTokens,
		CacheReadTokens:     t.CacheReadTokens + other.CacheReadTokens,
		CacheCreationTokens: t.CacheCreationTokens + other.CacheCreationTokens,
		TotalTokens:         t.TotalTokens + other.TotalTokens,
	}
}

// ModelPrice stores USD prices per one million tokens.
type ModelPrice struct {
	Model              string  `json:"model"`
	InputPer1M         float64 `json:"input_per_1m"`
	OutputPer1M        float64 `json:"output_per_1m"`
	CacheReadPer1M     float64 `json:"cache_read_per_1m"`
	CacheCreationPer1M float64 `json:"cache_creation_per_1m"`
	CachedPer1M        float64 `json:"cached_per_1m,omitempty"`
	Source             string  `json:"source"`
	SourceModelID      string  `json:"source_model_id,omitempty"`
	UpdatedAt          string  `json:"updated_at"`
}

// Event stores a single request usage event.
type Event struct {
	RequestID         string
	Timestamp         time.Time
	Provider          string
	Model             string
	ModelAlias        string
	Endpoint          string
	AuthIndex         string
	AuthFileName      string
	APIKeyHash        string
	CredentialKeyHash string
	AccountRef        string
	AuthType          string
	ExecutorType      string
	ServiceTier       string
	CacheInputMode    string
	NormalizedCached  int64
	NormalizedRead    int64
	NormalizedCreated int64
	UncachedInput     int64
	TotalInput        int64
	ReasoningEffort   string
	StatusCode        int
	LatencyMS         int64
	TTFTMS            int64
	FailStatusCode    int
	FailSummary       string
	FailBody          string
	Tokens            TokenUsage
	Failed            bool
}

// AnalyticsRequest describes a management analytics query over raw usage events.
type AnalyticsRequest struct {
	FromMS       int64            `json:"from_ms"`
	ToMS         int64            `json:"to_ms"`
	Filters      AnalyticsFilters `json:"filters"`
	Include      AnalyticsInclude `json:"include"`
	ModelAliases []ModelAliasRule `json:"-"`
}

// AnalyticsFilters scopes an analytics query.
type AnalyticsFilters struct {
	Providers     []string `json:"providers"`
	Models        []string `json:"models"`
	AuthFiles     []string `json:"auth_files"`
	AuthIndices   []string `json:"auth_indices"`
	APIKeyHashes  []string `json:"api_key_hashes"`
	Accounts      []string `json:"accounts"`
	FailedOnly    bool     `json:"failed_only"`
	IncludeFailed *bool    `json:"include_failed"`
}

// AnalyticsInclude selects which analytics blocks are returned.
type AnalyticsInclude struct {
	Summary         bool                 `json:"summary"`
	Timeline        bool                 `json:"timeline"`
	ModelStats      bool                 `json:"model_stats"`
	APIKeyStats     bool                 `json:"api_key_stats"`
	CredentialStats bool                 `json:"credential_stats"`
	EventsPage      *AnalyticsEventsPage `json:"events_page"`
}

// AnalyticsEventsPage requests a descending event page.
type AnalyticsEventsPage struct {
	Limit             int    `json:"limit"`
	BeforeMS          *int64 `json:"before_ms,omitempty"`
	BeforeID          *int64 `json:"before_id,omitempty"`
	IncludeTotalCount *bool  `json:"include_total_count,omitempty"`
}

// AnalyticsResponse contains management-facing usage analytics.
type AnalyticsResponse struct {
	GeneratedAtMS   int64                     `json:"generated_at_ms"`
	Summary         *AnalyticsSummary         `json:"summary,omitempty"`
	Timeline        []AnalyticsTimelinePoint  `json:"timeline,omitempty"`
	ModelStats      []AnalyticsModelStat      `json:"model_stats,omitempty"`
	APIKeyStats     []AnalyticsAPIKeyStat     `json:"api_key_stats,omitempty"`
	CredentialStats []AnalyticsCredentialStat `json:"credential_stats,omitempty"`
	Events          *AnalyticsEventsResponse  `json:"events,omitempty"`
}

type AnalyticsSummary struct {
	TotalCalls          int64    `json:"total_calls"`
	SuccessCalls        int64    `json:"success_calls"`
	FailureCalls        int64    `json:"failure_calls"`
	InputTokens         int64    `json:"input_tokens"`
	UncachedInputTokens int64    `json:"uncached_input_tokens"`
	TotalInputTokens    int64    `json:"total_input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	ReasoningTokens     int64    `json:"reasoning_tokens"`
	CachedTokens        int64    `json:"cached_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	CacheCreationTokens int64    `json:"cache_creation_tokens"`
	TotalTokens         int64    `json:"total_tokens"`
	CacheHitRate        float64  `json:"cache_hit_rate"`
	TotalCost           *float64 `json:"total_cost,omitempty"`
}

type AnalyticsTimelinePoint struct {
	BucketMS            int64    `json:"bucket_ms"`
	Calls               int64    `json:"calls"`
	Success             int64    `json:"success"`
	Failure             int64    `json:"failure"`
	InputTokens         int64    `json:"input_tokens"`
	UncachedInputTokens int64    `json:"uncached_input_tokens"`
	TotalInputTokens    int64    `json:"total_input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	ReasoningTokens     int64    `json:"reasoning_tokens"`
	CachedTokens        int64    `json:"cached_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	CacheCreationTokens int64    `json:"cache_creation_tokens"`
	TotalTokens         int64    `json:"total_tokens"`
	CacheHitRate        float64  `json:"cache_hit_rate"`
	Cost                *float64 `json:"cost,omitempty"`
}

type AnalyticsModelStat struct {
	Model               string   `json:"model"`
	Calls               int64    `json:"calls"`
	SuccessCalls        int64    `json:"success_calls"`
	FailureCalls        int64    `json:"failure_calls"`
	InputTokens         int64    `json:"input_tokens"`
	UncachedInputTokens int64    `json:"uncached_input_tokens"`
	TotalInputTokens    int64    `json:"total_input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	ReasoningTokens     int64    `json:"reasoning_tokens"`
	CachedTokens        int64    `json:"cached_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	CacheCreationTokens int64    `json:"cache_creation_tokens"`
	TotalTokens         int64    `json:"total_tokens"`
	CacheHitRate        float64  `json:"cache_hit_rate"`
	Cost                *float64 `json:"cost,omitempty"`
}

type AnalyticsAPIKeyStat struct {
	Provider            string   `json:"provider"`
	Providers           []string `json:"providers,omitempty"`
	APIKeyHash          string   `json:"api_key_hash"`
	APIKeyPreview       string   `json:"api_key_preview,omitempty"`
	AccountRef          string   `json:"account_ref,omitempty"`
	Calls               int64    `json:"calls"`
	SuccessCalls        int64    `json:"success_calls"`
	FailureCalls        int64    `json:"failure_calls"`
	InputTokens         int64    `json:"input_tokens"`
	UncachedInputTokens int64    `json:"uncached_input_tokens"`
	TotalInputTokens    int64    `json:"total_input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	ReasoningTokens     int64    `json:"reasoning_tokens"`
	CachedTokens        int64    `json:"cached_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	CacheCreationTokens int64    `json:"cache_creation_tokens"`
	TotalTokens         int64    `json:"total_tokens"`
	CacheHitRate        float64  `json:"cache_hit_rate"`
	Cost                *float64 `json:"cost,omitempty"`
}

type AnalyticsCredentialStat struct {
	Provider              string   `json:"provider"`
	AuthIndex             string   `json:"auth_index"`
	AuthFileName          string   `json:"auth_file_name"`
	CredentialDisplayName string   `json:"credential_display_name,omitempty"`
	AccountRef            string   `json:"account_ref"`
	Calls                 int64    `json:"calls"`
	SuccessCalls          int64    `json:"success_calls"`
	FailureCalls          int64    `json:"failure_calls"`
	InputTokens           int64    `json:"input_tokens"`
	UncachedInputTokens   int64    `json:"uncached_input_tokens"`
	TotalInputTokens      int64    `json:"total_input_tokens"`
	OutputTokens          int64    `json:"output_tokens"`
	ReasoningTokens       int64    `json:"reasoning_tokens"`
	CachedTokens          int64    `json:"cached_tokens"`
	CacheReadTokens       int64    `json:"cache_read_tokens"`
	CacheCreationTokens   int64    `json:"cache_creation_tokens"`
	TotalTokens           int64    `json:"total_tokens"`
	CacheHitRate          float64  `json:"cache_hit_rate"`
	Cost                  *float64 `json:"cost,omitempty"`
}

type AnalyticsEventsResponse struct {
	Items        []AnalyticsEventRow `json:"items"`
	NextBeforeMS int64               `json:"next_before_ms,omitempty"`
	NextBeforeID int64               `json:"next_before_id,omitempty"`
	HasMore      bool                `json:"has_more"`
	TotalCount   int64               `json:"total_count"`
}

type AnalyticsEventRow struct {
	ID                    int64      `json:"id"`
	RequestID             string     `json:"request_id"`
	TimestampMS           int64      `json:"timestamp_ms"`
	Provider              string     `json:"provider"`
	Model                 string     `json:"model"`
	UpstreamModel         string     `json:"upstream_model,omitempty"`
	Endpoint              string     `json:"endpoint"`
	AuthIndex             string     `json:"auth_index"`
	AuthFileName          string     `json:"auth_file_name"`
	CredentialDisplayName string     `json:"credential_display_name,omitempty"`
	APIKeyHash            string     `json:"api_key_hash"`
	CredentialKeyHash     string     `json:"credential_key_hash"`
	AccountRef            string     `json:"account_ref"`
	AuthType              string     `json:"auth_type"`
	ExecutorType          string     `json:"executor_type,omitempty"`
	ServiceTier           string     `json:"service_tier"`
	CacheInputMode        string     `json:"cache_input_mode,omitempty"`
	ReasoningEffort       string     `json:"reasoning_effort,omitempty"`
	StatusCode            int        `json:"status_code,omitempty"`
	LatencyMS             *int64     `json:"latency_ms,omitempty"`
	TTFTMS                *int64     `json:"ttft_ms,omitempty"`
	FailStatusCode        int        `json:"fail_status_code,omitempty"`
	FailSummary           string     `json:"fail_summary,omitempty"`
	FailBody              string     `json:"fail_body,omitempty"`
	Tokens                TokenUsage `json:"tokens"`
	Failed                bool       `json:"failed"`
	EstimatedCostUSD      *float64   `json:"estimated_cost_usd,omitempty"`
	MissingPriceModelName string     `json:"missing_price_model_name,omitempty"`
}

// Window defines an inclusive start and exclusive end query range.
type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// IsZero reports whether the window has no usable bounds.
func (w Window) IsZero() bool {
	return w.Start.IsZero() || w.End.IsZero() || !w.End.After(w.Start)
}

// SummaryFilter scopes a usage summary query to one credential/account/window.
type SummaryFilter struct {
	Provider   string
	Model      string
	AuthIndex  string
	APIKeyHash string
	AccountRef string
	Window     Window
}

// ModelSummary stores usage for one model in a summary response.
type ModelSummary struct {
	Model              string     `json:"model"`
	RequestCount       int64      `json:"request_count"`
	FailedCount        int64      `json:"failed_count"`
	Tokens             TokenUsage `json:"tokens"`
	EstimatedCostUSD   *float64   `json:"estimated_cost_usd"`
	MissingPriceModels []string   `json:"missing_price_models,omitempty"`
}

// Summary stores aggregate usage and cost data.
type Summary struct {
	Window             Window         `json:"window"`
	RequestCount       int64          `json:"request_count"`
	FailedCount        int64          `json:"failed_count"`
	Tokens             TokenUsage     `json:"tokens"`
	EstimatedCostUSD   *float64       `json:"estimated_cost_usd"`
	MissingPriceModels []string       `json:"missing_price_models"`
	Rows               []ModelSummary `json:"rows"`
	Source             string         `json:"source"`
}

// Store is the persistence surface used by management APIs and usage plugins.
type Store interface {
	InsertEvent(context.Context, Event) (bool, error)
	Summary(context.Context, SummaryFilter) (Summary, error)
	Analytics(context.Context, AnalyticsRequest) (AnalyticsResponse, error)
	ListModelPrices(context.Context) ([]ModelPrice, error)
	UpsertModelPrice(context.Context, ModelPrice) error
	ReplaceModelPrices(context.Context, []ModelPrice) error
	DeleteModelPrice(context.Context, string) error
	CleanupBefore(context.Context, time.Time) (int64, error)
	Close() error
}
