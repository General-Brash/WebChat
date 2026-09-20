package auth

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/secretbox"
	idpport "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/identityprovider"
)

func TestBuildProviderAuthURLOIDCIncludesPKCEAndStateBoundNonce(t *testing.T) {
	const (
		slug        = "acme"
		redirectURI = "http://127.0.0.1:8080/auth/callback?provider=acme"
	)

	provider := &domainuser.IdentityProvider{
		Type:                domainuser.IdentityProviderTypeOIDC,
		Name:                "Acme OIDC",
		Slug:                slug,
		LoginEnabled:        true,
		RegistrationEnabled: true,
		ClientID:            "oidc-client",
		AuthURL:             "https://idp.example.com/authorize",
		TokenURL:            "https://idp.example.com/token",
		UserInfoURL:         "https://idp.example.com/userinfo",
		Scopes:              "openid profile email",
	}
	service := newTestService(config.Config{
		JWTSecret:              "test-jwt-secret",
		CORSAllowOrigin:        "http://127.0.0.1:8080",
		ThirdPartyLoginEnabled: true,
	}, &providerLoginRepo{providersBySlug: map[string]*domainuser.IdentityProvider{slug: provider}}, nil)
	codeChallenge := strings.Repeat("c", 43)

	authorizationURL, err := service.BuildProviderAuthURL(
		context.Background(),
		slug,
		redirectURI,
		"/chat",
		codeChallenge,
		providerIntentLogin,
	)
	if err != nil {
		t.Fatalf("build provider authorization URL: %v", err)
	}

	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatalf("parse provider authorization URL: %v", err)
	}
	query := parsed.Query()
	if got := query.Get("code_challenge"); got != codeChallenge {
		t.Fatalf("expected PKCE code challenge %q, got %q", codeChallenge, got)
	}
	if got := query.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("expected S256 code challenge method, got %q", got)
	}
	if query.Get("state") == "" {
		t.Fatal("expected signed provider state in authorization URL")
	}

	verifiedState, err := service.verifyProviderState(slug, redirectURI, query.Get("state"))
	if err != nil {
		t.Fatalf("verify provider state from authorization URL: %v", err)
	}
	if strings.TrimSpace(verifiedState.Nonce) == "" {
		t.Fatal("expected a non-empty nonce in the signed provider state")
	}
	if got := query.Get("nonce"); got != verifiedState.Nonce {
		t.Fatalf("expected authorization nonce %q from signed provider state, got %q", verifiedState.Nonce, got)
	}
}

func TestStartProviderAuthBridgeOIDCIncludesStateBoundNonce(t *testing.T) {
	service, _ := newProviderAuthBridgeTestService()
	repo := service.repo.(*providerLoginRepo)
	repo.providersBySlug["acme"].Type = domainuser.IdentityProviderTypeOIDC
	clientVerifier := strings.Repeat("c", 43)

	result, err := service.StartProviderAuthBridge(context.Background(), "acme", ProviderAuthBridgeStartInput{
		ClientID:      ProviderAuthNativeClientID,
		RedirectURI:   providerAuthNativeRedirect,
		CodeChallenge: providerCodeChallenge(clientVerifier),
		ClientState:   strings.Repeat("s", 43),
		Intent:        providerIntentLogin,
	})
	if err != nil {
		t.Fatalf("start OIDC provider auth bridge: %v", err)
	}

	authorizationURL, err := url.Parse(result.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse OIDC provider authorization URL: %v", err)
	}
	nonce := authorizationURL.Query().Get("nonce")
	if strings.TrimSpace(nonce) == "" {
		t.Fatal("expected a non-empty OIDC bridge nonce")
	}
	state, err := service.verifyProviderAuthBridgeState("acme", authorizationURL.Query().Get("state"))
	if err != nil {
		t.Fatalf("verify OIDC bridge state: %v", err)
	}
	if strings.TrimSpace(state.Nonce) == "" {
		t.Fatal("expected a non-empty nonce in the signed OIDC bridge state")
	}
	if nonce != state.Nonce {
		t.Fatalf("expected OIDC bridge nonce %q from signed state, got %q", state.Nonce, nonce)
	}
}

func TestExchangeProviderCodeOIDCUsesBasicAuthorizationWithoutClientCredentialsFormFields(t *testing.T) {
	const (
		clientID     = "oidc-client"
		clientSecret = "oidc-secret"
	)

	fakeClient := &oidcCompatFakeIdentityProviderClient{
		postFormResponse: idpport.Response{
			StatusCode: 200,
			Status:     "200 OK",
			Body:       []byte(`{"access_token":"access-token","token_type":"Bearer"}`),
		},
	}
	service := newTestService(config.Config{
		JWTSecret:         "test-jwt-secret",
		DataEncryptionKey: "test-data-encryption-key",
	}, nil, nil)
	service.providerHTTPClient = fakeClient
	encryptedSecret, err := secretbox.EncryptString(service.cfg.Snapshot().DataEncryptionKey, clientSecret)
	if err != nil {
		t.Fatalf("encrypt provider client secret: %v", err)
	}
	provider := domainuser.IdentityProvider{
		Type:         domainuser.IdentityProviderTypeOIDC,
		ClientID:     clientID,
		ClientSecret: encryptedSecret,
		AuthURL:      "https://idp.example.com/authorize",
		TokenURL:     "https://idp.example.com/token",
		UserInfoURL:  "https://idp.example.com/userinfo",
	}

	const (
		code         = "authorization-code"
		redirectURI  = "https://chat.example.com/auth/callback?provider=acme"
		codeVerifier = "verifier-value"
	)
	resolution, err := service.resolveProviderEndpointResolution(context.Background(), provider, provider.Type == domainuser.IdentityProviderTypeOIDC)
	if err != nil {
		t.Fatalf("resolve provider endpoint resolution: %v", err)
	}
	if _, err = service.exchangeProviderCodeWithResolution(context.Background(), provider, resolution, code, redirectURI, codeVerifier); err != nil {
		t.Fatalf("exchange provider code: %v", err)
	}

	if got := fakeClient.postFormHeaders["Authorization"]; got != "Basic "+base64.StdEncoding.EncodeToString([]byte(clientID+":"+clientSecret)) {
		t.Fatalf("expected Basic authorization header, got %q", got)
	}
	if _, ok := fakeClient.postFormValues["client_id"]; ok {
		t.Fatal("token exchange form must not contain client_id for OIDC")
	}
	if _, ok := fakeClient.postFormValues["client_secret"]; ok {
		t.Fatal("token exchange form must not contain client_secret for OIDC")
	}
	for key, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  redirectURI,
		"code_verifier": codeVerifier,
	} {
		if got := fakeClient.postFormValues.Get(key); got != want {
			t.Fatalf("expected token exchange form %s=%q, got %q", key, want, got)
		}
	}
}

type oidcCompatFakeIdentityProviderClient struct {
	postFormResponse idpport.Response
	postFormValues   url.Values
	postFormHeaders  map[string]string
	postFormURL      string
}

func (f *oidcCompatFakeIdentityProviderClient) Get(context.Context, string, []string, map[string]string) (idpport.Response, error) {
	return idpport.Response{}, nil
}

func (f *oidcCompatFakeIdentityProviderClient) PostForm(_ context.Context, targetURL string, _ []string, form url.Values, headers map[string]string) (idpport.Response, error) {
	f.postFormURL = targetURL
	f.postFormValues = form
	f.postFormHeaders = headers
	return f.postFormResponse, nil
}
