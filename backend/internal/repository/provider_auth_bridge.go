package repository

import (
	"context"
	"errors"
	"time"
)

// ProviderAuthTransaction is the short-lived server-side state for an OAuth
// provider authorization started by a public client.
type ProviderAuthTransaction struct {
	ProviderSlug         string    `json:"providerSlug"`
	ClientID             string    `json:"clientID"`
	ClientRedirectURI    string    `json:"clientRedirectURI"`
	ClientState          string    `json:"clientState"`
	ClientCodeChallenge  string    `json:"clientCodeChallenge"`
	ProviderCodeVerifier string    `json:"providerCodeVerifier"`
	ProviderNonce        string    `json:"providerNonce,omitempty"`
	ProviderIssuer       string    `json:"providerIssuer,omitempty"`
	BrowserBindingHash   string    `json:"browserBindingHash,omitempty"`
	Intent               string    `json:"intent"`
	Next                 string    `json:"next"`
	ExpiresAt            time.Time `json:"expiresAt"`
}

// ProviderAuthGrant is the one-time handoff from the server callback to the
// public client. Sensitive provider codes and tokens never leave the server.
type ProviderAuthGrant struct {
	ProviderSlug string    `json:"providerSlug"`
	ClientID     string    `json:"clientID"`
	UserID       uint      `json:"userID"`
	Subject      string    `json:"subject"`
	ErrorCode    string    `json:"errorCode,omitempty"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	ErrorDetails string    `json:"errorDetails,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// ErrProviderAuthTransactionBindingMismatch means the transaction exists but
// its browser binding condition did not match. The transaction remains usable.
var ErrProviderAuthTransactionBindingMismatch = errors.New("provider auth transaction browser binding mismatch")

// ProviderAuthBridgeRepository stores and atomically consumes the short-lived
// transaction and grant records used by the provider auth bridge.
type ProviderAuthBridgeRepository interface {
	PutProviderAuthTransaction(ctx context.Context, id string, item ProviderAuthTransaction, ttl time.Duration) error
	ConsumeProviderAuthTransaction(ctx context.Context, id string) (*ProviderAuthTransaction, error)
	// ConsumeProviderAuthTransactionIfBrowserBindingMatches atomically compares
	// the already-hashed browser binding and deletes only on a match. A mismatch
	// must return ErrProviderAuthTransactionBindingMismatch without consuming the
	// transaction.
	ConsumeProviderAuthTransactionIfBrowserBindingMatches(ctx context.Context, id string, browserBindingHash string) (*ProviderAuthTransaction, error)
	PutProviderAuthGrant(ctx context.Context, key string, item ProviderAuthGrant, ttl time.Duration) error
	ConsumeProviderAuthGrant(ctx context.Context, key string) (*ProviderAuthGrant, error)
}
