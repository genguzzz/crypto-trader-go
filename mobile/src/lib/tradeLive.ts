// Keeping the trading pages fresh (web composables/tradeLive.ts + store.openPrompts): stream events
// `trade` (with the account id) and `interact` trigger refetches, bursts coalesced; the open
// confirmation count drives the 交易 tab badge and the 待确认 segment badge.
import { useCallback, useEffect, useRef, useSyncExternalStore } from "react";
import { AppState } from "react-native";
import { useFocusEffect } from "expo-router";
import { onStreamEvent } from "./market";
import { interactApi } from "./tradeApi";

/** Calls `fn` (debounced 250 ms) on trade events of `accountId` (all accounts when 0) and, with `interact`, on confirmation events — only while the screen is focused. */
export function useTradeEvents(fn: () => unknown, opts: { accountId?: number; interact?: boolean } = {}) {
  const ref = useRef(fn);
  useEffect(() => {
    ref.current = fn;
  });
  const { accountId = 0, interact = false } = opts;
  useFocusEffect(
    useCallback(() => {
      let t: ReturnType<typeof setTimeout> | null = null;
      const kick = () => {
        if (t) return;
        t = setTimeout(() => { t = null; void ref.current(); }, 250);
      };
      const off = onStreamEvent((type, data) => {
        if (type === "trade") {
          let id = 0;
          try { id = Number((JSON.parse(data) as { accountId?: number })?.accountId) || 0; } catch { /* refetch */ }
          if (!accountId || !id || id === accountId) kick();
        } else if (type === "interact" && interact) kick();
      });
      return () => { off(); if (t) clearTimeout(t); };
    }, [accountId, interact]),
  );
}

// ---- open confirmations (tab badge) ----
let open = 0;
const listeners = new Set<() => void>();
const sub = (l: () => void) => { listeners.add(l); return () => void listeners.delete(l); };
export function setOpenPrompts(n: number) {
  if (n === open) return;
  open = n;
  listeners.forEach((l) => l());
}
export async function refreshPrompts() {
  try { setOpenPrompts((await interactApi.prompts("open")).length); } catch { /* offline: keep the last count */ }
}
let started = false;
function start() {
  if (started) return;
  started = true;
  void refreshPrompts();
  onStreamEvent((type) => { if (type === "interact") void refreshPrompts(); });
  setInterval(() => { if (AppState.currentState === "active") void refreshPrompts(); }, 30_000);
  AppState.addEventListener("change", (s) => { if (s === "active") void refreshPrompts(); });
}
/** Open confirmation count; the first caller starts the 30 s poll (tabs layout). */
export function useOpenPrompts() {
  useEffect(start, []);
  return useSyncExternalStore(sub, () => open);
}
