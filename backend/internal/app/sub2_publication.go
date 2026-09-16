package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	sub2port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

var errSub2CacheWriteTTLPriceRequired = errors.New("Sub2 publication requires explicit independent 5m and 1h cache-write prices; an aggregate cache-write price cannot be derived")

// publishedCacheWriteRates deliberately refuses to derive Anthropic cache TTL
// prices from Chat's legacy aggregate field. The current Chat pricing source
// does not carry independent 5m/1h inputs, so a non-zero aggregate is a
// publication-time configuration error rather than an invitation to guess a
// price ratio.
func publishedCacheWriteRates(modelName string, aggregate int64) (int64, int64, error) {
	if aggregate < 0 {
		return 0, 0, fmt.Errorf("%w: model %q has a negative aggregate cache-write price", errSub2CacheWriteTTLPriceRequired, modelName)
	}
	if aggregate > 0 {
		return 0, 0, fmt.Errorf("%w: model %q only exposes the aggregate cache-write price", errSub2CacheWriteTTLPriceRequired, modelName)
	}
	return 0, 0, nil
}

func (g *sub2Gateway) pricingSnapshot(ctx context.Context, playerVersion int64) (map[string]any, string, error) {
	prices, _, err := g.prices.ListModelPricing(ctx, "", 0, 100000)
	if err != nil {
		return nil, "", err
	}
	sort.Slice(prices, func(i, j int) bool { return prices[i].PlatformModelName < prices[j].PlatformModelName })
	models := make([]map[string]any, 0, len(prices))
	for _, price := range prices {
		if price.IsFree {
			price.InputNanousdPerMTokens = 0
			price.OutputNanousdPerMTokens = 0
			price.CacheReadNanousdPerMTokens = 0
			price.CacheWriteNanousdPerMTokens = 0
			price.CallNanousdPerCall = 0
			price.DurationNanousdPerSecond = 0
			price.PricingMode = "token"
			price.TieredPricingJSON = "{}"
		}
		cacheWrite5m, cacheWrite1h, err := publishedCacheWriteRates(price.PlatformModelName, price.CacheWriteNanousdPerMTokens)
		if err != nil {
			return nil, "", err
		}
		models = append(models, map[string]any{
			"model":                               price.PlatformModelName,
			"currency":                            "USD",
			"pricing_mode":                        price.PricingMode,
			"input_nanousd_per_m_tokens":          price.InputNanousdPerMTokens,
			"cache_read_nanousd_per_m_tokens":     price.CacheReadNanousdPerMTokens,
			"cache_write_nanousd_per_m_tokens":    price.CacheWriteNanousdPerMTokens,
			"cache_write_5m_nanousd_per_m_tokens": cacheWrite5m,
			"cache_write_1h_nanousd_per_m_tokens": cacheWrite1h,
			"output_nanousd_per_m_tokens":         price.OutputNanousdPerMTokens,
			"call_nanousd_per_call":               price.CallNanousdPerCall,
			"duration_nanousd_per_second":         price.DurationNanousdPerSecond,
			"tiered_pricing_json":                 price.TieredPricingJSON,
		})
	}
	groups, err := g.channels.ListPermissionGroups(ctx)
	if err != nil {
		return nil, "", err
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	players := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		if group.RateMultiplierPercent <= 0 {
			group.RateMultiplierPercent = 100
		}
		players = append(players, map[string]any{"player_group_id": group.ID, "multiplier_bps": group.RateMultiplierPercent * 100})
	}
	enabled, err := g.prices.GetNativeToolBillingEnabled(ctx)
	if err != nil {
		return nil, "", err
	}
	raw, err := g.prices.GetNativeToolPricingJSON(ctx)
	if err != nil {
		return nil, "", err
	}
	toolPrices, err := g.billing.ListNativeToolPricing(ctx, raw)
	if err != nil {
		return nil, "", err
	}
	// Only model-side native tools belong to the retail policy. Independent MCP
	// service charges are settled separately and must not receive player discounts.
	nativeTools := make([]map[string]any, 0, len(toolPrices))
	for _, tool := range toolPrices {
		price := tool.PriceNanousd
		if !enabled || !tool.Billable {
			price = 0
		}
		nativeTools = append(nativeTools, map[string]any{"tool": tool.Provider + ":" + tool.ToolKey, "nanousd_per_call": price})
	}
	sort.Slice(nativeTools, func(i, j int) bool { return nativeTools[i]["tool"].(string) < nativeTools[j]["tool"].(string) })
	snapshot := map[string]any{"models": models, "native_tools": nativeTools, "player_discounts": players}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	snapshot["player_policy_version"] = playerVersion
	return snapshot, hash, nil
}

func stablePublicationKey(scope string, value any) string {
	encoded, _ := json.Marshal(map[string]any{"scope": scope, "value": value})
	digest := sha256.Sum256(encoded)
	return scope + ":" + hex.EncodeToString(digest[:])
}

func orderedUniquePositiveGroupIDs(ids []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, errors.New("invalid Sub2 group")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

// buildGlobalGroupSelections requires the persisted administrator order. The
// variadic shape keeps the reserved readiness caller source-compatible while
// making a missing order fail closed; it must never infer priority from
// upstream traversal or database IDs.
func buildGlobalGroupSelections(upstreams []model.LLMUpstream, configuredOrders ...[]int64) ([]map[string]any, map[uint][]int64, error) {
	if len(configuredOrders) != 1 {
		return nil, nil, errors.New("Sub2 global group priority is not configured; save an explicit ordered group list before publishing")
	}
	configured, err := sub2port.NormalizeGlobalGroupPriority(configuredOrders[0])
	if err != nil {
		return nil, nil, err
	}
	// Keep each upstream allowlist intact. The global order only determines the
	// deduplicated authority publication; it never expands application groups or
	// bypasses Sub2's user-effective permission intersection.
	groupsByUpstream := make(map[uint][]int64, len(upstreams))
	available := make(map[int64]struct{})
	for _, upstream := range upstreams {
		var raw []int64
		if err := json.Unmarshal([]byte(upstream.Sub2GroupIDsJSON), &raw); err != nil {
			return nil, nil, fmt.Errorf("Sub2 upstream %d has invalid group selection: %w", upstream.ID, err)
		}
		ids, err := orderedUniquePositiveGroupIDs(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("Sub2 upstream %d has invalid group selection: %w", upstream.ID, err)
		}
		groupsByUpstream[upstream.ID] = ids
		for _, id := range ids {
			available[id] = struct{}{}
		}
	}
	missing := make([]int64, 0)
	stale := make([]int64, 0)
	configuredSet := make(map[int64]struct{}, len(configured))
	for _, id := range configured {
		configuredSet[id] = struct{}{}
		if _, ok := available[id]; !ok {
			stale = append(stale, id)
		}
	}
	for id := range available {
		if _, ok := configuredSet[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 || len(stale) > 0 {
		sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
		sort.Slice(stale, func(i, j int) bool { return stale[i] < stale[j] })
		return nil, nil, fmt.Errorf("Sub2 global group priority does not match active upstream groups; save exactly one ordered list containing every active group once (missing=%v stale=%v)", missing, stale)
	}
	selections := make([]map[string]any, 0, len(configured))
	for priority, id := range configured {
		selections = append(selections, map[string]any{"group_id": id, "priority": priority})
	}
	return selections, groupsByUpstream, nil
}

func (g *sub2Gateway) loadGlobalGroupPriority(ctx context.Context) ([]int64, error) {
	setting, err := g.channels.GetLLMSetting(ctx, sub2port.GlobalGroupPrioritySettingKey)
	if err != nil {
		return nil, fmt.Errorf("Sub2 global group priority is not configured; save an explicit ordered group list before publishing: %w", err)
	}
	configured, err := sub2port.ParseGlobalGroupPriority(setting.Value)
	if err != nil {
		return nil, fmt.Errorf("Sub2 global group priority is invalid; save an explicit ordered group list before publishing: %w", err)
	}
	return configured, nil
}

type sub2ModelPublicationPayload struct {
	SyncItems    []map[string]any
	Publications []map[string]any
}

type stableModelSyncItem struct {
	Model         string          `json:"model"`
	Protocol      string          `json:"protocol"`
	SourceGroupID int64           `json:"source_group_id"`
	Capabilities  json.RawMessage `json:"capabilities"`
}

// stableModelSyncPayloadHash is the wire-compatible digest for models/sync.
// It deliberately sorts the complete model/protocol/source-group set so the
// same snapshot hashes identically even when upstream enumeration order varies.
func stableModelSyncPayloadHash(items []map[string]any) (string, error) {
	canonical := make([]stableModelSyncItem, 0, len(items))
	for _, item := range items {
		modelName, ok := item["model"].(string)
		if !ok {
			return "", fmt.Errorf("model sync item has invalid model")
		}
		protocol, ok := item["protocol"].(string)
		if !ok {
			return "", fmt.Errorf("model sync item has invalid protocol")
		}
		groupID, ok := item["source_group_id"].(int64)
		if !ok || groupID <= 0 {
			return "", fmt.Errorf("model sync item has invalid source group")
		}
		capabilities := item["capabilities"]
		if capabilities == nil {
			capabilities = map[string]any{}
		}
		rawCapabilities, err := json.Marshal(capabilities)
		if err != nil {
			return "", err
		}
		var decoded any
		if err := json.Unmarshal(rawCapabilities, &decoded); err != nil {
			return "", err
		}
		rawCapabilities, err = json.Marshal(decoded)
		if err != nil {
			return "", err
		}
		canonical = append(canonical, stableModelSyncItem{
			Model: strings.TrimSpace(modelName), Protocol: strings.ToLower(strings.TrimSpace(protocol)),
			SourceGroupID: groupID, Capabilities: rawCapabilities,
		})
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].Model != canonical[j].Model {
			return canonical[i].Model < canonical[j].Model
		}
		if canonical[i].Protocol != canonical[j].Protocol {
			return canonical[i].Protocol < canonical[j].Protocol
		}
		if canonical[i].SourceGroupID != canonical[j].SourceGroupID {
			return canonical[i].SourceGroupID < canonical[j].SourceGroupID
		}
		return string(canonical[i].Capabilities) < string(canonical[j].Capabilities)
	})
	encoded, err := json.Marshal(struct {
		Models []stableModelSyncItem `json:"models"`
	}{Models: canonical})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// buildModelPublicationPayload creates the exact model allowlist payload used
// by models/sync and models/publish. Keeping this in one deterministic helper
// lets readiness compare the local model/config snapshot with the last remote
// publication instead of checking prices alone.
func (g *sub2Gateway) buildModelPublicationPayload(ctx context.Context, strategy map[string]any, groupsByUpstream map[uint][]int64) (*sub2ModelPublicationPayload, error) {
	payload := &sub2ModelPublicationPayload{SyncItems: []map[string]any{}, Publications: []map[string]any{}}
	seen := map[string]bool{}
	appendRoute := func(name, protocol string, route repository.ChannelUpstreamRouteRow) {
		name = strings.TrimSpace(name)
		protocol = strings.TrimSpace(strings.ToLower(protocol))
		if name == "" || protocol == "" {
			return
		}
		if strings.TrimSpace(strings.ToLower(route.Protocol)) != protocol {
			return
		}
		ids := filterPublishedGroupIDs(groupsByUpstream[route.UpstreamID], route.UpstreamModelRawJSON)
		for _, groupID := range ids {
			payload.SyncItems = append(payload.SyncItems, map[string]any{
				"model":           route.UpstreamModelName,
				"protocol":        protocol,
				"source_group_id": groupID,
				"capabilities":    map[string]any{"retail_model": name},
			})
		}
		key := route.UpstreamModelName + "/" + protocol
		if len(ids) > 0 && !seen[key] {
			seen[key] = true
			payload.Publications = append(payload.Publications, map[string]any{"model": route.UpstreamModelName, "protocol": protocol})
		}
	}
	for _, price := range strategy["models"].([]map[string]any) {
		name, _ := price["model"].(string)
		routes, err := g.channels.ListActiveRoutesByModel(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, route := range routes {
			appendRoute(name, route.Protocol, route)
		}
	}
	cfg := g.cfg
	if g.runtime != nil {
		cfg = g.runtime.Snapshot()
	}
	auxiliary := []struct{ name, protocol string }{}
	if strings.TrimSpace(cfg.RAGModel) != "" {
		auxiliary = append(auxiliary, struct{ name, protocol string }{cfg.RAGModel, "embeddings"})
	}
	if strings.TrimSpace(cfg.ExtractMistralOCRModel) != "" {
		auxiliary = append(auxiliary, struct{ name, protocol string }{cfg.ExtractMistralOCRModel, "mistral_ocr"})
	}
	sort.Slice(auxiliary, func(i, j int) bool {
		if auxiliary[i].name == auxiliary[j].name {
			return auxiliary[i].protocol < auxiliary[j].protocol
		}
		return auxiliary[i].name < auxiliary[j].name
	})
	for _, item := range auxiliary {
		routes, err := g.channels.ListActiveRoutesByModel(ctx, item.name)
		if err != nil {
			return nil, err
		}
		for _, route := range routes {
			appendRoute(item.name, item.protocol, route)
		}
	}

	// One raw model/protocol/group may serve multiple explicitly priced Chat
	// aliases. Preserve all mappings rather than overwriting the last alias.
	merged := make([]map[string]any, 0, len(payload.SyncItems))
	indices := map[string]int{}
	for _, item := range payload.SyncItems {
		key := fmt.Sprintf("%s/%s/%v", item["model"], item["protocol"], item["source_group_id"])
		capabilities, _ := item["capabilities"].(map[string]any)
		retail, _ := capabilities["retail_model"].(string)
		if index, ok := indices[key]; ok {
			caps := merged[index]["capabilities"].(map[string]any)
			names := caps["retail_models"].([]string)
			if retail != "" && !containsString(names, retail) {
				caps["retail_models"] = append(names, retail)
			}
			continue
		}
		item["capabilities"] = map[string]any{"retail_models": []string{retail}}
		indices[key] = len(merged)
		merged = append(merged, item)
	}
	for _, item := range merged {
		caps := item["capabilities"].(map[string]any)
		names := caps["retail_models"].([]string)
		sort.Strings(names)
	}
	sort.Slice(merged, func(i, j int) bool {
		leftModel, _ := merged[i]["model"].(string)
		rightModel, _ := merged[j]["model"].(string)
		if leftModel != rightModel {
			return leftModel < rightModel
		}
		leftProtocol, _ := merged[i]["protocol"].(string)
		rightProtocol, _ := merged[j]["protocol"].(string)
		if leftProtocol != rightProtocol {
			return leftProtocol < rightProtocol
		}
		leftGroup, _ := merged[i]["source_group_id"].(int64)
		rightGroup, _ := merged[j]["source_group_id"].(int64)
		return leftGroup < rightGroup
	})
	sort.Slice(payload.Publications, func(i, j int) bool {
		leftModel, _ := payload.Publications[i]["model"].(string)
		rightModel, _ := payload.Publications[j]["model"].(string)
		if leftModel != rightModel {
			return leftModel < rightModel
		}
		leftProtocol, _ := payload.Publications[i]["protocol"].(string)
		rightProtocol, _ := payload.Publications[j]["protocol"].(string)
		return leftProtocol < rightProtocol
	})
	payload.SyncItems = merged
	return payload, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// filterPublishedGroupIDs fails closed when the authority model metadata cannot
// prove its source-group scope. Missing, malformed, or empty metadata must not
// widen an upstream route to every configured group; the caller will reject an
// empty result before publication or execution.
func filterPublishedGroupIDs(configured []int64, rawJSON string) []int64 {
	if len(configured) == 0 {
		return nil
	}
	var metadata struct {
		SourceGroupIDs []int64 `json:"source_group_ids"`
	}
	if strings.TrimSpace(rawJSON) == "" || json.Unmarshal([]byte(rawJSON), &metadata) != nil || len(metadata.SourceGroupIDs) == 0 {
		return nil
	}
	allowed := make(map[int64]struct{}, len(metadata.SourceGroupIDs))
	for _, id := range metadata.SourceGroupIDs {
		if id <= 0 {
			return nil
		}
		allowed[id] = struct{}{}
	}
	result := make([]int64, 0, len(configured))
	seen := make(map[int64]struct{}, len(configured))
	for _, id := range configured {
		if _, ok := allowed[id]; !ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func publicationFingerprint(pricingHash string, groupRevision, pricingVersion, playerVersion, syncRevision int64, selections []map[string]any, payload *sub2ModelPublicationPayload) string {
	syncPayloadHash, _ := stableModelSyncPayloadHash(payload.SyncItems)
	encoded, _ := json.Marshal(map[string]any{
		"pricing_hash":      pricingHash,
		"group_revision":    groupRevision,
		"pricing_version":   pricingVersion,
		"player_version":    playerVersion,
		"sync_revision":     syncRevision,
		"selections":        selections,
		"sync_payload_hash": syncPayloadHash,
		"publications":      payload.Publications,
	})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
func (g *sub2Gateway) publish(ctx context.Context, userID uint, groupsOnly bool) (result map[string]any, err error) {
	g.publishMu.Lock()
	defer g.publishMu.Unlock()

	remoteMutated := false
	defer func() {
		if err != nil && remoteMutated {
			// A remote phase may have committed before its response was lost. Keep
			// the local paid gate closed, but rely on the content-bound remote
			// idempotency records for a later publish call to resume the phase; this
			// is not a destructive rollback of the authority state.
			_ = g.invalidatePublication(ctx)
		}
	}()

	identity, err := g.auth.ResolveSub2Identity(ctx, userID)
	if err != nil {
		return nil, err
	}
	current, err := g.client.GetApplicationState(ctx, identity)
	if err != nil {
		return nil, err
	}
	var upstreams []model.LLMUpstream
	if err = g.db.WithContext(ctx).Where("kind=? AND status=?", "sub2", "active").Order("id").Find(&upstreams).Error; err != nil {
		return nil, err
	}
	configuredOrder, err := g.loadGlobalGroupPriority(ctx)
	if err != nil {
		return nil, err
	}
	selections, groupsByUpstream, err := buildGlobalGroupSelections(upstreams, configuredOrder)
	if err != nil {
		return nil, err
	}
	var strategy map[string]any
	var pricingHash string
	var playerVersion int64
	if !groupsOnly {
		playerVersion = current.PricingVersion + 1
		strategy, pricingHash, err = g.pricingSnapshot(ctx, playerVersion)
		if err != nil {
			return nil, err
		}
		if len(strategy["models"].([]map[string]any)) == 0 {
			return nil, errSub2PublicationRequired
		}
	}
	// The idempotency key binds the requested content, not the mutable
	// expected revision. If the remote publish committed before its response was
	// lost, a retry must replay that same revision after reading a newer head.
	groupPublishKey := stablePublicationKey("group-policy.publish", map[string]any{
		"application": g.cfg.Sub2Application,
		"groups":      selections,
	})
	var groupResult struct {
		Revision int64 `json:"revision"`
	}
	remoteMutated = true
	if err = g.client.CallApplication(ctx, identity, "POST", "group-policy/publish", map[string]any{
		"expected_revision": current.GroupRevision,
		"idempotency_key":   groupPublishKey,
		"groups":            selections,
	}, &groupResult); err != nil {
		return nil, err
	}
	remoteMutated = true
	if err = g.client.CallApplication(ctx, identity, "POST", "group-policy/confirm", map[string]any{
		"revision":          groupResult.Revision,
		"expected_revision": groupResult.Revision,
		"idempotency_key":   stablePublicationKey("group-policy.confirm", map[string]any{"application": g.cfg.Sub2Application, "revision": groupResult.Revision}),
	}, nil); err != nil {
		return nil, err
	}
	if groupsOnly {
		// A group-only publication intentionally invalidates the local paid
		// reference. It must not keep executing with the previous group revision.
		if err = g.invalidatePublication(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"group_revision": groupResult.Revision, "paid_ready": false}, nil
	}

	remoteMutated = true
	var pricingResult struct {
		Version int64 `json:"version"`
	}
	if err = g.client.CallApplication(ctx, identity, "POST", "retail-policy/publish", map[string]any{
		"expected_version": current.PricingVersion,
		"idempotency_key": stablePublicationKey("retail-policy.publish", map[string]any{
			"application":    g.cfg.Sub2Application,
			"player_version": playerVersion,
			"pricing_hash":   pricingHash,
		}),
		"currency":  "USD",
		"unit":      "nanousd",
		"precision": 9,
		"strategy":  strategy,
	}, &pricingResult); err != nil {
		return nil, err
	}
	remoteMutated = true
	if err = g.client.CallApplication(ctx, identity, "POST", "retail-policy/confirm", map[string]any{
		"version":          pricingResult.Version,
		"expected_version": pricingResult.Version,
		"idempotency_key":  stablePublicationKey("retail-policy.confirm", map[string]any{"application": g.cfg.Sub2Application, "version": pricingResult.Version}),
	}, nil); err != nil {
		return nil, err
	}

	payload, err := g.buildModelPublicationPayload(ctx, strategy, groupsByUpstream)
	if err != nil {
		return nil, err
	}
	if len(payload.Publications) == 0 {
		return nil, errSub2PublicationRequired
	}
	syncPayloadHash, err := stableModelSyncPayloadHash(payload.SyncItems)
	if err != nil {
		return nil, err
	}
	syncRevision := current.SyncRevision + 1
	// The remote models/sync contract is a transactionally protected snapshot.
	// Include its canonical hash so the authority can reject same-revision drift.
	syncIdempotencyKey := stablePublicationKey("models.sync", map[string]any{
		"application":  g.cfg.Sub2Application,
		"payload_hash": syncPayloadHash,
	})
	remoteMutated = true
	if err = g.client.CallApplication(ctx, identity, "POST", "models/sync", map[string]any{"sync_revision": syncRevision, "payload_hash": syncPayloadHash, "idempotency_key": syncIdempotencyKey, "models": payload.SyncItems}, nil); err != nil {
		return nil, err
	}
	postSync, err := g.client.GetApplicationState(ctx, identity)
	if err != nil {
		return nil, err
	}
	if postSync.SyncRevision != syncRevision {
		return nil, fmt.Errorf("Sub2 model sync revision changed during publication")
	}
	modelPublishKey := stablePublicationKey("models.publish", map[string]any{
		"application":       g.cfg.Sub2Application,
		"sync_payload_hash": syncPayloadHash,
		"models":             payload.Publications,
	})
	remoteMutated = true
	if err = g.client.CallApplication(ctx, identity, "POST", "models/publish", map[string]any{"replace": true, "expected_sync_revision": syncRevision, "idempotency_key": modelPublishKey, "models": payload.Publications}, nil); err != nil {
		return nil, err
	}
	// Commit local references only after ALL remote confirmations. A partial
	// publish fails closed because the old local versions no longer match.
	publicationHash := publicationFingerprint(pricingHash, groupResult.Revision, pricingResult.Version, playerVersion, syncRevision, selections, payload)
	state := model.Sub2ApplicationPublication{Application: g.cfg.Sub2Application, GroupRevision: groupResult.Revision, PricingVersion: pricingResult.Version, PlayerVersion: playerVersion, PricingHash: publicationHash, UpdatedAt: time.Now()}
	if err = g.db.WithContext(ctx).Save(&state).Error; err != nil {
		return nil, err
	}
	return map[string]any{"group_revision": state.GroupRevision, "pricing_version": state.PricingVersion, "player_version": state.PlayerVersion, "published_models": len(payload.Publications), "paid_ready": len(payload.Publications) > 0 && len(selections) > 0}, nil
}

func (g *sub2Gateway) invalidatePublication(ctx context.Context) error {
	if g == nil || g.db == nil {
		return nil
	}
	fresh, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return g.db.WithContext(fresh).Where("application = ?", g.cfg.Sub2Application).Delete(&model.Sub2ApplicationPublication{}).Error
}
func (g *sub2Gateway) revokePolicy(ctx context.Context, userID uint) (map[string]any, error) {
	identity, err := g.auth.ResolveSub2Identity(ctx, userID)
	if err != nil {
		return nil, err
	}
	policy, err := g.client.GetApplicationRetailPolicy(ctx, identity)
	if err != nil {
		// Unknown remote state must still stop new paid execution locally.
		_ = g.invalidatePublication(ctx)
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(policy.Status), "confirmed") {
		if err = g.invalidatePublication(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"pricing_version": policy.Version, "status": policy.Status, "paid_ready": false}, nil
	}
	var local model.Sub2ApplicationPublication
	if loadErr := g.db.WithContext(ctx).Where("application=?", g.cfg.Sub2Application).First(&local).Error; loadErr == nil && local.PricingVersion > 0 && local.PricingVersion != policy.Version {
		_ = g.invalidatePublication(ctx)
		return nil, fmt.Errorf("Sub2 retail policy changed before revoke")
	}
	// Invalidate before the remote command. A timeout or lost response cannot
	// leave Chat accepting new paid work while the authority outcome is unknown.
	if err = g.invalidatePublication(ctx); err != nil {
		return nil, err
	}
	if err = g.client.CallApplication(ctx, identity, "POST", "retail-policy/revoke", map[string]any{
		"version":          policy.Version,
		"expected_version": policy.Version,
		"idempotency_key":  stablePublicationKey("retail-policy.revoke", map[string]any{"application": g.cfg.Sub2Application, "version": policy.Version}),
		"reason":           "administrator_explicit_revoke",
	}, nil); err != nil {
		return nil, err
	}
	return map[string]any{"pricing_version": policy.Version, "status": "revoked", "paid_ready": false}, nil
}
