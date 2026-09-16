package billing

import (
	"context"
	"errors"
	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/apperr"
)

var (
	// ErrSub2AuthorityRequired is fail-closed when local wallet/settlement is disabled.
	ErrSub2AuthorityRequired = apperr.New("sub2.billing_authority_required", "Sub2 billing authority is required")
)

// externalIdentityChecker is called before paid operations in Sub2 authority mode.
type externalIdentityChecker interface {
	CheckExternalIdentity(ctx context.Context, userID uint) error
}

// SetLocalBillingEnabled explicitly selects whether Chat's local wallet and
// settlement implementation may be used. The default constructor keeps local
// behavior for existing callers; the composition root must set false in Sub2 mode.
func (s *Service) SetLocalBillingEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.localBillingEnabled = enabled
	s.billingAuthorityConfigured = true
}

// SetExternalIdentityChecker wires the status/epoch check used before paid work.
func (s *Service) SetExternalIdentityChecker(checker externalIdentityChecker) {
	if s == nil {
		return
	}
	s.externalIdentityChecker = checker
}

func (s *Service) localBillingAllowed() bool {
	if s == nil || !s.billingAuthorityConfigured {
		return true
	}
	return s.localBillingEnabled
}

func (s *Service) requireLocalBilling() error {
	if s.localBillingAllowed() {
		return nil
	}
	return ErrSub2AuthorityRequired
}

func (s *Service) checkExternalIdentity(ctx context.Context, userID uint) error {
	if s.localBillingAllowed() || s.externalIdentityChecker == nil {
		if !s.localBillingAllowed() && s.externalIdentityChecker == nil {
			return ErrSub2AuthorityRequired
		}
		return nil
	}
	if userID == 0 {
		return ErrSub2AuthorityRequired
	}
	if err := s.externalIdentityChecker.CheckExternalIdentity(ctx, userID); err != nil {
		return err
	}
	return nil
}

// IsSub2BillingAuthority reports whether Chat local money effects are disabled.
func (s *Service) IsSub2BillingAuthority() bool {
	return !s.localBillingAllowed()
}

// IsSub2AuthorityError allows transport layers to map the fail-closed error without
// importing configuration or repository packages.
func IsSub2AuthorityError(err error) bool {
	return errors.Is(err, ErrSub2AuthorityRequired)
}

// Sub2UsageAuthority keeps the existing usage-session lifecycle but replaces
// local reservation/debit with authority admission and receipt projection.
type Sub2UsageAuthority interface {
	AuthorizeSub2Usage(context.Context, uint, string, string) (*domainbilling.UsageAuthorization, error)
	BuildSub2UsageLedger(context.Context, UsagePricingInput) (*domainbilling.UsageLedger, error)
	RecordSub2Usage(context.Context, *domainbilling.UsageLedger, *domainbilling.UsageAuthorization) error
}

func (s *Service) SetSub2UsageAuthority(authority Sub2UsageAuthority) { s.sub2Usage = authority }
