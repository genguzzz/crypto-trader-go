// 币对视角: one instrument's candles with the account's trades marked — the web TradeCandleChart in a
// WebView (route /embed/trades/:accountId?inst=&trade=, web views/EmbedTrades.vue). The app selects a
// trade with window.ctgSelect(id); the page posts {type:'pick', ids} for taps on marks. While a finger
// is on it the page scroll is locked (onTouchActive), like ChartView. Key it by instrument. The owner
// key is injected before load, as in ChartView.
import { useEffect, useRef, useState, type ComponentType, type Ref } from "react";
import { ActivityIndicator, Platform, Text, View, type StyleProp, type ViewStyle } from "react-native";
import { WebView as RNWebView, type WebViewMessageEvent, type WebViewProps } from "react-native-webview";
import { keyScript, useAdminKey } from "@/lib/adminKey";
import { WEB_ORIGIN } from "@/lib/api";
import { openPage } from "@/lib/device";
import { logger } from "@/lib/log";
import { colors, fs } from "@/lib/theme";
import { PillButton } from "../ui";

const log = logger("tradechart");
const WebView = RNWebView as unknown as ComponentType<WebViewProps & { ref?: Ref<{ injectJavaScript: (s: string) => void }> }>;

export function TradeCandleView({ accountId, instId, tradeId, onPick, onTouchActive, style }: {
  accountId: number; instId: string; tradeId: number; onPick?: (ids: number[]) => void; onTouchActive?: (on: boolean) => void; style?: StyleProp<ViewStyle>;
}) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [nonce, setNonce] = useState(0);
  const key = useAdminKey();
  const web = useRef<{ injectJavaScript: (s: string) => void } | null>(null);
  const loaded = useRef(false);
  // The trade only seeds the first load; later selections go through ctgSelect without a reload.
  // The parent keys this view by instrument, so another instrument is a fresh page.
  const [uri] = useState(() => `${WEB_ORIGIN}/embed/trades/${accountId}?inst=${encodeURIComponent(instId)}${tradeId ? `&trade=${tradeId}` : ""}`);
  const frame = useRef<HTMLIFrameElement | null>(null);

  useEffect(() => {
    const js = `window.ctgSelect && window.ctgSelect(${tradeId || 0}); true;`;
    if (Platform.OS === "web") {
      try { (frame.current?.contentWindow as unknown as { ctgSelect?: (id: number) => void })?.ctgSelect?.(tradeId || 0); } catch { /* cross-origin preview */ }
    } else if (loaded.current) web.current?.injectJavaScript(js);
  }, [tradeId]);

  const onMessage = (e: WebViewMessageEvent) => {
    try {
      const m = JSON.parse(e.nativeEvent.data) as { type: string; ids?: number[] };
      if (m.type === "pick" && Array.isArray(m.ids)) onPick?.(m.ids);
    } catch {
      // not ours
    }
  };

  if (Platform.OS === "web") {
    return (
      <View style={[{ backgroundColor: colors.card, overflow: "hidden" }, style]}>
        <iframe ref={frame} src={uri} style={{ border: 0, width: "100%", height: "100%", background: colors.card }} />
      </View>
    );
  }
  const lock = (on: boolean) => onTouchActive?.(on);
  return (
    <View style={[{ backgroundColor: colors.card, overflow: "hidden" }, style]} onTouchStart={() => lock(true)} onTouchEnd={() => lock(false)} onTouchCancel={() => lock(false)}>
      <WebView
        key={`${uri}#${nonce}|${key}`}
        ref={web}
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
        onLoadStart={() => { loaded.current = false; }}
        onLoadEnd={() => { loaded.current = true; setLoading(false); }}
        onError={(e) => { log.warn("load failed", { accountId, err: e.nativeEvent.description }); setError(e.nativeEvent.description || "加载失败"); }}
        onHttpError={(e) => setError(`服务器返回 ${e.nativeEvent.statusCode}`)}
        onContentProcessDidTerminate={() => setNonce((n) => n + 1)}
        onShouldStartLoadWithRequest={(r) => {
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
