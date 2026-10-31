import { describe, expect, it } from 'vitest'

// A link to a route name that does not exist throws while rendering, and
// RouterLink stubs in view tests never notice. Every name a component links
// or navigates to must be declared in the router.
const sources = import.meta.glob('/src/**/*.{vue,ts}', { query: '?raw', import: 'default', eager: true }) as Record<string, string>

describe('named routes', () => {
  it('only links to names the router declares', () => {
    const declared = new Set([...sources['/src/router/index.ts'].matchAll(/\bname: '([^']+)'/g)].map(m => m[1]))
    const missing: string[] = []
    for (const [file, source] of Object.entries(sources)) {
      if (file.includes('__tests__') || file.endsWith('/router/index.ts')) continue
      for (const m of source.matchAll(/(?::to="|push\(|replace\(|resolve\()\{\s*name:\s*'([^']+)'/g)) {
        if (!declared.has(m[1])) missing.push(`${file}: ${m[1]}`)
      }
    }
    expect(missing).toEqual([])
  })
})
