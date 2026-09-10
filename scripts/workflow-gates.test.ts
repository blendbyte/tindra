import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'

function workflow(name: string): any {
  return Bun.YAML.parse(readFileSync(new URL(`../.github/workflows/${name}.yml`, import.meta.url), 'utf8'))
}

const verify = workflow('verify')

for (const name of ['ci', 'release']) {
  describe(`${name} publication gates`, () => {
    const caller = workflow(name)

    test('verifies the caller revision with the shared workflow', () => {
      expect(caller.jobs.verify.uses).toBe('./.github/workflows/verify.yml')
      expect(caller.jobs.verify.if).toBeUndefined()
      expect(caller.jobs.verify['continue-on-error']).toBeUndefined()
      // A checkout override could publish a different revision than was tested.
      for (const job of Object.values(caller.jobs) as any[]) {
        for (const step of job.steps ?? []) {
          if (step.uses?.startsWith('actions/checkout@')) {
            expect(step.with?.ref).toBeUndefined()
            expect(step.with?.repository).toBeUndefined()
          }
        }
      }
    })

    test('every publishing job requires successful verification', () => {
      const publishers = (Object.values(caller.jobs) as any[]).filter(job =>
        job.steps?.some((step: any) =>
          step.uses?.startsWith('softprops/action-gh-release@') ||
          (step.uses?.startsWith('docker/build-push-action@') && step.with?.push === true),
        ),
      )
      expect(publishers.length).toBeGreaterThan(0)
      for (const job of publishers) {
        const needs = [job.needs].flat()
        // Release publication can inherit the gate through its build jobs.
        const gated = needs.includes('verify') || (needs.length > 0 && needs.every((id: string) =>
          [caller.jobs[id]?.needs].flat().includes('verify'),
        ))
        expect(gated).toBe(true)
        // These status functions can bypass GitHub's implicit success() guard.
        expect(job.if ?? '').not.toMatch(/\b(always|failure|cancelled)\s*\(/)
        expect(job['continue-on-error']).toBeUndefined()
      }
    })
  })
}

test('all verification checks are mandatory for every revision', () => {
  expect(verify.on.workflow_call).toBeDefined()
  expect(Object.keys(verify.jobs).sort()).toEqual([
    'action-pins', 'frontend', 'frontend-test', 'go-lint', 'go-test',
  ])
  for (const job of Object.values(verify.jobs) as any[]) {
    expect(job.if).toBeUndefined()
    expect(job['continue-on-error']).toBeUndefined()
    for (const step of job.steps) {
      expect(step['continue-on-error']).toBeUndefined()
      if (!step.uses?.startsWith('actions/upload-artifact@')) {
        expect(step.if).toBeUndefined()
      }
      if (step.uses?.startsWith('actions/checkout@')) {
        expect(step.with?.ref).toBeUndefined()
        expect(step.with?.repository).toBeUndefined()
      }
    }
  }
  const browser = verify.jobs.frontend
  expect(browser.services.postgres).toBeDefined()
  expect(browser.env.BIND_ADDR).toBe('127.0.0.1:18080')
  const commands = browser.steps.map((step: any) => step.run ?? '')
  expect(commands).toContain('bun run build')
  expect(commands).toContain('go build -o bin/tindra ./cmd/tindra')
  expect(commands).toContain('bunx playwright install --with-deps chromium')
  expect(commands).toContain('bun run test:e2e')
})

test('Go and frontend unit suites cannot be replaced by build-only checks', () => {
  expect(verify.jobs['go-test'].steps.map((step: any) => step.run)).toContain(
    'go test -race -p 8 -coverprofile=coverage.out -covermode=atomic ./...',
  )
  expect(verify.jobs['go-test'].services.postgres).toBeDefined()
  expect(verify.jobs['go-lint'].steps.some((step: any) =>
    step.uses?.startsWith('golangci/golangci-lint-action@'),
  )).toBe(true)
  expect(verify.jobs['frontend-test'].steps.map((step: any) => step.run)).toContain('bun run test:coverage')
})

test('release binaries consume the frontend artifact from mandatory verification', () => {
  const release = workflow('release')
  const producer = verify.jobs.frontend.steps.find((step: any) =>
    step.uses?.startsWith('actions/upload-artifact@') && step.with?.name === 'web-dist',
  )
  const consumer = release.jobs.binaries.steps.find((step: any) =>
    step.uses?.startsWith('actions/download-artifact@'),
  )
  expect(producer).toBeDefined()
  expect(consumer).toBeDefined()
  expect(producer.if).toBeUndefined()
  expect(producer.with['if-no-files-found']).toBe('error')
  expect(consumer.with.name).toBe(producer.with.name)
  expect(consumer.with.path).toBe(producer.with.path)
  expect(release.jobs.binaries.needs).toBe('verify')
  expect(release.jobs.binaries.if).toBeUndefined()
  expect(release.jobs.binaries['continue-on-error']).toBeUndefined()
})

test('verification has read-only permissions and dev publishing only runs on main pushes', () => {
  const ci = workflow('ci')
  const release = workflow('release')
  for (const document of [verify, ci, release]) {
    expect(document.permissions).toEqual({ contents: 'read' })
  }
  expect(ci.jobs['docker-dev'].if).toBe("github.event_name == 'push' && github.ref == 'refs/heads/main'")
  expect(ci.jobs['docker-dev'].permissions).toEqual({ contents: 'read', packages: 'write' })
  expect(release.jobs.docker.permissions).toEqual({ contents: 'read', packages: 'write' })
  expect(release.jobs.release.permissions).toEqual({ contents: 'write' })
  for (const job of Object.values(verify.jobs) as any[]) {
    expect(job.permissions ?? verify.permissions).toEqual({ contents: 'read' })
  }
})
