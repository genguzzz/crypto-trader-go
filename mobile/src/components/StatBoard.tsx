// The instrument's key numbers as one row of tiles (four in view, swipe for the rest), with two ways into
// the detail behind each:
//  · tap a tile → the card deck: a sheet of swipeable cards (neighbours peeking at the edges, a tab
//    strip in sync), one per number, each the full detail view;
//  · press and hold → a peek card pops up over the page; keep the finger down and slide across the
//    tiles to flip through them (a tick per change); slide up past the line and let go to pin it as
//    the deck, or just let go to close.
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Animated, FlatList, Platform, Pressable, ScrollView, StyleSheet, Text, useWindowDimensions, View, type GestureResponderEvent, type HostInstance, type ScrollViewInstance } from "react-native";
import { FullWindowOverlay } from "react-native-screens";
import { Ionicons } from "@expo/vector-icons";
import { haptic } from "@/lib/device";
import { colors, fs, radius, tnum } from "@/lib/theme";
import { Sheet, tap } from "./ui";

export interface Tile {
  key: string;
  label: string;
  /** card title in the peek and the deck */
  title: string;
  value: ReactNode;
  sub?: ReactNode;
  body: () => ReactNode;
}

const COLS = 4;
const HOLD_MS = 260;
const PIN_PX = 70;

export function StatBoard({ tiles, onHold }: { tiles: Tile[]; onHold?: (holding: boolean) => void }) {
  const [deck, setDeck] = useState<number | null>(null);
  const [peek, setPeek] = useState<number | null>(null);
  const [pin, setPin] = useState(false);
  const box = useRef<HostInstance>(null);
  const frame = useRef({ x: 0, y: 0, w: 1, h: 1 });
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const start = useRef({ x: 0, y: 0, i: 0 });
  const peeking = useRef(false);
  const touched = useRef(0); // the tile the finger came down on (set by the tile itself, before any measuring)
  const [viewW, setViewW] = useState(0);
  const tileW = viewW / COLS;
  const scrollX = useRef(0);

  // one scrolling row: the tile under a window x, counting the row's scroll offset
  const tileAt = (px: number) => {
    const f = frame.current;
    const i = Math.floor((px - f.x + scrollX.current) / (f.w / COLS));
    return Math.max(0, Math.min(tiles.length - 1, i));
  };
  const endPeek = () => {
    peeking.current = false;
    setPeek(null);
    setPin(false);
    onHold?.(false);
  };

  const handlers = {
    onStartShouldSetResponder: () => true,
    onMoveShouldSetResponder: () => peeking.current,
    // while peeking the board keeps the finger; before that a scroll may take it
    onResponderTerminationRequest: () => !peeking.current,
    onResponderGrant: (e: GestureResponderEvent) => {
      const { pageX, pageY } = e.nativeEvent;
      box.current?.measureInWindow((x: number, y: number, w: number, h: number) => { frame.current = { x, y, w, h }; });
      start.current = { x: pageX, y: pageY, i: -1 };
      timer.current = setTimeout(() => {
        timer.current = null;
        const i = touched.current;
        start.current.i = i;
        peeking.current = true;
        onHold?.(true);
        void haptic("medium");
        setPeek(i);
      }, HOLD_MS);
    },
    onResponderMove: (e: GestureResponderEvent) => {
      const { pageX, pageY } = e.nativeEvent;
      if (!peeking.current) {
        if (Math.abs(pageX - start.current.x) > 8 || Math.abs(pageY - start.current.y) > 8) {
          if (timer.current) clearTimeout(timer.current);
          timer.current = null;
        }
        return;
      }
      const up = frame.current.y - pageY > PIN_PX;
      setPin((p) => {
        if (p !== up) void haptic("select");
        return up;
      });
      if (!up) {
        const i = tileAt(pageX);
        setPeek((cur) => {
          if (cur !== i) void haptic("select");
          return i;
        });
      }
    },
    onResponderRelease: () => {
      if (timer.current) { // a tap
        clearTimeout(timer.current);
        timer.current = null;
        tap();
        setDeck(touched.current);
        return;
      }
      if (!peeking.current) return;
      const i = peek ?? start.current.i;
      if (pin) setDeck(i);
      endPeek();
    },
    onResponderTerminate: () => {
      if (timer.current) clearTimeout(timer.current);
      timer.current = null;
      if (peeking.current) endPeek();
    },
  };

  return (
    <>
      <View ref={box} onLayout={(e) => setViewW(e.nativeEvent.layout.width)}>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} scrollEnabled={peek == null} scrollEventThrottle={16}
          onScroll={(e) => { scrollX.current = e.nativeEvent.contentOffset.x; }} snapToInterval={tileW || undefined} decelerationRate="fast">
          <View {...handlers} style={{ flexDirection: "row" }}>
            {tiles.map((t, i) => (
              <View key={t.key} onTouchStart={() => { touched.current = i; }} onPointerDown={() => { touched.current = i; }} style={{ width: tileW || 88, paddingHorizontal: 8, paddingVertical: 7, gap: 2,
                backgroundColor: peek === i ? colors.raised : "transparent",
                borderRightWidth: i === tiles.length - 1 ? 0 : StyleSheet.hairlineWidth, borderColor: colors.borderStrong }}>
                <Text style={{ fontSize: fs.xs, color: colors.textFaint }} numberOfLines={1}>{t.label}</Text>
                <Text style={[{ fontSize: fs.md, fontWeight: "600", color: colors.text }, tnum]} numberOfLines={1} adjustsFontSizeToFit minimumFontScale={0.75}>{t.value}</Text>
                {t.sub != null ? <Text style={[{ fontSize: 10.5, color: colors.textFaint }, tnum]} numberOfLines={1}>{t.sub}</Text> : null}
              </View>
            ))}
          </View>
        </ScrollView>
      </View>
      {peek != null ? <Peek tiles={tiles} index={peek} pin={pin} /> : null}
      <Deck tiles={tiles} index={deck} onClose={() => setDeck(null)} />
    </>
  );
}

/** The card that pops up while a tile is held (rendered at the page root by the caller's layout). */
function Peek({ tiles, index, pin }: { tiles: Tile[]; index: number; pin: boolean }) {
  const { height } = useWindowDimensions();
  const t = tiles[index];
  const [pop] = useState(() => new Animated.Value(0));
  useEffect(() => { Animated.spring(pop, { toValue: 1, useNativeDriver: true, damping: 16, stiffness: 260 }).start(); }, [pop]);
  const card = (
    <View pointerEvents="none" style={{ position: "absolute", left: 0, right: 0, top: 0, height: height, zIndex: 50 }}>
      <Animated.View style={{ position: "absolute", left: 0, right: 0, top: 0, bottom: 0, backgroundColor: "rgba(0,0,0,0.55)", opacity: pop }} />
      <Animated.View style={{ position: "absolute", left: 12, right: 12, top: height * 0.12, maxHeight: height * 0.56, borderRadius: 18, backgroundColor: colors.sheet,
        borderWidth: 1, borderColor: pin ? colors.primary : colors.borderStrong, overflow: "hidden", shadowColor: "#000", shadowOpacity: 0.6, shadowRadius: 24, shadowOffset: { width: 0, height: 12 },
        opacity: pop, transform: [{ scale: pop.interpolate({ inputRange: [0, 1], outputRange: [0.92, 1] }) }] }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 14, paddingTop: 12, paddingBottom: 8 }}>
          <Text style={{ flex: 1, fontSize: fs.lg, fontWeight: "700", color: colors.text }}>{t.title}</Text>
          <Dots n={tiles.length} i={index} />
        </View>
        <View style={{ paddingHorizontal: 14, paddingBottom: 14 }}>{t.body()}</View>
      </Animated.View>
      <View style={{ position: "absolute", left: 0, right: 0, top: height * 0.12 + height * 0.56 + 12, alignItems: "center" }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 6, paddingHorizontal: 12, paddingVertical: 6, borderRadius: 14, backgroundColor: pin ? colors.primary : "rgba(30,30,30,0.95)" }}>
          <Ionicons name={pin ? "lock-closed" : "swap-horizontal"} size={13} color={pin ? "#000" : colors.textSoft} />
          <Text style={{ fontSize: fs.sm, color: pin ? "#000" : colors.textSoft }}>{pin ? "松手固定为卡片" : "左右滑动切换 · 上滑固定 · 松手关闭"}</Text>
        </View>
      </View>
    </View>
  );
  // its own window above the page (and the tab bar / orb); the finger stays with the tiles underneath
  return Platform.OS === "ios" ? <FullWindowOverlay>{card}</FullWindowOverlay> : card;
}

function Dots({ n, i }: { n: number; i: number }) {
  return (
    <View style={{ flexDirection: "row", gap: 4 }}>
      {Array.from({ length: n }, (_, k) => (
        <View key={k} style={{ width: k === i ? 14 : 5, height: 5, borderRadius: 3, backgroundColor: k === i ? colors.primary : colors.borderStrong }} />
      ))}
    </View>
  );
}

/** Swipeable cards in a native sheet: neighbours peek at the edges and shrink a little; the tab strip follows. */
function Deck({ tiles, index, onClose }: { tiles: Tile[]; index: number | null; onClose: () => void }) {
  const { width } = useWindowDimensions();
  const cardW = width - 44, gap = 10, step = cardW + gap, side = (width - cardW) / 2;
  const [cur, setCur] = useState(index ?? 0);
  const [shownFor, setShownFor] = useState<number | null>(null);
  const list = useRef<FlatList<Tile>>(null);
  const tabs = useRef<ScrollViewInstance>(null);
  const [x] = useState(() => new Animated.Value(0));
  if (index != null && shownFor !== index) {
    setShownFor(index);
    setCur(index);
  }
  if (index == null && shownFor != null) setShownFor(null);
  const go = (i: number) => {
    setCur(i);
    list.current?.scrollToOffset({ offset: i * step, animated: true });
    tabs.current?.scrollTo({ x: Math.max(0, i * 72 - width / 3), animated: true });
  };
  return (
    <Sheet visible={index != null} onClose={onClose} title={tiles[cur]?.title} fill>
      <ScrollView ref={tabs} horizontal showsHorizontalScrollIndicator={false} style={{ marginHorizontal: -16, flexGrow: 0 }} contentContainerStyle={{ paddingHorizontal: 16, gap: 6, paddingBottom: 10 }}>
        {tiles.map((t, i) => (
          <Pressable key={t.key} onPress={() => { void haptic("select"); go(i); }} style={{ paddingHorizontal: 11, height: 30, borderRadius: 15, justifyContent: "center", backgroundColor: i === cur ? colors.primarySoft : colors.cardMuted, borderWidth: 1, borderColor: i === cur ? "rgba(188,255,47,0.35)" : "transparent" }}>
            <Text style={{ fontSize: fs.sm, color: i === cur ? colors.primary : colors.textSoft, fontWeight: i === cur ? "600" : "400" }}>{t.label}</Text>
          </Pressable>
        ))}
      </ScrollView>
      <Animated.FlatList
        ref={list as never}
        data={tiles}
        keyExtractor={(t: Tile) => t.key}
        horizontal
        style={{ marginHorizontal: -16, flex: 1 }}
        contentContainerStyle={{ paddingHorizontal: side }}
        showsHorizontalScrollIndicator={false}
        snapToInterval={step}
        decelerationRate="fast"
        disableIntervalMomentum
        initialScrollIndex={index ?? 0}
        getItemLayout={(_: unknown, i: number) => ({ length: step, offset: step * i, index: i })}
        onScroll={Animated.event([{ nativeEvent: { contentOffset: { x } } }], { useNativeDriver: true })}
        onMomentumScrollEnd={(e: { nativeEvent: { contentOffset: { x: number } } }) => {
          const i = Math.round(e.nativeEvent.contentOffset.x / step);
          if (i !== cur) { void haptic("select"); setCur(i); tabs.current?.scrollTo({ x: Math.max(0, i * 72 - width / 3), animated: true }); }
        }}
        renderItem={({ item, index: i }: { item: Tile; index: number }) => {
          const r = [(i - 1) * step, i * step, (i + 1) * step];
          return (
            <Animated.View style={{ width: cardW, marginRight: gap, borderRadius: radius.lg, backgroundColor: colors.card, borderWidth: 1, borderColor: colors.borderStrong, overflow: "hidden",
              transform: [{ scale: x.interpolate({ inputRange: r, outputRange: [0.94, 1, 0.94], extrapolate: "clamp" }) }],
              opacity: x.interpolate({ inputRange: r, outputRange: [0.55, 1, 0.55], extrapolate: "clamp" }) }}>
              <ScrollView nestedScrollEnabled showsVerticalScrollIndicator={false} contentContainerStyle={{ padding: 14, paddingBottom: 28 }}>
                {item.body()}
              </ScrollView>
            </Animated.View>
          );
        }}
      />
    </Sheet>
  );
}
