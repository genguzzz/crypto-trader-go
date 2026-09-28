// Screenshot a running ctg instance on desktop (1600×900) and phone (400×860) with the local Chrome.
// Usage: node web/scripts/shot.mjs [url] [outDir]   (default http://127.0.0.1:8686/ → ./shots)
// Env: WAIT=ms to wait after load (default 5000), CHROME=path to the Chrome binary.
// Fails (exit 1) on page errors or when the phone layout overflows horizontally.
import { chromium } from 'playwright-core'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'

const url = process.argv[2] || 'http://127.0.0.1:8686/'
const out = process.argv[3] || 'shots'
const wait = Number(process.env.WAIT || 5000)
const chrome = process.env.CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
mkdirSync(out, { recursive: true })

const browser = await chromium.launch({ executablePath: chrome })
let failed = false
for (const [name, width, height] of [['desktop', 1600, 900], ['phone', 400, 860]]) {
  const page = await (await browser.newContext({ viewport: { width, height } })).newPage()
  const errors = []
  page.on('pageerror', e => errors.push(e.message))
  await page.goto(url)
  await page.waitForTimeout(wait)
  const file = join(out, `${name}.png`)
  await page.screenshot({ path: file })
  const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth)
  const overflow = name === 'phone' && scrollWidth > width
  if (errors.length || overflow) failed = true
  console.log(`${file}  errors=${JSON.stringify(errors)}  scrollWidth=${scrollWidth}${overflow ? '  OVERFLOW' : ''}`)
}
await browser.close()
process.exit(failed ? 1 : 0)
