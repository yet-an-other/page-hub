import { expect, test } from '@playwright/test'

const assertion = 'playwright-only-assertion'

test('compiled binary serves the authenticated manager shell', async ({ page, request }) => {
  const unauthorized = await request.get('/', {
    headers: { 'X-Page-Hub-Assertion': '' },
  })
  expect(unauthorized.status()).toBe(401)

  const response = await page.goto('/')
  expect(response?.ok()).toBeTruthy()
  await expect(page.getByRole('heading', { name: 'Page Hub' })).toBeVisible()
  await expect(page.getByText('S3-compatible storage')).toBeVisible()
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
  await expect(page.getByText('misconfigured')).toBeVisible({ timeout: 10_000 })
})
