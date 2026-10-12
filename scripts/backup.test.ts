import { afterEach, expect, test } from 'bun:test'
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// backup.sh against a fake docker: enough to check where the archive goes and
// what the manifest says. The restore steps were verified against real
// containers when they were written.
const script = new URL('../backup.sh', import.meta.url).pathname
const dirs: string[] = []
afterEach(() => { for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true }) })

test('a relative output directory is relative to where the script runs', () => {
  const root = mkdtempSync(join(tmpdir(), 'tindra-backup-'))
  dirs.push(root)
  const bin = join(root, 'bin')
  mkdirSync(bin)
  mkdirSync(join(root, 'stack'))
  mkdirSync(join(root, 'backups'))
  writeFileSync(join(root, 'stack', 'docker-compose.yml'), 'services: {}\n')
  writeFileSync(join(bin, 'docker'), `#!/usr/bin/env bash
case "$*" in
  "compose version"*) echo v2 ;;
  *pg_dump*) printf 'dump' ;;
  "compose ps -q tindra") echo cid ;;
  "cp cid:/data "*) mkdir -p "$3" && echo map > "$3/file.map" ;;
esac
exit 0
`)
  chmodSync(join(bin, 'docker'), 0o755)

  const result = Bun.spawnSync(['bash', script, 'stack', 'backups'], {
    cwd: root,
    env: { ...process.env, PATH: `${bin}:${process.env.PATH}` },
  })
  expect(result.exitCode).toBe(0)
  const archives = readdirSync(join(root, 'backups'))
  expect(archives).toHaveLength(1)
  expect(existsSync(join(root, 'stack', 'backups'))).toBe(false)

  const out = join(root, 'extract')
  mkdirSync(out)
  Bun.spawnSync(['tar', '-xzf', join(root, 'backups', archives[0]), '-C', out])
  const folder = readdirSync(out)[0]
  const text = readFileSync(join(out, folder, 'manifest.txt'), 'utf8')
  expect(text).toContain(`B=/path/to/${folder}`)
  expect(text).toContain('dropdb -U tindra --force --if-exists tindra')
  expect(text).toContain('pg_restore -U tindra -d tindra --no-owner < "$B/postgres.dump"')
  expect(text).toContain("chown -R 65532:65532 /data")
})

test('a missing output directory fails before anything is dumped', () => {
  const root = mkdtempSync(join(tmpdir(), 'tindra-backup-'))
  dirs.push(root)
  const result = Bun.spawnSync(['bash', script, '.', 'does-not-exist'], { cwd: root })
  expect(result.exitCode).not.toBe(0)
  expect(result.stderr.toString()).toContain('Output directory does not exist')
})
