package sub2

import "time"

const AuthoritativeUsageSnapshotVersion = 1

type UsageSnapshotAvailability string

const (
	UsageSnapshotAvailable   UsageSnapshotAvailability = "available"
	UsageSnapshotPending     UsageSnapshotAvailability = "pending"
	UsageSnapshotUnavailable UsageSnapshotAvailability = "unavailable"
	UsageSnapshotQuarantined UsageSnapshotAvailability = "quarantined"
)

// AuthoritativeUsageSnapshot is optional for backward compatibility. When it is
// absent, the legacy amount/state response is not a full-usage authority.
type AuthoritativeUsageSnapshot struct {
	SchemaVersion           int                           `json:"schema_version"`
	Availability            UsageSnapshotAvailability     `json:"availability"`
	Authoritative           bool                          `json:"authoritative"`
	Source                  string                        `json:"source,omitempty"`
	ApplicationID           string                        `json:"application_id"`
	ExecutionID             string                        `json:"execution_id"`
	LogicalRunID            string                        `json:"logical_run_id"`
	State                   string                        `json:"state"`
	PayerUserID             int64                         `json:"payer_user_id"`
	UserID                  int64                         `json:"user_id"`
	APIKeyID                int64                         `json:"api_key_id,omitempty"`
	OriginalModel           string                        `json:"original_model"`
	Protocol                string                        `json:"protocol"`
	Purpose                 string                        `json:"purpose"`
	BilledNanousd           int64                         `json:"billed_nanousd"`
	AuthorityExecutionID    string                        `json:"authority_execution_id"`
	AuthorityUsageIDs       []string                      `json:"authority_usage_ids,omitempty"`
	AuthorityTransactionIDs []string                      `json:"authority_transaction_ids,omitempty"`
	RequestKeys             []string                      `json:"request_keys,omitempty"`
	RequestFingerprints     []string                      `json:"request_fingerprints,omitempty"`
	Usage                   *AuthoritativeUsageMeasures   `json:"usage,omitempty"`
	Records                 []AuthoritativeUsageRecord    `json:"records,omitempty"`
	ServiceCharges          []AuthoritativeServiceMeasure `json:"service_charges,omitempty"`
	UnavailableDimensions   []string                      `json:"unavailable_dimensions,omitempty"`
	ErrorCode               string                        `json:"error_code,omitempty"`
	CreatedAt               time.Time                     `json:"created_at"`
	UpdatedAt               time.Time                     `json:"updated_at"`
	TerminalAt              *time.Time                    `json:"terminal_at,omitempty"`
	CheckedAt               time.Time                     `json:"checked_at"`
}

type AuthoritativeUsageMeasures struct {
	InputTokens           *int64                   `json:"input_tokens,omitempty"`
	OutputTokens          *int64                   `json:"output_tokens,omitempty"`
	CacheCreationTokens   *int64                   `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens       *int64                   `json:"cache_read_tokens,omitempty"`
	CacheCreation5mTokens *int64                   `json:"cache_creation_5m_tokens,omitempty"`
	CacheCreation1hTokens *int64                   `json:"cache_creation_1h_tokens,omitempty"`
	ReasoningEfforts      []string                 `json:"reasoning_efforts,omitempty"`
	Image                 *AuthoritativeImageUsage `json:"image,omitempty"`
	Video                 *AuthoritativeVideoUsage `json:"video,omitempty"`
	Audio                 *AuthoritativeAudioUsage `json:"audio,omitempty"`
	NativeToolCalls       map[string]int64         `json:"native_tool_calls,omitempty"`
}

type AuthoritativeImageUsage struct {
	Count        *int64 `json:"count,omitempty"`
	InputTokens  *int64 `json:"input_tokens,omitempty"`
	OutputTokens *int64 `json:"output_tokens,omitempty"`
}

type AuthoritativeVideoUsage struct {
	Count           *int64   `json:"count,omitempty"`
	DurationSeconds *int64   `json:"duration_seconds,omitempty"`
	Resolutions     []string `json:"resolutions,omitempty"`
}

type AuthoritativeAudioUsage struct {
	Count           *int64 `json:"count,omitempty"`
	DurationSeconds *int64 `json:"duration_seconds,omitempty"`
	InputTokens     *int64 `json:"input_tokens,omitempty"`
	OutputTokens    *int64 `json:"output_tokens,omitempty"`
}

type AuthoritativeUsageRecord struct {
	UsageID                  int64     `json:"usage_id"`
	RequestKey               string    `json:"request_key"`
	RequestID                string    `json:"request_id"`
	Model                    string    `json:"model"`
	RequestedModel           string    `json:"requested_model,omitempty"`
	UsageClass               string    `json:"usage_class"`
	InputTokens              *int64    `json:"input_tokens,omitempty"`
	OutputTokens             *int64    `json:"output_tokens,omitempty"`
	CacheCreationTokens      *int64    `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens          *int64    `json:"cache_read_tokens,omitempty"`
	CacheCreation5mTokens    *int64    `json:"cache_creation_5m_tokens,omitempty"`
	CacheCreation1hTokens    *int64    `json:"cache_creation_1h_tokens,omitempty"`
	ImageCount               *int64    `json:"image_count,omitempty"`
	ImageInputTokens         *int64    `json:"image_input_tokens,omitempty"`
	ImageOutputTokens        *int64    `json:"image_output_tokens,omitempty"`
	VideoCount               *int64    `json:"video_count,omitempty"`
	VideoDurationSeconds     *int64    `json:"video_duration_seconds,omitempty"`
	VideoResolution          string    `json:"video_resolution,omitempty"`
	MediaType                string    `json:"media_type,omitempty"`
	ServiceTier              string    `json:"service_tier,omitempty"`
	ReasoningEffort          string    `json:"reasoning_effort,omitempty"`
	RequestedReasoningEffort string    `json:"requested_reasoning_effort,omitempty"`
	RequestType              string    `json:"request_type,omitempty"`
	Stream                   bool      `json:"stream,omitempty"`
	OpenAIWSMode             bool      `json:"openai_ws_mode,omitempty"`
	CreatedAt                time.Time `json:"created_at"`
}

type AuthoritativeServiceMeasure struct {
	UsageID       int64  `json:"usage_id"`
	RequestKey    string `json:"request_key"`
	BilledNanousd int64  `json:"billed_nanousd"`
	MeasureStatus string `json:"measure_status"`
}
