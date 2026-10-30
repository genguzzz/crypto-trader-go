import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import Markdown from 'unplugin-vue-markdown/vite'
import Shiki from '@shikijs/markdown-it'
import { createHash } from 'node:crypto'
import { readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join, relative } from 'node:path'
import type { Plugin } from 'vite'
import { nativeRuntime } from './scripts/native-runtime.mjs'

// dist/bundle.json identifies a web build for the iOS app's over-the-air updates (docs/ios.md):
// version = hash of every other output file, runtime = the native binary it needs (scripts/native-runtime.mjs).
function bundleManifest(): Plugin {
  return {
    name: 'ctg-bundle-manifest',
    apply: 'build',
    closeBundle() {
      const out = 'dist'
      const walk = (d: string): string[] => readdirSync(d, { withFileTypes: true }).flatMap(e => e.isDirectory() ? walk(join(d, e.name)) : [join(d, e.name)])
      const lines = walk(out).map(f => relative(out, f).split('\\').join('/')).filter(p => p !== 'bundle.json' && !p.startsWith('.')).sort()
        .map(p => `${p}:${createHash('sha256').update(readFileSync(join(out, p))).digest('hex')}`)
      const version = createHash('sha256').update(lines.join('\n')).digest('hex').slice(0, 16)
      writeFileSync(join(out, 'bundle.json'), JSON.stringify({ version, runtime: nativeRuntime() }) + '\n')
    },
  }
}

// Help articles (src/docs/**/*.md) compile to Vue components at build time; code blocks are
// highlighted by Shiki then, so the browser gets plain HTML.
// Voice input in the iOS app needs NSMicrophoneUsageDescription in the native build this web bundle
// targets (the bundle's runtime id covers Info.plist, so the two always match).
const nativeMic = readFileSync('ios/App/App/Info.plist', 'utf8').includes('NSMicrophoneUsageDescription')

export default defineConfig({
  define: { __NATIVE_MIC__: JSON.stringify(nativeMic) },
  plugins: [
    vue({ include: [/\.vue$/, /\.md$/] }),
    Markdown({
      wrapperClasses: 'md',
      markdownItOptions: { html: false, linkify: true, typographer: false },
      async markdownItSetup(md) {
        md.use(await Shiki({ theme: 'github-dark-dimmed', langs: ['python', 'json', 'bash', 'sh', 'yaml', 'text'], fallbackLanguage: 'text' }))
      },
    }),
    bundleManifest(),
  ],
  server: { proxy: { '/api': { target: process.env.CTG_API || 'http://127.0.0.1:8686', changeOrigin: true } } }, // CTG_API=https://… proxies to a remote instance
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 800 },
})
