// Package sub2 implements the authenticated Chat-to-Sub2 boundary.
package sub2

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	domainsub2 "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/sub2"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
	"github.com/google/uuid"
)

const (
	identityStatusPath      = "/api/v1/integration/identity/status"
	subjectExchangePath     = "/api/v1/integration/identity/subject-exchange"
	walletPath              = "/api/v1/integration/wallet"
	modelCatalogPath        = "/api/v1/integration/models"
	modelAdmissionPath      = "/api/v1/integration/models/admission"
	executionQueryPath      = "/api/v1/integration/executions/query"
	executionCancelPath     = "/api/v1/integration/executions/cancel"
	defaultMaxResponseBytes = 4 * 1024 * 1024
)

var (
	ErrNotConfigured     = errors.New("sub2 client is not configured")
	ErrInvalidResponse   = errors.New("invalid Sub2 response")
	ErrIdentityMismatch  = errors.New("Sub2 response identity mismatch")
	ErrExecutionMismatch = errors.New("Sub2 response execution mismatch")
)

// RemoteError is a bounded, non-secret representation of a Sub2 HTTP error.
type RemoteError struct {
	StatusCode int
	Code       string
}

func (e *RemoteError) Error() string {
	if e == nil {
		return "Sub2 request failed"
	}
	if e.Code == "" {
		return fmt.Sprintf("Sub2 request failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("Sub2 request failed with status %d (%s)", e.StatusCode, e.Code)
}

// Client is an mTLS-capable, Ed25519-signed Sub2 client.
type Client struct {
	executionConfigured bool
	httpClient          *http.Client
	baseURL             *url.URL
	serviceID           string
	signingKeyID        string
	signingPrivateKey   ed25519.PrivateKey
	application         string
	maxResponseBytes    int64
	trustedOrigins      map[string]struct{}

	oidcIssuer       string
	oidcClientID     string
	oidcClientSecret string
	oidcRedirectURI  string
	oidcAuthURL      string
	oidcTokenURL     string
	oidcJWKSURL      string
	oidcScopes       string
	oidcClockSkew    time.Duration

	jwksMu        sync.RWMutex
	jwksKeys      map[string]*ecdsaPublicKey
	jwksFetchedAt time.Time
}

// New creates a Sub2 client without opening a network connection. Local mode
// intentionally returns nil so existing local behavior stays unchanged.
func New(cfg config.Config) (*Client, error) {
	if !cfg.UsesSub2Authority() {
		return nil, nil
	}
	baseURL, err := parseConfiguredURL(cfg.Sub2BaseURL)
	if err != nil {
		return nil, fmt.Errorf("configure Sub2 base URL: %w", err)
	}
	privateKey, err := loadSigningPrivateKey(cfg.Sub2SigningPrivateKey, cfg.Sub2SigningPrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load Sub2 signing key: %w", err)
	}
	trustedURLs := []string{
		cfg.Sub2BaseURL,
		cfg.Sub2OIDCIssuer,
		cfg.Sub2OIDCRedirectURI,
		cfg.Sub2OIDCAuthURL,
		cfg.Sub2OIDCTokenURL,
		cfg.Sub2OIDCJWKSURL,
	}
	policy, err := cfg.StrictOutboundPolicy().WithTrustedHTTPURLs(trustedURLs...)
	if err != nil {
		return nil, fmt.Errorf("configure Sub2 outbound policy: %w", err)
	}
	transport, err := newTransport(policy, cfg)
	if err != nil {
		return nil, err
	}
	trustedOrigins := make(map[string]struct{})
	for _, raw := range trustedURLs {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		origin, originErr := security.HTTPOrigin(raw)
		if originErr != nil {
			return nil, fmt.Errorf("configure Sub2 trusted origin: %w", originErr)
		}
		trustedOrigins[origin] = struct{}{}
	}
	maxResponseBytes := cfg.Sub2MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	requestTimeout := time.Duration(cfg.Sub2RequestTimeoutSeconds) * time.Second
	if requestTimeout <= 0 {
		requestTimeout = 10 * time.Second
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("Sub2 request stopped after too many redirects")
			}
			origin, originErr := security.HTTPOrigin(request.URL.String())
			if originErr != nil {
				return fmt.Errorf("validate Sub2 redirect: %w", originErr)
			}
			if _, ok := trustedOrigins[origin]; !ok {
				return errors.New("Sub2 redirect changed the configured origin")
			}
			return nil
		},
	}
	clockSkew := time.Duration(cfg.Sub2OIDCClockSkewSeconds) * time.Second
	if clockSkew <= 0 {
		clockSkew = time.Minute
	}
	return &Client{
		httpClient:        client,
		baseURL:           baseURL,
		serviceID:         strings.TrimSpace(cfg.Sub2ServiceID),
		signingKeyID:      strings.TrimSpace(cfg.Sub2SigningKeyID),
		signingPrivateKey: privateKey,
		application:       strings.TrimSpace(cfg.Sub2Application),
		maxResponseBytes:  maxResponseBytes,
		trustedOrigins:    trustedOrigins,
		oidcIssuer:        strings.TrimSpace(cfg.Sub2OIDCIssuer),
		oidcClientID:      strings.TrimSpace(cfg.Sub2OIDCClientID),
		oidcClientSecret:  cfg.Sub2OIDCClientSecret,
		oidcRedirectURI:   strings.TrimSpace(cfg.Sub2OIDCRedirectURI),
		oidcAuthURL:       strings.TrimSpace(cfg.Sub2OIDCAuthURL),
		oidcTokenURL:      strings.TrimSpace(cfg.Sub2OIDCTokenURL),
		oidcJWKSURL:       strings.TrimSpace(cfg.Sub2OIDCJWKSURL),
		oidcScopes:        strings.TrimSpace(cfg.Sub2OIDCScopes),
		oidcClockSkew:     clockSkew,
		jwksKeys:          make(map[string]*ecdsaPublicKey),
	}, nil
}

func newTransport(policy security.OutboundPolicy, cfg config.Config) (*http.Transport, error) {
	connectTimeout := time.Duration(cfg.Sub2RequestTimeoutSeconds) * time.Second
	if connectTimeout <= 0 {
		connectTimeout = 10 * time.Second
	}
	transport := security.NewOutboundHTTPTransport(policy, connectTimeout)
	transport.ForceAttemptHTTP2 = true
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	if transport.TLSClientConfig != nil {
		tlsConfig = transport.TLSClientConfig.Clone()
		if tlsConfig.MinVersion < tls.VersionTLS13 {
			tlsConfig.MinVersion = tls.VersionTLS13
		}
	}
	if serverName := strings.TrimSpace(cfg.Sub2TLSServerName); serverName != "" {
		tlsConfig.ServerName = serverName
	}
	if certFile, keyFile := strings.TrimSpace(cfg.Sub2ClientCertFile), strings.TrimSpace(cfg.Sub2ClientKeyFile); certFile != "" && keyFile != "" {
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load Sub2 mTLS client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	if caFile := strings.TrimSpace(cfg.Sub2CACertFile); caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read Sub2 CA certificate: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, errors.New("Sub2 CA certificate does not contain a PEM certificate")
		}
		tlsConfig.RootCAs = pool
	}
	transport.TLSClientConfig = tlsConfig
	return transport, nil
}

func parseConfiguredURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("Sub2 URL must be an absolute HTTP(S) URL without credentials")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("Sub2 URL must use HTTP or HTTPS")
	}
	return parsed, nil
}

func loadSigningPrivateKey(raw string, file string) (ed25519.PrivateKey, error) {
	if strings.TrimSpace(file) != "" {
		data, err := os.ReadFile(strings.TrimSpace(file))
		if err != nil {
			return nil, err
		}
		return decodeSigningPrivateKey(data)
	}
	return decodeSigningPrivateKey([]byte(strings.TrimSpace(raw)))
}

func decodeSigningPrivateKey(data []byte) (ed25519.PrivateKey, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("empty Ed25519 private key")
	}
	if block, _ := pem.Decode(trimmed); block != nil {
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#8 Ed25519 private key: %w", err)
		}
		privateKey, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("PKCS#8 key is not Ed25519")
		}
		return append(ed25519.PrivateKey(nil), privateKey...), nil
	}
	candidates := [][]byte{trimmed}
	for _, decoder := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		if decoded, err := decoder.DecodeString(string(trimmed)); err == nil {
			candidates = append(candidates, decoded)
		}
	}
	if decoded, err := hex.DecodeString(string(trimmed)); err == nil {
		candidates = append(candidates, decoded)
	}
	for _, candidate := range candidates {
		switch len(candidate) {
		case ed25519.SeedSize:
			return ed25519.NewKeyFromSeed(candidate), nil
		case ed25519.PrivateKeySize:
			return append(ed25519.PrivateKey(nil), candidate...), nil
		}
	}
	return nil, errors.New("Ed25519 private key must be a seed, private key, base64/hex value, or PKCS#8 PEM")
}

func (c *Client) buildURL(route string) (string, error) {
	if c == nil || c.baseURL == nil {
		return "", ErrNotConfigured
	}
	copyURL := *c.baseURL
	copyURL.Path = path.Join(strings.TrimRight(c.baseURL.Path, "/"), route)
	copyURL.RawPath = ""
	copyURL.RawQuery = ""
	copyURL.Fragment = ""
	return copyURL.String(), nil
}

func (c *Client) doJSON(ctx context.Context, method string, targetURL string, payload any, destination any) error {
	if c == nil || c.httpClient == nil {
		return ErrNotConfigured
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal Sub2 request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Sub2 request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	if err := c.sign(request, body); err != nil {
		return err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request Sub2: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Sub2 response: %w", err)
	}
	if int64(len(responseBody)) > c.maxResponseBytes {
		return fmt.Errorf("%w: response exceeds %d bytes", ErrInvalidResponse, c.maxResponseBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return parseRemoteError(response.StatusCode, responseBody)
	}
	if destination == nil || len(bytes.TrimSpace(responseBody)) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, destination); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidResponse, err)
	}
	return nil
}

func (c *Client) sign(request *http.Request, body []byte) error {
	if c == nil || len(c.signingPrivateKey) != ed25519.PrivateKeySize {
		return ErrNotConfigured
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := uuid.NewString()
	hash := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(hash[:])
	requestPath := request.URL.EscapedPath()
	if requestPath == "" {
		requestPath = "/"
	}
	canonicalQuery := request.URL.Query().Encode()
	canonical := strings.Join([]string{
		strings.ToUpper(request.Method),
		requestPath,
		canonicalQuery,
		bodyHash,
		now,
		nonce,
	}, "\n")
	signature := ed25519.Sign(c.signingPrivateKey, []byte(canonical))
	request.Header.Set("X-Sub2-Service-ID", c.serviceID)
	request.Header.Set("X-Sub2-Key-ID", c.signingKeyID)
	request.Header.Set("X-Sub2-Signature-Algorithm", "Ed25519")
	request.Header.Set("X-Sub2-Timestamp", now)
	request.Header.Set("X-Sub2-Nonce", nonce)
	request.Header.Set("X-Sub2-Body-SHA256", bodyHash)
	request.Header.Set("X-Sub2-Signature", base64.RawURLEncoding.EncodeToString(signature))
	return nil
}

func parseRemoteError(statusCode int, body []byte) error {
	var envelope struct {
		ErrorCode string `json:"errorCode"`
		Code      string `json:"code"`
	}
	_ = json.Unmarshal(body, &envelope)
	code := strings.TrimSpace(envelope.ErrorCode)
	if code == "" {
		code = strings.TrimSpace(envelope.Code)
	}
	return &RemoteError{StatusCode: statusCode, Code: code}
}

func validateIdentityRequest(identity sub2.IdentityRef) error {
	if strings.TrimSpace(identity.Issuer) == "" || strings.TrimSpace(identity.Subject) == "" {
		return fmt.Errorf("%w: issuer and subject are required", ErrInvalidResponse)
	}
	return nil
}

func validateBoundIdentityRequest(identity sub2.IdentityRef) error {
	if err := validateIdentityRequest(identity); err != nil {
		return err
	}
	if strings.TrimSpace(identity.SubjectAssertion) == "" {
		return fmt.Errorf("%w: encrypted binding proof is required", ErrInvalidResponse)
	}
	return nil
}

func validateIdentityEcho(expected sub2.IdentityRef, actual sub2.IdentityRef) error {
	if strings.TrimSpace(actual.Issuer) != strings.TrimSpace(expected.Issuer) ||
		strings.TrimSpace(actual.Subject) != strings.TrimSpace(expected.Subject) {
		return ErrIdentityMismatch
	}
	return nil
}

func validateExecutionID(executionID string) error {
	value := strings.TrimSpace(executionID)
	if value == "" || strings.ContainsAny(value, "/\\") {
		return fmt.Errorf("%w: execution id is invalid", ErrInvalidResponse)
	}
	return nil
}

// CloseIdleConnections releases pooled Sub2 and OIDC connections.
func (c *Client) CloseIdleConnections() {
	if c != nil && c.httpClient != nil {
		if transport, ok := c.httpClient.Transport.(interface{ CloseIdleConnections() }); ok {
			transport.CloseIdleConnections()
		}
	}
}

// CheckReadiness reports static composition readiness without contacting Sub2.
// Phase 1 deliberately remains not ready until the user-level execution
// dispatcher is wired; local provider calls must not be used as a fallback.
func (c *Client) CheckReadiness() (bool, string) {
	if c == nil || c.httpClient == nil {
		return false, "not_configured"
	}
	if !c.executionConfigured {
		return false, "execution_dispatch_not_configured"
	}
	return true, "configured"
}

// GetIdentityStatus revalidates a persisted Sub2 identity and epoch.
func (c *Client) GetIdentityStatus(ctx context.Context, req sub2.IdentityStatusRequest) (*sub2.IdentityStatusResponse, error) {
	if err := validateBoundIdentityRequest(req.Identity); err != nil {
		return nil, err
	}
	target, err := c.buildURL(identityStatusPath)
	if err != nil {
		return nil, err
	}
	var result sub2.IdentityStatusResponse
	if err := c.doJSON(ctx, http.MethodPost, target, req, &result); err != nil {
		return nil, err
	}
	if err := validateIdentityEcho(req.Identity, result.Identity); err != nil {
		return nil, err
	}
	if result.IdentityEpoch == 0 || strings.TrimSpace(result.Status) == "" || !result.PermissionVersionPresent || !domainsub2.IsPermissionVersionValid(result.PermissionVersion) {
		return nil, fmt.Errorf("%w: identity status role/version is required", ErrInvalidResponse)
	}
	if _, ok := domainsub2.MapExternalRole(result.Role); !ok {
		return nil, fmt.Errorf("%w: identity status role is missing or unknown", ErrInvalidResponse)
	}
	return &result, nil
}

// ExchangeSubject exchanges a validated OIDC subject for a short-lived service assertion.
func (c *Client) ExchangeSubject(ctx context.Context, req sub2.SubjectExchangeRequest) (*sub2.SubjectExchangeResponse, error) {
	if err := validateIdentityRequest(req.Identity); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.SubjectToken) == "" {
		return nil, fmt.Errorf("%w: subject exchange subject_token is required", ErrInvalidResponse)
	}
	if strings.TrimSpace(req.Audience) == "" || strings.TrimSpace(req.Purpose) == "" {
		return nil, fmt.Errorf("%w: subject exchange audience and purpose are required", ErrInvalidResponse)
	}
	target, err := c.buildURL(subjectExchangePath)
	if err != nil {
		return nil, err
	}
	var result sub2.SubjectExchangeResponse
	if err := c.doJSON(ctx, http.MethodPost, target, req, &result); err != nil {
		return nil, err
	}
	if err := validateIdentityEcho(req.Identity, result.Identity); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Assertion) == "" || result.Identity.IdentityEpoch == 0 || result.ExpiresAt.IsZero() ||
		result.ExpiresAt.Before(time.Now()) || !result.PermissionVersionPresent || !domainsub2.IsPermissionVersionValid(result.PermissionVersion) {
		return nil, fmt.Errorf("%w: subject exchange response is incomplete", ErrInvalidResponse)
	}
	if _, ok := domainsub2.MapExternalRole(result.Role); !ok {
		return nil, fmt.Errorf("%w: subject exchange role is missing or unknown", ErrInvalidResponse)
	}
	return &result, nil
}

// GetWallet retrieves a non-authoritative display/authorization snapshot.
func (c *Client) GetWallet(ctx context.Context, req sub2.WalletQueryRequest) (*sub2.WalletResponse, error) {
	if err := validateBoundIdentityRequest(req.Identity); err != nil {
		return nil, err
	}
	target, err := c.buildURL(walletPath)
	if err != nil {
		return nil, err
	}
	var result sub2.WalletResponse
	if err := c.doJSON(ctx, http.MethodPost, target, req, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Currency) == "" || result.CheckedAt.IsZero() {
		return nil, fmt.Errorf("%w: wallet response is incomplete", ErrInvalidResponse)
	}
	return &result, nil
}

// ListModels retrieves the Sub2-authoritative model catalog for a subject.
func (c *Client) ListModels(ctx context.Context, req sub2.ModelCatalogRequest) (*sub2.ModelCatalogResponse, error) {
	if err := validateBoundIdentityRequest(req.Identity); err != nil {
		return nil, err
	}
	target, err := c.buildURL(modelCatalogPath)
	if err != nil {
		return nil, err
	}
	var result sub2.ModelCatalogResponse
	if err := c.doJSON(ctx, http.MethodPost, target, req, &result); err != nil {
		return nil, err
	}
	if result.CheckedAt.IsZero() {
		return nil, fmt.Errorf("%w: model catalog response is incomplete", ErrInvalidResponse)
	}
	return &result, nil
}

// AdmitModel asks Sub2 to authorize one concrete model request.
func (c *Client) AdmitModel(ctx context.Context, req sub2.ModelAdmissionRequest) (*sub2.ModelAdmissionResponse, error) {
	if err := validateBoundIdentityRequest(req.Identity); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Model) == "" || strings.TrimSpace(req.RequestHash) == "" {
		return nil, fmt.Errorf("%w: model and request hash are required", ErrInvalidResponse)
	}
	target, err := c.buildURL(modelAdmissionPath)
	if err != nil {
		return nil, err
	}
	var result sub2.ModelAdmissionResponse
	if err := c.doJSON(ctx, http.MethodPost, target, req, &result); err != nil {
		return nil, err
	}
	if result.IdentityEpoch == 0 {
		return nil, fmt.Errorf("%w: model admission identity epoch is required", ErrInvalidResponse)
	}
	return &result, nil
}

// QueryExecution queries the authoritative state before any recovery retry.
func (c *Client) QueryExecution(ctx context.Context, req sub2.ExecutionQueryRequest) (*sub2.ExecutionQueryResponse, error) {
	if err := validateBoundIdentityRequest(req.Identity); err != nil {
		return nil, err
	}
	if err := validateExecutionID(req.ExecutionID); err != nil {
		return nil, err
	}
	target, err := c.buildURL(executionQueryPath)
	if err != nil {
		return nil, err
	}
	var result sub2.ExecutionQueryResponse
	if err := c.doJSON(ctx, http.MethodPost, target, req, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.ExecutionID) != strings.TrimSpace(req.ExecutionID) {
		return nil, ErrExecutionMismatch
	}
	if result.IdentityEpoch == 0 {
		return nil, fmt.Errorf("%w: execution identity epoch is required", ErrInvalidResponse)
	}
	return &result, nil
}

// CancelExecution requests an idempotent cancellation at the authority.
func (c *Client) CancelExecution(ctx context.Context, req sub2.ExecutionCancelRequest) (*sub2.ExecutionCancelResponse, error) {
	if err := validateBoundIdentityRequest(req.Identity); err != nil {
		return nil, err
	}
	if err := validateExecutionID(req.ExecutionID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return nil, fmt.Errorf("%w: cancellation idempotency key is required", ErrInvalidResponse)
	}
	target, err := c.buildURL(executionCancelPath)
	if err != nil {
		return nil, err
	}
	var result sub2.ExecutionCancelResponse
	if err := c.doJSON(ctx, http.MethodPost, target, req, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.ExecutionID) != strings.TrimSpace(req.ExecutionID) {
		return nil, ErrExecutionMismatch
	}
	if result.IdentityEpoch == 0 {
		return nil, fmt.Errorf("%w: cancellation identity epoch is required", ErrInvalidResponse)
	}
	return &result, nil
}

var _ sub2.Client = (*Client)(nil)

func (c *Client) EnableExecutionDispatcher() {
	if c != nil {
		c.executionConfigured = true
	}
}
