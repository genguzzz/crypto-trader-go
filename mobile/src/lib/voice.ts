// Voice for the AI chat (port of web/src/composables/recorder.ts + holdToTalk.ts): expo-audio records
// m4a with metering for a live level waveform; the clip goes to /api/v1/ai/transcribe (Groq Whisper).
// expo-audio is loaded lazily behind has("audio") so a binary without it never crashes on import.
import { useCallback, useEffect, useRef, useState } from "react";
import { has, haptic, micPermission } from "./device";
import { transcribe } from "./ai";
import { logger } from "./log";

const log = logger("voice");
const BARS = 24;

type Recorder = {
  prepareToRecordAsync: () => Promise<void>;
  record: () => void;
  stop: () => Promise<void>;
  getStatus: () => { metering?: number; isRecording: boolean };
  uri: string | null;
  release?: () => void;
};

async function makeRecorder(): Promise<Recorder> {
  const A = await import("expo-audio");
  const { default: AudioModule } = await import("expo-audio/build/AudioModule");
  const { createRecordingOptions } = await import("expo-audio/build/utils/options");
  // take audio focus while the owner talks: music / video in other apps pauses (default mixWithOthers kept playing)
  await A.setAudioModeAsync({ allowsRecording: true, playsInSilentMode: true, interruptionMode: "doNotMix" });
  const opts = createRecordingOptions({ ...A.RecordingPresets.HIGH_QUALITY, isMeteringEnabled: true });
  return new (AudioModule as unknown as { AudioRecorder: new (o: unknown) => Recorder }).AudioRecorder(opts);
}

async function audioOff() {
  try {
    const A = await import("expo-audio");
    await A.setAudioModeAsync({ allowsRecording: false, interruptionMode: "mixWithOthers" });
    // release the session; iOS notifies the paused app (notifyOthersOnDeactivation) so it can resume
    await A.setIsAudioActiveAsync(false);
  } catch {
    // best effort
  }
}

/** Recording with a live level (0–1) and the recent levels (waveform bars, newest last). */
export function useRecorder() {
  const [levels, setLevels] = useState<number[]>(() => Array(BARS).fill(0));
  const [level, setLevel] = useState(0);
  const [secs, setSecs] = useState(0);
  const rec = useRef<Recorder | null>(null);
  const timer = useRef<ReturnType<typeof setInterval> | null>(null);
  const startedAt = useRef(0);
  const lv = useRef(0);

  const cleanup = useCallback(() => {
    if (timer.current) clearInterval(timer.current);
    timer.current = null;
    lv.current = 0;
    setLevel(0);
    setSecs(0);
    setLevels(Array(BARS).fill(0));
  }, []);

  const start = useCallback(async () => {
    if (rec.current) return;
    if (!has("audio")) throw new Error("此设备不支持录音（需要安装含麦克风权限的新版 App）");
    if (!(await micPermission())) throw new Error("没有麦克风权限");
    const r = await makeRecorder();
    await r.prepareToRecordAsync();
    r.record();
    rec.current = r;
    startedAt.current = Date.now();
    timer.current = setInterval(() => {
      // metering is dBFS (-160..0); speech sits around -45..-10 (same mapping as Murmur and the web).
      // Rise at once, fall a little smoothed, so the bars track the voice without flicker.
      const db = r.getStatus().metering;
      const x = db == null ? 0 : Math.max(0, Math.min(1, (db + 55) / 45));
      lv.current = x >= lv.current ? x : lv.current * 0.55 + x * 0.45;
      setLevel(lv.current);
      setLevels((a) => [...a.slice(1), lv.current]);
      setSecs(Math.floor((Date.now() - startedAt.current) / 1000));
    }, 50);
  }, []);

  /** Stop and return the file (null when shorter than 0.5 s). */
  const stop = useCallback(async (): Promise<string | null> => {
    const r = rec.current;
    rec.current = null;
    cleanup();
    if (!r) return null;
    const long = Date.now() - startedAt.current >= 500;
    try {
      await r.stop();
    } finally {
      void audioOff();
    }
    const uri = r.uri;
    r.release?.();
    return long && uri ? uri : null;
  }, [cleanup]);

  const cancel = useCallback(() => {
    const r = rec.current;
    rec.current = null;
    cleanup();
    if (r) {
      void r.stop().catch(() => {}).finally(() => {
        r.release?.();
        void audioOff();
      });
    }
  }, [cleanup]);

  useEffect(() => () => cancel(), [cancel]);
  return { level, levels, secs, start, stop, cancel };
}

async function toText(uri: string | null, onText: (t: string) => void, onError: (m: string) => void) {
  if (!uri) return onError("说话时间太短");
  const text = (await transcribe(uri)).trim();
  if (text) onText(text);
  else onError("没有识别到语音");
}

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

/** Tap to start, tap again to stop and send (the composer mic). cancel() drops the recording. */
export function useTapToTalk(opts: { onText: (text: string) => void; onError: (msg: string) => void }) {
  const rec = useRecorder();
  const [phase, setPhase] = useState<"idle" | "rec" | "busy">("idle");
  const o = useRef(opts);
  useEffect(() => {
    o.current = opts;
  });

  const toggle = async () => {
    if (phase === "busy") return;
    if (phase === "idle") {
      setPhase("rec");
      void haptic("medium");
      try {
        await rec.start();
      } catch (e) {
        log.warn("start failed", e);
        setPhase("idle");
        o.current.onError(errText(e));
      }
      return;
    }
    setPhase("busy");
    try {
      await toText(await rec.stop(), o.current.onText, o.current.onError);
    } catch (e) {
      o.current.onError(errText(e));
    } finally {
      setPhase("idle");
    }
  };

  const cancel = () => {
    if (phase === "rec") rec.cancel();
    if (phase !== "busy") setPhase("idle");
  };

  return { phase, levels: rec.levels, level: rec.level, secs: rec.secs, toggle, cancel };
}

const HOLD_MS = 260;
const CANCEL_PX = 70;
const NEW_PX = 70;

/** Tap vs press-and-hold on one view (responder props): a tap calls onTap; holding records until
 *  release, then transcribes and calls onText. Sliding up by CANCEL_PX arms "release to cancel";
 *  with `newChat`, sliding left by NEW_PX arms "release to send as a new conversation" (onText gets
 *  fresh = true). */
export function useHoldToTalk(opts: { onTap?: () => void; onText: (text: string, fresh?: boolean) => void; onError: (msg: string) => void; newChat?: boolean }) {
  const rec = useRecorder();
  const [phase, setPhase] = useState<"idle" | "arming" | "rec" | "busy">("idle");
  const [cancelZone, setCancelZone] = useState(false);
  const [newZone, setNewZone] = useState(false);
  const fresh = useRef(false);
  const startX = useRef(0);
  const ph = useRef<"idle" | "arming" | "rec" | "busy">("idle");
  const o = useRef(opts);
  useEffect(() => {
    o.current = opts;
  });
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const startY = useRef(0);
  const zone = useRef(false);

  const set = (p: typeof phase) => {
    ph.current = p;
    setPhase(p);
  };

  const handlers = {
    onStartShouldSetResponder: () => ph.current !== "busy",
    onResponderTerminationRequest: () => ph.current === "idle",
    onResponderGrant: (e: { nativeEvent: { pageX: number; pageY: number } }) => {
      startY.current = e.nativeEvent.pageY;
      startX.current = e.nativeEvent.pageX;
      zone.current = false;
      fresh.current = false;
      setCancelZone(false);
      setNewZone(false);
      set("arming");
      timer.current = setTimeout(async () => {
        timer.current = null;
        if (ph.current !== "arming") return;
        set("rec");
        void haptic("medium");
        try {
          await rec.start();
          if ((ph.current as string) !== "rec") rec.cancel(); // released while the mic was opening
        } catch (err) {
          set("idle");
          o.current.onError(errText(err));
        }
      }, HOLD_MS);
    },
    onResponderMove: (e: { nativeEvent: { pageX: number; pageY: number } }) => {
      if (ph.current !== "rec") return;
      const z = startY.current - e.nativeEvent.pageY > CANCEL_PX;
      const n = !z && !!o.current.newChat && startX.current - e.nativeEvent.pageX > NEW_PX;
      if (z !== zone.current) {
        zone.current = z;
        setCancelZone(z);
        void haptic("select");
      }
      if (n !== fresh.current) {
        fresh.current = n;
        setNewZone(n);
        void haptic("select");
      }
    },
    onResponderRelease: async () => {
      if (timer.current) {
        clearTimeout(timer.current);
        timer.current = null;
        set("idle");
        o.current.onTap?.();
        return;
      }
      if (ph.current !== "rec") return;
      if (zone.current) {
        rec.cancel();
        set("idle");
        zone.current = false;
        setCancelZone(false);
        return;
      }
      set("busy");
      const asNew = fresh.current;
      fresh.current = false;
      setNewZone(false);
      try {
        await toText(await rec.stop(), (t) => o.current.onText(t, asNew), o.current.onError);
      } catch (err) {
        o.current.onError(errText(err));
      } finally {
        set("idle");
      }
    },
    onResponderTerminate: () => {
      if (timer.current) clearTimeout(timer.current);
      timer.current = null;
      if (ph.current === "rec") rec.cancel();
      if (ph.current !== "busy") set("idle");
      zone.current = false;
      setCancelZone(false);
      fresh.current = false;
      setNewZone(false);
    },
  };

  return { phase, cancelZone, newZone, levels: rec.levels, level: rec.level, secs: rec.secs, handlers };
}
