package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/embedding"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/google/uuid"
)

// Auxiliary requests use exactly the same signed subject/policy/execution
// boundary as chat. Legacy service-host credentials are never forwarded.
func (g *sub2Gateway) auxiliaryHTTP(modelName, protocol, path string, request *http.Request) (*http.Response, error) {
	subject := llm.ExecutionSubjectFromContext(request.Context())
	if !subject.HasTriggerer() {
		return nil, llm.ErrTrustedTriggerRequired
	}
	trigger := subject.TrustedTriggerContext
	if request.Body == nil {
		request.Body = io.NopCloser(bytes.NewReader(nil))
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, (64<<20)+1))
	request.Body.Close()
	if err != nil || len(body) > 64<<20 {
		return nil, fmt.Errorf("auxiliary request too large")
	}
	executionID := strings.TrimSpace(trigger.ExecutionID)
	if executionID == "" && trigger.RunID != "" {
		digest := sha256.Sum256(body)
		executionID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s:%d:%s:%s:%s", g.cfg.Sub2Application, trigger.TriggererUserID, trigger.RunID, protocol, hex.EncodeToString(digest[:])))).String()
	}
	trigger.ExecutionID = executionID
	ctx, route, dispatch, err := g.prepare(request.Context(), llm.RouteConfig{RetailModel: modelName, UpstreamModel: modelName, Protocol: protocol}, llm.GenerateInput{RunID: trigger.RunID, ExecutionID: executionID, TriggerContext: &trigger})
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err = json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	payload["model"] = route.UpstreamModel
	body, err = json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	dispatch.request.Protocol = protocol
	forwarded := request.Clone(ctx)
	forwarded.URL.Path = path
	forwarded.URL.RawPath = ""
	forwarded.URL.RawQuery = ""
	forwarded.Body = io.NopCloser(bytes.NewReader(body))
	forwarded.ContentLength = int64(len(body))
	forwarded.GetBody = nil
	response, err := g.transport(route, forwarded)
	return response, err
}
func (g *sub2Gateway) embeddingHTTP(input embedding.Request, request *http.Request) (*http.Response, error) {
	return g.auxiliaryHTTP(input.Model, "embeddings", "/v1/embeddings", request)
}
func (g *sub2Gateway) mistralHTTP(modelName string, request *http.Request) (*http.Response, error) {
	return g.auxiliaryHTTP(modelName, "mistral_ocr", "/v1/ocr", request)
}
