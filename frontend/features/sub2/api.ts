import { authedRequest } from "@/shared/api/authed-client";
import { readAccessToken } from "@/shared/auth/session";

export type Sub2Status = { enabled: boolean; paidReady?: boolean; issuer?: string; groupRevision?: number; pricingVersion?: number; playerVersion?: number };
export type Sub2Action = { id?: number; targetUserID?: number; query?: string; command?: Record<string, unknown>; idempotencyKey?: string; executionID?: string };
export type Sub2Group = { group_id: number; name: string; platform: string; status: string; is_exclusive: boolean; available_account_count: number; observed_at: string };
export function sub2Status(): Promise<Sub2Status> {
  return authedRequest<Sub2Status>("/api/v1/sub2/status", { accessToken: readAccessToken() }, true);
}
export function sub2Action<T>(operation: string, body: Sub2Action = {}, admin = false): Promise<T> {
  return authedRequest<T>(`/api/v1/${admin ? "admin/" : ""}sub2/${encodeURIComponent(operation)}`, { method: "POST", accessToken: readAccessToken(), body }, true);
}
export function sub2Portal(issuer?: string): string | null {
  try { const url = new URL(issuer || ""); return ["http:", "https:"].includes(url.protocol) ? url.origin : null; } catch { return null; }
}
