import { afterEach, describe, expect, test } from 'bun:test'
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const makefile = new URL('../Makefile', import.meta.url).pathname
const dirs: string[] = []
afterEach(() => { for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true }) })

// make runs in a fresh process each time; on a busy CI runner that can take
// longer than bun's 5 s default, which failed release runs before.
const MAKE_TIMEOUT = 30_000

// Runs make against a copy of the Makefile in an empty directory. A stub
// docker first on PATH records whether make called it.
function make(args: string[], migrations: string[] = []) {
  const dir = mkdtempSync(join(tmpdir(), 'tindra-make-'))
  dirs.push(dir)
  copyFileSync(makefile, join(dir, 'Makefile'))
  mkdirSync(join(dir, 'migrations'))
  for (const name of migrations) writeFileSync(join(dir, 'migrations', name), '')
  const bin = join(dir, 'stub-bin')
  mkdirSync(bin)
  const marker = join(dir, 'docker-called')
  writeFileSync(join(bin, 'docker'), `#!/bin/sh\ntouch '${marker}'\nexit 1\n`)
  chmodSync(join(bin, 'docker'), 0o755)
  const result = Bun.spawnSync(['make', ...args], { cwd: dir, env: { ...process.env, PATH: `${bin}:${process.env.PATH}` } })
  return {
    exitCode: result.exitCode,
    output: result.stdout.toString() + result.stderr.toString(),
    files: () => readdirSync(join(dir, 'migrations')).sort(),
    dockerCalled: existsSync(marker),
  }
}

describe('Makefile', () => {
  test('migrate-create adds only an up migration after the highest number', () => {
    const r = make(['migrate-create', 'name=add_things'], ['0007_a.up.sql', '0009_b.up.sql'])
    expect(r.exitCode).toBe(0)
    expect(r.files()).toEqual(['0007_a.up.sql', '0009_b.up.sql', '0010_add_things.up.sql'])
  }, MAKE_TIMEOUT)

  test('migrate-create starts at 0001 and requires a name', () => {
    expect(make(['migrate-create', 'name=init']).files()).toEqual(['0001_init.up.sql'])
    expect(make(['migrate-create']).exitCode).not.toBe(0)
  }, MAKE_TIMEOUT)

  test('a mistyped target fails instead of silently succeeding', () => {
    const r = make(['tset'])
    expect(r.exitCode).not.toBe(0)
    expect(r.output).toContain('No rule to make target')
  }, MAKE_TIMEOUT)

  test('targets that do not need Docker never call it', () => {
    for (const args of [['migrate-create', 'name=x'], ['tset'], ['-n', 'build'], ['-n', 'lint']]) {
      expect({ args, dockerCalled: make(args).dockerCalled }).toEqual({ args, dockerCalled: false })
    }
    expect(make(['-n', 'db']).dockerCalled).toBe(true)
  }, MAKE_TIMEOUT)

  test('build bundles the frontend first and test keeps the container reaper', () => {
    const build = make(['-n', 'build']).output
    expect(build.indexOf('bun run build')).toBeGreaterThan(-1)
    expect(build.indexOf('bun run build')).toBeLessThan(build.indexOf('go build'))
    expect(make(['-n', 'test']).output).not.toContain('TESTCONTAINERS_RYUK_DISABLED')
  }, MAKE_TIMEOUT)
})
