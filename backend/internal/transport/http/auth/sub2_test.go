package auth

import (
	"testing"

	appauth "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/auth"
)

func TestSub2LoginBindingCookieClearsOnlyAfterSuccessOrConsumedState(t *testing.T) {
	if !shouldClearSub2LoginBindingCookie(nil) {
		t.Fatal("successful callback must clear the one-time browser binding cookie")
	}
	if shouldClearSub2LoginBindingCookie(appauth.ErrSub2AuthorizationBrowserBindingMismatch) {
		t.Fatal("wrong-browser callback must preserve the local browser binding cookie")
	}
	if shouldClearSub2LoginBindingCookie(appauth.ErrSub2AuthorizationTransactionInvalid) {
		t.Fatal("unconsumed invalid/replayed callback must not clear a potentially valid local cookie")
	}
}
