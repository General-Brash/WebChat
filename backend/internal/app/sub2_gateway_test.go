package app

import (
	"context"
	"errors"
	"testing"

	domainsub2 "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/sub2"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	sub2port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
)

func TestSub2ExecutionRequestHashBindsCanonicalDispatchParameters(t *testing.T) {
	base := sub2port.GatewayRequest{
		Application:    "deeix-chat",
		RunID:          "run-1",
		Model:          "provider-model",
		RetailModel:    "retail-model",
		Protocol:       "openai_responses",
		Method:         "POST",
		Path:           "/v1/responses",
		Query:          "background=true",
		ContentType:    "application/json",
		GroupIDs:       []int64{10, 20},
		GroupRevision:  3,
		PricingVersion: 4,
		PlayerVersion:  5,
		PlayerGroupIDs: []uint{30},
		Headers:        map[string]string{"OpenAI-Beta": "responses=v1"},
		Body:           []byte(`{"z":1,"a":"same"}`),
	}
	want := sub2ExecutionRequestHash(base)
	if want == "" {
		t.Fatal("expected execution fingerprint")
	}
	canonicalEquivalent := base
	canonicalEquivalent.Body = []byte(`{"a":"same","z":1}`)
	if got := sub2ExecutionRequestHash(canonicalEquivalent); got != want {
		t.Fatalf("canonical JSON body changed fingerprint: got %q want %q", got, want)
	}

	mutations := []struct {
		name   string
		mutate func(*sub2port.GatewayRequest)
	}{
		{name: "model", mutate: func(v *sub2port.GatewayRequest) { v.Model = "provider-model-2" }},
		{name: "retail model", mutate: func(v *sub2port.GatewayRequest) { v.RetailModel = "retail-model-2" }},
		{name: "protocol", mutate: func(v *sub2port.GatewayRequest) { v.Protocol = "openai_chat_completions" }},
		{name: "path", mutate: func(v *sub2port.GatewayRequest) { v.Path = "/v1/chat/completions" }},
		{name: "query", mutate: func(v *sub2port.GatewayRequest) { v.Query = "background=false" }},
		{name: "group", mutate: func(v *sub2port.GatewayRequest) { v.GroupIDs = []int64{20, 10} }},
		{name: "version", mutate: func(v *sub2port.GatewayRequest) { v.PricingVersion++ }},
		{name: "run", mutate: func(v *sub2port.GatewayRequest) { v.RunID = "run-2" }},
		{name: "body", mutate: func(v *sub2port.GatewayRequest) { v.Body = []byte(`{"a":"changed","z":1}`) }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			changed := base
			changed.GroupIDs = append([]int64(nil), base.GroupIDs...)
			changed.PlayerGroupIDs = append([]uint(nil), base.PlayerGroupIDs...)
			changed.Headers = map[string]string{"OpenAI-Beta": "responses=v1"}
			tc.mutate(&changed)
			if got := sub2ExecutionRequestHash(changed); got == want {
				t.Fatalf("mutation %q reused the original fingerprint %q", tc.name, got)
			}
		})
	}
}

func TestSub2PrepareUsesTrustedActorNotInputOrRouteUser(t *testing.T) {
	ctx, err := llm.WithAuthenticatedExecutionSubject(context.Background(), 11, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	ctx = llm.WithExecutionSubject(ctx, 22, "run-a")
	ctx, subject, err := trustedSub2Subject(ctx, llm.GenerateInput{UserID: 44, RunID: "run-a", ExecutionID: "exec-a"})
	if err != nil {
		t.Fatalf("trusted subject resolution: %v", err)
	}
	if subject.TriggererUserID != 11 || subject.ResourceOwnerUserID != 22 {
		t.Fatalf("subject = %#v, want actor 11 and owner 22", subject)
	}
	if llm.ExecutionSubjectFromContext(ctx).TriggererUserID != 11 {
		t.Fatal("child input fields changed the trusted actor")
	}
}

func TestSub2PrepareFailsClosedBeforeTransportWithoutTrustedActor(t *testing.T) {
	gateway := &sub2Gateway{}
	_, _, dispatch, err := gateway.prepare(context.Background(), llm.RouteConfig{UserID: 33}, llm.GenerateInput{UserID: 44, RunID: "run-a", ExecutionID: "exec-a"})
	if !errors.Is(err, llm.ErrTrustedTriggerRequired) {
		t.Fatalf("error = %v, want trusted trigger requirement", err)
	}
	if dispatch != nil {
		t.Fatal("missing actor must not create a dispatch")
	}
}

func TestSub2ExecutionAttributionMismatchCannotReuseExecution(t *testing.T) {
	previous := &domainsub2.ExternalExecution{
		TriggererUserID:     11,
		ResourceOwnerUserID: 0,
		Purpose:             "chat.main",
		ParentExecutionID:   "parent-a",
		ChatRunID:           "run-a",
		Issuer:              "issuer",
		Subject:             "subject-a",
		ExternalUserID:      "external-a",
		IdentityEpoch:       7,
	}
	current := &sub2Dispatch{
		triggererUserID:     12,
		resourceOwnerUserID: 0,
		purpose:             "chat.main",
		parentExecutionID:   "parent-a",
		request: sub2port.GatewayRequest{
			RunID: "run-a",
		},
	}
	if sameSub2ExecutionAttribution(previous, current, sub2port.IdentityRef{Issuer: "issuer", Subject: "subject-a", ExternalUserID: "external-a", IdentityEpoch: 7}) {
		t.Fatal("same execution accepted a different trusted triggerer")
	}
}

func TestSub2ExecutionAttributionUsesStableIdentityNotRotatingAssertion(t *testing.T) {
	previous := &domainsub2.ExternalExecution{
		TriggererUserID: 11, ResourceOwnerUserID: 22, Purpose: "chat.main", ParentExecutionID: "parent-a",
		ChatRunID: "run-a", Application: "chat", Issuer: "issuer", Subject: "subject-a", ExternalUserID: "external-a", IdentityEpoch: 7,
	}
	current := &sub2Dispatch{
		triggererUserID: 11, resourceOwnerUserID: 22, purpose: "chat.main", parentExecutionID: "parent-a",
		request: sub2port.GatewayRequest{RunID: "run-a", Application: "chat"},
	}
	rotated := sub2port.IdentityRef{Issuer: "issuer", Subject: "subject-a", ExternalUserID: "external-a", IdentityEpoch: 7, SubjectAssertion: "rotated-proof"}
	if !sameSub2ExecutionAttribution(previous, current, rotated) {
		t.Fatal("rotating assertion bytes must not create an ownership conflict")
	}
	for _, tc := range []struct {
		name     string
		identity sub2port.IdentityRef
		app      string
	}{
		{name: "different issuer", identity: sub2port.IdentityRef{Issuer: "other-issuer", Subject: "subject-a", ExternalUserID: "external-a", IdentityEpoch: 7}},
		{name: "different subject", identity: sub2port.IdentityRef{Issuer: "issuer", Subject: "subject-b", ExternalUserID: "external-a", IdentityEpoch: 7}},
		{name: "different application", identity: rotated, app: "other-app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := *current
			candidate.request.Application = tc.app
			if candidate.request.Application == "" {
				candidate.request.Application = "chat"
			}
			if sameSub2ExecutionAttribution(previous, &candidate, tc.identity) {
				t.Fatalf("%s was accepted", tc.name)
			}
		})
	}
}

func TestSub2ControlOperationsDoNotCreatePaidSubmissionRecords(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
		want   bool
	}{
		{method: "GET", path: "/v1/responses/resp-1", want: false},
		{method: "POST", path: "/v1/responses/resp-1/cancel", want: false},
		{method: "POST", path: "/v1/responses", want: true},
	} {
		if got := isSub2PaidModelSubmission(tc.method, tc.path); got != tc.want {
			t.Fatalf("%s %s submission = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
