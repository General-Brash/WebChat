"use client";
import * as React from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAppLocale } from "@/i18n/app-i18n-provider";
import { sub2Action, sub2Portal, type Sub2Action, type Sub2Status } from "./api";
import { useSub2Authority } from "./use-sub2";

type Wallet = { permanent_available_nanousd: number; temporary_available_nanousd: number; temporary_expires_at?: string; checked_at: string };
type Plan = { id: number; name: string; description?: string; price: number; currency?: string; validity_days: number; payment_credit_type: string; benefit_type: string };
type Quote = { product_type: string; product_id: number; name: string; price: string; currency: string; payment_credit_type: string; quote_version: string };
type LedgerRow = { row_id: string; id: number; label: string; model?: string; category: string; amount: string; currency: string; permanent_delta?: string; temporary_delta?: string; created_at: string };
type Ledger = { items: LedgerRow[]; page: number; pages: number; total: number };
type Order = { id: number; out_trade_no?: string; amount: number; currency?: string; status: string; created_at: string };
type PaymentConfig = { enabled: boolean; enabled_payment_types?: string[]; min_amount?: number; max_amount?: number };

export function Sub2FinancePanel({ admin = false, status: supplied }: { admin?: boolean; status?: Sub2Status }) {
  const { locale } = useAppLocale();
  const text = (zh: string, en: string) => locale === "zh-CN" ? zh : en;
  const authority = useSub2Authority();
  const status = supplied || authority.status;
  const [wallet, setWallet] = React.useState<Wallet | null>(null);
  const [plans, setPlans] = React.useState<Plan[]>([]);
  const [config, setConfig] = React.useState<PaymentConfig | null>(null);
  const [orders, setOrders] = React.useState<Order[]>([]);
  const [ledger, setLedger] = React.useState<Ledger | null>(null);
  const [page, setPage] = React.useState(1);
  const [code, setCode] = React.useState("");
  const [paymentType, setPaymentType] = React.useState("");
  const [amount, setAmount] = React.useState("");
  const [targetUser, setTargetUser] = React.useState("");
  const [notes, setNotes] = React.useState("");
  const [quote, setQuote] = React.useState<Quote | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  const [notice, setNotice] = React.useState("");
  const [checkout, setCheckout] = React.useState<{ order_id: number; pay_url?: string } | null>(null);
  const intent = React.useRef<{ signature: string; key: string } | null>(null);
  const inFlight = React.useRef(false);
  const reload = React.useCallback(async () => {
    if (!status?.enabled) return;
    const result = await Promise.allSettled([
      sub2Action<Wallet>("wallet"), sub2Action<Plan[]>("finance-plans"), sub2Action<PaymentConfig>("finance-config"),
      sub2Action<Ledger>(admin ? "finance-admin-ledger" : "finance-ledger", { query: `page=${page}&days=30` }, admin),
      sub2Action<Order[] | { items: Order[] }>(admin ? "finance-admin-orders" : "finance-orders", { query: "page=1&page_size=20" }, admin),
    ]);
    if (result[0].status === "fulfilled") setWallet(result[0].value);
    if (result[1].status === "fulfilled") setPlans(result[1].value || []);
    if (result[2].status === "fulfilled") setConfig(result[2].value);
    if (result[3].status === "fulfilled") setLedger(result[3].value);
    if (result[4].status === "fulfilled") { const value = result[4].value; setOrders(Array.isArray(value) ? value : value.items || []); }
    const failed = result.find((item) => item.status === "rejected");
    if (failed?.status === "rejected") setError(failed.reason instanceof Error ? failed.reason.message : "Sub2 unavailable");
  }, [admin, page, status?.enabled]);
  React.useEffect(() => { void reload(); }, [reload]);
  const mutate = async <T,>(operation: string, body: Sub2Action, operator = false): Promise<T | null> => {
    if (inFlight.current) return null;
    inFlight.current = true; setBusy(true); setError(""); setNotice("");
    const signature = JSON.stringify([operation, body]);
    if (!intent.current || intent.current.signature !== signature) intent.current = { signature, key: crypto.randomUUID() };
    try {
      const result = await sub2Action<T>(operation, { ...body, idempotencyKey: intent.current.key }, operator);
      intent.current = null; setNotice(text("Sub2 已受理，请以权威订单/流水状态为准。", "Sub2 accepted the operation. Its order/ledger state is authoritative.")); await reload(); return result;
    } catch (cause) { setError((cause instanceof Error ? cause.message : "Sub2 unavailable") + text("。结果不明时先查原订单或流水，不要创建重复操作。", ". If the result is unknown, check the original order or ledger before creating another operation.")); return null; }
    finally { inFlight.current = false; setBusy(false); }
  };
  if (!status?.enabled) return null;
  const portal = sub2Portal(status.issuer);
  const dollars = (value: number) => (value / 1e9).toLocaleString(locale, { maximumFractionDigits: 8 });
  const showQuote = async (plan: Plan) => { setError(""); try { setQuote(await sub2Action<Quote>("finance-quote", { query: `product_type=subscription&product_id=${plan.id}` })); } catch (cause) { setError(cause instanceof Error ? cause.message : "Sub2 unavailable"); } };
  const beginCheckout = async (plan?: Plan) => {
    const result = await mutate<{ order_id: number; pay_url?: string }>("finance-checkout", { command: { payment_type: paymentType, order_type: plan ? "subscription" : "balance", plan_id: plan?.id || 0, amount: plan ? plan.price : Number(amount) } });
    if (result) setCheckout(result);
  };
  let payURL: string | null = null;
  if (checkout?.pay_url) { try { const url = new URL(checkout.pay_url); if (["https:", "http:"].includes(url.protocol)) payURL = url.href; } catch { /* Invalid provider URL is never navigated. */ } }
  return <section className="space-y-5 rounded-lg border bg-card p-4">
    <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="text-sm font-semibold">{text(admin ? "Sub2 财务管理" : "Sub2 钱包与套餐", admin ? "Sub2 financial management" : "Sub2 wallet and products")}</h3><Button size="sm" variant="outline" disabled={busy} onClick={() => { setError(""); void reload(); }}>{text("刷新权威记录", "Refresh authoritative records")}</Button></div>
    <p className="text-xs text-muted-foreground">{text("身份、永久/临时余额、订单和退款均由 Sub2 管理；Chat 不维护第二份资金账。模型显示待核对时不代表免费，也不要重复生成。", "Identity, permanent/temporary credit, orders and refunds belong to Sub2. Chat has no second wallet. Pending settlement does not mean free usage; do not replay a generation.")}</p>
    {wallet && <div className="grid gap-3 sm:grid-cols-2"><div className="rounded-md border p-3"><div className="text-xs text-muted-foreground">{text("永久可用余额", "Permanent available credit")}</div><div className="mt-1 text-lg">USD {dollars(wallet.permanent_available_nanousd)}</div></div><div className="rounded-md border p-3"><div className="text-xs text-muted-foreground">{text("临时可用余额", "Temporary available credit")}</div><div className="mt-1 text-lg">USD {dollars(wallet.temporary_available_nanousd)}</div>{wallet.temporary_expires_at && <small>{new Date(wallet.temporary_expires_at).toLocaleString(locale)}</small>}</div><p className="text-xs text-muted-foreground">{text("查询时间", "Checked at")}: {new Date(wallet.checked_at).toLocaleString(locale)}</p></div>}
    <div className="flex flex-wrap gap-2"><Input aria-label={text("兑换码", "Redemption code")} value={code} onChange={(event) => setCode(event.target.value)} className="max-w-sm" /><Button disabled={busy || !code.trim()} onClick={() => void mutate("finance-redeem", { command: { code: code.trim() } })}>{text("由 Sub2 兑换", "Redeem with Sub2")}</Button></div>
    <div className="flex flex-wrap items-end gap-2"><label className="space-y-1 text-xs">{text("支付渠道", "Payment method")}<select className="h-9 rounded-md border bg-background px-2" value={paymentType} onChange={(event) => setPaymentType(event.target.value)}><option value="">{text("请选择", "Select")}</option>{(config?.enabled_payment_types || []).map((item) => <option key={item} value={item}>{item}</option>)}</select></label><Input aria-label={text("充值金额", "Top-up amount")} value={amount} onChange={(event) => setAmount(event.target.value)} type="number" min={config?.min_amount || 0} max={config?.max_amount || undefined} className="max-w-40" /><Button disabled={busy || !config?.enabled || !paymentType || !(Number(amount) > 0)} onClick={() => void beginCheckout()}>{text("创建 Sub2 充值订单", "Create Sub2 top-up order")}</Button></div>
    {checkout && <div role="status" className="space-x-3 rounded-md border p-3 text-sm"><span>{text("订单", "Order")} #{checkout.order_id}</span>{payURL && <a className="underline" href={payURL} target="_blank" rel="noopener noreferrer">{text("继续支付", "Continue payment")}</a>}{portal && <a className="underline" href={`${portal}/mall`} target="_blank" rel="noopener noreferrer">{text("二维码、内嵌支付或渠道验证请在 Sub2 完成", "Complete QR, embedded payment or provider verification in Sub2")}</a>}</div>}
    <div className="grid gap-3 sm:grid-cols-2">{plans.map((plan) => <div key={plan.id} className="space-y-2 rounded-md border p-3"><h4 className="text-sm font-medium">{plan.name}</h4><p className="text-xs text-muted-foreground">{plan.description}</p><p className="text-sm">{plan.currency || "USD"} {plan.price} · {plan.validity_days} {text("天", "days")} · {plan.benefit_type}</p><div className="flex flex-wrap gap-2"><Button size="sm" variant="outline" disabled={busy} onClick={() => void showQuote(plan)}>{text("查看余额购买报价", "Get credit purchase quote")}</Button>{plan.benefit_type !== "daily_temporary_credit" && <Button size="sm" disabled={busy || !paymentType || !config?.enabled} onClick={() => void beginCheckout(plan)}>{text("外部支付", "External checkout")}</Button>}</div></div>)}</div>
    {quote && <div className="space-y-2 rounded-md border p-3"><p className="text-sm">{quote.name} · {quote.currency} {quote.price} · {quote.payment_credit_type}</p><p className="text-xs text-muted-foreground">{text("以下购买绑定当前报价版本；变价会拒绝，不会静默改价。", "The purchase binds this quote version. Changed prices are rejected, not silently substituted.")}</p><Button disabled={busy} onClick={async () => { const result = await mutate("finance-purchase", { command: { product_type: quote.product_type, product_id: quote.product_id, expected_quote_version: quote.quote_version } }); if (result) setQuote(null); }}>{text("确认按此报价购买", "Confirm this quoted purchase")}</Button></div>}
    {admin && <div className="space-y-2 rounded-md border p-3"><h4 className="text-sm font-medium">{text("授权调账/赠送", "Authorized adjustment/grant")}</h4><p className="text-xs text-muted-foreground">{text("填写 Chat 用户 ID，由服务端映射目标；Sub2 会再次检查操作者的具体权限。", "Enter the Chat user ID; the server resolves the target. Sub2 rechecks the operator's specific permissions.")}</p><div className="flex flex-wrap gap-2"><Input aria-label="Chat user ID" placeholder="Chat user ID" value={targetUser} onChange={(event) => setTargetUser(event.target.value)} className="max-w-36" /><Input aria-label={text("金额", "Amount")} value={amount} onChange={(event) => setAmount(event.target.value)} className="max-w-36" /><Input aria-label={text("原因", "Reason")} placeholder={text("原因", "Reason")} value={notes} onChange={(event) => setNotes(event.target.value)} className="max-w-sm" /></div><div className="flex flex-wrap gap-2"><Button size="sm" disabled={busy || !(Number(targetUser) > 0) || !(Number(amount) > 0) || !notes.trim()} onClick={() => void mutate("finance-admin-balance", { targetUserID: Number(targetUser), command: { balance: Number(amount), operation: "add", notes } }, true)}>{text("增加永久余额", "Add permanent credit")}</Button><Button size="sm" variant="outline" disabled={busy || !(Number(targetUser) > 0) || !(Number(amount) > 0) || !notes.trim()} onClick={() => void mutate("finance-admin-temporary-credit", { targetUserID: Number(targetUser), command: { amount, notes } }, true)}>{text("按 Sub2 规则赠送临时余额", "Grant temporary credit under Sub2 rules")}</Button>{portal && <a className="self-center text-xs underline" href={`${portal}/admin/subscriptions`} target="_blank" rel="noopener noreferrer">{text("管理 Sub2 产品与订阅", "Manage Sub2 products and subscriptions")}</a>}</div></div>}
    {orders.length > 0 && <div className="overflow-auto"><table className="w-full text-left text-xs"><thead><tr><th className="p-2">{text("订单", "Order")}</th><th className="p-2">{text("金额", "Amount")}</th><th className="p-2">{text("状态", "State")}</th><th className="p-2">{text("操作", "Action")}</th></tr></thead><tbody>{orders.map((order) => <tr key={order.id} className="border-t"><td className="p-2">{order.out_trade_no || order.id}</td><td className="p-2">{order.currency || ""} {order.amount}</td><td className="p-2">{order.status}</td><td className="p-2">{!admin && order.status === "pending" && <Button size="sm" variant="ghost" disabled={busy} onClick={() => void mutate("finance-cancel-order", { id: order.id })}>{text("取消", "Cancel")}</Button>}{!admin && order.status === "completed" && <Button size="sm" variant="ghost" disabled={busy} onClick={() => void mutate("finance-refund-request", { id: order.id, command: { reason: text("用户从 Chat 申请退款", "Refund requested from Chat") } })}>{text("申请退款", "Request refund")}</Button>}{admin && portal && <a className="underline" href={`${portal}/admin/orders/mall-transactions`} target="_blank" rel="noopener noreferrer">{text("在 Sub2 审核/处理退款", "Review/refund in Sub2")}</a>}</td></tr>)}</tbody></table></div>}
    {ledger && <div className="space-y-2 overflow-auto"><h4 className="text-sm font-medium">{text("Sub2 权威流水", "Sub2 authoritative ledger")}</h4><table className="w-full text-left text-xs"><thead><tr><th className="p-2">{text("时间/事项", "Time / event")}</th><th className="p-2">{text("金额", "Amount")}</th><th className="p-2">{text("永久变动", "Permanent delta")}</th><th className="p-2">{text("临时变动", "Temporary delta")}</th></tr></thead><tbody>{(ledger.items || []).map((row) => <tr key={row.row_id || row.id} className="border-t"><td className="p-2">{row.label} {row.model || ""}<small className="block text-muted-foreground">{new Date(row.created_at).toLocaleString(locale)}</small></td><td className="p-2">{row.currency} {row.amount}</td><td className="p-2">{row.permanent_delta || "—"}</td><td className="p-2">{row.temporary_delta || "—"}</td></tr>)}</tbody></table><div className="flex items-center gap-2"><Button size="sm" variant="outline" disabled={page <= 1} onClick={() => setPage((value) => value - 1)}>{text("上一页", "Previous")}</Button><span className="text-xs">{page} / {Math.max(ledger.pages || 1, 1)}</span><Button size="sm" variant="outline" disabled={page >= ledger.pages} onClick={() => setPage((value) => value + 1)}>{text("下一页", "Next")}</Button></div></div>}
    {notice && <p role="status" className="text-xs text-muted-foreground">{notice}</p>}{error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>;
}
