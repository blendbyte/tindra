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
    'action-pins', 'frontend', 'frontend-test', 'go-lint', 'go-test', 'go-vuln',
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
  expect(verify.jobs['frontend-test'].steps.map((step: any) => step.run)).toContain('bun run lint')
  expect(verify.jobs['action-pins'].steps.map((step: any) => step.run)).toContain('bun test scripts/install.test.ts scripts/backup.test.ts scripts/makefile.test.ts scripts/style.test.ts')
})

test('vulnerability scan uses the toolchain that ships in releases', () => {
  const steps = verify.jobs['go-vuln'].steps
  const setupGo = steps.find((step: any) => step.uses?.startsWith('actions/setup-go@'))
  expect(setupGo.with['go-version-file']).toBe('go.mod')
  expect(steps.some((step: any) =>
    /^go run golang\.org\/x\/vuln\/cmd\/govulncheck@v\d+\.\d+\.\d+ \.\/\.\.\.$/.test(step.run ?? ''),
  )).toBe(true)
})

test('Docker builder uses the same Go version as go.mod', () => {
  const goVersion = readFileSync(new URL('../go.mod', import.meta.url), 'utf8').match(/^go (\S+)$/m)?.[1]
  const dockerfile = readFileSync(new URL('../Dockerfile', import.meta.url), 'utf8')
  expect(goVersion).toBeDefined()
  expect(dockerfile).toContain(`golang:${goVersion}-alpine`)
})

test('Docker base images are pinned and Bun matches the frontend toolchain', () => {
  const dockerfile = readFileSync(new URL('../Dockerfile', import.meta.url), 'utf8')
  const froms = [...dockerfile.matchAll(/^FROM\s+(?:--platform=\S+\s+)?(\S+)/gm)].map(m => m[1])
  expect(froms.length).toBe(3)
  for (const image of froms) expect(image).toMatch(/:[^@]+@sha256:[0-9a-f]{64}$/)

  const pkg = JSON.parse(readFileSync(new URL('../web/package.json', import.meta.url), 'utf8'))
  const bunVersion = pkg.packageManager?.match(/^bun@(\d+\.\d+\.\d+)$/)?.[1]
  expect(bunVersion).toBeDefined()
  expect(froms[0]).toStartWith(`oven/bun:${bunVersion}`)
  for (const job of Object.values(verify.jobs) as any[]) {
    for (const step of job.steps.filter((s: any) => s.uses?.startsWith('oven-sh/setup-bun@'))) {
      expect(step.with?.['bun-version-file']).toBe('web/package.json')
    }
  }

  // vue-tsc cannot check .vue files under Bun's runtime, so the image only
  // bundles; publishing jobs already require the type-checked verify run.
  expect(dockerfile).toContain('RUN bunx vite build')
  expect(dockerfile).not.toMatch(/^RUN .*(vue-tsc|bun run build)/m)
  expect(dockerfile).toContain('HEALTHCHECK')
  expect(dockerfile).toContain('CMD ["/tindra", "healthcheck"]')
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
  // id-token and attestations sign the build provenance for the release files.
  expect(release.jobs.release.permissions).toEqual({ contents: 'write', 'id-token': 'write', attestations: 'write' })
  for (const job of Object.values(verify.jobs) as any[]) {
    expect(job.permissions ?? verify.permissions).toEqual({ contents: 'read' })
  }
})

test('every job has a timeout and runs are grouped', () => {
  const ci = workflow('ci')
  const release = workflow('release')
  for (const document of [verify, ci, release]) {
    for (const [name, job] of Object.entries(document.jobs) as [string, any][]) {
      // Jobs that call a reusable workflow inherit its job timeouts.
      if (job.uses) continue
      expect(job['timeout-minutes'], name).toBeGreaterThan(0)
    }
  }
  expect(ci.concurrency.group).toBe('ci-${{ github.ref }}')
  expect(ci.concurrency['cancel-in-progress']).toBe("${{ github.event_name == 'pull_request' }}")
  expect(release.concurrency['cancel-in-progress']).toBe(false)
})

test('lint and release tooling are pinned and releases are verifiable', () => {
  const lint = verify.jobs['go-lint'].steps.find((step: any) => step.uses?.startsWith('golangci/golangci-lint-action@'))
  expect(lint.with.version).toMatch(/^v\d+\.\d+\.\d+$/)

  const release = workflow('release')
  const steps = release.jobs.release.steps
  const index = (prefix: string) => steps.findIndex((step: any) => (step.uses ?? step.run ?? '').startsWith(prefix))
  // Checksums are taken before anything else is added to the directory.
  expect(index('sha256sum tindra-* > SHA256SUMS')).toBeGreaterThan(-1)
  expect(index('sha256sum')).toBeLessThan(index('anchore/sbom-action@'))
  expect(steps[index('actions/attest-build-provenance@')].with['subject-checksums']).toBe('SHA256SUMS')
  const files = steps[index('softprops/action-gh-release@')].with.files
  for (const name of ['tindra-*', 'SHA256SUMS', 'sbom.spdx.json']) expect(files).toContain(name)

  const image = release.jobs.docker.steps.find((step: any) => step.uses?.startsWith('docker/build-push-action@'))
  expect(image.with.sbom).toBe(true)
  expect(image.with.provenance).toBe('mode=max')
})
