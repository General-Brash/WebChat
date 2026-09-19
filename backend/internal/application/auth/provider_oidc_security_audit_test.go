package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/secretbox"
	idpport "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/identityprovider"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestProviderOIDCSecurityAuditValidatesSignedIDTokenNonceAndClaims(t *testing.T) {
	privateKey, publicKey := newOIDCAuditRSAKey(t)
	const (
		issuer = "https://idp.example.com"
		client = "oidc-client"
		nonce  = "nonce-audit-1"
	)
	service := newTestService(config.Config{JWTSecret: "test-secret", DataEncryptionKey: "test-data-key"}, nil, nil)
	service.providerHTTPClient = &oidcAuditProviderClient{jwksBody: oidcAuditJWKS(t, publicKey)}
	provider := oidcAuditProvider(service)
	resolution := providerEndpointResolution{
		Issuer:  issuer,
		JWKSURL: "https://idp.example.com/jwks",
	}
	idToken := oidcAuditIDToken(t, privateKey, issuer, client, "subject-audit", nonce, "audit-key")

	claims, err := service.validateOIDCIdentityToken(context.Background(), provider, resolution, idToken, nonce)
	if err != nil {
		t.Fatalf("validate OIDC ID token: %v", err)
	}
	if got := claimString(claims, "sub"); got != "subject-audit" {
		t.Fatalf("expected validated subject, got %q", got)
	}
}

func TestProviderOIDCSecurityAuditRejectsMissingOrMismatchedNonce(t *testing.T) {
	privateKey, publicKey := newOIDCAuditRSAKey(t)
	service := newTestService(config.Config{JWTSecret: "test-secret", DataEncryptionKey: "test-data-key"}, nil, nil)
	service.providerHTTPClient = &oidcAuditProviderClient{jwksBody: oidcAuditJWKS(t, publicKey)}
	provider := oidcAuditProvider(service)
	resolution := providerEndpointResolution{
		Issuer:  "https://idp.example.com",
		JWKSURL: "https://idp.example.com/jwks",
	}
	idToken := oidcAuditIDToken(t, privateKey, resolution.Issuer, provider.ClientID, "subject-audit", "nonce-from-idp", "audit-key")

	for name, rawIDToken := range map[string]string{
		"missing id token": "",
		"nonce mismatch":   idToken,
	} {
		t.Run(name, func(t *testing.T) {
			expectedNonce := "nonce-from-idp"
			if name == "nonce mismatch" {
				expectedNonce = "nonce-from-callback"
			}
			_, err := service.validateOIDCIdentityToken(context.Background(), provider, resolution, rawIDToken, expectedNonce)
			if !errors.Is(err, ErrProviderAuthenticationFailed) {
				t.Fatalf("expected classified provider authentication failure, got %v", err)
			}
		})
	}
}

func TestProviderOIDCSecurityAuditRejectsUserInfoSubjectSubstitution(t *testing.T) {
	privateKey, publicKey := newOIDCAuditRSAKey(t)
	const nonce = "nonce-subject-check"
	client := &oidcAuditProviderClient{
		discoveryBody: []byte(`{"issuer":"https://idp.example.com","jwks_uri":"https://idp.example.com/jwks"}`),
		jwksBody:      oidcAuditJWKS(t, publicKey),
		tokenBody: func() []byte {
			return []byte(`{"access_token":"access-token","token_type":"Bearer","id_token":"` + oidcAuditIDToken(t, privateKey, "https://idp.example.com", "oidc-client", "id-token-subject", nonce, "audit-key") + `"}`)
		}(),
		userInfoBody: []byte(`{"sub":"different-userinfo-subject","email":"user@example.com"}`),
	}
	service := newTestService(config.Config{DataEncryptionKey: "test-data-key"}, nil, nil)
	service.providerHTTPClient = client
	provider := oidcAuditProvider(service)
	provider.JWKSURL = ""

	_, _, err := service.resolveProviderLoginCodeWithNonce(
		context.Background(),
		provider,
		"authorization-code",
		"https://chat.example.com/auth/callback?provider=acme",
		strings.Repeat("v", 43),
		nonce,
	)
	if !errors.Is(err, ErrProviderAuthenticationFailed) {
		t.Fatalf("expected ID-token/userinfo subject mismatch to be classified, got %v", err)
	}
}

func TestProviderOIDCSecurityAuditClassifiesAndRedactsProviderErrors(t *testing.T) {
	const (
		clientSecret = "client-secret-audit"
		accessToken  = "access-token-audit"
		authCode     = "authorization-code-audit"
		nonce        = "nonce-audit"
	)
	service := newTestService(config.Config{DataEncryptionKey: "test-data-key"}, nil, nil)
	clientSecretEncrypted, err := secretbox.EncryptString(service.cfg.Snapshot().DataEncryptionKey, clientSecret)
	if err != nil {
		t.Fatalf("encrypt client secret: %v", err)
	}
	provider := domainuser.IdentityProvider{
		Type:         domainuser.IdentityProviderTypeOAuth2,
		ClientID:     "oauth-client",
		ClientSecret: clientSecretEncrypted,
		AuthURL:      "https://oauth.example.com/authorize",
		TokenURL:     "https://oauth.example.com/token",
		UserInfoURL:  "https://oauth.example.com/userinfo",
	}
	secretBearingErr := fmt.Errorf("upstream failed code=%s token=%s secret=%s nonce=%s", authCode, accessToken, clientSecret, nonce)
	service.providerHTTPClient = &oidcAuditProviderClient{postErr: secretBearingErr}
	core, logs := observer.New(zap.WarnLevel)
	service.SetLogger(zap.New(core))

	tokenResponse, err := service.exchangeProviderCode(
		context.Background(),
		provider,
		authCode,
		"https://chat.example.com/auth/callback?provider=acme",
		strings.Repeat("p", 43),
	)
	if tokenResponse != nil {
		t.Fatalf("expected no token response on upstream failure, got %#v", tokenResponse)
	}
	if !errors.Is(err, ErrProviderUpstreamFailed) {
		t.Fatalf("expected provider upstream classification, got %v", err)
	}
	if strings.Contains(err.Error(), accessToken) || strings.Contains(err.Error(), authCode) || strings.Contains(err.Error(), clientSecret) || strings.Contains(err.Error(), nonce) {
		t.Fatalf("provider error exposed sensitive request data: %v", err)
	}

	grant := repository.ProviderAuthGrant{ProviderSlug: "acme"}
	service.populateProviderAuthGrantError(&grant, err)
	if logs.Len() != 1 {
		t.Fatalf("expected one sanitized provider warning, got %d", logs.Len())
	}
	logFields := fmt.Sprint(logs.All()[0].ContextMap())
	if strings.Contains(logFields, accessToken) || strings.Contains(logFields, authCode) || strings.Contains(logFields, clientSecret) || strings.Contains(logFields, nonce) {
		t.Fatalf("provider warning exposed sensitive request data: %s", logFields)
	}
}

func TestProviderOIDCSecurityAuditRejectsUnsafeCallbackRedirectVariants(t *testing.T) {
	service := newTestService(config.Config{CORSAllowOrigin: "https://chat.example.com"}, nil, nil)
	for _, redirectURI := range []string{
		"https://chat.example.com/auth/callback?provider=acme#fragment",
		"https://chat.example.com/auth/callback?provider=acme&provider=acme",
	} {
		if err := service.validateProviderRedirectURI("acme", redirectURI); !errors.Is(err, ErrInvalidRedirectURI) {
			t.Fatalf("expected redirect %q to be rejected, got %v", redirectURI, err)
		}
	}
}

func TestProviderOIDCSecurityAuditRejectsMalformedSignedStateContract(t *testing.T) {
	service := newTestService(config.Config{JWTSecret: "test-secret", CORSAllowOrigin: "https://chat.example.com"}, nil, nil)
	state, err := service.signProviderState(providerOAuthState{
		Provider:    "acme",
		RedirectURI: "https://chat.example.com/auth/callback?provider=acme",
		Intent:      providerIntentLogin,
		ExpiresAt:   time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("sign malformed provider state: %v", err)
	}
	if _, err = service.verifyProviderState("acme", "https://chat.example.com/auth/callback?provider=acme", state); !errors.Is(err, ErrOAuthStateInvalid) {
		t.Fatalf("expected malformed state contract rejection, got %v", err)
	}
}

func TestProviderOIDCSecurityAuditBridgeStateRequiresNonce(t *testing.T) {
	service := newTestService(config.Config{JWTSecret: "test-secret"}, nil, nil)
	state, err := service.signProviderAuthBridgeState(providerAuthBridgeState{
		Audience:      providerAuthBridgeAudience,
		Provider:      "acme",
		TransactionID: "transaction-audit",
		ExpiresAt:     time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("sign bridge state: %v", err)
	}
	if _, err = service.verifyProviderAuthBridgeState("acme", state); !errors.Is(err, ErrProviderBridgeStateMismatch) {
		t.Fatalf("expected bridge state without nonce to be rejected, got %v", err)
	}
}

func TestProviderOIDCSecurityAuditKeepsMissingEmailVerificationUnverifiedAndIgnoresRoleClaim(t *testing.T) {
	if resolveProviderEmailVerified(map[string]any{"email": "user@example.com", "role": "super_admin"}, domainuser.IdentityProvider{EmailVerifiedField: "email_verified"}) {
		t.Fatal("missing email_verified must remain unverified")
	}
	repo := &oidcAuditRepo{}
	service := newTestService(config.Config{DataEncryptionKey: "test-data-key"}, repo, nil)
	provider := domainuser.IdentityProvider{
		ID:                  10,
		Type:                domainuser.IdentityProviderTypeOIDC,
		Name:                "Acme OIDC",
		Slug:                "acme",
		LoginEnabled:        true,
		RegistrationEnabled: true,
		DefaultRole:         domainuser.RoleUser,
	}
	userItem, err := service.resolveProviderUser(context.Background(), resolveProviderUserInput{
		Provider:      provider,
		Subject:       "subject-role-audit",
		Email:         "role@example.com",
		DisplayName:   "Role Audit",
		EmailVerified: false,
		ProfileJSON:   `{"sub":"subject-role-audit","role":"super_admin"}`,
	})
	if err != nil {
		t.Fatalf("resolve provider user: %v", err)
	}
	if userItem.Role != domainuser.RoleUser || repo.createdUser == nil || repo.createdUser.Role != domainuser.RoleUser {
		t.Fatalf("provider profile role must not promote user: user=%q persisted=%#v", userItem.Role, repo.createdUser)
	}
}

type oidcAuditRepo struct {
	repository.AuthRepository
	createdUser *domainuser.User
}

func (r *oidcAuditRepo) GetUserIdentityByProviderSubject(context.Context, uint, string) (*domainuser.UserIdentity, error) {
	return nil, repository.ErrNotFound
}

func (r *oidcAuditRepo) GetByEmail(context.Context, string) (*domainuser.User, error) {
	return nil, repository.ErrNotFound
}

func (r *oidcAuditRepo) CreateWithCredentialAndIdentity(_ context.Context, input repository.CreateWithCredentialAndIdentityInput) error {
	userCopy := *input.User
	userCopy.ID = 100
	r.createdUser = &userCopy
	return nil
}

type oidcAuditProviderClient struct {
	discoveryBody []byte
	jwksBody      []byte
	tokenBody     []byte
	userInfoBody  []byte
	postErr       error
}

func (c *oidcAuditProviderClient) Get(_ context.Context, targetURL string, _ []string, _ map[string]string) (idpport.Response, error) {
	switch {
	case strings.HasSuffix(targetURL, "/openid-configuration"):
		return idpport.Response{StatusCode: 200, Status: "200 OK", Body: c.discoveryBody}, nil
	case strings.HasSuffix(targetURL, "/jwks"):
		return idpport.Response{StatusCode: 200, Status: "200 OK", Body: c.jwksBody}, nil
	case strings.HasSuffix(targetURL, "/userinfo"):
		return idpport.Response{StatusCode: 200, Status: "200 OK", Body: c.userInfoBody}, nil
	default:
		return idpport.Response{StatusCode: 404, Status: "404 Not Found"}, nil
	}
}

func (c *oidcAuditProviderClient) PostForm(context.Context, string, []string, url.Values, map[string]string) (idpport.Response, error) {
	if c.postErr != nil {
		return idpport.Response{}, c.postErr
	}
	return idpport.Response{StatusCode: 200, Status: "200 OK", Body: c.tokenBody}, nil
}

func newOIDCAuditRSAKey(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return privateKey, &privateKey.PublicKey
}

func oidcAuditProvider(service *Service) domainuser.IdentityProvider {
	clientSecret, err := secretbox.EncryptString(service.cfg.Snapshot().DataEncryptionKey, "oidc-secret")
	if err != nil {
		panic(err)
	}
	return domainuser.IdentityProvider{
		ID:                  10,
		Type:                domainuser.IdentityProviderTypeOIDC,
		Name:                "Acme OIDC",
		Slug:                "acme",
		LoginEnabled:        true,
		RegistrationEnabled: true,
		ClientID:            "oidc-client",
		ClientSecret:        clientSecret,
		IssuerURL:           "https://idp.example.com",
		AuthURL:             "https://idp.example.com/authorize",
		TokenURL:            "https://idp.example.com/token",
		UserInfoURL:         "https://idp.example.com/userinfo",
		JWKSURL:             "https://idp.example.com/jwks",
		Scopes:              "openid profile email",
		DefaultRole:         domainuser.RoleUser,
		SubjectField:        "sub",
		EmailField:          "email",
		EmailVerifiedField:  "email_verified",
		NameField:           "name",
		AvatarField:         "picture",
	}
}

func oidcAuditJWKS(t *testing.T, publicKey *rsa.PublicKey) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": "audit-key",
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(bigIntBytes(publicKey.E)),
		}},
	})
	if err != nil {
		t.Fatalf("marshal JWKS: %v", err)
	}
	return body
}

func bigIntBytes(value int) []byte {
	return []byte{byte(value >> 16), byte(value >> 8), byte(value)}
}

func oidcAuditIDToken(t *testing.T, privateKey *rsa.PrivateKey, issuer, audience, subject, nonce, keyID string) string {
	t.Helper()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   issuer,
		"sub":   subject,
		"aud":   audience,
		"nonce": nonce,
		"iat":   now.Add(-time.Minute).Unix(),
		"exp":   now.Add(5 * time.Minute).Unix(),
	})
	token.Header["kid"] = keyID
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign ID token: %v", err)
	}
	return signed
}
