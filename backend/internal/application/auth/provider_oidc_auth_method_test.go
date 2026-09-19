package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"testing"

	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/secretbox"
	idpport "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/identityprovider"
)

func TestExchangeProviderCodeSelectsOIDCDiscoveryTokenAuthMethod(t *testing.T) {
	const (
		clientID     = "oidc-client"
		clientSecret = "oidc-secret"
	)

	tests := []struct {
		name              string
		discoveryBody     string
		wantBasic         bool
		wantPost          bool
		wantErr           bool
		wantDiscoveryGets int
	}{
		{
			name:              "basic preferred when advertised with post",
			discoveryBody:     `{"token_endpoint_auth_methods_supported":["client_secret_post","client_secret_basic"]}`,
			wantBasic:         true,
			wantDiscoveryGets: 1,
		},
		{
			name:              "post retained when it is the only advertised method",
			discoveryBody:     `{"token_endpoint_auth_methods_supported":["client_secret_post"]}`,
			wantPost:          true,
			wantDiscoveryGets: 1,
		},
		{
			name:              "missing metadata defaults OIDC to basic",
			discoveryBody:     `{}`,
			wantBasic:         true,
			wantDiscoveryGets: 1,
		},
		{
			name:              "explicit unsupported methods fail closed",
			discoveryBody:     `{"token_endpoint_auth_methods_supported":["private_key_jwt"]}`,
			wantErr:           true,
			wantDiscoveryGets: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := &discoveryAuthMethodFakeClient{
				discoveryResponse: idpport.Response{
					StatusCode: 200,
					Status:     "200 OK",
					Body:       []byte(tc.discoveryBody),
				},
				postResponse: idpport.Response{
					StatusCode: 200,
					Status:     "200 OK",
					Body:       []byte(`{"access_token":"access-token","token_type":"Bearer"}`),
				},
			}
			service := newTestService(config.Config{DataEncryptionKey: "test-data-encryption-key"}, nil, nil)
			service.providerHTTPClient = fakeClient
			encryptedSecret, err := secretbox.EncryptString(service.cfg.Snapshot().DataEncryptionKey, clientSecret)
			if err != nil {
				t.Fatalf("encrypt provider client secret: %v", err)
			}
			provider := domainuser.IdentityProvider{
				Type:         domainuser.IdentityProviderTypeOIDC,
				ClientID:     clientID,
				ClientSecret: encryptedSecret,
				DiscoveryURL: "https://idp.example.com/.well-known/openid-configuration",
				AuthURL:      "https://idp.example.com/authorize",
				TokenURL:     "https://idp.example.com/token",
				UserInfoURL:  "https://idp.example.com/userinfo",
			}

			_, err = service.exchangeProviderCode(context.Background(), provider, "code", "https://chat.example.com/auth/callback?provider=acme", "verifier")
			if tc.wantErr {
				if err == nil || !errors.Is(err, ErrProviderUpstreamFailed) {
					t.Fatalf("expected unsupported discovery authentication error, got %v", err)
				}
				if fakeClient.postCalls != 0 {
					t.Fatalf("token endpoint must not be called for unsupported authentication metadata; calls=%d", fakeClient.postCalls)
				}
			} else if err != nil {
				t.Fatalf("exchange provider code: %v", err)
			}
			if fakeClient.getCalls != tc.wantDiscoveryGets {
				t.Fatalf("expected %d discovery request, got %d", tc.wantDiscoveryGets, fakeClient.getCalls)
			}
			if tc.wantErr {
				return
			}

			authorization := fakeClient.postHeaders["Authorization"]
			_, hasClientID := fakeClient.postForm["client_id"]
			_, hasClientSecret := fakeClient.postForm["client_secret"]
			if tc.wantBasic {
				wantAuthorization := "Basic " + base64.StdEncoding.EncodeToString([]byte(clientID+":"+clientSecret))
				if authorization != wantAuthorization {
					t.Fatalf("expected Basic authorization %q, got %q", wantAuthorization, authorization)
				}
				if hasClientID || hasClientSecret {
					t.Fatalf("Basic authentication must not also send form credentials: %v", fakeClient.postForm)
				}
			}
			if tc.wantPost {
				if authorization != "" {
					t.Fatalf("post authentication must not also send Authorization header, got %q", authorization)
				}
				if !hasClientID || !hasClientSecret {
					t.Fatalf("post authentication must send both client form credentials: %v", fakeClient.postForm)
				}
				if fakeClient.postForm.Get("client_id") != clientID || fakeClient.postForm.Get("client_secret") != clientSecret {
					t.Fatalf("unexpected post credentials: %v", fakeClient.postForm)
				}
			}
		})
	}
}

func TestExchangeProviderCodeOAuth2RetainsClientSecretPostWithoutDiscovery(t *testing.T) {
	const clientSecret = "oauth-secret"
	fakeClient := &discoveryAuthMethodFakeClient{
		postResponse: idpport.Response{
			StatusCode: 200,
			Status:     "200 OK",
			Body:       []byte(`{"access_token":"access-token"}`),
		},
	}
	service := newTestService(config.Config{DataEncryptionKey: "test-data-encryption-key"}, nil, nil)
	service.providerHTTPClient = fakeClient
	encryptedSecret, err := secretbox.EncryptString(service.cfg.Snapshot().DataEncryptionKey, clientSecret)
	if err != nil {
		t.Fatalf("encrypt provider client secret: %v", err)
	}
	provider := domainuser.IdentityProvider{
		Type:         domainuser.IdentityProviderTypeOAuth2,
		ClientID:     "oauth-client",
		ClientSecret: encryptedSecret,
		AuthURL:      "https://oauth.example.com/authorize",
		TokenURL:     "https://oauth.example.com/token",
		UserInfoURL:  "https://oauth.example.com/userinfo",
	}

	if _, err = service.exchangeProviderCode(context.Background(), provider, "code", "https://chat.example.com/auth/callback?provider=oauth", "verifier"); err != nil {
		t.Fatalf("exchange OAuth2 provider code: %v", err)
	}
	if fakeClient.getCalls != 0 {
		t.Fatalf("OAuth2 token exchange must not perform OIDC discovery, got %d requests", fakeClient.getCalls)
	}
	if fakeClient.postHeaders["Authorization"] != "" {
		t.Fatalf("OAuth2 compatibility path must not send Basic authorization, got %q", fakeClient.postHeaders["Authorization"])
	}
	if fakeClient.postForm.Get("client_id") != "oauth-client" || fakeClient.postForm.Get("client_secret") != clientSecret {
		t.Fatalf("OAuth2 compatibility path must retain post credentials: %v", fakeClient.postForm)
	}
}

type discoveryAuthMethodFakeClient struct {
	discoveryResponse idpport.Response
	postResponse      idpport.Response
	getCalls          int
	postCalls         int
	postForm          url.Values
	postHeaders       map[string]string
}

func (f *discoveryAuthMethodFakeClient) Get(context.Context, string, []string, map[string]string) (idpport.Response, error) {
	f.getCalls++
	return f.discoveryResponse, nil
}

func (f *discoveryAuthMethodFakeClient) PostForm(_ context.Context, _ string, _ []string, form url.Values, headers map[string]string) (idpport.Response, error) {
	f.postCalls++
	f.postForm = form
	f.postHeaders = headers
	return f.postResponse, nil
}
