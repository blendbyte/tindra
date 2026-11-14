import { test, expect, type Locator } from '@playwright/test'

const projectName = 'The Great Football Pool'
const longName = 'production-'.repeat(30)
const timestamp = '2026-09-13T12:00:00Z'
const projects = [projectName, longName].map((name, i) => ({
  id: `p${i + 1}`, name, slug: `project-${i}`, public_key: 'public', created_at: timestamp,
  event_count: 1234567, events_24h: 123456, transaction_count: 1,
}))
const summaries = projects.map(p => ({
  project_id: p.id, transaction: '/v3/standings/' + 'very-long-path/'.repeat(20), op: 'http.server',
  sample_count: 123456789, tpm: 123456.78, p50: 56789, p95: 987654,
  apdex: 0.98, failure_rate: 0.1234, time_spent_ms: 987654321,
}))
const transactions = summaries.map((s, i) => ({
  ...s, id: `t${i}`, trace_id: 'a'.repeat(32), start_timestamp: timestamp,
  duration_ms: s.p95, status: i ? 'deadline_exceeded' : 'ok',
  measurements: { lcp: { value: 2500 }, inp: { value: 200 }, cls: { value: 0.12 } },
}))
const issue = { id: 'i1', project_id: 'p2', title: 'RuntimeError: ' + longName,
  level: 'error', status: 'open', environment: longName, event_count: 123456789,
  user_count: 23, first_seen: timestamp, last_seen: timestamp, sparkline: [1, 2, 3] }
const monitor = { id: 'm1', project_id: 'p2', name: longName, schedule: '0 * * * *',
  state: 'ok', status: 'active', grace_period_secs: 300, last_checkin_at: timestamp,
  next_expected_at: timestamp, recent_checkins: [], url: 'https://example.com/' + longName,
  method: 'GET', interval_secs: 300, timeout_secs: 10, expected_codes: '200-299',
  consecutive_failures: 0, last_checked_at: timestamp, next_check_at: timestamp, recent_checks: [] }

test.beforeEach(async ({ page }) => {
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (!path.startsWith('/api/')) return route.fallback()
    let body: unknown = []
    if (path === '/api/me') body = { id: 'u1', name: 'Alex', email: 'alex@example.com', mfa_enabled: true,
      timezone: 'UTC', permissions: { manage_projects: true, manage_users: true, manage_alerts: true, manage_issues: true } }
    else if (path === '/api/config') body = { require_mfa: false }
    else if (path === '/api/tokens') body = [{ id: 'token1', name: longName, project_id: 'p2', writable: true, created_at: timestamp }]
    else if (path === '/api/users') body = [{ id: 'user2', name: longName, email: longName + '@example.com', created_at: timestamp, permissions: {}, mfa_enabled: true }]
    else if (path === '/api/invites') body = [{ id: 'invite1', name: longName, email: longName + '@example.com', expires_at: timestamp }]
    else if (path === '/api/settings') body = { event_limit: 0 }
    else if (path === '/api/projects' || path === '/api/projects/metadata') body = projects
    else if (path === '/api/setup-status') body = projects.map(p => ({ id: p.id, setup_complete: true }))
    else if (path === '/api/transactions/summaries') body = url.searchParams.has('offset')
      ? summaries.map(s => ({ ...s, p50: 1, p95: 1 })) : summaries
    else if (path === '/api/transactions') body = { transactions, has_more: false }
    else if (path.includes('timeseries') || path === '/api/transactions/counts') body = { buckets: [], bucket_size: 'hour' }
    else if (path === '/api/issues' || path === '/api/issues/overview') body = { issues: [issue], total: 1, has_more: false }
    else if (path === '/api/logs') body = { logs: [{ id: 'l1', project_id: 'p2', timestamp,
      body: longName, level: 'warning', environment: longName, transaction_id: 't1' }], has_more: false }
    else if (path.startsWith('/api/spans/')) body = [{ description: longName, op: 'db.query',
      rate: 123456789, p50: 56789, p95: 987654, time_pct: 99.9, error_rate: 0.1, miss_rate: 0.1 }]
    else if (path === '/api/monitors' || path === '/api/uptime-monitors') body = [monitor]
    else if (path.startsWith('/api/releases')) body = { releases: [{ id: 'r1', version: longName,
      project_id: 'p2', deployed_at: timestamp, tx_count: 1234567, tx_p50: 123456,
      tx_error_rate: 12.3, new_issues: 1234567 }], total: 1, has_more: false }
    else if (path === '/api/vitals/pages') body = [{ transaction: longName, sessions: 123456789,
      lcp_p75: 987654, inp_p75: 12345, cls_p75: 0.12, pass_rate: 0.9 }]
    else if (path === '/api/vitals') body = Object.fromEntries(['lcp', 'inp', 'cls', 'fcp', 'ttfb']
      .map(key => [key, { p75: 100, count: 1, pass_rate: 0.9 }]))
    else if (path === '/api/environments') body = [longName]
    else if (path === '/api/alert-rules') body = { rules: [] }
    await route.fulfill({ json: body })
  })
})

// Check the actual painted text, allowing only deliberate clipping/ellipsis.
// Bounding boxes alone miss the original bug: the badge fits, its text spills.
async function expectContainedRows(rows: Locator) {
  const failures = await rows.evaluateAll(rows => rows.flatMap(row => {
    const cells = Array.from(row.children).filter(cell => cell.getBoundingClientRect().width > 0)
    return cells.flatMap((cell, i) => {
      const bounds = cell.getBoundingClientRect()
      const problems: string[] = []
      // Cells overlap only when they share a line: phone layouts stack a
      // row's facts on a second line.
      const prev = i ? cells[i - 1]!.getBoundingClientRect() : null
      if (prev && prev.bottom > bounds.top + 1 && prev.top < bounds.bottom - 1 && prev.right > bounds.left + 1) {
        problems.push(`Overlapping cells: ${cell.className}`)
      }
      const walker = document.createTreeWalker(cell, NodeFilter.SHOW_TEXT)
      while (walker.nextNode()) {
        const node = walker.currentNode
        if (!node.textContent?.trim()) continue
        let clipped = false
        for (let el = node.parentElement; el && cell.contains(el); el = el.parentElement) {
          const css = getComputedStyle(el)
          if (css.overflowX !== 'visible' || css.display === 'none') clipped = true
        }
        if (clipped) continue
        const range = document.createRange()
        range.selectNodeContents(node)
        for (const rect of range.getClientRects()) {
          if (rect.width && (rect.right > bounds.right + 1 || rect.left < bounds.left - 1)) {
            problems.push(`Text spills from ${cell.className}: ${node.textContent?.trim().slice(0, 40)}`)
          }
        }
      }
      return problems
    })
  }))
  expect(failures).toEqual([])
}

const lists = [
  { path: '/performance/transactions', row: 'a.txrow', header: '.txrow--header', count: 10 },
  { path: '/performance/transactions?user=alice', row: 'a.txrow', header: '.txrow--header', count: 6 },
  { path: '/performance/browser?user=alice', row: 'a.txrow', header: '.txrow--header', count: 6 },
  { path: '/transactions/profile?name=checkout', row: '.tx-sample-row:not(.tx-sample-row--head)', header: '.tx-sample-row--head', count: 5 },
  { path: '/releases', row: '.relrow:not(.relrow--header)', header: '.relrow--header', count: 6 },
  { path: '/issues', row: '.issuerow[role="row"]' },
  { path: '/logs', row: '.perf-table__row' },
  { path: '/performance/queries', row: '.perf-table__row' },
  { path: '/performance/caches', row: '.perf-table__row' },
  { path: '/performance/jobs', row: '.perf-table__row' },
  { path: '/performance/browser', row: '.perf-table__row' },
  { path: '/monitors/cron', row: '.monrow:not(.monrow--header)' },
  { path: '/monitors/uptime', row: '.monrow:not(.monrow--header)' },
  { path: '/settings/projects', row: '.proj-card__head--row' },
  { path: '/settings/tokens', row: '.token-table tbody tr' },
  { path: '/settings/users', row: '.token-table tbody tr' },
  { path: '/dashboard', row: '.db-proj-row, .db-tx-row' },
]

for (const list of lists) {
  test(`list columns contain long content: ${list.path}`, async ({ page }, testInfo) => {
    await page.goto(list.path)
    await expect(page.locator(list.row).first()).toBeVisible()
    await page.evaluate(() => document.fonts.ready)
    for (const width of [320, 640, 768, 900, 1023, 1024, 1440]) {
      await page.setViewportSize({ width, height: 900 })
      const rows = page.locator(list.row)
      await expectContainedRows(rows)
      // On phones transaction lists stack each row on two lines, with the
      // ranking figure beside the name, instead of a table to scroll sideways.
      const stacked = width <= 640 && list.path.startsWith('/performance/transactions')
      if (stacked) {
        await expect(rows.first().locator(':scope > :visible')).toHaveCount(4)
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      }
      if (list.header && !stacked) {
        await expect(page.locator(list.header).locator(':scope > :visible')).toHaveCount(list.count!)
        await expect(rows.first().locator(':scope > :visible')).toHaveCount(list.count!)
        const headerBounds = await page.locator(list.header).boundingBox()
        const rowBounds = await rows.first().boundingBox()
        expect(headerBounds!.y + headerBounds!.height).toBeLessThanOrEqual(rowBounds!.y + 1)
        const headerTracks = await page.locator(list.header).evaluate(el => getComputedStyle(el).gridTemplateColumns)
        expect(await rows.first().evaluate(el => getComputedStyle(el).gridTemplateColumns)).toBe(headerTracks)
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      if (list.path === '/performance/transactions' && width === 1440) {
        // The stat strip's error rate takes the same red as the table's,
        // instead of the plain figure colour that the value rule sets.
        const red = await rows.locator('.tx-failure').first().evaluate(el => getComputedStyle(el).color)
        expect(await page.locator('.txstat__value.tx-failure').evaluate(el => getComputedStyle(el).color)).toBe(red)
      }
      if (list.path.startsWith('/performance/transactions')) {
        const badge = rows.first().locator('.projtag')
        await expect(badge).toHaveAttribute('title', projectName)
        expect(await badge.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
        const projectWidths = await rows.locator('.projtag').evaluateAll(els => els.map(el => el.getBoundingClientRect().width))
        expect(Math.max(...projectWidths)).toBeLessThanOrEqual(240)
      }
      if (list.path === '/logs') expect(await page.locator('.log-msg__body').evaluate(el => el.clientWidth)).toBeGreaterThan(100)
      // On phones a log line puts its message on its own line under the time
      // and level, instead of a table cut off at the screen edge.
      if (list.path === '/logs' && width <= 640) {
        const [time, message] = await Promise.all([rows.first().locator('.log-time-col').boundingBox(), rows.first().locator('.log-msg-col').boundingBox()])
        expect(message!.y).toBeGreaterThanOrEqual(time!.y + time!.height - 1)
        expect(message!.width).toBeGreaterThan(width * 0.8)
      }
      if (width === 320 && list.header && !stacked) {
        const scroller = page.getByRole('region').filter({ has: rows.first() })
        await scroller.focus()
        await page.keyboard.press('ArrowRight')
        await expect.poll(() => scroller.evaluate(el => el.scrollLeft)).toBeGreaterThan(0)
        await scroller.evaluate(el => { el.scrollLeft = 0; (el as HTMLElement).blur() })
      }
      if (width === 320 || width === 1440) await page.screenshot({ path: testInfo.outputPath(`list-${width}.png`) })
    }
  })
}

// Phones hide the checkbox column, so the header's Select toggle brings it back
// and row taps select instead of opening the issue.
test('phones select issues from the list header toggle', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/issues')
  const rows = page.locator('.issuerow[role="row"]')
  const check = rows.first().locator('.row-check')
  await expect(rows.first()).toBeVisible()
  await expect(check).toBeHidden()

  const toggle = page.locator('.issuerow--header .select-toggle')
  await expect(toggle).toHaveText('Select')
  await toggle.click()
  await expect(toggle).toHaveText('Done')
  await expect(check).toBeVisible()
  await expect(page.locator('.row-check--header')).toBeVisible()
  await expectContainedRows(page.locator('.issuerow'))
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)

  await rows.first().click()
  await expect(page).toHaveURL(/\/issues(\?|$)/)
  await expect(check).toBeChecked()
  await expect(page.locator('.bulkbar')).toContainText('1 selected')

  // The bar docks to the bottom edge as one row, at every phone width.
  const bar = page.locator('.bulkbar')
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 })
    await expect.poll(async () => { const b = await bar.boundingBox(); return b && Math.round(b.y + b.height) }).toBe(844)
    const box = (await bar.boundingBox())!
    expect(box.x).toBe(0)
    expect(box.width).toBe(width)
    const centers = await bar.locator(':scope > :visible').evaluateAll(els => els.map(el => {
      const r = el.getBoundingClientRect()
      return r.top + r.height / 2
    }))
    expect(Math.max(...centers) - Math.min(...centers), `bar wraps at ${width}px`).toBeLessThanOrEqual(2)
    // Narrow phones show the count alone; the label stays for screen readers.
    const label = (await bar.locator('.bulkbar__count-label').boundingBox())!
    expect(label.width > 1).toBe(width > 374)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  }
  // The last row scrolls clear of the bar.
  await page.evaluate(() => scrollTo(0, document.documentElement.scrollHeight))
  const lastRow = (await rows.last().boundingBox())!
  expect(lastRow.y + lastRow.height).toBeLessThanOrEqual((await bar.boundingBox())!.y)
  await page.setViewportSize({ width: 390, height: 844 })

  await toggle.click()
  await expect(toggle).toHaveText('Select')
  await expect(page.locator('.bulkbar')).toHaveCount(0)
  await expect(check).toBeHidden()
})

// Phones keep Unignore and Merge in the overflow menu, never in the bar itself.
test('phones fold the rarer bulk actions into the more menu', async ({ page }) => {
  const twin = { ...issue, id: 'i2', status: 'ignored' }
  await page.route('**/api/issues?**', route => route.fulfill({ json: { issues: [issue, twin], total: 2, has_more: false } }))
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/issues?status=All')
  await page.locator('.issuerow--header .select-toggle').click()
  await page.locator('.row-check--header').click()

  const bar = page.locator('.bulkbar')
  await expect(bar.locator('.btn:visible')).toHaveText(['Resolve', 'Ignore', '', ''])
  await bar.getByRole('button', { name: 'More actions' }).click()
  await expect(bar.locator('.bulkbar__more-menu .popover__item')).toHaveText(['Unignore', 'Merge'])

  await page.setViewportSize({ width: 1024, height: 768 })
  await expect(bar.locator('.btn:visible')).toHaveText(['Resolve', 'Ignore', '', 'Unignore', 'Merge'])
})
