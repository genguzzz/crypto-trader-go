import type { ExpoConfig } from "expo/config";

import pkg from "./package.json";

// The release version comes from package.json (bumped by scripts/release.sh);
// CI sets BUILD_NUMBER (GitHub run number) as the build number.
const build = process.env.BUILD_NUMBER ?? "0";

// OTA updates only reach binaries with the same runtime version. Bump this whenever native code
// changes (new/upgraded native module, config plugin, permission, Expo SDK), then cut a new IPA with
// scripts/release.sh. Independent of the Capacitor app's runtime ids (web/scripts/native-runtime.mjs).
// 1: first build — router, NativeTabs, WebView (K-line embed), and the capability pack ahead of
//    features: glass, haptics, blur, symbols, secure-store, sqlite, local notifications, speech
//    recognition + audio recording (AI voice), clipboard, sharing, linking, web-browser, network,
//    keep-awake, quick actions, Face ID, screen orientation.
export const RUNTIME_VERSION = "1";
// Shown on the home screen and in the 全能签 source; "β" while the Capacitor "OKX Helper" is still installed.
const DISPLAY_NAME = "OKX Helper β";

const config: ExpoConfig = {
  name: DISPLAY_NAME,
  slug: "okx-helper",
  scheme: "okxhelper",
  version: pkg.version,
  runtimeVersion: RUNTIME_VERSION,
  updates: {
    url: "https://ctg.gugenzzz.top/api/v1/expo/manifest",
    checkAutomatically: "ON_LOAD",
    fallbackToCacheTimeout: 0,
  },
  // The full-screen chart rotates to landscape (root stack orientation); everything else is portrait.
  orientation: "default",
  icon: "./assets/icon.png",
  userInterfaceStyle: "dark",
  backgroundColor: "#000000",
  ios: {
    bundleIdentifier: "top.gugenzzz.okxhelper",
    buildNumber: build,
    supportsTablet: false,
    infoPlist: {
      ITSAppUsesNonExemptEncryption: false,
      CFBundleDisplayName: DISPLAY_NAME,
    },
  },
  android: { package: "top.gugenzzz.okxhelper", versionCode: Number(build) || 1 },
  web: { favicon: "./assets/favicon.png" },
  plugins: [
    "expo-router",
    "expo-updates",
    "expo-secure-store",
    // Local notifications only (alerts, trade prompts): no APNs entitlement, so 全能签 re-signing works.
    ["expo-notifications", { enableRemoteNotifications: false }],
    "expo-web-browser",
    "expo-screen-orientation",
    "expo-sqlite",
    "expo-quick-actions",
    ["expo-local-authentication", { faceIDPermission: "OKX Helper 用面容 ID 保护所有者密钥和交易设置。" }],
    ["expo-speech-recognition", { speechRecognitionPermission: "OKX Helper 用语音识别把你说的话填进 AI 对话和搜索框。", microphonePermission: "OKX Helper 需要麦克风来听你对 AI 说的话。" }],
    ["expo-audio", { microphonePermission: "OKX Helper 需要麦克风来录下你对 AI 说的话。", enableBackgroundPlayback: false }],
  ],
  experiments: { typedRoutes: true },
};

export default config;
