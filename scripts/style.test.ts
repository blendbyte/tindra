import { expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'

// Project style: no em dashes in code, comments, docs or UI text. Applied
// migrations are exempt, because editing them would change shipped history.
test('tracked text files contain no em dashes', () => {
  const root = new URL('..', import.meta.url).pathname
  const files = Bun.spawnSync(['git', 'ls-files'], { cwd: root }).stdout.toString().split('\n').filter(Boolean)
  const offenders: string[] = []
  for (const file of files) {
    if (file.startsWith('migrations/') || /\.(png|jpe?g|gif|ico|woff2?|ttf|otf|lock|lockb)$|(^|\/)bun\.lock$/.test(file)) continue
    let text: string
    try {
      text = readFileSync(root + file, 'utf8')
    } catch {
      continue // deleted in the working tree
    }
    text.split('\n').forEach((line, i) => {
      if (line.includes('—')) offenders.push(`${file}:${i + 1}`)
    })
  }
  expect(offenders).toEqual([])
})
