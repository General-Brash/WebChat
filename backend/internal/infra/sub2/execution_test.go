package sub2

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	sub2port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
)

func newServiceSettlementTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{
		httpClient:        server.Client(),
		baseURL:           baseURL,
		serviceID:         "chat",
		signingKeyID:      "key",
		signingPrivateKey: ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)),
		application:       "deeix-chat",
		maxResponseBytes:  1 << 20,
	}
}

func TestQueryServiceSettlementUsesStableContract(t *testing.T) {
	fingerprint := "10a63d4ca7a2208e2f70d8b24be24bd88d8173d0d5b247b5a48da920bd319b43"
	requestKey := "s2svc2ae36b2e339089a09bd3a76327af496bcd3b6341bd55ffb956811213605"
	client := newServiceSettlementTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != serviceSettlementQueryPath || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var request sub2port.ServiceSettlementQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Application != "deeix-chat" || request.RunID != "run-1" || request.RequestKey != requestKey || request.PayloadFingerprint != fingerprint {
			t.Fatalf("request = %#v", request)
		}
		_ = json.NewEncoder(w).Encode(sub2port.ServiceSettlementResponse{Application: "deeix-chat", RunID: "run-1", RequestKey: requestKey, PayloadFingerprint: fingerprint, State: sub2port.ServiceSettlementStateNotSubmitted, IdentityEpoch: 7})
	}))
	result, err := client.QueryServiceSettlement(context.Background(), sub2port.ServiceSettlementQueryRequest{
		Identity:           sub2port.IdentityRef{Issuer: "https://issuer", Subject: "subject", SubjectAssertion: "proof", IdentityEpoch: 7},
		Application:        "deeix-chat",
		RunID:              "run-1",
		RequestKey:         requestKey,
		PayloadFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if result.State != sub2port.ServiceSettlementStateNotSubmitted {
		t.Fatalf("state = %q", result.State)
	}
}

func TestSettleServicesPreservesParentPendingAsRemoteError(t *testing.T) {
	fingerprint := "10a63d4ca7a2208e2f70d8b24be24bd88d8173d0d5b247b5a48da920bd319b43"
	requestKey := "s2svc2ae36b2e339089a09bd3a76327af496bcd3b6341bd55ffb956811213605"
	client := newServiceSettlementTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != serviceSettlementPath {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "parent_settlement_pending"})
	}))
	_, err := client.SettleServices(context.Background(), sub2port.ServiceSettlementRequest{
		Identity:           sub2port.IdentityRef{Issuer: "https://issuer", Subject: "subject", SubjectAssertion: "proof", IdentityEpoch: 7},
		Application:        "deeix-chat",
		RunID:              "run-1",
		RequestKey:         requestKey,
		PayloadFingerprint: fingerprint,
		PricingVersion:     1,
		Items:              []sub2port.ServiceSettlementItem{{Code: "mcp:2:search", Count: 2, ExpectedUnitNanousd: 1000}},
	})
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "parent_settlement_pending" {
		t.Fatalf("error = %v", err)
	}
}
