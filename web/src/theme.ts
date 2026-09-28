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
