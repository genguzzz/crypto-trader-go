// Screenshots of the trade explorer (report + live account) including hover / tap states.
//
//   node scripts/mock-trade.mjs &                          # fake data incl. a 253-trade account (id 5)
//   node scripts/shot-report.mjs [baseUrl] [outDir] [reportId] [liveId]
//     default http://127.0.0.1:5180 → ../data/shots/report-ux, report 5, live 1
//   PREFIX=real- node scripts/shot-report.mjs http://127.0.0.1:5182 ../data/shots/report-ux 1 4   # real server via PASSTHROUGH
// Fails (exit 1) on page errors or phone overflow. Env: CHROME, ONLY (name filter).
import { chromium } from 'playwright-core'
import { existsSync, mkdirSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { homedir } from 'node:os'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const base = (process.argv[2] || 'http://127.0.0.1:5180').replace(/\/$/, '')
const out = process.argv[3] || join(here, '..', '..', 'data', 'shots', 'report-ux')
const rid = process.argv[4] || '5', lid = process.argv[5] || '1'
const P = process.env.PREFIX || ''
mkdirSync(out, { recursive: true })
function findChrome() {
  if (process.env.CHROME) return process.env.CHROME
  const dir = join(homedir(), '.cache', 'ms-playwright')
  const builds = existsSync(dir) ? readdirSync(dir).filter(d => /^chromium-\d+$/.test(d)).sort((a, b) => +b.split('-')[1] - +a.split('-')[1]) : []
  for (const b of builds) if (existsSync(join(dir, b, 'chrome-linux/chrome'))) return join(dir, b, 'chrome-linux/chrome')
  return '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
}

/** Page coordinates of the chart's bubble / cluster hit areas (exposed by the chart for tests). */
async function hits(page) {
  return page.evaluate(() => {
    const el = [...document.querySelectorAll('.cv')].find(e => e.offsetParent && e.__hits)
    if (!el) return []
    const r = el.getBoundingClientRect()
    return el.__hits().map(h => ({ x: r.left + h.x, y: r.top + h.y, n: h.ids.length, ids: h.ids }))
  })
}
const wait = (page, ms = 500) => page.waitForTimeout(ms)
let failed = false
const browser = await chromium.launch({ executablePath: findChrome() })

async function shot(page, name, full = false) {
  const file = join(out, `${P}${name}.png`)
  const vw = page.viewportSize().width
  const h = full ? Math.min(2400, await page.evaluate(() => document.documentElement.scrollHeight)) : undefined
  await page.screenshot({ path: file, fullPage: full, clip: full ? { x: 0, y: 0, width: vw, height: h } : undefined })
  const sw = await page.evaluate(() => document.documentElement.scrollWidth)
  const over = vw <= 500 && sw > vw
  if (over) failed = true
  console.log(file + (over ? `  OVERFLOW ${sw}` : ''))
}
const want = n => !process.env.ONLY || n.includes(process.env.ONLY)

// ---------------- desktop ----------------
{
  const ctx = await browser.newContext({ viewport: { width: 1600, height: 900 } })
  const page = await ctx.newPage()
  page.on('pageerror', e => { failed = true; console.log('pageerror', e.message) })
  await page.goto(`${base}/trading/backtests/${rid}`)
  await wait(page, 2500)
  const panel = page.locator('.tx').first()
  await panel.scrollIntoViewIfNeeded()
  await wait(page)
  if (want('report-desktop')) await shot(page, 'report-desktop')
  // scroll the content so the explorer fills the window
  await page.evaluate(() => document.querySelector('.tx')?.scrollIntoView({ block: 'start' }))
  await wait(page)
  if (want('equity')) await shot(page, 'equity-desktop')
  let hs = await hits(page)
  const single = hs.find(h => h.n === 1), clusterHit = hs.find(h => h.n > 2)
  if (single && want('hover')) { await page.mouse.move(single.x, single.y); await wait(page, 400); await shot(page, 'equity-hover-trade-desktop') }
  if (clusterHit && want('hover')) { await page.mouse.move(clusterHit.x, clusterHit.y); await wait(page, 400); await shot(page, 'equity-hover-cluster-desktop') }
  // crosshair legend somewhere mid-chart
  const box = await page.locator('.etc .cv').first().boundingBox()
  if (box && want('crosshair')) { await page.mouse.move(box.x + box.width * 0.55, box.y + box.height * 0.5); await wait(page, 300); await shot(page, 'equity-crosshair-desktop') }
  if (clusterHit && want('cluster')) {
    await page.mouse.click(clusterHit.x, clusterHit.y); await wait(page, 700)
    await page.mouse.move(5, 5); await wait(page, 300)
    await shot(page, 'equity-cluster-zoomed-desktop')
    await page.locator('button[aria-label="显示全部"]').click(); await wait(page)
  }
  // filter: losses only → other nodes fade
  if (want('filter')) {
    await page.getByRole('radio', { name: '亏损' }).first().click().catch(() => {})
    await wait(page, 500)
    await shot(page, 'equity-filter-loss-desktop')
    await page.getByRole('radio', { name: '全部' }).nth(1).click().catch(() => {})
    await wait(page, 300)
  }
  // click a bubble → 币对视角
  hs = await hits(page)
  const one = hs.filter(h => h.n === 1)[Math.floor(hs.filter(h => h.n === 1).length / 2)]
  if (one) {
    await page.mouse.click(one.x, one.y); await wait(page, 1500)
    await page.mouse.move(5, 5); await wait(page, 300)
    if (want('kline')) await shot(page, 'kline-desktop')
    await page.keyboard.press('ArrowRight'); await wait(page, 900)
    if (want('kline')) await shot(page, 'kline-next-desktop')
    const kh = await hits(page)
    const k1 = kh.find(h => h.n === 1)
    if (k1 && want('kline')) { await page.mouse.move(k1.x, k1.y); await wait(page, 300); await shot(page, 'kline-hover-desktop') }
  }
  // instrument chip with most trades
  if (want('chips')) {
    await page.locator('.chip2').nth(2).click().catch(() => {}); await wait(page, 1200)
    await page.mouse.move(5, 5)
    await shot(page, 'kline-inst-switch-desktop')
  }
  await page.close()

  if (want('live')) {
    const lp = await ctx.newPage()
    lp.on('pageerror', e => { failed = true; console.log('pageerror', e.message) })
    await lp.goto(`${base}/trading/accounts/${lid}`); await wait(lp, 2500)
    await shot(lp, 'live-account-desktop')
    await lp.close()
  }
  if (want('risk')) {
    const wp = await ctx.newPage()
    await wp.goto(`${base}/trading/new`); await wait(wp, 1200)
    await wp.getByRole('button', { name: '下一步' }).click(); await wait(wp, 400)
    await shot(wp, 'risk-slider-desktop')
    await wp.fill('input[aria-label="单笔风险（%）"]', '150'); await wait(wp, 300)
    await wp.locator('.rs').scrollIntoViewIfNeeded()
    await shot(wp, 'risk-slider-over-desktop')
    await wp.close()
  }
  await ctx.close()
}

// ---------------- phone ----------------
{
  const ctx = await browser.newContext({ viewport: { width: 400, height: 860 }, isMobile: true, hasTouch: true, deviceScaleFactor: 2 })
  const page = await ctx.newPage()
  page.on('pageerror', e => { failed = true; console.log('pageerror', e.message) })
  await page.goto(`${base}/trading/backtests/${rid}`)
  await wait(page, 2500)
  if (want('report-phone')) await shot(page, 'report-phone', true)
  await page.evaluate(() => document.querySelector('.tx')?.scrollIntoView({ block: 'start' }))
  await wait(page)
  const hs = (await hits(page)).filter(h => h.n === 1)
  const one = hs[Math.floor(hs.length / 2)]
  if (one) {
    await page.touchscreen.tap(one.x, one.y); await wait(page, 600)
    if (want('tap')) await shot(page, 'equity-tap-phone')
    await page.getByRole('button', { name: '在 K 线中查看' }).click().catch(() => {}); await wait(page, 1500)
    await page.evaluate(() => document.querySelector('.tx')?.scrollIntoView({ block: 'start' }))
    await wait(page, 300)
    if (want('kline')) await shot(page, 'kline-phone')
    await page.evaluate(() => window.scrollBy(0, 360)); await wait(page, 300)
    if (want('kline')) await shot(page, 'kline-card-phone')
  }
  // tab bar with 更多 open
  if (want('more')) {
    await page.locator('.tabbar .tab.more').click(); await wait(page, 400)
    await shot(page, 'tabbar-more-phone')
  }
  await page.close()
  if (want('live')) {
    const lp = await ctx.newPage()
    await lp.goto(`${base}/trading/accounts/${lid}`); await wait(lp, 2500)
    await shot(lp, 'live-account-phone', true)
    await lp.close()
  }
  if (want('risk')) {
    const wp = await ctx.newPage()
    await wp.goto(`${base}/trading/new`); await wait(wp, 1200)
    await wp.getByRole('button', { name: '下一步' }).click(); await wait(wp, 400)
    await wp.fill('input[aria-label="单笔风险（%）"]', '150'); await wait(wp, 300)
    await wp.locator('.rs').scrollIntoViewIfNeeded()
    await shot(wp, 'risk-slider-phone')
    await wp.close()
  }
  await ctx.close()
}
await browser.close()
process.exit(failed ? 1 : 0)
