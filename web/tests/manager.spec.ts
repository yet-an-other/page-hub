import { expect, test } from '@playwright/test'

const assertion = 'playwright-only-assertion'

test('compiled binary serves the authenticated manager shell', async ({ page, request }) => {
  const unauthorized = await request.get('/', {
    headers: { 'X-Page-Hub-Assertion': '' },
  })
  expect(unauthorized.status()).toBe(401)

  const unauthenticatedInventory = await request.get('/_page-hub/api/v1/inventory', {
    headers: { 'X-Page-Hub-Assertion': '' },
  })
  expect(unauthenticatedInventory.status()).toBe(401)

  const response = await page.goto('/')
  expect(response?.ok()).toBeTruthy()
  await expect(page.getByRole('heading', { name: 'Page Hub' })).toBeVisible()
  await expect(page.getByText('S3-compatible storage')).toBeVisible()
})

test('manager shows the catalog-backed inventory', async ({ page }) => {
  await page.goto('/')
  const inventory = page.getByRole('region', { name: 'Publication inventory' })
  await expect(inventory.getByRole('heading', { name: 'Notes' })).toBeVisible()

  const publication = inventory.getByText('2026 Report')
  await expect(publication).toBeVisible()
  await expect(inventory.getByText('notes/2026-report')).toBeVisible()
  // The accepted size, not the observed size, is the row value.
  await expect(inventory.getByText('150 B')).toBeVisible()
  await expect(inventory.getByText(/Content changed 2026-01-02/)).toBeVisible()
  await expect(inventory.getByText('directory_index')).toBeVisible()
  await expect(inventory.getByText('in sync')).toBeVisible()

  const api = await page.request.get('/_page-hub/api/v1/inventory')
  expect(api.ok()).toBeTruthy()
  const payload = await api.json()
  expect(payload.projects).toHaveLength(1)
  expect(payload.projects[0]).toMatchObject({
    prefix: 'notes',
    publications: [{ path: 'notes/2026-report', size: 150, routingMode: 'directory_index' }],
  })
})

test('manager reports exact storage usage from the bucket observation', async ({ page }) => {
  await page.goto('/')
  const usage = page.getByRole('region', { name: 'Storage usage' })
  await expect(usage).toBeVisible()
  await expect(usage.getByText('Quota')).toBeVisible()
  await expect(usage.getByText('1.0 MiB')).toBeVisible()
  await expect(usage.getByText('Bucket usage')).toBeVisible()
  await expect(usage.getByText('Accepted Publications')).toBeVisible()
  await expect(usage.getByText('Unclaimed storage')).toBeVisible()

  // The seeded observation is older than five minutes, so it is stale.
  await expect(usage.getByText(/This observation is stale/)).toBeVisible()

  const api = await page.request.get('/_page-hub/api/v1/inventory')
  const payload = await api.json()
  expect(payload.observation).toMatchObject({
    stale: true,
    mutationLock: 'none',
    usage: { quotaBytes: 1048576, totalBytes: 150, acceptedBytes: 150, unclaimedBytes: 0 },
  })
})

test('stale inventory stays readable through a failed refresh', async ({ page }) => {
  await page.goto('/')
  const inventory = page.getByRole('region', { name: 'Publication inventory' })
  const usage = page.getByRole('region', { name: 'Storage usage' })
  await expect(inventory.getByRole('heading', { name: 'Notes' })).toBeVisible()

  // Storage is unconfigured here, so the refresh fails while every cataloged
  // value stays on screen.
  await page.getByRole('button', { name: 'Refresh' }).click()
  await expect(page.getByRole('alert')).toContainText('The last refresh failed')
  await expect(page.getByRole('alert')).toContainText('storage misconfigured')

  // Nothing was cleared or replaced by the failed refresh.
  await expect(inventory.getByText('2026 Report')).toBeVisible()
  await expect(inventory.getByText('150 B')).toBeVisible()
  await expect(usage.getByText('1.0 MiB')).toBeVisible()
  await expect(usage.getByText(/This observation is stale/)).toBeVisible()

  const api = await page.request.post('/_page-hub/api/v1/refresh')
  expect(api.ok()).toBeTruthy()
  expect(await api.json()).toMatchObject({ running: false, lastOutcome: 'misconfigured' })
})

test('manager assets stay below the reserved prefix', async ({ page }) => {
  const response = await page.goto('/')
  expect(response?.ok()).toBeTruthy()
  const assetURLs = await page.locator('script[src], link[rel="stylesheet"]').evaluateAll((elements) =>
    elements.map((element) => (element as HTMLScriptElement | HTMLLinkElement).src || (element as HTMLLinkElement).href),
  )
  expect(assetURLs.length).toBeGreaterThan(0)
  for (const url of assetURLs) expect(new URL(url).pathname).toMatch(/^\/_page-hub\//)

  const status = await page.request.get('/_page-hub/api/v1/status', {
    headers: { 'X-Page-Hub-Assertion': assertion },
  })
  expect(status.ok()).toBeTruthy()
  expect(await status.json()).toMatchObject({ storage: { status: 'misconfigured' } })
  // exact match: the badge reads "misconfigured" while refresh warnings only
  // contain the word.
  await expect(page.getByText('misconfigured', { exact: true })).toBeVisible({ timeout: 10_000 })
})
