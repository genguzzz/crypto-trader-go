// End-to-end check of the trading UI against a real server, then desktop + phone screenshots.
// The server's embedded web build may be older, so serve this checkout's build in front of it:
//
//   PASSTHROUGH=1 PORT=5181 BACKEND=http://127.0.0.1:8787 node scripts/mock-trade.mjs &
//   ADMIN_KEY=$(cat /path/to/admin_key) node scripts/trade-e2e.mjs [baseUrl] [outDir]
//
// Flows: create a backtest account (majors-trend, last DAYS days) through the wizard, run it and wait
// for the report; create a live account (majors-trend, exec auto) and start it. Then screenshots of
// every trading page and the trading settings. Exit 1 on page errors, API errors (except expected
// ones) or phone overflow. Env: DAYS (default 10), SKIP_FLOWS=1 (screenshots only), CHROME.
import { chromium } from 'playwright-core'
import { existsSync, mkdirSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { homedir } from 'node:os'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const base = (process.argv[2] || 'http://127.0.0.1:5181').replace(/\/$/, '')
const out = process.argv[3] || join(here, '..', '..', 'data', 'shots', 'trade-real')
const key = process.env.ADMIN_KEY || ''
const DAYS = Number(process.env.DAYS || 10)
mkdirSync(out, { recursive: true })

function findChrome() {
  if (process.env.CHROME) return process.env.CHROME
  const dir = join(homedir(), '.cache', 'ms-playwright')
  const builds = existsSync(dir) ? readdirSync(dir).filter(d => /^chromium-\d+$/.test(d)).sort((a, b) => +b.split('-')[1] - +a.split('-')[1]) : []
  for (const b of builds) if (existsSync(join(dir, b, 'chrome-linux/chrome'))) return join(dir, b, 'chrome-linux/chrome')
  return '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
}
const day = ms => { const d = new Date(ms + 8 * 3600e3); return d.toISOString().slice(0, 10) } // UTC+8 date

const browser = await chromium.launch({ executablePath: findChrome() })
let failed = false
const problems = []
function watch(page, tag) {
  page.on('pageerror', e => { failed = true; problems.push(`${tag} pageerror: ${e.message}`) })
  page.on('response', r => {
    if (r.url().includes('/api/') && r.status() >= 400 && !r.url().includes('/ai/memory/')) problems.push(`${tag} HTTP ${r.status()} ${r.request().method()} ${r.url().replace(base, '')}`)
  })
}
async function newPage(ctx, tag) {
  const page = await ctx.newPage()
  watch(page, tag)
  await page.addInitScript(k => { try { localStorage.setItem('ctg.adminKey', k) } catch { /* */ } }, key)
  return page
}
const click = async (page, name) => { await page.getByRole('button', { name }).first().click(); await page.waitForTimeout(400) }
async function addStrategy(page, name) {
  await click(page, '添加策略')
  await page.locator('.it', { hasText: name }).getByRole('button', { name: '添加' }).click()
  await page.waitForTimeout(500)
}

const ids = { bt: process.env.BT_ID, live: process.env.LIVE_ID }
if (!process.env.SKIP_FLOWS) {
  ids.bt = ids.live = undefined
  const ctx = await browser.newContext({ viewport: { width: 1600, height: 900 } })
  // ---- backtest ----
  let page = await newPage(ctx, 'backtest-flow')
  await page.goto(`${base}/trading/new?mode=backtest`)
  await page.waitForTimeout(1200)
  await page.fill('#w-name', `E2E 回测 ${DAYS} 天`)
  await page.fill('input[aria-label="开始日期"]', day(Date.now() - DAYS * 86400e3))
  await page.fill('input[aria-label="结束日期"]', day(Date.now()))
  await page.screenshot({ path: join(out, 'flow-backtest-1.png') })
  await click(page, '下一步'); await click(page, '下一步')
  await addStrategy(page, 'majors-trend')
  await page.screenshot({ path: join(out, 'flow-backtest-3.png') })
  await click(page, '下一步')
  await page.screenshot({ path: join(out, 'flow-backtest-4.png') })
  await click(page, '创建并开始回测')
  await page.waitForURL(/\/trading\/backtests\/\d+/, { timeout: 20_000 })
  ids.bt = page.url().match(/backtests\/(\d+)/)[1]
  await page.waitForFunction(() => /已完成|失败|已取消/.test(document.querySelector('.hd')?.textContent || ''), null, { timeout: 180_000 }).catch(() => problems.push('backtest did not finish in 180 s'))
  await page.waitForTimeout(1500)
  await page.screenshot({ path: join(out, 'flow-backtest-report.png') })
  console.log('backtest account', ids.bt, '→', (await page.locator('.hd').textContent())?.replace(/\s+/g, ' '))
  await page.close()

  // ---- live ----
  page = await newPage(ctx, 'live-flow')
  await page.goto(`${base}/trading/new`)
  await page.waitForTimeout(1200)
  await page.fill('#w-name', 'E2E 实时趋势')
  await click(page, '下一步'); await click(page, '下一步')
  await addStrategy(page, 'majors-trend')
  await click(page, '下一步')
  await click(page, '创建账户')
  await page.waitForURL(/\/trading\/accounts\/\d+/, { timeout: 20_000 })
  ids.live = page.url().match(/accounts\/(\d+)/)[1]
  await page.waitForTimeout(3000)
  console.log('live account', ids.live, '→', (await page.locator('.ic .st').allTextContents()).join(','))
  await page.close()
  await ctx.close()
}

// ---- screenshots ----
if (!ids.bt || !ids.live) {
  const r = await (await fetch(`${base}/api/v1/trade/accounts`)).json()
  ids.bt ||= String(r.find(a => a.mode === 'backtest')?.id ?? '')
  ids.live ||= String(r.find(a => a.mode === 'live')?.id ?? '')
}
const pages = [
  ['accounts', '/trading'],
  ['account-live', `/trading/accounts/${ids.live}`],
  ['account-backtest', `/trading/accounts/${ids.bt}`],
  ['report', `/trading/backtests/${ids.bt}`],
  ['wizard', '/trading/new'],
  ['strategies', '/trading/strategies'],
  ['prompts', '/trading/prompts'],
  ['ai', '/trading/ai'],
  ['settings-interact', '/settings', '#interact'],
  ['settings-ai', '/settings', '#ai'],
]
for (const [dev, width, height] of [['desktop', 1600, 900], ['phone', 400, 860]]) {
  const ctx = await browser.newContext({ viewport: { width, height }, ...(dev === 'phone' ? { isMobile: true, hasTouch: true } : {}) })
  for (const [name, path, anchor] of pages) {
    const page = await newPage(ctx, `${name}-${dev}`)
    await page.goto(base + path)
    await page.waitForTimeout(2500)
    if (anchor) { await page.locator(anchor).scrollIntoViewIfNeeded(); await page.waitForTimeout(300) }
    const full = dev === 'phone' && !anchor
    const h = full ? Math.min(1720, await page.evaluate(() => document.documentElement.scrollHeight)) : height
    const file = join(out, `${name}-${dev}.png`)
    await page.screenshot({ path: file, fullPage: full, clip: full ? { x: 0, y: 0, width, height: h } : undefined })
    const sw = await page.evaluate(() => document.documentElement.scrollWidth)
    if (dev === 'phone' && sw > width) { failed = true; problems.push(`${name} phone overflow ${sw}`) }
    console.log(file)
    await page.close()
  }
  await ctx.close()
}
await browser.close()
if (problems.length) console.log('PROBLEMS:\n' + problems.join('\n'))
process.exit(failed ? 1 : 0)
