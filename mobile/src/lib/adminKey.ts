// Owner key for protected writes (header X-CTG-Admin), like web/src/composables/adminKey.ts:
// kept in the keychain, asked once through AdminKeySheet when a request comes back
// 401 {code:"admin_required"}. Concurrent asks share one sheet.
import { useSyncExternalStore } from "react";
import { Platform } from "react-native";
import * as SecureStore from "expo-secure-store";
import { logger } from "./log";

const log = logger("adminKey");
const KEY = "okxh.adminKey";
const store =
  Platform.OS === "web"
    ? {
        get: async () => globalThis.localStorage?.getItem(KEY) ?? null,
        set: async (v: string) => (v ? globalThis.localStorage?.setItem(KEY, v) : globalThis.localStorage?.removeItem(KEY)),
      }
    : { get: () => SecureStore.getItemAsync(KEY), set: (v: string) => (v ? SecureStore.setItemAsync(KEY, v) : SecureStore.deleteItemAsync(KEY)) };

let key = "";
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((l) => l());
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

export async function loadAdminKey() {
  try {
    key = (await store.get()) ?? "";
  } catch (e) {
    log.warn("load failed", e);
  }
  emit();
}

export const getAdminKey = () => key;

export function setAdminKey(v: string) {
  key = v.trim();
  emit();
  void Promise.resolve(store.set(key)).catch((e) => log.warn("save failed", e));
}

export const clearAdminKey = () => setAdminKey("");
export const useAdminKeySet = () => useSyncExternalStore(subscribe, () => !!key);

// ---------- prompt ----------

let prompt: { open: boolean; error: string } = { open: false, error: "" };
let waiters: ((k: string | null) => void)[] = [];

export function askAdminKey(error = ""): Promise<string | null> {
  prompt = { open: true, error };
  emit();
  return new Promise((res) => waiters.push(res));
}

export function resolveAdminKey(k: string | null) {
  if (k) setAdminKey(k);
  prompt = { open: false, error: "" };
  emit();
  const w = waiters;
  waiters = [];
  w.forEach((f) => f(k));
}

export const useAdminPrompt = () => useSyncExternalStore(subscribe, () => prompt);
