import type { AdminLLMSetting } from "@/features/admin/api/llm.types";

export const SUB2_GLOBAL_GROUP_PRIORITY_KEY = "sub2.global_group_priority";

export function readGlobalGroupPriority(settings: AdminLLMSetting[]): number[] {
  const setting = settings.find((item) => item.key === SUB2_GLOBAL_GROUP_PRIORITY_KEY);
  if (!setting) return [];
  try {
    const value: unknown = JSON.parse(setting.value);
    if (!Array.isArray(value)) return [];
    const seen = new Set<number>();
    const result: number[] = [];
    for (const item of value) {
      if (typeof item !== "number" || !Number.isSafeInteger(item) || item <= 0 || seen.has(item)) continue;
      seen.add(item);
      result.push(item);
    }
    return result;
  } catch {
    return [];
  }
}

export function moveGroupPriority(ids: number[], index: number, delta: number): number[] {
  const other = index + delta;
  if (index < 0 || index >= ids.length || other < 0 || other >= ids.length) return ids;
  const next = [...ids];
  [next[index], next[other]] = [next[other], next[index]];
  return next;
}
