package conversation

import (
	"context"
	"strings"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"go.uber.org/zap"
)

// deleteConversationFiles 清理会话删除后不再被其他活跃会话引用的文件。
func (s *Service) deleteConversationFiles(ctx context.Context, userID uint, fileIDs []string) (int, *model.StorageQuota) {
	if len(fileIDs) == 0 || s.uploadSvc == nil {
		return 0, nil
	}
	deletedCount := 0
	var latestQuota *model.StorageQuota
	for _, rawFileID := range fileIDs {
		fileID := strings.TrimSpace(rawFileID)
		if fileID == "" {
			continue
		}
		result, deleted, err := s.uploadSvc.DeleteFileIfUnreferenced(ctx, userID, fileID)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("delete_conversation_file_failed",
					zap.Uint("user_id", userID),
					zap.String("file_id", fileID),
					zap.Error(err),
				)
			}
			continue
		}
		if !deleted || result == nil {
			continue
		}
		deletedCount++
		quota := result.Quota
		latestQuota = &quota
	}
	return deletedCount, latestQuota
}
