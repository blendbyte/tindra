import { afterEach, describe, expect, test } from 'bun:test'
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// install.sh insists on a terminal, so it runs under a pseudo-terminal (via
// Python's pty module) with a fake docker that records how it was called.
const installer = new URL('../install.sh', import.meta.url).pathname
const dirs: string[] = []
afterEach(() => { for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true }) })

const driver = `
import os, pty, sys, time
pid, fd = pty.fork()
if pid == 0:
    os.execvp('bash', ['bash', sys.argv[1]])
out = b''
for line in sys.argv[2].encode().splitlines(keepends=True):
    time.sleep(0.2)
    os.write(fd, line)
while True:
    try:
        chunk = os.read(fd, 4096)
    except OSError:
        break
    if not chunk:
        break
    out += chunk
_, status = os.waitpid(pid, 0)
sys.stdout.write(out.decode(errors='replace'))
sys.exit(os.waitstatus_to_exitcode(status))
`

function run(answers: (dir: string) => string[]) {
  const dir = mkdtempSync(join(tmpdir(), 'tindra-install-'))
  dirs.push(dir)
  const bin = join(dir, 'bin')
  const log = join(dir, 'docker.log')
  mkdirSync(bin)
  writeFileSync(join(bin, 'docker'), `#!/usr/bin/env bash
printf 'ARGS %s\\n' "$*" >> "${log}"
case "$*" in
  *"users create"*) printf 'STDIN %s\\n' "$(cat)" >> "${log}" ;;
  "compose version"*) echo v2.30.0 ;;
esac
exit 0
`)
  chmodSync(join(bin, 'docker'), 0o755)
  const input = answers(dir).map(a => a + '\n').join('')
  const result = Bun.spawnSync(['python3', '-c', driver, installer, input], {
    env: { ...process.env, PATH: `${bin}:${process.env.PATH}` },
    cwd: dir,
  })
  const composePath = join(dir, 'docker-compose.yml')
  return {
    exitCode: result.exitCode,
    output: result.stdout.toString(),
    compose: () => readFileSync(composePath, 'utf8'),
    composeMode: () => statSync(composePath).mode & 0o777,
    log: () => readFileSync(log, 'utf8'),
  }
}

const password = 'correct-horse-battery'
const account = ['admin@example.com', 'Admin', password, password]

describe('install.sh', () => {
  test('behind a local proxy it binds to loopback and keeps secrets private', () => {
    const r = run(dir => [dir, 'https://tindra.example.com', '8080', 'y', ...account, 'y'])
    expect(r.exitCode).toBe(0)
    expect(r.compose()).toContain('- "127.0.0.1:8080:8080"')
    expect(r.compose()).toContain('TRUSTED_PROXIES: "127.0.0.1,::1,172.16.0.0/12,192.168.0.0/16,10.0.0.0/8"')
    // The compose file holds the database password.
    expect(r.composeMode()).toBe(0o600)
    // The account password goes in on stdin, never as an argument.
    expect(r.log()).toContain('users create --email admin@example.com --name Admin --password-stdin')
    expect(r.log()).toContain(`STDIN ${password}`)
    expect(r.log()).not.toContain(`ARGS ${password}`)
    expect(r.log().split('\n').filter(l => l.startsWith('ARGS')).join('\n')).not.toContain(password)
  }, 30_000)

  test('without a local proxy it warns about the exposed port and records the proxy IP', () => {
    const r = run(dir => [dir, 'http://tindra.internal', '9000', 'n', '10.0.0.5', ...account, 'n'])
    expect(r.exitCode).toBe(0)
    expect(r.compose()).toContain('- "9000:8080"')
    expect(r.compose()).toContain('TRUSTED_PROXIES: "10.0.0.5"')
    expect(r.output).toContain('Docker bypasses ufw/firewalld rules')
    // Not started: the hint must be a runnable command with every placeholder filled.
    expect(r.output).toMatch(/docker compose -f \S+docker-compose\.yml up -d/)
    expect(r.output).toMatch(/docker compose -f \S+docker-compose\.yml exec tindra \/tindra users create --email 'admin@example\.com' --name 'Admin'/)
    expect(r.output).not.toContain('%s')
  }, 30_000)
})
