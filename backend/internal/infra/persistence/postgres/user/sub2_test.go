package user

import "testing"

func TestShouldRevokeSub2SessionsOnIdentityEpochPromotion(t *testing.T) {
	if !shouldRevokeSub2Sessions("user", "active", "admin", "active", 41, 42) {
		t.Fatal("identity epoch changes must revoke sessions even when the role is promoted")
	}
}

func TestShouldRevokeSub2SessionsKeepsSameEpochPromotionNonRevoking(t *testing.T) {
	if shouldRevokeSub2Sessions("user", "active", "admin", "active", 41, 41) {
		t.Fatal("same-epoch role projection should not be treated as an identity epoch change")
	}
}
