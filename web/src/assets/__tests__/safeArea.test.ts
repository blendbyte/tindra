import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

// Vitest runs from web/; happy-dom gives import.meta.url a non-file scheme.
const css = readFileSync(join(process.cwd(), 'src/assets/styles.css'), 'utf8')
const html = readFileSync(join(process.cwd(), 'index.html'), 'utf8')

function rule(selector: string): string {
  const start = css.indexOf(`\n${selector} {`)
  expect(start, `missing rule ${selector}`).toBeGreaterThan(-1)
  return css.slice(start, css.indexOf('}', start))
}

// On iPhones the bottom edge belongs to the home indicator: taps there go to
// iOS, most of all in the installed app, which has no Safari toolbar below it.
describe('safe areas', () => {
  it('asks for the safe area insets, which are all 0 without viewport-fit=cover', () => {
    expect(html).toMatch(/<meta name="viewport" content="[^"]*viewport-fit=cover[^"]*"/)
  })

  it('keeps bottom bars and panels above the home indicator', () => {
    expect(rule('.bulkbar')).toContain('bottom: calc(24px + env(safe-area-inset-bottom))')
    expect(rule('.toast-stack')).toContain('bottom: calc(24px + env(safe-area-inset-bottom))')
    expect(rule('.nav__mobile-drawer')).toContain('calc(4px + env(safe-area-inset-bottom))')
    expect(rule('.samples-panel')).toContain('padding-bottom: env(safe-area-inset-bottom)')
  })

  it('keeps content beside a landscape notch', () => {
    const body = rule('body')
    expect(body).toContain('padding-left: env(safe-area-inset-left)')
    expect(body).toContain('padding-right: env(safe-area-inset-right)')
    expect(rule('.toast-stack')).toContain('right: calc(24px + env(safe-area-inset-right))')
  })
})
