"use client";
import * as React from "react";
import { useAuthSession } from "@/shared/auth/auth-session-context";
import { sub2Status, type Sub2Status } from "./api";

export function useSub2Authority() {
  const { accessToken } = useAuthSession();
  const [status, setStatus] = React.useState<Sub2Status | null>(null);
  const [error, setError] = React.useState("");
  const [loading, setLoading] = React.useState(true);
  const sequence = React.useRef(0);
  const reload = React.useCallback(async () => {
    if (!accessToken) return;
    const request = ++sequence.current;
    setLoading(true);
    try { const next = await sub2Status(); if (request === sequence.current) { setStatus(next); setError(""); } }
    catch (cause) { if (request === sequence.current) setError(cause instanceof Error ? cause.message : "Sub2 unavailable"); }
    finally { if (request === sequence.current) setLoading(false); }
  }, [accessToken]);
  React.useEffect(() => { void reload(); return () => { sequence.current++; }; }, [reload]);
  return { status, error, loading, reload };
}
