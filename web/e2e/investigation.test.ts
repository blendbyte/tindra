import { test, expect } from '@playwright/test'

const projectId='11111111-1111-4111-8111-111111111111'
test('keeps investigation context through navigation, reload, and profile samples', async ({ page }) => {
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
    else if(url.pathname==='/api/projects/metadata') data=[{id:projectId,name:'API',slug:'api'}]
    else if(url.pathname==='/api/environments') data=['production','preview','eu-west']
    else if(url.pathname==='/api/logs') data={logs:[],has_more:false}
    else if(url.pathname==='/api/issues') data={issues:[],total:0,has_more:false}
    else if(url.pathname==='/api/transactions') data={transactions:[]}
    else if(url.pathname.includes('timeseries')) data={buckets:[],bucket_size:'hour'}
    else if(url.pathname==='/api/releases/metadata') data={releases:[]}
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
  await bar.getByRole('button', { name: /Time range:/ }).click()
  await bar.getByRole('button', { name: '1h', exact: true }).click()
  await expect(page).toHaveURL(/range=1h/)
  await page.locator('.nav__projects-trigger').click()
  await page.locator('.popover').getByRole('button', { name: 'All projects', exact: true }).click()
  await expect(page).toHaveURL(/project_id=all/)
  await expect.poll(() => requests.filter(u => u.pathname === '/api/logs').at(-1)?.searchParams.has('project_id')).toBe(false)
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(bar.getByRole('button', { name: /Time range:/ })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto(`/transactions/profile?name=checkout&project_id=${projectId}&environment=eu-west&range=7d`)
  await expect.poll(()=>requests.filter(u=>u.pathname==='/api/transactions'&&u.searchParams.has('name')).length).toBeGreaterThan(0)
  const samples=requests.filter(u=>u.pathname==='/api/transactions'&&u.searchParams.has('name')).at(-1)!
  const chart=requests.filter(u=>u.pathname==='/api/transactions/timeseries'&&u.searchParams.has('name')).at(-1)!
  expect(samples.searchParams.get('from')).toBe(chart.searchParams.get('from'))
  expect(samples.searchParams.get('to')).toBe(chart.searchParams.get('to'))
  await bar.getByRole('button',{name:'Pause',exact:true}).click()
  await expect(bar).toContainText('Auto-refresh paused')
  expect(errors).toEqual([])
})
