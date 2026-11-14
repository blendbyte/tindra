import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

// Vitest runs from web/; happy-dom gives import.meta.url a non-file scheme.
const srcDir = join(process.cwd(), 'src')
const css = readFileSync(join(srcDir, 'assets/styles.css'), 'utf8')

function rule(selector: string): string {
  const start = css.indexOf(`\n${selector} {`)
  expect(start, `missing rule ${selector}`).toBeGreaterThan(-1)
  return css.slice(start, css.indexOf('}', start))
}

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap(name => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return name === '__tests__' ? [] : sourceFiles(path)
    return /\.(vue|css)$/.test(name) ? [path] : []
  })
}

// Native checkboxes ignore the theme and render as white boxes on the dark
// surfaces, so every checkbox is drawn from the design tokens instead.
describe('checkbox styling', () => {
  it('draws checkboxes from tokens instead of the native control', () => {
    const base = rule('input[type="checkbox"]')
    expect(base).toContain('appearance: none')
    expect(base).toContain('border: 1px solid var(--text-3)')

    const on = rule('input[type="checkbox"]:checked,\ninput[type="checkbox"]:indeterminate')
    expect(on).toContain('background: var(--accent)')
    expect(rule('input[type="checkbox"]::before')).toContain('background: var(--on-accent)')
    expect(rule('input[type="checkbox"]:indeterminate')).toContain('--check-mark')
  })

  it('does not fall back to accent-color tinting of the native checkbox', () => {
    const offenders = sourceFiles(srcDir).filter(file => readFileSync(file, 'utf8').includes('accent-color'))
    expect(offenders).toEqual([])
  })

  it('keeps issue row checkboxes hidden until hover or selection', () => {
    expect(rule('input.row-check')).toContain('opacity: 0')
    expect(css).toContain('.row-check.row-check--header { opacity: 1; }')
  })

  it('shows issue row checkboxes on touch screens and in select mode', () => {
    expect(css).toMatch(/@media \(hover: none\) \{\s*input\.row-check \{ opacity: 1; \}/)
    expect(css).toContain('.issuelist--selecting .row-check { opacity: 1; }')
  })

  it('brings the checkbox column back on phones in select mode', () => {
    const phone = css.slice(css.indexOf('@media (max-width: 768px)'))
    expect(phone).toContain('.select-toggle { display: inline-flex; }')
    expect(phone).toMatch(/\.issuelist--selecting \.issuerow \{\s*grid-template-columns: 16px 14px minmax\(0, 1fr\) 80px 16px;/)
    expect(phone).toContain('.issuelist--selecting .issuerow > :nth-child(1) { display: inline-grid; }')
  })

  it('docks the bulk action bar to the bottom edge on phones', () => {
    const phone = css.slice(css.indexOf('/* Phones: the bar docks'))
    const block = phone.slice(0, phone.indexOf('/* Narrow phones'))
    expect(block).toMatch(/\.bulkbar \{[^}]*left: 0;[^}]*right: 0;[^}]*bottom: 0;[^}]*flex-wrap: nowrap;/)
    expect(block).toContain('env(safe-area-inset-bottom)')
    expect(block).toContain('.bulkbar .bulkbar__wide { display: none; }')
    expect(block).toContain('.bulkbar__more { display: inline-flex; }')
    expect(rule('.bulkbar__more')).toContain('display: none')
  })

  it('keeps the old tablet wrap rules out of the 1199px block', () => {
    const tablet = css.slice(css.indexOf('@media (max-width: 1199px)'))
    expect(tablet.slice(0, tablet.indexOf('\n}\n'))).not.toContain('.bulkbar')
  })
})
