package embedding

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/extraction"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/filetrigger"
	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/filetype"
	portembedding "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/embedding"
	authorityllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/apperr"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/background"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/embeddingutil"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/tokenestimate"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

var (
	ErrEmbeddingServiceNotConfigured = apperr.NewMasked("embedding.service_not_configured", "embedding service is not configured", "embedding service not configured")
	ErrEmbeddingServiceUnavailable   = errors.New("embedding service unavailable")
	ErrEmbeddingQueueUnavailable     = errors.New("embedding queue unavailable")
	ErrTooManyTargetedFiles          = errors.New("too many files for targeted embedding")
	ErrReindexInitiatorRequired      = apperr.NewMasked("embedding.reindex_initiator_required", "an authenticated administrator is required to start reindex", "reindex requires an authenticated administrator")
	ErrReindexPersistenceUnavailable = errors.New("embedding reindex persistence unavailable")
	errNoExtractableText             = errors.New("no extractable text in file")
	errEmptyChunks                   = errors.New("embedding produced no chunks")
	errEmbeddingConfigurationChanged = errors.New("embedding configuration changed")
)

const (
	embeddingErrorLimit           = 255
	embeddingFailureMessage       = "向量化失败，请稍后重试。"
	embeddingUnavailableMessage   = "向量化服务暂时不可用，请稍后重试。"
	embeddingNotConfiguredMessage = "向量化服务尚未配置。"
	embeddingTimeoutMessage       = "向量化超时，请稍后重试。"
	embeddingCanceledMessage      = "向量化已取消。"
	embeddingNoTextMessage        = "无法读取文件提取文本。"
	embeddingEmptyChunksMessage   = "文件没有可用于向量化的内容。"
	embeddingConfigurationChanged = "向量化配置已变更，请重新提交任务。"
)

// ErrorSummary returns a bounded, user-visible description without exposing
// provider responses, URLs, credentials, or internal storage details.
func ErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return embeddingCanceledMessage
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return embeddingTimeoutMessage
	}
	if errors.Is(err, ErrEmbeddingServiceNotConfigured) {
		return embeddingNotConfiguredMessage
	}
	if errors.Is(err, ErrEmbeddingServiceUnavailable) {
		return embeddingUnavailableMessage
	}
	if errors.Is(err, errNoExtractableText) {
		return embeddingNoTextMessage
	}
	if errors.Is(err, errEmptyChunks) {
		return embeddingEmptyChunksMessage
	}
	if errors.Is(err, errEmbeddingConfigurationChanged) {
		return embeddingConfigurationChanged
	}
	return embeddingFailureMessage
}

const (
	WorkerConcurrency = 4
	MaxTargetedFiles  = 100
)

const (
	SkipReasonNotFound     = "not_found"
	SkipReasonNotReady     = "not_ready"
	SkipReasonUnsupported  = "unsupported"
	SkipReasonAlreadyReady = "already_ready"
	SkipReasonProcessing   = "processing"
	SkipReasonQueueBusy    = "queue_busy"
	SkipReasonSubmitFailed = "submit_failed"
	ReasonOutdatedIndex    = "outdated_index"
)

type TargetedFileSkip struct {
	FileID string
	Reason string
}

type TargetedSubmissionResult struct {
	SubmittedFileIDs []string
	Skipped          []TargetedFileSkip
}

type TargetedJob struct {
	FileID             string
	UserID             uint
	EmbeddingSignature string
	EmbeddingHost      string
	// TriggerContext is the immutable per-file operation envelope persisted in
	// the Q2 queue before the worker/provider boundary.
	TriggerContext authorityllm.TrustedTriggerContext
}

type TargetedSubmissionPlan struct {
	Jobs    []TargetedJob
	Skipped []TargetedFileSkip
}

type FileVectorizationCapability struct {
	CanVectorize bool
	Reason       string
}

// Service 封装文件 embedding 执行与状态管理能力。
type Service struct {
	cfg         *config.Runtime
	repo        repository.EmbeddingRepository
	extractSvc  *extraction.Service
	embedClient EmbeddingClient
	logger      *zap.Logger
	workSlots   chan struct{}
	reindexRepo  repository.EmbeddingReindexRepository
	reindexQueue repository.FileProcessingQueueRepository
	reindexJobs  chan string
	reindexMu    sync.Mutex
	reindexing   bool
	reindexWorkerID string

	vectorStoreMu        sync.Mutex
	vectorStoreChecked   bool
	vectorStoreAvailable bool
}

// EmbeddingClient 调用外部服务将文本批量转换为向量。
type EmbeddingClient interface {
	CallAPI(ctx context.Context, input portembedding.Request) ([][]float32, error)
}

// NewServiceWithRuntime 创建使用运行时配置容器的 embedding 服务。
func NewServiceWithRuntime(cfg *config.Runtime, repo repository.EmbeddingRepository, extractSvc *extraction.Service, embedClient EmbeddingClient, logger *zap.Logger) *Service {
	return &Service{
		cfg:         cfg,
		repo:        repo,
		extractSvc:  extractSvc,
		embedClient: embedClient,
		logger:      logger,
		workSlots:   make(chan struct{}, WorkerConcurrency),
		reindexJobs: make(chan string, 1),
	}
}

// SetReindexRepository injects the dedicated durable global-reindex store.
func (s *Service) SetReindexRepository(repo repository.EmbeddingReindexRepository) {
	if s != nil {
		s.reindexRepo = repo
	}
}

// SetReindexQueue injects the existing Q2 file-embedding queue. Global work
// must use the same durable queue envelope as targeted file embedding.
func (s *Service) SetReindexQueue(queue repository.FileProcessingQueueRepository) {
	if s != nil {
		s.reindexQueue = queue
	}
}

// StartBackgroundWorkers resumes only already-attributed durable jobs. It does
// not invent an actor from startup, a file owner, or the current browser.
func (s *Service) StartBackgroundWorkers(ctx context.Context) {
	if s == nil || ctx == nil {
		return
	}
	s.reindexWorkerID = uuid.NewString()
	background.Go(s.logger, "embedding_reindex_dispatch", func() {
		if s.reindexRepo != nil {
			if job, err := s.reindexRepo.GetActive(ctx); err == nil && job != nil {
				s.signalReindex(job.JobID)
			} else if err != nil && s.logger != nil {
				s.logger.Warn("embedding_reindex_resume_lookup_failed", zap.Error(err))
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case jobID := <-s.reindexJobs:
				s.runReindex(ctx, jobID)
			}
		}
	})
}

func (s *Service) signalReindex(jobID string) {
	if s == nil || strings.TrimSpace(jobID) == "" {
		return
	}
	select {
	case s.reindexJobs <- strings.TrimSpace(jobID):
	default:
	}
}

// Available 返回当前对话 RAG 检索能力是否可用及原因。
func (s *Service) Available(ctx context.Context) (bool, string) {
	cfg := s.snapshot()
	if !cfg.RAGEnabled {
		return false, "rag_disabled"
	}
	available, reason, _ := s.indexingAvailable(ctx, cfg)
	return available, reason
}

// IndexingAvailable 返回文件向量索引维护能力是否可用及原因。
func (s *Service) IndexingAvailable(ctx context.Context) (bool, string) {
	available, reason, _ := s.indexingAvailable(ctx, s.snapshot())
	return available, reason
}

func (s *Service) indexingAvailable(ctx context.Context, cfg config.Config) (bool, string, error) {
	if !cfg.EmbeddingEnabled {
		return false, "embedding_disabled", nil
	}
	if strings.TrimSpace(cfg.RAGModel) == "" {
		return false, "embedding_model_missing", nil
	}
	if strings.TrimSpace(cfg.EmbeddingHost) == "" {
		return false, "embedding_host_missing", nil
	}
	if s.embedClient == nil {
		return false, "embedding_client_missing", nil
	}
	if s.repo == nil {
		return false, "vector_store_unavailable", nil
	}
	available, err := s.cachedVectorStoreAvailable(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("embedding vector store availability check failed", zap.Error(err))
		}
		return false, "vector_store_error", err
	}
	if !available {
		return false, "vector_store_unavailable", nil
	}
	return true, "available", nil
}

// cachedVectorStoreAvailable 缓存随进程启动确定的向量存储结构状态。
// 配置项仍由 indexingAvailable 每次读取运行时快照，只有昂贵且在运行期间不应变化的
// 扩展、字段和索引结构检查会被缓存；失败结果不会缓存，避免瞬时数据库错误污染后续请求。
func (s *Service) cachedVectorStoreAvailable(ctx context.Context) (bool, error) {
	s.vectorStoreMu.Lock()
	defer s.vectorStoreMu.Unlock()
	if s.vectorStoreChecked {
		return s.vectorStoreAvailable, nil
	}
	available, err := s.repo.VectorStoreAvailable(ctx)
	if err != nil {
		return false, err
	}
	s.vectorStoreAvailable = available
	s.vectorStoreChecked = true
	return available, nil
}

// ShouldTrigger 判断当前文件是否应触发 embedding。
func (s *Service) ShouldTrigger(fileObj domainconversation.FileObject) bool {
	cfg := s.snapshot()
	if !cfg.EmbeddingEnabled || !cfg.EmbedTriggerOnUpload || strings.TrimSpace(cfg.RAGModel) == "" || strings.TrimSpace(cfg.EmbeddingHost) == "" {
		return false
	}
	return canEmbedFile(cfg, fileObj)
}

func canEmbedFile(cfg config.Config, fileObj domainconversation.FileObject) bool {
	if strings.TrimSpace(fileObj.StoragePath) == "" || strings.ToLower(strings.TrimSpace(fileObj.Status)) != "active" {
		return false
	}
	return supportsEmbeddingSource(fileObj, cfg)
}

// MaybeTrigger 在满足条件时异步触发 embedding。
func (s *Service) MaybeTrigger(ctx context.Context, fileObj domainconversation.FileObject) {
	if !s.ShouldTrigger(fileObj) {
		return
	}
	background.Go(s.logger, "embedding_process_file", func() {
		ctx, cancel := background.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if available, _, _ := s.indexingAvailable(ctx, s.snapshot()); !available {
			return
		}
		if err := s.ProcessFile(ctx, fileObj); err != nil && s.logger != nil {
			s.logger.Warn("embedding_failed",
				zap.String("file_id", fileObj.FileID),
				zap.Error(err),
			)
		}
	})
}

// PlanFiles 校验当前用户指定文件并生成向量化任务计划。
// 任务认领与投递由 processing 应用服务逐项完成，避免批量预认领后因中途失败遗留 processing 状态。
func (s *Service) PlanFiles(ctx context.Context, userID uint, fileIDs []string) (TargetedSubmissionPlan, error) {
	plan := TargetedSubmissionPlan{
		Jobs:    []TargetedJob{},
		Skipped: []TargetedFileSkip{},
	}
	normalizedIDs := normalizeTargetedFileIDs(fileIDs)
	if len(normalizedIDs) > MaxTargetedFiles {
		return plan, ErrTooManyTargetedFiles
	}
	if len(normalizedIDs) == 0 {
		return plan, nil
	}
	if !authorityllm.ExecutionSubjectFromContext(ctx).HasTriggerer() {
		return plan, authorityllm.ErrTrustedTriggerRequired
	}
	operationCtx := ctx
	var rootTrigger authorityllm.TrustedTriggerContext
	hasRootTrigger := false
	if authorityllm.ExecutionSubjectFromContext(ctx).HasTriggerer() {
		var err error
		operationCtx, rootTrigger, err = filetrigger.Ensure(ctx, userID, filetrigger.PurposeFileEmbedding)
		if err != nil {
			return plan, err
		}
		hasRootTrigger = true
	}

	cfg := s.snapshot()
	available, reason, err := s.indexingAvailable(operationCtx, cfg)
	if !available {
		return plan, embeddingAvailabilityError(reason, err)
	}

	files, err := s.repo.GetActiveFileObjectsByIDs(operationCtx, userID, normalizedIDs)
	if err != nil {
		return plan, err
	}
	filesByID := make(map[string]domainconversation.FileObject, len(files))
	for i := range files {
		filesByID[files[i].FileID] = files[i]
	}

	embeddingSignature := configuredModelSignature(cfg)
	embeddingHost := strings.TrimRight(strings.TrimSpace(cfg.EmbeddingHost), "/")
	for _, fileID := range normalizedIDs {
		fileObj, found := filesByID[fileID]
		if !found {
			plan.Skipped = append(plan.Skipped, TargetedFileSkip{FileID: fileID, Reason: SkipReasonNotFound})
			continue
		}
		if reason := fileVectorizationSkipReason(cfg, fileObj, embeddingSignature); reason != "" {
			plan.Skipped = append(plan.Skipped, TargetedFileSkip{FileID: fileID, Reason: reason})
			continue
		}

		job := TargetedJob{
			FileID:             fileID,
			UserID:             userID,
			EmbeddingSignature: embeddingSignature,
			EmbeddingHost:      embeddingHost,
		}
		if hasRootTrigger {
			_, childTrigger, childErr := filetrigger.ChildForFile(operationCtx, fileID, embeddingSignature)
			if childErr != nil {
				return plan, childErr
			}
			job.TriggerContext = childTrigger
		} else {
			job.TriggerContext = rootTrigger
		}
		plan.Jobs = append(plan.Jobs, job)
	}
	return plan, nil
}

// QueueTargetedJob 原子登记单个已规划任务，防止并发提交产生重复队列消息。
// 真正的 processing 状态由 worker 领取消息后再设置。
func (s *Service) QueueTargetedJob(ctx context.Context, job TargetedJob) (bool, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(job.FileID) == "" || strings.TrimSpace(job.EmbeddingSignature) == "" {
		return false, nil
	}
	job.TriggerContext = repository.NormalizeTrustedTriggerContext(job.TriggerContext)
	if !job.TriggerContext.HasTriggerer() {
		return false, authorityllm.ErrTrustedTriggerRequired
	}
	if repository.HasTrustedTriggerMetadata(job.TriggerContext) {
		if job.TriggerContext.ResourceOwnerUserID != job.UserID {
			return false, authorityllm.ErrTrustedTriggerMismatch
		}
		if job.TriggerContext.Purpose != filetrigger.PurposeFileEmbedding {
			return false, authorityllm.ErrTrustedTriggerMismatch
		}
	}
	return s.repo.QueueFileEmbedding(ctx, job.UserID, job.FileID, job.EmbeddingSignature)
}

// ResolveFileVectorizationCapabilities 返回前端展示所需的后端事实状态。
func (s *Service) ResolveFileVectorizationCapabilities(
	ctx context.Context,
	files []domainconversation.FileObject,
) map[string]FileVectorizationCapability {
	capabilities := make(map[string]FileVectorizationCapability, len(files))
	cfg := s.snapshot()
	signature := configuredModelSignature(cfg)
	available, reason, _ := s.indexingAvailable(ctx, cfg)
	if !available {
		for i := range files {
			capabilityReason := reason
			if fileVectorIndexOutdated(files[i], signature) {
				capabilityReason = ReasonOutdatedIndex
			}
			capabilities[files[i].FileID] = FileVectorizationCapability{Reason: capabilityReason}
		}
		return capabilities
	}
	for i := range files {
		skipReason := fileVectorizationSkipReason(cfg, files[i], signature)
		reason := skipReason
		if reason == "" && fileVectorIndexOutdated(files[i], signature) {
			reason = ReasonOutdatedIndex
		}
		capabilities[files[i].FileID] = FileVectorizationCapability{
			CanVectorize: skipReason == "",
			Reason:       reason,
		}
	}
	return capabilities
}

// ProcessTargetedJob 执行从可恢复队列中领取的显式向量化任务。
func (s *Service) ProcessTargetedJob(ctx context.Context, job TargetedJob) error {
	if s == nil || s.repo == nil || strings.TrimSpace(job.FileID) == "" {
		return nil
	}
	job.TriggerContext = repository.NormalizeTrustedTriggerContext(job.TriggerContext)
	if !job.TriggerContext.HasTriggerer() {
		return authorityllm.ErrTrustedTriggerRequired
	}
	if repository.HasTrustedTriggerMetadata(job.TriggerContext) {
		var err error
		ctx, job.TriggerContext, err = filetrigger.Restore(ctx, job.TriggerContext)
		if err != nil {
			return err
		}
	}
	releaseSlot, err := s.acquireWorkSlot(ctx)
	if err != nil {
		return err
	}
	defer releaseSlot()

	cfg := s.snapshot()
	if configuredModelSignature(cfg) != strings.TrimSpace(job.EmbeddingSignature) ||
		strings.TrimRight(strings.TrimSpace(cfg.EmbeddingHost), "/") != strings.TrimRight(strings.TrimSpace(job.EmbeddingHost), "/") {
		_ = s.updateFileObjectEmbedStatus(ctx, job.UserID, job.FileID, job.EmbeddingSignature, "stale", errEmbeddingConfigurationChanged)
		return nil
	}
	available, reason, err := s.indexingAvailable(ctx, cfg)
	if !available {
		switch reason {
		case "embedding_disabled", "embedding_model_missing", "embedding_host_missing":
			_ = s.updateFileObjectEmbedStatus(ctx, job.UserID, job.FileID, job.EmbeddingSignature, "stale", errEmbeddingConfigurationChanged)
			return nil
		default:
			return embeddingAvailabilityError(reason, err)
		}
	}
	fileObj, err := s.repo.GetActiveFileObjectByID(ctx, job.UserID, job.FileID)
	if err != nil || fileObj == nil {
		return err
	}
	if fileObj.EmbedSignature != job.EmbeddingSignature || strings.ToLower(strings.TrimSpace(fileObj.EmbedStatus)) != "processing" {
		claimed, claimErr := s.repo.ClaimFileEmbedding(ctx, job.UserID, job.FileID, job.EmbeddingSignature)
		if claimErr != nil || !claimed {
			return claimErr
		}
	}
	return s.processClaimedFile(ctx, *fileObj, cfg, job.EmbeddingSignature, job.TriggerContext)
}

// FailTargetedJob 将投递失败的已领取任务释放为可重试状态。
func (s *Service) FailTargetedJob(ctx context.Context, job TargetedJob, cause error) error {
	return s.updateFileObjectEmbedStatus(ctx, job.UserID, job.FileID, job.EmbeddingSignature, "failed", cause)
}

// RequeueTargetedJob 将等待重试的任务恢复为排队状态，避免重试退避期间误显示为执行中或失败。
func (s *Service) RequeueTargetedJob(ctx context.Context, job TargetedJob, cause error) error {
	return s.updateFileObjectEmbedStatus(ctx, job.UserID, job.FileID, job.EmbeddingSignature, "queued", cause)
}

func fileVectorizationSkipReason(cfg config.Config, fileObj domainconversation.FileObject, embeddingSignature string) string {
	if fileObj.EmbedSignature == embeddingSignature {
		switch strings.ToLower(strings.TrimSpace(fileObj.EmbedStatus)) {
		case "ready":
			return SkipReasonAlreadyReady
		case "queued", "processing":
			return SkipReasonProcessing
		}
	}
	if !fileObj.ProcessingReady {
		return SkipReasonNotReady
	}
	if !canEmbedFile(cfg, fileObj) {
		return SkipReasonUnsupported
	}
	return ""
}

func fileVectorIndexOutdated(fileObj domainconversation.FileObject, embeddingSignature string) bool {
	status := strings.ToLower(strings.TrimSpace(fileObj.EmbedStatus))
	return status == "stale" || (status == "ready" && strings.TrimSpace(embeddingSignature) != "" && fileObj.EmbedSignature != embeddingSignature)
}

func normalizeTargetedFileIDs(fileIDs []string) []string {
	normalized := make([]string, 0, len(fileIDs))
	seen := make(map[string]struct{}, len(fileIDs))
	for _, value := range fileIDs {
		fileID := strings.TrimSpace(value)
		if fileID == "" {
			continue
		}
		if _, exists := seen[fileID]; exists {
			continue
		}
		seen[fileID] = struct{}{}
		normalized = append(normalized, fileID)
	}
	return normalized
}

func embeddingAvailabilityError(reason string, cause error) error {
	if cause != nil {
		return fmt.Errorf("%w: %w", ErrEmbeddingServiceUnavailable, cause)
	}
	if reason == "embedding_disabled" || reason == "embedding_model_missing" || reason == "embedding_host_missing" {
		return ErrEmbeddingServiceNotConfigured
	}
	return ErrEmbeddingServiceUnavailable
}

// ProcessFile 执行 embedding 完整流程。
func (s *Service) ProcessFile(ctx context.Context, fileObj domainconversation.FileObject) error {
	cfg := s.snapshot()
	embeddingSignature := configuredModelSignature(cfg)
	if !cfg.EmbeddingEnabled || strings.TrimSpace(cfg.RAGModel) == "" || strings.TrimSpace(cfg.EmbeddingHost) == "" {
		return nil
	}
	if s.repo == nil {
		return nil
	}
	if !canEmbedFile(cfg, fileObj) {
		return nil
	}
	var trigger authorityllm.TrustedTriggerContext
	if subject := authorityllm.ExecutionSubjectFromContext(ctx); subject.ExecutionID != "" {
		trigger = subject.TrustedTriggerContext
	} else {
		var restored bool
		var err error
		ctx, trigger, restored, err = filetrigger.RestoreFileObject(ctx, fileObj)
		if err != nil {
			return err
		}
		if !restored {
			trigger = authorityllm.TrustedTriggerContext{}
		}
	}
	if !trigger.HasTriggerer() {
		return authorityllm.ErrTrustedTriggerRequired
	}
	if trigger.Purpose == filetrigger.PurposeFileExtract {
		var err error
		ctx, trigger, err = filetrigger.ChildForFile(ctx, fileObj.FileID, embeddingSignature)
		if err != nil {
			return err
		}
	}
	releaseSlot, err := s.acquireWorkSlot(ctx)
	if err != nil {
		return err
	}
	defer releaseSlot()

	claimed, err := s.repo.ClaimFileEmbedding(ctx, fileObj.UserID, fileObj.FileID, embeddingSignature)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	return s.processClaimedFile(ctx, fileObj, cfg, embeddingSignature, trigger)
}

func (s *Service) processClaimedFile(
	ctx context.Context,
	fileObj domainconversation.FileObject,
	cfg config.Config,
	embeddingSignature string,
	trigger authorityllm.TrustedTriggerContext,
) error {
	if !trigger.HasTriggerer() {
		return authorityllm.ErrTrustedTriggerRequired
	}
	text, err := s.loadSourceText(ctx, fileObj)
	if err != nil {
		_ = s.updateFileObjectEmbedStatus(ctx, fileObj.UserID, fileObj.FileID, embeddingSignature, "failed", err)
		return err
	}
	if strings.TrimSpace(text) == "" {
		_ = s.updateFileObjectEmbedStatus(ctx, fileObj.UserID, fileObj.FileID, embeddingSignature, "failed", errNoExtractableText)
		return fmt.Errorf("%w %s", errNoExtractableText, fileObj.FileID)
	}

	chunks := embeddingutil.ChunkText(text, cfg.EmbedChunkSizeTokens, cfg.EmbedChunkOverlapTokens)
	if len(chunks) == 0 {
		_ = s.updateFileObjectEmbedStatus(ctx, fileObj.UserID, fileObj.FileID, embeddingSignature, "failed", errEmptyChunks)
		return errEmptyChunks
	}

	embeddings, err := s.embedTextsWithConfig(ctx, chunks, cfg, "file.embedding")
	if err != nil {
		_ = s.updateFileObjectEmbedStatus(ctx, fileObj.UserID, fileObj.FileID, embeddingSignature, "failed", err)
		return err
	}

	now := time.Now()
	fileChunks := make([]domainconversation.FileChunk, 0, len(chunks))
	for i, chunk := range chunks {
		fileChunks = append(fileChunks, domainconversation.FileChunk{
			FileObjID:          fileObj.ID,
			UserID:             fileObj.UserID,
			ChunkIndex:         i,
			Content:            chunk,
			TokenCount:         int(tokenestimate.Estimate(chunk)),
			EmbeddingSignature: embeddingSignature,
			CreatedAt:          now,
		})
	}
	published, err := s.repo.ReplaceFileChunks(ctx, fileObj.ID, embeddingSignature, fileChunks, embeddings)
	if err != nil {
		_ = s.updateFileObjectEmbedStatus(ctx, fileObj.UserID, fileObj.FileID, embeddingSignature, "failed", err)
		return err
	}
	if !published {
		return nil
	}

	if current, countErr := s.repo.UpdateFileObjectChunkCount(ctx, fileObj.ID, embeddingSignature, len(fileChunks)); countErr != nil {
		return countErr
	} else if !current {
		return nil
	}
	return s.completeFileEmbedding(ctx, fileObj, embeddingSignature, cfg.EmbeddingHost)
}

func (s *Service) acquireWorkSlot(ctx context.Context) (func(), error) {
	if s == nil || s.workSlots == nil {
		return func() {}, nil
	}
	select {
	case s.workSlots <- struct{}{}:
		return func() { <-s.workSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) completeFileEmbedding(ctx context.Context, fileObj domainconversation.FileObject, expectedSignature string, expectedHost string) error {
	const configurationChanged = "embedding configuration changed during processing"
	if !s.embeddingConfigurationCurrent(expectedSignature, expectedHost) {
		_, err := s.repo.UpdateFileObjectEmbedStatus(ctx, fileObj.UserID, fileObj.FileID, expectedSignature, "stale", configurationChanged)
		return err
	}
	current, err := s.repo.UpdateFileObjectEmbedStatus(ctx, fileObj.UserID, fileObj.FileID, expectedSignature, "ready", "")
	if err != nil || !current {
		return err
	}
	// The second check closes the window where configuration changes between
	// the first check and publishing the ready state. A later change observes
	// a ready file and is handled by the normal global invalidation path.
	if !s.embeddingConfigurationCurrent(expectedSignature, expectedHost) {
		_, err = s.repo.UpdateFileObjectEmbedStatus(ctx, fileObj.UserID, fileObj.FileID, expectedSignature, "stale", configurationChanged)
		return err
	}
	return nil
}

func (s *Service) embeddingConfigurationCurrent(expectedSignature string, expectedHost string) bool {
	cfg := s.snapshot()
	return configuredModelSignature(cfg) == expectedSignature &&
		strings.TrimRight(strings.TrimSpace(cfg.EmbeddingHost), "/") == strings.TrimRight(strings.TrimSpace(expectedHost), "/")
}

func (s *Service) updateFileObjectEmbedStatus(ctx context.Context, userID uint, fileID string, embeddingSignature string, status string, embedErr error) error {
	if s == nil || s.repo == nil {
		return nil
	}
	writeCtx := ctx
	if writeCtx == nil || writeCtx.Err() != nil {
		var cancel context.CancelFunc
		writeCtx, cancel = background.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	_, err := s.repo.UpdateFileObjectEmbedStatus(writeCtx, userID, fileID, embeddingSignature, status, ErrorSummary(embedErr))
	return err
}

// WaitReady 轮询等待文件 embedding 就绪。
func (s *Service) WaitReady(ctx context.Context, userID uint, fileID string, timeout time.Duration) bool {
	if s == nil || s.repo == nil {
		return false
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		fo, err := s.repo.GetActiveFileObjectByID(ctx, userID, fileID)
		if err != nil || fo == nil {
			return false
		}
		if fo.EmbedStatus == "ready" {
			return true
		}
		if fo.EmbedStatus == "failed" {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	return false
}

func (s *Service) loadSourceText(ctx context.Context, fileObj domainconversation.FileObject) (string, error) {
	if s != nil && s.repo != nil {
		if result, err := s.repo.GetFileObjectProcessingByObjectID(ctx, fileObj.ID); err == nil && result != nil {
			if path := strings.TrimSpace(result.ExtractStoragePath); path != "" && s.extractSvc != nil {
				text, readErr := s.extractSvc.ReadExtractedText(ctx, path)
				if readErr == nil && strings.TrimSpace(text) != "" {
					return text, nil
				}
			}
		}
	}

	cfg := s.snapshot()
	if s.extractSvc == nil {
		return "", fmt.Errorf("extract service not configured")
	}
	result, err := s.extractSvc.ExtractStoredFile(ctx, extraction.ExtractInput{
		File:                  fileObj,
		PDFMaxPages:           cfg.FileFullContextPDFMaxPages,
		OCREngine:             cfg.ExtractOCREngine,
		ImageOCREnabled:       cfg.ExtractImageOCREnabled,
		PDFOCRFallbackEnabled: cfg.ExtractPDFOCRFallbackEnabled,
	})
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// EmbedTexts 对外暴露向量化能力，供消息历史 embedding 等场景复用。
// 参数与返回值与内部 embedTexts 相同，失败时返回 error 而非 panic。
func (s *Service) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	embeddings, _, err := s.EmbedTextsWithSignature(ctx, texts)
	return embeddings, err
}

// EmbedTextsWithSignature 使用同一份配置快照生成向量和签名，避免配置切换期间错标向量空间。
func (s *Service) EmbedTextsWithSignature(ctx context.Context, texts []string) ([][]float32, string, error) {
	return s.EmbedTextsWithSignatureFor(ctx, texts, "embedding")
}

// EmbedTextsWithSignatureFor 为指定辅助 producer 生成向量，并在 provider 边界
// 显式建立可信 execution child。operation 只用于稳定区分不同 producer。
func (s *Service) EmbedTextsWithSignatureFor(ctx context.Context, texts []string, operation string) ([][]float32, string, error) {
	cfg := s.snapshot()
	embeddings, err := s.embedTextsWithConfig(ctx, texts, cfg, operation)
	if err != nil {
		return nil, "", err
	}
	return embeddings, configuredModelSignature(cfg), nil
}

func (s *Service) embedTextsWithConfig(ctx context.Context, texts []string, cfg config.Config, operation string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	model := strings.TrimSpace(cfg.RAGModel)
	host := strings.TrimSpace(cfg.EmbeddingHost)
	if !cfg.EmbeddingEnabled {
		return nil, fmt.Errorf("embedding disabled")
	}
	if model == "" || host == "" {
		return nil, fmt.Errorf("embedding model or host missing")
	}
	if s.embedClient == nil {
		return nil, fmt.Errorf("embedding client not configured")
	}

	providerCtx, err := prepareEmbeddingProviderContext(ctx, texts, cfg, operation)
	if err != nil {
		return nil, err
	}
	apiBase := strings.TrimRight(host, "/")
	apiKey := strings.TrimSpace(cfg.EmbeddingKey)
	batchSize := cfg.EmbedBatchSize
	if batchSize <= 0 {
		batchSize = 20
	}

	var allEmbeddings [][]float32
	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batchEmbeddings, batchErr := s.embedClient.CallAPI(providerCtx, portembedding.Request{
			APIBase:        apiBase,
			APIKey:         apiKey,
			Model:          model,
			Texts:          texts[start:end],
			Dimensions:     cfg.EmbeddingOutputDimensions,
			TimeoutSeconds: cfg.EmbeddingTimeoutSeconds,
		})
		if batchErr != nil {
			return nil, batchErr
		}
		if len(batchEmbeddings) != end-start {
			return nil, fmt.Errorf("embedding batch returned %d vectors for %d texts", len(batchEmbeddings), end-start)
		}
		allEmbeddings = append(allEmbeddings, batchEmbeddings...)
	}
	if !cfg.EmbeddingNormalize {
		return allEmbeddings, nil
	}
	for index := range allEmbeddings {
		allEmbeddings[index] = l2Normalize(allEmbeddings[index])
	}
	return allEmbeddings, nil
}

func prepareEmbeddingProviderContext(ctx context.Context, texts []string, cfg config.Config, operation string) (context.Context, error) {
	if ctx == nil {
		return ctx, authorityllm.ErrTrustedTriggerRequired
	}
	subject := authorityllm.ExecutionSubjectFromContext(ctx)
	if !subject.HasTriggerer() {
		return ctx, authorityllm.ErrTrustedTriggerRequired
	}
	operation = strings.TrimSpace(operation)
	if operation == "" {
		operation = "embedding"
	}
	seed := strings.Join([]string{
		"deeix-chat:embedding-provider",
		operation,
		subject.RunID,
		subject.ExecutionID,
		configuredModelSignature(cfg),
		strings.TrimRight(strings.TrimSpace(cfg.EmbeddingHost), "/"),
		strings.Join(texts, "\x00"),
	}, "\x00")
	if strings.TrimSpace(subject.ExecutionID) != "" {
		childID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed)).String()
		childCtx, err := authorityllm.WithTrustedExecutionChild(ctx, childID)
		if err != nil {
			return ctx, err
		}
		return childCtx, nil
	}

	trigger := subject.TrustedTriggerContext
	trigger.TriggererUserID = subject.TriggererUserID
	if strings.TrimSpace(trigger.Purpose) == "" {
		trigger.Purpose = "embedding"
	}
	if strings.TrimSpace(trigger.RunID) == "" {
		trigger.RunID = "embedding_" + uuid.NewString()
	}
	trigger.ExecutionID = uuid.NewString()
	if trigger.CreatedAt.IsZero() {
		trigger.CreatedAt = time.Now().UTC()
	}
	providerCtx, err := authorityllm.WithTrustedTriggerContext(ctx, trigger)
	if err != nil {
		return ctx, err
	}
	return providerCtx, nil
}

func (s *Service) snapshot() config.Config {
	if s == nil || s.cfg == nil {
		return config.Config{}
	}
	snapshot := s.cfg.Snapshot()
	if snapshot.UsesSub2Authority() {
		snapshot.EmbeddingHost = snapshot.Sub2BaseURL
		snapshot.EmbeddingKey = ""
	}
	return snapshot
}

// EmbeddingIndexStatus 表示向量索引的当前健康状态。
type EmbeddingIndexStatus struct {
	ModelSignature       string
	ReadyCount           int64
	StaleCount           int64
	PendingCount         int64
	FailedCount          int64
	NeedsReindex         bool
	ReindexJobID         string
	ReindexStatus        string
	ReindexPayerUserID   uint
	ReindexTotalFiles    int64
	ReindexSubmitted     int64
	ReindexCompleted     int64
	ReindexFailed        int64
	ReindexCursor        uint
	ReindexLastError     string
}

// ComputeModelSignature 根据模型名和输出维度计算模型签名（格式: hex8@dims）。
// 相同模型/维度组合始终产生相同签名，用于检测配置变更。
func ComputeModelSignature(model string, outputDimensions int) string {
	return embeddingutil.ModelSignature(model, outputDimensions)
}

// ComputeSpaceSignature derives a new opaque vector-space identifier when an
// administrator changes the model, output dimensions, or provider endpoint.
func ComputeSpaceSignature(model string, outputDimensions int, endpoint string) string {
	return embeddingutil.SpaceSignature(model, outputDimensions, endpoint)
}

func configuredModelSignature(cfg config.Config) string {
	if signature := strings.TrimSpace(cfg.EmbeddingModelSignature); signature != "" {
		return signature
	}
	if strings.TrimSpace(cfg.RAGModel) == "" {
		return ""
	}
	return ComputeModelSignature(cfg.RAGModel, cfg.EmbeddingOutputDimensions)
}

// GetIndexStatus 返回向量索引的健康状态快照。
func (s *Service) GetIndexStatus(ctx context.Context) (EmbeddingIndexStatus, error) {
	cfg := s.snapshot()
	signature := configuredModelSignature(cfg)
	status := EmbeddingIndexStatus{
		ModelSignature: signature,
	}
	if s.repo == nil {
		return status, nil
	}
	var err error
	if status.ReadyCount, err = s.repo.CountFilesByEmbedStatus(ctx, "ready"); err != nil {
		return status, err
	}
	if status.StaleCount, err = s.repo.CountFilesByEmbedStatus(ctx, "stale"); err != nil {
		return status, err
	}
	if status.FailedCount, err = s.repo.CountFilesByEmbedStatus(ctx, "failed"); err != nil {
		return status, err
	}
	noneCount, _ := s.repo.CountFilesByEmbedStatus(ctx, "none")
	queuedCount, _ := s.repo.CountFilesByEmbedStatus(ctx, "queued")
	processingCount, _ := s.repo.CountFilesByEmbedStatus(ctx, "processing")
	status.PendingCount = noneCount + queuedCount + processingCount
	status.NeedsReindex = status.StaleCount > 0
	if s.reindexRepo != nil {
		if job, jobErr := s.reindexRepo.GetActive(ctx); jobErr != nil {
			return status, jobErr
		} else if job != nil {
			status.ReindexJobID = job.JobID
			status.ReindexStatus = job.Status
			status.ReindexPayerUserID = job.TriggererUserID
			status.ReindexTotalFiles = job.TotalFiles
			status.ReindexSubmitted = job.SubmittedFiles
			status.ReindexCompleted = job.CompletedFiles
			status.ReindexFailed = job.FailedFiles
			status.ReindexCursor = job.Cursor
			status.ReindexLastError = job.LastError
		}
	}
	return status, nil
}

// MarkFilesStale 将不属于目标向量空间的文件标记为失效。
func (s *Service) MarkFilesStale(ctx context.Context, activeSignature string) (int64, error) {
	if s.repo == nil {
		return 0, nil
	}
	signature := strings.TrimSpace(activeSignature)
	if signature == "" {
		return 0, nil
	}
	return s.repo.MarkEmbeddedFilesStale(ctx, signature)
}

// ReconcileIndex 对账当前运行时配置与文件索引状态，用于启动恢复和失败补偿。
func (s *Service) ReconcileIndex(ctx context.Context) (int64, error) {
	return s.MarkFilesStale(ctx, configuredModelSignature(s.snapshot()))
}

// ReindexStaleFiles creates one durable, administrator-attributed intent and
// schedules it through the existing Q2 embedding queue. A missing trigger is
// never replaced with a file owner, worker, startup, or browser actor.
func (s *Service) ReindexStaleFiles(ctx context.Context, triggers ...authorityllm.TrustedTriggerContext) (int, error) {
	if s == nil || s.repo == nil || s.reindexRepo == nil || s.reindexQueue == nil {
		return 0, ErrReindexPersistenceUnavailable
	}
	if len(triggers) != 1 || !triggers[0].HasTriggerer() {
		return 0, ErrReindexInitiatorRequired
	}
	trigger := repository.NormalizeTrustedTriggerContext(triggers[0])
	if !repository.HasTrustedTriggerMetadata(trigger) || strings.TrimSpace(trigger.RunID) == "" || strings.TrimSpace(trigger.ExecutionID) == "" {
		return 0, ErrReindexInitiatorRequired
	}
	cfg := s.snapshot()
	available, _, err := s.indexingAvailable(ctx, cfg)
	if err != nil {
		return 0, err
	}
	if !available {
		return 0, ErrEmbeddingServiceNotConfigured
	}

	createdAt := trigger.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	total, err := s.countReindexCandidates(ctx, cfg, createdAt)
	if err != nil {
		return 0, err
	}
	if total == 0 {
		return 0, nil
	}
	job := &repository.EmbeddingReindexJob{
		JobID:               uuid.NewString(),
		TriggererUserID:     trigger.TriggererUserID,
		ResourceOwnerUserID: trigger.ResourceOwnerUserID,
		Purpose:             trigger.Purpose,
		RunID:               trigger.RunID,
		ExecutionID:         trigger.ExecutionID,
		ParentExecutionID:   trigger.ParentExecutionID,
		TriggerCreatedAt:    createdAt,
		EmbeddingSignature:  configuredModelSignature(cfg),
		EmbeddingHost:       strings.TrimRight(strings.TrimSpace(cfg.EmbeddingHost), "/"),
		EmbeddingModel:      strings.TrimSpace(cfg.RAGModel),
		EmbeddingDimensions: cfg.EmbeddingOutputDimensions,
		ConfigIdentity:      ComputeSpaceSignature(cfg.RAGModel, cfg.EmbeddingOutputDimensions, cfg.EmbeddingHost),
		Status:              repository.EmbeddingReindexStatusQueued,
		TotalFiles:          int64(total),
	}
	stored, created, err := s.reindexRepo.CreateOrGet(ctx, job)
	if err != nil {
		return 0, err
	}
	if !created || stored == nil {
		return 0, nil
	}
	s.signalReindex(stored.JobID)
	return total, nil
}

func (s *Service) countReindexCandidates(ctx context.Context, cfg config.Config, before time.Time) (int, error) {
	const pageSize = 100
	count := 0
	var afterID uint
	for {
		files, err := s.repo.ListFilesForReindex(ctx, pageSize, afterID)
		if err != nil {
			return count, err
		}
		if len(files) == 0 {
			return count, nil
		}
		for _, fileObj := range files {
			if !fileObj.CreatedAt.IsZero() && fileObj.CreatedAt.After(before) {
				continue
			}
			if canEmbedFile(cfg, fileObj) {
				count++
			}
		}
		if len(files) < pageSize {
			return count, nil
		}
		afterID = files[len(files)-1].ID
	}
}

func (s *Service) runReindex(ctx context.Context, jobID string) {
	if s == nil || s.reindexRepo == nil || s.reindexQueue == nil || strings.TrimSpace(jobID) == "" {
		return
	}
	now := time.Now().UTC()
	leaseUntil := now.Add(2 * time.Minute)
	job, claimed, err := s.reindexRepo.Claim(ctx, jobID, s.reindexWorkerID, now, leaseUntil)
	if err != nil || !claimed || job == nil {
		if err != nil && s.logger != nil {
			s.logger.Warn("embedding_reindex_claim_failed", zap.String("job_id", jobID), zap.Error(err))
		}
		return
	}
	s.reindexMu.Lock()
	s.reindexing = true
	s.reindexMu.Unlock()
	defer func() {
		s.reindexMu.Lock()
		s.reindexing = false
		s.reindexMu.Unlock()
	}()

	if !repository.HasTrustedTriggerMetadata(authorityllm.TrustedTriggerContext{
		TriggererUserID: job.TriggererUserID, ResourceOwnerUserID: job.ResourceOwnerUserID, Purpose: job.Purpose,
		RunID: job.RunID, ExecutionID: job.ExecutionID, ParentExecutionID: job.ParentExecutionID, CreatedAt: job.TriggerCreatedAt,
	}) {
		job.Status = repository.EmbeddingReindexStatusNeedsInitiator
		job.LastError = "durable reindex has no authenticated initiator; an administrator must trigger it again"
		_ = s.reindexRepo.SaveProgress(ctx, job)
		return
	}
	cfg := s.snapshot()
	if configuredModelSignature(cfg) != job.EmbeddingSignature || strings.TrimRight(strings.TrimSpace(cfg.EmbeddingHost), "/") != job.EmbeddingHost {
		job.Status = repository.EmbeddingReindexStatusFailed
		job.LastError = "embedding configuration changed; trigger a new reindex for the new vector space"
		completedAt := time.Now().UTC()
		job.CompletedAt = &completedAt
		_ = s.reindexRepo.SaveProgress(ctx, job)
		return
	}

	const pageSize = 100
	for ctx.Err() == nil {
		if !s.refreshReindexLease(ctx, job) {
			return
		}
		files, listErr := s.repo.ListFilesForReindex(ctx, pageSize, job.Cursor)
		if listErr != nil {
			job.Status = repository.EmbeddingReindexStatusFailed
			job.LastError = ErrorSummary(listErr)
			completedAt := time.Now().UTC()
			job.CompletedAt = &completedAt
			_ = s.reindexRepo.SaveProgress(ctx, job)
			return
		}
		if len(files) == 0 {
			break
		}
		for _, fileObj := range files {
			job.Cursor = fileObj.ID
			if !fileObj.CreatedAt.IsZero() && fileObj.CreatedAt.After(job.TriggerCreatedAt) {
				if !s.refreshReindexLease(ctx, job) {
					return
				}
				continue
			}
			if !canEmbedFile(cfg, fileObj) {
				if !s.refreshReindexLease(ctx, job) {
					return
				}
				continue
			}
			queued, queueErr := s.queueReindexFile(ctx, job, fileObj)
			if queueErr != nil {
				job.Status = repository.EmbeddingReindexStatusFailed
				job.LastError = ErrorSummary(queueErr)
				job.FailedFiles++
				completedAt := time.Now().UTC()
				job.CompletedAt = &completedAt
				_ = s.reindexRepo.SaveProgress(ctx, job)
				return
			}
			if queued {
				job.SubmittedFiles++
			}
			if !s.refreshReindexLease(ctx, job) {
				return
			}
		}
		if len(files) < pageSize {
			break
		}
	}
	if ctx.Err() != nil {
		return
	}
	s.monitorReindex(ctx, job)
}

func (s *Service) queueReindexFile(ctx context.Context, job *repository.EmbeddingReindexJob, fileObj domainconversation.FileObject) (bool, error) {
	rootCtx, _, err := filetrigger.Restore(ctx, authorityllm.TrustedTriggerContext{
		TriggererUserID: job.TriggererUserID, ResourceOwnerUserID: job.ResourceOwnerUserID, Purpose: job.Purpose,
		RunID: job.RunID, ExecutionID: job.ExecutionID, ParentExecutionID: job.ParentExecutionID, CreatedAt: job.TriggerCreatedAt,
	})
	if err != nil {
		return false, err
	}
	_, child, err := filetrigger.ChildForFile(rootCtx, fileObj.FileID, job.EmbeddingSignature)
	if err != nil {
		return false, err
	}
	// The Q2 message UserID remains the actual file owner. The global root has
	// resource owner 0 because one operation spans many owners; bind the child
	// envelope to the trusted file owner for downstream ACL/storage scope while
	// retaining the immutable administrator payer and operation/run identity.
	child.ResourceOwnerUserID = fileObj.UserID
	queued, err := s.repo.QueueFileEmbedding(ctx, fileObj.UserID, fileObj.FileID, job.EmbeddingSignature)
	if err != nil || !queued {
		return false, err
	}
	if err := s.reindexQueue.EnqueueFileEmbedding(ctx, fileObj.UserID, fileObj.FileID, job.EmbeddingSignature, job.EmbeddingHost, child); err != nil {
		_, _ = s.repo.UpdateFileObjectEmbedStatus(ctx, fileObj.UserID, fileObj.FileID, job.EmbeddingSignature, "failed", err)
		return false, err
	}
	return true, nil
}

func (s *Service) refreshReindexLease(ctx context.Context, job *repository.EmbeddingReindexJob) bool {
	if job == nil || s.reindexRepo == nil {
		return false
	}
	leaseUntil := time.Now().UTC().Add(2 * time.Minute)
	job.LeaseOwner = s.reindexWorkerID
	job.LeaseExpiresAt = &leaseUntil
	return s.reindexRepo.SaveProgress(ctx, job) == nil
}

func (s *Service) monitorReindex(ctx context.Context, job *repository.EmbeddingReindexJob) {
	if job == nil || s.reindexRepo == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		pending, pendingErr := s.reindexRepo.CountPendingFiles(ctx, job.EmbeddingSignature, job.TriggerCreatedAt)
		failed, failedErr := s.reindexRepo.CountFailedFiles(ctx, job.EmbeddingSignature, job.TriggerCreatedAt)
		if pendingErr != nil || failedErr != nil {
			if ctx.Err() != nil {
				return
			}
			job.LastError = "unable to read durable reindex progress"
			_ = s.refreshReindexLease(ctx, job)
		} else {
			job.FailedFiles = failed
			job.CompletedFiles = job.TotalFiles - pending - failed
			if job.CompletedFiles < 0 {
				job.CompletedFiles = 0
			}
			if pending == 0 {
				completedAt := time.Now().UTC()
				job.CompletedAt = &completedAt
				if failed > 0 {
					job.Status = repository.EmbeddingReindexStatusFailed
					job.LastError = "one or more files failed embedding; inspect file status and re-trigger"
				} else {
					job.Status = repository.EmbeddingReindexStatusCompleted
					job.LastError = ""
				}
				_ = s.reindexRepo.SaveProgress(ctx, job)
				return
			}
			if !s.refreshReindexLease(ctx, job) {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func supportsEmbeddingSource(fileObj domainconversation.FileObject, cfg config.Config) bool {
	switch strings.ToLower(strings.TrimSpace(fileObj.FileCategory)) {
	case "video":
		return false
	case "image":
		return cfg.ExtractImageOCREnabled
	}
	mime := strings.ToLower(strings.TrimSpace(fileObj.MimeType))
	name := strings.TrimSpace(fileObj.FileName)
	return filetype.IsText(mime, name) || isPDFMIME(mime, name) || isWordMIME(mime, name) || isPresentationMIME(mime, name) || isExcelMIME(mime, name)
}

// l2Normalize 对向量做 L2 归一化（除以欧氏模长），返回单位向量。
// 零向量（模为 0）保持不变，避免除零。
func l2Normalize(vector []float32) []float32 {
	var sumSq float64
	for _, v := range vector {
		sumSq += float64(v) * float64(v)
	}
	if sumSq == 0 {
		return vector
	}
	norm := float32(1.0 / math.Sqrt(sumSq))
	result := make([]float32, len(vector))
	for i, v := range vector {
		result[i] = v * norm
	}
	return result
}

func isPDFMIME(mimeType, fileName string) bool {
	m := strings.ToLower(strings.TrimSpace(mimeType))
	if m == "application/pdf" {
		return true
	}
	if idx := strings.LastIndex(fileName, "."); idx >= 0 {
		return strings.ToLower(fileName[idx+1:]) == "pdf"
	}
	return false
}

func isWordMIME(mimeType, fileName string) bool {
	m := strings.ToLower(strings.TrimSpace(mimeType))
	ext := ""
	if idx := strings.LastIndex(fileName, "."); idx >= 0 {
		ext = strings.ToLower(fileName[idx+1:])
	}
	return strings.Contains(m, "wordprocessingml") || strings.Contains(m, "msword") ||
		ext == "docx" || ext == "doc"
}

func isPresentationMIME(mimeType, fileName string) bool {
	m := strings.ToLower(strings.TrimSpace(mimeType))
	ext := ""
	if idx := strings.LastIndex(fileName, "."); idx >= 0 {
		ext = strings.ToLower(fileName[idx+1:])
	}
	return strings.Contains(m, "presentationml") || strings.Contains(m, "ms-powerpoint") ||
		ext == "pptx" || ext == "ppt"
}

func isExcelMIME(mimeType, fileName string) bool {
	m := strings.ToLower(strings.TrimSpace(mimeType))
	ext := ""
	if idx := strings.LastIndex(fileName, "."); idx >= 0 {
		ext = strings.ToLower(fileName[idx+1:])
	}
	return strings.Contains(m, "spreadsheetml") || strings.Contains(m, "ms-excel") ||
		m == "text/csv" || ext == "xlsx" || ext == "xls" || ext == "csv"
}
