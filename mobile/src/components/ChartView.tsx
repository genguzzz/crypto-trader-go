// The K-line chart: the web app's own PriceChart (route /embed/chart/:id on ctg) in a WebView, so it
// looks and behaves exactly like the web (periods, layers, footprint, liquidation map, pinch zoom).
// The page posts {type:'live', price} for the forming bar and {type:'fullscreen'|'exitFullscreen'}
// from its full-screen button. While a finger is on the chart the parent's scroll is locked
// (onTouchActive), so pans and pinches belong to the chart; the page scrolls everywhere else.
// The owner key is injected before the page loads (window.ctgAdminKey, memory only there): every API
// request the page makes needs it; a new key reloads the page. `proposal` makes the page draw that AI trade proposal's
// entry / stop / target (query `proposal=<id>`); `signal` a signal subscription's levels (query `signal=<id>`).
// `compact` (query `compact=1`) leaves only the candles, those levels and a period switch: no layer/overlay
// chips, Delta/足迹, liquidation-map controls or plan/position bars (signal and proposal popups).
import { useMemo, useState, type ComponentType } from "react";
import { ActivityIndicator, Platform, Text, View, type StyleProp, type ViewStyle } from "react-native";
import { WebView as RNWebView, type WebViewMessageEvent, type WebViewProps } from "react-native-webview";
import { WEB_ORIGIN } from "@/lib/api";
import { keyScript, useAdminKey } from "@/lib/adminKey";
import { logger } from "@/lib/log";
import { openPage } from "@/lib/device";
import { colors, fs } from "@/lib/theme";
import { PillButton } from "./ui";

const log = logger("chart");
// react-native-webview 14 types `WebView<P = undefined>` as Component<WebViewProps & P>, which is `never` under strict TS.
const WebView = RNWebView as unknown as ComponentType<WebViewProps>;

export interface ChartMsg { type: "live"; price: number }

export function ChartView({ id, hasSpot, hasOi, full, compact, proposal, signal, style, onLive, onFullscreen, onTouchActive }: {
  id: string;
  hasSpot?: boolean;
  hasOi?: boolean;
  full?: boolean;
  compact?: boolean;
  proposal?: number;
  signal?: number;
  style?: StyleProp<ViewStyle>;
  onLive?: (price: number) => void;
  onFullscreen?: (enter: boolean) => void;
  onTouchActive?: (active: boolean) => void;
}) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [nonce, setNonce] = useState(0);
  const key = useAdminKey();
  const uri = useMemo(() => {
    const p = new URLSearchParams();
    if (hasSpot !== undefined) p.set("spot", hasSpot ? "1" : "0");
    if (hasOi !== undefined) p.set("oi", hasOi ? "1" : "0");
    if (full) p.set("full", "1");
    if (compact) p.set("compact", "1");
    if (proposal) p.set("proposal", String(proposal));
    if (signal) p.set("signal", String(signal));
    return `${WEB_ORIGIN}/embed/chart/${encodeURIComponent(id)}?${p.toString()}`;
  }, [id, hasSpot, hasOi, full, compact, proposal, signal]);

  const onMessage = (e: WebViewMessageEvent) => {
    try {
      const m = JSON.parse(e.nativeEvent.data) as { type: string; price?: number };
      if (m.type === "live" && typeof m.price === "number") onLive?.(m.price);
      else if (m.type === "fullscreen") onFullscreen?.(true);
      else if (m.type === "exitFullscreen") onFullscreen?.(false);
    } catch {
      // not ours
    }
  };

  if (Platform.OS === "web") {
    // Web preview only: an iframe of the same page.
    return (
      <View style={[{ backgroundColor: colors.card, overflow: "hidden" }, style]}>
        <iframe src={uri} style={{ border: 0, width: "100%", height: "100%", background: colors.card }} />
      </View>
    );
  }

  const lock = (on: boolean) => onTouchActive?.(on);
  return (
    <View
      style={[{ backgroundColor: colors.card, overflow: "hidden" }, style]}
      onTouchStart={() => lock(true)}
      onTouchEnd={() => lock(false)}
      onTouchCancel={() => lock(false)}
    >
      <WebView
        key={`${nonce}|${key}`}
        source={{ uri }}
        injectedJavaScriptBeforeContentLoaded={keyScript(key)}
        style={{ flex: 1, backgroundColor: colors.card }}
        containerStyle={{ backgroundColor: colors.card }}
        originWhitelist={["https://*"]}
        scrollEnabled={false}
        bounces={false}
        overScrollMode="never"
        contentInsetAdjustmentBehavior="never"
        automaticallyAdjustContentInsets={false}
        setSupportMultipleWindows={false}
        allowsBackForwardNavigationGestures={false}
        allowsLinkPreview={false}
        textInteractionEnabled={false}
        webviewDebuggingEnabled={__DEV__}
        onMessage={onMessage}
        onLoadEnd={() => setLoading(false)}
        onError={(e) => {
          log.warn("load failed", { id, err: e.nativeEvent.description });
          setError(e.nativeEvent.description || "加载失败");
        }}
        onHttpError={(e) => setError(`服务器返回 ${e.nativeEvent.statusCode}`)}
        onContentProcessDidTerminate={() => {
          log.warn("web content process terminated", { id });
          setNonce((n) => n + 1);
        }}
        onShouldStartLoadWithRequest={(r) => {
          // stay on the embed page; anything else (help links) opens in the in-app browser
          if (r.url.startsWith(`${WEB_ORIGIN}/embed/`) || r.url === "about:blank") return true;
          if (r.isTopFrame) void openPage(r.url);
          return !r.isTopFrame;
        }}
      />
      {loading && !error ? (
        <View pointerEvents="none" style={{ position: "absolute", left: 0, right: 0, top: 0, bottom: 0, alignItems: "center", justifyContent: "center" }}>
          <ActivityIndicator color={colors.primary} />
        </View>
      ) : null}
      {error ? (
        <View style={{ position: "absolute", left: 0, right: 0, top: 0, bottom: 0, alignItems: "center", justifyContent: "center", gap: 12, backgroundColor: colors.card }}>
          <Text style={{ color: colors.textSoft, fontSize: fs.md }}>K 线加载失败：{error}</Text>
          <PillButton small variant="soft" icon="refresh" label="重新加载" onPress={() => { setError(""); setLoading(true); setNonce((n) => n + 1); }} />
        </View>
      ) : null}
    </View>
  );
}
