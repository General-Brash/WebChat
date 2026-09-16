package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/billing"
	domainsub2 "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/sub2"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	sub2port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
)

const (
	sub2ExecutionReconciliationInterval = 30 * time.Second
	sub2ExecutionReconciliationAge      = 20 * time.Second
	sub2ExecutionReconciliationBatch    = 100
)

// StartBackgroundWorkers starts the persistent, query-only execution recovery
// loop. It never dispatches a model request and therefore cannot turn an
// UNKNOWN result into a blind paid retry.
func (g *sub2Gateway) StartBackgroundWorkers(ctx context.Context) {
	if g == nil || ctx == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(sub2ExecutionReconciliationInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = g.ReconcilePendingExecutions(ctx)
			}
		}
	}()
}

// ReconcilePendingExecutions queries durable Chat-side execution records and
// applies only the authority result. Recovery is intentionally query-only:
// there is no provider/model dispatch path here.
func (g *sub2Gateway) ReconcilePendingExecutions(ctx context.Context) error {
	if g == nil || g.executions == nil || g.client == nil || g.auth == nil {
		return nil
	}
	records, err := g.executions.ListPendingExternalExecutions(
		ctx,
		time.Now().Add(-sub2ExecutionReconciliationAge),
		sub2ExecutionReconciliationBatch,
	)
	if err != nil {
		return err
	}
	var firstErr error
	users := make(map[uint]struct{}, len(records))
	for _, record := range records {
		if record == nil || strings.TrimSpace(record.ExecutionID) == "" || record.UserID == 0 {
			continue
		}
		users[record.UserID] = struct{}{}
		identity, identityErr := g.auth.ResolveSub2Identity(ctx, record.UserID)
		if identityErr != nil {
			if markErr := g.markPendingExecutionUnknown(ctx, record, "identity_recheck_required"); markErr != nil && firstErr == nil {
				firstErr = markErr
			}
			continue
		}
		queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		result, queryErr := g.client.QueryExecution(queryCtx, sub2port.ExecutionQueryRequest{
			Identity:      identity,
			Application:   record.Application,
			ExecutionID:   record.ExecutionID,
			ExpectedEpoch: record.IdentityEpoch,
		})
		cancel()
		if queryErr != nil {
			if markErr := g.markPendingExecutionUnknown(ctx, record, "reconciliation_query_pending"); markErr != nil && firstErr == nil {
				firstErr = markErr
			}
			continue
		}
		if updateErr := g.recordExecutionQueryResult(ctx, record.UserID, record.ExecutionID, result); updateErr != nil && firstErr == nil {
			firstErr = updateErr
		}
	}
	// Also discover pending usage projections that have no matching execution
	// row (for example, a crash between usage projection and the next query).
	// This is bounded and still enters only the authority query/settlement
	// reconciliation path; it never dispatches a provider request.
	var pendingUserIDs []uint
	if queryErr := g.db.WithContext(ctx).Model(&model.UsageLedger{}).
		Where("pricing_snapshot_json LIKE ? AND pricing_snapshot_json LIKE ?", `%"authority":"sub2"%`, `%"authority_state":"PENDING"%`).
		Distinct("user_id").
		Limit(sub2ExecutionReconciliationBatch).
		Pluck("user_id", &pendingUserIDs).Error; queryErr != nil && firstErr == nil {
		firstErr = queryErr
	}
	for _, userID := range pendingUserIDs {
		if userID != 0 {
			users[userID] = struct{}{}
		}
	}
	// Refresh any pending usage projections associated with the records just
	// queried. This remains projection-only and never dispatches a model.
	for userID := range users {
		if reconcileErr := g.reconcileReceipts(ctx, userID); reconcileErr != nil && firstErr == nil {
			firstErr = reconcileErr
		}
	}
	return firstErr
}

func (g *sub2Gateway) recordExecutionQueryResult(ctx context.Context, userID uint, executionID string, result *sub2port.ExecutionQueryResponse) error {
	if result == nil {
		return fmt.Errorf("empty Sub2 execution query result")
	}
	record, err := g.executions.GetExternalExecution(ctx, userID, executionID)
	if err != nil {
		return err
	}
	if domainsub2.IsTerminalExecutionState(record.State) {
		return nil
	}
	now := time.Now()
	state := domainsub2.NormalizeAuthorityExecutionState(result.State, result.Terminal)
	record.State = state
	record.TerminalStatus = strings.TrimSpace(result.State)
	record.AuthorityExecutionID = strings.TrimSpace(result.AuthorityExecutionID)
	record.AuthorityUsageID = strings.TrimSpace(result.AuthorityUsageID)
	record.AuthorityTransactionID = strings.TrimSpace(result.AuthorityTransactionID)
	record.ErrorCode = strings.TrimSpace(result.ErrorCode)
	record.ErrorMessage = truncateExecutionMessage(result.ErrorMessage)
	record.LastQueriedAt = &now
	record.ReconciliationAt = &now
	if result.Terminal {
		terminalAt := result.TerminalAt
		if terminalAt == nil {
			terminalAt = &now
		}
		record.TerminalAt = terminalAt
	}
	return g.executions.UpdateExternalExecution(ctx, record)
}

func (g *sub2Gateway) markPendingExecutionUnknown(ctx context.Context, record *domainsub2.ExternalExecution, code string) error {
	if record == nil || domainsub2.IsTerminalExecutionState(record.State) {
		return nil
	}
	now := time.Now()
	record.State = domainsub2.ExecutionStateUnknown
	record.ErrorCode = strings.TrimSpace(code)
	record.UnknownAt = &now
	record.ReconciliationAt = &now
	record.LastQueriedAt = &now
	return g.executions.UpdateExternalExecution(ctx, record)
}

func truncateExecutionMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 255 {
		return value[:255]
	}
	return value
}

// reconcileReceipts queries existing executions and projects known authority
// results. An UNKNOWN service settlement is isolated rather than replayed: no
// model or financial command is blindly retried without an authority query.
func (g *sub2Gateway) reconcileReceipts(ctx context.Context, userID uint) error {
	identity, err := g.auth.ResolveSub2Identity(ctx, userID)
	if err != nil {
		return err
	}
	var receipts []model.UsageLedger
	if err = g.db.WithContext(ctx).Where("user_id=? AND pricing_snapshot_json LIKE ?", userID, `%"authority_state":"PENDING"%`).Order("id DESC").Limit(25).Find(&receipts).Error; err != nil {
		return err
	}
	for _, receipt := range receipts {
		var snapshot map[string]json.RawMessage
		if json.Unmarshal([]byte(receipt.PricingSnapshotJSON), &snapshot) != nil {
			continue
		}
		var tools []appbilling.MCPToolUsageInput
		_ = json.Unmarshal(snapshot["mcp_tool_usage"], &tools)
		serviceRequest, requestErr := buildSub2ServiceSettlementRequest(g.cfg.Sub2Application, receipt.RefNo, tools)
		if requestErr == nil {
			serviceRequest.Identity = identity
			var pricingVersion int64
			_ = json.Unmarshal(snapshot["pricing_version"], &pricingVersion)
			serviceRequest.PricingVersion = pricingVersion
		}

		var oldRefs []map[string]any
		_ = json.Unmarshal(snapshot["executions"], &oldRefs)
		executionIDs := make([]string, 0, len(oldRefs))
		for _, ref := range oldRefs {
			if executionID, ok := ref["execution_id"].(string); ok && strings.TrimSpace(executionID) != "" {
				executionIDs = append(executionIDs, executionID)
			}
		}
		projection := projectSub2ExecutionUsage(g.querySub2ExecutionResults(ctx, identity, g.cfg.Sub2Application, executionIDs))
		if requestErr != nil {
			projection.State = sub2AuthoritativeUsageQuarantined
			projection.Complete = false
			projection.ParentSettled = false
		}

		serviceState := sub2port.ServiceSettlementStateSettled
		var serviceResult *sub2port.ServiceSettlementResponse
		if requestErr == nil && len(serviceRequest.Items) > 0 {
			storedKey := ""
			storedFingerprint := ""
			_ = json.Unmarshal(snapshot["service_request_key"], &storedKey)
			_ = json.Unmarshal(snapshot["service_payload_fingerprint"], &storedFingerprint)
			if (storedKey != "" && storedKey != serviceRequest.RequestKey) || (storedFingerprint != "" && storedFingerprint != serviceRequest.PayloadFingerprint) {
				serviceState = sub2port.ServiceSettlementStateConflict
			} else {
				serviceState, serviceResult = g.reconcileSub2ServiceSettlement(ctx, identity, serviceRequest, projection.ParentSettled)
				if serviceState == sub2port.ServiceSettlementStateSettled {
					// Re-query after service settlement. The service response is
					// amount-only metadata; the execution query is the full usage
					// authority and includes the committed service record.
					projection = projectSub2ExecutionUsage(g.querySub2ExecutionResults(ctx, identity, g.cfg.Sub2Application, executionIDs))
				}
			}
		}

		pending := requestErr != nil || !projection.ParentSettled || !projection.Complete || serviceState != sub2port.ServiceSettlementStateSettled
		status := "PENDING"
		if !pending {
			status = sub2port.ServiceSettlementStateSettled
		}
		snapshot["authority_state"], _ = json.Marshal(status)
		snapshot["authoritative_usage_state"], _ = json.Marshal(projection.State)
		snapshot["authoritative_usage_complete"], _ = json.Marshal(projection.Complete)
		snapshot["authoritative_usage"], _ = json.Marshal(projection.authoritativeSnapshot())
		snapshot["usage_outbox"], _ = json.Marshal(projection.Outboxes)
		snapshot["service_settlement_state"], _ = json.Marshal(serviceState)
		snapshot["service_request_key"], _ = json.Marshal(serviceRequest.RequestKey)
		snapshot["service_payload_fingerprint"], _ = json.Marshal(serviceRequest.PayloadFingerprint)
		if serviceResult != nil {
			snapshot["service_settlement"], _ = json.Marshal(serviceResult)
		}
		snapshot["executions"], _ = json.Marshal(projection.Refs)
		raw, _ := json.Marshal(snapshot)
		updates := map[string]any{"pricing_snapshot_json": string(raw)}
		if !pending {
			for key, value := range sub2UsageProjectionUpdates(projection) {
				updates[key] = value
			}
		}
		// Projection-only update: no local billing account or balance mutation.
		if err = g.db.WithContext(ctx).Model(&model.UsageLedger{}).Where("id=? AND user_id=?", receipt.ID, userID).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}
