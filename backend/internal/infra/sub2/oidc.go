package sub2

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
	"github.com/golang-jwt/jwt/v5"
)

type ecdsaPublicKey struct {
	key *ecdsa.PublicKey
}

type oidcJWTClaims struct {
	Email             string           `json:"email,omitempty"`
	EmailVerified     bool             `json:"email_verified,omitempty"`
	Name              string           `json:"name,omitempty"`
	PreferredUsername string           `json:"preferred_username,omitempty"`
	Picture           string           `json:"picture,omitempty"`
	Nonce             string           `json:"nonce,omitempty"`
	AuthorizedParty   string           `json:"azp,omitempty"`
	AuthTime          *jwt.NumericDate `json:"auth_time,omitempty"`
	jwt.RegisteredClaims
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// AuthorizationURL creates a strict authorization-code request. The redirect
// URI is always the server-configured URI; callers cannot substitute one.
func (c *Client) AuthorizationURL(state string, nonce string, codeChallenge string) (string, error) {
	if c == nil || strings.TrimSpace(c.oidcAuthURL) == "" {
		return "", ErrNotConfigured
	}
	if strings.TrimSpace(state) == "" || strings.TrimSpace(nonce) == "" || strings.TrimSpace(codeChallenge) == "" {
		return "", errors.New("OIDC state, nonce and S256 code challenge are required")
	}
	parsed, err := parseConfiguredURL(c.oidcAuthURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", c.oidcClientID)
	query.Set("redirect_uri", c.oidcRedirectURI)
	query.Set("scope", strings.TrimSpace(c.oidcScopes))
	query.Set("state", strings.TrimSpace(state))
	query.Set("nonce", strings.TrimSpace(nonce))
	query.Set("code_challenge", strings.TrimSpace(codeChallenge))
	query.Set("code_challenge_method", "S256")
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String(), nil
}

// ExchangeAuthorizationCode performs the confidential server-side code exchange.
// The authorization code and verifier are never returned to the browser by this method.
func (c *Client) ExchangeAuthorizationCode(ctx context.Context, code string, redirectURI string, codeVerifier string) (*sub2.TokenSet, error) {
	if c == nil || c.httpClient == nil || strings.TrimSpace(c.oidcTokenURL) == "" {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(code) == "" || strings.TrimSpace(codeVerifier) == "" {
		return nil, errors.New("OIDC authorization code and verifier are required")
	}
	if strings.TrimSpace(redirectURI) != c.oidcRedirectURI {
		return nil, errors.New("OIDC redirect URI does not match configured URI")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {strings.TrimSpace(code)},
		"client_id":     {c.oidcClientID},
		"redirect_uri":  {c.oidcRedirectURI},
		"code_verifier": {strings.TrimSpace(codeVerifier)},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oidcTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build OIDC token request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if strings.TrimSpace(c.oidcClientSecret) != "" {
		request.SetBasicAuth(c.oidcClientID, c.oidcClientSecret)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request OIDC token endpoint: %w", err)
	}
	defer response.Body.Close()
	body, err := readBoundedBody(response.Body, c.maxResponseBytes)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, parseRemoteError(response.StatusCode, body)
	}
	var result sub2.TokenSet
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("%w: decode OIDC token response: %v", ErrInvalidResponse, err)
	}
	if strings.TrimSpace(result.IDToken) == "" || strings.TrimSpace(result.AccessToken) == "" {
		return nil, fmt.Errorf("%w: OIDC token response is missing id_token or access_token", ErrInvalidResponse)
	}
	return &result, nil
}

// ValidateIDToken enforces ES256, a configured issuer and client audience,
// required azp semantics, nonce, time claims and a fetched JWKS signature.
func (c *Client) ValidateIDToken(ctx context.Context, rawIDToken string, expectedNonce string) (*sub2.IDTokenClaims, error) {
	if c == nil || strings.TrimSpace(c.oidcIssuer) == "" || strings.TrimSpace(c.oidcClientID) == "" {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(rawIDToken) == "" || strings.TrimSpace(expectedNonce) == "" {
		return nil, errors.New("OIDC ID token and expected nonce are required")
	}
	var claims oidcJWTClaims
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer(c.oidcIssuer),
		jwt.WithAudience(c.oidcClientID),
		jwt.WithLeeway(c.oidcClockSkew),
	)
	token, err := parser.ParseWithClaims(rawIDToken, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodES256 {
			return nil, fmt.Errorf("OIDC ID token must use ES256")
		}
		if typ, _ := token.Header["typ"].(string); typ != "" && typ != "JWT" {
			return nil, errors.New("OIDC ID token type is invalid")
		}
		kid, ok := token.Header["kid"].(string)
		if !ok || strings.TrimSpace(kid) == "" {
			return nil, errors.New("OIDC ID token key id is missing")
		}
		key, keyErr := c.jwksKey(ctx, strings.TrimSpace(kid))
		if keyErr != nil {
			return nil, keyErr
		}
		return key.key, nil
	})
	if err != nil || token == nil || !token.Valid {
		if err == nil {
			err = errors.New("OIDC ID token signature is invalid")
		}
		return nil, fmt.Errorf("invalid OIDC ID token: %w", err)
	}
	if claims.Issuer != c.oidcIssuer || strings.TrimSpace(claims.Subject) == "" {
		return nil, errors.New("OIDC issuer or subject is invalid")
	}
	if len(claims.Audience) == 0 || !containsString(claims.Audience, c.oidcClientID) {
		return nil, errors.New("OIDC audience is invalid")
	}
	if claims.AuthorizedParty != "" && claims.AuthorizedParty != c.oidcClientID {
		return nil, errors.New("OIDC authorized party is invalid")
	}
	if len(claims.Audience) > 1 && claims.AuthorizedParty != c.oidcClientID {
		return nil, errors.New("OIDC azp is required for multiple audiences")
	}
	if claims.Nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(strings.TrimSpace(expectedNonce))) != 1 {
		return nil, errors.New("OIDC nonce is invalid")
	}
	if claims.ExpiresAt == nil || claims.IssuedAt == nil {
		return nil, errors.New("OIDC exp and iat claims are required")
	}
	now := time.Now()
	if claims.ExpiresAt.Time.Before(now.Add(-c.oidcClockSkew)) {
		return nil, errors.New("OIDC ID token is expired")
	}
	if claims.IssuedAt.Time.After(now.Add(c.oidcClockSkew)) {
		return nil, errors.New("OIDC ID token iat is in the future")
	}
	if claims.ExpiresAt.Time.Before(claims.IssuedAt.Time) {
		return nil, errors.New("OIDC ID token exp precedes iat")
	}
	if claims.NotBefore != nil && claims.NotBefore.Time.After(now.Add(c.oidcClockSkew)) {
		return nil, errors.New("OIDC ID token is not yet valid")
	}
	if claims.AuthTime != nil && claims.AuthTime.Time.After(now.Add(c.oidcClockSkew)) {
		return nil, errors.New("OIDC auth_time is in the future")
	}

	claimMap, err := decodeClaims(rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("%w: decode OIDC claims: %v", ErrInvalidResponse, err)
	}
	result := &sub2.IDTokenClaims{
		Issuer:            claims.Issuer,
		Subject:           claims.Subject,
		Audience:          append([]string(nil), claims.Audience...),
		AuthorizedParty:   claims.AuthorizedParty,
		Nonce:             claims.Nonce,
		Email:             strings.TrimSpace(claims.Email),
		EmailVerified:     claims.EmailVerified,
		Name:              strings.TrimSpace(claims.Name),
		PreferredUsername: strings.TrimSpace(claims.PreferredUsername),
		Picture:           strings.TrimSpace(claims.Picture),
		IssuedAt:          claims.IssuedAt.Time,
		ExpiresAt:         claims.ExpiresAt.Time,
		Claims:            claimMap,
	}
	if claims.NotBefore != nil {
		value := claims.NotBefore.Time
		result.NotBefore = &value
	}
	if claims.AuthTime != nil {
		value := claims.AuthTime.Time
		result.AuthTime = &value
	}
	return result, nil
}

func (c *Client) jwksKey(ctx context.Context, kid string) (*ecdsaPublicKey, error) {
	c.jwksMu.RLock()
	key := c.jwksKeys[kid]
	fresh := !c.jwksFetchedAt.IsZero() && time.Since(c.jwksFetchedAt) < 5*time.Minute
	c.jwksMu.RUnlock()
	if key != nil && fresh {
		return key, nil
	}
	if err := c.refreshJWKS(ctx); err != nil {
		return nil, err
	}
	c.jwksMu.RLock()
	defer c.jwksMu.RUnlock()
	key = c.jwksKeys[kid]
	if key == nil {
		return nil, errors.New("OIDC JWKS does not contain the requested key")
	}
	return key, nil
}

func (c *Client) refreshJWKS(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.oidcJWKSURL, nil)
	if err != nil {
		return fmt.Errorf("build OIDC JWKS request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request OIDC JWKS: %w", err)
	}
	defer response.Body.Close()
	body, err := readBoundedBody(response.Body, c.maxResponseBytes)
	if err != nil {
		return err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return parseRemoteError(response.StatusCode, body)
	}
	var document jwksDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return fmt.Errorf("%w: decode OIDC JWKS: %v", ErrInvalidResponse, err)
	}
	keys := make(map[string]*ecdsaPublicKey, len(document.Keys))
	for _, item := range document.Keys {
		key, err := parseES256JWK(item)
		if err != nil {
			return err
		}
		if key == nil {
			continue
		}
		if _, exists := keys[item.Kid]; exists {
			return errors.New("OIDC JWKS contains duplicate key ids")
		}
		keys[item.Kid] = key
	}
	if len(keys) == 0 {
		return errors.New("OIDC JWKS contains no usable ES256 signing keys")
	}
	c.jwksMu.Lock()
	c.jwksKeys = keys
	c.jwksFetchedAt = time.Now()
	c.jwksMu.Unlock()
	return nil
}

func parseES256JWK(item jwk) (*ecdsaPublicKey, error) {
	if strings.TrimSpace(item.Kid) == "" {
		return nil, errors.New("OIDC JWKS signing key id is missing")
	}
	if item.Kty != "EC" || item.Crv != "P-256" {
		return nil, nil
	}
	if item.Use != "" && item.Use != "sig" {
		return nil, nil
	}
	if item.Alg != "" && item.Alg != "ES256" {
		return nil, nil
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(item.X)
	if err != nil {
		return nil, fmt.Errorf("decode OIDC JWKS x coordinate: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(item.Y)
	if err != nil {
		return nil, fmt.Errorf("decode OIDC JWKS y coordinate: %w", err)
	}
	if len(xBytes) != 32 || len(yBytes) != 32 {
		return nil, errors.New("OIDC JWKS P-256 coordinates have invalid length")
	}
	publicKey := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xBytes), Y: new(big.Int).SetBytes(yBytes)}
	if !publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
		return nil, errors.New("OIDC JWKS P-256 point is invalid")
	}
	return &ecdsaPublicKey{key: publicKey}, nil
}

func readBoundedBody(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Sub2 response: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrInvalidResponse, maxBytes)
	}
	return body, nil
}

func decodeClaims(rawToken string) (map[string]any, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("JWT must have three segments")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func containsString(items []string, expected string) bool {
	for _, item := range items {
		if item == expected {
			return true
		}
	}
	return false
}

var _ sub2.OIDCProvider = (*Client)(nil)
