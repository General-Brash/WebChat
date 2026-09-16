package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	domainsub2 "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/sub2"
	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/cache/memory"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/secretbox"
	sub2port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/requestmeta"
)

const sub2TestDataKey = "test-data-encryption-key-for-sub2"

type sub2AuthRepoStub struct {
	repository.AuthRepository
	user               *domainuser.User
	credential         *domainuser.Credential
	twoFactor          *domainuser.UserTwoFactor
	createSessionCall  int
	lastLoginUserID    uint
	revokeAllCalls     int
	updateProfileCalls int
}

func (r *sub2AuthRepoStub) GetByID(_ context.Context, userID uint) (*domainuser.User, error) {
	if r.user == nil || r.user.ID != userID {
		return nil, repository.ErrNotFound
	}
	copyUser := *r.user
	return &copyUser, nil
}

func (r *sub2AuthRepoStub) GetCredentialByUserID(_ context.Context, userID uint) (*domainuser.Credential, error) {
	if r.credential == nil || r.credential.UserID != userID {
		return nil, repository.ErrNotFound
	}
	copyCredential := *r.credential
	return &copyCredential, nil
}

func (r *sub2AuthRepoStub) UpdateProfile(_ context.Context, userID uint, input repository.UpdateUserFieldsInput) (*domainuser.User, error) {
	if r.user == nil || r.user.ID != userID {
		return nil, repository.ErrNotFound
	}
	if input.OnboardingCompletedAt != nil {
		r.user.OnboardingCompletedAt = *input.OnboardingCompletedAt
	}
	r.updateProfileCalls++
	copyUser := *r.user
	return &copyUser, nil
}

func (r *sub2AuthRepoStub) GetUserTwoFactorByUserID(_ context.Context, userID uint) (*domainuser.UserTwoFactor, error) {
	if r.twoFactor == nil || r.twoFactor.UserID != userID {
		return nil, repository.ErrNotFound
	}
	copyTwoFactor := *r.twoFactor
	return &copyTwoFactor, nil
}

func (r *sub2AuthRepoStub) CreateSession(_ context.Context, _ *domainuser.Session) error {
	r.createSessionCall++
	return nil
}

func (r *sub2AuthRepoStub) UpdateLastLogin(_ context.Context, userID uint) error {
	r.lastLoginUserID = userID
	return nil
}

func (r *sub2AuthRepoStub) RevokeAllSessions(_ context.Context, _ uint, _ string) error {
	r.revokeAllCalls++
	return nil
}

func (r *sub2AuthRepoStub) RecordAuthEvent(_ context.Context, _ repository.AuthEventInput) error {
	return nil
}

type sub2IdentityRepoStub struct {
	binding   *domainsub2.IdentityBinding
	syncInput repository.SyncSub2UserAndIdentityInput
}

func (r *sub2IdentityRepoStub) GetSub2IdentityBinding(_ context.Context, issuer, subject string) (*domainsub2.IdentityBinding, error) {
	if r.binding == nil || r.binding.Issuer != issuer || r.binding.Subject != subject {
		return nil, repository.ErrNotFound
	}
	copyBinding := *r.binding
	return &copyBinding, nil
}

func (r *sub2IdentityRepoStub) GetSub2IdentityBindingByUserID(_ context.Context, userID uint) (*domainsub2.IdentityBinding, error) {
	if r.binding == nil || r.binding.UserID != userID {
		return nil, repository.ErrNotFound
	}
	copyBinding := *r.binding
	return &copyBinding, nil
}

func (r *sub2IdentityRepoStub) CreateSub2IdentityBinding(_ context.Context, item *domainsub2.IdentityBinding) (*domainsub2.IdentityBinding, error) {
	return item, nil
}

func (r *sub2IdentityRepoStub) UpdateSub2IdentityBinding(_ context.Context, item *domainsub2.IdentityBinding) error {
	r.binding = item
	return nil
}

func (r *sub2IdentityRepoStub) CreateSub2UserWithIdentity(_ context.Context, _ repository.CreateSub2UserWithIdentityInput) error {
	return nil
}

func (r *sub2IdentityRepoStub) SyncSub2UserAndIdentity(_ context.Context, input repository.SyncSub2UserAndIdentityInput) error {
	r.syncInput = input
	copyBinding := *input.Identity
	r.binding = &copyBinding
	return nil
}

type sub2ProviderStub struct {
	status                   *sub2port.IdentityStatusResponse
	statusRequest            sub2port.IdentityStatusRequest
	tokenSet                 *sub2port.TokenSet
	claims                   *sub2port.IDTokenClaims
	subjectResponse          *sub2port.SubjectExchangeResponse
	exchangeAuthorizationErr error
	validateErr              error
	subjectErr               error
	statusErr                error
	exchangeCalls            int
	validateCalls            int
	subjectCalls             int
	authorizationCall        int
}

func (p *sub2ProviderStub) AuthorizationURL(state, nonce, codeChallenge string) (string, error) {
	p.authorizationCall++
	values := url.Values{"state": {state}, "nonce": {nonce}, "code_challenge": {codeChallenge}}
	return "https://sub2.example.test/authorize?" + values.Encode(), nil
}

func (p *sub2ProviderStub) ExchangeAuthorizationCode(context.Context, string, string, string) (*sub2port.TokenSet, error) {
	p.exchangeCalls++
	if p.exchangeAuthorizationErr != nil {
		return nil, p.exchangeAuthorizationErr
	}
	if p.tokenSet != nil {
		return p.tokenSet, nil
	}
	return nil, errors.New("unexpected authorization-code exchange")
}

func (p *sub2ProviderStub) ValidateIDToken(context.Context, string, string) (*sub2port.IDTokenClaims, error) {
	p.validateCalls++
	if p.validateErr != nil {
		return nil, p.validateErr
	}
	if p.claims != nil {
		return p.claims, nil
	}
	return nil, errors.New("unexpected ID-token validation")
}

func (p *sub2ProviderStub) GetIdentityStatus(_ context.Context, input sub2port.IdentityStatusRequest) (*sub2port.IdentityStatusResponse, error) {
	p.statusRequest = input
	if p.statusErr != nil {
		return nil, p.statusErr
	}
	return p.status, nil
}

func (p *sub2ProviderStub) ExchangeSubject(context.Context, sub2port.SubjectExchangeRequest) (*sub2port.SubjectExchangeResponse, error) {
	p.subjectCalls++
	if p.subjectErr != nil {
		return nil, p.subjectErr
	}
	if p.subjectResponse != nil {
		return p.subjectResponse, nil
	}
	return nil, errors.New("unexpected subject exchange")
}

func (p *sub2ProviderStub) GetWallet(context.Context, sub2port.WalletQueryRequest) (*sub2port.WalletResponse, error) {
	return nil, nil
}

func (p *sub2ProviderStub) ListModels(context.Context, sub2port.ModelCatalogRequest) (*sub2port.ModelCatalogResponse, error) {
	return nil, nil
}

func (p *sub2ProviderStub) AdmitModel(context.Context, sub2port.ModelAdmissionRequest) (*sub2port.ModelAdmissionResponse, error) {
	return nil, nil
}

func (p *sub2ProviderStub) QueryExecution(context.Context, sub2port.ExecutionQueryRequest) (*sub2port.ExecutionQueryResponse, error) {
	return nil, nil
}

func (p *sub2ProviderStub) CancelExecution(context.Context, sub2port.ExecutionCancelRequest) (*sub2port.ExecutionCancelResponse, error) {
	return nil, nil
}

func sub2TestConfig() config.Config {
	return config.Config{
		JWTSecret:            "test-jwt-secret",
		DataEncryptionKey:    sub2TestDataKey,
		Sub2AuthorityMode:    config.AuthorityModeSub2,
		BillingAuthority:     config.AuthorityModeSub2,
		Sub2Application:      "deeix-chat",
		Sub2OIDCIssuer:       "https://sub2.example.test",
		Sub2OIDCClientID:     "chat-client",
		Sub2OIDCRedirectURI:  "https://chat.example.test/api/v1/auth/sub2/callback",
		TokenTTLHours:        1,
		RefreshTokenTTLHours: 24,
		PublicWebBaseURL:     "https://chat.example.test",
	}
}

func newSub2TestService() (*Service, *memory.Cache, *sub2ProviderStub, *sub2IdentityRepoStub, *sub2AuthRepoStub) {
	authRepo := &sub2AuthRepoStub{}
	identityRepo := &sub2IdentityRepoStub{}
	provider := &sub2ProviderStub{}
	service := newTestService(sub2TestConfig(), authRepo, nil)
	cache := memory.New()
	service.SetProviderAuthBridge(memory.NewProviderAuthBridge(cache))
	service.SetSub2Integration(provider, identityRepo)
	return service, cache, provider, identityRepo, authRepo
}

func TestStartSub2LoginStoresBrowserBindingHash(t *testing.T) {
	service, cache, _, _, _ := newSub2TestService()
	result, err := service.StartSub2Login(context.Background(), "/chat")
	if err != nil {
		t.Fatalf("start Sub2 login: %v", err)
	}
	if result.BrowserBindingToken == "" {
		t.Fatal("expected a browser binding token")
	}
	authorizationURL, err := url.Parse(result.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	transaction, err := cache.ConsumeProviderAuthTransaction(context.Background(), authorizationURL.Query().Get("state"))
	if err != nil {
		t.Fatalf("consume transaction: %v", err)
	}
	if transaction.BrowserBindingHash == "" || transaction.BrowserBindingHash == result.BrowserBindingToken {
		t.Fatalf("expected a one-way browser binding hash, got %#v", transaction.BrowserBindingHash)
	}
	if !matchesSub2BrowserBinding(transaction.BrowserBindingHash, result.BrowserBindingToken) {
		t.Fatal("stored browser binding hash did not match the issued secret")
	}
}

func TestCompleteSub2LoginRejectsUnboundCallbackWithoutConsumingState(t *testing.T) {
	for _, bindingToken := range []string{"", "wrong-browser"} {
		t.Run(map[string]string{"": "missing", "wrong-browser": "mismatch"}[bindingToken], func(t *testing.T) {
			service, cache, provider, _, _ := newSub2TestService()
			result, err := service.StartSub2Login(context.Background(), "/chat")
			if err != nil {
				t.Fatalf("start Sub2 login: %v", err)
			}
			authorizationURL, err := url.Parse(result.AuthorizationURL)
			if err != nil {
				t.Fatalf("parse authorization URL: %v", err)
			}
			_, err = service.CompleteSub2Login(context.Background(), Sub2LoginCallbackInput{
				Code:                "authorization-code",
				State:               authorizationURL.Query().Get("state"),
				BrowserBindingToken: bindingToken,
			})
			if !errors.Is(err, ErrSub2AuthorizationTransactionInvalid) {
				t.Fatalf("expected browser-bound callback rejection, got %v", err)
			}
			if provider.exchangeCalls != 0 {
				t.Fatalf("provider exchange must not run for an unbound callback, got %d calls", provider.exchangeCalls)
			}
			if _, err = cache.ConsumeProviderAuthTransactionIfBrowserBindingMatches(context.Background(), authorizationURL.Query().Get("state"), hashToken(result.BrowserBindingToken)); err != nil {
				t.Fatalf("wrong-browser callback must preserve the legitimate transaction: %v", err)
			}
		})
	}
}

func TestCompleteSub2LoginDoesNotRequireLegacyChatTOTP(t *testing.T) {
	repo := &sub2AuthRepoStub{
		user:       &domainuser.User{ID: 42, Username: "alice", DisplayName: "Alice", Role: domainuser.RoleUser, Status: domainuser.StatusActive},
		credential: &domainuser.Credential{UserID: 42, PasswordEnabled: false, PasswordOrigin: domainuser.PasswordOriginSSOPlaceholder},
		twoFactor:  &domainuser.UserTwoFactor{UserID: 42, TOTPEnabled: true, TOTPSecretEncrypted: "legacy-secret"},
	}
	service := newTestService(sub2TestConfig(), repo, nil)
	result, err := service.completeSub2LoginForUser(context.Background(), repo.user, "subject-42", "request-42", requestmeta.SessionAuditContext{})
	if err != nil {
		t.Fatalf("complete Sub2 login: %v", err)
	}
	if result == nil || result.TwoFactorRequired {
		t.Fatalf("legacy Chat TOTP must not create a Sub2 login challenge: %#v", result)
	}
	if repo.createSessionCall != 1 || repo.lastLoginUserID != repo.user.ID {
		t.Fatalf("expected a normal Chat session, got create=%d last_login=%d", repo.createSessionCall, repo.lastLoginUserID)
	}
}

func TestCheckExternalIdentityRequiresSSOAfterEpochPromotion(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	encryptedAssertion, err := secretbox.EncryptString(sub2TestDataKey, "assertion")
	if err != nil {
		t.Fatalf("encrypt assertion: %v", err)
	}
	repo := &sub2AuthRepoStub{user: &domainuser.User{ID: 42, Username: "alice", Role: domainuser.RoleUser, Status: domainuser.StatusActive}}
	identityRepo := &sub2IdentityRepoStub{binding: &domainsub2.IdentityBinding{
		ID:                        7,
		UserID:                    42,
		Issuer:                    "https://sub2.example.test",
		Subject:                   "subject-42",
		ExternalUserID:            "42",
		IdentityEpoch:             1,
		Status:                    domainsub2.IdentityStatusActive,
		Role:                      domainsub2.ExternalRoleUser,
		PermissionVersion:         0,
		SubjectAssertionEncrypted: encryptedAssertion,
		SubjectAssertionExpiresAt: &expiresAt,
	}}
	provider := &sub2ProviderStub{status: &sub2port.IdentityStatusResponse{
		Identity: sub2port.IdentityRef{Issuer: "https://sub2.example.test", Subject: "subject-42", ExternalUserID: "42"},
		Active:   true, Status: domainsub2.IdentityStatusActive, IdentityEpoch: 2,
		Role: domainsub2.ExternalRoleAdmin, PermissionVersion: 1, PermissionVersionPresent: true,
		AssertionExpiresAt: expiresAt,
	}}
	service := newTestService(sub2TestConfig(), repo, nil)
	service.SetSub2Integration(provider, identityRepo)

	err = service.CheckExternalIdentity(context.Background(), repo.user.ID)
	if !errors.Is(err, ErrSub2IdentityEpochMismatch) {
		t.Fatalf("expected promotion after an epoch change to require fresh SSO, got %v", err)
	}
	if identityRepo.syncInput.UserRole != domainuser.RoleAdmin {
		t.Fatalf("expected trusted admin projection, got %q", identityRepo.syncInput.UserRole)
	}
	if repo.user.Status != domainuser.StatusActive {
		t.Fatalf("epoch promotion must not mark an active account suspended, got %q", repo.user.Status)
	}
	if provider.statusRequest.ExpectedIdentityEpoch != 1 {
		t.Fatalf("expected Chat to send the persisted epoch as a stale-session assertion, got %d", provider.statusRequest.ExpectedIdentityEpoch)
	}
}

func configureSuccessfulSub2Provider(provider *sub2ProviderStub, nonce string, externalRole string) {
	identity := sub2port.IdentityRef{
		Issuer:         "https://sub2.example.test",
		Subject:        "subject-42",
		ExternalUserID: "42",
		IdentityEpoch:  7,
	}
	expiresAt := time.Now().Add(time.Hour)
	provider.tokenSet = &sub2port.TokenSet{IDToken: "id-token"}
	provider.claims = &sub2port.IDTokenClaims{
		Issuer:            identity.Issuer,
		Subject:           identity.Subject,
		Nonce:             nonce,
		Email:             "alice@example.com",
		EmailVerified:     true,
		Name:              "Alice",
		PreferredUsername: "alice",
	}
	provider.subjectResponse = &sub2port.SubjectExchangeResponse{
		Identity:                 identity,
		Assertion:                "subject-assertion",
		ExpiresAt:                expiresAt,
		Role:                     externalRole,
		PermissionVersion:        1,
		PermissionVersionPresent: true,
	}
	provider.status = &sub2port.IdentityStatusResponse{
		Identity:                 identity,
		Active:                   true,
		Status:                   domainsub2.IdentityStatusActive,
		IdentityEpoch:            identity.IdentityEpoch,
		Role:                     externalRole,
		PermissionVersion:        1,
		PermissionVersionPresent: true,
		AssertionExpiresAt:       expiresAt,
	}
}

func TestCompleteSub2LoginWrongBrowserThenLegitimateCallbackSucceedsAndReplayFails(t *testing.T) {
	service, _, provider, identityRepo, repo := newSub2TestService()
	repo.user = &domainuser.User{ID: 42, Username: "alice", DisplayName: "Alice", Role: domainuser.RoleUser, Status: domainuser.StatusActive}
	repo.credential = &domainuser.Credential{UserID: 42, PasswordOrigin: domainuser.PasswordOriginSSOPlaceholder}
	identityRepo.binding = &domainsub2.IdentityBinding{ID: 7, UserID: 42, Issuer: "https://sub2.example.test", Subject: "subject-42", ExternalUserID: "42", IdentityEpoch: 7, Status: domainsub2.IdentityStatusActive, Role: domainsub2.ExternalRoleUser, PermissionVersion: 1}

	start, err := service.StartSub2Login(context.Background(), "/chat")
	if err != nil {
		t.Fatalf("start Sub2 login: %v", err)
	}
	authorizationURL, err := url.Parse(start.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	state := authorizationURL.Query().Get("state")
	configureSuccessfulSub2Provider(provider, authorizationURL.Query().Get("nonce"), domainsub2.ExternalRoleUser)

	_, err = service.CompleteSub2Login(context.Background(), Sub2LoginCallbackInput{
		Code:                "authorization-code",
		State:               state,
		BrowserBindingToken: "wrong-browser",
	})
	if !errors.Is(err, ErrSub2AuthorizationBrowserBindingMismatch) || !errors.Is(err, ErrSub2AuthorizationTransactionInvalid) {
		t.Fatalf("wrong-browser callback error = %v, want invalid binding and state compatibility", err)
	}
	if IsSub2AuthorizationTransactionConsumed(err) {
		t.Fatal("wrong-browser callback must not mark the transaction consumed")
	}
	if provider.exchangeCalls != 0 {
		t.Fatalf("wrong-browser callback must not exchange a provider code, got %d calls", provider.exchangeCalls)
	}

	result, err := service.CompleteSub2Login(context.Background(), Sub2LoginCallbackInput{
		Code:                "authorization-code",
		State:               state,
		BrowserBindingToken: start.BrowserBindingToken,
	})
	if err != nil {
		t.Fatalf("legitimate callback after wrong browser: %v", err)
	}
	if result == nil || result.User.ID != repo.user.ID || result.User.InitialSecurityRequired || result.User.InitialUsernameRequired {
		t.Fatalf("Sub2 callback returned inappropriate local onboarding state: %#v", result)
	}
	if provider.exchangeCalls != 1 || repo.updateProfileCalls != 1 || repo.user.OnboardingCompletedAt == nil {
		t.Fatalf("expected one provider exchange and legacy onboarding repair, exchanges=%d updates=%d onboarding=%v", provider.exchangeCalls, repo.updateProfileCalls, repo.user.OnboardingCompletedAt)
	}
	if provider.statusRequest.ExpectedIdentityEpoch != 7 {
		t.Fatalf("expected stale-session epoch assertion 7, got %d", provider.statusRequest.ExpectedIdentityEpoch)
	}

	_, err = service.CompleteSub2Login(context.Background(), Sub2LoginCallbackInput{
		Code:                "authorization-code",
		State:               state,
		BrowserBindingToken: start.BrowserBindingToken,
	})
	if !errors.Is(err, ErrSub2AuthorizationTransactionInvalid) || IsSub2AuthorizationTransactionConsumed(err) {
		t.Fatalf("replay error = %v, want unconsumed invalid state", err)
	}
	if provider.exchangeCalls != 1 {
		t.Fatalf("replay must not exchange provider code again, got %d calls", provider.exchangeCalls)
	}
}

func TestCompleteSub2LoginProviderDenialConsumesMatchingTransaction(t *testing.T) {
	service, _, provider, _, _ := newSub2TestService()
	start, err := service.StartSub2Login(context.Background(), "/chat")
	if err != nil {
		t.Fatalf("start Sub2 login: %v", err)
	}
	authorizationURL, err := url.Parse(start.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	input := Sub2LoginCallbackInput{State: authorizationURL.Query().Get("state"), ProviderError: "access_denied", BrowserBindingToken: start.BrowserBindingToken}
	_, err = service.CompleteSub2Login(context.Background(), input)
	if !errors.Is(err, ErrSub2AuthorizationDenied) || !IsSub2AuthorizationTransactionConsumed(err) {
		t.Fatalf("provider denial error = %v, want consumed authorization denial", err)
	}
	if provider.exchangeCalls != 0 {
		t.Fatalf("provider denial must not exchange an authorization code, got %d calls", provider.exchangeCalls)
	}
	input.ProviderError = ""
	input.Code = "authorization-code"
	_, err = service.CompleteSub2Login(context.Background(), input)
	if !errors.Is(err, ErrSub2AuthorizationTransactionInvalid) || IsSub2AuthorizationTransactionConsumed(err) {
		t.Fatalf("replayed denied callback error = %v, want invalid unused state", err)
	}
}

func TestCompleteSub2LoginRejectsExpiredTransaction(t *testing.T) {
	service, cache, _, _, _ := newSub2TestService()
	state := "expired-state"
	if err := cache.PutProviderAuthTransaction(context.Background(), state, repository.ProviderAuthTransaction{
		ProviderSlug:         sub2ProviderSlug,
		ClientID:             sub2TestConfig().Sub2OIDCClientID,
		ClientRedirectURI:    sub2TestConfig().Sub2OIDCRedirectURI,
		ProviderCodeVerifier: strings.Repeat("v", 43),
		ProviderNonce:        "nonce",
		BrowserBindingHash:   hashToken("browser"),
		ExpiresAt:            time.Now().Add(-time.Second),
	}, time.Millisecond); err != nil {
		t.Fatalf("put expired transaction: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	_, err := service.CompleteSub2Login(context.Background(), Sub2LoginCallbackInput{Code: "code", State: state, BrowserBindingToken: "browser"})
	if !errors.Is(err, ErrSub2AuthorizationTransactionInvalid) || IsSub2AuthorizationTransactionConsumed(err) {
		t.Fatalf("expired transaction error = %v, want unconsumed invalid state", err)
	}
}

func TestResolveSub2UserMarksJITUsersOnboardingCompleteForTrustedRoles(t *testing.T) {
	tests := []struct {
		name         string
		externalRole string
		chatRole     string
	}{
		{name: "user", externalRole: domainsub2.ExternalRoleUser, chatRole: domainuser.RoleUser},
		{name: "admin", externalRole: domainsub2.ExternalRoleAdmin, chatRole: domainuser.RoleAdmin},
		{name: "super_admin", externalRole: domainsub2.ExternalRoleSuperAdmin, chatRole: domainuser.RoleSuperAdmin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _, _, identityRepo, _ := newSub2TestService()
			now := time.Now().Add(time.Hour)
			identity := sub2port.IdentityRef{Issuer: "https://sub2.example.test", Subject: "subject-" + tt.name, ExternalUserID: tt.name, IdentityEpoch: 1}
			claims := &sub2port.IDTokenClaims{Email: tt.name + "@example.com", EmailVerified: true, PreferredUsername: tt.name, Name: tt.name}
			status := &sub2port.IdentityStatusResponse{Identity: identity, Active: true, Status: domainsub2.IdentityStatusActive, IdentityEpoch: 1, Role: tt.externalRole, PermissionVersion: 0, PermissionVersionPresent: true, AssertionExpiresAt: now}
			exchange := &sub2port.SubjectExchangeResponse{Identity: identity, Assertion: "assertion", ExpiresAt: now, Role: tt.externalRole, PermissionVersion: 0, PermissionVersionPresent: true}
			userItem, err := service.resolveSub2User(context.Background(), claims, identity, status, exchange)
			if err != nil {
				t.Fatalf("resolve JIT user: %v", err)
			}
			if userItem.Role != tt.chatRole || userItem.Status != domainuser.StatusActive || userItem.OnboardingCompletedAt == nil {
				t.Fatalf("unexpected JIT projection: %#v", userItem)
			}
			_ = identityRepo
		})
	}
}

func TestSub2TrustedRolesBypassLocalOnboardingAndTOTPWithoutWalletOrModelSetup(t *testing.T) {
	roles := []struct {
		name string
		role string
	}{
		{name: "user", role: domainuser.RoleUser},
		{name: "admin", role: domainuser.RoleAdmin},
		{name: "superadmin", role: domainuser.RoleSuperAdmin},
	}
	for _, tt := range roles {
		t.Run(tt.name, func(t *testing.T) {
			repo := &sub2AuthRepoStub{
				user:       &domainuser.User{ID: 42, Username: tt.name, DisplayName: tt.name, Role: tt.role, Status: domainuser.StatusActive},
				credential: &domainuser.Credential{UserID: 42, PasswordEnabled: true, MustResetPassword: true, PasswordOrigin: domainuser.PasswordOriginAdminCreated},
				twoFactor:  &domainuser.UserTwoFactor{UserID: 42, TOTPEnabled: true, TOTPSecretEncrypted: "legacy-secret"},
			}
			service := newTestService(sub2TestConfig(), repo, nil)
			result, err := service.completeSub2LoginForUser(context.Background(), repo.user, "subject-42", "request-42", requestmeta.SessionAuditContext{})
			if err != nil {
				t.Fatalf("complete trusted %s login without local wallet/model setup: %v", tt.role, err)
			}
			if result == nil || result.User.Role != tt.role || result.User.InitialSecurityRequired || result.User.InitialUsernameRequired || result.User.MustResetPassword || result.User.TwoFactorRequired || result.User.TwoFactorEnabled {
				t.Fatalf("trusted %s login exposed local security gates: %#v", tt.role, result)
			}
		})
	}
}

func TestLocalModeRetainsOnboardingAndTOTPBehavior(t *testing.T) {
	repo := &sub2AuthRepoStub{
		user:       &domainuser.User{ID: 42, Username: "alice", DisplayName: "Alice", Role: domainuser.RoleUser, Status: domainuser.StatusActive},
		credential: &domainuser.Credential{UserID: 42, PasswordEnabled: true},
		twoFactor:  &domainuser.UserTwoFactor{UserID: 42, TOTPEnabled: true, TOTPSecretEncrypted: "legacy-secret"},
	}
	service := newTestService(config.Config{JWTSecret: "local-jwt-secret", DataEncryptionKey: sub2TestDataKey}, repo, nil)
	view, err := service.BuildUserView(context.Background(), *repo.user)
	if err != nil {
		t.Fatalf("build local user view: %v", err)
	}
	if !view.InitialSecurityRequired {
		t.Fatal("local mode must keep onboarding required for an incomplete user")
	}
	status, err := service.GetCurrentTwoFactorStatus(context.Background(), repo.user.ID)
	if err != nil {
		t.Fatalf("get local TOTP status: %v", err)
	}
	if status == nil || !status.Available || !status.TOTPEnabled {
		t.Fatalf("local mode must retain TOTP status, got %#v", status)
	}
}

var _ sub2Provider = (*sub2ProviderStub)(nil)
var _ repository.Sub2IdentityRepository = (*sub2IdentityRepoStub)(nil)
