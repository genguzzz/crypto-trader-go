// Device capabilities behind one door (like mediahub/Murmur lib/device): feature code calls these,
// never expo-* directly. Every wrapper checks that the native module exists in this binary (OTA code
// can run on an older runtime), logs and tracks the call, and returns null instead of throwing when
// the user cancels or the module is missing. The binary ships these ahead of features (runtime 1).
import { Platform, Share, TurboModuleRegistry } from "react-native";
import { requireOptionalNativeModule } from "expo-modules-core";
import { logger } from "./log";
import { track } from "./track";

const log = logger("device");

const MODULES = {
  glass: "ExpoGlassEffect",
  haptics: "ExpoHaptics",
  blur: "ExpoBlurView",
  symbols: "SymbolModule",
  secureStore: "ExpoSecureStore",
  sqlite: "ExpoSQLite",
  notifications: "ExpoNotificationScheduler",
  speech: "ExpoSpeechRecognition",
  audio: "ExpoAudio",
  clipboard: "ExpoClipboard",
  sharing: "ExpoSharing",
  linking: "ExpoLinking",
  webBrowser: "ExpoWebBrowser",
  webview: "RNCWebViewModule",
  network: "ExpoNetwork",
  keepAwake: "ExpoKeepAwake",
  quickActions: "ExpoQuickActions",
  biometrics: "ExpoLocalAuthentication",
  orientation: "ExpoScreenOrientation",
} as const;
export type Capability = keyof typeof MODULES;

export const CAPABILITY_LABEL: Record<Capability, string> = {
  glass: "液态玻璃", haptics: "触感反馈", blur: "模糊", symbols: "SF Symbols", secureStore: "钥匙串", sqlite: "SQLite",
  notifications: "本地通知", speech: "语音识别", audio: "录音", clipboard: "剪贴板", sharing: "分享", linking: "链接",
  webBrowser: "内置浏览器", webview: "WebView（K 线）", network: "网络状态", keepAwake: "保持亮屏", quickActions: "主屏快捷操作",
  biometrics: "面容 ID", orientation: "屏幕方向",
};

const WEB_OK = new Set<Capability>(["clipboard", "linking", "webview"]);
const present = new Map<Capability, boolean>();

export function has(c: Capability): boolean {
  if (Platform.OS === "web") return WEB_OK.has(c);
  if (!present.has(c)) {
    const name = MODULES[c];
    present.set(c, c === "webview" ? TurboModuleRegistry.get(name) != null : requireOptionalNativeModule(name) !== null);
  }
  return present.get(c)!;
}

export function capabilities(): Record<Capability, boolean> {
  return Object.fromEntries((Object.keys(MODULES) as Capability[]).map((c) => [c, has(c)])) as Record<Capability, boolean>;
}

// ---------- haptics ----------

export async function haptic(kind: "light" | "medium" | "success" | "error" | "select" = "light") {
  if (!has("haptics")) return;
  const H = await import("expo-haptics");
  try {
    if (kind === "select") await H.selectionAsync();
    else if (kind === "success" || kind === "error") await H.notificationAsync(kind === "success" ? H.NotificationFeedbackType.Success : H.NotificationFeedbackType.Error);
    else await H.impactAsync(kind === "medium" ? H.ImpactFeedbackStyle.Medium : H.ImpactFeedbackStyle.Light);
  } catch {
    // haptics are best effort
  }
}

// ---------- clipboard / share / browser ----------

export async function copy(text: string) {
  if (!has("clipboard")) return false;
  const m = await import("expo-clipboard");
  await m.setStringAsync(text);
  track("device.copy");
  return true;
}

export async function shareText(text: string) {
  track("device.share");
  await Share.share({ message: text });
}

export async function shareFile(uri: string) {
  if (!has("sharing")) return shareText(uri);
  const m = await import("expo-sharing");
  if (await m.isAvailableAsync()) await m.shareAsync(uri);
}

export async function openPage(url: string) {
  track("device.browser");
  if (!has("webBrowser")) {
    const L = await import("expo-linking");
    return L.openURL(url);
  }
  const m = await import("expo-web-browser");
  await m.openBrowserAsync(url, { presentationStyle: m.WebBrowserPresentationStyle.PAGE_SHEET, controlsColor: "#bcff2f" });
}

// ---------- keep awake ----------

export async function keepAwake(tag: string, on: boolean) {
  if (!has("keepAwake")) return;
  const m = await import("expo-keep-awake");
  try {
    if (on) await m.activateKeepAwakeAsync(tag);
    else await m.deactivateKeepAwake(tag);
  } catch (e) {
    log.warn("keepAwake failed", { on, err: e instanceof Error ? e.message : e });
  }
}

// ---------- Face ID / passcode ----------

export async function authenticate(reason: string): Promise<boolean> {
  if (!has("biometrics")) return true;
  const m = await import("expo-local-authentication");
  const r = await m.authenticateAsync({ promptMessage: reason, cancelLabel: "取消", disableDeviceFallback: false });
  track("device.auth", { ok: r.success, err: r.success ? undefined : r.error });
  return r.success;
}

// ---------- network ----------

export async function connection(): Promise<{ type: string; cellular: boolean } | null> {
  if (!has("network")) return null;
  const m = await import("expo-network");
  const s = await m.getNetworkStateAsync();
  return { type: String(s.type ?? "unknown"), cellular: s.type === m.NetworkStateType.CELLULAR };
}

// ---------- local notifications ----------

export async function notify(title: string, body: string, data?: Record<string, unknown>): Promise<boolean> {
  if (!has("notifications")) return false;
  const N = await import("expo-notifications");
  const perm = await N.getPermissionsAsync();
  if (!perm.granted && !(await N.requestPermissionsAsync()).granted) return false;
  await N.scheduleNotificationAsync({ content: { title, body, data }, trigger: null });
  track("device.notify");
  return true;
}

// ---------- voice ----------

/** One-shot speech → text (Chinese by default); null when unavailable, denied or nothing heard. */
export async function listenOnce(lang = "zh-CN", timeoutMs = 10_000): Promise<string | null> {
  if (!has("speech")) return null;
  const { ExpoSpeechRecognitionModule: S } = await import("expo-speech-recognition");
  const perm = await S.requestPermissionsAsync();
  if (!perm.granted) {
    track("device.speech", { ok: false, err: "denied" });
    return null;
  }
  return new Promise((resolve) => {
    let text = "";
    let done = false;
    const subs = [
      S.addListener("result", (e: { results: { transcript: string }[]; isFinal: boolean }) => {
        text = e.results[0]?.transcript ?? text;
        if (e.isFinal) finish();
      }),
      S.addListener("error", () => finish()),
      S.addListener("end", () => finish()),
    ];
    const timer = setTimeout(() => {
      S.stop();
      finish();
    }, timeoutMs);
    function finish() {
      if (done) return;
      done = true;
      clearTimeout(timer);
      subs.forEach((s) => s.remove());
      track("device.speech", { ok: !!text, len: text.length });
      resolve(text || null);
    }
    S.start({ lang, interimResults: true, continuous: false });
  });
}

/** Microphone permission for recordings (the AI chat sends voice clips); false when unavailable or denied. */
export async function micPermission(): Promise<boolean> {
  if (!has("audio")) return false;
  const A = await import("expo-audio");
  const r = await A.requestRecordingPermissionsAsync();
  track("device.mic", { ok: r.granted });
  return r.granted;
}
