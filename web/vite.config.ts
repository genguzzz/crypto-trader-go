import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import Markdown from 'unplugin-vue-markdown/vite'
import Shiki from '@shikijs/markdown-it'

// Help articles (src/docs/**/*.md) compile to Vue components at build time; code blocks are
// highlighted by Shiki then, so the browser gets plain HTML.
export default defineConfig({
  plugins: [
    vue({ include: [/\.vue$/, /\.md$/] }),
    Markdown({
      wrapperClasses: 'md',
      markdownItOptions: { html: false, linkify: true, typographer: false },
      async markdownItSetup(md) {
        md.use(await Shiki({ theme: 'github-dark-dimmed', langs: ['python', 'json', 'bash', 'sh', 'yaml', 'text'], fallbackLanguage: 'text' }))
      },
    }),
  ],
  server: { proxy: { '/api': { target: process.env.CTG_API || 'http://127.0.0.1:8686', changeOrigin: true } } }, // CTG_API=https://… proxies to a remote instance
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 800 },
})
