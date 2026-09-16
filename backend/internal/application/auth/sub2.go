package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	userapp "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/user"
	domainsub2 "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/sub2"
	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/conv"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/secretbox"
	sub2port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/apperr"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/requestmeta"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	sub2ProviderSlug       = "sub2"
	sub2AuthorizationTTL   = 5 * time.Minute
	sub2ApplicationPurpose = "chat.login"
)

var (
	// ErrSub2IntegrationDisabled means the explicit local mode is active.
	ErrSub2IntegrationDisabled = apperr.New("sub2.integration_disabled", "Sub2 integration is disabled")
	// ErrSub2IntegrationNotReady prevents an incomplete integration from falling back to local auth.
	ErrSub2IntegrationNotReady = apperr.New("sub2.integration_not_ready", "Sub2 integration is not ready")
	// ErrSub2AuthorizationDenied masks provider-specific authorization details.
	ErrSub2AuthorizationDenied = apperr.NewMasked("sub2.authorization_denied", "Sub2 authorization failed", "Sub2 authorization was denied")
	// ErrSub2AuthorizationTransactionInvalid is returned for missing, expired or replayed server state.
	ErrSub2AuthorizationTransactionInvalid = apperr.NewMasked("sub2.authorization_state_invalid", "invalid Sub2 authorization state", "Sub2 authorization transaction is invalid, expired, or already used")
	// ErrSub2AuthorizationBrowserBindingMismatch preserves a legitimate transaction
	// when a callback comes from the wrong or missing browser cookie. It uses the
	// same public error code as other invalid-state failures.
	ErrSub2AuthorizationBrowserBindingMismatch = apperr.NewMasked("sub2.authorization_state_invalid", "invalid Sub2 authorization state", "Sub2 browser binding does not match authorization state")
	// ErrSub2IDTokenInvalid masks cryptographic/provider validation details at the API boundary.
	ErrSub2IDTokenInvalid = apperr.NewMasked("sub2.id_token_invalid", "invalid Sub2 identity token", "Sub2 ID token validation failed")
	// ErrSub2IdentityNotLinked means no trusted Chat binding exists for the user.
	ErrSub2IdentityNotLinked = apperr.New("sub2.identity_not_linked", "Sub2 identity is not linked")
	// ErrSub2IdentityRevoked indicates the authority has disabled the external identity.
	ErrSub2IdentityRevoked = apperr.New("sub2.identity_revoked", "Sub2 identity is no longer active")
	// ErrSub2IdentityEpochMismatch invalidates stale sessions and credentials.
	ErrSub2IdentityEpochMismatch = apperr.New("sub2.identity_epoch_mismatch", "Sub2 identity must be reauthenticated")
	// ErrSub2IdentityStatusUnavailable is fail-closed and safe to expose as 503.
	ErrSub2IdentityStatusUnavailable = apperr.NewMasked("sub2.identity_unavailable", "Sub2 identity authority is unavailable", "Sub2 identity status check failed")
)

// sub2Provider is the narrow application dependency used by the auth flow.
type sub2Provider interface {
	sub2port.Client
	sub2port.OIDCProvider
}

// Sub2LoginStartResult contains the redirect URL and the short-lived browser
// binding secret that the HTTP layer stores in an HttpOnly cookie.
type Sub2LoginStartResult struct {
	AuthorizationURL    string
	ExpiresAt           time.Time
	BrowserBindingToken string
}

// Sub2LoginCallbackInput contains the browser callback and audit context.
type Sub2LoginCallbackInput struct {
	Code                string
	State               string
	ProviderError       string
	BrowserBindingToken string
	RequestID           string
	AuditContext        requestmeta.SessionAuditContext
}

// SetSub2Integration wires the server-side Sub2 authority and Chat binding store.
// Passing nil leaves the integration unavailable; local mode is not changed implicitly.
func (s *Service) SetSub2Integration(provider sub2Provider, identities repository.Sub2IdentityRepository) {
	if s == nil {
		return
	}
	s.sub2Provider = provider
	s.sub2IdentityRepo = identities
}

// StartSub2Login creates a one-time server-side OIDC transaction and redirects to Sub2.
func (s *Service) StartSub2Login(ctx context.Context, nextPath string) (*Sub2LoginStartResult, error) {
	if !s.sub2Enabled() {
		return nil, ErrSub2IntegrationDisabled
	}
	if s.sub2Provider == nil || s.providerAuthBridge == nil {
		return nil, ErrSub2IntegrationNotReady
	}
	verifier, err := randomURLToken(32)
	if err != nil {
		return nil, err
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return nil, err
	}
	state, err := randomURLToken(32)
	if err != nil {
		return nil, err
	}
	challenge := providerCodeChallenge(verifier)
	browserBindingToken, err := randomURLToken(32)
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(sub2AuthorizationTTL)
	transaction := repository.ProviderAuthTransaction{
		ProviderSlug:         sub2ProviderSlug,
		ClientID:             s.cfg.Snapshot().Sub2OIDCClientID,
		ClientRedirectURI:    s.cfg.Snapshot().Sub2OIDCRedirectURI,
		ProviderCodeVerifier: verifier,
		ProviderNonce:        nonce,
		ProviderIssuer:       s.cfg.Snapshot().Sub2OIDCIssuer,
		BrowserBindingHash:   hashToken(browserBindingToken),
		Intent:               providerIntentLogin,
		Next:                 normalizeProviderNextPath(nextPath),
		ExpiresAt:            expiresAt,
	}
	if err := s.providerAuthBridge.PutProviderAuthTransaction(ctx, state, transaction, sub2AuthorizationTTL); err != nil {
		return nil, fmt.Errorf("store Sub2 authorization transaction: %w", err)
	}
	authorizationURL, err := s.sub2Provider.AuthorizationURL(state, nonce, challenge)
	if err != nil {
		return nil, fmt.Errorf("build Sub2 authorization URL: %w", err)
	}
	return &Sub2LoginStartResult{AuthorizationURL: authorizationURL, ExpiresAt: expiresAt, BrowserBindingToken: browserBindingToken}, nil
}

// sub2AuthorizationTransactionConsumedError marks failures that happen after
// the matching browser has atomically consumed the one-time transaction. The
// wrapped application error remains unchanged for errors.Is and HTTP mapping.
type sub2AuthorizationTransactionConsumedError struct {
	err error
}

func (e *sub2AuthorizationTransactionConsumedError) Error() string {
	if e == nil || e.err == nil {
		return "Sub2 authorization transaction consumed"
	}
	return e.err.Error()
}

func (e *sub2AuthorizationTransactionConsumedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// IsSub2AuthorizationTransactionConsumed reports whether a callback consumed
// its matching one-time transaction before the operation failed. HTTP handlers
// use this to clear only a cookie whose transaction is no longer usable.
func IsSub2AuthorizationTransactionConsumed(err error) bool {
	var target *sub2AuthorizationTransactionConsumedError
	return errors.As(err, &target)
}

// CompleteSub2Login atomically consumes the browser-bound one-time state after
// matching its binding, validates the ES256 ID token, exchanges the subject
// server-side, and issues the normal Chat application session. Any failure
// after consumption is marked so the HTTP layer can clear the spent cookie;
// an unmatched browser never reaches the provider exchange or cookie clear.
func (s *Service) CompleteSub2Login(ctx context.Context, input Sub2LoginCallbackInput) (result *LoginResult, err error) {
	consumed := false
	defer func() {
		if consumed && err != nil {
			err = &sub2AuthorizationTransactionConsumedError{err: err}
		}
	}()
	if !s.sub2Enabled() {
		return nil, ErrSub2IntegrationDisabled
	}
	if s.sub2Provider == nil || s.providerAuthBridge == nil || s.sub2IdentityRepo == nil {
		return nil, ErrSub2IntegrationNotReady
	}
	state := strings.TrimSpace(input.State)
	if state == "" {
		return nil, ErrSub2AuthorizationTransactionInvalid
	}
	browserBindingHash := ""
	if strings.TrimSpace(input.BrowserBindingToken) != "" {
		browserBindingHash = hashToken(input.BrowserBindingToken)
	}
	transaction, consumeErr := s.providerAuthBridge.ConsumeProviderAuthTransactionIfBrowserBindingMatches(ctx, state, browserBindingHash)
	if errors.Is(consumeErr, repository.ErrProviderAuthTransactionBindingMismatch) {
		// Keep the historical invalid-state errors.Is contract while exposing a
		// distinct sentinel to the HTTP layer for cookie-preservation semantics.
		return nil, fmt.Errorf("%w: %w", ErrSub2AuthorizationBrowserBindingMismatch, ErrSub2AuthorizationTransactionInvalid)
	}
	if consumeErr != nil || transaction == nil {
		return nil, ErrSub2AuthorizationTransactionInvalid
	}
	consumed = true
	cfg := s.cfg.Snapshot()
	if transaction.ProviderSlug != sub2ProviderSlug ||
		transaction.ClientID != cfg.Sub2OIDCClientID ||
		transaction.ClientRedirectURI != cfg.Sub2OIDCRedirectURI ||
		transaction.ProviderIssuer != cfg.Sub2OIDCIssuer ||
		transaction.ExpiresAt.Before(time.Now()) {
		return nil, ErrSub2AuthorizationTransactionInvalid
	}
	if len(transaction.ProviderCodeVerifier) < 43 || len(transaction.ProviderCodeVerifier) > 128 || transaction.ProviderNonce == "" {
		return nil, ErrSub2AuthorizationTransactionInvalid
	}
	if strings.TrimSpace(input.ProviderError) != "" {
		return nil, ErrSub2AuthorizationDenied
	}
	if strings.TrimSpace(input.Code) == "" {
		return nil, ErrSub2AuthorizationTransactionInvalid
	}
	tokenSet, err := s.sub2Provider.ExchangeAuthorizationCode(ctx, input.Code, transaction.ClientRedirectURI, transaction.ProviderCodeVerifier)
	if err != nil {
		return nil, fmt.Errorf("%w: exchange authorization code: %v", ErrSub2IDTokenInvalid, err)
	}
	if tokenSet == nil || strings.TrimSpace(tokenSet.IDToken) == "" {
		return nil, fmt.Errorf("%w: authorization response did not contain an ID token", ErrSub2IDTokenInvalid)
	}
	claims, err := s.sub2Provider.ValidateIDToken(ctx, tokenSet.IDToken, transaction.ProviderNonce)
	if err != nil || claims == nil {
		if err == nil {
			err = errors.New("validated ID token claims are empty")
		}
		return nil, fmt.Errorf("%w: %v", ErrSub2IDTokenInvalid, err)
	}
	identity := sub2port.IdentityRef{Issuer: claims.Issuer, Subject: claims.Subject}
	exchange, err := s.sub2Provider.ExchangeSubject(ctx, sub2port.SubjectExchangeRequest{
		Identity:        identity,
		SubjectToken:    tokenSet.IDToken,
		Audience:        cfg.Sub2OIDCClientID,
		Application:     cfg.Sub2Application,
		Purpose:         sub2ApplicationPurpose,
		Nonce:           claims.Nonce,
		RequestedScopes: []string{"identity.status"},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: subject exchange: %v", ErrSub2IdentityStatusUnavailable, err)
	}
	if exchange == nil || exchange.Identity.Issuer != identity.Issuer || exchange.Identity.Subject != identity.Subject ||
		exchange.Identity.IdentityEpoch == 0 || strings.TrimSpace(exchange.Identity.ExternalUserID) == "" ||
		strings.TrimSpace(exchange.Assertion) == "" || exchange.ExpiresAt.Before(time.Now()) ||
		!exchange.PermissionVersionPresent || !domainsub2.IsPermissionVersionValid(exchange.PermissionVersion) {
		return nil, fmt.Errorf("%w: subject exchange response is incomplete", ErrSub2IDTokenInvalid)
	}
	if _, ok := domainsub2.MapExternalRole(exchange.Role); !ok {
		return nil, fmt.Errorf("%w: subject exchange role is missing or unknown", ErrSub2IDTokenInvalid)
	}
	identity = exchange.Identity
	identity.SubjectAssertion = exchange.Assertion
	// ExpectedIdentityEpoch is a stale-session assertion carried to the
	// authority. Sub2 validates the opaque assertion against its current
	// user/role/epoch state and may ignore this hint; Chat still compares the
	// authoritative response epoch below and forces re-SSO on any change.
	status, err := s.sub2Provider.GetIdentityStatus(ctx, sub2port.IdentityStatusRequest{
		Identity:              identity,
		Application:           cfg.Sub2Application,
		ExpectedIdentityEpoch: exchange.Identity.IdentityEpoch,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSub2IdentityStatusUnavailable, err)
	}
	if status == nil || !status.Active || status.Status != domainsub2.IdentityStatusActive {
		return nil, ErrSub2IdentityRevoked
	}
	if status.Identity.Issuer != identity.Issuer || status.Identity.Subject != identity.Subject ||
		(strings.TrimSpace(status.Identity.ExternalUserID) != "" && status.Identity.ExternalUserID != identity.ExternalUserID) {
		return nil, fmt.Errorf("%w: identity status subject does not match the verified ID token", ErrSub2IDTokenInvalid)
	}
	if status.IdentityEpoch != exchange.Identity.IdentityEpoch || status.Role != exchange.Role || status.PermissionVersion != exchange.PermissionVersion {
		return nil, ErrSub2IdentityEpochMismatch
	}
	if _, ok := domainsub2.MapExternalRole(status.Role); !ok || !status.PermissionVersionPresent || !domainsub2.IsPermissionVersionValid(status.PermissionVersion) {
		return nil, fmt.Errorf("%w: status role is missing or unknown", ErrSub2IdentityStatusUnavailable)
	}
	userItem, err := s.resolveSub2User(ctx, claims, identity, status, exchange)
	if err != nil {
		return nil, err
	}
	result, err = s.completeSub2LoginForUser(ctx, userItem, claims.Subject, input.RequestID, input.AuditContext)
	if err != nil {
		return nil, err
	}
	result.RedirectPath = transaction.Next
	return result, nil
}

// completeSub2LoginForUser issues a Chat session after Sub2 has completed the
// authoritative authentication and identity checks. A legacy Chat TOTP record
// is intentionally not consulted here: Sub2's authentication, including its
// own MFA policy, is authoritative for this login path.
func (s *Service) completeSub2LoginForUser(
	ctx context.Context,
	userItem *domainuser.User,
	subject string,
	requestID string,
	auditCtx requestmeta.SessionAuditContext,
) (*LoginResult, error) {
	if err := ensureProviderLoginUserActive(userItem); err != nil {
		return nil, err
	}
	normalizedAuditCtx := s.resolveSessionAuditContext(ctx, auditCtx)
	result, err := s.issueLoginResult(ctx, userItem, normalizedAuditCtx, time.Now())
	if err != nil {
		return nil, err
	}
	s.RecordAuthEvent(
		ctx,
		repository.AuthEventInput{
			UserID:     result.User.ID,
			RequestID:  requestID,
			EventType:  "sub2_login",
			Result:     "success",
			ClientIP:   normalizedAuditCtx.ClientIP,
			UserAgent:  normalizedAuditCtx.UserAgent,
			DetailJSON: marshalAuthEventDetail(map[string]any{"subject": subject, "session_id": result.SessionID}),
		},
	)
	return result, nil
}

// Sub2FrontendRedirect returns a same-origin-safe frontend path for the browser
// callback. No access or refresh token is ever placed in the URL.
func (s *Service) Sub2FrontendRedirect(nextPath string) string {
	target := normalizeProviderNextPath(nextPath)
	base := strings.TrimRight(strings.TrimSpace(s.cfg.Snapshot().PublicWebBaseURL), "/")
	if base == "" {
		return target
	}
	return base + target
}

// CheckExternalIdentity keeps the existing error-only signature while
// revalidating the authority, synchronizing role/version/status atomically, and
// revoking local sessions when a bound identity loses authority.
func (s *Service) CheckExternalIdentity(ctx context.Context, userID uint) error {
	if !s.sub2Enabled() {
		return nil
	}
	if s.sub2Provider == nil || s.sub2IdentityRepo == nil {
		return ErrSub2IntegrationNotReady
	}
	binding, err := s.sub2IdentityRepo.GetSub2IdentityBindingByUserID(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return s.failClosedSub2Identity(ctx, userID, "sub2_identity_not_linked", ErrSub2IdentityNotLinked)
	}
	if err != nil {
		return fmt.Errorf("%w: load identity binding: %v", ErrSub2IdentityStatusUnavailable, err)
	}
	if binding == nil || binding.Status != domainsub2.IdentityStatusActive || binding.IdentityEpoch == 0 ||
		!domainsub2.IsPermissionVersionValid(binding.PermissionVersion) || strings.TrimSpace(binding.Role) == "" {
		return s.failClosedSub2Identity(ctx, userID, "sub2_identity_binding_invalid", ErrSub2IdentityRevoked)
	}
	assertion, err := s.decryptSub2Assertion(binding)
	if err != nil {
		return s.failClosedSub2Identity(ctx, userID, "sub2_subject_assertion_invalid", err)
	}
	userItem, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("%w: load Chat user: %v", ErrSub2IdentityStatusUnavailable, err)
	}
	status, err := s.sub2Provider.GetIdentityStatus(ctx, sub2port.IdentityStatusRequest{
		Identity: sub2port.IdentityRef{
			Issuer:           binding.Issuer,
			Subject:          binding.Subject,
			ExternalUserID:   binding.ExternalUserID,
			IdentityEpoch:    binding.IdentityEpoch,
			SubjectAssertion: assertion,
		},
		Application:           s.cfg.Snapshot().Sub2Application,
		ExpectedIdentityEpoch: binding.IdentityEpoch,
	})
	if err != nil {
		return s.failClosedSub2Identity(ctx, userID, "sub2_identity_status_unavailable", fmt.Errorf("%w: %v", ErrSub2IdentityStatusUnavailable, err))
	}
	if status == nil || status.IdentityEpoch == 0 || !status.PermissionVersionPresent || !domainsub2.IsPermissionVersionValid(status.PermissionVersion) ||
		status.Identity.Issuer != binding.Issuer || status.Identity.Subject != binding.Subject ||
		(strings.TrimSpace(status.Identity.ExternalUserID) != "" && status.Identity.ExternalUserID != binding.ExternalUserID) {
		return s.failClosedSub2Identity(ctx, userID, "sub2_identity_status_invalid", fmt.Errorf("%w: incomplete or mismatched identity status response", ErrSub2IdentityStatusUnavailable))
	}
	chatRole, localStatus, err := sub2LocalRoleAndStatus(status)
	if err != nil {
		return s.failClosedSub2Identity(ctx, userID, "sub2_identity_role_invalid", err)
	}
	// A response older than the persisted permission/identity snapshot is not
	// allowed to change the local account or cause a false downgrade.
	staleResponse := status.IdentityEpoch < binding.IdentityEpoch ||
		(status.IdentityEpoch == binding.IdentityEpoch && status.PermissionVersion < binding.PermissionVersion)
	now := time.Now()
	incoming := *binding
	incoming.ExternalUserID = firstNonEmpty(strings.TrimSpace(status.Identity.ExternalUserID), binding.ExternalUserID)
	incoming.IdentityEpoch = status.IdentityEpoch
	incoming.Status = strings.TrimSpace(status.Status)
	incoming.Role = strings.TrimSpace(status.Role)
	incoming.PermissionVersion = status.PermissionVersion
	incoming.LastCheckedAt = &now
	if status.Active && status.AssertionExpiresAt.After(now) {
		incoming.SubjectAssertionExpiresAt = &status.AssertionExpiresAt
	}
	if syncErr := s.sub2IdentityRepo.SyncSub2UserAndIdentity(ctx, repository.SyncSub2UserAndIdentityInput{
		Identity:   &incoming,
		UserRole:   chatRole,
		UserStatus: localStatus,
	}); syncErr != nil {
		if errors.Is(syncErr, repository.ErrConflict) {
			return ErrSub2IdentityRevoked
		}
		return s.failClosedSub2Identity(ctx, userID, "sub2_identity_sync_failed", fmt.Errorf("%w: synchronize identity binding: %v", ErrSub2IdentityStatusUnavailable, syncErr))
	}
	// Re-read after the locked transaction so an older response cannot make a
	// request pass after a concurrent newer downgrade has already committed.
	latestBinding, latestErr := s.sub2IdentityRepo.GetSub2IdentityBindingByUserID(ctx, userID)
	if latestErr != nil || latestBinding == nil {
		if latestErr == nil {
			latestErr = repository.ErrNotFound
		}
		return s.failClosedSub2Identity(ctx, userID, "sub2_identity_snapshot_reread_failed", fmt.Errorf("%w: reread identity binding: %v", ErrSub2IdentityStatusUnavailable, latestErr))
	}
	latestUser, latestErr := s.repo.GetByID(ctx, userID)
	if latestErr != nil || latestUser == nil {
		if latestErr == nil {
			latestErr = repository.ErrNotFound
		}
		return s.failClosedSub2Identity(ctx, userID, "sub2_user_snapshot_reread_failed", fmt.Errorf("%w: reread Chat user: %v", ErrSub2IdentityStatusUnavailable, latestErr))
	}
	identityEpochChanged := status.IdentityEpoch > binding.IdentityEpoch
	if identityEpochChanged || latestBinding.IdentityEpoch > binding.IdentityEpoch {
		if !status.Active || status.Status != domainsub2.IdentityStatusActive ||
			latestBinding.Status != domainsub2.IdentityStatusActive || latestUser.Status != domainuser.StatusActive {
			return ErrSub2IdentityRevoked
		}
		return ErrSub2IdentityEpochMismatch
	}
	if latestBinding.IdentityEpoch > status.IdentityEpoch ||
		(latestBinding.IdentityEpoch == status.IdentityEpoch && latestBinding.PermissionVersion > status.PermissionVersion) || staleResponse {
		if latestBinding.Status != domainsub2.IdentityStatusActive || latestUser.Status != domainuser.StatusActive {
			return ErrSub2IdentityRevoked
		}
		latestRole, ok := domainsub2.MapExternalRole(latestBinding.Role)
		if !ok {
			return s.failClosedSub2Identity(ctx, userID, "sub2_identity_snapshot_role_invalid", ErrSub2IdentityRevoked)
		}
		if domainsub2.IsRoleDowngrade(userItem.Role, latestRole) {
			return ErrSub2IdentityRevoked
		}
		return nil
	}
	if !status.Active || status.Status != domainsub2.IdentityStatusActive {
		return ErrSub2IdentityRevoked
	}
	if domainsub2.IsRoleDowngrade(userItem.Role, chatRole) {
		return ErrSub2IdentityRevoked
	}
	return nil
}

// IsSub2IdentityBound reports whether a user is managed by the dedicated Sub2 binding.
func (s *Service) IsSub2IdentityBound(ctx context.Context, userID uint) (bool, error) {
	if !s.sub2Enabled() || s.sub2IdentityRepo == nil || userID == 0 {
		return false, nil
	}
	_, err := s.sub2IdentityRepo.GetSub2IdentityBindingByUserID(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) failClosedSub2Identity(ctx context.Context, userID uint, reason string, baseErr error) error {
	if s.repo == nil || userID == 0 {
		return baseErr
	}
	if err := s.repo.RevokeAllSessions(ctx, userID, reason); err != nil {
		return fmt.Errorf("%w: revoke local sessions after Sub2 identity failure: %v", ErrSub2IdentityStatusUnavailable, err)
	}
	return baseErr
}

func (s *Service) resolveSub2User(ctx context.Context, claims *sub2port.IDTokenClaims, identity sub2port.IdentityRef, status *sub2port.IdentityStatusResponse, exchange *sub2port.SubjectExchangeResponse) (*domainuser.User, error) {
	if claims == nil || status == nil || exchange == nil {
		return nil, ErrSub2IDTokenInvalid
	}
	chatRole, userStatus, err := sub2LocalRoleAndStatus(status)
	if err != nil || userStatus != domainuser.StatusActive {
		return nil, ErrSub2IdentityRevoked
	}
	encryptedAssertion, err := s.encryptSub2Assertion(exchange.Assertion)
	if err != nil {
		return nil, err
	}
	binding, err := s.sub2IdentityRepo.GetSub2IdentityBinding(ctx, identity.Issuer, identity.Subject)
	if err == nil && binding != nil {
		now := time.Now()
		normalizedEmail, emailErr := normalizeProviderEmail(claims.Email)
		if emailErr != nil {
			return nil, emailErr
		}
		if normalizedEmail == "" {
			normalizedEmail = binding.Email
		}
		incoming := &domainsub2.IdentityBinding{
			ID:                        binding.ID,
			UserID:                    binding.UserID,
			Issuer:                    identity.Issuer,
			Subject:                   identity.Subject,
			ExternalUserID:            identity.ExternalUserID,
			IdentityEpoch:             status.IdentityEpoch,
			Status:                    status.Status,
			Role:                      status.Role,
			PermissionVersion:         status.PermissionVersion,
			SubjectAssertionEncrypted: encryptedAssertion,
			SubjectAssertionExpiresAt: &exchange.ExpiresAt,
			Email:                     normalizedEmail,
			EmailVerified:             claims.EmailVerified,
			LastCheckedAt:             &now,
			LastLoginAt:               &now,
		}
		if syncErr := s.sub2IdentityRepo.SyncSub2UserAndIdentity(ctx, repository.SyncSub2UserAndIdentityInput{
			Identity:   incoming,
			UserRole:   chatRole,
			UserStatus: userStatus,
		}); syncErr != nil {
			return nil, syncErr
		}
		userItem, userErr := s.repo.GetByID(ctx, binding.UserID)
		if userErr != nil {
			return nil, userErr
		}
		if activeErr := ensureProviderLoginUserActive(userItem); activeErr != nil {
			return nil, activeErr
		}
		userItem, err = s.ensureSub2OnboardingCompleted(ctx, userItem)
		if err != nil {
			return nil, err
		}
		return userItem, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	now := time.Now()
	normalizedEmail, err := normalizeProviderEmail(claims.Email)
	if err != nil {
		return nil, err
	}
	emailVerifiedAt := (*time.Time)(nil)
	emailSource := domainuser.EmailSourceProviderUnverified
	if claims.EmailVerified && normalizedEmail != "" {
		emailVerifiedAt = &now
		emailSource = domainuser.EmailSourceProviderVerified
	}
	userItem := &domainuser.User{
		PublicID:              conv.NormalizePublicID(uuid.NewString()),
		Username:              sub2GeneratedUsername(claims, identity),
		DisplayName:           userapp.NormalizeGeneratedDisplayName(firstNonEmpty(claims.Name, claims.PreferredUsername, normalizedEmail, "Sub2 user")),
		AvatarURL:             strings.TrimSpace(claims.Picture),
		Email:                 normalizedEmail,
		EmailSource:           emailSource,
		EmailVerifiedAt:       emailVerifiedAt,
		Role:                  chatRole,
		Status:                userStatus,
		Timezone:              "Etc/UTC",
		Locale:                "en-US",
		OnboardingCompletedAt: &now,
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(uuid.NewString()), passwordHashCost)
	if err != nil {
		return nil, err
	}
	binding = &domainsub2.IdentityBinding{
		UserID:                    0,
		Issuer:                    identity.Issuer,
		Subject:                   identity.Subject,
		ExternalUserID:            identity.ExternalUserID,
		IdentityEpoch:             status.IdentityEpoch,
		Status:                    status.Status,
		Role:                      status.Role,
		PermissionVersion:         status.PermissionVersion,
		SubjectAssertionEncrypted: encryptedAssertion,
		SubjectAssertionExpiresAt: &exchange.ExpiresAt,
		Email:                     normalizedEmail,
		EmailVerified:             claims.EmailVerified,
		LastCheckedAt:             &now,
		LastLoginAt:               &now,
	}
	for attempt := 0; attempt < 20; attempt++ {
		userItem.ID = 0
		userItem.Username = generatedUsernameWithSuffix(sub2GeneratedUsername(claims, identity), attempt)
		err = s.sub2IdentityRepo.CreateSub2UserWithIdentity(ctx, repository.CreateSub2UserWithIdentityInput{
			CreateWithCredentialInput: repository.CreateWithCredentialInput{
				User: userItem,
				Credential: domainuser.Credential{
					PasswordHash:      string(passwordHash),
					PasswordAlgo:      "bcrypt",
					PasswordEnabled:   false,
					PasswordUpdatedAt: &now,
					PasswordOrigin:    domainuser.PasswordOriginSSOPlaceholder,
				},
			},
			Identity: binding,
		})
		if errors.Is(err, repository.ErrDuplicateUsername) {
			continue
		}
		if errors.Is(err, repository.ErrDuplicate) {
			if existing, findErr := s.sub2IdentityRepo.GetSub2IdentityBinding(ctx, identity.Issuer, identity.Subject); findErr == nil && existing != nil {
				return s.resolveSub2User(ctx, claims, identity, status, exchange)
			}
		}
		if err != nil {
			return nil, err
		}
		return userItem, nil
	}
	return nil, ErrUsernameTaken
}

func (s *Service) ensureSub2OnboardingCompleted(ctx context.Context, item *domainuser.User) (*domainuser.User, error) {
	if item == nil || item.OnboardingCompletedAt != nil {
		return item, nil
	}
	now := time.Now()
	completedAt := &now
	updated, err := s.repo.UpdateProfile(ctx, item.ID, repository.UpdateUserFieldsInput{
		OnboardingCompletedAt: &completedAt,
	})
	if err != nil {
		return nil, err
	}
	if updated != nil {
		if updated.OnboardingCompletedAt == nil {
			updated.OnboardingCompletedAt = &now
		}
		return updated, nil
	}
	item.OnboardingCompletedAt = &now
	return item, nil
}

func sub2LocalRoleAndStatus(status *sub2port.IdentityStatusResponse) (string, string, error) {
	if status == nil || strings.TrimSpace(status.Status) == "" || !status.PermissionVersionPresent || !domainsub2.IsPermissionVersionValid(status.PermissionVersion) {
		return "", "", fmt.Errorf("%w: Sub2 role or permission version is missing", ErrSub2IdentityStatusUnavailable)
	}
	chatRole, ok := domainsub2.MapExternalRole(status.Role)
	if !ok {
		return "", "", fmt.Errorf("%w: Sub2 role is unknown", ErrSub2IdentityStatusUnavailable)
	}
	if status.Active && strings.TrimSpace(status.Status) == domainsub2.IdentityStatusActive {
		return chatRole, domainuser.StatusActive, nil
	}
	return domainuser.RoleUser, domainuser.StatusSuspended, nil
}

func (s *Service) encryptSub2Assertion(assertion string) (string, error) {
	if strings.TrimSpace(assertion) == "" {
		return "", fmt.Errorf("%w: subject exchange did not return a binding proof", ErrSub2IdentityStatusUnavailable)
	}
	key := strings.TrimSpace(s.cfg.Snapshot().DataEncryptionKey)
	if key == "" {
		return "", fmt.Errorf("%w: Chat data encryption key is unavailable", ErrSub2IdentityStatusUnavailable)
	}
	encrypted, err := secretbox.EncryptString(key, assertion)
	if err != nil {
		return "", fmt.Errorf("%w: encrypt subject binding proof", ErrSub2IdentityStatusUnavailable)
	}
	return encrypted, nil
}

func (s *Service) decryptSub2Assertion(binding *domainsub2.IdentityBinding) (string, error) {
	if binding == nil || binding.SubjectAssertionExpiresAt == nil {
		return "", ErrSub2IdentityEpochMismatch
	}
	key := strings.TrimSpace(s.cfg.Snapshot().DataEncryptionKey)
	if key == "" || strings.TrimSpace(binding.SubjectAssertionEncrypted) == "" {
		return "", ErrSub2IdentityEpochMismatch
	}
	assertion, err := secretbox.DecryptString(key, binding.SubjectAssertionEncrypted)
	if err != nil || strings.TrimSpace(assertion) == "" {
		return "", ErrSub2IdentityEpochMismatch
	}
	return assertion, nil
}

func (s *Service) sub2Enabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Snapshot().UsesSub2Authority()
}

func randomURLToken(size int) (string, error) {
	if size <= 0 {
		return "", errors.New("random token size must be positive")
	}
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func matchesSub2BrowserBinding(expectedHash string, token string) bool {
	expectedHash = strings.TrimSpace(expectedHash)
	token = strings.TrimSpace(token)
	if expectedHash == "" || token == "" {
		return false
	}
	actualHash := hashToken(token)
	return subtle.ConstantTimeCompare([]byte(expectedHash), []byte(actualHash)) == 1
}

func sub2GeneratedUsername(claims *sub2port.IDTokenClaims, identity sub2port.IdentityRef) string {
	if claims != nil {
		if candidate, err := userapp.NormalizeUsername(claims.PreferredUsername); err == nil {
			return candidate
		}
	}
	return providerUsername(sub2ProviderSlug, identity.Issuer+":"+identity.Subject)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "user"
}

// ResolveSub2Identity returns a fresh server-only proof for an authenticated
// Chat user. The encrypted binding is the only source of external identity.
func (s *Service) ResolveSub2Identity(ctx context.Context, userID uint) (sub2port.IdentityRef, error) {
	if err := s.CheckExternalIdentity(ctx, userID); err != nil {
		return sub2port.IdentityRef{}, err
	}
	binding, err := s.sub2IdentityRepo.GetSub2IdentityBindingByUserID(ctx, userID)
	if err != nil {
		return sub2port.IdentityRef{}, err
	}
	proof, err := s.decryptSub2Assertion(binding)
	if err != nil {
		return sub2port.IdentityRef{}, err
	}
	if binding.SubjectAssertionExpiresAt == nil || !time.Now().Before(*binding.SubjectAssertionExpiresAt) {
		return sub2port.IdentityRef{}, ErrSub2IdentityRevoked
	}
	return sub2port.IdentityRef{Issuer: binding.Issuer, Subject: binding.Subject, ExternalUserID: binding.ExternalUserID, IdentityEpoch: binding.IdentityEpoch, SubjectAssertion: proof}, nil
}

// UsesSub2Authority exposes the immutable authentication authority to safety guards.
func (s *Service) UsesSub2Authority() bool { return s.sub2Enabled() }
