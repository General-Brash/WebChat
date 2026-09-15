import type { AdminOfficialPricingCatalogItemDTO, UpsertAdminModelPricingRequest } from "@/features/admin/api/billing.types";
import type { BillingModelPricingRow, PricingFormState } from "@/features/admin/model/billing-settings";

export type OfficialPricingCatalogItem = AdminOfficialPricingCatalogItemDTO;

export type OfficialModelPricingSuggestion = {
  item: OfficialPricingCatalogItem;
  score: number;
  reason: "exact" | "vendor" | "similar";
  payload: UpsertAdminModelPricingRequest;
};

const VENDOR_ALIASES: Record<string, string[]> = {
  anthropic: ["anthropic"],
  cohere: ["cohere"],
  deepseek: ["deepseek"],
  google: ["google"],
  gemini: ["google"],
  meta: ["meta-llama", "meta"],
  "meta-llama": ["meta-llama", "meta"],
  microsoft: ["microsoft"],
  mistral: ["mistralai", "mistral"],
  mistralai: ["mistralai", "mistral"],
  moonshot: ["moonshotai", "moonshot"],
  moonshotai: ["moonshotai", "moonshot"],
  openai: ["openai"],
  qwen: ["qwen", "alibaba"],
  alibaba: ["qwen", "alibaba"],
  xai: ["x-ai", "xai"],
  "x-ai": ["x-ai", "xai"],
  zai: ["z-ai", "zai"],
  "z-ai": ["z-ai", "zai"],
};

function normalizeKey(value: string): string {
  return value
    .toLowerCase()
    .replace(/[:_/\\.\s]+/g, "-")
    .replace(/[^a-z0-9-]+/g, "")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
}

function tokenize(value: string): string[] {
  return normalizeKey(value)
    .split("-")
    .filter((part) => part.length > 1 && part !== "model" && part !== "latest");
}

function vendorAliases(vendor: string): string[] {
  const normalized = normalizeKey(vendor);
  return VENDOR_ALIASES[normalized] ?? (normalized ? [normalized] : []);
}

function candidateModelIDs(row: Pick<BillingModelPricingRow, "platformModelName" | "vendor">): string[] {
  const rawName = row.platformModelName.trim().toLowerCase();
  const values = new Set<string>();
  if (!rawName) return [];
  values.add(rawName);
  values.add(normalizeKey(rawName));
  for (const alias of vendorAliases(row.vendor)) {
    if (!rawName.includes("/")) {
      values.add(`${alias}/${rawName}`);
      values.add(normalizeKey(`${alias}/${rawName}`));
    }
  }
  return Array.from(values).filter(Boolean);
}

function tokenSimilarity(left: string[], right: string[]): number {
  if (left.length === 0 || right.length === 0) return 0;
  const rightSet = new Set(right);
  const hits = left.filter((token) => rightSet.has(token)).length;
  return hits / left.length;
}

function compactCatalogID(id: string): string {
  const parts = id.split("/");
  return parts.length > 1 ? parts.slice(1).join("/") : id;
}

function queryFieldScore(normalizedQuery: string, field: string): number {
  const normalizedField = normalizeKey(field);
  if (!normalizedField) return 0;
  if (normalizedField === normalizedQuery) return 100;
  const closeness = Math.min(1, normalizedQuery.length / normalizedField.length);
  if (normalizedField.startsWith(`${normalizedQuery}-`)) {
    return Math.round(78 + closeness * 16);
  }
  if (normalizedField.endsWith(`-${normalizedQuery}`)) {
    return Math.round(76 + closeness * 14);
  }
  if (normalizedField.includes(normalizedQuery)) {
    return Math.round(68 + closeness * 18);
  }
  return 0;
}

function searchCatalogItemScore(query: string, item: OfficialPricingCatalogItem): number {
  const normalizedQuery = normalizeKey(query);
  const fields = Array.from(new Set([
    item.id,
    compactCatalogID(item.id),
    item.canonicalSlug,
    compactCatalogID(item.canonicalSlug),
    item.name,
  ].filter(Boolean)));
  const fieldScore = Math.max(0, ...fields.map((field) => queryFieldScore(normalizedQuery, field)));
  const tokenScore = Math.round(48 + tokenSimilarity(tokenize(query), tokenize(fields.join(" "))) * 34);
  return Math.max(fieldScore, tokenScore);
}

function scoreCatalogItem(row: BillingModelPricingRow, item: OfficialPricingCatalogItem): Omit<OfficialModelPricingSuggestion, "payload"> | null {
  const candidateIDs = candidateModelIDs(row);
  const itemIDs = [item.id, item.canonicalSlug].filter(Boolean);
  const normalizedItemIDs = itemIDs.map(normalizeKey);

  for (const candidate of candidateIDs) {
    if (itemIDs.includes(candidate)) {
      return { item, score: 100, reason: "exact" };
    }
    const normalized = normalizeKey(candidate);
    if (normalizedItemIDs.includes(normalized)) {
      return { item, score: 96, reason: "exact" };
    }
    if (itemIDs.some((id) => id.endsWith(`/${candidate}`)) || normalizedItemIDs.some((id) => id.endsWith(`-${normalized}`))) {
      return { item, score: 92, reason: "vendor" };
    }
  }

  const rowTokens = tokenize(`${row.vendor} ${row.platformModelName}`);
  const itemTokens = tokenize(`${item.id} ${item.canonicalSlug} ${item.name}`);
  const similarity = tokenSimilarity(rowTokens, itemTokens);
  const vendorBonus = vendorAliases(row.vendor).some((alias) => item.id.startsWith(`${alias}/`)) ? 8 : 0;
  const score = Math.min(89, Math.round(48 + similarity * 36 + vendorBonus));
  return score >= 64 ? { item, score, reason: "similar" } : null;
}

function pricePerMillion(raw: string): number | null {
  if (raw === "") return 0;
  const value = Number(raw);
  if (!Number.isFinite(value) || value < 0) return null;
  return Number((value * 1_000_000).toFixed(6));
}

export function formatOfficialPricingValue(value: number): string {
  if (!Number.isFinite(value) || value <= 0) {
    return "0.000";
  }
  return new Intl.NumberFormat("en-US", { minimumFractionDigits: 3, maximumFractionDigits: 3 }).format(value);
}

export function officialPricingPayload(
  row: Pick<BillingModelPricingRow, "platformModelName">,
  item: OfficialPricingCatalogItem,
): UpsertAdminModelPricingRequest | null {
  const input = pricePerMillion(item.pricing.prompt);
  const output = pricePerMillion(item.pricing.completion);
  if (input == null || output == null) {
    return null;
  }
  const cacheRead = pricePerMillion(item.pricing.inputCacheRead) ?? 0;
  const cacheWrite = pricePerMillion(item.pricing.inputCacheWrite) ?? 0;
  const isFree = input === 0 && output === 0 && cacheRead === 0 && cacheWrite === 0;
  return {
    platformModelName: row.platformModelName,
    currency: "USD",
    isFree,
    pricingMode: "token",
    inputUSDPerMTokens: input,
    cacheReadUSDPerMTokens: cacheRead,
    cacheWriteUSDPerMTokens: cacheWrite,
    outputUSDPerMTokens: output,
    callUSDPerCall: 0,
    durationUSDPerSecond: 0,
  };
}

export function findOfficialPricingSuggestions(
  row: BillingModelPricingRow,
  catalog: OfficialPricingCatalogItem[],
  limit = 5,
): OfficialModelPricingSuggestion[] {
  const suggestions: OfficialModelPricingSuggestion[] = [];
  for (const item of catalog) {
    const scored = scoreCatalogItem(row, item);
    if (!scored) continue;
    const payload = officialPricingPayload(row, item);
    if (!payload) continue;
    suggestions.push({ ...scored, payload });
  }
  return suggestions
    .sort((left, right) => right.score - left.score || left.item.id.localeCompare(right.item.id))
    .slice(0, limit);
}

export function searchOfficialPricingCatalog(
  query: string,
  row: BillingModelPricingRow,
  catalog: OfficialPricingCatalogItem[],
  limit = 8,
): OfficialModelPricingSuggestion[] {
  const normalizedQuery = normalizeKey(query);
  if (!normalizedQuery) {
    return findOfficialPricingSuggestions(row, catalog, limit);
  }
  const suggestions: OfficialModelPricingSuggestion[] = [];
  for (const item of catalog) {
    const score = searchCatalogItemScore(query, item);
    if (score < 62) continue;
    const payload = officialPricingPayload(row, item);
    if (!payload) continue;
    suggestions.push({ item, score, reason: score >= 96 ? "exact" : "similar", payload });
  }
  return suggestions
    .sort((left, right) => right.score - left.score || left.item.id.localeCompare(right.item.id))
    .slice(0, limit);
}

export function applyOfficialPricingToForm(form: PricingFormState, payload: UpsertAdminModelPricingRequest): PricingFormState {
  return {
    ...form,
    pricingMode: "token",
    input: String(payload.inputUSDPerMTokens),
    cacheRead: String(payload.cacheReadUSDPerMTokens),
    cacheWrite: String(payload.cacheWriteUSDPerMTokens),
    output: String(payload.outputUSDPerMTokens),
    call: "0",
    duration: "0",
    isFree: payload.isFree,
  };
}
