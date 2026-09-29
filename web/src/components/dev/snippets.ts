/** Connection snippets for the open API and the MCP server; `key` falls back to a placeholder. */
export const KEY_PLACEHOLDER = '<YOUR_KEY>'
export const origin = () => location.origin
export const restBase = () => `${origin()}/api/open/v1`
export const mcpUrl = () => `${origin()}/mcp`

export const curlSnippet = (key = KEY_PLACEHOLDER, path = 'universe?limit=20') =>
  `curl -H "Authorization: Bearer ${key}" \\\n  "${restBase()}/${path}"`

export const claudeCodeSnippet = (key = KEY_PLACEHOLDER) =>
  `claude mcp add --transport http okx-helper ${mcpUrl()} \\\n  --header "Authorization: Bearer ${key}"`

export const mcpJsonSnippet = (key = KEY_PLACEHOLDER) =>
  JSON.stringify({ mcpServers: { 'okx-helper': { type: 'http', url: mcpUrl(), headers: { Authorization: `Bearer ${key}` } } } }, null, 2)

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    const ta = document.createElement('textarea') // non-secure contexts (plain http on a LAN address)
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  }
}

export const ttlLabel = (h: number) => (h === 0 ? '永不过期' : h < 24 ? `${h} 小时` : `${h / 24} 天`)
