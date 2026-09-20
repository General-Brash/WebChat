package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/secretbox"
	idpport "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/identityprovider"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

func TestProviderProtocolContractEmptyScopeDefaults(t *testing.T) {
	service := newTestService(config.Config{
		JWTSecret:         "test-secret",
		DataEncryptionKey: "test-data-key",
	}, &providerProtocolContractRepo{}, nil)

	tests := []struct {
		name       string
		input      UpsertIdentityProviderInput
		wantScopes string
	}{
		{
			name: "oidc defaults include openid",
			input: UpsertIdentityProviderInput{
				ActorRole:    domainuser.RoleAdmin,
				Type:         domainuser.IdentityProviderTypeOIDC,
				Name:         "Sub2 OIDC",
				ClientID:     "oidc-client",
				ClientSecret: "oidc-secret",
				DiscoveryURL: "https://sub2.example.com/.well-known/openid-configuration",
			},
			wantScopes: "openid profile email",
		},
		{
			name: "oidc adds openid to explicit scopes",
			input: UpsertIdentityProviderInput{
				ActorRole:    domainuser.RoleAdmin,
				Type:         domainuser.IdentityProviderTypeOIDC,
				Name:         "Sub2 OIDC explicit",
				ClientID:     "oidc-client",
				ClientSecret: "oidc-secret",
				DiscoveryURL: "https://sub2.example.com/.well-known/openid-configuration",
				Scopes:       "profile email",
			},
			wantScopes: "openid profile email",
		},
		{
			name: "oauth2 defaults omit openid",
			input: UpsertIdentityProviderInput{
				ActorRole:    domainuser.RoleAdmin,
				Type:         domainuser.IdentityProviderTypeOAuth2,
				Name:         "Generic OAuth2",
				ClientID:     "oauth-client",
				ClientSecret: "oauth-secret",
				AuthURL:      "https://oauth.example.com/authorize",
				TokenURL:     "https://oauth.example.com/token",
				UserInfoURL:  "https://oauth.example.com/userinfo",
			},
			wantScopes: "profile email",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := service.normalizeProviderInput(tc.input, nil)
			if err != nil {
				t.Fatalf("normalize provider input: %v", err)
			}
			if provider.Scopes != tc.wantScopes {
				t.Fatalf("expected scopes %q, got %q", tc.wantScopes, provider.Scopes)
			}
		})
	}
}

func TestProviderProtocolContractProfileDisplayNameFallback(t *testing.T) {
	tests := []struct {
		name        string
		profile     map[string]any
		wantDisplay string
	}{
		{
			name: "preferred_username before email",
			profile: map[string]any{
				"sub":                "sub-1",
				"preferred_username": "sub2-alice",
				"email":              "a@e.co",
			},
			wantDisplay: "sub2-alice",
		},
		{
			name: "name before email",
			profile: map[string]any{
				"sub":   "sub-2",
				"name":  "Alice Example",
				"email": "a@e.co",
			},
			wantDisplay: "Alice Example",
		},
		{
			name: "email before subject",
			profile: map[string]any{
				"sub":   "sub-3",
				"email": "a@e.co",
			},
			wantDisplay: "a@e.co",
		},
		{
			name: "subject last",
			profile: map[string]any{
				"sub": "sub-4",
			},
			wantDisplay: "sub-4",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service, repo := newProviderProtocolContractService(t, tc.profile)
			provider := providerProtocolContractProvider(t, service)

			userItem, subject, err := service.resolveProviderLoginCodeWithNonce(
				context.Background(),
				provider,
				"authorization-code",
				"https://chat.example.com/auth/callback?provider=sub2",
				strings.Repeat("v", 43),
				"",
			)
			if err != nil {
				t.Fatalf("resolve provider login code: %v", err)
			}
			if subject != tc.profile["sub"] {
				t.Fatalf("expected subject %q, got %q", tc.profile["sub"], subject)
			}
			if userItem.DisplayName != tc.wantDisplay {
				t.Fatalf("expected display name %q, got %q", tc.wantDisplay, userItem.DisplayName)
			}
			if repo.createdUser == nil || repo.createdUser.DisplayName != tc.wantDisplay {
				t.Fatalf("expected persisted display name %q, got %#v", tc.wantDisplay, repo.createdUser)
			}
		})
	}
}

func TestProviderProtocolContractMissingEmailVerifiedIsUnverified(t *testing.T) {
	profile := map[string]any{
		"sub":   "sub-1",
		"email": "a@e.co",
	}
	provider := domainuser.IdentityProvider{EmailVerifiedField: "email_verified"}

	if resolveProviderEmailVerified(profile, provider) {
		t.Fatal("missing email_verified must not be treated as verified")
	}
}

func TestProviderProtocolContractSub2RoleDoesNotOverrideProviderDefaultRole(t *testing.T) {
	service, repo := newProviderProtocolContractService(t, map[string]any{
		"sub":                "sub-role",
		"preferred_username": "sub2-admin",
		"email":              "admin@example.com",
		"email_verified":     true,
		"role":               "super_admin",
	})
	provider := providerProtocolContractProvider(t, service)
	provider.DefaultRole = domainuser.RoleUser

	userItem, _, err := service.resolveProviderLoginCodeWithNonce(
		context.Background(),
		provider,
		"authorization-code",
		"https://chat.example.com/auth/callback?provider=sub2",
		strings.Repeat("v", 43),
		"",
	)
	if err != nil {
		t.Fatalf("resolve provider login code: %v", err)
	}
	if userItem.Role != domainuser.RoleUser {
		t.Fatalf("expected provider default role %q, got %q", domainuser.RoleUser, userItem.Role)
	}
	if repo.createdUser == nil || repo.createdUser.Role != domainuser.RoleUser {
		t.Fatalf("expected persisted provider default role %q, got %#v", domainuser.RoleUser, repo.createdUser)
	}
}

func TestProviderProtocolContractDiscoveryTokenEndpointAuthPolicy(t *testing.T) {
	tests := []struct {
		name                string
		authMethods         []string
		wantBasic           bool
		wantFormCredentials bool
	}{
		{
			name:        "client_secret_basic advertised",
			authMethods: []string{"client_secret_basic", "client_secret_post"},
			wantBasic:   true,
		},
		{
			name:                "client_secret_post only",
			authMethods:         []string{"client_secret_post"},
			wantFormCredentials: true,
		},
		{
			name:      "metadata field missing defaults to basic",
			wantBasic: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const (
				clientID     = "oidc-client"
				clientSecret = "oidc-secret"
			)
			client := &providerProtocolContractClient{authMethods: tc.authMethods}
			service := newTestService(config.Config{
				JWTSecret:         "test-secret",
				DataEncryptionKey: "test-data-key",
			}, &providerProtocolContractRepo{}, nil)
			service.providerHTTPClient = client
			encryptedSecret, err := secretbox.EncryptString(service.cfg.Snapshot().DataEncryptionKey, clientSecret)
			if err != nil {
				t.Fatalf("encrypt provider client secret: %v", err)
			}
			provider := domainuser.IdentityProvider{
				Type:         domainuser.IdentityProviderTypeOIDC,
				ClientID:     clientID,
				ClientSecret: encryptedSecret,
				DiscoveryURL: "https://idp.example.com/.well-known/openid-configuration",
			}

			resolution, err := service.resolveProviderEndpointResolution(context.Background(), provider, provider.Type == domainuser.IdentityProviderTypeOIDC)
			if err != nil {
				t.Fatalf("resolve provider endpoint resolution: %v", err)
			}
			if _, err = service.exchangeProviderCodeWithResolution(
				context.Background(),
				provider,
				resolution,
				"authorization-code",
				"https://chat.example.com/auth/callback?provider=sub2",
				strings.Repeat("v", 43),
			); err != nil {
				t.Fatalf("exchange provider code: %v", err)
			}

			wantAuthorization := ""
			if tc.wantBasic {
				wantAuthorization = "Basic " + base64.StdEncoding.EncodeToString([]byte(clientID+":"+clientSecret))
			}
			if got := client.postHeaders["Authorization"]; got != wantAuthorization {
				t.Fatalf("expected Authorization %q, got %q", wantAuthorization, got)
			}
			if tc.wantFormCredentials {
				if got := client.postForm.Get("client_id"); got != clientID {
					t.Fatalf("expected client_id form credential %q, got %q", clientID, got)
				}
				if got := client.postForm.Get("client_secret"); got != clientSecret {
					t.Fatalf("expected client_secret form credential, got %q", got)
				}
			} else if client.postForm.Get("client_id") != "" || client.postForm.Get("client_secret") != "" {
				t.Fatalf("expected no client credentials in form, got %v", client.postForm)
			}
		})
	}
}

func newProviderProtocolContractService(t *testing.T, profile map[string]any) (*Service, *providerProtocolContractRepo) {
	t.Helper()
	repo := &providerProtocolContractRepo{}
	client := &providerProtocolContractClient{userInfo: profile}
	service := newTestService(config.Config{
		JWTSecret:         "test-secret",
		DataEncryptionKey: "test-data-key",
	}, repo, nil)
	service.providerHTTPClient = client
	return service, repo
}

func providerProtocolContractProvider(t *testing.T, service *Service) domainuser.IdentityProvider {
	t.Helper()
	encryptedSecret, err := secretbox.EncryptString(service.cfg.Snapshot().DataEncryptionKey, "oidc-secret")
	if err != nil {
		t.Fatalf("encrypt provider client secret: %v", err)
	}
	return domainuser.IdentityProvider{
		ID:                  10,
		Type:                domainuser.IdentityProviderTypeOIDC,
		Name:                "Sub2",
		Slug:                "sub2",
		LoginEnabled:        true,
		RegistrationEnabled: true,
		ClientID:            "oidc-client",
		ClientSecret:        encryptedSecret,
		AuthURL:             "https://idp.example.com/authorize",
		TokenURL:            "https://idp.example.com/token",
		UserInfoURL:         "https://idp.example.com/userinfo",
		Scopes:              "openid profile email",
		DefaultRole:         domainuser.RoleUser,
		SubjectField:        "sub",
		EmailField:          "email",
		EmailVerifiedField:  "email_verified",
		NameField:           "name",
		AvatarField:         "picture",
	}
}

type providerProtocolContractRepo struct {
	repository.AuthRepository
	createdUser     *domainuser.User
	createdIdentity *domainuser.UserIdentity
}

func (r *providerProtocolContractRepo) GetUserIdentityByProviderSubject(context.Context, uint, string) (*domainuser.UserIdentity, error) {
	return nil, repository.ErrNotFound
}

func (r *providerProtocolContractRepo) GetByEmail(context.Context, string) (*domainuser.User, error) {
	return nil, repository.ErrNotFound
}

func (r *providerProtocolContractRepo) CreateWithCredentialAndIdentity(_ context.Context, input repository.CreateWithCredentialAndIdentityInput) error {
	userCopy := *input.User
	userCopy.ID = 100
	input.User.ID = userCopy.ID
	r.createdUser = &userCopy
	if input.Identity != nil {
		identityCopy := *input.Identity
		identityCopy.ID = 200
		identityCopy.UserID = userCopy.ID
		r.createdIdentity = &identityCopy
	}
	return nil
}

type providerProtocolContractClient struct {
	authMethods []string
	userInfo    map[string]any
	postForm    url.Values
	postHeaders map[string]string
}

func (c *providerProtocolContractClient) Get(_ context.Context, targetURL string, _ []string, _ map[string]string) (idpport.Response, error) {
	switch targetURL {
	case "https://idp.example.com/.well-known/openid-configuration":
		payload := map[string]any{
			"authorization_endpoint": "https://idp.example.com/authorize",
			"token_endpoint":         "https://idp.example.com/token",
			"userinfo_endpoint":      "https://idp.example.com/userinfo",
		}
		if c.authMethods != nil {
			payload["token_endpoint_auth_methods_supported"] = c.authMethods
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return idpport.Response{}, err
		}
		return idpport.Response{StatusCode: 200, Status: "200 OK", Body: body}, nil
	case "https://idp.example.com/userinfo":
		body, err := json.Marshal(c.userInfo)
		if err != nil {
			return idpport.Response{}, err
		}
		return idpport.Response{StatusCode: 200, Status: "200 OK", Body: body}, nil
	default:
		return idpport.Response{StatusCode: 404, Status: "404 Not Found"}, nil
	}
}

func (c *providerProtocolContractClient) PostForm(_ context.Context, _ string, _ []string, form url.Values, headers map[string]string) (idpport.Response, error) {
	c.postForm = form
	c.postHeaders = headers
	return idpport.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Body:       []byte(`{"access_token":"access-token","token_type":"Bearer"}`),
	}, nil
}
