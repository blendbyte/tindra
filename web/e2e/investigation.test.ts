import { test, expect } from '@playwright/test'

const projectId='11111111-1111-4111-8111-111111111111'
test('keeps investigation context through navigation, reload, and profile samples', async ({ page }, testInfo) => {
  const errors:string[]=[]
  const requests:URL[]=[]
  page.on('pageerror',error=>errors.push(error.message))
  await page.route('**/api/**',async route=>{
    const url=new URL(route.request().url())
    if (!url.pathname.startsWith('/api/')) { await route.continue(); return }
    requests.push(url)
    let data:unknown=[]
    if(url.pathname==='/api/me') data={id:'user-1',email:'test@example.com',name:'Test',mfa_enabled:true,timezone:'UTC',permissions:{manage_alerts:true}}
    else if(url.pathname==='/api/config') data={require_mfa:false}
    else if(url.pathname==='/api/settings') data={event_limit:0}
    else if(url.pathname==='/api/projects') data=[{id:projectId,name:'API',slug:'api',event_count:1,transaction_count:1}]
    else if(url.pathname==='/api/projects/metadata') data=[{id:projectId,name:'API',slug:'api'}]
    else if(url.pathname==='/api/app-users') data=[{identity:'alice',user_id:'alice',name:'Alice',project_id:projectId,last_seen:'2026-01-01T00:00:00Z',email:null,username:null}]
    else if(url.pathname==='/api/environments') data=['production','preview','eu-west']
    else if(url.pathname==='/api/logs') data={logs:[],has_more:false}
    else if(url.pathname==='/api/issues') data={issues:[],total:0,has_more:false}
    else if(url.pathname==='/api/transactions') data={transactions:[]}
    else if(url.pathname.includes('timeseries')) data={buckets:[],bucket_size:'hour'}
    else if(url.pathname==='/api/releases/metadata') data={releases:[{id:'release-1',version:'v1',project_id:projectId}]}
    else if(url.pathname==='/api/vitals') data=null
    await route.fulfill({json:data})
  })
  await page.goto(`/performance/transactions?project_id=${projectId}&env=eu-west&window=7d`)
  const bar=page.getByRole('region',{name:'Investigation filters and freshness'})
  await expect(bar).toContainText('eu-west');await expect(bar).toContainText('7d')
  await page.locator('.nav__links').getByRole('link',{name:'Logs',exact:true}).click()
  await expect(page).toHaveURL(/range=7d/)
  await expect(bar).toContainText('eu-west')
  await expect.poll(()=>requests.filter(u=>u.pathname==='/api/logs').length).toBeGreaterThan(0)
  const logs=requests.filter(u=>u.pathname==='/api/logs').at(-1)!
  expect(logs.searchParams.get('environment')).toBe('eu-west')
  expect(Date.parse(logs.searchParams.get('to')!)-Date.parse(logs.searchParams.get('from')!)).toBe(7*86400000)
  await page.goBack(); await expect(page).toHaveURL(/performance\/transactions/)
  await expect(bar).toContainText('7d')
  await page.goForward(); await expect(page).toHaveURL(/logs/)
  await page.reload();await expect(bar).toContainText('eu-west')
  for (const width of [320, 1280]) {
    await page.setViewportSize({ width, height: 844 })
    for (const name of [/Time range:/, /Environment:/, /^Filter by user$/]) {
      const trigger = bar.getByRole('button', { name })
      await trigger.click()
      const menu = bar.locator('.popover')
      await expect(menu).toBeVisible()
      const box = await menu.boundingBox()
      const button = await trigger.boundingBox()
      expect(box!.x).toBeGreaterThanOrEqual(8)
      expect(box!.x + box!.width).toBeLessThanOrEqual(width - 8)
      expect(Math.abs(box!.y - button!.y - button!.height - 6)).toBeLessThan(2)
      const option = menu.locator('.popover__item').first()
      const row = await option.boundingBox()
      expect(row!.width).toBeGreaterThan(box!.width - 4)
      expect(await option.evaluate(el => getComputedStyle(el).textAlign)).toBe('left')
      if (name.source.includes('Time')) expect(box!.width).toBe(160)
      await page.screenshot({ path: testInfo.outputPath(`dropdown-${width}-${name.source.replace(/[^a-z]/gi, '')}.png`) })
      await page.keyboard.press('Escape')
      await expect(menu).toHaveCount(0)
    }
  }
  await bar.getByRole('button', { name: /Time range:/ }).click()
  await bar.getByRole('button', { name: '1h', exact: true }).click()
  await expect(page).toHaveURL(/range=1h/)
  await page.locator('.nav__projects-trigger').click()
  await page.locator('.popover').getByRole('button', { name: 'All projects', exact: true }).click()
  await expect(page).toHaveURL(/project_id=all/)
  await expect.poll(() => requests.filter(u => u.pathname === '/api/logs').at(-1)?.searchParams.has('project_id')).toBe(false)
  await page.keyboard.press('Escape')
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(bar.getByRole('button', { name: /Time range:/ })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  for (const width of [320, 390]) {
    await page.setViewportSize({ width, height: 844 })
    await expect(bar.getByText(/Updated .* ago/)).not.toBeVisible()
    const bounds = await bar.boundingBox()
    expect(bounds!.height).toBeLessThanOrEqual(50)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await bar.getByLabel('Data freshness and scope', { exact: true }).click()
    await expect(bar.locator('.investigation-bar__popover')).toBeVisible()
    const popup = await bar.locator('.investigation-bar__popover').boundingBox()
    expect(popup!.x).toBeGreaterThanOrEqual(0)
    expect(popup!.x + popup!.width).toBeLessThanOrEqual(width)
    await page.keyboard.press('Escape')
    await expect(bar.locator('.investigation-bar__popover')).not.toBeVisible()
  }
  const refresh = bar.getByRole('button', { name: 'Refresh now', exact: true })
  const iconBox = await refresh.locator('svg').boundingBox()
  const ageBox = await refresh.locator('.investigation-bar__age').boundingBox()
  // Center the trimmed text box, without a manual pixel offset.
  expect(Math.abs((iconBox!.y + iconBox!.height / 2) - (ageBox!.y + ageBox!.height / 2))).toBeLessThanOrEqual(0.5)
  await refresh.screenshot({ path: testInfo.outputPath('refresh-alignment.png') })
  await page.screenshot({ path: testInfo.outputPath('toolbar-mobile.png') })
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto(`/transactions/profile?name=checkout&project_id=${projectId}&environment=eu-west&range=7d`)
  await expect.poll(()=>requests.filter(u=>u.pathname==='/api/transactions'&&u.searchParams.has('name')).length).toBeGreaterThan(0)
  const samples=requests.filter(u=>u.pathname==='/api/transactions'&&u.searchParams.has('name')).at(-1)!
  const chart=requests.filter(u=>u.pathname==='/api/transactions/timeseries'&&u.searchParams.has('name')).at(-1)!
  expect(samples.searchParams.get('from')).toBe(chart.searchParams.get('from'))
  expect(samples.searchParams.get('to')).toBe(chart.searchParams.get('to'))
  await bar.getByRole('button',{name:'Pause',exact:true}).click()
  await bar.getByLabel('Data freshness and scope', { exact: true }).click()
  await expect(bar.getByText('Auto-refresh paused', { exact: true })).toBeVisible()
  await page.goto('/performance/transactions?range=24h')
  await bar.getByRole('button', { name: /Time range:/ }).click()
  await bar.getByRole('button', { name: '90d', exact: true }).click()
  await expect(page).toHaveURL(/range=90d/)
  await page.locator('.nav__links').getByRole('link', { name: 'Dashboard', exact: true }).click()
  await expect(page).toHaveURL(/dashboard.*range=90d/)
  await expect(page.getByText('This view supports time ranges', { exact: false })).toHaveCount(0)
  await expect(page.locator('.db-kpis')).toBeVisible()
  await expect.poll(() => requests.filter(u => u.pathname === '/api/transactions/summaries').at(-1)?.searchParams.get('hours')).toBe('2160')
  const dashboard = requests.filter(u => u.pathname === '/api/transactions/summaries').at(-1)!
  expect(Date.parse(dashboard.searchParams.get('to')!) - Date.parse(dashboard.searchParams.get('from')!)).toBe(90 * 86400000)
  await bar.getByRole('button', { name: 'Filter by user', exact: true }).click()
  await bar.getByRole('button', { name: 'Alice', exact: true }).click()
  await expect(page).toHaveURL(/user=alice/)
  await expect.poll(() => requests.filter(u => u.pathname === '/api/transactions/summaries').at(-1)?.searchParams.get('user')).toBe('alice')
  await page.locator('.nav__links').getByRole('link', { name: 'Logs', exact: true }).click()
  await expect(page).toHaveURL(/user=alice/)
  await expect.poll(() => requests.filter(u => u.pathname === '/api/logs').at(-1)?.searchParams.get('user')).toBe('alice')
  expect(await page.getByRole('button', { name: 'Change user filter' }).count()).toBe(1)
  for (const width of [320, 390]) {
    await page.setViewportSize({ width, height: 844 })
    await expect(bar.getByRole('button', { name: 'Clear user filter' })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    const globalInset = await bar.locator('.filterchip').first().boundingBox()
    const localInset = await page.locator('.filterbar .filterchip').first().boundingBox()
    expect(globalInset!.x).toBe(localInset!.x)
  }
  await bar.getByRole('button', { name: 'Clear user filter' }).click()
  await expect.poll(() => requests.filter(u => u.pathname === '/api/logs').at(-1)?.searchParams.has('user')).toBe(false)
  for (const view of ['transactions', 'queries', 'caches', 'jobs', 'browser']) {
    await page.goto(`/performance/${view}?user=`)
    await expect(page.locator('.perf-subnav')).toBeVisible()
    await expect(page.locator('.filterbar')).toHaveCount(0)
    for (const width of [320, 1280]) {
      await page.setViewportSize({ width, height: 844 })
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      const nav = await page.locator('.perf-subnav').boundingBox()
      expect(nav!.height).toBeLessThanOrEqual(44)
      if (['queries', 'caches', 'jobs'].includes(view)) {
        await expect(page.locator('.perf-subnav input')).toBeVisible()
        await page.locator('.perf-subnav input').fill('test')
      }
      if (view === 'transactions') await expect(page.locator('.perf-subnav').getByRole('button', { name: /Release:/ })).toBeVisible()
      if (width === 1280 && ['transactions', 'browser'].includes(view)) await page.screenshot({ path: testInfo.outputPath(`performance-${view}.png`) })
    }
  }
  expect(errors).toEqual([])
})
