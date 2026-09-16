package memory

import (
	"context"
	"crypto/subtle"
	"strings"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type expiringProviderAuthTransaction struct {
	value     repository.ProviderAuthTransaction
	expiresAt time.Time
}

type expiringProviderAuthGrant struct {
	value     repository.ProviderAuthGrant
	expiresAt time.Time
}

// PutProviderAuthTransaction stores a short-lived provider authentication transaction.
func (c *Cache) PutProviderAuthTransaction(_ context.Context, id string, item repository.ProviderAuthTransaction, ttl time.Duration) error {
	if c == nil || id == "" || ttl <= 0 {
		return repository.ErrInvalidInput
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.maybeSweepLocked(now)
	c.providerAuthTransactions[id] = expiringProviderAuthTransaction{value: item, expiresAt: now.Add(ttl)}
	return nil
}

// ConsumeProviderAuthTransaction retrieves and deletes a provider authentication transaction.
func (c *Cache) ConsumeProviderAuthTransaction(_ context.Context, id string) (*repository.ProviderAuthTransaction, error) {
	if c == nil || id == "" {
		return nil, repository.ErrNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.providerAuthTransactions[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	delete(c.providerAuthTransactions, id)
	if time.Now().After(item.expiresAt) {
		return nil, repository.ErrNotFound
	}
	value := item.value
	return &value, nil
}

// ConsumeProviderAuthTransactionIfBrowserBindingMatches atomically consumes a
// transaction only when its stored browser binding hash matches the supplied
// hash. A mismatch leaves the transaction available for the rightful browser.
func (c *Cache) ConsumeProviderAuthTransactionIfBrowserBindingMatches(_ context.Context, id string, browserBindingHash string) (*repository.ProviderAuthTransaction, error) {
	if c == nil || id == "" {
		return nil, repository.ErrNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.providerAuthTransactions[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	now := time.Now()
	if now.After(item.expiresAt) {
		delete(c.providerAuthTransactions, id)
		return nil, repository.ErrNotFound
	}
	expectedHash := strings.TrimSpace(item.value.BrowserBindingHash)
	actualHash := strings.TrimSpace(browserBindingHash)
	if expectedHash == "" || actualHash == "" || subtle.ConstantTimeCompare([]byte(expectedHash), []byte(actualHash)) != 1 {
		return nil, repository.ErrProviderAuthTransactionBindingMismatch
	}
	delete(c.providerAuthTransactions, id)
	value := item.value
	return &value, nil
}

// PutProviderAuthGrant stores a short-lived provider authentication grant.
func (c *Cache) PutProviderAuthGrant(_ context.Context, key string, item repository.ProviderAuthGrant, ttl time.Duration) error {
	if c == nil || key == "" || ttl <= 0 {
		return repository.ErrInvalidInput
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.maybeSweepLocked(now)
	c.providerAuthGrants[key] = expiringProviderAuthGrant{value: item, expiresAt: now.Add(ttl)}
	return nil
}

// ConsumeProviderAuthGrant retrieves and deletes a provider authentication grant.
func (c *Cache) ConsumeProviderAuthGrant(_ context.Context, key string) (*repository.ProviderAuthGrant, error) {
	if c == nil || key == "" {
		return nil, repository.ErrNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.providerAuthGrants[key]
	if !ok {
		return nil, repository.ErrNotFound
	}
	delete(c.providerAuthGrants, key)
	if time.Now().After(item.expiresAt) {
		return nil, repository.ErrNotFound
	}
	value := item.value
	return &value, nil
}
