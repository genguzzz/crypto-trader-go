// Number and time formatting, ported from web/src/fmt.ts so both clients show the same strings.
import { getSettings } from "./settings";

export function usd(v?: number | null) {
  if (v == null || !isFinite(v)) return "—";
  const a = Math.abs(v);
  if (a >= 1e8) return (v / 1e8).toFixed(2) + "亿";
  if (a >= 1e4) return (v / 1e4).toFixed(a >= 1e6 ? 0 : 1) + "万";
  return v.toFixed(0);
}

export function price(v?: number | null) {
  if (v == null || !isFinite(v) || v === 0) return "—";
  const a = Math.abs(v);
  const d = a >= 1000 ? 1 : a >= 10 ? 3 : a >= 1 ? 4 : a >= 0.01 ? 5 : 8;
  return v.toFixed(d);
}

/** log return → percent string */
export function logPct(v?: number | null, d = 2) {
  if (v == null || !isFinite(v)) return "—";
  const p = (Math.exp(v) - 1) * 100;
  return (p > 0 ? "+" : "") + p.toFixed(d) + "%";
}

/** plain fraction → percent string */
export function pct(v?: number | null, d = 2) {
  if (v == null || !isFinite(v)) return "—";
  return (v > 0 ? "+" : "") + (v * 100).toFixed(d) + "%";
}

export function num(v?: number | null, d = 2) {
  if (v == null || !isFinite(v)) return "—";
  return v.toFixed(d);
}

export function fundingH(v?: number | null) {
  if (v == null || !isFinite(v)) return "—";
  return (v * 100).toFixed(4) + "%";
}

export function fieldValue(key: string, v?: number | null) {
  if (v == null || !isFinite(v)) return "—";
  switch (key) {
    case "turnover12h":
      return usd(v);
    case "funding_hourly":
    case "funding_pred_hourly":
      return fundingH(v) + "/h";
    case "spread_bps":
      return v.toFixed(1) + "bps";
    case "trend_z_4h":
    case "trend_ema":
    case "eff_ratio_24h":
      return v.toFixed(2);
    case "beta_btc_48h":
      return v.toFixed(2) + "β";
    case "vol_surprise_4h":
      return "×" + Math.exp(v).toFixed(2);
    default:
      return logPct(v);
  }
}

// ---- display time zone (Settings → 时区), default UTC+8 like the web app ----
export const tzOptions = [
  { value: "480", label: "UTC+8（北京）" },
  { value: "0", label: "UTC" },
  { value: "local", label: "本机时区" },
];
function tzOffsetMin(ms: number) {
  const v = getSettings().tz;
  return v === "local" ? -new Date(ms).getTimezoneOffset() : Number(v) || 0;
}
const wall = (ms: number) => new Date(ms + tzOffsetMin(ms) * 60_000);
const p2 = (n: number) => String(n).padStart(2, "0");

export function time(ms?: number, withDate = false) {
  if (!ms) return "—";
  const d = wall(ms);
  const hm = `${p2(d.getUTCHours())}:${p2(d.getUTCMinutes())}`;
  return withDate ? `${d.getUTCMonth() + 1}-${d.getUTCDate()} ${hm}` : hm;
}

export function clock(ms?: number) {
  if (!ms) return "—";
  const d = wall(ms);
  return `${p2(d.getUTCHours())}:${p2(d.getUTCMinutes())}:${p2(d.getUTCSeconds())}`;
}

export function ago(ms: number) {
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (s < 60) return s + "s";
  if (s < 3600) return Math.round(s / 60) + "m";
  return Math.round(s / 3600) + "h";
}

export type Tone = "up" | "down" | "";
export const sign = (v?: number | null): Tone => (v == null || v === 0 || !isFinite(v) ? "" : v > 0 ? "up" : "down");
export const catLabel: Record<string, string> = { "1": "加密", "3": "股票", "4": "商品" };
export const sym = (id: string) => id.replace("-USDT-SWAP", "");

/**
 * Funding rate level on its 8h-equivalent value: 0 ≤ 0.01% (OKX's baseline), 1 ≤ 0.05%,
 * 2 ≤ 0.1%, 3 above. `rate` is per settlement, `intervalH` the settlement interval.
 */
export function fundingLevel(rate: number | null | undefined, intervalH = 8) {
  if (rate == null || !isFinite(rate)) return 0;
  const r8 = ((Math.abs(rate) * 8) / (intervalH || 8)) * 100;
  return r8 <= 0.01 ? 0 : r8 <= 0.05 ? 1 : r8 <= 0.1 ? 2 : 3;
}
export const fundingLevelText = ["常态（≤0.01%/8h）", "偏高（≤0.05%/8h）", "高（≤0.1%/8h）", "极端（>0.1%/8h）"];

/** Log-logistic fill of the OI bar (web fmt.oiFrac): half point → 50%, 10× → 76%, 100× → 91%. */
export function oiFrac(v?: number | null, half = getSettings().oiHalf) {
  if (v == null || !(v > 0) || !(half > 0)) return 0;
  const x = Math.pow(v / half, 0.5);
  return x / (1 + x);
}
