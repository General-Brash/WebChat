package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	appbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/billing"
	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	sub2infra "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/sub2"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	sub2port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
	"github.com/google/uuid"
)

func canonicalSub2Triggerer(ctx context.Context, requestedUserID uint) (uint, error) {
	subject := llm.ExecutionSubjectFromContext(ctx)
	if !subject.HasTriggerer() || requestedUserID == 0 {
		return 0, llm.ErrTrustedTriggerRequired
	}
	if requestedUserID != subject.TriggererUserID {
		return 0, llm.ErrTrustedTriggerMismatch
	}
	return subject.TriggererUserID, nil
}

func (g *sub2Gateway) AuthorizeSub2Usage(ctx context.Context, userID uint, name, ref string) (*domainbilling.UsageAuthorization, error) {
	triggererUserID, err := canonicalSub2Triggerer(ctx, userID)
	if err != nil {
		return nil, err
	}
	identity, err := g.auth.ResolveSub2Identity(ctx, triggererUserID)
	if err != nil {
		return nil, err
	}
	state, err := g.publicationForIdentity(ctx, identity)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ref) == "" {
		ref = uuid.NewString()
	}
	return &domainbilling.UsageAuthorization{Mode: "sub2", UserID: triggererUserID, RefNo: ref, PricingVersion: state.PricingVersion, PlayerVersion: state.PlayerVersion, GroupRevision: state.GroupRevision}, nil
}

func sub2ServiceSettlementItems(usage []appbilling.MCPToolUsageInput) []sub2port.ServiceSettlementItem {
	items := make([]sub2port.ServiceSettlementItem, 0, len(usage))
	for _, tool := range usage {
		if tool.CallCount > 0 && tool.PriceNanousd > 0 {
			items = append(items, sub2port.ServiceSettlementItem{
				Code:                fmt.Sprintf("mcp:%d:%s", tool.ServerID, strings.TrimSpace(tool.ToolName)),
				Count:               tool.CallCount,
				ExpectedUnitNanousd: tool.PriceNanousd,
			})
		}
	}
	return sub2port.NormalizeServiceSettlementItems(items)
}

func buildSub2ServiceSettlementRequest(application, runID string, usage []appbilling.MCPToolUsageInput) (sub2port.ServiceSettlementRequest, error) {
	items := sub2ServiceSettlementItems(usage)
	if len(items) == 0 {
		return sub2port.ServiceSettlementRequest{}, nil
	}
	fingerprint, err := sub2port.ServiceSettlementPayloadFingerprint(application, runID, items)
	if err != nil {
		return sub2port.ServiceSettlementRequest{}, err
	}
	return sub2port.ServiceSettlementRequest{
		Application:        strings.TrimSpace(application),
		RunID:              strings.TrimSpace(runID),
		RequestKey:         sub2port.ServiceSettlementRequestKey(application, runID, fingerprint),
		PayloadFingerprint: fingerprint,
		Items:              items,
	}, nil
}

// reconcileSub2ServiceSettlement queries the authority before every service
// command. Only an authoritative NOT_SUBMITTED result combined with a fully
// SETTLED parent execution permits a new settlement command. UNKNOWN, CONFLICT
// and transport errors are quarantined and are never replayed blindly.
func (g *sub2Gateway) reconcileSub2ServiceSettlement(ctx context.Context, identity sub2port.IdentityRef, request sub2port.ServiceSettlementRequest, parentSettled bool) (string, *sub2port.ServiceSettlementResponse) {
	query, err := g.client.QueryServiceSettlement(ctx, sub2port.ServiceSettlementQueryRequest{
		Identity:           identity,
		Application:        request.Application,
		RunID:              request.RunID,
		RequestKey:         request.RequestKey,
		PayloadFingerprint: request.PayloadFingerprint,
	})
	if err != nil {
		return sub2port.ServiceSettlementStateUnknown, nil
	}
	if query == nil {
		return sub2port.ServiceSettlementStateUnknown, nil
	}
	switch strings.ToUpper(strings.TrimSpace(query.State)) {
	case sub2port.ServiceSettlementStateSettled:
		return sub2port.ServiceSettlementStateSettled, query
	case sub2port.ServiceSettlementStateParentPending:
		return sub2port.ServiceSettlementStateParentPending, query
	case sub2port.ServiceSettlementStateConflict:
		return sub2port.ServiceSettlementStateConflict, query
	case sub2port.ServiceSettlementStateNotSubmitted:
		if !parentSettled {
			return sub2port.ServiceSettlementStateParentPending, query
		}
		settled, settleErr := g.client.SettleServices(ctx, request)
		if settleErr != nil {
			var remote *sub2infra.RemoteError
			if errors.As(settleErr, &remote) && strings.EqualFold(strings.TrimSpace(remote.Code), "parent_settlement_pending") {
				return sub2port.ServiceSettlementStateParentPending, query
			}
			return sub2port.ServiceSettlementStateUnknown, query
		}
		if settled == nil || !strings.EqualFold(strings.TrimSpace(settled.State), sub2port.ServiceSettlementStateSettled) {
			return sub2port.ServiceSettlementStateUnknown, settled
		}
		return sub2port.ServiceSettlementStateSettled, settled
	default:
		return sub2port.ServiceSettlementStateUnknown, query
	}
}


const (
	sub2AuthoritativeUsageAvailable   = "AVAILABLE"
	sub2AuthoritativeUsagePending     = "PENDING"
	sub2AuthoritativeUsageUnknown     = "UNKNOWN"
	sub2AuthoritativeUsageMissing     = "MISSING"
	sub2AuthoritativeUsageUnavailable = "UNAVAILABLE"
	sub2AuthoritativeUsageQuarantined = "QUARANTINED"
)

type sub2ExecutionUsageQuery struct {
	ExecutionID string
	Result      *sub2port.ExecutionQueryResponse
	Err         error
}

type sub2UsageAccumulator struct {
	inputTokens          int64
	inputTokensPresent   bool
	outputTokens         int64
	outputTokensPresent  bool
	cacheReadTokens      int64
	cacheReadPresent     bool
	cacheWriteTokens     int64
	cacheWritePresent    bool
	cacheWrite5mTokens   int64
	cacheWrite5mPresent  bool
	cacheWrite1hTokens   int64
	cacheWrite1hPresent  bool
	callCount            int64
	callCountPresent     bool
	durationSeconds      int64
	durationPresent      bool
	serviceTier          string
}

type sub2UsageProjection struct {
	State          string
	Complete       bool
	ParentSettled  bool
	BilledNanousd  int64
	Refs           []map[string]any
	Snapshots      []*sub2port.AuthoritativeUsageSnapshot
	Outboxes       []map[string]any
	Accumulator    sub2UsageAccumulator
}

func (g *sub2Gateway) querySub2ExecutionResults(ctx context.Context, identity sub2port.IdentityRef, application string, executionIDs []string) []sub2ExecutionUsageQuery {
	results := make([]sub2ExecutionUsageQuery, 0, len(executionIDs))
	for _, executionID := range executionIDs {
		executionID = strings.TrimSpace(executionID)
		if executionID == "" {
			results = append(results, sub2ExecutionUsageQuery{Err: errors.New("Sub2 execution id is missing")})
			continue
		}
		if g == nil || g.client == nil {
			results = append(results, sub2ExecutionUsageQuery{ExecutionID: executionID, Err: errors.New("Sub2 authority client is unavailable")})
			continue
		}
		result, err := g.client.QueryExecution(ctx, sub2port.ExecutionQueryRequest{
			Identity:      identity,
			Application:   strings.TrimSpace(application),
			ExecutionID:   executionID,
			ExpectedEpoch: identity.IdentityEpoch,
		})
		results = append(results, sub2ExecutionUsageQuery{ExecutionID: executionID, Result: result, Err: err})
	}
	return results
}

func sub2UsageOutboxPending(status *sub2port.UsageOutboxStatus) bool {
	if status == nil {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(status.State)) {
	case "DONE", "SUCCEEDED", "SUCCESS", "COMPLETED", "SETTLED":
		return false
	default:
		return true
	}
}

func sub2UsageErrorCode(primary, fallback string) string {
	if value := strings.TrimSpace(primary); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func sub2AuthoritativeUsageState(result *sub2port.ExecutionQueryResponse, queryErr error) (string, string) {
	if queryErr != nil {
		return sub2AuthoritativeUsageUnknown, "authoritative_usage_query_failed"
	}
	if result == nil {
		return sub2AuthoritativeUsageUnknown, "authoritative_usage_result_missing"
	}
	state := strings.ToUpper(strings.TrimSpace(result.State))
	if state == "UNKNOWN" {
		return sub2AuthoritativeUsageUnknown, sub2UsageErrorCode(result.ErrorCode, "execution_unknown")
	}
	if result.AuthoritativeUsage == nil {
		if !result.Terminal || state != sub2port.ServiceSettlementStateSettled || sub2UsageOutboxPending(result.UsageOutbox) {
			return sub2AuthoritativeUsagePending, sub2UsageErrorCode(result.ErrorCode, "authoritative_usage_pending")
		}
		return sub2AuthoritativeUsageMissing, "authoritative_usage_missing"
	}
	if err := validateSub2AuthoritativeUsageSnapshot(result.AuthoritativeUsage); err != nil {
		return sub2AuthoritativeUsageQuarantined, "authoritative_usage_invalid"
	}
	availability := strings.ToLower(strings.TrimSpace(string(result.AuthoritativeUsage.Availability)))
	switch availability {
	case string(sub2port.UsageSnapshotAvailable):
		if !result.Terminal || state != sub2port.ServiceSettlementStateSettled || strings.ToUpper(strings.TrimSpace(result.AuthoritativeUsage.State)) != sub2port.ServiceSettlementStateSettled {
			return sub2AuthoritativeUsageQuarantined, "authoritative_usage_state_mismatch"
		}
		return sub2AuthoritativeUsageAvailable, ""
	case string(sub2port.UsageSnapshotPending):
		return sub2AuthoritativeUsagePending, sub2UsageErrorCode(result.AuthoritativeUsage.ErrorCode, "settlement_pending")
	case string(sub2port.UsageSnapshotUnavailable):
		return sub2AuthoritativeUsageUnavailable, sub2UsageErrorCode(result.AuthoritativeUsage.ErrorCode, "authoritative_usage_unavailable")
	case string(sub2port.UsageSnapshotQuarantined):
		return sub2AuthoritativeUsageQuarantined, sub2UsageErrorCode(result.AuthoritativeUsage.ErrorCode, "authoritative_usage_quarantined")
	default:
		return sub2AuthoritativeUsageQuarantined, "authoritative_usage_availability_invalid"
	}
}

func validateSub2AuthoritativeUsageSnapshot(snapshot *sub2port.AuthoritativeUsageSnapshot) error {
	if snapshot == nil {
		return errors.New("authoritative usage snapshot is nil")
	}
	if snapshot.SchemaVersion != sub2port.AuthoritativeUsageSnapshotVersion {
		return fmt.Errorf("unsupported authoritative usage snapshot version %d", snapshot.SchemaVersion)
	}
	availability := strings.ToLower(strings.TrimSpace(string(snapshot.Availability)))
	switch availability {
	case string(sub2port.UsageSnapshotAvailable), string(sub2port.UsageSnapshotPending), string(sub2port.UsageSnapshotUnavailable), string(sub2port.UsageSnapshotQuarantined):
	default:
		return errors.New("authoritative usage snapshot availability is invalid")
	}
	if snapshot.Authoritative != (availability == string(sub2port.UsageSnapshotAvailable)) {
		return errors.New("authoritative usage snapshot authority flag is inconsistent")
	}
	if snapshot.BilledNanousd < 0 {
		return errors.New("authoritative usage snapshot amount is negative")
	}
	if usage := snapshot.Usage; usage != nil {
		for name, value := range map[string]*int64{
			"input_tokens":             usage.InputTokens,
			"output_tokens":            usage.OutputTokens,
			"cache_creation_tokens":    usage.CacheCreationTokens,
			"cache_read_tokens":         usage.CacheReadTokens,
			"cache_creation_5m_tokens": usage.CacheCreation5mTokens,
			"cache_creation_1h_tokens": usage.CacheCreation1hTokens,
		} {
			if err := validateSub2UsageCounterMap(name, value); err != nil {
				return err
			}
		}
		if usage.Image != nil {
			for name, value := range map[string]*int64{
				"image_count":         usage.Image.Count,
				"image_input_tokens":  usage.Image.InputTokens,
				"image_output_tokens": usage.Image.OutputTokens,
			} {
				if err := validateSub2UsageCounterMap(name, value); err != nil {
					return err
				}
			}
		}
		if usage.Video != nil {
			for name, value := range map[string]*int64{
				"video_count":            usage.Video.Count,
				"video_duration_seconds": usage.Video.DurationSeconds,
			} {
				if err := validateSub2UsageCounterMap(name, value); err != nil {
					return err
				}
			}
		}
		if usage.Audio != nil {
			for name, value := range map[string]*int64{
				"audio_count":            usage.Audio.Count,
				"audio_duration_seconds": usage.Audio.DurationSeconds,
				"audio_input_tokens":     usage.Audio.InputTokens,
				"audio_output_tokens":    usage.Audio.OutputTokens,
			} {
				if err := validateSub2UsageCounterMap(name, value); err != nil {
					return err
				}
			}
		}
		for name, value := range usage.NativeToolCalls {
			if value < 0 {
				return fmt.Errorf("%s is negative", name)
			}
		}
	}
	for _, record := range snapshot.Records {
		if record.UsageID < 0 {
			return errors.New("authoritative usage record id is negative")
		}
		for name, value := range map[string]*int64{
			"record_input_tokens":             record.InputTokens,
			"record_output_tokens":            record.OutputTokens,
			"record_cache_creation_tokens":    record.CacheCreationTokens,
			"record_cache_read_tokens":         record.CacheReadTokens,
			"record_cache_creation_5m_tokens":  record.CacheCreation5mTokens,
			"record_cache_creation_1h_tokens":  record.CacheCreation1hTokens,
			"record_image_count":               record.ImageCount,
			"record_image_input_tokens":        record.ImageInputTokens,
			"record_image_output_tokens":       record.ImageOutputTokens,
			"record_video_count":               record.VideoCount,
			"record_video_duration_seconds":    record.VideoDurationSeconds,
		} {
			if err := validateSub2UsageCounterMap(name, value); err != nil {
				return err
			}
		}
	}
	for _, charge := range snapshot.ServiceCharges {
		if charge.UsageID < 0 || charge.BilledNanousd < 0 {
			return errors.New("authoritative service charge is invalid")
		}
	}
	return nil
}

func validateSub2UsageCounterMap(name string, value *int64) error {
	if value != nil && *value < 0 {
		return fmt.Errorf("%s is negative", name)
	}
	return nil
}

func (p *sub2UsageProjection) applyState(state string) {
	if p == nil {
		return
	}
	if sub2UsageStatePriority(state) > sub2UsageStatePriority(p.State) {
		p.State = state
	}
}

func sub2UsageStatePriority(state string) int {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case sub2AuthoritativeUsageUnknown:
		return 60
	case sub2AuthoritativeUsageQuarantined:
		return 50
	case sub2AuthoritativeUsageMissing:
		return 45
	case sub2AuthoritativeUsageUnavailable:
		return 40
	case sub2AuthoritativeUsagePending:
		return 30
	case sub2AuthoritativeUsageAvailable:
		return 10
	default:
		return 0
	}
}

func projectSub2ExecutionUsage(queries []sub2ExecutionUsageQuery) sub2UsageProjection {
	projection := sub2UsageProjection{
		State:         sub2AuthoritativeUsageAvailable,
		Complete:      len(queries) > 0,
		ParentSettled: len(queries) > 0,
		Refs:          make([]map[string]any, 0, len(queries)),
		Snapshots:     make([]*sub2port.AuthoritativeUsageSnapshot, 0, len(queries)),
		Outboxes:      make([]map[string]any, 0, len(queries)),
	}
	if len(queries) == 0 {
		projection.State = sub2AuthoritativeUsagePending
		return projection
	}
	for _, query := range queries {
		executionID := strings.TrimSpace(query.ExecutionID)
		ref := map[string]any{"execution_id": executionID}
		if query.Err != nil || query.Result == nil {
			projection.Complete = false
			projection.ParentSettled = false
			state, code := sub2AuthoritativeUsageState(query.Result, query.Err)
			projection.applyState(state)
			ref["state"] = state
			ref["terminal"] = false
			ref["authoritative_usage_state"] = state
			if code != "" {
				ref["error_code"] = code
			}
			projection.Refs = append(projection.Refs, ref)
			continue
		}
		result := query.Result
		ref["state"] = strings.TrimSpace(result.State)
		ref["terminal"] = result.Terminal
		if value := strings.TrimSpace(result.AuthorityExecutionID); value != "" {
			ref["authority_execution_id"] = value
		}
		if value := strings.TrimSpace(result.AuthorityUsageID); value != "" {
			ref["authority_usage_id"] = value
		}
		if value := strings.TrimSpace(result.AuthorityTransactionID); value != "" {
			ref["authority_transaction_id"] = value
		}
		if result.UsageOutbox != nil {
			ref["usage_outbox"] = result.UsageOutbox
			projection.Outboxes = append(projection.Outboxes, map[string]any{
				"execution_id": executionID,
				"advisory":     true,
				"status":       result.UsageOutbox,
			})
		}
		if result.AuthoritativeUsage != nil {
			ref["authoritative_usage"] = result.AuthoritativeUsage
			projection.Snapshots = append(projection.Snapshots, result.AuthoritativeUsage)
		}
		usageState, code := sub2AuthoritativeUsageState(result, nil)
		if usageState == sub2AuthoritativeUsageAvailable {
			if err := projection.Accumulator.add(result.AuthoritativeUsage); err != nil {
				usageState = sub2AuthoritativeUsageQuarantined
				code = "authoritative_usage_measure_invalid"
			}
			if usageState == sub2AuthoritativeUsageAvailable {
				var err error
				projection.BilledNanousd, err = addSub2UsageAmount(projection.BilledNanousd, result.AuthoritativeUsage.BilledNanousd)
				if err != nil {
					usageState = sub2AuthoritativeUsageQuarantined
					code = "authoritative_usage_amount_out_of_range"
				}
			}
		}
		if usageState != sub2AuthoritativeUsageAvailable {
			projection.Complete = false
		}
		if !result.Terminal || !strings.EqualFold(strings.TrimSpace(result.State), sub2port.ServiceSettlementStateSettled) {
			projection.ParentSettled = false
		}
		projection.applyState(usageState)
		ref["authoritative_usage_state"] = usageState
		if code != "" {
			ref["error_code"] = code
		}
		projection.Refs = append(projection.Refs, ref)
	}
	return projection
}

func (a *sub2UsageAccumulator) add(snapshot *sub2port.AuthoritativeUsageSnapshot) error {
	if a == nil || snapshot == nil {
		return errors.New("authoritative usage snapshot is missing")
	}
	modelRecords := 0
	for _, record := range snapshot.Records {
		if strings.EqualFold(strings.TrimSpace(record.UsageClass), "service_fee") {
			continue
		}
		modelRecords++
		if a.serviceTier == "" {
			a.serviceTier = strings.TrimSpace(record.ServiceTier)
		}
	}
	if snapshot.Usage == nil {
		if modelRecords > 0 {
			return errors.New("model authoritative usage measures are missing")
		}
		// A service-only snapshot has no model usage. A nil Records list is
		// also accepted for additive contract compatibility; in that case the
		// typed Usage object still supplies the dimensions, but not call count.
		return nil
	}
	usage := snapshot.Usage
	if err := addSub2UsageDimension(&a.inputTokens, &a.inputTokensPresent, usage.InputTokens); err != nil {
		return err
	}
	if err := addSub2UsageDimension(&a.outputTokens, &a.outputTokensPresent, usage.OutputTokens); err != nil {
		return err
	}
	if err := addSub2UsageDimension(&a.cacheWriteTokens, &a.cacheWritePresent, usage.CacheCreationTokens); err != nil {
		return err
	}
	if err := addSub2UsageDimension(&a.cacheReadTokens, &a.cacheReadPresent, usage.CacheReadTokens); err != nil {
		return err
	}
	if err := addSub2UsageDimension(&a.cacheWrite5mTokens, &a.cacheWrite5mPresent, usage.CacheCreation5mTokens); err != nil {
		return err
	}
	if err := addSub2UsageDimension(&a.cacheWrite1hTokens, &a.cacheWrite1hPresent, usage.CacheCreation1hTokens); err != nil {
		return err
	}
	if modelRecords > 0 {
		if err := addSub2UsageAmount(a.callCount, int64(modelRecords)); err != nil {
			return err
		}
		a.callCount += int64(modelRecords)
		a.callCountPresent = true
	}
	if usage.Video != nil {
		if err := addSub2UsageDimension(&a.durationSeconds, &a.durationPresent, usage.Video.DurationSeconds); err != nil {
			return err
		}
	}
	return nil
}

func addSub2UsageDimension(dst *int64, present *bool, value *int64) error {
	if value == nil {
		return nil
	}
	if dst == nil || present == nil {
		return errors.New("authoritative usage destination is missing")
	}
	result, err := addSub2UsageAmount(*dst, *value)
	if err != nil {
		return err
	}
	*dst = result
	*present = true
	return nil
}

func addSub2UsageAmount(current, next int64) (int64, error) {
	if current < 0 || next < 0 || next > int64(^uint64(0)>>1)-current {
		return 0, errors.New("authoritative usage amount is out of range")
	}
	return current + next, nil
}

func applySub2UsageProjection(receipt *domainbilling.UsageLedger, projection sub2UsageProjection, complete bool) {
	if receipt == nil || !complete || !projection.Complete {
		return
	}
	receipt.BilledNanousd = projection.BilledNanousd
	accumulator := projection.Accumulator
	if accumulator.inputTokensPresent {
		receipt.InputTokens = accumulator.inputTokens
	}
	if accumulator.outputTokensPresent {
		receipt.OutputTokens = accumulator.outputTokens
	}
	if accumulator.cacheReadPresent {
		receipt.CacheReadTokens = accumulator.cacheReadTokens
	}
	if accumulator.cacheWritePresent {
		receipt.CacheWriteTokens = accumulator.cacheWriteTokens
	}
	if accumulator.cacheWrite5mPresent {
		receipt.CacheWrite5mTokens = accumulator.cacheWrite5mTokens
	}
	if accumulator.cacheWrite1hPresent {
		receipt.CacheWrite1hTokens = accumulator.cacheWrite1hTokens
	}
	if accumulator.callCountPresent {
		receipt.CallCount = accumulator.callCount
	}
	if accumulator.durationPresent {
		receipt.DurationSeconds = accumulator.durationSeconds
	}
	if accumulator.serviceTier != "" {
		receipt.ServiceTier = accumulator.serviceTier
	}
}

func sub2UsageProjectionUpdates(projection sub2UsageProjection) map[string]any {
	updates := map[string]any{"billed_nanousd": projection.BilledNanousd}
	accumulator := projection.Accumulator
	if accumulator.inputTokensPresent {
		updates["input_tokens"] = accumulator.inputTokens
	}
	if accumulator.outputTokensPresent {
		updates["output_tokens"] = accumulator.outputTokens
	}
	if accumulator.cacheReadPresent {
		updates["cache_read_tokens"] = accumulator.cacheReadTokens
	}
	if accumulator.cacheWritePresent {
		updates["cache_write_tokens"] = accumulator.cacheWriteTokens
	}
	if accumulator.cacheWrite5mPresent {
		updates["cache_write_5m_tokens"] = accumulator.cacheWrite5mTokens
	}
	if accumulator.cacheWrite1hPresent {
		updates["cache_write_1h_tokens"] = accumulator.cacheWrite1hTokens
	}
	if accumulator.callCountPresent {
		updates["call_count"] = accumulator.callCount
	}
	if accumulator.durationPresent {
		updates["duration_seconds"] = accumulator.durationSeconds
	}
	if accumulator.serviceTier != "" {
		updates["service_tier"] = accumulator.serviceTier
	}
	return updates
}

func (p sub2UsageProjection) authoritativeSnapshot() map[string]any {
	return map[string]any{
		"state":          p.State,
		"complete":       p.Complete,
		"parent_settled": p.ParentSettled,
		"snapshots":      p.Snapshots,
	}
}

func (g *sub2Gateway) BuildSub2UsageLedger(ctx context.Context, input appbilling.UsagePricingInput) (*domainbilling.UsageLedger, error) {
	triggererUserID, err := canonicalSub2Triggerer(ctx, input.UserID)
	if err != nil {
		return nil, err
	}
	authorization := input.Authorization
	if authorization == nil || authorization.Mode != "sub2" || authorization.UserID != triggererUserID {
		return nil, errors.New("Sub2 usage authorization is required")
	}
	now := input.BillingAt
	if now.IsZero() {
		now = time.Now()
	}
	// This ledger is a read-only Chat receipt. Usage and amount fields are filled
	// only from a complete authoritative_usage projection; request/result estimates
	// are deliberately not copied into the receipt.
	receipt := &domainbilling.UsageLedger{
		UserID:             triggererUserID,
		RefNo:              authorization.RefNo,
		ConversationID:     input.ConversationID,
		ProviderProtocol:   input.ProviderProtocol,
		PlatformModelName:  input.PlatformModelName,
		UpstreamModelName:  input.UpstreamModelName,
		UpstreamName:       input.UpstreamName,
		RoutedBindingCode:  input.RoutedBindingCode,
		BillingAt:          now,
		UsageDate:         now,
		BilledCurrency:     "USD",
	}
	var records []model.Sub2ExternalExecution
	if err := g.db.WithContext(ctx).Where("user_id=? AND application=? AND chat_run_id=?", triggererUserID, g.cfg.Sub2Application, authorization.RefNo).Order("id").Find(&records).Error; err != nil {
		return nil, err
	}
	identity, err := g.auth.ResolveSub2Identity(ctx, triggererUserID)
	if err != nil {
		return nil, err
	}
	serviceRequest, err := buildSub2ServiceSettlementRequest(g.cfg.Sub2Application, authorization.RefNo, input.MCPToolUsage)
	if err != nil {
		return nil, err
	}
	serviceRequest.Identity = identity
	serviceRequest.PricingVersion = authorization.PricingVersion
	executionIDs := make([]string, 0, len(records))
	for _, record := range records {
		executionIDs = append(executionIDs, record.ExecutionID)
	}
	projection := projectSub2ExecutionUsage(g.querySub2ExecutionResults(ctx, identity, g.cfg.Sub2Application, executionIDs))
	serviceState := sub2port.ServiceSettlementStateSettled
	var serviceResult *sub2port.ServiceSettlementResponse
	if len(serviceRequest.Items) > 0 {
		serviceState, serviceResult = g.reconcileSub2ServiceSettlement(ctx, identity, serviceRequest, projection.ParentSettled)
		// The service response is amount-only metadata. Once it is settled, query
		// the execution again so the receipt consumes the combined authoritative
		// usage snapshot, including committed service charges.
		if serviceState == sub2port.ServiceSettlementStateSettled {
			projection = projectSub2ExecutionUsage(g.querySub2ExecutionResults(ctx, identity, g.cfg.Sub2Application, executionIDs))
		}
	}
	pending := !projection.ParentSettled || !projection.Complete || serviceState != sub2port.ServiceSettlementStateSettled
	status := sub2port.ServiceSettlementStateSettled
	if pending {
		status = "PENDING"
	}
	applySub2UsageProjection(receipt, projection, !pending)
	serviceSnapshot := map[string]any{"state": serviceState}
	if serviceResult != nil {
		serviceSnapshot["execution_id"] = serviceResult.ExecutionID
		serviceSnapshot["usage_id"] = serviceResult.AuthorityUsageID
		serviceSnapshot["transaction_id"] = serviceResult.AuthorityTransactionID
		serviceSnapshot["billed_nanousd"] = serviceResult.BilledNanousd
		serviceSnapshot["amount_only_advisory"] = true
		serviceSnapshot["legacy_request"] = serviceResult.LegacyRequest
	}
	raw, _ := json.Marshal(map[string]any{
		"authority":                   "sub2",
		"authority_state":             status,
		"authoritative_usage_state":   projection.State,
		"authoritative_usage_complete": projection.Complete,
		"authoritative_usage":         projection.authoritativeSnapshot(),
		"usage_outbox":                projection.Outboxes,
		"service_settlement_state":    serviceState,
		"service_request_key":         serviceRequest.RequestKey,
		"service_payload_fingerprint": serviceRequest.PayloadFingerprint,
		"service_settlement":          serviceSnapshot,
		"pricing_version":              authorization.PricingVersion,
		"player_version":               authorization.PlayerVersion,
		"group_revision":               authorization.GroupRevision,
		"executions":                   projection.Refs,
		"service_items":                input.ServiceItems,
		"mcp_tool_usage":               input.MCPToolUsage,
	})
	receipt.PricingSnapshotJSON = string(raw)
	return receipt, nil
}

func authorizationUserID(authorization *domainbilling.UsageAuthorization) uint {
	if authorization == nil {
		return 0
	}
	return authorization.UserID
}

func (g *sub2Gateway) RecordSub2Usage(ctx context.Context, receipt *domainbilling.UsageLedger, authorization *domainbilling.UsageAuthorization) error {
	triggererUserID, err := canonicalSub2Triggerer(ctx, authorizationUserID(authorization))
	if err != nil {
		return err
	}
	if receipt == nil || authorization == nil || receipt.UserID != triggererUserID || authorization.Mode != "sub2" || authorization.Reservation != nil {
		return errors.New("invalid Sub2 receipt binding")
	}
	var snapshot struct {
		Authority string `json:"authority"`
	}
	if json.Unmarshal([]byte(receipt.PricingSnapshotJSON), &snapshot) != nil || snapshot.Authority != "sub2" {
		return errors.New("unverified receipt source")
	}
	receipt.RefNo = authorization.RefNo
	// AddUsage ONLY inserts a read-only receipt. It does not reserve, debit,
	// release, grant subscriptions or touch the local billing account.
	return g.prices.AddUsage(ctx, receipt)
}
