package memory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

func TestConsumeProviderAuthTransactionIfBrowserBindingMatchesPreservesOnMismatch(t *testing.T) {
	cache := New()
	item := repository.ProviderAuthTransaction{BrowserBindingHash: "binding-hash", ExpiresAt: time.Now().Add(time.Minute)}
	if err := cache.PutProviderAuthTransaction(context.Background(), "state", item, time.Minute); err != nil {
		t.Fatalf("put transaction: %v", err)
	}

	if _, err := cache.ConsumeProviderAuthTransactionIfBrowserBindingMatches(context.Background(), "state", "wrong-hash"); !errors.Is(err, repository.ErrProviderAuthTransactionBindingMismatch) {
		t.Fatalf("wrong binding error = %v, want binding mismatch", err)
	}
	got, err := cache.ConsumeProviderAuthTransactionIfBrowserBindingMatches(context.Background(), "state", "binding-hash")
	if err != nil {
		t.Fatalf("matching binding should consume preserved transaction: %v", err)
	}
	if got == nil || got.BrowserBindingHash != item.BrowserBindingHash {
		t.Fatalf("unexpected consumed transaction: %#v", got)
	}
	if _, err = cache.ConsumeProviderAuthTransactionIfBrowserBindingMatches(context.Background(), "state", "binding-hash"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("replay error = %v, want not found", err)
	}
}

func TestConsumeProviderAuthTransactionIfBrowserBindingMatchesDoesNotConsumeExpired(t *testing.T) {
	cache := New()
	item := repository.ProviderAuthTransaction{BrowserBindingHash: "binding-hash", ExpiresAt: time.Now().Add(-time.Second)}
	if err := cache.PutProviderAuthTransaction(context.Background(), "state", item, time.Millisecond); err != nil {
		t.Fatalf("put transaction: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := cache.ConsumeProviderAuthTransactionIfBrowserBindingMatches(context.Background(), "state", "binding-hash"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expired error = %v, want not found", err)
	}
}

func TestConsumeProviderAuthTransactionIfBrowserBindingMatchesIsSingleWinner(t *testing.T) {
	cache := New()
	if err := cache.PutProviderAuthTransaction(context.Background(), "state", repository.ProviderAuthTransaction{BrowserBindingHash: "binding-hash"}, time.Minute); err != nil {
		t.Fatalf("put transaction: %v", err)
	}

	const attempts = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cache.ConsumeProviderAuthTransactionIfBrowserBindingMatches(context.Background(), "state", "binding-hash"); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			} else if !errors.Is(err, repository.ErrNotFound) {
				t.Errorf("concurrent consume error = %v, want not found for losers", err)
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successful concurrent consumes = %d, want 1", successes)
	}
}
