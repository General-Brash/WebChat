package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	sub2port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
	sub2http "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/sub2"
)

func (g *sub2Gateway) Action(ctx context.Context, userID uint, operation string, req sub2http.ActionRequest) (any, error) {
	if g == nil {
		if operation == "status" {
			return map[string]any{"enabled": false}, nil
		}
		return nil, errors.New("Sub2 authority is disabled")
	}
	if operation == "status" {
		var state model.Sub2ApplicationPublication
		_ = g.db.WithContext(ctx).Where("application=?", g.cfg.Sub2Application).First(&state).Error
		identity, identityErr := g.auth.ResolveSub2Identity(ctx, userID)
		readyErr := identityErr
		if identityErr == nil {
			_, readyErr = g.publicationForIdentity(ctx, identity)
		}
		return map[string]any{"enabled": true, "paidReady": readyErr == nil, "issuer": g.cfg.Sub2OIDCIssuer, "groupRevision": state.GroupRevision, "pricingVersion": state.PricingVersion, "playerVersion": state.PlayerVersion}, nil
	}
	identity, err := g.auth.ResolveSub2Identity(ctx, userID)
	if err != nil {
		return nil, err
	}
	switch operation {
	case "wallet":
		if err := g.reconcileReceipts(ctx, userID); err != nil {
			return nil, err
		}
		return g.client.GetWallet(ctx, sub2port.WalletQueryRequest{Identity: identity, Application: g.cfg.Sub2Application})
	case "execution":
		return g.client.QueryExecution(ctx, sub2port.ExecutionQueryRequest{Identity: identity, Application: g.cfg.Sub2Application, ExecutionID: req.ExecutionID})
	case "services-query":
		return g.client.QueryServiceSettlement(ctx, sub2port.ServiceSettlementQueryRequest{Identity: identity, Application: g.cfg.Sub2Application, RunID: req.RunID, RequestKey: req.RequestKey, PayloadFingerprint: req.PayloadFingerprint})
	case "cancel":
		return g.client.CancelExecution(ctx, sub2port.ExecutionCancelRequest{Identity: identity, Application: g.cfg.Sub2Application, ExecutionID: req.ExecutionID, IdempotencyKey: req.IdempotencyKey})
	case "groups":
		var result json.RawMessage
		err := g.client.CallApplication(ctx, identity, "GET", "group-catalog", nil, &result)
		return result, err
	case "publish-groups":
		return g.publish(ctx, userID, true)
	case "publish":
		return g.publish(ctx, userID, false)
	case "revoke-policy":
		return g.revokePolicy(ctx, userID)
	case "catalog":
		up, err := g.channels.GetUpstreamByID(ctx, uint(req.ID))
		if err != nil || up.Kind != "sub2" {
			return nil, errSub2PublicationRequired
		}
		groups := []int64{}
		if err = json.Unmarshal([]byte(up.Sub2GroupIDsJSON), &groups); err != nil {
			return nil, err
		}
		return g.client.DiscoverModels(ctx, identity, groups)
	}
	if strings.HasPrefix(operation, "finance-") {
		op := strings.TrimPrefix(operation, "finance-")
		if req.TargetUserID > 0 {
			if !strings.HasPrefix(op, "admin-") {
				return nil, errors.New("target user is only allowed for an administrator operation")
			}
			var binding model.Sub2IdentityBinding
			if err := g.db.WithContext(ctx).Where("user_id=?", req.TargetUserID).First(&binding).Error; err != nil {
				return nil, err
			}
			id, err := strconv.ParseInt(binding.ExternalUserID, 10, 64)
			if err != nil || id <= 0 {
				return nil, errors.New("invalid external user binding")
			}
			req.ID = id
			var command map[string]any
			if len(req.Command) > 0 {
				if err = json.Unmarshal(req.Command, &command); err != nil {
					return nil, err
				}
			} else {
				command = map[string]any{}
			}
			command["user_id"] = id
			req.Command, _ = json.Marshal(command)
		}
		result, err := g.client.CallFinance(ctx, op, sub2port.FinanceRequest{Identity: identity, ID: req.ID, Query: req.Query, Command: req.Command, IdempotencyKey: req.IdempotencyKey})
		if err != nil {
			return nil, err
		}
		var wrapper struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(result, &wrapper) == nil && len(wrapper.Data) > 0 {
			return wrapper.Data, nil
		}
		return result, nil
	}
	return nil, errors.New("unsupported Sub2 action")
}
