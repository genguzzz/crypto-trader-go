// Market data shared by every screen: field/rule metadata, the pool snapshot and live prices.
// The web app gets these over SSE (/api/v1/stream: `universe` → refetch the snapshot, `prices` every
// 2 s). Here the snapshot is a useQuery entry polled while a screen shows it, and the stream is read
// with expo/fetch (streaming body) while the app is in the foreground; on web, EventSource. If the
// stream fails, polling alone keeps the list current (prices then come from the snapshot).
import { useSyncExternalStore } from "react";
import { AppState, Platform } from "react-native";
import { api, API_ORIGIN, type Meta, type Row, type Score, type Snapshot } from "./api";
import { load, mutate, useQuery } from "./query";
import { getSettings, setSettings, useSettings } from "./settings";
import { logger } from "./log";
import { track } from "./track";

const log = logger("market");

export function useMeta() {
  return useQuery<Meta>("meta", () => api.meta(), { staleMs: 5 * 60_000 });
}

/** Pool snapshot (active pool + watchlist). Polls every 15 s while shown; the stream refreshes it sooner. */
export function useUniverse() {
  return useQuery<Snapshot>("universe", () => api.universe("active"), { interval: 15_000, staleMs: 8_000 });
}

/** The rule whose scores the lists show: the owner's pick, else the server default. */
export function useRuleId(meta?: Meta) {
  const picked = useSettings((s) => s.ruleId);
  if (picked && meta?.rules.some((r) => r.id === picked)) return picked;
  return meta?.settings.defaultRule || meta?.rules[0]?.id || picked;
}

export const setRule = (id: string) => setSettings({ ruleId: id });

export function scoreOf(r: Row, ruleId: string, side: "long" | "short"): number | null {
  return r.scores?.[ruleId]?.[side]?.score ?? null;
}
export const gateOf = (r: Row, ruleId: string): Score["gate"] | undefined => r.scores?.[ruleId]?.gate;

/** Optimistic watchlist toggle: flips the row everywhere at once, then refetches. */
export async function toggleWatch(instId: string, on: boolean) {
  track("watch", { on });
  mutate<Snapshot>("universe", (s) => (s ? { ...s, rows: (s.rows ?? []).map((r) => (r.instId === instId ? { ...r, watch: on } : r)) } : s));
  try {
    await api.watch(instId, on);
  } finally {
    void load("universe", () => api.universe("active"));
  }
}

// ---------- live stream ----------

type Prices = Record<string, [number, number]>;
let prices: Prices = {};
let connected = false;
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((l) => l());
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

/** Live last price for an instrument (falls back to the snapshot's). */
export function useLivePrice(instId: string, fallback?: number) {
  return useSyncExternalStore(subscribe, () => prices[instId]?.[0]) ?? fallback;
}
export const usePrices = () => useSyncExternalStore(subscribe, () => prices);
export const useStreamConnected = () => useSyncExternalStore(subscribe, () => connected);

function onEvent(type: string, data: string) {
  if (type === "prices") {
    try {
      prices = JSON.parse(data) as Prices;
      emit();
    } catch {
      // partial frame; next one comes in 2 s
    }
  } else if (type === "universe" || type === "hello") {
    void load("universe", () => api.universe("active"));
  }
}

function setConnected(v: boolean) {
  if (connected === v) return;
  connected = v;
  emit();
}

let stop: (() => void) | null = null;

function decodeUtf8(bytes: Uint8Array): string {
  if (typeof TextDecoder !== "undefined") return new TextDecoder().decode(bytes);
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  try {
    return decodeURIComponent(escape(s));
  } catch {
    return s;
  }
}

async function readStream(signal: AbortSignal) {
  const { fetch: streamFetch } = await import("expo/fetch");
  const res = await streamFetch(API_ORIGIN + "/api/v1/stream", { headers: { accept: "text/event-stream" }, signal });
  if (!res.ok || !res.body) throw new Error(`stream ${res.status}`);
  setConnected(true);
  const reader = res.body.getReader();
  let buf = "";
  let pending: number[] = [];
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    // keep a split multi-byte character for the next chunk: only decode up to the last newline
    pending = pending.concat(Array.from(value));
    const nl = pending.lastIndexOf(10);
    if (nl < 0) continue;
    buf += decodeUtf8(Uint8Array.from(pending.slice(0, nl + 1)));
    pending = pending.slice(nl + 1);
    let i: number;
    while ((i = buf.indexOf("\n\n")) >= 0) {
      const block = buf.slice(0, i);
      buf = buf.slice(i + 2);
      let type = "message";
      const data: string[] = [];
      for (const line of block.split("\n")) {
        if (line.startsWith("event:")) type = line.slice(6).trim();
        else if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
      }
      if (data.length) onEvent(type, data.join("\n"));
    }
  }
}

function connect(): () => void {
  if (Platform.OS === "web") {
    const es = new EventSource(API_ORIGIN + "/api/v1/stream");
    es.addEventListener("hello", (e) => { setConnected(true); onEvent("hello", (e as MessageEvent).data); });
    es.addEventListener("universe", (e) => onEvent("universe", (e as MessageEvent).data));
    es.addEventListener("prices", (e) => onEvent("prices", (e as MessageEvent).data));
    es.onerror = () => setConnected(false);
    return () => { es.close(); setConnected(false); };
  }
  const ctrl = new AbortController();
  let retry: ReturnType<typeof setTimeout> | null = null;
  let fails = 0;
  const run = () => {
    readStream(ctrl.signal)
      .then(() => { fails = 0; })
      .catch((e) => {
        if (ctrl.signal.aborted) return;
        fails++;
        if (fails <= 3 || fails % 20 === 0) log.warn("stream failed", { fails, err: e });
      })
      .finally(() => {
        setConnected(false);
        if (!ctrl.signal.aborted) retry = setTimeout(run, Math.min(30_000, 1000 * 2 ** Math.min(fails, 5)));
      });
  };
  run();
  return () => {
    ctrl.abort();
    if (retry) clearTimeout(retry);
    setConnected(false);
  };
}

/** Starts the live stream while the app is active (call once from the root layout). */
export function startLive() {
  const sync = (s: string) => {
    if (s === "active" && !stop) stop = connect();
    else if (s !== "active" && stop) {
      stop();
      stop = null;
    }
  };
  sync(AppState.currentState ?? "active");
  AppState.addEventListener("change", sync);
}

export const currentRule = () => getSettings().ruleId;
