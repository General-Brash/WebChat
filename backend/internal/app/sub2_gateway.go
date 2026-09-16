package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	appauth "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/auth"
	appbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/billing"
	domainsub2 "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/sub2"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	llminfra "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/llm"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	channelrepo "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/postgres/channel"
	executionrepo "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/postgres/sub2"
	sub2infra "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/sub2"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	sub2port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var errSub2PublicationRequired = errors.New("Sub2 pool, model and retail policy must be published before execution")

type sub2Gateway struct {
	runtime    *config.Runtime
	cfg        config.Config
	db         *gorm.DB
	auth       *appauth.Service
	client     *sub2infra.Client
	decoder    *llminfra.Client
	channels   *channelrepo.Repo
	prices     repository.BillingRepository
	billing    *appbilling.Service
	executions *executionrepo.Repo
	publishMu  sync.Mutex
}
type sub2DispatchKey struct{}
type sub2Dispatch struct {
	triggererUserID     uint
	resourceOwnerUserID uint
	purpose             string
	parentExecutionID   string
	conversationID      uint
	trigger             llm.TrustedTriggerContext
	request             sub2port.GatewayRequest
}

func newSub2Gateway(cfg config.Config, db *gorm.DB, auth *appauth.Service, client *sub2infra.Client, decoder *llminfra.Client, channels *channelrepo.Repo, prices repository.BillingRepository, billing *appbilling.Service) *sub2Gateway {
	g := &sub2Gateway{cfg: cfg, db: db, auth: auth, client: client, decoder: decoder, channels: channels, prices: prices, billing: billing, executions: executionrepo.NewRepo(db)}
	decoder.SetRequestExecutor(g.transport)
	client.EnableExecutionDispatcher()
	return g
}
func (g *sub2Gateway) publicationForIdentity(ctx context.Context, identity sub2port.IdentityRef) (*model.Sub2ApplicationPublication, error) {
	var state model.Sub2ApplicationPublication
	if err := g.db.WithContext(ctx).Where("application = ?", g.cfg.Sub2Application).First(&state).Error; err != nil {
		return nil, errSub2PublicationRequired
	}
	if state.GroupRevision <= 0 || state.PricingVersion <= 0 || state.PlayerVersion <= 0 {
		return nil, errSub2PublicationRequired
	}
	strategy, pricingHash, err := g.pricingSnapshot(ctx, state.PlayerVersion)
	if err != nil {
		return nil, errSub2PublicationRequired
	}
	groupPolicy, err := g.client.GetApplicationGroupPolicy(ctx, identity)
	if err != nil || groupPolicy.Revision != state.GroupRevision || !strings.EqualFold(strings.TrimSpace(groupPolicy.Status), "confirmed") {
		return nil, errSub2PublicationRequired
	}
	retailPolicy, err := g.client.GetApplicationRetailPolicy(ctx, identity)
	if err != nil || retailPolicy.Version != state.PricingVersion || !strings.EqualFold(strings.TrimSpace(retailPolicy.Status), "confirmed") || retailPolicy.Strategy.PlayerPolicyVersion != state.PlayerVersion {
		return nil, errSub2PublicationRequired
	}
	if retailPolicy.EffectiveAt != nil && retailPolicy.EffectiveAt.After(time.Now().UTC()) {
		return nil, errSub2PublicationRequired
	}
	var upstreams []model.LLMUpstream
	if err := g.db.WithContext(ctx).Where("kind=? AND status=?", "sub2", "active").Order("id").Find(&upstreams).Error; err != nil {
		return nil, errSub2PublicationRequired
	}
	configuredOrder, err := g.loadGlobalGroupPriority(ctx)
	if err != nil {
		return nil, errSub2PublicationRequired
	}
	selections, groupsByUpstream, err := buildGlobalGroupSelections(upstreams, configuredOrder)
	if err != nil {
		return nil, errSub2PublicationRequired
	}
	payload, err := g.buildModelPublicationPayload(ctx, strategy, groupsByUpstream)
	if err != nil || len(payload.Publications) == 0 || len(selections) == 0 {
		return nil, errSub2PublicationRequired
	}
	remoteState, err := g.client.GetApplicationState(ctx, identity)
	if err != nil || remoteState.SyncRevision <= 0 {
		return nil, errSub2PublicationRequired
	}
	if publicationFingerprint(pricingHash, state.GroupRevision, state.PricingVersion, state.PlayerVersion, remoteState.SyncRevision, selections, payload) != state.PricingHash {
		return nil, errSub2PublicationRequired
	}
	return &state, nil
}
func trustedSub2Subject(ctx context.Context, input llm.GenerateInput) (context.Context, llm.ExecutionSubject, error) {
	if input.TriggerContext != nil {
		var err error
		ctx, err = llm.WithTrustedTriggerContext(ctx, *input.TriggerContext)
		if err != nil {
			return ctx, llm.ExecutionSubject{}, err
		}
	}
	subject := llm.ExecutionSubjectFromContext(ctx)
	if !subject.HasTriggerer() {
		return ctx, subject, llm.ErrTrustedTriggerRequired
	}
	requestedRunID := strings.TrimSpace(input.RunID)
	if subject.RunID != "" && requestedRunID != "" && subject.RunID != requestedRunID {
		return ctx, subject, llm.ErrTrustedTriggerMismatch
	}
	requestedExecutionID := strings.TrimSpace(input.ExecutionID)
	if subject.ExecutionID != "" && requestedExecutionID != "" && subject.ExecutionID != requestedExecutionID {
		return ctx, subject, llm.ErrTrustedTriggerMismatch
	}
	if subject.RunID == "" && requestedRunID != "" {
		ctx = llm.WithExecutionSubject(ctx, subject.UserID, requestedRunID)
		subject = llm.ExecutionSubjectFromContext(ctx)
	}
	return ctx, subject, nil
}

func (g *sub2Gateway) prepare(ctx context.Context, route llm.RouteConfig, input llm.GenerateInput) (context.Context, llm.RouteConfig, *sub2Dispatch, error) {
	// TriggerContext is server-owned metadata. UserID and route.UserID remain
	// resource/access fields and are never allowed to choose the payer.
	var err error
	ctx, subject, err := trustedSub2Subject(ctx, input)
	if err != nil {
		return ctx, route, nil, err
	}
	resourceOwnerUserID := subject.ResourceOwnerUserID
	identity, err := g.auth.ResolveSub2Identity(ctx, subject.TriggererUserID)
	if err != nil {
		return ctx, route, nil, err
	}
	state, err := g.publicationForIdentity(ctx, identity)
	if err != nil {
		return ctx, route, nil, err
	}
	if route.UpstreamID == 0 {
		name := route.RetailModel
		if name == "" {
			name = route.UpstreamModel
		}
		candidates, err := g.channels.ListActiveRoutesByModel(ctx, name)
		if err != nil {
			return ctx, route, nil, err
		}
		for _, candidate := range candidates {
			up, err := g.channels.GetUpstreamByID(ctx, candidate.UpstreamID)
			if err == nil && up.Kind == "sub2" {
				route.UpstreamID = up.ID
				route.RetailModel = name
				route.UpstreamModel = candidate.UpstreamModelName
				route.UpstreamModelRawJSON = candidate.UpstreamModelRawJSON
				route.Protocol = candidate.Protocol
				break
			}
		}
	}
	upstream, err := g.channels.GetUpstreamByID(ctx, route.UpstreamID)
	if err != nil || upstream.Kind != "sub2" || upstream.Status != "active" {
		return ctx, route, nil, errSub2PublicationRequired
	}
	var rawGroups []int64
	if json.Unmarshal([]byte(upstream.Sub2GroupIDsJSON), &rawGroups) != nil {
		return ctx, route, nil, errSub2PublicationRequired
	}
	groups, groupErr := orderedUniquePositiveGroupIDs(rawGroups)
	if groupErr != nil || len(groups) == 0 {
		return ctx, route, nil, errSub2PublicationRequired
	}
	groups = filterPublishedGroupIDs(groups, route.UpstreamModelRawJSON)
	if len(groups) == 0 {
		return ctx, route, nil, errSub2PublicationRequired
	}
	if err := g.validateRouteAgainstCatalog(ctx, identity, route.UpstreamModel, route.Protocol); err != nil {
		return ctx, route, nil, err
	}
	platformModel, err := g.channels.GetActiveModelByName(ctx, route.RetailModel)
	if err != nil {
		return ctx, route, nil, err
	}
	// Sub2 policy groups are resolved for the trusted triggerer. Resource
	// ownership/access checks remain in the producer/application layer and are
	// not granted by choosing this payer identity.
	userGroups, err := g.channels.ListUserGroupIDs(ctx, subject.TriggererUserID)
	if err != nil {
		return ctx, route, nil, err
	}
	defaults, err := g.channels.ListDefaultGroupIDs(ctx)
	if err != nil {
		return ctx, route, nil, err
	}
	userGroups = append(userGroups, defaults...)
	modelGroups, err := g.channels.ListModelGroupIDs(ctx, platformModel.ID)
	if err != nil {
		return ctx, route, nil, err
	}
	allowed := map[uint]bool{}
	for _, id := range modelGroups {
		allowed[id] = true
	}
	players := []uint{}
	seen := map[uint]bool{}
	for _, id := range userGroups {
		if allowed[id] && !seen[id] {
			players = append(players, id)
			seen[id] = true
		}
	}
	executionID := strings.TrimSpace(input.ExecutionID)
	if executionID == "" {
		executionID = strings.TrimSpace(subject.ExecutionID)
	}
	if executionID == "" {
		executionID = uuid.NewString()
	}
	trigger := subject.TrustedTriggerContext
	trigger.ResourceOwnerUserID = resourceOwnerUserID
	trigger.RunID = subject.RunID
	trigger.ExecutionID = executionID
	ctx, err = llm.WithTrustedTriggerContext(ctx, trigger)
	if err != nil {
		return ctx, route, nil, err
	}
	dispatch := &sub2Dispatch{
		triggererUserID:     trigger.TriggererUserID,
		resourceOwnerUserID: resourceOwnerUserID,
		purpose:             strings.TrimSpace(trigger.Purpose),
		parentExecutionID:   strings.TrimSpace(trigger.ParentExecutionID),
		conversationID:      input.ConversationID,
		trigger:             trigger,
		request:             sub2port.GatewayRequest{Identity: identity, Application: g.cfg.Sub2Application, ExecutionID: executionID, RunID: trigger.RunID, Model: route.UpstreamModel, RetailModel: route.RetailModel, Protocol: route.Protocol, GroupIDs: groups, GroupRevision: state.GroupRevision, PricingVersion: state.PricingVersion, PlayerVersion: state.PlayerVersion, PlayerGroupIDs: players},
	}
	route.BaseURL = g.cfg.Sub2BaseURL
	route.APIKey = "sub2-subject-only"
	route.HeadersJSON = "{}"
	return context.WithValue(ctx, sub2DispatchKey{}, dispatch), route, dispatch, nil
}
func (g *sub2Gateway) Generate(ctx context.Context, route llm.RouteConfig, input llm.GenerateInput) (*llm.GenerateOutput, error) {
	ctx, route, d, err := g.prepare(ctx, route, input)
	if err != nil {
		return nil, err
	}
	output, err := g.decoder.Generate(ctx, route, input)
	g.finish(ctx, d, output, err)
	return output, err
}
func (g *sub2Gateway) GenerateStream(ctx context.Context, route llm.RouteConfig, input llm.GenerateInput, onEvent func(llm.GenerateStreamEvent) error) (*llm.GenerateOutput, error) {
	ctx, route, d, err := g.prepare(ctx, route, input)
	if err != nil {
		return nil, err
	}
	output, err := g.decoder.GenerateStream(ctx, route, input, onEvent)
	g.finish(ctx, d, output, err)
	return output, err
}
func (g *sub2Gateway) responseOperation(ctx context.Context, route llm.RouteConfig, responseID string, cancel bool) (*llm.GenerateOutput, error) {
	subject := llm.ExecutionSubjectFromContext(ctx)
	if !subject.HasTriggerer() {
		return nil, llm.ErrTrustedTriggerRequired
	}
	record, err := g.executions.GetExternalExecutionByProviderResponseID(ctx, subject.TriggererUserID, g.cfg.Sub2Application, responseID)
	if err != nil {
		return nil, err
	}
	if record.TriggererUserID > 0 && record.TriggererUserID != subject.TriggererUserID {
		return nil, llm.ErrTrustedTriggerRequired
	}
	legacyReadOnly := record.TriggererUserID == 0
	var input llm.GenerateInput
	if legacyReadOnly {
		// A legacy row can be queried only in its original authenticated scope.
		// It cannot authorize cancel/replay, and this branch never persists an
		// inferred triggerer or constructs a new paid POST body.
		if cancel || !domainsub2.IsTerminalExecutionState(record.State) {
			return nil, llm.ErrTrustedTriggerRequired
		}
		input = llm.GenerateInput{ExecutionID: record.ExecutionID}
	} else {
		storedTrigger := llm.TrustedTriggerContext{
			TriggererUserID:     record.TriggererUserID,
			ResourceOwnerUserID: record.ResourceOwnerUserID,
			Purpose:             record.Purpose,
			RunID:               record.ChatRunID,
			ExecutionID:         record.ExecutionID,
			ParentExecutionID:   record.ParentExecutionID,
		}
		input = llm.GenerateInput{RunID: record.ChatRunID, ExecutionID: record.ExecutionID, TriggerContext: &storedTrigger}
	}
	ctx, route, d, err := g.prepare(ctx, route, input)
	if err != nil {
		return nil, err
	}
	if cancel {
		_, err = g.client.CancelExecution(ctx, sub2port.ExecutionCancelRequest{Identity: d.request.Identity, Application: g.cfg.Sub2Application, ExecutionID: d.request.ExecutionID, IdempotencyKey: "cancel:" + d.request.ExecutionID})
		if err != nil {
			return nil, err
		}
		return &llm.GenerateOutput{ResponseID: responseID}, nil
	}
	return g.decoder.RetrieveOpenAIResponse(ctx, route, responseID)
}
func (g *sub2Gateway) RetrieveOpenAIResponse(ctx context.Context, route llm.RouteConfig, id string) (*llm.GenerateOutput, error) {
	return g.responseOperation(ctx, route, id, false)
}
func (g *sub2Gateway) CancelOpenAIResponse(ctx context.Context, route llm.RouteConfig, id string) (*llm.GenerateOutput, error) {
	return g.responseOperation(ctx, route, id, true)
}
func (g *sub2Gateway) ListModels(ctx context.Context, route llm.RouteConfig) ([]llm.ModelItem, error) {
	subject := llm.ExecutionSubjectFromContext(ctx)
	if !subject.HasTriggerer() {
		return nil, llm.ErrTrustedTriggerRequired
	}
	identity, err := g.auth.ResolveSub2Identity(ctx, subject.TriggererUserID)
	if err != nil {
		return nil, err
	}
	up, err := g.channels.GetUpstreamByID(ctx, route.UpstreamID)
	if err != nil || up.Kind != "sub2" {
		return nil, errSub2PublicationRequired
	}
	groups := []int64{}
	_ = json.Unmarshal([]byte(up.Sub2GroupIDsJSON), &groups)
	if len(groups) == 0 {
		return nil, errSub2PublicationRequired
	}
	raw, err := g.client.DiscoverModels(ctx, identity, groups)
	if err != nil {
		return nil, err
	}
	return decodeSub2ModelItems(raw)
}

// validateRouteAgainstCatalog performs the subject-scoped final allowlist
// check immediately before dispatch. The local catalog/publication hash
// prevents Chat-side drift; Sub2 remains authoritative for the current
// subject, enabled provider model and protocol.
func (g *sub2Gateway) validateRouteAgainstCatalog(ctx context.Context, identity sub2port.IdentityRef, providerModel, protocol string) error {
	catalog, err := g.client.ListModels(ctx, sub2port.ModelCatalogRequest{Identity: identity, Application: g.cfg.Sub2Application})
	if err != nil {
		return errSub2PublicationRequired
	}
	providerModel = strings.TrimSpace(providerModel)
	protocol = strings.TrimSpace(strings.ToLower(protocol))
	for _, item := range catalog.Models {
		if !item.Enabled || strings.TrimSpace(item.Model) != providerModel {
			continue
		}
		for _, candidate := range item.Protocols {
			if strings.TrimSpace(strings.ToLower(candidate)) == protocol {
				return nil
			}
		}
	}
	return errSub2PublicationRequired
}

// decodeSub2ModelItems preserves the authority catalog's protocol and source
// group metadata. A model ID alone is not sufficient to select a safe route:
// the same ID may be exposed by more than one protocol or source group.
func decodeSub2ModelItems(raw []byte) ([]llm.ModelItem, error) {
	type item struct {
		Model          string   `json:"model"`
		ID             string   `json:"id"`
		DisplayName    string   `json:"display_name"`
		OwnedBy        string   `json:"owned_by"`
		Protocols      []string `json:"protocols"`
		SourceGroupIDs []int64  `json:"source_group_ids"`
		Enabled        *bool    `json:"enabled"`
	}
	var payload struct {
		Models []item `json:"models"`
		Data   []item `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	items := payload.Models
	if len(items) == 0 {
		items = payload.Data
	}
	byID := make(map[string]llm.ModelItem, len(items))
	for _, item := range items {
		modelID := strings.TrimSpace(item.Model)
		if modelID == "" {
			modelID = strings.TrimSpace(item.ID)
		}
		if modelID == "" || (item.Enabled != nil && !*item.Enabled) {
			continue
		}
		current := byID[modelID]
		if current.ID == "" {
			current = llm.ModelItem{ID: modelID, OwnedBy: strings.TrimSpace(item.OwnedBy), DisplayName: strings.TrimSpace(item.DisplayName)}
		}
		if current.OwnedBy == "" {
			current.OwnedBy = strings.TrimSpace(item.OwnedBy)
		}
		if current.DisplayName == "" {
			current.DisplayName = strings.TrimSpace(item.DisplayName)
		}
		current.Protocols = appendUniqueLowerStrings(current.Protocols, item.Protocols...)
		current.SourceGroupIDs = appendUniquePositiveInt64s(current.SourceGroupIDs, item.SourceGroupIDs...)
		byID[modelID] = current
	}
	result := make([]llm.ModelItem, 0, len(byID))
	for _, item := range byID {
		item.Protocols = normalizeSub2ModelMetadataStrings(item.Protocols)
		item.SourceGroupIDs = normalizeSub2ModelMetadataInt64s(item.SourceGroupIDs)
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func normalizeSub2ModelMetadataStrings(values []string) []string {
	result := appendUniqueLowerStrings(nil, values...)
	sort.Strings(result)
	return result
}

func normalizeSub2ModelMetadataInt64s(values []int64) []int64 {
	result := appendUniquePositiveInt64s(nil, values...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func appendUniqueLowerStrings(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		value = strings.TrimSpace(strings.ToLower(value))
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func appendUniquePositiveInt64s(dst []int64, values ...int64) []int64 {
	seen := make(map[int64]struct{}, len(dst)+len(values))
	for _, value := range dst {
		if value > 0 {
			seen[value] = struct{}{}
		}
	}
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}
func sub2ExecutionRequestHash(request sub2port.GatewayRequest) string {
	body := canonicalGatewayBody(request.Body, request.ContentType)
	bodyDigest := sha256.Sum256(body)
	fingerprint := struct {
		Application    string            `json:"application"`
		RunID          string            `json:"run_id"`
		Model          string            `json:"model"`
		RetailModel    string            `json:"retail_model"`
		Protocol       string            `json:"protocol"`
		Method         string            `json:"method"`
		Path           string            `json:"path"`
		Query          string            `json:"query"`
		ContentType    string            `json:"content_type"`
		GroupIDs       []int64           `json:"group_ids"`
		GroupRevision  int64             `json:"group_revision"`
		PricingVersion int64             `json:"pricing_version"`
		PlayerVersion  int64             `json:"player_version"`
		PlayerGroupIDs []uint            `json:"player_group_ids"`
		Headers        map[string]string `json:"headers,omitempty"`
		BodySHA256     string            `json:"body_sha256"`
	}{
		Application:    strings.TrimSpace(request.Application),
		RunID:          strings.TrimSpace(request.RunID),
		Model:          strings.TrimSpace(request.Model),
		RetailModel:    strings.TrimSpace(request.RetailModel),
		Protocol:       strings.ToLower(strings.TrimSpace(request.Protocol)),
		Method:         strings.ToUpper(strings.TrimSpace(request.Method)),
		Path:           canonicalGatewayPath(strings.TrimSpace(request.Path)),
		Query:          request.Query,
		ContentType:    strings.ToLower(strings.TrimSpace(request.ContentType)),
		GroupIDs:       append([]int64(nil), request.GroupIDs...),
		GroupRevision:  request.GroupRevision,
		PricingVersion: request.PricingVersion,
		PlayerVersion:  request.PlayerVersion,
		PlayerGroupIDs: append([]uint(nil), request.PlayerGroupIDs...),
		Headers:        cloneGatewayHeaders(request.Headers),
		BodySHA256:     hex.EncodeToString(bodyDigest[:]),
	}
	raw, _ := json.Marshal(fingerprint)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func canonicalGatewayBody(body []byte, contentType string) []byte {
	if len(body) == 0 || !strings.Contains(strings.ToLower(contentType), "json") {
		return body
	}
	var value any
	if json.Unmarshal(body, &value) != nil {
		return body
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return body
	}
	return canonical
}

func cloneGatewayHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return cloned
}

func sameSub2ExecutionAttribution(previous *domainsub2.ExternalExecution, current *sub2Dispatch, identity sub2port.IdentityRef) bool {
	if previous == nil || current == nil {
		return false
	}
	return previous.TriggererUserID == current.triggererUserID &&
		previous.ResourceOwnerUserID == current.resourceOwnerUserID &&
		strings.TrimSpace(previous.Purpose) == strings.TrimSpace(current.purpose) &&
		strings.TrimSpace(previous.ParentExecutionID) == strings.TrimSpace(current.parentExecutionID) &&
		strings.TrimSpace(previous.ChatRunID) == strings.TrimSpace(current.request.RunID) &&
		strings.TrimSpace(previous.Application) == strings.TrimSpace(current.request.Application) &&
		strings.TrimSpace(previous.Issuer) == strings.TrimSpace(identity.Issuer) &&
		strings.TrimSpace(previous.Subject) == strings.TrimSpace(identity.Subject) &&
		strings.TrimSpace(previous.ExternalUserID) == strings.TrimSpace(identity.ExternalUserID) &&
		previous.IdentityEpoch == identity.IdentityEpoch
}

func isSub2PaidModelSubmission(method, path string) bool {
	return method == http.MethodPost && !strings.HasSuffix(path, "/cancel")
}

func (g *sub2Gateway) transport(route llm.RouteConfig, request *http.Request) (*http.Response, error) {
	d, ok := request.Context().Value(sub2DispatchKey{}).(*sub2Dispatch)
	if !ok || d == nil {
		return nil, sub2port.ErrExecutionDispatchUnavailable
	}
	if d.triggererUserID == 0 {
		return nil, llm.ErrTrustedTriggerRequired
	}
	e := d.request
	identity, err := g.auth.ResolveSub2Identity(request.Context(), d.triggererUserID)
	if err != nil {
		return nil, err
	}
	e.Identity = identity
	if request.Body == nil {
		request.Body = io.NopCloser(bytes.NewReader(nil))
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, (64<<20)+1))
	request.Body.Close()
	if err != nil || len(body) > 64<<20 {
		return nil, errors.New("invalid gateway body")
	}
	e.Body = body
	e.Method = request.Method
	e.Path = canonicalGatewayPath(request.URL.Path)
	e.Query = request.URL.RawQuery
	e.ContentType = request.Header.Get("Content-Type")
	e.Headers = map[string]string{}
	for _, name := range []string{"Accept", "Anthropic-Version", "Anthropic-Beta", "Openai-Beta"} {
		if value := request.Header.Get(name); value != "" {
			e.Headers[name] = value
		}
	}
	if e.Path == "" {
		return nil, errors.New("unsupported Sub2 gateway path")
	}
	hash := sub2ExecutionRequestHash(e)
	// Retrieval GETs and explicit cancel POSTs are control operations. They
	// retain the original protocol method in the authority request but must not
	// create a new Chat execution record or re-submit a model body.
	if isSub2PaidModelSubmission(request.Method, e.Path) {
		now := time.Now()
		record := &domainsub2.ExternalExecution{
			ExecutionID:         e.ExecutionID,
			UserID:              d.triggererUserID,
			TriggererUserID:     d.triggererUserID,
			ResourceOwnerUserID: d.resourceOwnerUserID,
			Purpose:             d.purpose,
			ParentExecutionID:   d.parentExecutionID,
			Issuer:              identity.Issuer,
			Subject:             identity.Subject,
			ExternalUserID:      identity.ExternalUserID,
			IdentityEpoch:       identity.IdentityEpoch,
			Application:         e.Application,
			ChatRunID:           e.RunID,
			ConversationID:      d.conversationID,
			TaskType:            e.Protocol,
			ModelName:           e.RetailModel,
			RequestHash:         hash,
			IdempotencyKey:      e.ExecutionID,
			State:               domainsub2.ExecutionStateDispatched,
			DispatchedAt:        &now,
		}
		if err := g.executions.CreateExternalExecution(request.Context(), record); err != nil {
			previous, loadErr := g.executions.GetExternalExecution(request.Context(), d.triggererUserID, e.ExecutionID)
			if loadErr == nil {
				if previous.RequestHash != hash {
					return nil, llm.MarkRequestAccepted(errors.New("execution id conflict: request fingerprint mismatch"))
				}
				if !sameSub2ExecutionAttribution(previous, d, identity) {
					return nil, llm.MarkRequestAccepted(domainsub2.ErrExecutionAttributionMismatch)
				}
				result, queryErr := g.client.QueryExecution(request.Context(), sub2port.ExecutionQueryRequest{Identity: identity, Application: e.Application, ExecutionID: e.ExecutionID})
				if queryErr == nil && result.Terminal && result.ResponseStatus > 0 && len(result.ResponseBody) > 0 {
					header := make(http.Header)
					header.Set("Content-Type", result.ResponseContentType)
					return &http.Response{StatusCode: result.ResponseStatus, Header: header, Body: io.NopCloser(bytes.NewReader(result.ResponseBody)), Request: request}, nil
				}
			}
			return nil, llm.MarkRequestAccepted(fmt.Errorf("execution exists or cannot be durably recorded; query before retry: %w", err))
		}
	}
	response, err := g.client.DispatchGateway(request.Context(), e)
	if err != nil {
		g.markUnknown(request.Context(), d, "transport_unknown")
		return nil, llm.MarkRequestAccepted(err)
	}
	if code := terminalSub2FailureCode(response); code != "" {
		// A managed key that is exhausted, revoked or expired must not trigger
		// Chat route/key failover. Remove the local paid reference so every new
		// paid dispatch fails closed until an authorized operator restores it and
		// explicitly publishes a new policy.
		_ = g.invalidatePublication(request.Context())
		if response.Body != nil {
			response.Body.Close()
		}
		return nil, llm.MarkRequestAccepted(fmt.Errorf("Sub2 paid execution closed: %s", code))
	}
	if response.StatusCode == http.StatusConflict {
		response.Body.Close()
		g.markUnknown(request.Context(), d, "authority_query_required")
		return nil, llm.MarkRequestAccepted(errors.New("Sub2 execution requires reconciliation; not replayed"))
	}
	return response, nil
}

func terminalSub2FailureCode(response *http.Response) string {
	if response == nil || response.StatusCode < http.StatusBadRequest || response.Body == nil {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	response.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var envelope struct {
		Code      string `json:"code"`
		ErrorCode string `json:"errorCode"`
		Error     struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	values := []string{envelope.Code, envelope.ErrorCode, envelope.Error.Code, envelope.Error.Type, envelope.Error.Message}
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		normalized = strings.NewReplacer("-", "_", " ", "_").Replace(normalized)
		switch normalized {
		case "managed_key_unavailable", "api_key_quota_exhausted", "api_key_expired", "api_key_disabled", "api_key_revoked", "pricing_policy_changed", "retail_policy_revoked", "policy_closed":
			return normalized
		}
	}
	return ""
}
func canonicalGatewayPath(path string) string {
	for _, prefix := range []string{"/v1beta/", "/v1/"} {
		if index := strings.Index(path, prefix); index >= 0 {
			return path[index:]
		}
	}
	for _, prefix := range []string{"/responses", "/chat/completions", "/messages", "/images/", "/videos/", "/embeddings"} {
		if index := strings.Index(path, prefix); index >= 0 {
			return "/v1" + path[index:]
		}
	}
	return ""
}
func (g *sub2Gateway) markUnknown(ctx context.Context, d *sub2Dispatch, code string) {
	fresh, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	now := time.Now()
	_ = g.db.WithContext(fresh).Model(&model.Sub2ExternalExecution{}).Where("user_id=? AND execution_id=? AND state NOT IN ?", d.triggererUserID, d.request.ExecutionID, []string{"SETTLED", "SUCCEEDED", "FAILED", "CANCELED"}).Updates(map[string]any{
		"state":             "UNKNOWN",
		"unknown_at":        now,
		"reconciliation_at": now,
		"last_queried_at":   now,
		"error_code":        code,
	}).Error
}
func (g *sub2Gateway) finish(ctx context.Context, d *sub2Dispatch, output *llm.GenerateOutput, cause error) {
	fresh, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if output != nil && output.ResponseID != "" {
		metadata := map[string]any{
			"provider_response_id": output.ResponseID,
			"request_model":        d.request.Model,
			"billing_model":        d.request.RetailModel,
			"provider_protocol":    d.request.Protocol,
			"group_revision":       d.request.GroupRevision,
			"pricing_version":      d.request.PricingVersion,
			"player_version":       d.request.PlayerVersion,
		}
		if strings.TrimSpace(output.ReturnedModel) != "" {
			metadata["returned_model"] = strings.TrimSpace(output.ReturnedModel)
		}
		raw, _ := json.Marshal(metadata)
		_ = g.db.WithContext(fresh).Model(&model.Sub2ExternalExecution{}).Where("user_id=? AND execution_id=?", d.triggererUserID, d.request.ExecutionID).Update("metadata_json", string(raw)).Error
	}
	identity, err := g.auth.ResolveSub2Identity(fresh, d.triggererUserID)
	if err != nil {
		g.markUnknown(fresh, d, "identity_recheck_required")
		return
	}
	result, err := g.client.QueryExecution(fresh, sub2port.ExecutionQueryRequest{Identity: identity, Application: g.cfg.Sub2Application, ExecutionID: d.request.ExecutionID, ExpectedEpoch: d.request.Identity.IdentityEpoch})
	if err != nil {
		g.markUnknown(fresh, d, "authority_query_pending")
		return
	}
	if err := g.recordExecutionQueryResult(fresh, d.triggererUserID, d.request.ExecutionID, result); err != nil {
		g.markUnknown(fresh, d, "execution_projection_failed")
	}
}

var _ llmExecutionGateway = (*sub2Gateway)(nil)
