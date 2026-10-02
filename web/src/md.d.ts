declare module '*.md' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent
  export default component
  export const title: string | undefined // frontmatter keys are exported one by one
  export const summary: string | undefined
}
