// Design tokens: the web app's dark trading-terminal theme (web/src/style.css, web/src/theme.ts) —
// pure black canvas, neutral grey panels and hairlines, OKX lime accent, OKX green/red for up/down.
// One palette (the web app has no light theme). Keep values in step with style.css.

export const colors = {
  // surfaces
  bg: "#000000", // --bg-0 app background
  card: "#0e0e0e", // --bg-1 panel
  raised: "#1a1a1a", // --bg-2 raised / pressed
  cardMuted: "#262626", // --bg-3 input / selected
  oiBar: "#141414", // scanner open-interest bar
  border: "#1d1d1d", // --line hairline
  borderStrong: "#383838", // --line-2 inputs, chips
  sheet: "#0e0e0e",
  backdrop: "rgba(0,0,0,0.6)",
  // text
  text: "#fafafa", // --text-1
  textSoft: "#b3b3b3", // --text-2
  textFaint: "#909090", // --text-3
  textDim: "#5b5b5b", // --text-4
  // semantic
  up: "#25a750",
  upBg: "rgba(37,167,80,0.14)",
  down: "#ca3f64",
  downBg: "rgba(202,63,100,0.14)",
  warn: "#f7a600",
  warnBg: "rgba(247,166,0,0.12)",
  primary: "#bcff2f", // --accent (lime)
  primarySoft: "rgba(188,255,47,0.1)",
  primarySolid: "#1f2611",
  onPrimary: "#000000",
  info: "#5b9bff",
  infoBg: "rgba(91,155,255,0.14)",
  gold: "#f5c542",
  // aliases used by the shared components
  success: "#25a750",
  warning: "#f7a600",
  danger: "#ca3f64",
  white: "#ffffff",
};

export const radius = { sm: 6, md: 10, lg: 16, pill: 999 };

// Font sizes (web: 11/12/13/14/16/20/28).
export const fs = { xs: 11, sm: 12, md: 13, lg: 14, xl: 16, x2: 20, x3: 28 };

// Numbers never jitter while prices update.
export const tnum = { fontVariant: ["tabular-nums" as const] };

export const shadow = () => ({});
