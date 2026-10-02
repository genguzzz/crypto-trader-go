// Screenshot the trading pages (desktop 1600×900 viewport, phone 400×860 full page) against a
// running server — normally scripts/mock-trade.mjs. Fails (exit 1) on page errors or when a phone
// page overflows horizontally.
//
//   node scripts/mock-trade.mjs &            # serves web/dist + fake trading API on :5180
//   node scripts/shot-trade.mjs [baseUrl] [outDir]   (default http://127.0.0.1:5180 → ../data/shots/trade)
// Env: CHROME = Chromium/Chrome binary (default: newest ~/.cache/ms-playwright/chromium-*), ONLY = name filter.
import { chromium } from 'playwright-core'
import { existsSync, mkdirSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { homedir } from 'node:os'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const base = (process.argv[2] || 'http://127.0.0.1:5180').replace(/\/$/, '')
const out = process.argv[3] || join(here, '..', '..', 'data', 'shots', 'trade')
mkdirSync(out, { recursive: true })

function findChrome() {
  if (process.env.CHROME) return process.env.CHROME
  for (const p of ['/opt/pw-browsers/chromium/chrome-linux/chrome']) if (existsSync(p)) return p
  const dir = join(homedir(), '.cache', 'ms-playwright')
  const builds = existsSync(dir) ? readdirSync(dir).filter(d => /^chromium-\d+$/.test(d)).sort((a, b) => +b.split('-')[1] - +a.split('-')[1]) : []
  for (const b of builds) for (const sub of ['chrome-linux/chrome', 'chrome-linux64/chrome']) if (existsSync(join(dir, b, sub))) return join(dir, b, sub)
  return '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
}

const click = text => async page => { await page.getByRole('button', { name: text }).first().click(); await page.waitForTimeout(400) }
const next = click('下一步')
const pages = [
  { name: 'accounts', path: '/trading' },
  { name: 'account-live', path: '/trading/accounts/1' },
  { name: 'account-halted', path: '/trading/accounts/2' },
  { name: 'report', path: '/trading/backtests/3' },
  { name: 'wizard-1', path: '/trading/new' },
  { name: 'wizard-2', path: '/trading/new', steps: [next] },
  { name: 'wizard-3', path: '/trading/new?mode=backtest&strategy=3c4d5e6f7a8b', steps: [next, next] },
  { name: 'wizard-4', path: '/trading/new?mode=backtest&strategy=9f8e7d6c5b4a', steps: [next, next, next] },
  { name: 'strategies', path: '/trading/strategies' },
  { name: 'prompts', path: '/trading/prompts' },
  { name: 'ai', path: '/trading/ai' },
  { name: 'settings-trade', path: '/settings', scrollTo: '#interact' },
  { name: 'admin-key', path: '/trading/accounts/1', noKey: true, steps: [click('入金'), async p => { await p.getByRole('button', { name: '确认入金' }).click(); await p.waitForTimeout(500) }] },
  { name: 'more-menu', path: '/trading', phoneOnly: true, steps: [click('更多')] },
  // end to end: create a backtest account through the wizard; lands on its (running) report
  { name: 'created-backtest', path: '/trading/new?mode=backtest&strategy=9f8e7d6c5b4a', steps: [next, next, next, click('创建并开始回测'), async p => { await p.waitForURL(/\/trading\/backtests\/\d+/); await p.waitForTimeout(6500) }] },
].filter(p => !process.env.ONLY || p.name.includes(process.env.ONLY))

const browser = await chromium.launch({ executablePath: findChrome() })
let failed = false
for (const [dev, width, height] of [['desktop', 1600, 900], ['phone', 400, 860]]) {
  const ctx = await browser.newContext({ viewport: { width, height }, deviceScaleFactor: 1, ...(dev === 'phone' ? { isMobile: true, hasTouch: true } : {}) })
  for (const pg of pages) {
    if (pg.phoneOnly && dev !== 'phone') continue
    const page = await ctx.newPage()
    const errors = []
    page.on('pageerror', e => errors.push(e.message))
    await page.addInitScript(k => { try { k ? localStorage.setItem('ctg.adminKey', k) : localStorage.removeItem('ctg.adminKey') } catch { /* */ } }, pg.noKey ? '' : 'mock-owner-key')
    await page.goto(base + pg.path)
    await page.waitForTimeout(1500)
    for (const s of pg.steps || []) await s(page)
    if (pg.scrollTo) { await page.locator(pg.scrollTo).scrollIntoViewIfNeeded(); await page.waitForTimeout(300) }
    const file = join(out, `${pg.name}-${dev}.png`)
    // phone: the first ~2 screens of the page (a full page of a long list is unreadable)
    const full = dev === 'phone' && !pg.scrollTo && !pg.noKey
    const h = full ? Math.min(1720, await page.evaluate(() => document.documentElement.scrollHeight)) : height
    await page.screenshot({ path: file, fullPage: full, clip: full ? { x: 0, y: 0, width, height: h } : undefined })
    const sw = await page.evaluate(() => document.documentElement.scrollWidth)
    const overflow = dev === 'phone' && sw > width
    if (errors.length || overflow) failed = true
    console.log(`${file}  errors=${JSON.stringify(errors)}  scrollWidth=${sw}${overflow ? '  OVERFLOW' : ''}`)
    await page.close()
  }
  await ctx.close()
}
await browser.close()
process.exit(failed ? 1 : 0)
