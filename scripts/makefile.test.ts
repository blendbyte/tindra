import { afterEach, describe, expect, test } from 'bun:test'
import { copyFileSync, mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const makefile = new URL('../Makefile', import.meta.url).pathname
const dirs: string[] = []
afterEach(() => { for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true }) })

// Runs make against a copy of the Makefile in an empty directory.
function make(args: string[], migrations: string[] = []) {
  const dir = mkdtempSync(join(tmpdir(), 'tindra-make-'))
  dirs.push(dir)
  copyFileSync(makefile, join(dir, 'Makefile'))
  mkdirSync(join(dir, 'migrations'))
  for (const name of migrations) writeFileSync(join(dir, 'migrations', name), '')
  const result = Bun.spawnSync(['make', ...args], { cwd: dir })
  return { exitCode: result.exitCode, output: result.stdout.toString() + result.stderr.toString(), files: () => readdirSync(join(dir, 'migrations')).sort() }
}

describe('Makefile', () => {
  test('migrate-create adds only an up migration after the highest number', () => {
    const r = make(['migrate-create', 'name=add_things'], ['0007_a.up.sql', '0009_b.up.sql'])
    expect(r.exitCode).toBe(0)
    expect(r.files()).toEqual(['0007_a.up.sql', '0009_b.up.sql', '0010_add_things.up.sql'])
  })

  test('migrate-create starts at 0001 and requires a name', () => {
    expect(make(['migrate-create', 'name=init']).files()).toEqual(['0001_init.up.sql'])
    expect(make(['migrate-create']).exitCode).not.toBe(0)
  })

  test('a mistyped target fails instead of silently succeeding', () => {
    const r = make(['tset'])
    expect(r.exitCode).not.toBe(0)
    expect(r.output).toContain('No rule to make target')
  })

  test('build bundles the frontend first and test keeps the container reaper', () => {
    const build = make(['-n', 'build']).output
    expect(build.indexOf('bun run build')).toBeGreaterThan(-1)
    expect(build.indexOf('bun run build')).toBeLessThan(build.indexOf('go build'))
    expect(make(['-n', 'test']).output).not.toContain('TESTCONTAINERS_RYUK_DISABLED')
  })
})
