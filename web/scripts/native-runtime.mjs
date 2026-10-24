// Native runtime id of the iOS app: a hash of everything compiled into the binary (the Xcode
// project, the local live-update plugin, the Capacitor config and native plugin versions).
// The web build records it in dist/bundle.json, CI writes it into the IPA's Info.plist, and an
// over-the-air bundle is only installed by a binary with the same id (docs/ios.md).
// CLI: node scripts/native-runtime.mjs → prints the id.
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const webDir = join(dirname(fileURLToPath(import.meta.url)), '..')
const NATIVE_PATHS = ['ios', 'native', 'capacitor.config.ts']
const NATIVE_PACKAGE = /^node_modules\/(@capacitor\/(?!cli$)[^/]+|ctg-live-update)$/

export function nativeRuntime() {
  let files
  try {
    files = execFileSync('git', ['ls-files', '-z', '--', ...NATIVE_PATHS], { cwd: webDir }).toString().split('\0').filter(Boolean).sort()
  } catch {
    return 'dev' // no git checkout: never matches a released binary
  }
  const h = createHash('sha256')
  for (const f of files) h.update(`${f}\0`).update(readFileSync(join(webDir, f))).update('\0')
  const lock = JSON.parse(readFileSync(join(webDir, 'package-lock.json'), 'utf8'))
  for (const [k, v] of Object.entries(lock.packages).sort(([a], [b]) => a.localeCompare(b))) {
    if (NATIVE_PACKAGE.test(k)) h.update(`${k}@${v.version}\0`)
  }
  return h.digest('hex').slice(0, 12)
}

if (process.argv[1] === fileURLToPath(import.meta.url)) console.log(nativeRuntime())
