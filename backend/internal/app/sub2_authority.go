package app

import (
	"context"
	"time"

	appbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/billing"
	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
	llmport "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

// sub2Readiness is intentionally static: readiness must not probe external
// systems during composition or compile-only validation.
type sub2Readiness interface {
	CheckReadiness() (bool, string)
}

type llmExecutionGateway interface {
	Generate(context.Context, llmport.RouteConfig, llmport.GenerateInput) (*llmport.GenerateOutput, error)
	ListModels(context.Context, llmport.RouteConfig) ([]llmport.ModelItem, error)
	GenerateStream(context.Context, llmport.RouteConfig, llmport.GenerateInput, func(llmport.GenerateStreamEvent) error) (*llmport.GenerateOutput, error)
	RetrieveOpenAIResponse(context.Context, llmport.RouteConfig, string) (*llmport.GenerateOutput, error)
	CancelOpenAIResponse(context.Context, llmport.RouteConfig, string) (*llmport.GenerateOutput, error)
}

// sub2BillingRepository blocks local money effects, but retains Chat retail configuration while
// retaining the interface shape required by existing application services.
type sub2BillingRepository struct {
	repository.BillingRepository
}

func (r *sub2BillingRepository) blocked() error {
	return appbilling.ErrSub2AuthorityRequired
}

func (r *sub2BillingRepository) GetBillingMode(context.Context) (string, error) {
	return "sub2", nil
}

func (r *sub2BillingRepository) ListActivePlans(context.Context) ([]domainbilling.Plan, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) ListActivePricesByPlanIDs(context.Context, []uint) ([]domainbilling.Price, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) GetPriceByID(context.Context, uint) (*domainbilling.Price, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) GetPlanByID(context.Context, uint) (*domainbilling.Plan, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) ListPlansByIDs(context.Context, []uint) ([]domainbilling.Plan, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) GetActivePlanByCode(context.Context, string) (*domainbilling.Plan, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) UpdatePlanWithDefaultPrice(context.Context, *domainbilling.Plan, *domainbilling.Price) error {
	return r.blocked()
}
func (r *sub2BillingRepository) ListSubscriptionEntitlementsByUserIDs(context.Context, []uint, time.Time) ([]domainbilling.Subscription, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) ReplaceSubscription(context.Context, *domainbilling.Subscription) error {
	return r.blocked()
}
func (r *sub2BillingRepository) CreatePaymentOrder(context.Context, *domainbilling.PaymentOrder) (*domainbilling.PaymentOrder, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) UpdatePaymentOrderCheckout(context.Context, string, string, string) error {
	return r.blocked()
}
func (r *sub2BillingRepository) GetPaymentOrderByOrderNo(context.Context, string) (*domainbilling.PaymentOrder, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) MarkPaymentOrderPaidAndGrantSubscription(context.Context, string, string, time.Time, *domainbilling.Subscription) (*domainbilling.PaymentOrder, bool, error) {
	return nil, false, r.blocked()
}
func (r *sub2BillingRepository) AddUsage(context.Context, *domainbilling.UsageLedger) error {
	return r.blocked()
}
func (r *sub2BillingRepository) AddUsageAndSettleBalance(context.Context, *domainbilling.UsageLedger, *domainbilling.UsageBalanceReservation) error {
	return r.blocked()
}
func (r *sub2BillingRepository) AddPeriodUsageAndSettleOverage(context.Context, *domainbilling.UsageLedger, time.Time, time.Time, int64, *domainbilling.UsageBalanceReservation) error {
	return r.blocked()
}
func (r *sub2BillingRepository) ReserveUsageBalance(context.Context, domainbilling.UsageBalanceReservationRequest) (*domainbilling.UsageBalanceReservation, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) RaiseUsageBalanceReservation(context.Context, uint, string, int64) error {
	return r.blocked()
}
func (r *sub2BillingRepository) RenewUsageBalanceReservation(context.Context, uint, string) error {
	return r.blocked()
}
func (r *sub2BillingRepository) ReleaseUsageBalanceReservation(context.Context, uint, string) error {
	return r.blocked()
}
func (r *sub2BillingRepository) MarkUsageReservationReconciliationRequired(context.Context, uint, string, string) error {
	return r.blocked()
}
func (r *sub2BillingRepository) GetOrCreateBillingAccount(context.Context, uint) (*domainbilling.BillingAccount, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) ListBillingAccountsByUserIDs(context.Context, []uint) ([]domainbilling.BillingAccount, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) SetBillingAccountBalance(context.Context, uint, int64, string, string) (*domainbilling.BillingAccount, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) MarkPaymentOrderPaidAndCreditBalance(context.Context, string, string, time.Time) (*domainbilling.PaymentOrder, bool, error) {
	return nil, false, r.blocked()
}
func (r *sub2BillingRepository) ListRedemptionCodes(context.Context, repository.RedemptionCodeListFilter, int, int) ([]domainbilling.RedemptionCode, int64, error) {
	return nil, 0, r.blocked()
}
func (r *sub2BillingRepository) GetRedemptionCodeByID(context.Context, uint) (*domainbilling.RedemptionCode, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) CreateRedemptionCode(context.Context, *domainbilling.RedemptionCode) (*domainbilling.RedemptionCode, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) PatchRedemptionCode(context.Context, uint, repository.RedemptionCodePatch) (*domainbilling.RedemptionCode, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) DeleteRedemptionCode(context.Context, uint) error {
	return r.blocked()
}
func (r *sub2BillingRepository) RedeemCode(context.Context, repository.RedemptionApplyInput) (*repository.RedemptionApplyResult, error) {
	return nil, r.blocked()
}
func (r *sub2BillingRepository) ListRedemptions(context.Context, repository.RedemptionListFilter, int, int) ([]repository.RedemptionRecord, int64, error) {
	return nil, 0, r.blocked()
}
func (r *sub2BillingRepository) GetBillingPrepaidAmountNanousd(context.Context) (int64, error) {
	return 0, r.blocked()
}
func (r *sub2BillingRepository) ListPaymentOrders(context.Context, repository.PaymentOrderListFilter, int, int) ([]domainbilling.PaymentOrder, int64, error) {
	return nil, 0, r.blocked()
}

var _ repository.BillingRepository = (*sub2BillingRepository)(nil)
