package settings

import (
	appembedding "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/embedding"
	appruntime "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/runtime"
)

type ServiceRuntimeResponse struct {
	Source        string `json:"source"`
	BaseURL       string `json:"baseURL"`
	ContainerName string `json:"containerName"`
	Image         string `json:"image"`
	Network       string `json:"network"`
	Status        string `json:"status"`
	Reachable     bool   `json:"reachable"`
	Message       string `json:"message"`
}

type EmbeddingIndexStatusResponse struct {
	ModelSignature     string `json:"modelSignature"`
	ReadyCount         int64  `json:"readyCount"`
	StaleCount         int64  `json:"staleCount"`
	PendingCount       int64  `json:"pendingCount"`
	FailedCount        int64  `json:"failedCount"`
	NeedsReindex       bool   `json:"needsReindex"`
	ReindexJobID       string `json:"reindexJobId,omitempty"`
	ReindexStatus      string `json:"reindexStatus"`
	ReindexPayerUserID uint   `json:"reindexPayerUserId,omitempty"`
	ReindexTotal       int64  `json:"reindexTotal"`
	ReindexSubmitted   int64  `json:"reindexSubmitted"`
	ReindexCompleted   int64  `json:"reindexCompleted"`
	ReindexFailed      int64  `json:"reindexFailed"`
	ReindexCursor      uint   `json:"reindexCursor"`
	ReindexLastError   string `json:"reindexLastError,omitempty"`
}

type EmbeddingReindexResponse struct {
	Submitted     int    `json:"submitted"`
	JobID         string `json:"jobId,omitempty"`
	Status        string `json:"status"`
	PayerUserID   uint   `json:"payerUserId,omitempty"`
	Message       string `json:"message"`
}

func toServiceRuntimeResponse(view appruntime.ServiceRuntimeView) ServiceRuntimeResponse {
	return ServiceRuntimeResponse{
		Source:        view.Source,
		BaseURL:       view.BaseURL,
		ContainerName: view.ContainerName,
		Image:         view.Image,
		Network:       view.Network,
		Status:        view.Status,
		Reachable:     view.Reachable,
		Message:       view.Message,
	}
}

func toEmbeddingIndexStatusResponse(status appembedding.EmbeddingIndexStatus) EmbeddingIndexStatusResponse {
	return EmbeddingIndexStatusResponse{
		ModelSignature: status.ModelSignature, ReadyCount: status.ReadyCount, StaleCount: status.StaleCount,
		PendingCount: status.PendingCount, FailedCount: status.FailedCount, NeedsReindex: status.NeedsReindex,
		ReindexJobID: status.ReindexJobID, ReindexStatus: status.ReindexStatus, ReindexPayerUserID: status.ReindexPayerUserID,
		ReindexTotal: status.ReindexTotalFiles, ReindexSubmitted: status.ReindexSubmitted, ReindexCompleted: status.ReindexCompleted,
		ReindexFailed: status.ReindexFailed, ReindexCursor: status.ReindexCursor, ReindexLastError: status.ReindexLastError,
	}
}

func toTikaRuntimeResponse(view appruntime.ServiceRuntimeView) ServiceRuntimeResponse {
	return toServiceRuntimeResponse(view)
}

func toDoclingRuntimeResponse(view appruntime.ServiceRuntimeView) ServiceRuntimeResponse {
	return toServiceRuntimeResponse(view)
}

func toTesseractRuntimeResponse(view appruntime.ServiceRuntimeView) ServiceRuntimeResponse {
	return toServiceRuntimeResponse(view)
}

func toRapidOCRRuntimeResponse(view appruntime.ServiceRuntimeView) ServiceRuntimeResponse {
	return toServiceRuntimeResponse(view)
}

func toMinerURuntimeResponse(view appruntime.ServiceRuntimeView) ServiceRuntimeResponse {
	return toServiceRuntimeResponse(view)
}
