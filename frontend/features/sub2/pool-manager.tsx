"use client";
import * as React from "react";
import { ArrowUp, ArrowDown, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { useAppLocale } from "@/i18n/app-i18n-provider";
import { authedRequest } from "@/shared/api/authed-client";
import { readAccessToken } from "@/shared/auth/session";
import type { AdminLLMUpstreamView } from "@/features/admin/api/llm.types";
import { listAdminLLMSettings, updateAdminLLMSetting } from "@/features/admin/api/llm";
import { readGlobalGroupPriority, moveGroupPriority, SUB2_GLOBAL_GROUP_PRIORITY_KEY } from "./group-priority";
import { sub2Action, type Sub2Group } from "./api";
import { useSub2Authority } from "./use-sub2";

type Pool = AdminLLMUpstreamView & { kind?: string; sub2GroupIDs?: number[] };
export function Sub2PoolManager({ items, onChanged }: { items: Pool[]; onChanged: () => Promise<void> }) {
  const { locale } = useAppLocale();
  const text = (zh: string, en: string) => locale === "zh-CN" ? zh : en;
  const authority = useSub2Authority();
  const [groups, setGroups] = React.useState<Sub2Group[]>([]);
  const [globalOrder, setGlobalOrder] = React.useState<number[]>([]);
  const [selected, setSelected] = React.useState<number[]>([]);
  const [target, setTarget] = React.useState(0);
  const [name, setName] = React.useState("Sub2");
  const [compatible, setCompatible] = React.useState("openai");
  const [busy, setBusy] = React.useState(false);
  const [priorityLoading, setPriorityLoading] = React.useState(false);
  const [notice, setNotice] = React.useState("");
  const [error, setError] = React.useState("");
  React.useEffect(() => {
    if (!authority.status?.enabled) return;
    let disposed = false;
    setPriorityLoading(true);
    void Promise.all([
      sub2Action<Sub2Group[]>("groups", {}, true),
      listAdminLLMSettings(readAccessToken()),
    ]).then(([rows, settings]) => {
      if (disposed) return;
      setGroups(rows || []);
      setGlobalOrder(readGlobalGroupPriority(settings));
      setError("");
    }).catch((cause: unknown) => {
      if (!disposed) setError(cause instanceof Error ? cause.message : "Sub2 unavailable");
    }).finally(() => {
      if (!disposed) setPriorityLoading(false);
    });
    return () => { disposed = true; };
  }, [authority.status?.enabled]);
  if (!authority.status?.enabled) return null;
  const pools = items.filter((item) => item.kind === "sub2");
  const selectPool = (id: number) => {
    setTarget(id); const pool = pools.find((item) => item.id === id);
    setName(pool?.name || "Sub2"); setCompatible(pool?.compatible || "openai"); setSelected(pool?.sub2GroupIDs || []); setNotice(""); setError("");
  };
  const action = async (work: () => Promise<unknown>, success: string) => {
    if (busy) return; setBusy(true); setError(""); setNotice("");
    try { await work(); setNotice(success); await onChanged(); await authority.reload(); }
    catch (cause) { setError(cause instanceof Error ? cause.message : "Sub2 unavailable"); }
    finally { setBusy(false); }
  };
  const move = (index: number, delta: number) => setSelected((ids) => moveGroupPriority(ids, index, delta));
  const moveGlobal = (index: number, delta: number) => setGlobalOrder((ids) => moveGroupPriority(ids, index, delta));
  const updateGlobalSelection = (groupID: number, checked: boolean) => setGlobalOrder((ids) => {
    if (checked) return ids.includes(groupID) ? ids : [...ids, groupID];
    return ids.filter((id) => id !== groupID);
  });
  const saveGlobalPriority = async () => {
    if (busy || priorityLoading || globalOrder.length === 0) return;
    await action(async () => {
      await updateAdminLLMSetting(readAccessToken(), SUB2_GLOBAL_GROUP_PRIORITY_KEY, JSON.stringify(globalOrder));
    }, text("全局分组优先级已保存，请继续确认分组以发布。", "Global group priority saved. Confirm groups next to publish it."));
  };
  return <section className="space-y-3 rounded-lg border bg-card p-4">
    <div className="flex flex-wrap items-center justify-between gap-2"><h4 className="text-sm font-semibold">{text("Sub2 账号池", "Sub2 account pool")}</h4><span className="text-xs text-muted-foreground">{text("分组版本", "Groups")} {authority.status.groupRevision || 0} · {text("价格版本", "Prices")} {authority.status.pricingVersion || 0}</span></div>
    <p className="text-xs text-muted-foreground">{text("仅保存分组不会开放付费。先确认分组，再使用下方原有模型同步/上架与价格管理，最后发布完整策略。实际权限始终与 Sub2 用户权限相交。", "Confirm groups first, then use the existing model sync/listing and price editor below. Publish the full policy last. User permissions are always enforced by Sub2.")}</p>
    <div className="grid gap-3 sm:grid-cols-3">
      <label className="space-y-1 text-xs">{text("账号池", "Pool")}<select className="h-9 w-full rounded-md border bg-background px-2" value={target} onChange={(event) => selectPool(Number(event.target.value))}><option value={0}>{text("新增 Sub2 账号池", "New Sub2 pool")}</option>{pools.map((pool) => <option key={pool.id} value={pool.id}>{pool.name}</option>)}</select></label>
      <label className="space-y-1 text-xs">{text("名称", "Name")}<Input value={name} onChange={(event) => setName(event.target.value)} maxLength={128} /></label>
      <label className="space-y-1 text-xs">{text("默认兼容协议", "Default compatibility")}<select className="h-9 w-full rounded-md border bg-background px-2" value={compatible} onChange={(event) => setCompatible(event.target.value)}>{["openai", "anthropic", "google", "xai", "openrouter", "custom"].map((value) => <option key={value} value={value}>{value}</option>)}</select></label>
    </div>
    <div className="max-h-56 space-y-2 overflow-auto rounded-md border p-3">{groups.length === 0 && <p className="text-xs text-muted-foreground">{text("没有可读取的分组，请检查 Sub2 配置权限。", "No readable groups. Check Sub2 configuration permissions.")}</p>}{groups.map((group) => <label key={group.group_id} className="flex items-start gap-2 text-xs"><Checkbox checked={selected.includes(group.group_id)} disabled={busy || (group.status !== "active" && !selected.includes(group.group_id))} onCheckedChange={(checked) => setSelected((ids) => checked === true ? [...ids.filter((id) => id !== group.group_id), group.group_id] : ids.filter((id) => id !== group.group_id))} /><span>{group.name} · #{group.group_id} · {group.platform} · {group.is_exclusive ? text("专属", "Exclusive") : text("公开", "Public")} · {group.status} · {text("可用账号", "Accounts")} {group.available_account_count}<small className="block text-muted-foreground">{group.observed_at ? new Date(group.observed_at).toLocaleString() : ""}</small></span></label>)}</div>
    {selected.length === 0 && <p className="text-xs text-muted-foreground">{text("空选择表示禁止使用账号池，不表示全部开放。", "An empty selection denies access; it never selects every group.")}</p>}
    <div className="flex flex-wrap gap-2">{selected.map((id, index) => <div key={id} className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs"><span>{index + 1}. #{id}</span><Button size="sm" variant="ghost" disabled={busy || index === 0} onClick={() => move(index, -1)} aria-label={text("提高优先级", "Higher priority")}><ArrowUp className="h-3 w-3" /></Button><Button size="sm" variant="ghost" disabled={busy || index === selected.length - 1} onClick={() => move(index, 1)} aria-label={text("降低优先级", "Lower priority")}><ArrowDown className="h-3 w-3" /></Button></div>)}</div>
    <div className="space-y-2 rounded-md border border-dashed p-3">
      <div className="flex flex-wrap items-center justify-between gap-2"><div><h5 className="text-xs font-semibold">{text("全局 Sub2 分组优先级", "Global Sub2 group priority")}</h5><p className="text-xs text-muted-foreground">{text("跨所有上游只配置一份顺序；重复分组只保留一次。该列表必须与已选上游分组完全一致。", "Configure one order across all upstreams; duplicate groups are kept once. The list must exactly match selected upstream groups.")}</p></div><span className="text-xs text-muted-foreground">{priorityLoading ? text("读取中…", "Loading…") : `${globalOrder.length} ${text("项", "items")}`}</span></div>
      <div className="max-h-44 space-y-2 overflow-auto rounded-md bg-muted/20 p-2">{groups.map((group) => <label key={`global-${group.group_id}`} className="flex items-start gap-2 text-xs"><Checkbox checked={globalOrder.includes(group.group_id)} disabled={busy || priorityLoading || (group.status !== "active" && !globalOrder.includes(group.group_id))} onCheckedChange={(checked) => updateGlobalSelection(group.group_id, checked === true)} /><span>{group.name} · #{group.group_id} · {group.platform} · {group.status}</span></label>)}{groups.length === 0 && <p className="text-xs text-muted-foreground">{text("没有可配置的分组。", "No groups available for configuration.")}</p>}</div>
      <div className="flex flex-wrap gap-2">{globalOrder.map((id, index) => <div key={`priority-${id}`} className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs"><span>{index + 1}. #{id}</span><Button size="sm" variant="ghost" disabled={busy || priorityLoading || index === 0} onClick={() => moveGlobal(index, -1)} aria-label={text("提高全局优先级", "Move global priority up")}><ArrowUp className="h-3 w-3" /></Button><Button size="sm" variant="ghost" disabled={busy || priorityLoading || index === globalOrder.length - 1} onClick={() => moveGlobal(index, 1)} aria-label={text("降低全局优先级", "Move global priority down")}><ArrowDown className="h-3 w-3" /></Button><Button size="sm" variant="ghost" disabled={busy || priorityLoading} onClick={() => updateGlobalSelection(id, false)} aria-label={text("移除全局分组", "Remove global group")}><X className="h-3 w-3" /></Button></div>)}</div>
      {globalOrder.length === 0 && <p className="text-xs text-destructive">{text("未配置全局顺序；发布将失败关闭，不会自动开放全部分组。", "No global order is configured; publication will fail closed and never open all groups automatically.")}</p>}
      <Button size="sm" variant="outline" disabled={busy || priorityLoading || globalOrder.length === 0} onClick={() => void saveGlobalPriority()}>{text("保存全局优先级", "Save global priority")}</Button>
    </div>
    <div className="flex flex-wrap gap-2"><Button size="sm" disabled={busy || name.trim().length < 2} onClick={() => void action(async () => {
      await authedRequest(target ? `/api/v1/admin/llm/upstreams/${target}` : "/api/v1/admin/llm/upstreams", { method: target ? "PATCH" : "POST", accessToken: readAccessToken(), body: { name: name.trim(), kind: "sub2", sub2GroupIDs: selected, compatible, status: "active" } }, true);
      await sub2Action("publish-groups", {}, true);
    }, text("分组已确认，请继续同步模型、设置价格并发布完整策略。", "Groups confirmed. Sync models, set prices and publish the full policy."))}>{text("保存并确认分组", "Save and confirm groups")}</Button>
    <Button size="sm" variant="outline" disabled={busy} onClick={() => void action(() => sub2Action("publish", {}, true), text("完整策略已确认。", "Full policy confirmed."))}>{text("发布已上架模型、价格与玩家优惠", "Publish listed models, prices and discounts")}</Button><Button size="sm" variant="outline" disabled={busy} onClick={() => { if (window.confirm(text("确认撤销当前生效的 Chat 付费策略？不会自动回退旧策略。", "Revoke the currently effective Chat paid policy? The previous policy will not be restored automatically."))) void action(() => sub2Action("revoke-policy", {}, true), text("当前付费策略已撤销。", "The current paid policy was revoked.")); }}>{text("撤销当前付费策略", "Revoke current paid policy")}</Button></div>
    {notice && <p role="status" className="text-xs text-muted-foreground">{notice}</p>}{(error || authority.error) && <p role="alert" className="text-xs text-destructive">{error || authority.error}</p>}
  </section>;
}
