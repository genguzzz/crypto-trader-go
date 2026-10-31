// App preferences (scanner filters/sort, rule pick, display options), one JSON blob in the keychain
// like mediahub. The owner key lives separately (adminKey.ts).
import { useSyncExternalStore } from "react";
import { Platform } from "react-native";
import * as SecureStore from "expo-secure-store";
import { logger } from "./log";

const log = logger("settings");
const KEY = "okxh.settings";

export type Dir = "all" | "long" | "short";

export interface Settings {
  ruleId: string; // "" = follow the server's default rule
  dir: Dir;
  sortKey: string;
  sortDesc: boolean;
  cats: string[]; // instrument categories shown: 1 crypto, 3 stock, 4 commodity
  onlyPass: boolean; // only rows whose spread gate passes
  tz: string; // display time zone: UTC offset in minutes or "local" (web default UTC+8)
  oiHalf: number; // OI (USD) that fills half a scanner row
  chartBar: string; // last K-line period in the detail chart
  chartTall: boolean; // detail chart height: tall (520) or normal (420)
}

const store =
  Platform.OS === "web"
    ? { get: async (k: string) => globalThis.localStorage?.getItem(k) ?? null, set: async (k: string, v: string) => globalThis.localStorage?.setItem(k, v) }
    : { get: (k: string) => SecureStore.getItemAsync(k), set: (k: string, v: string) => SecureStore.setItemAsync(k, v) };

let state: Settings = {
  ruleId: "",
  dir: "all",
  sortKey: "score",
  sortDesc: true,
  cats: ["1", "3", "4"],
  onlyPass: false,
  tz: "480",
  oiHalf: 5e6,
  chartBar: "15m",
  chartTall: false,
};
let loaded = false;
const listeners = new Set<() => void>();

export function getSettings(): Settings {
  return state;
}

export async function loadSettings(): Promise<Settings> {
  try {
    const raw = await store.get(KEY);
    if (raw) state = { ...state, ...(JSON.parse(raw) as Partial<Settings>) };
  } catch (e) {
    log.warn("load failed", e);
  }
  loaded = true;
  listeners.forEach((l) => l());
  return state;
}

export function setSettings(patch: Partial<Settings>) {
  state = { ...state, ...patch };
  listeners.forEach((l) => l());
  void Promise.resolve(store.set(KEY, JSON.stringify(state))).catch((e) => log.warn("save failed", e));
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function useSettings<T>(pick: (s: Settings) => T): T {
  return useSyncExternalStore(subscribe, () => pick(state));
}

export function useSettingsLoaded(): boolean {
  return useSyncExternalStore(subscribe, () => loaded);
}
