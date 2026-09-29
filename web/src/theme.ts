/**
 * Colours for canvas charts (lightweight-charts, footprint, depth). Same values as the CSS tokens
 * in style.css — canvases can't read CSS variables, so both live here and there.
 */
const rgb = { up: '37,167,80', down: '202,63,100', gold: '245,197,66', oi: '91,155,255', oiDown: '247,166,0' }
export const C = {
  bg: '#0e0e0e',        // --bg-1: charts sit on panels
  grid: '#191919',
  border: '#2e2e2e',    // --line-2
  text: '#8a8a8a',
  label: '#222222',     // --bg-3: floating axis label
  up: '#25a750',
  down: '#ca3f64',
  gold: '#f5c542',      // POC, spot line, CVD
  oi: '#5b9bff',
  lime: '#bcff2f',      // --accent
  violet: '#9b7df0',
  amber: '#f7a600',
  ink: 'rgba(245,245,245,.85)',
}
/** rgba() of a named colour at alpha a. */
export const alpha = (k: keyof typeof rgb, a: number) => `rgba(${rgb[k]},${a})`

/**
 * Liquidation-map leverage tiers: one ordinal ramp per side (long = red, short = green), dim at
 * low leverage, bright at high — validated for monotone lightness on the dark panel. Colour
 * follows the tier's leverage, so hiding a tier never repaints the others.
 */
export const LIQ_TIERS = [5, 10, 25, 50, 100]
const liqRamp = {
  long: ['#7b2a40', '#a13451', '#ca3f64', '#d56583', '#e090a5'],
  short: ['#1b6734', '#208541', '#25a750', '#51b973', '#81cc9a'],
}
/** Colour of a leverage tier on one side (a contract below 5x max leverage takes the 5x step). */
export const liqTierColor = (lever: number, long: boolean) => {
  let i = 0
  LIQ_TIERS.forEach((l, k) => { if (lever >= l) i = k })
  return (long ? liqRamp.long : liqRamp.short)[i]
}
