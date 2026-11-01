// Owner key (header X-CTG-Admin): the server requires it for every /api/v1 request (backend
// internal/api/admin.go). Kept in the keychain (localStorage on the web preview). Without a key, or
// once the server answers 401 admin_required, the full-screen KeyGate asks for it; requests that hit
// the 401 wait for the new key and retry once (keyRejected).
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

/** What KeyGate shows: open, why (error), and whether it may be closed (changing a key that works). */
export interface GateState { open: boolean; error: string; cancellable: boolean }

let key = "";
let gate: GateState = { open: false, error: "", cancellable: false };
let waiters: ((k: string) => void)[] = [];
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((l) => l());
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

function persist(v: string) {
  key = v;
  void Promise.resolve(store.set(v)).catch((e) => log.warn("save failed", e));
}

let loading: Promise<void> | null = null;

/** Loads the stored key once (requests await it); with none the gate opens right away. */
export function loadAdminKey(): Promise<void> {
  loading ??= (async () => {
    try {
      key = (await store.get()) ?? "";
    } catch (e) {
      log.warn("load failed", e);
    }
    if (!key) gate = { open: true, error: "", cancellable: false };
    emit();
  })();
  return loading;
}

export const getAdminKey = () => key;
export const useAdminKey = () => useSyncExternalStore(subscribe, () => key);
export const useAdminKeySet = () => useSyncExternalStore(subscribe, () => !!key);
export const useKeyGate = () => useSyncExternalStore(subscribe, () => gate);

/** A key KeyGate has verified against the server: store it, close the gate, resume waiting requests. */
export function acceptKey(v: string) {
  persist(v.trim());
  gate = { open: false, error: "", cancellable: false };
  emit();
  const w = waiters;
  waiters = [];
  w.forEach((f) => f(key));
}

/** Settings → 更换密钥: the gate over a working key (can be cancelled). */
export function changeKey() {
  gate = { open: true, error: "", cancellable: !!key };
  emit();
}

export function closeKeyGate() {
  if (!gate.cancellable) return;
  gate = { open: false, error: "", cancellable: false };
  emit();
}

/** Settings → 清除: forget the key; the gate asks for a new one. */
export function clearAdminKey() {
  persist("");
  gate = { open: true, error: "", cancellable: false };
  emit();
}

/**
 * A request sent with `sent` came back 401 admin_required. Clears that key (unless it was replaced
 * meanwhile), opens the gate and resolves with the next accepted key, for one retry.
 */
export function keyRejected(sent: string): Promise<string> {
  if (key && key !== sent) return Promise.resolve(key); // already replaced: just retry
  if (key) {
    log.warn("key rejected by the server");
    persist("");
  }
  if (!gate.open || gate.cancellable) gate = { open: true, error: sent ? "密钥无效或已更换，请重新输入" : "", cancellable: false };
  emit();
  return new Promise((res) => waiters.push(res));
}

/** Script injected before an /embed/* page loads: the page sends the key with every API request
 *  (web main.ts installKeyedFetch). Key the WebView by the key so a new key reloads it. */
export const keyScript = (k: string) => `window.ctgAdminKey = ${JSON.stringify(k)}; true;`;
